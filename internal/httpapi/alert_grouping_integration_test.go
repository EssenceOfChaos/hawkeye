package httpapi_test

import "testing"

func TestAlertGroupingIsOnByDefaultAndOwnerOnly(t *testing.T) {
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@agency.test")
	if r := owner.call("GET", "/api/v1/notification-settings", nil); r.Status != 200 || r.Body["alert_grouping"] != true {
		t.Fatalf("on by default: %d %v", r.Status, r.Body)
	}
	if r := owner.call("PUT", "/api/v1/notification-settings/grouping", map[string]any{}); r.Status != 400 {
		t.Fatalf("enabled is required: %d", r.Status)
	}
	if r := owner.call("PUT", "/api/v1/notification-settings/grouping", map[string]any{"enabled": false}); r.Status != 200 || r.Body["alert_grouping"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := owner.call("GET", "/api/v1/notification-settings", nil); r.Body["alert_grouping"] != false {
		t.Fatalf("%v", r.Body)
	}
	var logged int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = 'organization.alert_grouping_changed'`).Scan(&logged)
	if logged != 1 {
		t.Fatalf("the change is in the security log: %d", logged)
	}

	member, _ := e.register(t, "member@agency.test")
	e.verify(t, member, "member@agency.test")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = 'member@agency.test'`, org)
	if r := member.callIn(org, "PUT", "/api/v1/notification-settings/grouping", map[string]any{"enabled": true}); r.Status != 403 {
		t.Fatalf("a member can't change it: %d", r.Status)
	}
}

func TestFlapSuppressionIsAMonitorSetting(t *testing.T) {
	e := newEnv(t, 1000)
	owner, _ := e.register(t, "owner@example.com")
	m := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "url": "https://shop.example.com"}).Body["monitor"].(map[string]any)
	if m["flap_suppression"] != true || m["flapping_since"] != nil {
		t.Fatalf("on by default, not flapping: %v %v", m["flap_suppression"], m["flapping_since"])
	}
	id := m["id"].(string)
	r := owner.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"flap_suppression": false})
	if r.Status != 200 || r.Body["monitor"].(map[string]any)["flap_suppression"] != false {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	off := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Api", "url": "https://api.example.com", "flap_suppression": false})
	if off.Body["monitor"].(map[string]any)["flap_suppression"] != false {
		t.Fatalf("%v", off.Body)
	}
}
