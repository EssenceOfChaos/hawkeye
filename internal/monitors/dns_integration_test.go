package monitors_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/domains"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

func dnsIn(host string) monitors.CreateInput {
	return monitors.CreateInput{Type: monitors.TypeDNS, Name: "DNS", URL: host}
}

func TestCreateDNSMonitor(t *testing.T) {
	svc, pool, org := setup(t, "free", "active") // DNS monitors are on every plan
	in := dnsIn(" https://Example.com/path ")
	in.DNSRecordType, in.DNSExpected = ptr("mx"), ptr("Mail.Example.com.")
	m, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != monitors.TypeDNS || m.URL != "example.com" || m.DNS == nil || m.DNS.RecordType != "MX" || m.DNS.Expected == nil || *m.DNS.Expected != "mail.example.com" {
		t.Fatalf("unexpected monitor: %+v, dns %+v", m, m.DNS)
	}
	if m.CheckIntervalSeconds != 300 || m.Heartbeat != nil {
		t.Fatalf("unexpected defaults: %+v", m)
	}
	var due bool
	if err := pool.QueryRow(t.Context(), `SELECT next_check_at <= now() FROM monitors WHERE id = $1`, m.ID).Scan(&due); err != nil || !due {
		t.Fatalf("a new DNS monitor must be due at once: %v, %v", due, err)
	}

	bare, err := svc.Create(t.Context(), org, dnsIn("_dmarc.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if bare.URL != "_dmarc.example.com" || bare.DNS.RecordType != "A" || bare.DNS.Expected != nil {
		t.Fatalf("unexpected defaults: %+v, dns %+v", bare, bare.DNS)
	}
}

func TestDNSMonitorValidation(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	cases := []struct {
		name  string
		edit  func(*monitors.CreateInput)
		field string
	}{
		{"ip address", func(in *monitors.CreateInput) { in.URL = "8.8.8.8" }, "url"},
		{"single label", func(in *monitors.CreateInput) { in.URL = "db" }, "url"},
		{"private suffix", func(in *monitors.CreateInput) { in.URL = "db.internal" }, "url"},
		{"container network", func(in *monitors.CreateInput) { in.URL = "db.hawkeye_default" }, "url"},
		{"unknown record", func(in *monitors.CreateInput) { in.DNSRecordType = ptr("CNAME") }, "dns_record_type"},
		{"wrong address family", func(in *monitors.CreateInput) { in.DNSExpected = ptr("2606:2800:220:1::1") }, "dns_expected"},
		{"private expected address", func(in *monitors.CreateInput) { in.DNSExpected = ptr("10.0.0.1") }, "dns_expected"},
		{"slow alerts", func(in *monitors.CreateInput) { in.SlowThresholdMs = ptr(500) }, "slow_threshold_ms"},
		{"request fields", func(in *monitors.CreateInput) { in.ExpectedStatusCode = ptr(200) }, "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := dnsIn("example.com")
			tc.edit(&in)
			_, err := svc.Create(t.Context(), org, in)
			var ve *validation.Error
			if !errors.As(err, &ve) || ve.Fields[tc.field] == "" {
				t.Fatalf("want a %s error, got %v", tc.field, err)
			}
		})
	}
}

func TestUpdateDNSMonitor(t *testing.T) {
	svc, pool, org := setup(t, "pro", "active")
	in := dnsIn("example.com")
	in.DNSExpected = ptr("93.184.216.34")
	m, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}
	markUp := func() {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `UPDATE monitors SET current_status = 'up', consecutive_successes = 3 WHERE id = $1`, m.ID); err != nil {
			t.Fatal(err)
		}
	}

	markUp()
	got, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: ptr("Renamed")})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStatus != "up" || got.DNS.Expected == nil || *got.DNS.Expected != "93.184.216.34" {
		t.Fatalf("a rename must keep the record and the state: %+v, dns %+v", got, got.DNS)
	}

	got, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{DNSExpected: monitors.Nullable[string]{Set: true, Value: ptr("93.184.216.35")}})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStatus != "unknown" || *got.DNS.Expected != "93.184.216.35" {
		t.Fatalf("a new expected value must reset the state: %+v, dns %+v", got, got.DNS)
	}

	// The old value was an address, which means nothing for a TXT record.
	markUp()
	got, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{DNSRecordType: ptr("TXT")})
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStatus != "unknown" || got.DNS.RecordType != "TXT" || got.DNS.Expected != nil {
		t.Fatalf("a new record type must clear the expected value and reset the state: %+v, dns %+v", got, got.DNS)
	}

	got, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{DNSExpected: monitors.Nullable[string]{Set: true, Value: ptr("v=spf1")}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{DNSExpected: monitors.Nullable[string]{Set: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got.DNS.Expected != nil {
		t.Fatalf("null must clear the expected value: %+v", got.DNS)
	}

	web, err := svc.Create(t.Context(), org, newIn("Site"))
	if err != nil {
		t.Fatal(err)
	}
	var ve *validation.Error
	if _, err := svc.Update(t.Context(), org, web.ID, monitors.UpdateInput{DNSRecordType: ptr("A")}); !errors.As(err, &ve) {
		t.Fatalf("a website monitor has no DNS settings: %v", err)
	}
}

func TestResumingDNSMonitorMakesItDueImmediately(t *testing.T) {
	svc, pool, org := setup(t, "pro", "active")
	m, err := svc.Create(t.Context(), org, dnsIn("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEnabled(t.Context(), org, m.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE monitors SET next_check_at = now() + interval '1 hour' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetEnabled(t.Context(), org, m.ID, true); err != nil {
		t.Fatal(err)
	}
	var due bool
	if err := pool.QueryRow(t.Context(), `SELECT next_check_at <= now() FROM monitors WHERE id = $1`, m.ID).Scan(&due); err != nil || !due {
		t.Fatalf("due = %v, %v", due, err)
	}
}

func TestBulkIntervalChangesDNSMonitors(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	m, err := svc.Create(t.Context(), org, dnsIn("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Bulk(t.Context(), org, monitors.BulkInput{Action: monitors.BulkSetInterval, MonitorIDs: []uuid.UUID{m.ID}, CheckIntervalSeconds: ptr(600)}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), org, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CheckIntervalSeconds != 600 {
		t.Fatalf("interval = %d", got.CheckIntervalSeconds)
	}
}

// The registration of the domain behind a DNS monitor matters as much as the records in it, so it is tracked like a website's.
func TestDNSMonitorTracksItsDomain(t *testing.T) {
	_, pool, org := setup(t, "pro", "active")
	svc := monitors.NewService(pool, plans.NewService("http://app.test"), monitors.WithSecretBox(testutil.SecretBox()), monitors.WithDomainSync(domains.SyncTx))
	if _, err := svc.Create(t.Context(), org, dnsIn("www.example.co.uk")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM domains WHERE organization_id = $1 AND name = 'example.co.uk'`, org).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tracked = %d, %v", n, err)
	}
}
