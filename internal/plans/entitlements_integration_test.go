package plans_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

func newOrg(t *testing.T, pool *pgxpool.Pool, planCode, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status)
		SELECT $1, id, $3 FROM plans WHERE code = $2`, id, planCode, status); err != nil {
		t.Fatal(err)
	}
	return id
}

func addMonitors(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, n int) {
	t.Helper()
	for range n {
		if _, err := pool.Exec(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
			VALUES ($1, 'm', 'https://example.com', 300)`, org); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEntitlementsByPlanAndStatus(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := plans.NewService("http://app.test")
	ctx := t.Context()

	cases := []struct {
		plan, status string
		monitors     int
		interval     int
		retention    int
		export       bool
	}{
		{"free", "active", 5, 300, 7, false},
		{"pro", "active", 50, 60, 90, true},
		{"pro", "trialing", 50, 60, 90, true},
		{"pro", "past_due", 50, 60, 90, true},
		{"agency", "active", 200, 60, 365, true},
		{"pro", "canceled", 5, 300, 7, false},      // falls back to free
		{"agency", "incomplete", 5, 300, 7, false}, // checkout without payment grants nothing
	}
	for _, c := range cases {
		org := newOrg(t, pool, c.plan, c.status)
		if n, _ := svc.GetMonitorLimit(ctx, pool, org); n != c.monitors {
			t.Errorf("%s/%s monitors=%d want %d", c.plan, c.status, n, c.monitors)
		}
		if n, _ := svc.GetMinimumCheckInterval(ctx, pool, org); n != c.interval {
			t.Errorf("%s/%s interval=%d want %d", c.plan, c.status, n, c.interval)
		}
		if n, _ := svc.GetHistoryRetention(ctx, pool, org); n != c.retention {
			t.Errorf("%s/%s retention=%d want %d", c.plan, c.status, n, c.retention)
		}
		if ok, _ := svc.CanExportReports(ctx, pool, org); ok != c.export {
			t.Errorf("%s/%s export=%v want %v", c.plan, c.status, ok, c.export)
		}
	}
}

func TestOrganizationWithoutSubscriptionIsFree(t *testing.T) {
	pool := testutil.NewPool(t)
	var org uuid.UUID
	_ = pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org)
	n, err := plans.NewService("").GetMonitorLimit(t.Context(), pool, org)
	if err != nil || n != 5 {
		t.Fatalf("got %d %v", n, err)
	}
}

func TestCanCreateMonitorLimitAndUpgradeHint(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := plans.NewService("http://app.test")
	org := newOrg(t, pool, "free", "active")

	addMonitors(t, pool, org, 4)
	if err := svc.CanCreateMonitor(t.Context(), pool, org); err != nil {
		t.Fatalf("4/5 should be allowed: %v", err)
	}
	addMonitors(t, pool, org, 1)
	err := svc.CanCreateMonitor(t.Context(), pool, org)
	var le *plans.LimitError
	if !errors.As(err, &le) || le.Limit != 5 || le.Used != 5 || le.Next == nil || le.Next.Code != "pro" ||
		le.UpgradeURL != "http://app.test/billing" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestDeletedMonitorsDoNotCount(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := plans.NewService("")
	org := newOrg(t, pool, "free", "active")
	addMonitors(t, pool, org, 5)
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = (SELECT id FROM monitors LIMIT 1)`)
	if err := svc.CanCreateMonitor(t.Context(), pool, org); err != nil {
		t.Fatalf("soft-deleted monitor should free a slot: %v", err)
	}
}

func TestDowngradeKeepsMonitorsButBlocksCreation(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := plans.NewService("http://app.test")
	org := newOrg(t, pool, "pro", "active")
	addMonitors(t, pool, org, 8)

	_, _ = pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled' WHERE organization_id = $1`, org)

	var n int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL`, org).Scan(&n)
	if n != 8 {
		t.Fatalf("downgrade must not delete monitors, have %d", n)
	}
	err := svc.CanCreateMonitor(t.Context(), pool, org)
	var le *plans.LimitError
	if !errors.As(err, &le) || le.Used != 8 || le.Limit != 5 {
		t.Fatalf("expected over-limit error, got %#v", err)
	}
	u, err := svc.Usage(t.Context(), pool, org)
	if err != nil || !u.OverLimit || u.MonitorsUsed != 8 || u.MonitorLimit != 5 {
		t.Fatalf("usage: %+v %v", u, err)
	}
}

func TestCheckIntervalAndTeamLimits(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := plans.NewService("")
	free := newOrg(t, pool, "free", "active")
	var le *plans.LimitError
	if err := svc.CheckInterval(t.Context(), pool, free, 60); !errors.As(err, &le) || le.Kind != plans.LimitCheckInterval || le.Limit != 300 {
		t.Fatalf("free should reject 60s: %v", err)
	}
	if err := svc.CheckInterval(t.Context(), pool, free, 300); err != nil {
		t.Fatal(err)
	}
	var uid uuid.UUID
	_ = pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ('a@b.co','x') RETURNING id`).Scan(&uid)
	_, _ = pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1,$2,'owner')`, free, uid)
	if err := svc.CanAddTeamMember(t.Context(), pool, free); !errors.As(err, &le) || le.Kind != plans.LimitTeamMembers {
		t.Fatalf("free allows 1 member: %v", err)
	}
	agency := newOrg(t, pool, "agency", "active")
	if err := svc.CanAddTeamMember(t.Context(), pool, agency); err != nil {
		t.Fatal(err)
	}
}
