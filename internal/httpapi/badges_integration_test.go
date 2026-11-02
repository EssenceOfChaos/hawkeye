package httpapi_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// badge fetches a public badge as an image proxy would: no session, no cookies.
func (e *env) badge(t *testing.T, path string) (int, http.Header, string) {
	t.Helper()
	res, err := http.Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(b)
}

func TestUptimeBadges(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	mon := owner.call("POST", "/api/v1/monitors", monitorBody("Website")).Body["monitor"].(map[string]any)
	id := mon["id"].(string)
	if mon["badge_token"] != nil {
		t.Fatal("a badge is off until the owner turns it on")
	}

	// A member may not publish a monitor's uptime; another organization cannot reach the monitor at all.
	member, _ := e.register(t, "member@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@example.com'`, org)
	if r := member.callIn(org, "POST", "/api/v1/monitors/"+id+"/badge", nil); r.Status != 403 {
		t.Fatalf("member: %d", r.Status)
	}
	stranger, _ := e.register(t, "stranger@example.com")
	if r := stranger.call("POST", "/api/v1/monitors/"+id+"/badge", nil); r.Status != 404 {
		t.Fatalf("stranger: %d", r.Status)
	}

	r := owner.call("POST", "/api/v1/monitors/"+id+"/badge", nil)
	token, _ := r.Body["badge_token"].(string)
	if r.Status != 200 || len(token) != 22 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if again := owner.call("POST", "/api/v1/monitors/"+id+"/badge", nil); again.Body["badge_token"] != token {
		t.Fatal("turning it on again keeps the address, so embedded badges keep working")
	}
	if got := owner.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)["badge_token"]; got != token {
		t.Fatalf("the monitor shows its badge token: %v", got)
	}

	// No checks yet: the unknown badge, with no number.
	status, hdr, svg := e.badge(t, "/badge/"+token+".svg")
	if status != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "image/svg+xml") || hdr.Get("X-Content-Type-Options") != "nosniff" ||
		hdr.Get("Cache-Control") != "public, max-age=300" || !strings.Contains(svg, "Not enough data yet") {
		t.Fatalf("%d %v\n%s", status, hdr, svg)
	}
	if strings.Contains(svg, "Website") || strings.Contains(svg, "example.com") {
		t.Fatal("a badge never shows the monitor's name or address")
	}

	// 99 good checks and 1 failure, monitor up: the Free plan's badge covers 7 days.
	e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success)
		SELECT $1, now() - g * interval '1 minute', now() - g * interval '1 minute', g <> 5 FROM generate_series(1, 100) g`, id)
	e.pool.Exec(t.Context(), `UPDATE monitors SET current_status = 'up' WHERE id = $1`, id)
	// The previous answer is cached for a minute; a fresh service would see the new data, so read through a new token.
	owner.call("DELETE", "/api/v1/monitors/"+id+"/badge", nil)
	token = owner.call("POST", "/api/v1/monitors/"+id+"/badge", nil).Body["badge_token"].(string)
	_, _, svg = e.badge(t, "/badge/"+token+".svg")
	if !strings.Contains(svg, "<tspan>99.00%</tspan>") || !strings.Contains(svg, "Last 7 days") || !strings.Contains(svg, "#29D68D") {
		t.Fatalf("%s", svg)
	}
	_, _, small := e.badge(t, "/badge/"+token+".svg?style=small")
	if !strings.Contains(small, `width="200" height="28"`) || !strings.Contains(small, ">7d<") {
		t.Fatalf("%s", small)
	}

	// Turned off: gone at once. Turned on again: a new address; the old one stays dead.
	if r := owner.call("DELETE", "/api/v1/monitors/"+id+"/badge", nil); r.Status != 204 {
		t.Fatalf("%d", r.Status)
	}
	if st, _, _ := e.badge(t, "/badge/"+token+".svg"); st != 404 {
		t.Fatalf("a badge that was turned off: %d", st)
	}
	fresh := owner.call("POST", "/api/v1/monitors/"+id+"/badge", nil).Body["badge_token"].(string)
	if fresh == token {
		t.Fatal("turning it back on issues a new address")
	}
	owner.call("DELETE", "/api/v1/monitors/"+id, nil)
	for _, path := range []string{"/badge/" + fresh + ".svg", "/badge/not-a-token.svg", "/badge/" + fresh + ".png", "/badge/..%2F..%2Fetc.svg"} {
		if st, _, _ := e.badge(t, path); st != 404 {
			t.Errorf("%s: %d", path, st)
		}
	}
}
