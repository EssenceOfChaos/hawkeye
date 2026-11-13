package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hawkeye/internal/mcpserver"
)

// headerTransport adds fixed headers to every request, as an MCP client configured with an API key does.
type headerTransport struct{ headers map[string]string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

// mcpSession connects to /mcp with the given key, as Claude Code does with `claude mcp add --transport http … --header "Authorization: Bearer …"`.
func (e *env) mcpSession(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	tr := &mcp.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: headerTransport{map[string]string{"Authorization": "Bearer " + token}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type toolResult struct {
	Data    map[string]any
	IsError bool
	Text    string
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) toolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out toolResult
	out.IsError = res.IsError
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out.Text += tc.Text
		}
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(b, &out.Data)
	}
	return out
}

func mustOK(t *testing.T, r toolResult, what string) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("%s: tool error: %s", what, r.Text)
	}
	return r.Data
}

func TestMCPListsEveryToolWithTheAnnotationsTheDirectoryRequires(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "read"})
	s := e.mcpSession(t, token)

	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := []string{"get_account_overview", "list_monitors", "get_monitor", "get_uptime", "list_incidents", "get_incident", "list_status_pages",
		"list_maintenance_windows", "suggest_monitors"}
	destructive := []string{"post_incident_update", "delete_incident_update", "delete_maintenance_window", "create_workflow"}
	additive := []string{"pause_monitors", "resume_monitors", "create_monitor", "schedule_maintenance", "test_workflow"}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if tool.Title == "" || a == nil || a.Title == "" || len(tool.Name) > 64 || tool.Description == "" {
			t.Errorf("%s: needs a title, annotations, a description and a short name", tool.Name)
			continue
		}
		switch {
		case slices.Contains(readOnly, tool.Name):
			if !a.ReadOnlyHint {
				t.Errorf("%s should be read-only", tool.Name)
			}
		case slices.Contains(destructive, tool.Name):
			if a.ReadOnlyHint || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Errorf("%s should be marked destructive, so Claude always asks first", tool.Name)
			}
		case slices.Contains(additive, tool.Name):
			if a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
				t.Errorf("%s should say explicitly that it is not destructive", tool.Name)
			}
		default:
			t.Errorf("%s is not classified in this test", tool.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, mcpserver.Names()) || len(names) != len(readOnly)+len(destructive)+len(additive) {
		t.Fatalf("tools %v", names)
	}
	if strings.Contains(strings.ToLower(res.Tools[0].Description), "you must") {
		t.Error("descriptions say what a tool does, not how Claude should behave")
	}
}

func TestMCPReadToolsAnswerFromTheKeysOrganization(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	site := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "url": "https://shop.example.com",
		"headers": []map[string]any{{"name": "X-Secret-Header", "value": "header-value-123"}},
		"auth":    map[string]any{"type": "bearer", "secret": "super-secret-token"}}).Body["monitor"].(map[string]any)["id"].(string)
	owner.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "Nightly backup", "check_interval_seconds": 86400})
	inc := e.openIncident(t, site)
	owner.call("POST", "/api/v1/status-pages", map[string]any{"slug": "acme", "title": "Acme", "published": true, "monitors": []map[string]any{{"monitor_id": site}}})
	owner.call("POST", "/api/v1/incidents/"+inc+"/updates", map[string]any{"status": "investigating", "message": "Looking into it."})
	owner.call("POST", "/api/v1/maintenance-windows", map[string]any{"name": "Upgrade", "starts_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339),
		"duration_minutes": 60, "repeat": "none", "all_monitors": true})

	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "read"})
	s := e.mcpSession(t, token)

	ov := mustOK(t, callTool(t, s, "get_account_overview", nil), "overview")
	if ov["plan"] != "Pro" || ov["monitors_used"] != float64(2) || ov["can_change_data"] != false || ov["open_incidents"] != float64(1) || ov["your_role"] != "owner" {
		t.Fatalf("%v", ov)
	}

	list := mustOK(t, callTool(t, s, "list_monitors", nil), "list")
	if list["total"] != float64(2) {
		t.Fatalf("%v", list)
	}
	paused := mustOK(t, callTool(t, s, "list_monitors", map[string]any{"status": "paused"}), "paused")
	if paused["total"] != float64(0) {
		t.Fatalf("%v", paused)
	}

	got := callTool(t, s, "get_monitor", map[string]any{"monitor_id": site})
	raw, _ := json.Marshal(got.Data)
	mustOK(t, got, "get")
	for _, secret := range []string{"super-secret-token", "header-value-123", "X-Secret-Header"} {
		if strings.Contains(string(raw)+got.Text, secret) {
			t.Fatalf("get_monitor must never return credentials or headers: %s", raw)
		}
	}
	if got.Data["type"] != "website" || got.Data["target"] != "https://shop.example.com" {
		t.Fatalf("%v", got.Data)
	}

	if up := mustOK(t, callTool(t, s, "get_uptime", map[string]any{"monitor_id": site, "range": "7d"}), "uptime"); up["scope"] != "Shop" {
		t.Fatalf("%v", up)
	}
	if r := callTool(t, s, "get_uptime", map[string]any{"range": "5y"}); !r.IsError || !strings.Contains(r.Text, "range") {
		t.Fatalf("a bad range is explained: %+v", r)
	}

	incs := mustOK(t, callTool(t, s, "list_incidents", map[string]any{"status": "open"}), "incidents")
	if incs["total"] != float64(1) {
		t.Fatalf("%v", incs)
	}
	detail := mustOK(t, callTool(t, s, "get_incident", map[string]any{"incident_id": inc}), "incident")
	ups := detail["updates"].([]any)
	pages := detail["shown_on_status_pages"].([]any)
	if len(ups) != 1 || ups[0].(map[string]any)["message"] != "Looking into it." || len(pages) != 1 || pages[0] != "http://app.test/status/acme" {
		t.Fatalf("%v", detail)
	}

	sp := mustOK(t, callTool(t, s, "list_status_pages", nil), "pages")["status_pages"].([]any)
	if len(sp) != 1 || sp[0].(map[string]any)["public_url"] != "http://app.test/status/acme" {
		t.Fatalf("%v", sp)
	}
	if w := mustOK(t, callTool(t, s, "list_maintenance_windows", nil), "windows")["maintenance_windows"].([]any); len(w) != 1 {
		t.Fatalf("%v", w)
	}

	// Another organization's monitor is simply not found.
	other, _ := e.register(t, "other@example.com")
	theirs := other.call("POST", "/api/v1/monitors", monitorBody("Theirs")).Body["monitor"].(map[string]any)["id"].(string)
	if r := callTool(t, s, "get_monitor", map[string]any{"monitor_id": theirs}); !r.IsError || !strings.Contains(r.Text, "No monitor with that ID") {
		t.Fatalf("%+v", r)
	}
	if r := callTool(t, s, "get_monitor", map[string]any{"monitor_id": "not-an-id"}); !r.IsError {
		t.Fatalf("%+v", r)
	}

	// A read key can't change anything, and says how to fix it.
	if r := callTool(t, s, "pause_monitors", map[string]any{"monitor_ids": []string{site}}); !r.IsError || !strings.Contains(r.Text, "read-only") {
		t.Fatalf("%+v", r)
	}
}

func TestMCPWriteToolsCreateHeartbeatsAndDNSMonitors(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "write"})
	s := e.mcpSession(t, token)

	hb := mustOK(t, callTool(t, s, "create_monitor", map[string]any{"type": "heartbeat", "name": "Backups", "check_interval_minutes": 1440, "grace_minutes": 30}), "heartbeat")
	if !strings.HasPrefix(hb["heartbeat_ping_url"].(string), "http://app.test/ping/") || hb["check_interval_seconds"] != float64(86400) {
		t.Fatalf("%v", hb)
	}
	dns := mustOK(t, callTool(t, s, "create_monitor", map[string]any{"type": "dns", "name": "Mail", "host": "example.com", "record_type": "mx", "expected": "mx.example.com"}), "dns")
	if dns["dns_record_type"] != "MX" || dns["target"] != "example.com" {
		t.Fatalf("%v", dns)
	}
	for _, bad := range []map[string]any{
		{"type": "ftp", "name": "x"},
		{"type": "website", "name": "x", "url": "https://example.com", "host": "example.com"},
		{"type": "website", "name": "", "url": "https://example.com"},
		{"type": "website", "name": "Internal", "url": "http://127.0.0.1"},
	} {
		if r := callTool(t, s, "create_monitor", bad); !r.IsError {
			t.Fatalf("%v should be refused: %+v", bad, r)
		}
	}

	// Pause and resume by tag.
	tag := owner.call("POST", "/api/v1/tags", map[string]any{"name": "Acme"}).Body["tag"].(map[string]any)["id"].(string)
	owner.call("POST", "/api/v1/monitors/bulk", map[string]any{"monitor_ids": []string{hb["id"].(string), dns["id"].(string)}, "action": "add_tags", "tag_ids": []string{tag}})
	if r := mustOK(t, callTool(t, s, "pause_monitors", map[string]any{"tag": "acme"}), "pause"); r["changed"] != float64(2) {
		t.Fatalf("%v", r)
	}
	if r := mustOK(t, callTool(t, s, "list_monitors", map[string]any{"status": "paused", "tag": "Acme"}), "list"); r["total"] != float64(2) {
		t.Fatalf("%v", r)
	}
	if r := callTool(t, s, "pause_monitors", map[string]any{"tag": "nope"}); !r.IsError || !strings.Contains(r.Text, "No tag") {
		t.Fatalf("%+v", r)
	}
	if r := mustOK(t, callTool(t, s, "resume_monitors", map[string]any{"monitor_ids": []string{hb["id"].(string)}}), "resume"); r["changed"] != float64(1) {
		t.Fatalf("%v", r)
	}
}

// TestToolsRefuseWhatTheAPIRefuses runs every write tool next to its REST route, for keys that differ in scope, role and plan, and checks they always
// agree: a tool may never do what the REST API would refuse (or refuse what it allows).
func TestToolsRefuseWhatTheAPIRefuses(t *testing.T) {
	scenarios := []struct {
		name, scope, plan string
		member            bool
		allowed           int // of the 7 actions; guards against both sides refusing everything
	}{
		{"owner with a write key on Pro", "write", "pro", false, 7},
		{"member with a write key on Pro", "write", "pro", true, 3}, // pause, resume, create; updates and maintenance are the owner's
		{"owner with a read key on Pro", "read", "pro", false, 0},
		{"owner with a write key, downgraded to Free", "write", "free", false, 0},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			e := newEnv(t, 100000)
			owner, org := e.register(t, "owner@example.com")
			e.markVerified(t, "owner@example.com")
			e.setOrgPlan(t, org, "pro")
			mon := owner.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
			inc := e.openIncident(t, mon)
			update := func() string {
				return owner.call("POST", "/api/v1/incidents/"+inc+"/updates", map[string]any{"status": "investigating", "message": "x"}).Body["update"].(map[string]any)["id"].(string)
			}
			window := func() string {
				return owner.call("POST", "/api/v1/maintenance-windows", map[string]any{"name": "W", "starts_at": time.Now().Add(time.Hour).Format(time.RFC3339),
					"duration_minutes": 30, "repeat": "none", "all_monitors": true}).Body["maintenance_window"].(map[string]any)["id"].(string)
			}
			u1, u2, w1, w2 := update(), update(), window(), window()

			keyOwner := owner
			if sc.member {
				member, _ := e.register(t, "member@example.com")
				e.markVerified(t, "member@example.com")
				mustExec(t, e, `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
				keyOwner = member
				r := member.callIn(org, "POST", "/api/v1/api-keys", map[string]any{"name": "k", "scope": sc.scope})
				if r.Status != 201 {
					t.Fatalf("%d %v", r.Status, r.Body)
				}
				if n := runParity(t, e, r.Body["token"].(string), mon, inc, u1, u2, w1, w2); n != sc.allowed {
					t.Fatalf("%d actions allowed, want %d", n, sc.allowed)
				}
				return
			}
			_, token := newKey(t, keyOwner, map[string]any{"name": "k", "scope": sc.scope})
			e.setOrgPlan(t, org, sc.plan)
			if n := runParity(t, e, token, mon, inc, u1, u2, w1, w2); n != sc.allowed {
				t.Fatalf("%d actions allowed, want %d", n, sc.allowed)
			}
		})
	}
}

// runParity makes each write once through REST and once through MCP with the same key, and returns how many were allowed.
func runParity(t *testing.T, e *env, token, mon, inc, u1, u2, w1, w2 string) int {
	t.Helper()
	rest := e.keyClient(t, token)
	s := e.mcpSession(t, token)
	allowed := 0
	start := time.Now().Add(2 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		tool   string
		args   map[string]any
		method string
		path   string
		body   map[string]any
	}{
		{"pause_monitors", map[string]any{"monitor_ids": []string{mon}}, "POST", "/api/v1/monitors/" + mon + "/pause", nil},
		{"resume_monitors", map[string]any{"monitor_ids": []string{mon}}, "POST", "/api/v1/monitors/" + mon + "/resume", nil},
		{"create_monitor", map[string]any{"type": "website", "name": "New", "url": "https://new.example.com"}, "POST", "/api/v1/monitors",
			map[string]any{"name": "New", "url": "https://new.example.com"}},
		{"post_incident_update", map[string]any{"incident_id": inc, "status": "identified", "message": "Found it"}, "POST", "/api/v1/incidents/" + inc + "/updates",
			map[string]any{"status": "identified", "message": "Found it"}},
		{"delete_incident_update", map[string]any{"incident_id": inc, "update_id": u1}, "DELETE", "/api/v1/incidents/" + inc + "/updates/" + u2, nil},
		{"schedule_maintenance", map[string]any{"name": "Deploy", "starts_at": start, "duration_minutes": 30}, "POST", "/api/v1/maintenance-windows",
			map[string]any{"name": "Deploy", "starts_at": start, "duration_minutes": 30, "repeat": "none", "all_monitors": true}},
		{"delete_maintenance_window", map[string]any{"window_id": w1}, "DELETE", "/api/v1/maintenance-windows/" + w2, nil},
	}
	for _, c := range cases {
		r := rest.call(c.method, c.path, c.body)
		restAllowed := r.Status >= 200 && r.Status < 300
		m := callTool(t, s, c.tool, c.args)
		if restAllowed == m.IsError {
			t.Errorf("%s: REST %s %s answered %d (%s), but the tool's error flag was %v (%s)", c.tool, c.method, c.path, r.Status, errCode(r), m.IsError, m.Text)
		}
		if m.IsError && strings.Contains(m.Text, "Something went wrong") {
			t.Errorf("%s: a refusal should explain itself: %s", c.tool, m.Text)
		}
		if restAllowed && !m.IsError {
			allowed++
		}
	}
	return allowed
}

func TestMCPEndpointAuthentication(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	key, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "read"})

	post := func(headers map[string]string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(res.Body).Decode(&m)
		return res, m
	}
	code := func(m map[string]any) string {
		if e, ok := m["error"].(map[string]any); ok {
			s, _ := e["code"].(string)
			return s
		}
		return ""
	}

	if res, m := post(nil); res.StatusCode != 401 || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Bearer") || code(m) != "UNAUTHENTICATED" ||
		!strings.Contains(m["error"].(map[string]any)["message"].(string), "MCP server for Claude") {
		t.Fatalf("no key: %d %v", res.StatusCode, m)
	}
	if res, m := post(map[string]string{"Authorization": "Bearer hk_" + strings.Repeat("A", 43)}); res.StatusCode != 401 ||
		!strings.Contains(m["error"].(map[string]any)["message"].(string), "not valid") {
		t.Fatalf("unknown key: %d %v", res.StatusCode, m)
	}
	// A session cookie is not enough: /mcp takes keys only.
	sess, _ := http.NewRequest("POST", e.srv.URL+"/mcp", strings.NewReader(`{}`))
	if res, err := owner.http.Do(sess); err != nil || res.StatusCode != 401 {
		t.Fatalf("a session cookie alone: %v %v", res.StatusCode, err)
	}
	// Browser origins other than Claude's are refused; Claude's and no origin (a desktop or CLI client) are fine.
	auth := "Bearer " + token
	if res, m := post(map[string]string{"Authorization": auth, "Origin": "https://evil.example"}); res.StatusCode != 403 || code(m) != "ORIGIN_NOT_ALLOWED" {
		t.Fatalf("other origin: %d %v", res.StatusCode, m)
	}
	for _, origin := range []string{"https://claude.ai", ""} {
		h := map[string]string{"Authorization": auth}
		if origin != "" {
			h["Origin"] = origin
		}
		if res, _ := post(h); res.StatusCode != 200 {
			t.Fatalf("origin %q: %d", origin, res.StatusCode)
		}
	}
	// Stateless: no standalone stream.
	req, _ := http.NewRequest("GET", e.srv.URL+"/mcp", nil)
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "text/event-stream")
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 405 {
		t.Fatalf("GET: %v %v", res.StatusCode, err)
	}
	// Expired keys say so.
	mustExec(t, e, `UPDATE api_tokens SET created_at = now() - interval '2 days', expires_at = now() - interval '1 minute' WHERE id = $1`, key["id"])
	if res, m := post(map[string]string{"Authorization": auth}); res.StatusCode != 401 || code(m) != "TOKEN_EXPIRED" {
		t.Fatalf("expired: %d %v", res.StatusCode, m)
	}

	off := newEnvOpts(t, 1000, envOpts{noAPIKeys: true})
	req, _ = http.NewRequest("POST", off.srv.URL+"/mcp", strings.NewReader(`{}`))
	req.Header.Set("Authorization", auth)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 503 {
		t.Fatalf("keys turned off: %v %v", res.StatusCode, err)
	}
}

func TestMCPWritesShareTheKeysWriteBudgetWithTheAPI(t *testing.T) {
	e := newEnvOpts(t, 100000, envOpts{shared: true})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "write"})
	s := e.mcpSession(t, token)
	for i := 0; i < mcpserver.WritesPerMinute; i++ {
		mustOK(t, callTool(t, s, "pause_monitors", map[string]any{"monitor_ids": []string{mon}}), "pause")
	}
	if r := callTool(t, s, "pause_monitors", map[string]any{"monitor_ids": []string{mon}}); !r.IsError || !strings.Contains(r.Text, "Too many changes") {
		t.Fatalf("%+v", r)
	}
	if r := e.keyClient(t, token).call("POST", "/api/v1/monitors/"+mon+"/resume", nil); r.Status != 429 {
		t.Fatalf("the REST API shares the budget: %d", r.Status)
	}
	// Reads are not limited by the write budget.
	mustOK(t, callTool(t, s, "list_monitors", nil), "list")
}
