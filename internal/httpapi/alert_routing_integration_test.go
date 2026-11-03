package httpapi_test

import (
	"testing"
)

func TestAlertRoutingOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, org := e.register(t, "owner@agency.test")
	e.verify(t, c, "owner@agency.test")
	// Registration already made the owner's own address an alert destination.
	ownID := ""
	for _, s := range c.call("GET", "/api/v1/notification-settings", nil).Body["notification_settings"].([]any) {
		if s := s.(map[string]any); s["destination"] == "owner@agency.test" {
			ownID = s["id"].(string)
		}
	}
	if ownID == "" {
		t.Fatal("the owner's own address is not a destination")
	}
	client := c.call("POST", "/api/v1/notification-settings", map[string]string{"destination": "client@acme.test"})
	clientID := client.Body["notification_setting"].(map[string]any)["id"].(string)
	if s := client.Body["notification_setting"].(map[string]any); s["monitor_scope"] != "all" || len(s["tag_ids"].([]any)) != 0 {
		t.Fatalf("new destinations get every monitor's alerts: %v", s)
	}
	acme := newTag(t, c, "Acme")
	body := monitorBody("Acme shop")
	body["tag_ids"] = []string{acme}
	c.call("POST", "/api/v1/monitors", body)
	c.call("POST", "/api/v1/monitors", monitorBody("Our blog"))

	r := c.call("PUT", "/api/v1/notification-settings/"+clientID+"/routing", map[string]any{"monitor_scope": "tags", "tag_ids": []string{acme, acme}})
	if r.Status != 200 {
		t.Fatalf("routing: %d %v", r.Status, r.Body)
	}
	if s := r.Body["notification_setting"].(map[string]any); s["monitor_scope"] != "tags" || len(s["tag_ids"].([]any)) != 1 || s["tag_ids"].([]any)[0] != acme {
		t.Fatalf("routed destination: %v", s)
	}
	for name, b := range map[string]map[string]any{
		"no tags":       {"monitor_scope": "tags", "tag_ids": []string{}},
		"unknown scope": {"monitor_scope": "some"},
		"unknown tag":   {"monitor_scope": "tags", "tag_ids": []string{"00000000-0000-0000-0000-000000000001"}},
	} {
		if r := c.call("PUT", "/api/v1/notification-settings/"+clientID+"/routing", b); r.Status != 400 {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}

	// Coverage: with the owner's address on "all", every monitor is covered.
	cov := c.call("GET", "/api/v1/notification-settings/coverage", nil)
	if cov.Status != 200 || len(cov.Body["uncovered_monitors"].([]any)) != 0 || cov.Body["fallback_available"] != true {
		t.Fatalf("coverage: %d %v", cov.Status, cov.Body)
	}
	// Scope the owner's address to Acme too: the blog is now covered by nobody, and its alerts would use the fallback (the owner's own address).
	c.call("PUT", "/api/v1/notification-settings/"+ownID+"/routing", map[string]any{"monitor_scope": "tags", "tag_ids": []string{acme}})
	cov = c.call("GET", "/api/v1/notification-settings/coverage", nil)
	unc := cov.Body["uncovered_monitors"].([]any)
	if len(unc) != 1 || unc[0].(map[string]any)["name"] != "Our blog" || cov.Body["fallback_available"] != true {
		t.Fatalf("coverage after scoping everything: %v", cov.Body)
	}
	// Back to all.
	if r := c.call("PUT", "/api/v1/notification-settings/"+ownID+"/routing", map[string]any{"monitor_scope": "all", "tag_ids": []string{acme}}); r.Status != 200 ||
		r.Body["notification_setting"].(map[string]any)["monitor_scope"] != "all" || len(r.Body["notification_setting"].(map[string]any)["tag_ids"].([]any)) != 0 {
		t.Fatalf("back to all: %v", r.Body)
	}

	// Another organization can neither change this destination's routing nor use its tags.
	bob, _ := e.register(t, "bob@other.test")
	e.verify(t, bob, "bob@other.test")
	if r := bob.call("PUT", "/api/v1/notification-settings/"+clientID+"/routing", map[string]any{"monitor_scope": "all"}); r.Status != 404 {
		t.Fatalf("foreign destination: %d", r.Status)
	}
	bobDest := bob.call("GET", "/api/v1/notification-settings", nil).Body["notification_settings"].([]any)[0].(map[string]any)["id"].(string)
	if r := bob.call("PUT", "/api/v1/notification-settings/"+bobDest+"/routing", map[string]any{"monitor_scope": "tags", "tag_ids": []string{acme}}); r.Status != 400 {
		t.Fatalf("foreign tag: %d %v", r.Status, r.Body)
	}

	// Members may read coverage but not change routing.
	member, _ := e.register(t, "member@agency.test")
	e.verify(t, member, "member@agency.test")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@agency.test'`, org)
	if r := member.callIn(org, "PUT", "/api/v1/notification-settings/"+clientID+"/routing", map[string]any{"monitor_scope": "all"}); r.Status != 403 {
		t.Fatalf("member changed routing: %d", r.Status)
	}
	if r := member.callIn(org, "GET", "/api/v1/notification-settings/coverage", nil); r.Status != 200 {
		t.Fatalf("member coverage: %d", r.Status)
	}
}

func TestMonitorAlertTimingOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "timing@example.com")

	m := c.call("POST", "/api/v1/monitors", monitorBody("Site")).Body["monitor"].(map[string]any)
	if m["alert_delay_minutes"].(float64) != 0 || m["quiet_hours"] != nil {
		t.Fatalf("defaults: %v", m)
	}
	id := m["id"].(string)

	q := map[string]any{"start": "22:00", "end": "07:00", "timezone": "America/New_York"}
	r := c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"alert_delay_minutes": 15, "quiet_hours": q})
	if r.Status != 200 {
		t.Fatalf("patch: %d %v", r.Status, r.Body)
	}
	got := r.Body["monitor"].(map[string]any)
	gq := got["quiet_hours"].(map[string]any)
	if got["alert_delay_minutes"].(float64) != 15 || gq["start"] != "22:00" || gq["end"] != "07:00" || gq["timezone"] != "America/New_York" {
		t.Fatalf("saved: %v", got)
	}
	// An unrelated change keeps them; null turns quiet hours off.
	if r := c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"name": "Renamed"}); r.Body["monitor"].(map[string]any)["quiet_hours"] == nil {
		t.Fatal("an unrelated patch cleared quiet hours")
	}
	if r := c.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"quiet_hours": nil}); r.Body["monitor"].(map[string]any)["quiet_hours"] != nil {
		t.Fatal("null did not clear quiet hours")
	}

	for name, b := range map[string]map[string]any{
		"negative delay":  {"alert_delay_minutes": -1},
		"day-long delay":  {"alert_delay_minutes": 1441},
		"bad start":       {"quiet_hours": map[string]any{"start": "25:00", "end": "07:00", "timezone": "UTC"}},
		"empty window":    {"quiet_hours": map[string]any{"start": "07:00", "end": "07:00", "timezone": "UTC"}},
		"unknown zone":    {"quiet_hours": map[string]any{"start": "22:00", "end": "07:00", "timezone": "Mars/Base"}},
		"server-local tz": {"quiet_hours": map[string]any{"start": "22:00", "end": "07:00", "timezone": "Local"}},
	} {
		if r := c.call("PATCH", "/api/v1/monitors/"+id, b); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}

	body := monitorBody("Created with timing")
	body["alert_delay_minutes"] = 5
	body["quiet_hours"] = map[string]any{"start": "12:00", "end": "13:00", "timezone": "Europe/London"}
	if r := c.call("POST", "/api/v1/monitors", body); r.Status != 201 || r.Body["monitor"].(map[string]any)["alert_delay_minutes"].(float64) != 5 {
		t.Fatalf("create: %d %v", r.Status, r.Body)
	}
}

func TestBulkAlertTimingOverHTTP(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "bulk-timing@example.com")
	ids := []string{}
	for _, n := range []string{"a", "b", "c"} {
		ids = append(ids, c.call("POST", "/api/v1/monitors", monitorBody(n)).Body["monitor"].(map[string]any)["id"].(string))
	}
	bulk := func(body map[string]any) resp {
		t.Helper()
		body["action"] = "set_alert_timing"
		return c.call("POST", "/api/v1/monitors/bulk", body)
	}
	get := func(id string) map[string]any {
		t.Helper()
		return c.call("GET", "/api/v1/monitors/"+id, nil).Body["monitor"].(map[string]any)
	}
	night := map[string]any{"start": "22:00", "end": "07:00", "timezone": "Europe/Berlin"}

	if r := bulk(map[string]any{"monitor_ids": ids[:2], "alert_delay_minutes": 10, "quiet_hours": night}); r.Status != 200 || r.Body["updated"].(float64) != 2 {
		t.Fatalf("set both: %d %v", r.Status, r.Body)
	}
	for _, id := range ids[:2] {
		m := get(id)
		if m["alert_delay_minutes"].(float64) != 10 || m["quiet_hours"].(map[string]any)["timezone"] != "Europe/Berlin" {
			t.Fatalf("after bulk: %v", m)
		}
	}
	if m := get(ids[2]); m["alert_delay_minutes"].(float64) != 0 || m["quiet_hours"] != nil {
		t.Fatalf("an unselected monitor changed: %v", m)
	}

	// Only the delay: quiet hours stay. Only quiet hours (null): the delay stays.
	if r := bulk(map[string]any{"monitor_ids": ids[:2], "alert_delay_minutes": 30}); r.Status != 200 {
		t.Fatalf("delay only: %d %v", r.Status, r.Body)
	}
	if m := get(ids[0]); m["alert_delay_minutes"].(float64) != 30 || m["quiet_hours"] == nil {
		t.Fatalf("delay only changed quiet hours: %v", m)
	}
	if r := bulk(map[string]any{"monitor_ids": ids[:1], "quiet_hours": nil}); r.Status != 200 {
		t.Fatalf("clear quiet hours: %d %v", r.Status, r.Body)
	}
	if m := get(ids[0]); m["alert_delay_minutes"].(float64) != 30 || m["quiet_hours"] != nil {
		t.Fatalf("clearing quiet hours: %v", m)
	}

	for name, b := range map[string]map[string]any{
		"nothing to set": {"monitor_ids": ids},
		"bad delay":      {"monitor_ids": ids, "alert_delay_minutes": 5000},
		"empty window":   {"monitor_ids": ids, "quiet_hours": map[string]any{"start": "07:00", "end": "07:00", "timezone": "UTC"}},
		"unknown zone":   {"monitor_ids": ids, "quiet_hours": map[string]any{"start": "22:00", "end": "07:00", "timezone": "Nowhere/Land"}},
	} {
		if r := bulk(b); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}

	// All or nothing: a foreign monitor in the list changes none of them.
	bob, _ := e.register(t, "bob-timing@example.com")
	foreign := bob.call("POST", "/api/v1/monitors", monitorBody("bob")).Body["monitor"].(map[string]any)["id"].(string)
	if r := bulk(map[string]any{"monitor_ids": []string{ids[2], foreign}, "alert_delay_minutes": 60}); r.Status != 404 {
		t.Fatalf("foreign monitor: %d", r.Status)
	}
	if get(ids[2])["alert_delay_minutes"].(float64) != 0 {
		t.Fatal("a failed bulk action changed a monitor")
	}
}
