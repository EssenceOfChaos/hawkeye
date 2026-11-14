package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"hawkeye/internal/incidentdraft"
)

// fakeClaude answers every Messages request with text, and records what was asked.
func fakeClaude(t *testing.T, text string) (*incidentdraft.Service, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		asked = append(asked, string(body))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": incidentdraft.Model,
			"stop_reason": "end_turn", "content": []map[string]any{{"type": "text", "text": text}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
	}))
	t.Cleanup(srv.Close)
	return incidentdraft.New("test", option.WithBaseURL(srv.URL), option.WithMaxRetries(0)), &asked
}

func TestAIDraftsArePaidOptInAndOwnerOnly(t *testing.T) {
	drafts, asked := fakeClaude(t, "We are seeing errors on the shop and are looking into it.")
	e := newEnvOpts(t, 1000, envOpts{drafts: drafts})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")

	r := owner.call("GET", "/api/v1/ai-drafts", nil)
	if r.Status != 200 || r.Body["available"] != true || r.Body["included"] != false || r.Body["enabled"] != false {
		t.Fatalf("Free: %d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-drafts", map[string]any{"enabled": true}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("Free can't turn them on: %d %v", r.Status, r.Body)
	}

	e.setOrgPlan(t, org, "pro")
	mon := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "url": "https://secret-shop.example.com/checkout"}).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	draft := func(c *client) resp {
		return c.call("POST", "/api/v1/incidents/"+inc+"/updates/draft", map[string]any{"status": "investigating"})
	}
	if r := draft(owner); r.Status != 409 || errCode(r) != "AI_DRAFTS_OFF" {
		t.Fatalf("off until the owner turns them on: %d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-drafts", map[string]any{"enabled": true}); r.Status != 200 || r.Body["enabled"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	r = draft(owner)
	if r.Status != 200 || r.Body["message"] != "We are seeing errors on the shop and are looking into it." {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "Shop") || strings.Contains((*asked)[0], "secret-shop.example.com") {
		t.Fatalf("the model gets the monitor's name, never its address: %v", *asked)
	}
	if r := owner.call("POST", "/api/v1/incidents/"+inc+"/updates/draft", map[string]any{"status": "fixed"}); r.Status != 400 {
		t.Fatalf("status: %d %v", r.Status, r.Body)
	}
	// Nothing was posted: a draft only goes back to the person.
	if r := owner.call("GET", "/api/v1/incidents/"+inc+"/updates", nil); len(r.Body["updates"].([]any)) != 0 {
		t.Fatalf("a draft must not be posted: %v", r.Body)
	}

	// A member can't draft or change the setting; neither can an API key.
	member, _ := e.register(t, "member@example.com")
	mustExec(t, e, `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
	if r := member.callIn(org, "POST", "/api/v1/incidents/"+inc+"/updates/draft", map[string]any{"status": "investigating"}); r.Status != 403 {
		t.Fatalf("member drafts: %d %v", r.Status, r.Body)
	}
	if r := member.callIn(org, "PUT", "/api/v1/ai-drafts", map[string]any{"enabled": false}); r.Status != 403 {
		t.Fatalf("member changes the setting: %d %v", r.Status, r.Body)
	}
	_, token := newKey(t, owner, map[string]any{"name": "k", "scope": "write"})
	if r := e.keyClient(t, token).call("POST", "/api/v1/incidents/"+inc+"/updates/draft", map[string]any{"status": "investigating"}); r.Status != 403 || errCode(r) != "SESSION_REQUIRED" {
		t.Fatalf("API key: %d %v", r.Status, r.Body)
	}

	// After a downgrade, drafting stops at once even though the setting is still on.
	e.setOrgPlan(t, org, "free")
	if r := draft(owner); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("after a downgrade: %d %v", r.Status, r.Body)
	}
}

func TestAIDraftsNeedAServerKey(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.setOrgPlan(t, org, "pro")
	if r := owner.call("GET", "/api/v1/ai-drafts", nil); r.Body["available"] != false {
		t.Fatalf("%v", r.Body)
	}
	if r := owner.call("PUT", "/api/v1/ai-drafts", map[string]any{"enabled": true}); r.Status != 503 || errCode(r) != "AI_DRAFTS_UNAVAILABLE" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}
