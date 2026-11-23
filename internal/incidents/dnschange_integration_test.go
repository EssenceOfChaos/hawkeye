package incidents_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/regions"
)

// dnsMonitor turns the env's monitor into an MX DNS monitor.
func (e *env) dnsMonitor(t *testing.T, recordType string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE monitors SET kind = 'dns', url = 'example.com', dns_record_type = $2 WHERE id = $1`, e.monitor, recordType); err != nil {
		t.Fatal(err)
	}
}

// lookup records one successful lookup with these answers, one minute after the previous check, from region ("" for the primary).
func (e *env) lookup(t *testing.T, store *checks.Store, region string, answers ...string) {
	t.Helper()
	e.clock = e.clock.Add(time.Minute)
	ms := 12
	r := checks.Result{CheckedAt: e.clock, FinishedAt: e.clock, Success: true, ResponseTimeMs: &ms, Region: region, DNSAnswers: checks.NormalizeDNSAnswers(answers)}
	if _, err := store.Record(t.Context(), e.monitor, r); err != nil {
		t.Fatal(err)
	}
}

func (e *env) openChange(t *testing.T) (before, after string, ok bool) {
	t.Helper()
	var b, a []string
	err := e.pool.QueryRow(t.Context(), `SELECT before, after FROM dns_changes WHERE monitor_id = $1 AND status = 'open'`, e.monitor).Scan(&b, &a)
	if err != nil {
		return "", "", false
	}
	return strings.Join(b, ","), strings.Join(a, ","), true
}

func (e *env) alerts(t *testing.T, event string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = $1`, event)
}

func TestADNSChangeAlertsOnceAndClosesWhenItChangesBack(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.dnsMonitor(t, "MX")

	e.lookup(t, e.store, "", "mail.example.com") // the baseline, silently
	e.lookup(t, e.store, "", "mx.other.net")     // seen once: not yet
	if _, _, open := e.openChange(t); open || e.alerts(t, "dns_change") != 0 {
		t.Fatal("one odd answer must not alert")
	}
	e.lookup(t, e.store, "", "mx.other.net") // twice in a row: a change
	before, after, open := e.openChange(t)
	if !open || before != "mail.example.com" || after != "mx.other.net" || e.alerts(t, "dns_change") != 1 {
		t.Fatalf("change %q → %q open=%v, %d alerts", before, after, open, e.alerts(t, "dns_change"))
	}
	e.lookup(t, e.store, "", "mx.other.net")
	if e.alerts(t, "dns_change") != 1 {
		t.Fatal("no second alert while the change is open")
	}
	// It isn't an outage.
	if e.count(t, `SELECT count(*) FROM incidents`) != 0 {
		t.Fatal("a DNS change is not an incident")
	}
	// The record changes back before anyone accepts it: closed as reverted, with one follow-up.
	e.lookup(t, e.store, "", "mail.example.com")
	if _, _, open := e.openChange(t); open || e.alerts(t, "dns_change_closed") != 1 ||
		e.count(t, `SELECT count(*) FROM dns_changes WHERE status = 'reverted'`) != 1 {
		t.Fatal("changing back closes the change as reverted")
	}
	// The answers are never stored with the results.
	if e.count(t, `SELECT count(*) FROM check_results WHERE error_summary LIKE '%other.net%'`) != 0 {
		t.Fatal("answers leaked into the results")
	}
}

func TestAcceptingADNSChangeMakesItTheNewBaseline(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.dnsMonitor(t, "NS")
	var user uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ('freddy@example.com', 'x') RETURNING id`).Scan(&user)

	e.lookup(t, e.store, "", "ns1.example.com", "ns2.example.com")
	e.lookup(t, e.store, "", "ns1.newdns.net", "ns2.newdns.net")
	e.lookup(t, e.store, "", "ns1.newdns.net", "ns2.newdns.net")
	var id uuid.UUID
	e.pool.QueryRow(t.Context(), `SELECT id FROM dns_changes WHERE monitor_id = $1 AND status = 'open'`, e.monitor).Scan(&id)
	if err := incidents.AcceptDNSChange(t.Context(), e.pool, e.org, e.monitor, id, user); err != nil {
		t.Fatal(err)
	}
	list, err := incidents.DNSChanges(t.Context(), e.pool, e.org, e.monitor, 10)
	if err != nil || len(list) != 1 || list[0].Status != "accepted" || list[0].AcceptedBy == nil || *list[0].AcceptedBy != "freddy@example.com" ||
		list[0].Summary != "2 answers added, 2 answers removed" {
		t.Fatalf("%v %+v", err, list)
	}
	if e.alerts(t, "dns_change_closed") != 1 {
		t.Fatal("accepting tells the team (and resolves the PagerDuty warning)")
	}
	// The new answers are normal now: no change, no alert.
	e.lookup(t, e.store, "", "ns1.newdns.net", "ns2.newdns.net")
	e.lookup(t, e.store, "", "ns1.newdns.net", "ns2.newdns.net")
	if _, _, open := e.openChange(t); open || e.alerts(t, "dns_change") != 1 {
		t.Fatal("the accepted answers are the baseline")
	}
	if err := incidents.AcceptDNSChange(t.Context(), e.pool, e.org, e.monitor, id, user); err != incidents.ErrDNSChangeNotFound {
		t.Fatalf("accepting a closed change: %v", err)
	}
}

// With several locations, each keeps its own baseline (GeoDNS answers differently by place), and most must confirm a change.
func TestADNSChangeNeedsMostLocations(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.dnsMonitor(t, "A")
	set, _ := regions.Parse("us,eu,ap")
	store := checks.NewStore(e.pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{}).WithRegions(set)
	e.pool.Exec(t.Context(), `UPDATE monitors SET regions = '{us,eu,ap}', dns_change_strict = true WHERE id = $1`, e.monitor)
	e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'agency') WHERE organization_id = $1`, e.org)

	// Different, stable answers per location: never a change.
	for range 3 {
		e.lookup(t, store, "us", "203.0.113.1")
		e.lookup(t, store, "eu", "198.51.100.1")
		e.lookup(t, store, "ap", "192.0.2.1")
	}
	// One location sees a new address twice: not enough.
	e.lookup(t, store, "us", "203.0.113.99")
	e.lookup(t, store, "us", "203.0.113.99")
	if _, _, open := e.openChange(t); open {
		t.Fatal("one location of three")
	}
	// A second location confirms one too: most of them have.
	e.lookup(t, store, "eu", "203.0.113.99")
	e.lookup(t, store, "eu", "203.0.113.99")
	if _, after, open := e.openChange(t); !open || after != "203.0.113.99" {
		t.Fatalf("two of three: open=%v after=%q", open, after)
	}
}

func TestDNSChangesPauseForMaintenanceAndResetWhenTurnedOff(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "ops@example.com", true, true)
	e.dnsMonitor(t, "TXT")
	e.lookup(t, e.store, "", "v=spf1 include:_spf.example.com ~all")
	e.pool.Exec(t.Context(), `INSERT INTO maintenance_windows (organization_id, name, starts_at, duration_minutes, all_monitors) VALUES ($1, 'w', $2, 30, true)`,
		e.org, e.clock)
	e.lookup(t, e.store, "", "v=spf1 -all")
	e.lookup(t, e.store, "", "v=spf1 -all")
	if _, _, open := e.openChange(t); open {
		t.Fatal("lookups during maintenance don't count")
	}
	// Turned off: no state is kept.
	e.pool.Exec(t.Context(), `DELETE FROM maintenance_windows`)
	e.pool.Exec(t.Context(), `UPDATE monitors SET dns_change_alerts = false WHERE id = $1`, e.monitor)
	e.pool.Exec(t.Context(), `DELETE FROM dns_baselines`)
	e.lookup(t, e.store, "", "v=spf1 -all")
	if e.count(t, `SELECT count(*) FROM dns_baselines`) != 0 {
		t.Fatal("no baseline while change alerts are off")
	}
}
