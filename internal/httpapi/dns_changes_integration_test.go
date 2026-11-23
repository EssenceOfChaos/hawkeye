package httpapi_test

import (
	"testing"
)

func TestDNSChangesCanBeListedAndAccepted(t *testing.T) {
	e := newEnv(t, 100000)
	owner, org := e.register(t, "owner@example.com")
	other, _ := e.register(t, "other@example.com")
	var id, changeID string
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, kind, dns_record_type)
		VALUES ($1, 'Mail', 'example.com', 300, 'dns', 'MX') RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO dns_baselines (monitor_id, region, answers, last, changed, updated_at)
		VALUES ($1, 'primary', '{mail.example.com}', '{mx.other.net}', true, now())`, id); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO dns_changes (monitor_id, record_type, before, after, detected_at)
		VALUES ($1, 'MX', '{mail.example.com}', '{mx.other.net}', now()) RETURNING id`, id).Scan(&changeID); err != nil {
		t.Fatal(err)
	}

	r := owner.call("GET", "/api/v1/monitors/"+id, nil)
	if dns := r.Body["monitor"].(map[string]any)["dns"].(map[string]any); dns["changed_since"] == nil || dns["change_alerts"] != true {
		t.Fatalf("monitor dns %v", dns)
	}
	r = owner.call("GET", "/api/v1/monitors/"+id+"/dns-changes", nil)
	list := r.Body["dns_changes"].([]any)
	if r.Status != 200 || len(list) != 1 || list[0].(map[string]any)["summary"] != "mail.example.com → mx.other.net" ||
		r.Body["current"].([]any)[0] != "mx.other.net" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if r := other.call("GET", "/api/v1/monitors/"+id+"/dns-changes", nil); r.Status != 404 {
		t.Fatalf("another organization: %d", r.Status)
	}
	if r := other.call("POST", "/api/v1/monitors/"+id+"/dns-changes/"+changeID+"/accept", nil); r.Status != 404 {
		t.Fatalf("another organization accepting: %d", r.Status)
	}

	r = owner.call("POST", "/api/v1/monitors/"+id+"/dns-changes/"+changeID+"/accept", nil)
	if r.Status != 200 || r.Body["dns_changes"].([]any)[0].(map[string]any)["status"] != "accepted" {
		t.Fatalf("accept: %d %v", r.Status, r.Body)
	}
	var baseline []string
	e.pool.QueryRow(t.Context(), `SELECT answers FROM dns_baselines WHERE monitor_id = $1`, id).Scan(&baseline)
	if len(baseline) != 1 || baseline[0] != "mx.other.net" {
		t.Fatalf("baseline %v", baseline)
	}
	if r := owner.call("POST", "/api/v1/monitors/"+id+"/dns-changes/"+changeID+"/accept", nil); r.Status != 404 {
		t.Fatalf("accepting twice: %d", r.Status)
	}
	var logged int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = 'monitor.dns_change_accepted'`).Scan(&logged)
	if logged != 1 {
		t.Fatalf("%d security log entries", logged)
	}
	// Turning change alerts off drops the baseline.
	if r := owner.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"dns_change_alerts": false}); r.Status != 200 {
		t.Fatalf("turn off: %d %v", r.Status, r.Body)
	}
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM dns_baselines WHERE monitor_id = $1`, id).Scan(&n)
	if n != 0 {
		t.Fatal("the baseline is deleted with change alerts off")
	}
	if r := owner.call("PATCH", "/api/v1/monitors/"+id, map[string]any{"dns_change_strict": true}); r.Status != 400 {
		t.Fatalf("strict on an MX monitor: %d", r.Status)
	}
}
