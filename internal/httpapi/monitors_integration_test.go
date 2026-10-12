package httpapi_test

import (
	"testing"
)

func monitorBody(name string) map[string]any {
	return map[string]any{"name": name, "url": "https://example.com"}
}

func TestMonitorCRUDOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "m@example.com")

	r := c.call("POST", "/api/v1/monitors", monitorBody("Home"))
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	m := r.Body["monitor"].(map[string]any)
	id := m["id"].(string)
	if m["check_interval_seconds"].(float64) != 300 || m["current_status"] != "unknown" {
		t.Fatalf("defaults: %v", m)
	}

	if r := c.call("GET", "/api/v1/monitors", nil); r.Status != 200 || r.Body["total"].(float64) != 1 {
		t.Fatalf("list: %d %v", r.Status, r.Body)
	}
	r = c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"name": "Renamed", "expected_status_code": 200})
	if r.Status != 200 || r.Body["monitor"].(map[string]any)["name"] != "Renamed" {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/monitors/"+id+"/pause", nil); r.Status != 200 || r.Body["monitor"].(map[string]any)["enabled"] != false {
		t.Fatalf("pause: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/monitors/"+id+"/resume", nil); r.Status != 200 || r.Body["monitor"].(map[string]any)["enabled"] != true {
		t.Fatalf("resume: %d %v", r.Status, r.Body)
	}
	if r := c.call("DELETE", "/api/v1/monitors/"+id, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if r := c.call("GET", "/api/v1/monitors/"+id, nil); r.Status != 404 || errCode(r) != "NOT_FOUND" {
		t.Fatalf("get deleted: %d %v", r.Status, r.Body)
	}
}

func TestClientCannotSetInternalState(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "s@example.com")
	body := monitorBody("Home")
	body["current_status"] = "up"
	body["consecutive_failures"] = 99
	body["last_checked_at"] = "2020-01-01T00:00:00Z"
	body["organization_id"] = "00000000-0000-0000-0000-000000000001"
	body["enabled"] = false
	m := c.call("POST", "/api/v1/monitors", body).Body["monitor"].(map[string]any)
	if m["current_status"] != "unknown" || m["consecutive_failures"].(float64) != 0 || m["last_checked_at"] != nil || m["enabled"] != true {
		t.Fatalf("internal state was client-controlled: %v", m)
	}
	patched := c.call("PATCH", "/api/v1/monitors/"+m["id"].(string), map[string]any{"current_status": "down", "consecutive_failures": 5}).Body["monitor"].(map[string]any)
	if patched["current_status"] != "unknown" || patched["consecutive_failures"].(float64) != 0 {
		t.Fatalf("patch changed internal state: %v", patched)
	}
}

// Phase 3 acceptance criterion.
func TestFreeUserLimitedToFiveMonitors(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "f@example.com")
	for i := 1; i <= 5; i++ {
		if r := c.call("POST", "/api/v1/monitors", monitorBody("m")); r.Status != 201 {
			t.Fatalf("monitor %d: %d %v", i, r.Status, r.Body)
		}
	}
	r := c.call("POST", "/api/v1/monitors", monitorBody("sixth"))
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("sixth: %d %v", r.Status, r.Body)
	}
	d := r.Body["error"].(map[string]any)["details"].(map[string]any)
	next := d["next_plan"].(map[string]any)
	if d["limit"].(float64) != 5 || d["used"].(float64) != 5 || d["limit_type"] != "monitors" || d["plan"] != "free" ||
		d["upgrade_url"] != "http://app.test/billing" || next["code"] != "pro" || next["max_monitors"].(float64) != 50 {
		t.Fatalf("details: %v", d)
	}

	u := c.call("GET", "/api/v1/usage", nil).Body["usage"].(map[string]any)
	if u["monitors_used"].(float64) != 5 || u["monitor_limit"].(float64) != 5 || u["over_limit"] != false {
		t.Fatalf("usage: %v", u)
	}

	// Intervals below the plan minimum are rejected server-side too.
	body := monitorBody("fast")
	body["check_interval_seconds"] = 60
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = (SELECT id FROM monitors LIMIT 1)`)
	if r := c.call("POST", "/api/v1/monitors", body); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("fast interval: %d %v", r.Status, r.Body)
	}
}

func TestMonitorTenantIsolationOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	alice, aliceOrg := e.register(t, "alice@example.com")
	bob, _ := e.register(t, "bob@example.com")
	id := alice.call("POST", "/api/v1/monitors", monitorBody("secret")).Body["monitor"].(map[string]any)["id"].(string)

	for name, r := range map[string]resp{
		"get":    bob.call("GET", "/api/v1/monitors/"+id, nil),
		"patch":  bob.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"name": "pwned"}),
		"pause":  bob.call("POST", "/api/v1/monitors/"+id+"/pause", nil),
		"delete": bob.call("DELETE", "/api/v1/monitors/"+id, nil),
	} {
		if r.Status != 404 {
			t.Errorf("%s: want 404, got %d %v", name, r.Status, r.Body)
		}
	}
	if l := bob.call("GET", "/api/v1/monitors", nil); l.Body["total"].(float64) != 0 {
		t.Fatalf("bob sees alice's monitors: %v", l.Body)
	}

	// Naming someone else's organization in the header must not grant access.
	req := func(method, path string, hdr string) int {
		return bob.doWithHeader(method, path, hdr)
	}
	if st := req("GET", "/api/v1/monitors", aliceOrg); st != 404 {
		t.Fatalf("foreign X-Organization-ID: %d", st)
	}
	if st := req("GET", "/api/v1/monitors", "garbage"); st != 404 {
		t.Fatalf("malformed X-Organization-ID: %d", st)
	}
	if got := alice.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any); got["name"] != "secret" || got["enabled"] != true {
		t.Fatalf("alice's monitor was changed: %v", got)
	}
}

func TestMonitorValidationAndAuthOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	anon := e.newClient(t)
	if r := anon.call("POST", "/api/v1/monitors", monitorBody("x")); r.Status != 401 {
		t.Fatalf("anon create: %d", r.Status)
	}
	c, _ := e.register(t, "v@example.com")
	r := c.call("POST", "/api/v1/monitors", map[string]any{"name": "x", "url": "http://169.254.169.254/"})
	if r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("metadata url: %d %v", r.Status, r.Body)
	}
	if r := c.call("GET", "/api/v1/monitors?limit=500&status=bogus", nil); r.Status != 400 {
		t.Fatalf("bad query: %d", r.Status)
	}
	if r := c.call("GET", "/api/v1/monitors/not-a-uuid", nil); r.Status != 404 {
		t.Fatalf("bad id: %d", r.Status)
	}
}

func TestPlansEndpointAndOverLimitAfterDowngrade(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "p@example.com")
	r := c.call("GET", "/api/v1/plans", nil)
	pl := r.Body["plans"].([]any)
	if r.Status != 200 || len(pl) != 3 || pl[0].(map[string]any)["code"] != "free" || pl[2].(map[string]any)["monthly_price_cents"].(float64) != 5900 {
		t.Fatalf("plans: %d %v", r.Status, r.Body)
	}

	// Simulate a paid org that lapsed with more monitors than the free plan allows.
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code='pro')`)
	for range 7 {
		if r := c.call("POST", "/api/v1/monitors", monitorBody("m")); r.Status != 201 {
			t.Fatalf("pro create: %d %v", r.Status, r.Body)
		}
	}
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled'`)

	if l := c.call("GET", "/api/v1/monitors", nil); l.Body["total"].(float64) != 7 {
		t.Fatalf("monitors must survive: %v", l.Body)
	}
	r = c.call("POST", "/api/v1/monitors", monitorBody("eighth"))
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("create over limit: %d %v", r.Status, r.Body)
	}
	msg := r.Body["error"].(map[string]any)["message"].(string)
	if want := "delete 2"; !contains(msg, want) {
		t.Fatalf("message should say how to recover (%q): %s", want, msg)
	}
	u := c.call("GET", "/api/v1/usage", nil).Body["usage"].(map[string]any)
	if u["over_limit"] != true {
		t.Fatalf("usage: %v", u)
	}
	// Existing monitors can still be paused or removed.
	list := c.call("GET", "/api/v1/monitors", nil).Body["monitors"].([]any)
	id := list[0].(map[string]any)["id"].(string)
	if r := c.call("POST", "/api/v1/monitors/"+id+"/pause", nil); r.Status != 200 {
		t.Fatalf("pause over limit: %d", r.Status)
	}
}
