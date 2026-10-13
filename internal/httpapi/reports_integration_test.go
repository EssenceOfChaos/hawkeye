package httpapi_test

import (
	"fmt"
	"testing"
	"time"
)

// seedResults inserts n results `every` apart ending `ago` before now; every failEvery-th fails (0 = none).
func seedResults(t *testing.T, e *env, monitorID string, n int, every, ago time.Duration, failEvery, ms int) {
	t.Helper()
	_, err := e.pool.Exec(t.Context(), `INSERT INTO check_results
		(monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary)
		SELECT $1, ts, ts, ok, CASE WHEN ok THEN 200 ELSE 503 END, CASE WHEN ok THEN $6::int END,
			CASE WHEN ok THEN NULL ELSE 'http_status' END, CASE WHEN ok THEN NULL ELSE 'Received HTTP 503' END
		FROM (SELECT now() - $3::interval - ($2::interval * g) AS ts, NOT ($4::int > 0 AND g % $4::int = 0) AS ok FROM generate_series(0, $5::int - 1) g) s`,
		monitorID, every, ago, failEvery, n, ms)
	if err != nil {
		t.Fatal(err)
	}
}

func newMonitorID(t *testing.T, c *client, name string) string {
	t.Helper()
	r := c.call("POST", "/api/v1/monitors", monitorBody(name))
	if r.Status != 201 {
		t.Fatalf("create monitor: %d %v", r.Status, r.Body)
	}
	return r.Body["monitor"].(map[string]any)["id"].(string)
}

func TestMonitorUptimeEndpoint(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "u@example.com")
	m := newMonitorID(t, c, "Site")
	seedResults(t, e, m, 100, 6*time.Minute, time.Minute, 10, 250) // 10% failures

	r := c.call("GET", "/api/v1/monitors/"+m+"/uptime?range=24h", nil)
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	u := r.Body["uptime"].(map[string]any)
	rg := r.Body["range"].(map[string]any)
	if u["uptime_percent"].(float64) != 90 || u["total_checks"].(float64) != 100 || u["failed_checks"].(float64) != 10 ||
		u["method"] != "check_based" || u["avg_response_ms"].(float64) != 250 || u["insufficient_data"] != false {
		t.Fatalf("uptime: %v", u)
	}
	if rg["preset"] != "24h" || rg["clamped_to_retention"] != false || rg["retention_days"].(float64) != 7 {
		t.Fatalf("range: %v", rg)
	}

	// The default range is 24h.
	if d := c.call("GET", "/api/v1/monitors/"+m+"/uptime", nil); d.Body["range"].(map[string]any)["preset"] != "24h" {
		t.Fatalf("default range: %v", d.Body)
	}
}

func TestFreePlanRangeIsClampedAndLabelled(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "f@example.com")
	m := newMonitorID(t, c, "Site")
	seedResults(t, e, m, 50, time.Minute, 20*24*time.Hour, 0, 100) // 20 days old: beyond the 7-day free window
	seedResults(t, e, m, 20, time.Minute, time.Minute, 0, 100)

	r := c.call("GET", "/api/v1/monitors/"+m+"/uptime?range=30d", nil)
	rg := r.Body["range"].(map[string]any)
	u := r.Body["uptime"].(map[string]any)
	if rg["clamped_to_retention"] != true || u["clamped_to_retention"] != true || u["total_checks"].(float64) != 20 {
		t.Fatalf("range=%v uptime=%v", rg, u)
	}
	// A pro plan sees further back.
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code='pro')`)
	r = c.call("GET", "/api/v1/monitors/"+m+"/uptime?range=30d", nil)
	if r.Body["range"].(map[string]any)["clamped_to_retention"] != false || r.Body["uptime"].(map[string]any)["total_checks"].(float64) != 70 {
		t.Fatalf("pro: %v", r.Body)
	}
}

func TestRangeValidation(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "v@example.com")
	m := newMonitorID(t, c, "Site")
	for _, q := range []string{"range=2w", "from=2026-01-01T00:00:00Z", "from=yesterday&to=today", "range=7d&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z",
		"from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z"} {
		for _, path := range []string{"/uptime", "/response-times", "/results"} {
			if r := c.call("GET", "/api/v1/monitors/"+m+path+"?"+q, nil); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
				t.Errorf("%s?%s: %d %v", path, q, r.Status, r.Body)
			}
		}
	}
	for _, q := range []string{"limit=0", "limit=1000", "offset=-3", "limit=abc"} {
		if r := c.call("GET", "/api/v1/monitors/"+m+"/results?"+q, nil); r.Status != 400 {
			t.Errorf("results?%s: %d", q, r.Status)
		}
	}
}

func TestResponseTimesAndResultsEndpoints(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "r@example.com")
	m := newMonitorID(t, c, "Site")
	seedResults(t, e, m, 60, time.Minute, time.Minute, 20, 180)

	r := c.call("GET", "/api/v1/monitors/"+m+"/response-times?range=24h", nil)
	pts := r.Body["points"].([]any)
	if r.Status != 200 || r.Body["bucket_seconds"].(float64) != 900 || len(pts) == 0 || len(pts) > 150 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	p := pts[0].(map[string]any)
	if _, ok := p["t"]; !ok || p["checks"] == nil || p["failed"] == nil {
		t.Fatalf("point shape: %v", p)
	}

	res := c.call("GET", "/api/v1/monitors/"+m+"/results?limit=10&offset=0", nil)
	if res.Status != 200 || res.Body["total"].(float64) != 60 || len(res.Body["results"].([]any)) != 10 {
		t.Fatalf("%d %v", res.Status, res.Body)
	}
	first := res.Body["results"].([]any)[0].(map[string]any)
	if _, ok := first["checked_at"]; !ok || first["success"] == nil {
		t.Fatalf("result shape: %v", first)
	}
}

func TestReportEndpointsAreTenantScoped(t *testing.T) {
	e := newEnv(t, 1000)
	alice, _ := e.register(t, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	m := newMonitorID(t, alice, "Secret")
	seedResults(t, e, m, 20, time.Minute, time.Minute, 0, 100)

	for _, p := range []string{"uptime", "response-times", "results", "incidents"} {
		if r := bob.call("GET", "/api/v1/monitors/"+m+"/"+p, nil); r.Status != 404 {
			t.Errorf("bob reading alice's %s: %d", p, r.Status)
		}
		if r := e.newClient(t).call("GET", "/api/v1/monitors/"+m+"/"+p, nil); r.Status != 401 {
			t.Errorf("anonymous %s: %d", p, r.Status)
		}
	}
	// Bob's dashboard must not include Alice's data.
	s := bob.call("GET", "/api/v1/dashboard/summary", nil).Body["summary"].(map[string]any)
	if s["monitors"].(map[string]any)["total"].(float64) != 0 || s["uptime"].(map[string]any)["total_checks"].(float64) != 0 {
		t.Fatalf("bob's summary leaks: %v", s)
	}
	if pts := bob.call("GET", "/api/v1/dashboard/response-times", nil).Body["points"].([]any); len(pts) != 0 {
		t.Fatalf("bob's series leaks: %v", pts)
	}
	// A deleted monitor's reports are gone.
	alice.call("DELETE", "/api/v1/monitors/"+m, nil)
	if r := alice.call("GET", "/api/v1/monitors/"+m+"/uptime", nil); r.Status != 404 {
		t.Fatalf("deleted monitor: %d", r.Status)
	}
}

func TestDashboardEndpoints(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "d@example.com")
	up, down := newMonitorID(t, c, "Up"), newMonitorID(t, c, "Down")
	paused := newMonitorID(t, c, "Paused")
	c.call("POST", "/api/v1/monitors/"+paused+"/pause", nil)
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status='up' WHERE id=$1`, up)
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status='down' WHERE id=$1`, down)
	seedResults(t, e, up, 40, time.Minute, time.Minute, 0, 100)
	seedResults(t, e, down, 40, time.Minute, time.Minute, 1, 100) // all failures
	for i := 0; i < 7; i++ {
		e.pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO incidents (monitor_id, started_at, resolved_at, status) VALUES ($1, now() - interval '%d hours', now() - interval '%d hours' + interval '5 minutes', 'resolved')`, 10+i, 10+i), up)
	}
	e.pool.Exec(t.Context(), `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now() - interval '1 hour')`, down)

	r := c.call("GET", "/api/v1/dashboard/summary", nil)
	s := r.Body["summary"].(map[string]any)
	counts := s["monitors"].(map[string]any)
	up24 := s["uptime"].(map[string]any)
	if r.Status != 200 || counts["total"].(float64) != 3 || counts["up"].(float64) != 1 || counts["down"].(float64) != 1 || counts["paused"].(float64) != 1 ||
		s["open_incidents"].(float64) != 1 || up24["total_checks"].(float64) != 80 || up24["uptime_percent"].(float64) != 50 {
		t.Fatalf("%v", s)
	}

	inc := c.call("GET", "/api/v1/dashboard/recent-incidents?limit=5", nil).Body["incidents"].([]any)
	if len(inc) != 5 || inc[0].(map[string]any)["status"] != "open" {
		t.Fatalf("recent incidents: %v", inc)
	}
	if r := c.call("GET", "/api/v1/dashboard/recent-incidents?limit=99", nil); r.Status != 400 {
		t.Fatalf("limit bound: %d", r.Status)
	}
	if r := c.call("GET", "/api/v1/dashboard/response-times?range=7d", nil); r.Status != 200 || len(r.Body["points"].([]any)) == 0 {
		t.Fatalf("org series: %d %v", r.Status, r.Body)
	}
}
