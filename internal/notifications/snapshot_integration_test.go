package notifications_test

import (
	"strings"
	"testing"

	"hawkeye/internal/notifications"
)

// The outage email carries the incident's snapshot; the recovery email doesn't, and neither ever carries the body excerpt.
func TestOutageAlertsCarryTheSnapshotButNeverTheExcerpt(t *testing.T) {
	e := newEnv(t, 5)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO incident_snapshots (incident_id, checked_at, region, facts, changes, body_excerpt)
		VALUES ($1, now(), 'primary', '{"status_code":503,"size_bytes":120,"server_addr":"203.0.113.7","headers":[{"name":"Server","value":"nginx"}]}',
			'[{"field":"status","before":"200","after":"503"}]', 'EXCERPT-MARKER')`, e.inc); err != nil {
		t.Fatal(err)
	}
	e.delivery(t, "owner@example.com", "outage")
	e.delivery(t, "other@example.com", "recovery")
	s := &scriptedSender{}
	d := dispatcher(e, s, notifications.DispatcherConfig{})
	for range 2 {
		if worked, err := d.ProcessOne(t.Context()); !worked || err != nil {
			t.Fatalf("worked=%v err=%v", worked, err)
		}
	}
	for _, m := range s.sent {
		if strings.Contains(m.Text+m.HTML, "EXCERPT-MARKER") {
			t.Fatalf("an alert carried the body excerpt: %s", m.Subject)
		}
		outage := strings.HasPrefix(m.Subject, "HawkEye alert")
		if got := strings.Contains(m.Text, "• Status: 200 → 503") && strings.Contains(m.Text, "203.0.113.7"); got != outage {
			t.Errorf("%q: snapshot shown %v\n%s", m.Subject, got, m.Text)
		}
	}
}
