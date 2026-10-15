package statuspage_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/statuspage"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

type env struct {
	pool *pgxpool.Pool
	svc  *statuspage.Service
	org  uuid.UUID
}

func newEnv(t *testing.T, plan string) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 5)
	e := &env{pool: pool, svc: statuspage.NewService(pool)}
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code=$2`, e.org, plan)
	return e
}

func (e *env) monitor(t *testing.T, name, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, current_status)
		VALUES ($1, $2, 'https://internal-admin.example.com/secret-path', 60, $3) RETURNING id`, e.org, name, status).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// checks inserts n checks (every minute from start), ok of them failing as given.
func (e *env) checks(t *testing.T, monitor uuid.UUID, start time.Time, n int, success, maintenance bool) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, in_maintenance, error_category, error_summary)
		SELECT $1, $2::timestamptz + g * interval '1 minute', $2::timestamptz + g * interval '1 minute', $3, $4, CASE WHEN $3 THEN NULL ELSE 'http_status' END,
			CASE WHEN $3 THEN NULL ELSE 'Received HTTP 503 from db-primary.internal' END
		FROM generate_series(0, $5 - 1) g`, monitor, start, success, maintenance, n); err != nil {
		t.Fatal(err)
	}
}

func (e *env) publish(t *testing.T, slug string, ms ...statuspage.MonitorConfig) {
	t.Helper()
	if _, err := e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: slug, Title: "Acme status", Description: "Live status", Published: true, Monitors: ms}); err != nil {
		t.Fatal(err)
	}
}

func TestPutGetDeleteAndValidation(t *testing.T) {
	e := newEnv(t, "free")
	if c, err := e.svc.Get(t.Context(), e.org); err != nil || c != nil {
		t.Fatalf("a new organization has no page: %+v %v", c, err)
	}
	a, b := e.monitor(t, "Website", "up"), e.monitor(t, "API", "up")
	c, err := e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: "Acme", Title: "Acme", Published: true,
		Monitors: []statuspage.MonitorConfig{{MonitorID: b, DisplayName: " Public API "}, {MonitorID: a}}})
	if err != nil || c.Slug != "acme" || len(c.Monitors) != 2 || c.Monitors[0].MonitorID != b || c.Monitors[0].DisplayName != "Public API" || c.Monitors[1].DisplayName != "" {
		t.Fatalf("%+v %v", c, err)
	}
	// Saving again replaces the page and the monitor list.
	c, err = e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: "acme2", Title: "Acme 2", Monitors: []statuspage.MonitorConfig{{MonitorID: a}}})
	if err != nil || c.Slug != "acme2" || c.Published || len(c.Monitors) != 1 {
		t.Fatalf("%+v %v", c, err)
	}

	var ve *validation.Error
	other := newEnv(t, "free")
	foreign := other.monitor(t, "Not yours", "up")
	if _, err := e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: "acme2", Title: "x", Monitors: []statuspage.MonitorConfig{{MonitorID: foreign}}}); !errors.As(err, &ve) || ve.Fields["monitors"] == "" {
		t.Fatalf("another organization's monitor: %v", err)
	}
	if _, err := e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: "acme2", Title: "x", Monitors: []statuspage.MonitorConfig{{MonitorID: uuid.New()}}}); !errors.As(err, &ve) {
		t.Fatalf("unknown monitor: %v", err)
	}

	if err := e.svc.Delete(t.Context(), e.org); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(t.Context(), e.org); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestSlugsAreUnique(t *testing.T) {
	e1 := newEnv(t, "free")
	var org2 uuid.UUID
	e1.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('two') RETURNING id`).Scan(&org2)
	if _, err := e1.svc.Put(t.Context(), e1.org, statuspage.Config{Slug: "taken", Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e1.svc.Put(t.Context(), org2, statuspage.Config{Slug: "TAKEN", Title: "Two"}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Fatalf("%v", err)
	}
	if err := e1.svc.Delete(t.Context(), e1.org); err != nil {
		t.Fatal(err)
	}
	if _, err := e1.svc.Put(t.Context(), org2, statuspage.Config{Slug: "taken", Title: "Two"}); err != nil {
		t.Fatalf("a deleted page frees its address: %v", err)
	}
}

func TestPublicPageShowsOnlyWhatTheOwnerChose(t *testing.T) {
	e := newEnv(t, "pro")
	up, down, hidden := e.monitor(t, "Website", "up"), e.monitor(t, "Checkout", "down"), e.monitor(t, "Internal admin", "up")
	now := time.Now().UTC()
	e.checks(t, up, now.Add(-90*time.Minute), 60, true, false)
	e.checks(t, down, now.Add(-90*time.Minute), 60, false, false)
	e.checks(t, hidden, now.Add(-90*time.Minute), 60, true, false)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO incidents (monitor_id, started_at, resolved_at, status, cause_category, summary) VALUES
		($1, now() - interval '1 hour', NULL, 'open', 'http_status', 'Received HTTP 503 from db-primary.internal'),
		($2, now() - interval '20 days', now() - interval '20 days' + interval '5 minutes', 'resolved', 'timeout', 'old'),
		($3, now() - interval '1 hour', NULL, 'open', 'timeout', 'hidden monitor incident')`, down, up, hidden); err != nil {
		t.Fatal(err)
	}
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: up, DisplayName: "Marketing site"}, statuspage.MonitorConfig{MonitorID: down})

	p, err := e.svc.Public(t.Context(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Acme status" || p.Overall != statuspage.Partial || p.HistoryDays != 90 || len(p.Monitors) != 2 {
		t.Fatalf("%+v", p)
	}
	if p.Monitors[0].Name != "Marketing site" || p.Monitors[0].State != "up" || p.Monitors[1].Name != "Checkout" || p.Monitors[1].State != "down" {
		t.Fatalf("%+v", p.Monitors)
	}
	if len(p.Monitors[0].Days) != 90 || p.Monitors[0].UptimePercent == nil || *p.Monitors[0].UptimePercent != 100 || *p.Monitors[1].UptimePercent != 0 {
		t.Fatalf("days=%d uptime=%v/%v", len(p.Monitors[0].Days), p.Monitors[0].UptimePercent, p.Monitors[1].UptimePercent)
	}
	if last := p.Monitors[1].Days[89]; last.Status != "down" && last.Status != "none" { // none if the checks straddle midnight
		t.Fatalf("%+v", last)
	}
	if len(p.Incidents) != 1 || p.Incidents[0].Monitor != "Checkout" || p.Incidents[0].ResolvedAt != nil {
		t.Fatalf("only recent incidents of listed monitors: %+v", p.Incidents)
	}

	raw, _ := json.Marshal(p)
	for _, secret := range []string{"internal-admin", "secret-path", "db-primary", "Internal admin", "hidden monitor", "http_status", "https://"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("public page leaks %q: %s", secret, raw)
		}
	}
}

func TestMaintenanceChecksAreLeftOutAndShownAsMaintenance(t *testing.T) {
	e := newEnv(t, "pro")
	m := e.monitor(t, "Website", "up")
	e.checks(t, m, time.Now().UTC().Add(-3*time.Hour), 30, true, false)
	e.checks(t, m, time.Now().UTC().Add(-2*time.Hour), 30, false, true) // failures during maintenance
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: m})
	p, _ := e.svc.Public(t.Context(), "acme")
	if p.Monitors[0].UptimePercent == nil || *p.Monitors[0].UptimePercent != 100 {
		t.Fatalf("maintenance checks must not count: %v", p.Monitors[0].UptimePercent)
	}

	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'w', now() - interval '1 minute', 60, true)`, e.org)
	e.svc.Delete(t.Context(), e.org) // clears the cache
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: m})
	if p, _ = e.svc.Public(t.Context(), "acme"); p.Monitors[0].State != "maintenance" || p.Overall != statuspage.Maintenance {
		t.Fatalf("%+v", p)
	}
}

func TestUnpublishedAndUnknownPagesAreNotFoundAndPlanLimitsHistory(t *testing.T) {
	e := newEnv(t, "free")
	m := e.monitor(t, "Website", "up")
	if _, err := e.svc.Public(t.Context(), "nope"); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Public(t.Context(), "../etc/passwd"); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.Put(t.Context(), e.org, statuspage.Config{Slug: "draft", Title: "Draft", Monitors: []statuspage.MonitorConfig{{MonitorID: m}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Public(t.Context(), "draft"); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("a draft must look like a page that does not exist: %v", err)
	}
	e.publish(t, "draft", statuspage.MonitorConfig{MonitorID: m})
	p, err := e.svc.Public(t.Context(), "draft")
	if err != nil || p.HistoryDays != 7 || len(p.Monitors[0].Days) != 7 {
		t.Fatalf("free plan keeps 7 days: %+v %v", p.HistoryDays, err)
	}
}

func TestPublicResultsAreCachedBrieflyAndEditsTakeEffectAtOnce(t *testing.T) {
	e := newEnv(t, "pro")
	m := e.monitor(t, "Website", "up")
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: m})
	first, _ := e.svc.Public(t.Context(), "acme")
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status = 'down' WHERE id = $1`, m)
	if again, _ := e.svc.Public(t.Context(), "acme"); again.Monitors[0].State != "up" || !again.GeneratedAt.Equal(first.GeneratedAt) {
		t.Fatal("within the cache window the same answer is served")
	}
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: m, DisplayName: "Renamed"})
	if p, _ := e.svc.Public(t.Context(), "acme"); p.Monitors[0].Name != "Renamed" || p.Monitors[0].State != "down" {
		t.Fatalf("saving the page must drop the cache: %+v", p.Monitors[0])
	}
}
