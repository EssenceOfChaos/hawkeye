package statuspage_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

type env struct {
	pool  *pgxpool.Pool
	svc   *statuspage.Service
	org   uuid.UUID
	pages map[string]uuid.UUID // slug -> id, for publish
}

func newEnv(t *testing.T, plan string) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 5)
	e := &env{pool: pool, svc: statuspage.NewService(pool, plans.NewService("http://app.test")), pages: map[string]uuid.UUID{}}
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code=$2`, e.org, plan)
	return e
}

// otherOrg is a second organization in the same database, for isolation tests (each env otherwise has a database of its own).
func (e *env) otherOrg(t *testing.T) *env {
	t.Helper()
	o := &env{pool: e.pool, svc: e.svc, pages: map[string]uuid.UUID{}}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('other') RETURNING id`).Scan(&o.org); err != nil {
		t.Fatal(err)
	}
	return o
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

// publish creates the page with this slug, or saves it again, published.
func (e *env) publish(t *testing.T, slug string, ms ...statuspage.MonitorConfig) {
	t.Helper()
	cfg := statuspage.Config{Slug: slug, Title: "Acme status", Description: "Live status", Published: true, Monitors: ms}
	var c *statuspage.Config
	var err error
	if id, ok := e.pages[slug]; ok {
		c, err = e.svc.Update(t.Context(), e.org, id, cfg)
	} else {
		c, err = e.svc.Create(t.Context(), e.org, cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	e.pages[slug] = c.ID
}

func TestCreateGetUpdateDeleteAndValidation(t *testing.T) {
	e := newEnv(t, "free")
	if list, err := e.svc.List(t.Context(), e.org); err != nil || len(list) != 0 {
		t.Fatalf("a new organization has no pages: %+v %v", list, err)
	}
	a, b := e.monitor(t, "Website", "up"), e.monitor(t, "API", "up")
	c, err := e.svc.Create(t.Context(), e.org, statuspage.Config{Slug: "Acme", Title: "Acme", Published: true,
		Monitors: []statuspage.MonitorConfig{{MonitorID: b, DisplayName: " Public API "}, {MonitorID: a}}})
	if err != nil || c.ID == uuid.Nil || c.Slug != "acme" || len(c.Monitors) != 2 || c.Monitors[0].MonitorID != b || c.Monitors[0].DisplayName != "Public API" || c.Monitors[1].DisplayName != "" {
		t.Fatalf("%+v %v", c, err)
	}
	// Saving again replaces the page and the monitor list.
	c, err = e.svc.Update(t.Context(), e.org, c.ID, statuspage.Config{Slug: "acme2", Title: "Acme 2", Monitors: []statuspage.MonitorConfig{{MonitorID: a}}})
	if err != nil || c.Slug != "acme2" || c.Published || len(c.Monitors) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	if got, err := e.svc.Get(t.Context(), e.org, c.ID); err != nil || got.Slug != "acme2" {
		t.Fatalf("%+v %v", got, err)
	}

	var ve *validation.Error
	other := e.otherOrg(t)
	foreign := other.monitor(t, "Not yours", "up")
	if _, err := e.svc.Update(t.Context(), e.org, c.ID, statuspage.Config{Slug: "acme2", Title: "x", Monitors: []statuspage.MonitorConfig{{MonitorID: foreign}}}); !errors.As(err, &ve) || ve.Fields["monitors"] == "" {
		t.Fatalf("another organization's monitor: %v", err)
	}
	if _, err := e.svc.Update(t.Context(), e.org, c.ID, statuspage.Config{Slug: "acme2", Title: "x", Monitors: []statuspage.MonitorConfig{{MonitorID: uuid.New()}}}); !errors.As(err, &ve) {
		t.Fatalf("unknown monitor: %v", err)
	}
	// Another organization cannot read, change or delete this page.
	if _, err := other.svc.Get(t.Context(), other.org, c.ID); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := other.svc.Update(t.Context(), other.org, c.ID, statuspage.Config{Slug: "stolen", Title: "x"}); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if err := other.svc.Delete(t.Context(), other.org, c.ID); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}

	if err := e.svc.Delete(t.Context(), e.org, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(t.Context(), e.org, c.ID); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestPagesPerPlanAndDowngrades(t *testing.T) {
	e := newEnv(t, "pro") // Pro includes 3
	m := e.monitor(t, "Website", "up")
	var ids []uuid.UUID
	for _, slug := range []string{"client-a", "client-b", "client-c"} {
		c, err := e.svc.Create(t.Context(), e.org, statuspage.Config{Slug: slug, Title: slug, Published: true, Monitors: []statuspage.MonitorConfig{{MonitorID: m}}})
		if err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		ids = append(ids, c.ID)
	}
	var le *plans.LimitError
	if _, err := e.svc.Create(t.Context(), e.org, statuspage.Config{Slug: "client-d", Title: "D"}); !errors.As(err, &le) || le.Kind != plans.LimitStatusPages || le.Limit != 3 {
		t.Fatalf("a fourth page on Pro: %v", err)
	}
	if list, _ := e.svc.List(t.Context(), e.org); len(list) != 3 || list[0].Slug != "client-a" {
		t.Fatalf("%+v", list)
	}

	// After a downgrade to Free (one page) every page keeps working and can still be edited; only adding another is refused.
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled' WHERE organization_id = $1`, e.org)
	for _, slug := range []string{"client-a", "client-b", "client-c"} {
		if _, err := e.svc.Public(t.Context(), slug); err != nil {
			t.Fatalf("%s must stay public after a downgrade: %v", slug, err)
		}
	}
	if _, err := e.svc.Update(t.Context(), e.org, ids[2], statuspage.Config{Slug: "client-c", Title: "Client C", Published: true, Monitors: []statuspage.MonitorConfig{{MonitorID: m}}}); err != nil {
		t.Fatalf("editing an existing page after a downgrade: %v", err)
	}
	if _, err := e.svc.Create(t.Context(), e.org, statuspage.Config{Slug: "client-d", Title: "D"}); !errors.As(err, &le) || le.Limit != 1 || le.Used != 3 ||
		!strings.Contains(le.Message(), "delete 3") {
		t.Fatalf("%v / %q", err, le.Message())
	}
}

func TestSlugsAreUnique(t *testing.T) {
	e1 := newEnv(t, "pro")
	e2 := e1.otherOrg(t)
	one, err := e1.svc.Create(t.Context(), e1.org, statuspage.Config{Slug: "taken", Title: "One"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e2.svc.Create(t.Context(), e2.org, statuspage.Config{Slug: "TAKEN", Title: "Two"}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Fatalf("another organization: %v", err)
	}
	if _, err := e1.svc.Create(t.Context(), e1.org, statuspage.Config{Slug: "taken", Title: "Again"}); !errors.Is(err, statuspage.ErrSlugTaken) {
		t.Fatalf("the same organization's other page: %v", err)
	}
	if err := e1.svc.Delete(t.Context(), e1.org, one.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.svc.Create(t.Context(), e2.org, statuspage.Config{Slug: "taken", Title: "Two"}); err != nil {
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
	e.publish(t, "acme", statuspage.MonitorConfig{MonitorID: m}) // saving clears the cache
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
	draft, err := e.svc.Create(t.Context(), e.org, statuspage.Config{Slug: "draft", Title: "Draft", Monitors: []statuspage.MonitorConfig{{MonitorID: m}}})
	if err != nil {
		t.Fatal(err)
	}
	e.pages["draft"] = draft.ID
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
