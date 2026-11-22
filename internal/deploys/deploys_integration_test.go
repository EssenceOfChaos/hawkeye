package deploys

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/plans"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

func setup(t *testing.T, plan string) (*Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool := testutil.NewPool(t)
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, org, plan); err != nil {
		t.Fatal(err)
	}
	return NewService(pool, plans.NewService("http://app.test"), checks.NewChecker(safehttp.Config{}), testutil.SecretBox()), pool, org
}

func monitor(t *testing.T, pool *pgxpool.Pool, org uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'm', 'https://example.com', 60) RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func quiet(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var q bool
	if err := pool.QueryRow(t.Context(), `SELECT in_maintenance(organization_id, id, now()) FROM monitors WHERE id = $1`, id).Scan(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

// A pipeline that never calls finish doesn't leave alerts off: the deploy ends by itself after its minutes.
func TestADeployEndsByItself(t *testing.T) {
	s, pool, org := setup(t, "pro")
	id := monitor(t, pool, org)
	s.now = func() time.Time { return time.Now().Add(-3 * time.Minute) } // started 3 minutes ago, for 2
	d, _, err := s.Start(t.Context(), org, nil, StartInput{Minutes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if quiet(t, pool, id) {
		t.Fatal("still quiet after its minutes")
	}
	s.now = time.Now
	list, err := s.List(t.Context(), org, 5)
	if err != nil || len(list) != 1 || list[0].ID != d.ID || list[0].Active || list[0].FinishedAt != nil {
		t.Fatalf("%v %+v", err, list)
	}
	// A new deploy for the same monitors opens a new window, rather than extending the one that ended.
	d2, extended, err := s.Start(t.Context(), org, nil, StartInput{})
	if err != nil || extended || d2.ID == d.ID || !quiet(t, pool, id) {
		t.Fatalf("%v extended=%v", err, extended)
	}
}

func TestDeployLimits(t *testing.T) {
	s, pool, org := setup(t, "agency")
	for range 11 {
		monitor(t, pool, org)
	}
	// Up to PerHour starts an hour (extending counts as a start only once: it opens no window).
	for i := range PerHour {
		note := "n"
		if _, err := pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, kind, name, starts_at, duration_minutes, note)
			VALUES ($1, 'deploy', 'Deploy', now() - interval '50 minutes', 1, $2)`, org, note); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if _, _, err := s.Start(t.Context(), org, nil, StartInput{}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("over the hourly limit: %v", err)
	}
	// A check covers at most MaxChecked monitors.
	var ve *validation.Error
	if _, _, err := s.CheckNow(t.Context(), org, Target{}); !errors.As(err, &ve) {
		t.Fatalf("11 monitors: %v", err)
	}
}
