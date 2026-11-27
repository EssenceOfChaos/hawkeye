package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/incidents"
	"hawkeye/internal/testutil"
)

// seedSnapshot gives an incident a snapshot, with a sealed excerpt when body is set.
func (e *env) seedSnapshot(t *testing.T, incidentID, body string) {
	t.Helper()
	var sealed *string
	if body != "" {
		s, err := testutil.SecretBox().Seal(body, incidents.ExcerptContext(uuid.MustParse(incidentID)))
		if err != nil {
			t.Fatal(err)
		}
		sealed = &s
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO incident_snapshots (incident_id, checked_at, region, facts, changes, body_excerpt)
		VALUES ($1, now(), 'primary', '{"status_code":502,"size_bytes":20}', '[{"field":"status","before":"200","after":"502"}]', $2)`,
		incidentID, sealed); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureFailureBodyIsOffByDefaultAndOnlyForRequests(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	m := c.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)
	if m["capture_failure_body"] != false {
		t.Fatalf("default %v", m["capture_failure_body"])
	}
	r := c.call("PATCH", "/api/v1/monitors/"+m["id"].(string), map[string]any{"capture_failure_body": true})
	if r.Status != 200 || r.Body["monitor"].(map[string]any)["capture_failure_body"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	hb := map[string]any{"name": "Nightly", "type": "heartbeat", "check_interval_seconds": 3600, "capture_failure_body": true}
	if r := c.call("POST", "/api/v1/monitors", hb); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("a heartbeat has no response to keep: %d %v", r.Status, r.Body)
	}
}

func TestSnapshotsAreTheOrganizationsAndTheBodyIsForPeopleInTheApp(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	e.seedSnapshot(t, inc, "<h1>Bad gateway</h1>")

	r := owner.call("GET", "/api/v1/incidents/"+inc+"/snapshot", nil)
	snap, _ := r.Body["snapshot"].(map[string]any)
	if r.Status != 200 || snap["has_body_excerpt"] != true || snap["facts"].(map[string]any)["status_code"] != float64(502) {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if _, leaked := snap["body"]; leaked {
		t.Fatal("the snapshot carries the body")
	}
	r = owner.call("GET", "/api/v1/incidents/"+inc+"/snapshot/body", nil)
	if r.Status != 200 || r.Body["body"].(map[string]any)["text"] != "<h1>Bad gateway</h1>" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	// An API key (and so a connected assistant) sees the snapshot, never the body.
	_, token := newKey(t, owner, map[string]any{"name": "Script", "scope": "read"})
	k := e.keyClient(t, token)
	if r := k.call("GET", "/api/v1/incidents/"+inc+"/snapshot", nil); r.Status != 200 {
		t.Fatalf("key snapshot: %d %v", r.Status, r.Body)
	}
	if r := k.call("GET", "/api/v1/incidents/"+inc+"/snapshot/body", nil); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
		t.Fatalf("key body: %d %v", r.Status, r.Body)
	}

	// Another organization's incident is not found.
	other, _ := e.register(t, "other@example.com")
	for _, p := range []string{"/snapshot", "/snapshot/body"} {
		if r := other.call("GET", "/api/v1/incidents/"+inc+p, nil); r.Status != 404 {
			t.Fatalf("%s from another organization: %d %v", p, r.Status, r.Body)
		}
	}
}

func TestNoSnapshotOrNoExcerptIsNotFound(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	if r := owner.call("GET", "/api/v1/incidents/"+inc+"/snapshot", nil); r.Status != 404 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	e.seedSnapshot(t, inc, "")
	if r := owner.call("GET", "/api/v1/incidents/"+inc+"/snapshot", nil); r.Status != 200 || r.Body["snapshot"].(map[string]any)["has_body_excerpt"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := owner.call("GET", "/api/v1/incidents/"+inc+"/snapshot/body", nil); r.Status != 404 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

// Assistants get the snapshot from get_incident, never the response body.
func TestMCPGetIncidentHasTheSnapshotButNotTheBody(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	e.seedSnapshot(t, inc, "EXCERPT-MARKER")
	_, token := newKey(t, owner, map[string]any{"name": "Claude", "scope": "read"})
	r := callTool(t, e.mcpSession(t, token), "get_incident", map[string]any{"incident_id": inc})
	if strings.Contains(r.Text, "EXCERPT-MARKER") {
		t.Fatal("get_incident returned the response body")
	}
	snap, _ := mustOK(t, r, "incident")["failure_snapshot"].(map[string]any)
	if snap == nil || snap["response"].(map[string]any)["status_code"] != float64(502) || snap["changes_since_last_good_check"].([]any)[0] != "Status: 200 → 502" {
		t.Fatalf("%s", r.Text)
	}
}
