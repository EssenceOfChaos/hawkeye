package httpapi_test

import (
	"strings"
	"testing"
)

// mcpWorkflowEnv is a verified owner on Pro with a read and write key, connected over MCP.
func mcpWorkflowEnv(t *testing.T) (*env, *client, string, string) {
	t.Helper()
	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "write"})
	return e, owner, org, token
}

// .invalid never resolves, so runs fail at their first step without depending on the network.
func signInStep() map[string]any {
	return map[string]any{"name": "Sign in", "method": "POST", "url": "https://api.hawkeye.invalid/login",
		"body": `{"password":"{{secret.password}}"}`, "save": []any{map[string]any{"name": "token", "from": "json_path", "path": "data.token"}}}
}

func TestAssistantsCreateWorkflowsWithoutSeeingSecrets(t *testing.T) {
	e, owner, _, token := mcpWorkflowEnv(t)
	s := e.mcpSession(t, token)

	// A literal credential is refused: only placeholders may be sent.
	literal := map[string]any{"name": "Leaky", "steps": []any{map[string]any{"name": "Get", "url": "https://api.hawkeye.invalid/x", "auth_type": "bearer", "auth_value": "sk_live_abc123"}}}
	if r := callTool(t, s, "create_workflow", literal); !r.IsError || !strings.Contains(r.Text, "must be a placeholder") {
		t.Fatalf("a literal key was accepted: %+v", r)
	}
	if n := e.countRows(t, `SELECT count(*) FROM monitors`); n != 0 {
		t.Fatalf("%d monitors saved", n)
	}

	orders := map[string]any{"name": "Orders", "url": "https://api.hawkeye.invalid/orders", "auth_type": "bearer", "auth_value": "{{token}}"}
	r := mustOK(t, callTool(t, s, "create_workflow", map[string]any{"name": "Checkout", "steps": []any{signInStep(), orders}, "secrets": []string{"password"}}), "create")
	m := r["monitor"].(map[string]any)
	id := m["id"].(string)
	if m["status"] != "paused" || strings.Join(toStrings(r["secrets_to_enter"]), ",") != "password" || !strings.HasSuffix(r["edit_url"].(string), "/monitors/"+id+"/edit") {
		t.Fatalf("created %v", r)
	}
	if steps := toStrings(m["workflow_steps"]); strings.Join(steps, ",") != "Sign in,Orders" {
		t.Fatalf("workflow_steps %v", steps)
	}
	if n := e.countRows(t, `SELECT count(*) FROM monitor_secrets WHERE name = 'password' AND value IS NULL`); n != 1 {
		t.Fatalf("the secret is not declared and empty: %d", n)
	}

	// Until a person enters the secret, nothing can run it: resuming (over MCP or in the app) and testing are refused.
	if r := callTool(t, s, "resume_monitors", map[string]any{"monitor_ids": []string{id}}); !r.IsError || !strings.Contains(r.Text, "enters its secrets (password)") {
		t.Fatalf("resumed without secrets: %+v", r)
	}
	if r := owner.call("POST", "/api/v1/monitors/"+id+"/resume", nil); r.Status != 409 || errCode(r) != "SECRETS_NOT_SET" {
		t.Fatalf("app resume: %d %v", r.Status, r.Body)
	}
	if r := callTool(t, s, "test_workflow", map[string]any{"monitor_id": id}); !r.IsError || !strings.Contains(r.Text, "secrets") {
		t.Fatalf("tested without secrets: %+v", r)
	}

	// The person enters it in the app; then it can be resumed and tested by id.
	got := owner.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)["workflow"].(map[string]any)
	if sec := got["secrets"].([]any)[0].(map[string]any); sec["name"] != "password" || sec["set"] != false {
		t.Fatalf("app view of the secret %v", sec)
	}
	if r := owner.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"secrets": map[string]any{"password": "entered-by-a-person"}}); r.Status != 200 {
		t.Fatalf("enter secret: %d %v", r.Status, r.Body)
	}
	if r := owner.call("POST", "/api/v1/monitors/"+id+"/resume", nil); r.Status != 200 {
		t.Fatalf("resume after entering: %d %v", r.Status, r.Body)
	}
	tr := mustOK(t, callTool(t, s, "test_workflow", map[string]any{"monitor_id": id}), "test saved")
	if tr["passed"] != false || tr["summary"] != "Step 1 (Sign in): DNS lookup failed" || len(tr["steps"].([]any)) != 1 {
		t.Fatalf("test result %v", tr)
	}
	if n := e.countRows(t, `SELECT count(*) FROM check_results`); n != 0 {
		t.Fatalf("a test run recorded %d results", n)
	}

	// The security log says it came through Claude, with the key.
	ev := find(t, logOf(t, owner, "/api/v1/security-log"), "monitor.created")
	if ev["actor"].(map[string]any)["credential_name"] != "Claude" || ev["details"].(map[string]any)["via"] != "mcp" {
		t.Fatalf("log %v", ev)
	}
}

func TestTestWorkflowDraftsCantWriteOrUseSecrets(t *testing.T) {
	e, _, _, token := mcpWorkflowEnv(t)
	s := e.mcpSession(t, token)
	post := map[string]any{"steps": []any{map[string]any{"name": "Change", "method": "POST", "url": "https://api.hawkeye.invalid/x"}}}
	if r := callTool(t, s, "test_workflow", post); !r.IsError || !strings.Contains(r.Text, "change data on demand") {
		t.Fatalf("a writing draft ran: %+v", r)
	}
	secret := map[string]any{"steps": []any{map[string]any{"name": "Get", "url": "https://api.hawkeye.invalid/x?k={{secret.key}}"}}}
	if r := callTool(t, s, "test_workflow", secret); !r.IsError || !strings.Contains(r.Text, "can't use secrets") {
		t.Fatalf("a draft with secrets ran: %+v", r)
	}
	ok := map[string]any{"steps": []any{map[string]any{"name": "Health", "url": "https://api.hawkeye.invalid/health"}}}
	tr := mustOK(t, callTool(t, s, "test_workflow", ok), "draft")
	if tr["passed"] != false || len(tr["steps"].([]any)) != 1 || tr["needs_fixing"] != false {
		t.Fatalf("draft result %v", tr)
	}
}

func TestReadOnlyKeysCantCreateOrTestWorkflows(t *testing.T) {
	e, owner, _, _ := mcpWorkflowEnv(t)
	_, ro := newKey(t, owner, map[string]any{"name": "Reader", "scope": "read"})
	s := e.mcpSession(t, ro)
	steps := []any{map[string]any{"name": "a", "url": "https://api.hawkeye.invalid/"}}
	for tool, args := range map[string]map[string]any{"create_workflow": {"name": "x", "steps": steps}, "test_workflow": {"steps": steps}} {
		if r := callTool(t, s, tool, args); !r.IsError || !strings.Contains(r.Text, "read-only") {
			t.Errorf("%s with a read-only key: %+v", tool, r)
		}
	}
}

func toStrings(v any) []string {
	var out []string
	if list, ok := v.([]any); ok {
		for _, x := range list {
			out = append(out, x.(string))
		}
	}
	return out
}
