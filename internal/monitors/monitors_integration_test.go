package monitors_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

func setup(t *testing.T, plan, status string) (*monitors.Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	return setupPool(t, plan, status, 5)
}

func setupPool(t *testing.T, plan, status string, conns int32) (*monitors.Service, *pgxpool.Pool, uuid.UUID) {
	t.Helper()
	pool := testutil.NewPoolSize(t, conns)
	var org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status)
		SELECT $1, id, $3 FROM plans WHERE code = $2`, org, plan, status); err != nil {
		t.Fatal(err)
	}
	return monitors.NewService(pool, plans.NewService("http://app.test")), pool, org
}

func newIn(name string) monitors.CreateInput {
	return monitors.CreateInput{Name: name, URL: "https://example.com"}
}

func TestCreateDefaultsFromPlan(t *testing.T) {
	svc, _, org := setup(t, "free", "active")
	m, err := svc.Create(t.Context(), org, newIn("Site"))
	if err != nil {
		t.Fatal(err)
	}
	if m.CheckIntervalSeconds != 300 || m.Method != "GET" || m.TimeoutSeconds != 10 || !m.Enabled ||
		m.CurrentStatus != "unknown" || m.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected defaults: %+v", m)
	}

	psvc, _, porg := setup(t, "pro", "active")
	pm, _ := psvc.Create(t.Context(), porg, newIn("Site"))
	if pm.CheckIntervalSeconds != 60 {
		t.Fatalf("pro default interval = %d", pm.CheckIntervalSeconds)
	}
}

func TestFreeLimitOfFive(t *testing.T) {
	svc, _, org := setup(t, "free", "active")
	for i := range 5 {
		if _, err := svc.Create(t.Context(), org, newIn("m")); err != nil {
			t.Fatalf("monitor %d: %v", i+1, err)
		}
	}
	_, err := svc.Create(t.Context(), org, newIn("sixth"))
	var le *plans.LimitError
	if !errors.As(err, &le) || le.Kind != plans.LimitMonitors || le.Limit != 5 {
		t.Fatalf("sixth monitor: %v", err)
	}
}

func TestConcurrentCreatesNeverExceedLimit(t *testing.T) {
	svc, pool, org := setupPool(t, "free", "active", 30)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, limited := 0, 0
	start := make(chan struct{})
	for range 25 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all requests together to maximise contention
			_, err := svc.Create(t.Context(), org, newIn("m"))
			mu.Lock()
			defer mu.Unlock()
			var le *plans.LimitError
			switch {
			case err == nil:
				ok++
			case errors.As(err, &le):
				limited++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	var n int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM monitors WHERE organization_id = $1`, org).Scan(&n)
	if ok != 5 || limited != 20 || n != 5 {
		t.Fatalf("ok=%d limited=%d rows=%d, want 5/20/5", ok, limited, n)
	}
}

func TestIntervalEnforcedByPlan(t *testing.T) {
	svc, _, org := setup(t, "free", "active")
	fast := 60
	in := newIn("m")
	in.CheckIntervalSeconds = &fast
	var le *plans.LimitError
	if _, err := svc.Create(t.Context(), org, in); !errors.As(err, &le) || le.Kind != plans.LimitCheckInterval {
		t.Fatalf("free must reject a 60s interval: %v", err)
	}
	m, _ := svc.Create(t.Context(), org, newIn("m"))
	if _, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{CheckIntervalSeconds: &fast}); !errors.As(err, &le) {
		t.Fatalf("update must enforce interval too: %v", err)
	}
}

func TestDowngradeClampsIntervalAndAllowsOtherEdits(t *testing.T) {
	svc, pool, org := setup(t, "pro", "active")
	m, _ := svc.Create(t.Context(), org, newIn("m"))
	if m.EffectiveCheckIntervalSeconds != 60 {
		t.Fatalf("pro effective = %d", m.EffectiveCheckIntervalSeconds)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled'`)

	got, _ := svc.Get(t.Context(), org, m.ID)
	if got.CheckIntervalSeconds != 60 || got.EffectiveCheckIntervalSeconds != 300 {
		t.Fatalf("stored=%d effective=%d, want 60/300", got.CheckIntervalSeconds, got.EffectiveCheckIntervalSeconds)
	}
	name := "renamed"
	if _, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: &name}); err != nil {
		t.Fatalf("unrelated edit after downgrade must work: %v", err)
	}
}

func TestValidation(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	badStatus, badTimeout, badInterval := 700, 0, 5
	method := "POST"
	_, err := svc.Create(t.Context(), org, monitors.CreateInput{
		Name: " ", URL: "http://127.0.0.1", Method: &method, ExpectedStatusCode: &badStatus,
		TimeoutSeconds: &badTimeout, CheckIntervalSeconds: &badInterval,
	})
	var ve *validation.Error
	if !errors.As(err, &ve) {
		t.Fatalf("expected validation error, got %v", err)
	}
	for _, f := range []string{"name", "url", "method", "expected_status_code", "timeout_seconds", "check_interval_seconds"} {
		if ve.Fields[f] == "" {
			t.Errorf("missing field error for %s: %v", f, ve.Fields)
		}
	}
}

func TestUpdatePatchSemanticsAndStateReset(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	code := 204
	in := newIn("m")
	in.ExpectedStatusCode = &code
	m, _ := svc.Create(t.Context(), org, in)
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET current_status='down', consecutive_failures=3 WHERE id=$1`, m.ID)

	name := "new name"
	u, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: &name})
	if err != nil || u.ExpectedStatusCode == nil || *u.ExpectedStatusCode != 204 || u.CurrentStatus != "down" {
		t.Fatalf("absent fields must be untouched: %+v %v", u, err)
	}
	u, _ = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{ExpectedStatusCode: monitors.Nullable[int]{Set: true}})
	if u.ExpectedStatusCode != nil {
		t.Fatalf("null must clear: %+v", u)
	}
	newURL := "https://other.example.org"
	u, _ = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{URL: &newURL})
	if u.CurrentStatus != "unknown" || u.ConsecutiveFailures != 0 {
		t.Fatalf("changing the target must reset health state: %+v", u)
	}
}

func TestTenantScoping(t *testing.T) {
	svc, pool, orgA := setup(t, "free", "active")
	var orgB uuid.UUID
	_ = pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('b') RETURNING id`).Scan(&orgB)
	m, _ := svc.Create(t.Context(), orgA, newIn("a's"))

	name := "x"
	if _, err := svc.Get(t.Context(), orgB, m.ID); !errors.Is(err, monitors.ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if _, err := svc.Update(t.Context(), orgB, m.ID, monitors.UpdateInput{Name: &name}); !errors.Is(err, monitors.ErrNotFound) {
		t.Errorf("update: %v", err)
	}
	if _, err := svc.SetEnabled(t.Context(), orgB, m.ID, false); !errors.Is(err, monitors.ErrNotFound) {
		t.Errorf("pause: %v", err)
	}
	if err := svc.Delete(t.Context(), orgB, m.ID); !errors.Is(err, monitors.ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	list, total, _ := svc.List(t.Context(), orgB, monitors.ListFilter{Limit: 10})
	if len(list) != 0 || total != 0 {
		t.Errorf("list leaks: %v", list)
	}
	if got, _ := svc.Get(t.Context(), orgA, m.ID); got.Name != "a's" || !got.Enabled {
		t.Errorf("owner's monitor was modified: %+v", got)
	}
}

func TestListFilteringPaginationAndSoftDelete(t *testing.T) {
	svc, pool, org := setup(t, "pro", "active")
	var ids []uuid.UUID
	for _, n := range []string{"Alpha", "Beta", "50%_off", "Gamma"} {
		m, err := svc.Create(t.Context(), org, monitors.CreateInput{Name: n, URL: "https://" + n[:1] + ".example.com"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET current_status = 'down' WHERE id = $1`, ids[1])
	_, _ = svc.SetEnabled(t.Context(), org, ids[3], false)

	page, total, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 2})
	if len(page) != 2 || total != 4 {
		t.Fatalf("page=%d total=%d", len(page), total)
	}
	page2, _, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 2, Offset: 2})
	if len(page2) != 2 || page2[0].ID == page[0].ID {
		t.Fatal("offset not applied")
	}
	if l, _, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 10, Status: "down"}); len(l) != 1 || l[0].Name != "Beta" {
		t.Errorf("status filter: %v", l)
	}
	off := false
	if l, _, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 10, Enabled: &off}); len(l) != 1 || l[0].Name != "Gamma" {
		t.Errorf("enabled filter: %v", l)
	}
	if l, _, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 10, Query: "alp"}); len(l) != 1 {
		t.Errorf("search: %v", l)
	}
	// LIKE wildcards in user input must be literal.
	if l, _, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 10, Query: "%"}); len(l) != 1 || l[0].Name != "50%_off" {
		t.Errorf("wildcard escaping: %v", l)
	}

	if err := svc.Delete(t.Context(), org, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(t.Context(), org, ids[0]); !errors.Is(err, monitors.ErrNotFound) {
		t.Errorf("deleted monitor still readable: %v", err)
	}
	if _, total, _ := svc.List(t.Context(), org, monitors.ListFilter{Limit: 10}); total != 3 {
		t.Errorf("total after delete = %d", total)
	}
}

func TestResumeAndTargetChangeMakeMonitorDueImmediately(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	m, _ := svc.Create(t.Context(), org, newIn("m"))
	pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() + interval '1 hour' WHERE id = $1`, m.ID)
	due := func() bool {
		var d bool
		pool.QueryRow(t.Context(), `SELECT next_check_at <= now() FROM monitors WHERE id = $1`, m.ID).Scan(&d)
		return d
	}

	svc.SetEnabled(t.Context(), org, m.ID, false)
	if due() {
		t.Fatal("pausing must not reschedule")
	}
	svc.SetEnabled(t.Context(), org, m.ID, true)
	if !due() {
		t.Fatal("resuming should check right away")
	}

	pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() + interval '1 hour' WHERE id = $1`, m.ID)
	name := "renamed"
	svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: &name})
	if due() {
		t.Fatal("renaming must not reschedule")
	}
	u := "https://other.example.org"
	svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{URL: &u})
	if !due() {
		t.Fatal("a new target should be checked right away")
	}
}
