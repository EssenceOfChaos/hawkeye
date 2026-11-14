package notifications

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/validation"
)

const pdKey = "R0123456789abcdefABCDEF012345678"

func TestPagerDutyURL(t *testing.T) {
	u, label, err := PagerDutyURL(" "+pdKey+" ", "")
	if err != nil || u.String() != "https://events.pagerduty.com/v2/enqueue?routing_key="+pdKey || label != "US service, key ending 5678" {
		t.Fatalf("%v %q %v", u, label, err)
	}
	if u, _, _ := PagerDutyURL(pdKey, "EU"); u.Host != "events.eu.pagerduty.com" {
		t.Fatalf("EU accounts use the EU host: %v", u)
	}
	for _, tc := range []struct{ key, region, field string }{
		{"", "us", "destination"},
		{"short", "us", "destination"},
		{strings.Repeat("a", 31) + "!", "us", "destination"},
		{pdKey, "ap", "region"},
	} {
		var ve *validation.Error
		if _, _, err := PagerDutyURL(tc.key, tc.region); !errors.As(err, &ve) || ve.Fields[tc.field] == "" {
			t.Errorf("%q %q: %v", tc.key, tc.region, err)
		}
	}
}

type pdEvent struct {
	RoutingKey  string `json:"routing_key"`
	EventAction string `json:"event_action"`
	DedupKey    string `json:"dedup_key"`
	Payload     *struct {
		Summary, Source, Severity, Class, Component, Timestamp string
		CustomDetails                                          map[string]string `json:"custom_details"`
	}
	Links []struct{ Href, Text string }
}

func pdBuild(t *testing.T, a Alert) (string, pdEvent) {
	t.Helper()
	to, body, h, err := buildRequest(ChannelPagerDuty, "https://events.pagerduty.com/v2/enqueue?routing_key="+pdKey, a, "", "d1", time.Now())
	if err != nil || h[signatureHeader] != "" {
		t.Fatal(err, h)
	}
	var ev pdEvent
	mustUnmarshal(t, body, &ev)
	return to, ev
}

func TestPagerDutyOutageAndRecoveryShareADedupKey(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	resolved := start.Add(20 * time.Minute)
	outage := Alert{Event: EventOutage, MonitorID: "m1", MonitorName: "Shop", MonitorURL: "https://shop.example.com/cart", Cause: "Received HTTP 503",
		StartedAt: start, AppBaseURL: "https://app"}
	to, trig := pdBuild(t, outage)
	if to != "https://events.pagerduty.com/v2/enqueue" {
		t.Fatalf("the key belongs in the body, not the URL: %s", to)
	}
	if trig.RoutingKey != pdKey || trig.EventAction != "trigger" || trig.Payload == nil || trig.Payload.Severity != "critical" ||
		trig.Payload.Summary != "Shop may be offline" || trig.Payload.Source != "shop.example.com" || trig.Payload.Component != "Shop" ||
		trig.Payload.Timestamp != "2026-11-13T09:00:00Z" || !strings.Contains(trig.Payload.CustomDetails["details"], "Received HTTP 503") ||
		len(trig.Links) != 1 || trig.Links[0].Href != "https://app/incidents" {
		t.Fatalf("trigger: %+v %+v", trig, trig.Payload)
	}

	recovery := outage
	recovery.Event, recovery.ResolvedAt = EventRecovery, &resolved
	_, res := pdBuild(t, recovery)
	if res.EventAction != "resolve" || res.DedupKey != trig.DedupKey || res.Payload != nil || res.RoutingKey != pdKey {
		t.Fatalf("the recovery must resolve the same alert, and carry nothing else: %+v (trigger %s)", res, trig.DedupKey)
	}

	another := outage
	another.StartedAt = start.Add(time.Hour)
	if _, ev := pdBuild(t, another); ev.DedupKey == trig.DedupKey {
		t.Fatal("a later outage of the same monitor is a new PagerDuty alert")
	}
}

func TestPagerDutyWarningsAndSlowdowns(t *testing.T) {
	start := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	_, slow := pdBuild(t, Alert{Event: EventSlow, MonitorID: "m1", MonitorName: "Shop", StartedAt: start, SlowThresholdMs: 800, SlowPeakMs: 2400, AppBaseURL: "https://app"})
	_, fast := pdBuild(t, Alert{Event: EventSlowRecovery, MonitorID: "m1", MonitorName: "Shop", StartedAt: start, AppBaseURL: "https://app"})
	if slow.Payload.Severity != "warning" || fast.EventAction != "resolve" || fast.DedupKey != slow.DedupKey {
		t.Fatalf("%+v %+v", slow, fast)
	}
	exp := start.Add(7 * 24 * time.Hour)
	_, w7 := pdBuild(t, Alert{Event: EventSSLExpiry, MonitorID: "m1", MonitorName: "Shop", SSLExpiresAt: exp, SSLBucket: 7, AppBaseURL: "https://app"})
	_, w1 := pdBuild(t, Alert{Event: EventSSLExpiry, MonitorID: "m1", MonitorName: "Shop", SSLExpiresAt: exp, SSLBucket: 1, AppBaseURL: "https://app"})
	_, gone := pdBuild(t, Alert{Event: EventSSLExpiry, MonitorID: "m1", MonitorName: "Shop", SSLExpiresAt: exp, SSLBucket: 0, AppBaseURL: "https://app"})
	if w7.Payload.Severity != "warning" || gone.Payload.Severity != "error" || w7.DedupKey != w1.DedupKey {
		t.Fatalf("later warnings for one certificate update one alert: %s %s %s", w7.DedupKey, w1.DedupKey, gone.Payload.Severity)
	}
	_, dom := pdBuild(t, Alert{Event: EventDomainExpiry, DomainName: "Example.com", DomainExpiresAt: exp, DomainBucket: 30, AppBaseURL: "https://app"})
	if dom.Payload.Source != "Example.com" || !strings.HasPrefix(dom.DedupKey, "hawkeye:domain:example.com:") {
		t.Fatalf("%+v", dom)
	}
	_, test := pdBuild(t, Alert{Event: EventTest, TestID: "t1", AppBaseURL: "https://app"})
	if test.Payload.Severity != "info" || test.DedupKey != "hawkeye:test:t1" || test.Payload.Summary != "HawkEye test alert" {
		t.Fatalf("%+v %+v", test, test.Payload)
	}
}
