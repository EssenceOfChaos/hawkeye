package statuspagedomains

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/validation"
)

func TestParseHostname(t *testing.T) {
	ok := map[string]string{
		"status.clientbrand.com":           "status.clientbrand.com",
		"  Status.ClientBrand.COM.  ":      "status.clientbrand.com",
		"https://status.clientbrand.com/":  "status.clientbrand.com",
		"status.client.co.uk":              "status.client.co.uk",
		"uptime.eu.clientbrand.com":        "uptime.eu.clientbrand.com",
		"status.bücher.de":                 "status.xn--bcher-kva.de",
		"http://status.clientbrand.com:80": "status.clientbrand.com",
	}
	for in, want := range ok {
		got, err := ParseHostname(in, "hawkeye.observer")
		if err != nil || got != want {
			t.Errorf("ParseHostname(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := map[string]string{
		"":                          "Enter a domain name",
		"clientbrand.com":           "Use a subdomain, such as status.clientbrand.com",
		"client.co.uk":              "Use a subdomain",
		"co.uk":                     "domain you own",
		"hawkeye.observer":          "belongs to HawkEye",
		"domains.hawkeye.observer":  "belongs to HawkEye",
		"evil.app.hawkeye.observer": "belongs to HawkEye",
		"1.2.3.4":                   "not an IP address",
		"*.clientbrand.com":         "without wildcards",
		"status.clientbrand.local":  "private networks",
		"status.clientbrand.com:22": "standard web ports",
		"user:pw@status.client.com": "without a user name",
	}
	for in, want := range bad {
		_, err := ParseHostname(in, "hawkeye.observer")
		var ve *validation.Error
		if !errors.As(err, &ve) || !strings.Contains(ve.Fields["hostname"], want) {
			t.Errorf("ParseHostname(%q) = %v; want a hostname error containing %q", in, err, want)
		}
	}
}

// fakeDNS answers from maps; a name missing from every map is NXDOMAIN, and timeout names time out.
type fakeDNS struct {
	txt     map[string][]string
	cname   map[string]string
	addrs   map[string][]string
	timeout map[string]bool
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func timedOut(name string) error {
	return &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
}
func (f *fakeDNS) slow(n string) bool { return f.timeout[n] }

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if f.slow(name) {
		return nil, timedOut(name)
	}
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, notFound(name)
}

func (f *fakeDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	if f.slow(host) {
		return "", timedOut(host)
	}
	if v, ok := f.cname[host]; ok {
		return v, nil
	}
	if _, ok := f.addrs[host]; ok {
		return host + ".", nil // like the real resolver: a name without a CNAME is its own canonical name
	}
	return "", notFound(host)
}

func (f *fakeDNS) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if f.slow(host) {
		return nil, timedOut(host)
	}
	if c, ok := f.cname[host]; ok {
		host = strings.TrimSuffix(c, ".")
	}
	v, ok := f.addrs[host]
	if !ok {
		return nil, notFound(host)
	}
	out := make([]netip.Addr, len(v))
	for i, s := range v {
		out[i] = netip.MustParseAddr(s)
	}
	return out, nil
}

const (
	host   = "status.clientbrand.com"
	target = "domains.hawkeye.observer"
	token  = "abcdefghijklmnopqrstuvwxyz234567"
)

func dnsWith(mod func(*fakeDNS)) *fakeDNS {
	f := &fakeDNS{
		txt:     map[string][]string{TXTPrefix + host: {"google-site-verification=xyz", TXTValue(token)}},
		cname:   map[string]string{host: target + "."},
		addrs:   map[string][]string{target: {"203.0.113.10", "2001:db8::10"}},
		timeout: map[string]bool{},
	}
	if mod != nil {
		mod(f)
	}
	return f
}

func TestVerify(t *testing.T) {
	cases := []struct {
		name                     string
		dns                      *fakeDNS
		owned, routed, transient bool
		problem                  string
	}{
		{"both records right", dnsWith(nil), true, true, false, ""},
		{"CNAME flattened to our addresses", dnsWith(func(f *fakeDNS) {
			delete(f.cname, host)
			f.addrs[host] = []string{"203.0.113.10"}
		}), true, true, false, ""},
		{"no TXT record", dnsWith(func(f *fakeDNS) { delete(f.txt, TXTPrefix+host) }), false, true, false, "wasn't found"},
		{"TXT record from another claim", dnsWith(func(f *fakeDNS) { f.txt[TXTPrefix+host] = []string{TXTValue("someoneelsestokenxxxxxxxxxxxxxxx")} }),
			false, true, false, "doesn't contain this domain's verification value"},
		{"no CNAME yet", dnsWith(func(f *fakeDNS) { delete(f.cname, host) }), true, false, false, "doesn't point anywhere yet"},
		{"CNAME to another provider", dnsWith(func(f *fakeDNS) {
			f.cname[host] = "pages.other.example."
			f.addrs["pages.other.example"] = []string{"198.51.100.7"}
		}), true, false, false, "points somewhere other than HawkEye"},
		{"some addresses are not ours", dnsWith(func(f *fakeDNS) {
			delete(f.cname, host)
			f.addrs[host] = []string{"203.0.113.10", "198.51.100.7"}
		}), true, false, false, "points somewhere other"},
		{"lookup timed out", dnsWith(func(f *fakeDNS) { f.timeout[TXTPrefix+host] = true }), false, true, true, "didn't get an answer"},
	}
	for _, c := range cases {
		v := &Verifier{Resolver: c.dns, Target: target}
		r, err := v.Verify(t.Context(), host, token)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Owned != c.owned || r.Routed != c.routed || r.Transient != c.transient || !strings.Contains(r.Problem, c.problem) || (c.problem == "") != (r.Problem == "") {
			t.Errorf("%s: got %+v", c.name, r)
		}
	}
}

func TestVerifyStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	v := &Verifier{Resolver: dnsWith(nil), Target: target}
	if _, err := v.Verify(ctx, host, token); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestStateSchedule(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ok := Result{Owned: true, Routed: true}
	bad := Result{Owned: false, Routed: true, Problem: "The TXT record wasn't found."}
	flaky := Result{Transient: true, Problem: "no answer"}
	in := func(d time.Duration) time.Time { return t0.Add(d) }

	pending := Status{State: StatePending, VerifyStartedAt: t0}
	if n := Next(pending, bad, in(10*time.Minute)); n.State != StatePending || !n.NextCheckAt.Equal(in(11*time.Minute)) || n.LastError == "" {
		t.Fatalf("new claim is checked every minute: %+v", n)
	}
	if n := Next(pending, bad, in(2*time.Hour)); !n.NextCheckAt.Equal(in(2*time.Hour + 15*time.Minute)) {
		t.Fatalf("after the first hour, every 15 minutes: %v", n.NextCheckAt)
	}
	gaveUp := Next(pending, bad, in(73*time.Hour))
	if gaveUp.State != StatePending || gaveUp.NextCheckAt != nil {
		t.Fatalf("after 3 days, no more automatic checks: %+v", gaveUp)
	}
	if r := Restart(gaveUp, in(80*time.Hour)); !r.VerifyStartedAt.Equal(in(80 * time.Hour)) {
		t.Fatalf("Check now starts a new attempt: %+v", r)
	}

	active := Next(pending, ok, in(5*time.Minute))
	if active.State != StateActive || !active.VerifiedAt.Equal(in(5*time.Minute)) || active.LastError != "" || !active.NextCheckAt.Equal(in(5*time.Minute+24*time.Hour)) {
		t.Fatalf("verified: active, checked again daily: %+v", active)
	}
	if r := Restart(active, in(time.Hour)); r.VerifyStartedAt != active.VerifyStartedAt {
		t.Fatal("Restart leaves an active domain alone")
	}
	if n := Next(active, flaky, in(30*time.Hour)); n.State != StateActive || n.FailingSince != nil {
		t.Fatalf("a lookup without an answer changes nothing: %+v", n)
	}

	failing := Next(active, bad, in(30*time.Hour))
	if failing.State != StateFailing || !failing.FailingSince.Equal(in(30*time.Hour)) || !failing.NextCheckAt.Equal(in(31*time.Hour)) {
		t.Fatalf("a wrong record: failing, checked hourly: %+v", failing)
	}
	if !Served(failing.State) || Served(StatePending) || Served(StateDisabled) {
		t.Fatal("failing is still served; pending and disabled are not")
	}
	still := Next(failing, bad, in(30*time.Hour+6*24*time.Hour))
	if still.State != StateFailing || !still.FailingSince.Equal(*failing.FailingSince) {
		t.Fatalf("within the grace period it stays failing: %+v", still)
	}
	if n := Next(failing, flaky, in(30*time.Hour+8*24*time.Hour)); n.State != StateFailing {
		t.Fatalf("no answer never disables: %+v", n)
	}
	disabled := Next(failing, bad, in(30*time.Hour+7*24*time.Hour))
	if disabled.State != StateDisabled || disabled.NextCheckAt != nil || disabled.FailingSince != nil {
		t.Fatalf("after 7 days failing: disabled, not checked automatically: %+v", disabled)
	}
	restarted := Restart(disabled, in(300*time.Hour))
	if n := Next(restarted, bad, in(300*time.Hour)); n.State != StateDisabled || n.NextCheckAt == nil {
		t.Fatalf("Check now on a disabled domain keeps checking for a while: %+v", n)
	}
	if n := Next(restarted, ok, in(300*time.Hour)); n.State != StateActive || !n.VerifiedAt.Equal(in(300*time.Hour)) {
		t.Fatalf("fixed: active again: %+v", n)
	}
	if back := Next(failing, ok, in(31*time.Hour)); back.State != StateActive || back.FailingSince != nil || !back.VerifiedAt.Equal(*active.VerifiedAt) {
		t.Fatalf("fixed within the grace period: active, first verification kept: %+v", back)
	}
}

func TestTokens(t *testing.T) {
	a, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newToken()
	if a == b || len(a) != 32 || strings.Trim(a, "abcdefghijklmnopqrstuvwxyz234567") != "" {
		t.Fatalf("tokens must be random, 32 characters of a-z2-7: %q %q", a, b)
	}
}
