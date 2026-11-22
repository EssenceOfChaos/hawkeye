package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// localMonitor adds a monitor checking url (a local test server, which the API's own validation would refuse) and returns its id.
func (e *env) localMonitor(t *testing.T, org, name, url string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds)
		VALUES ($1, $2, $3, 60, 5) RETURNING id`, org, name, url).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) tagMonitor(t *testing.T, org, monitor, tag string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `WITH t AS (INSERT INTO tags (organization_id, name) VALUES ($1, $2)
			ON CONFLICT (organization_id, lower(name)) DO UPDATE SET name = EXCLUDED.name RETURNING id)
		INSERT INTO monitor_tags (monitor_id, tag_id) SELECT $3, id FROM t`, org, tag, monitor); err != nil {
		t.Fatal(err)
	}
}

func (e *env) inMaintenance(t *testing.T, monitor string) (quiet, announced bool) {
	t.Helper()
	if err := e.pool.QueryRow(t.Context(), `SELECT in_maintenance(organization_id, id, now()), in_announced_maintenance(organization_id, id, now())
		FROM monitors WHERE id = $1`, monitor).Scan(&quiet, &announced); err != nil {
		t.Fatal(err)
	}
	return quiet, announced
}

// form sends a form-encoded POST with a key, as `curl -d tag=production` does.
func (k *keyClient) form(path string, values url.Values) resp {
	k.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, k.base+path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+k.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		k.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return resp{res.StatusCode, m}
}

func site(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
}

func TestADeployQuietsAlertsAndChecksTheSiteWhenItFinishes(t *testing.T) {
	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	healthy := site(200)
	defer healthy.Close()
	web := e.localMonitor(t, org, "Website", healthy.URL)
	other := e.localMonitor(t, org, "Blog", healthy.URL)
	e.tagMonitor(t, org, web, "production")

	key, token := newKey(t, owner, map[string]any{"name": "GitHub Actions", "scope": "deploy"})
	if key["scope"] != "deploy" {
		t.Fatalf("scope %v", key["scope"])
	}
	k := e.keyClient(t, token)

	// Started the way the docs show it: a form body, a tag and a note.
	r := k.form("/api/v1/deploys", url.Values{"tag": {"Production"}, "minutes": {"10"}, "note": {"v1.4.2"}})
	if r.Status != 201 || r.Body["extended"] != false {
		t.Fatalf("start: %d %v", r.Status, r.Body)
	}
	d := r.Body["deploy"].(map[string]any)
	if d["tag"] != "production" || d["monitors"] != float64(1) || d["note"] != "v1.4.2" || d["active"] != true {
		t.Fatalf("deploy %v", d)
	}
	if quiet, announced := e.inMaintenance(t, web); !quiet || announced {
		t.Fatalf("the tagged monitor's alerts are quiet (%v), but its status page doesn't say maintenance (%v)", quiet, announced)
	}
	if quiet, _ := e.inMaintenance(t, other); quiet {
		t.Fatal("a monitor without the tag keeps alerting")
	}
	// A retried pipeline step extends the same deploy instead of opening another.
	if r := k.form("/api/v1/deploys", url.Values{"tag": {"production"}}); r.Status != 200 || r.Body["extended"] != true ||
		r.Body["deploy"].(map[string]any)["id"] != d["id"] {
		t.Fatalf("extend: %d %v", r.Status, r.Body)
	}

	// A deploy key does nothing else, not even read, and isn't an MCP key.
	if r := k.call("GET", "/api/v1/monitors", nil); r.Status != 403 || errCode(r) != "TOKEN_SCOPE" {
		t.Fatalf("list monitors with a deploy key: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/monitors", monitorBody("x")); r.Status != 403 || errCode(r) != "TOKEN_SCOPE" {
		t.Fatalf("create a monitor with a deploy key: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); r.Status != 403 {
		t.Fatalf("MCP with a deploy key: %d %v", r.Status, r.Body)
	}

	r = k.call("POST", "/api/v1/deploys/latest/finish", nil)
	if r.Status != 200 || r.Body["passed"] != true {
		t.Fatalf("finish: %d %v", r.Status, r.Body)
	}
	checks := r.Body["checks"].([]any)
	if len(checks) != 1 || checks[0].(map[string]any)["name"] != "Website" || checks[0].(map[string]any)["passed"] != true {
		t.Fatalf("checks %v", checks)
	}
	if quiet, _ := e.inMaintenance(t, web); quiet {
		t.Fatal("finishing ends the quiet period at once")
	}
	var verified bool
	e.pool.QueryRow(t.Context(), `SELECT verified FROM maintenance_windows WHERE id = $1`, d["id"]).Scan(&verified)
	if !verified {
		t.Fatal("the deploy is marked verified")
	}
	// Finishing again (a pipeline retrying) checks again.
	if r := k.call("POST", "/api/v1/deploys/latest/finish", nil); r.Status != 200 {
		t.Fatalf("finish again: %d %v", r.Status, r.Body)
	}
	// Deploys are not scheduled windows: they don't appear there or use up their limit, and the security log has them.
	if r := owner.call("GET", "/api/v1/maintenance-windows", nil); len(r.Body["maintenance_windows"].([]any)) != 0 {
		t.Fatalf("maintenance windows %v", r.Body)
	}
	if r := owner.call("GET", "/api/v1/deploys", nil); r.Status != 200 || len(r.Body["deploys"].([]any)) != 1 {
		t.Fatalf("deploys %d %v", r.Status, r.Body)
	}
	var logged int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action IN ('deploy.started', 'deploy.finished')`).Scan(&logged)
	if logged != 4 {
		t.Fatalf("%d deploy events in the security log", logged)
	}
}

func TestAFailingSiteFailsTheFinishWithItsReason(t *testing.T) {
	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	broken := site(503)
	defer broken.Close()
	id := e.localMonitor(t, org, "Checkout", broken.URL)
	_, token := newKey(t, owner, map[string]any{"name": "CI", "scope": "deploy"})
	k := e.keyClient(t, token)

	if r := k.call("POST", "/api/v1/deploys", map[string]any{}); r.Status != 201 || r.Body["deploy"].(map[string]any)["all_monitors"] != true {
		t.Fatalf("start for every monitor: %d %v", r.Status, r.Body)
	}
	r := k.call("POST", "/api/v1/deploys/latest/finish", nil)
	if r.Status != 422 || r.Body["passed"] != false || !strings.Contains(r.Body["message"].(string), "Checkout (Received HTTP 503)") {
		t.Fatalf("finish: %d %v", r.Status, r.Body)
	}
	c := r.Body["checks"].([]any)[0].(map[string]any)
	if c["attempts"] != float64(2) || c["status_code"] != float64(503) {
		t.Fatalf("a failing monitor is tried once more: %v", c)
	}
	// Nothing was recorded: the scheduled checks, now alerting again, decide whether it's an outage.
	var results int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM check_results WHERE monitor_id = $1`, id).Scan(&results)
	if results != 0 {
		t.Fatalf("%d results recorded", results)
	}
	// verify=false only ends the quiet period.
	if r := k.call("POST", "/api/v1/deploys/latest/finish?verify=false", nil); r.Status != 200 {
		t.Fatalf("finish without a check: %d %v", r.Status, r.Body)
	}
}

func TestDeployPlansAndPermissions(t *testing.T) {
	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	healthy := site(200)
	defer healthy.Close()
	e.localMonitor(t, org, "Website", healthy.URL)
	_, token := newKey(t, owner, map[string]any{"name": "CI", "scope": "deploy"})
	k := e.keyClient(t, token)

	// Free: quieting alerts is maintenance (paid), but a check after a deploy works on every plan.
	if r := k.call("POST", "/api/v1/deploys", map[string]any{}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("start on Free: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/deploys/check", map[string]any{}); r.Status != 200 || r.Body["passed"] != true {
		t.Fatalf("check on Free: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/deploys/latest/finish", nil); r.Status != 404 {
		t.Fatalf("finish with no deploy: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/deploys/check", map[string]any{"tag": "nope"}); r.Status != 400 {
		t.Fatalf("an unknown tag: %d %v", r.Status, r.Body)
	}

	// The owner's own write key may report deploys too (decision 3); a read key may not.
	e.setOrgPlan(t, org, "pro")
	_, write := newKey(t, owner, map[string]any{"name": "Terminal", "scope": "write"})
	if r := e.keyClient(t, write).call("POST", "/api/v1/deploys", map[string]any{"minutes": 5}); r.Status != 201 {
		t.Fatalf("owner's write key: %d %v", r.Status, r.Body)
	}
	_, read := newKey(t, owner, map[string]any{"name": "Dashboard", "scope": "read"})
	if r := e.keyClient(t, read).call("POST", "/api/v1/deploys", map[string]any{}); r.Status != 403 || errCode(r) != "TOKEN_SCOPE" {
		t.Fatalf("read key: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/deploys", map[string]any{"minutes": 121}); r.Status != 400 {
		t.Fatalf("too long: %d %v", r.Status, r.Body)
	}
}

func TestOnlyTheOwnerCanReportDeploys(t *testing.T) {
	e, owner, org := teamEnv(t)
	e.setOrgPlan(t, org, "agency")
	member, _ := e.join(t, "member@example.com", e.invite(t, owner, org, "member@example.com"))
	if r := member.callIn(org, "POST", "/api/v1/api-keys", map[string]any{"name": "CI", "scope": "deploy"}); r.Status != 403 {
		t.Fatalf("a member creating a deploy key: %d %v", r.Status, r.Body)
	}
	r := member.callIn(org, "POST", "/api/v1/api-keys", map[string]any{"name": "Mine", "scope": "write"})
	if r.Status != 201 {
		t.Fatalf("member's write key: %d %v", r.Status, r.Body)
	}
	if r := e.keyClient(t, r.Body["token"].(string)).call("POST", "/api/v1/deploys", map[string]any{}); r.Status != 403 {
		t.Fatalf("a member's write key starting a deploy: %d %v", r.Status, r.Body)
	}
}
