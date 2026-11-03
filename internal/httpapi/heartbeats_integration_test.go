package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// rawPing requests a ping address the way a cron job does: no cookies, no CSRF token, no JSON.
func (e *env) rawPing(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rdr)
	req.Header.Set("User-Agent", "curl/8.5.0")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestHeartbeatsOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "cron@example.com")

	r := c.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "Nightly backup", "check_interval_seconds": 86400, "grace_seconds": 1800})
	if r.Status != 201 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
	m := r.Body["monitor"].(map[string]any)
	hb := m["heartbeat"].(map[string]any)
	token := hb["ping_token"].(string)
	if m["type"] != "heartbeat" || m["url"] != "" || hb["grace_seconds"].(float64) != 1800 || hb["due_by"] != nil || m["current_status"] != "unknown" {
		t.Fatalf("created: %v", m)
	}
	id := m["id"].(string)
	if r := c.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "x", "url": "https://example.com"}); r.Status != 400 ||
		errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("heartbeat with a url: %d %v", r.Status, r.Body)
	}

	// Every way a job pings: GET, HEAD and POST (with output), success, failure and exit statuses.
	for _, p := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/ping/" + token, "", 200},
		{"HEAD", "/ping/" + token, "", 200},
		{"POST", "/ping/" + token + "/0", "backup done: 2.1 GB", 200},
		{"POST", "/ping/" + token + "/fail", "rsync: connection refused", 200},
		{"GET", "/ping/" + token + "/2", "", 200},
		{"GET", "/ping/" + token + "/256", "", 404},
		{"GET", "/ping/" + token + "/01", "", 404},
		{"GET", "/ping/" + token + "/start", "", 404},
		{"GET", "/ping/AAAAAAAAAAAAAAAAAAAAAA", "", 404},
		{"GET", "/ping/not-a-token", "", 404},
		{"DELETE", "/ping/" + token, "", 405},
	} {
		if code, _ := e.rawPing(t, p.method, p.path, p.body); code != p.want {
			t.Errorf("%s %s: %d, want %d", p.method, p.path, code, p.want)
		}
	}
	if _, body := e.rawPing(t, "GET", "/ping/"+token, ""); body != "OK\n" {
		t.Fatalf("ping answer %q", body)
	}

	got := c.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)
	if got["current_status"] != "up" || got["heartbeat"].(map[string]any)["due_by"] == nil {
		t.Fatalf("after pings: %v", got)
	}
	pr := c.call("GET", "/api/v1/monitors/"+id+"/pings?limit=3", nil)
	pings := pr.Body["pings"].([]any)
	if pr.Status != 200 || len(pings) != 3 {
		t.Fatalf("ping log: %d %v", pr.Status, pr.Body)
	}
	fail := c.call("GET", "/api/v1/monitors/"+id+"/pings", nil).Body["pings"].([]any)[2].(map[string]any)
	if fail["kind"] != "fail" || fail["body"] != "rsync: connection refused" || fail["method"] != "POST" || fail["user_agent"] != "curl/8.5.0" {
		t.Fatalf("logged failure: %v", fail)
	}
	inc := c.call("GET", "/api/v1/monitors/"+id+"/incidents", nil)
	// /fail opened one incident (exit status 2 came while it was open) and the next successful ping resolved it.
	if incs := inc.Body["incidents"].([]any); inc.Status != 200 || len(incs) != 1 || incs[0].(map[string]any)["status"] != "resolved" ||
		incs[0].(map[string]any)["cause_category"] != "job_failed" {
		t.Fatalf("incidents: %v", inc.Body)
	}

	// A website monitor has no ping log; another organization cannot read this one or rotate its address.
	site := c.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)["id"].(string)
	if r := c.call("GET", "/api/v1/monitors/"+site+"/pings", nil); r.Status != 404 {
		t.Fatalf("website ping log: %d", r.Status)
	}
	bob, _ := e.register(t, "bob@example.com")
	if r := bob.call("GET", "/api/v1/monitors/"+id+"/pings", nil); r.Status != 404 {
		t.Fatalf("foreign ping log: %d", r.Status)
	}
	if r := bob.call("POST", "/api/v1/monitors/"+id+"/ping-token", nil); r.Status != 404 {
		t.Fatalf("foreign rotate: %d", r.Status)
	}

	rot := c.call("POST", "/api/v1/monitors/"+id+"/ping-token", nil)
	newToken := rot.Body["monitor"].(map[string]any)["heartbeat"].(map[string]any)["ping_token"].(string)
	if rot.Status != 200 || newToken == token {
		t.Fatalf("rotate: %d %v", rot.Status, rot.Body)
	}
	if code, _ := e.rawPing(t, "GET", "/ping/"+token, ""); code != 404 {
		t.Fatalf("old address after rotating: %d", code)
	}
	if code, _ := e.rawPing(t, "GET", "/ping/"+newToken, ""); code != 200 {
		t.Fatalf("new address: %d", code)
	}
	// Heartbeats count toward the plan's monitor limit like any monitor (Free: 5; two are in use).
	for i := range 3 {
		if r := c.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "job " + string(rune('a'+i))}); r.Status != 201 {
			t.Fatalf("heartbeat %d: %d %v", i, r.Status, r.Body)
		}
	}
	if r := c.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "one too many"}); r.Status != 403 {
		t.Fatalf("over the monitor limit: %d %v", r.Status, r.Body)
	}
}

// One ping address cannot be hammered: about one ping a second, after a burst no schedule needs.
func TestPingRateLimit(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "flood@example.com")
	token := c.call("POST", "/api/v1/monitors", map[string]any{"type": "heartbeat", "name": "Busy"}).Body["monitor"].(map[string]any)["heartbeat"].(map[string]any)["ping_token"].(string)
	limited := 0
	for range 40 {
		if code, _ := e.rawPing(t, "GET", "/ping/"+token, ""); code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("40 pings in a burst were all accepted")
	}
}
