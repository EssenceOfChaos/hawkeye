package badges_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/badges"
	"hawkeye/internal/testutil"
)

func TestBadgesFollowTheMonitorsStateAndPlan(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := t.Context()
	var org uuid.UUID
	pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org)
	pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = 'pro'`, org)

	monitor := func(status string, enabled bool) uuid.UUID {
		var id uuid.UUID
		pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, current_status, enabled)
			VALUES ($1, 'Site', 'https://example.com', 60, $2, $3) RETURNING id`, org, status, enabled).Scan(&id)
		pool.Exec(ctx, `INSERT INTO check_results (monitor_id, checked_at, finished_at, success)
			SELECT $1, now() - g * interval '1 hour', now() - g * interval '1 hour', true FROM generate_series(1, 20) g`, id)
		return id
	}
	svc := badges.NewService(pool)
	draw := func(id uuid.UUID) string {
		token, err := svc.Enable(ctx, org, id)
		if err != nil {
			t.Fatal(err)
		}
		svg, err := svc.Badge(ctx, token, badges.Large)
		if err != nil {
			t.Fatal(err)
		}
		return string(svg)
	}

	if s := draw(monitor("up", true)); !strings.Contains(s, "Last 30 days") || !strings.Contains(s, "<tspan>100%</tspan>") {
		t.Fatalf("Pro covers 30 days:\n%s", s)
	}
	if s := draw(monitor("down", true)); !strings.Contains(s, "Service currently down") {
		t.Fatalf("%s", s)
	}
	if s := draw(monitor("up", false)); !strings.Contains(s, "Status unknown · Last 30 days") {
		t.Fatalf("a paused monitor's state is unknown, though its uptime is still shown:\n%s", s)
	}
	inMaint := monitor("up", true)
	pool.Exec(ctx, `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'w', now() - interval '1 minute', 60, true)`, org)
	if s := draw(inMaint); !strings.Contains(s, "Status unknown") {
		t.Fatalf("during maintenance the state is unknown:\n%s", s)
	}

	// Another organization cannot turn on (or off) this organization's badge.
	var other uuid.UUID
	pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('other') RETURNING id`).Scan(&other)
	if _, err := svc.Enable(ctx, other, inMaint); err != badges.ErrNotFound {
		t.Fatalf("%v", err)
	}
	if err := svc.Disable(ctx, other, inMaint); err != badges.ErrNotFound {
		t.Fatalf("%v", err)
	}
}
