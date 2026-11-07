package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// openIncident starts an incident on a monitor directly in the database, as the worker would, and returns its id.
func (e *env) openIncident(t *testing.T, monitorID string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO incidents (monitor_id, started_at) VALUES ($1, now() - interval '10 minutes') RETURNING id::text`, monitorID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func publicIncidents(t *testing.T, anon *client, slug string) []any {
	t.Helper()
	r := anon.do("GET", "/api/v1/public/status/"+slug, nil, false)
	if r.Status != 200 {
		t.Fatalf("public page: %d %v", r.Status, r.Body)
	}
	return r.Body["incidents"].([]any)
}

func TestIncidentUpdatesAppearOnThePublicStatusPage(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Website")).Body["monitor"].(map[string]any)["id"].(string)
	page := map[string]any{"slug": "acme", "title": "Acme status", "published": true, "monitors": []map[string]any{{"monitor_id": mon}}}
	if r := owner.call("POST", "/api/v1/status-pages", page); r.Status != 201 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	inc := e.openIncident(t, mon)
	path := "/api/v1/incidents/" + inc + "/updates"
	anon := e.newClient(t)
	if got := publicIncidents(t, anon, "acme")[0].(map[string]any)["updates"].([]any); len(got) != 0 {
		t.Fatalf("no updates yet, as an empty list: %v", got)
	}

	// The team sees who will read an update before posting it.
	r := owner.call("GET", path, nil)
	if pages := r.Body["status_pages"].([]any); r.Status != 200 || len(pages) != 1 || pages[0].(map[string]any)["slug"] != "acme" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	// Line breaks are normalized; markup is stored as the text it is (the page shows it as text).
	posted := owner.call("POST", path, map[string]any{"status": "investigating", "message": "  We are looking into it.\r\n<b>Not bold</b>  "})
	if posted.Status != 201 {
		t.Fatalf("%d %v", posted.Status, posted.Body)
	}
	u := posted.Body["update"].(map[string]any)
	if u["message"] != "We are looking into it.\n<b>Not bold</b>" || u["status"] != "investigating" || u["author_email"] != "owner@example.com" {
		t.Fatalf("%v", u)
	}
	owner.call("POST", path, map[string]any{"status": "monitoring", "message": "A fix is out."})

	// Public at once (the page's cache is cleared), newest first, and without the author.
	pub := publicIncidents(t, anon, "acme")[0].(map[string]any)["updates"].([]any)
	if len(pub) != 2 || pub[0].(map[string]any)["status"] != "monitoring" || pub[1].(map[string]any)["message"] != "We are looking into it.\n<b>Not bold</b>" {
		t.Fatalf("%v", pub)
	}
	raw := anon.do("GET", "/api/v1/public/status/acme", nil, false)
	b, _ := json.Marshal(raw.Body)
	if strings.Contains(string(b), "owner@example.com") || strings.Contains(string(b), "author") {
		t.Fatalf("the public page must not say who posted: %s", b)
	}

	// The incident list shows how many there are.
	list := owner.call("GET", "/api/v1/incidents", nil).Body["incidents"].([]any)
	if list[0].(map[string]any)["update_count"] != float64(2) {
		t.Fatalf("%v", list[0])
	}

	// Deleting one takes it off the public page at once.
	id := u["id"].(string)
	if r := owner.call("DELETE", path+"/"+id, nil); r.Status != 204 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if got := publicIncidents(t, anon, "acme")[0].(map[string]any)["updates"].([]any); len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if r := owner.call("DELETE", path+"/"+id, nil); r.Status != 404 {
		t.Fatalf("deleting twice: %d", r.Status)
	}
	_ = org
}

func TestOnlyOwnersPostIncidentUpdates(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Website")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	path := "/api/v1/incidents/" + inc + "/updates"
	posted := owner.call("POST", path, map[string]any{"status": "identified", "message": "Found it."}).Body["update"].(map[string]any)["id"].(string)

	member, _ := e.register(t, "member@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
	if r := member.callIn(org, "GET", path, nil); r.Status != 200 || len(r.Body["updates"].([]any)) != 1 {
		t.Fatalf("members can read them: %d %v", r.Status, r.Body)
	}
	if r := member.callIn(org, "POST", path, map[string]any{"status": "resolved", "message": "Done"}); r.Status != 403 {
		t.Fatalf("a member cannot post to the public page: %d", r.Status)
	}
	if r := member.callIn(org, "DELETE", path+"/"+posted, nil); r.Status != 403 {
		t.Fatalf("or delete: %d", r.Status)
	}

	// Another organization cannot see or touch the incident at all.
	stranger, _ := e.register(t, "stranger@example.com")
	for _, c := range []struct{ method, path string }{{"GET", path}, {"POST", path}, {"DELETE", path + "/" + posted}} {
		if r := stranger.call(c.method, c.path, map[string]any{"status": "resolved", "message": "x"}); r.Status != 404 {
			t.Fatalf("%s %s by another organization: %d", c.method, c.path, r.Status)
		}
	}
	for _, bad := range []string{"/api/v1/incidents/not-a-uuid/updates", "/api/v1/incidents/00000000-0000-0000-0000-000000000000/updates"} {
		if r := owner.call("GET", bad, nil); r.Status != 404 {
			t.Fatalf("%s: %d", bad, r.Status)
		}
	}
}

func TestIncidentUpdateRules(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Website")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	path := "/api/v1/incidents/" + inc + "/updates"

	for name, body := range map[string]map[string]any{
		"unknown status":  {"status": "fixed", "message": "Done"},
		"empty":           {"status": "resolved", "message": "   "},
		"too long":        {"status": "resolved", "message": strings.Repeat("a", 1001)},
		"control char":    {"status": "resolved", "message": "ok\x07"},
		"direction trick": {"status": "resolved", "message": "safe\u202Eexe.txt"},
	} {
		if r := owner.call("POST", path, body); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Fatalf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	// 1,000 characters is the limit, counted as characters, not bytes.
	if r := owner.call("POST", path, map[string]any{"status": "resolved", "message": strings.Repeat("é", 1000)}); r.Status != 201 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	// No page lists this monitor yet: the team is told nobody outside sees it.
	if pages := owner.call("GET", path, nil).Body["status_pages"].([]any); len(pages) != 0 {
		t.Fatalf("%v", pages)
	}
	// A draft page does not count either.
	owner.call("POST", "/api/v1/status-pages", map[string]any{"slug": "draft", "title": "Draft", "published": false, "monitors": []map[string]any{{"monitor_id": mon}}})
	if pages := owner.call("GET", path, nil).Body["status_pages"].([]any); len(pages) != 0 {
		t.Fatalf("a draft is not public: %v", pages)
	}

	// At most 50 per incident.
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO incident_updates (incident_id, status, message) SELECT $1, 'monitoring', 'note' FROM generate_series(1, 49)`, inc); err != nil {
		t.Fatal(err)
	}
	if r := owner.call("POST", path, map[string]any{"status": "resolved", "message": "One more"}); r.Status != 400 || !strings.Contains(r.Body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)["message"].(string), "50") {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
}

func TestIncidentUpdatesAreInTheAccountExport(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Website")).Body["monitor"].(map[string]any)["id"].(string)
	inc := e.openIncident(t, mon)
	owner.call("POST", "/api/v1/incidents/"+inc+"/updates", map[string]any{"status": "identified", "message": "The database ran out of disk."})

	req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/account/export", nil)
	for _, ck := range owner.http.Jar.Cookies(req.URL) {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(raw), "The database ran out of disk.") {
		t.Fatalf("%d %s", res.StatusCode, raw)
	}
}
