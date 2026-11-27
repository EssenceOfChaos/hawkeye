package httpapi_test

import (
	"testing"
)

func TestAIAnalysisIsOptInAndCountedAgainstTheMonthlyAllowance(t *testing.T) {
	e := newEnvOpts(t, 1000, envOpts{analysis: true})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")

	r := owner.call("GET", "/api/v1/ai-analysis", nil)
	if r.Status != 200 || r.Body["available"] != true || r.Body["included"] != true || r.Body["enabled"] != false ||
		r.Body["limit"] != float64(25) || r.Body["used"] != float64(0) {
		t.Fatalf("Free includes 25 a month, off until turned on: %d %v", r.Status, r.Body)
	}

	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Shop")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	analyze := func() resp { return owner.call("POST", "/api/v1/incidents/"+inc+"/analysis", nil) }
	if r := analyze(); r.Status != 409 || errCode(r) != "AI_ANALYSIS_OFF" {
		t.Fatalf("off until the owner turns it on: %d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-analysis", map[string]any{"enabled": true}); r.Status != 200 || r.Body["enabled"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := analyze(); r.Status != 409 || errCode(r) != "NO_SNAPSHOT" {
		t.Fatalf("an incident without a snapshot has nothing to analyze: %d %v", r.Status, r.Body)
	}

	e.seedSnapshot(t, inc, "")
	r = analyze()
	if r.Status != 202 || r.Body["analysis"].(map[string]any)["status"] != "pending" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := analyze(); r.Status != 409 || errCode(r) != "ANALYSIS_IN_PROGRESS" {
		t.Fatalf("a second request while one waits: %d %v", r.Status, r.Body)
	}
	if r := owner.call("GET", "/api/v1/incidents/"+inc+"/analysis", nil); r.Status != 200 || r.Body["analysis"].(map[string]any)["status"] != "pending" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	list := owner.call("GET", "/api/v1/incidents", nil).Body["incidents"].([]any)
	if list[0].(map[string]any)["has_analysis"] != true {
		t.Fatalf("has_analysis: %v", list[0])
	}

	// An API key (and so a connected assistant) can read the analysis, but asking for one is for people in the app.
	_, token := newKey(t, owner, map[string]any{"name": "Script", "scope": "write"})
	k := e.keyClient(t, token)
	if r := k.call("GET", "/api/v1/incidents/"+inc+"/analysis", nil); r.Status != 200 {
		t.Fatalf("key read: %d %v", r.Status, r.Body)
	}
	if r := k.call("POST", "/api/v1/incidents/"+inc+"/analysis", nil); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
		t.Fatalf("key request: %d %v", r.Status, r.Body)
	}
	other, _ := e.register(t, "other@example.com")
	if r := other.call("GET", "/api/v1/incidents/"+inc+"/analysis", nil); r.Status != 404 {
		t.Fatalf("another organization: %d %v", r.Status, r.Body)
	}

	// With the month's analyses used up, a new one is refused at once, naming the plan.
	if _, err := e.pool.Exec(t.Context(), `UPDATE incident_analyses SET status = 'done', likely_cause = 'x' WHERE incident_id = $1`, inc); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO ai_analysis_usage (organization_id, incident_id, requested_at)
		SELECT $1, gen_random_uuid(), now() FROM generate_series(1, 25)`, org); err != nil {
		t.Fatal(err)
	}
	if r := owner.call("GET", "/api/v1/ai-analysis", nil); r.Body["used"] != float64(25) {
		t.Fatalf("used: %v", r.Body)
	}
	if r := analyze(); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("allowance used up: %d %v", r.Status, r.Body)
	}
	// Last month's analyses don't count.
	if _, err := e.pool.Exec(t.Context(), `UPDATE ai_analysis_usage SET created_at = now() - interval '40 days' WHERE organization_id = $1`, org); err != nil {
		t.Fatal(err)
	}
	if r := analyze(); r.Status != 202 {
		t.Fatalf("a new month: %d %v", r.Status, r.Body)
	}
}

func TestAIAnalysisCannotBeTurnedOnWithoutAModel(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	if r := owner.call("GET", "/api/v1/ai-analysis", nil); r.Status != 200 || r.Body["available"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-analysis", map[string]any{"enabled": true}); r.Status != 503 || errCode(r) != "AI_ANALYSIS_UNAVAILABLE" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-analysis", map[string]any{"enabled": false}); r.Status != 200 {
		t.Fatalf("turning it off always works: %d %v", r.Status, r.Body)
	}
}
