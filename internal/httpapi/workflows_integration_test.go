package httpapi_test

import (
	"strings"
	"testing"
)

func workflowBody(steps ...map[string]any) map[string]any {
	list := make([]any, len(steps))
	for i, s := range steps {
		list[i] = s
	}
	return map[string]any{"type": "workflow", "name": "Checkout", "steps": list}
}

func loginStep() map[string]any {
	return map[string]any{"name": "Sign in", "method": "POST", "url": "https://api.example.com/login",
		"body":        `{"user":"monitor","password":"{{secret.password}}"}`,
		"extractions": []any{map[string]any{"name": "token", "from": "json_path", "path": "data.token"}}}
}

func ordersStep() map[string]any {
	return map[string]any{"name": "Orders", "method": "GET", "url": "https://api.example.com/orders?after={{token}}",
		"auth": map[string]any{"type": "bearer", "secret": "{{token}}"}, "expected_status_code": 200}
}

func fieldErrors(r resp) map[string]any {
	if e, ok := r.Body["error"].(map[string]any); ok {
		if d, ok := e["details"].(map[string]any); ok {
			if f, ok := d["fields"].(map[string]any); ok {
				return f
			}
		}
	}
	return nil
}

func TestWorkflowsAreSavedWithoutRevealingSecrets(t *testing.T) {
	e := newEnv(t, 100000)
	c, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")

	body := workflowBody(loginStep(), ordersStep())
	body["secrets"] = map[string]any{"password": "correct horse battery"}
	body["check_interval_seconds"] = 300
	r := c.call("POST", "/api/v1/monitors", body)
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	m := r.Body["monitor"].(map[string]any)
	id := m["id"].(string)
	wf := m["workflow"].(map[string]any)
	steps := wf["steps"].([]any)
	if m["type"] != "workflow" || m["url"] != "https://api.example.com/login" || len(steps) != 2 {
		t.Fatalf("%v", m)
	}
	if a := steps[1].(map[string]any)["auth"].(map[string]any); a["type"] != "bearer" || a["secret_set"] != true || a["secret"] != nil {
		t.Fatalf("step auth %v", a)
	}
	if s := wf["secrets"].([]any); len(s) != 1 || s[0].(map[string]any)["name"] != "password" || s[0].(map[string]any)["value"] != nil {
		t.Fatalf("secrets %v", s)
	}
	for _, q := range []string{`SELECT count(*) FROM monitor_secrets WHERE value LIKE '%horse%'`, `SELECT count(*) FROM monitor_steps WHERE auth_secret LIKE '%token%'`} {
		if n := e.countRows(t, q); n != 0 {
			t.Fatalf("stored in clear: %s", q)
		}
	}

	// An edit that sends the steps again without the step's credentials keeps them; a secret set to null is removed.
	got := c.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)["workflow"].(map[string]any)["steps"]
	r = c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"steps": got, "secrets": map[string]any{"extra": "abcd1234", "password": "new horse"}})
	if r.Status != 200 {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	wf = r.Body["monitor"].(map[string]any)["workflow"].(map[string]any)
	if a := wf["steps"].([]any)[1].(map[string]any)["auth"].(map[string]any); a["secret_set"] != true {
		t.Fatalf("step credentials were dropped: %v", a)
	}
	if n := len(wf["secrets"].([]any)); n != 2 {
		t.Fatalf("secrets %v", wf["secrets"])
	}
	r = c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"secrets": map[string]any{"extra": nil}})
	if n := len(r.Body["monitor"].(map[string]any)["workflow"].(map[string]any)["secrets"].([]any)); r.Status != 200 || n != 1 {
		t.Fatalf("remove a secret: %d %v", r.Status, r.Body)
	}
	// Removing a secret a step still uses is refused.
	if r := c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"secrets": map[string]any{"password": nil}}); r.Status != 400 {
		t.Fatalf("removed a secret in use: %d %v", r.Status, r.Body)
	}

	// Deleting the monitor erases its steps and secrets at once.
	if r := c.call("DELETE", "/api/v1/monitors/"+id, nil); r.Status != 204 {
		t.Fatalf("delete: %d", r.Status)
	}
	if n := e.countRows(t, `SELECT (SELECT count(*) FROM monitor_steps) + (SELECT count(*) FROM monitor_secrets)`); n != 0 {
		t.Fatalf("%d step and secret rows left", n)
	}
}

func TestWorkflowValidation(t *testing.T) {
	e := newEnv(t, 100000)
	c, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")

	cases := map[string]struct {
		body  map[string]any
		field string
	}{
		"host from a value": {workflowBody(map[string]any{"name": "x", "url": "https://{{secret.host}}/a"}), "steps[0].url"},
		"unknown value":     {workflowBody(map[string]any{"name": "x", "url": "https://api.example.com/{{token}}"}), "steps[0].url"},
		"later value":       {workflowBody(ordersStep(), loginStep()), "steps[0].url"},
		"missing secret":    {workflowBody(loginStep()), "steps[0].body"},
		"bad extraction":    {workflowBody(map[string]any{"name": "x", "url": "https://api.example.com/", "extractions": []any{map[string]any{"name": "1bad", "from": "json_path", "path": "a"}}}), "steps[0].extractions[0]"},
		"no steps":          {workflowBody(), "steps"},
		"website fields": {func() map[string]any {
			b := workflowBody(map[string]any{"name": "x", "url": "https://api.example.com/"})
			b["url"] = "https://x.example.com/"
			return b
		}(), "url"},
		"write too often": {func() map[string]any {
			b := workflowBody(map[string]any{"name": "x", "method": "POST", "url": "https://api.example.com/"})
			b["check_interval_seconds"] = 60
			return b
		}(), "check_interval_seconds"},
	}
	for name, tc := range cases {
		r := c.call("POST", "/api/v1/monitors", tc.body)
		if r.Status != 400 || fieldErrors(r)[tc.field] == nil {
			t.Errorf("%s: %d, want a %s error: %v", name, r.Status, tc.field, r.Body)
		}
	}
	// Without an interval, a workflow that writes runs every 5 minutes.
	r := c.call("POST", "/api/v1/monitors", workflowBody(map[string]any{"name": "x", "method": "POST", "url": "https://api.example.com/"}))
	if r.Status != 201 || r.Body["monitor"].(map[string]any)["check_interval_seconds"].(float64) != 300 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	// A step's host can't be a type of monitor it isn't: the steps field belongs to workflows only.
	if r := c.call("POST", "/api/v1/monitors", map[string]any{"name": "w", "url": "https://example.com/", "steps": []any{}}); r.Status != 400 {
		t.Fatalf("steps on a website monitor: %d", r.Status)
	}
}

func TestWorkflowPlanLimits(t *testing.T) {
	e := newEnv(t, 100000)
	c, _ := e.register(t, "owner@example.com") // Free: one workflow of up to 3 steps
	e.markVerified(t, "owner@example.com")
	step := func(n string) map[string]any { return map[string]any{"name": n, "url": "https://api.example.com/" + n} }
	r := c.call("POST", "/api/v1/monitors", workflowBody(step("a"), step("b"), step("c"), step("d")))
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" || !strings.Contains(r.Body["error"].(map[string]any)["message"].(string), "up to 3 steps") {
		t.Fatalf("4 steps on Free: %d %v", r.Status, r.Body)
	}
	if r := c.call("POST", "/api/v1/monitors", workflowBody(step("a"), step("b"), step("c"))); r.Status != 201 {
		t.Fatalf("3 steps: %d %v", r.Status, r.Body)
	}
	r = c.call("POST", "/api/v1/monitors", workflowBody(step("a")))
	if r.Status != 403 || !strings.Contains(r.Body["error"].(map[string]any)["message"].(string), "multi-step API check") {
		t.Fatalf("a second workflow on Free: %d %v", r.Status, r.Body)
	}
	// It counts as a monitor, too.
	if u := c.call("GET", "/api/v1/usage", nil).Body["usage"].(map[string]any); u["monitors_used"].(float64) != 1 {
		t.Fatalf("usage %v", u)
	}
}

func TestTestWorkflowRunsWithoutSaving(t *testing.T) {
	e := newEnv(t, 100000)
	c, org := e.register(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	// .invalid never resolves (RFC 2606), so the run fails at its first step without depending on the network. The engine's own tests and the
	// worker's cover runs that succeed.
	login, orders := loginStep(), ordersStep()
	login["url"] = "https://api.hawkeye.invalid/login"
	orders["url"] = "https://api.hawkeye.invalid/orders"
	draft := map[string]any{"steps": []any{login, orders}, "secrets": map[string]any{"password": "sekrit-value"}}

	// A workflow that sends POST needs a verified address, even to try it.
	if r := c.call("POST", "/api/v1/monitors/test-workflow", draft); r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("unverified: %d %v", r.Status, r.Body)
	}
	e.markVerified(t, "owner@example.com")
	r := c.call("POST", "/api/v1/monitors/test-workflow", draft)
	if r.Status != 200 {
		t.Fatalf("test: %d %v", r.Status, r.Body)
	}
	res := r.Body["result"].(map[string]any)
	if res["success"] != false || res["error_category"] != "dns" || len(res["steps"].([]any)) != 1 ||
		res["error_summary"] != "Step 1 (Sign in): DNS lookup failed" {
		t.Fatalf("result %v", res)
	}
	if n := e.countRows(t, `SELECT count(*) FROM monitors`); n != 0 {
		t.Fatalf("a test run saved %d monitors", n)
	}
	// It is validated like a saved workflow.
	bad := map[string]any{"steps": []any{ordersStep()}}
	if r := c.call("POST", "/api/v1/monitors/test-workflow", bad); r.Status != 400 || fieldErrors(r)["steps[0].url"] == nil {
		t.Fatalf("invalid draft: %d %v", r.Status, r.Body)
	}
}
