package checks

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
)

// fakeDNS answers from fixed tables and remembers the names it was asked for.
type fakeDNS struct {
	ips   []netip.Addr
	mx    []*net.MX
	ns    []*net.NS
	txt   []string
	err   error
	asked []string
	nets  []string
}

func (f *fakeDNS) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	f.asked, f.nets = append(f.asked, host), append(f.nets, network)
	return f.ips, f.err
}
func (f *fakeDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	f.asked = append(f.asked, name)
	return f.mx, f.err
}
func (f *fakeDNS) LookupNS(_ context.Context, name string) ([]*net.NS, error) {
	f.asked = append(f.asked, name)
	return f.ns, f.err
}
func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.asked = append(f.asked, name)
	return f.txt, f.err
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, v := range s {
		out[i] = netip.MustParseAddr(v)
	}
	return out
}

func checkDNS(t *testing.T, f *fakeDNS, recordType, expected string) Result {
	t.Helper()
	c := NewChecker(safehttp.Config{}).WithDNSResolver(f)
	res, err := c.CheckDNS(context.Background(), DNSTarget{Host: "example.com", RecordType: recordType, Expected: expected, Timeout: time.Second})
	if err != nil {
		t.Fatalf("CheckDNS: %v", err)
	}
	return res
}

func TestCheckDNSSuccess(t *testing.T) {
	cases := []struct {
		name, recordType, expected string
		dns                        fakeDNS
	}{
		{"any address", RecordA, "", fakeDNS{ips: addrs("93.184.216.34")}},
		{"one of several addresses", RecordA, "93.184.216.35", fakeDNS{ips: addrs("93.184.216.34", "93.184.216.35")}},
		{"ipv6", RecordAAAA, "2606:2800:220:1::1", fakeDNS{ips: addrs("2606:2800:220:1::1")}},
		{"mx ignores case and the root dot", RecordMX, "mail.example.com", fakeDNS{mx: []*net.MX{{Host: "MAIL.example.com.", Pref: 10}}}},
		{"ns", RecordNS, "ns1.example.net", fakeDNS{ns: []*net.NS{{Host: "ns1.example.net."}}}},
		{"txt matches part of a record", RecordTXT, "include:_spf.example.net", fakeDNS{txt: []string{"other", "v=spf1 include:_spf.example.net ~all"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkDNS(t, &tc.dns, tc.recordType, tc.expected)
			if !res.Success || res.ErrorCategory != "" {
				t.Fatalf("want success, got %+v", res)
			}
			if res.ResponseTimeMs == nil || res.StatusCode != nil {
				t.Fatalf("want a lookup time and no status code, got %+v", res)
			}
		})
	}
}

func TestCheckDNSFailure(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}
	cases := []struct {
		name, recordType, expected string
		dns                        fakeDNS
		category, summary          string
	}{
		{"wrong address", RecordA, "93.184.216.99", fakeDNS{ips: addrs("93.184.216.34")}, CatAssertion, `A record for example.com does not match "93.184.216.99"`},
		{"wrong mail server", RecordMX, "mail.example.com", fakeDNS{mx: []*net.MX{{Host: "evil.example.net."}}}, CatAssertion, `MX record for example.com does not match "mail.example.com"`},
		{"txt setting gone", RecordTXT, "p=reject", fakeDNS{txt: []string{"v=DMARC1; p=none"}}, CatAssertion, `TXT record for example.com does not match "p=reject"`},
		{"no such record", RecordTXT, "", fakeDNS{err: notFound}, CatDNS, "No TXT record found for example.com"},
		{"empty answer", RecordNS, "", fakeDNS{}, CatDNS, "No NS record found for example.com"},
		{"server failure", RecordA, "", fakeDNS{err: &net.DNSError{Err: "server misbehaving", Name: "example.com"}}, CatDNS, "DNS lookup failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkDNS(t, &tc.dns, tc.recordType, tc.expected)
			if res.Success || res.ErrorCategory != tc.category || res.ErrorSummary != tc.summary {
				t.Fatalf("got %+v", res)
			}
		})
	}
}

// A monitor must not reveal what lives on this server's network, whatever the record says, and the answers themselves are never stored.
func TestCheckDNSRefusesPrivateAnswers(t *testing.T) {
	for _, ips := range [][]netip.Addr{addrs("10.0.0.5"), addrs("93.184.216.34", "169.254.169.254"), addrs("::ffff:127.0.0.1")} {
		res := checkDNS(t, &fakeDNS{ips: ips}, RecordA, "")
		if res.Success || res.ErrorCategory != CatBlocked {
			t.Fatalf("%v: got %+v", ips, res)
		}
		for _, ip := range ips {
			if strings.Contains(res.ErrorSummary, ip.Unmap().String()) {
				t.Fatalf("summary reveals the answer: %q", res.ErrorSummary)
			}
		}
	}
}

func TestCheckDNSAsksForFullyQualifiedNames(t *testing.T) {
	for _, recordType := range RecordTypes {
		f := &fakeDNS{ips: addrs("93.184.216.34"), mx: []*net.MX{{Host: "m."}}, ns: []*net.NS{{Host: "n."}}, txt: []string{"t"}}
		checkDNS(t, f, recordType, "")
		if len(f.asked) != 1 || f.asked[0] != "example.com." {
			t.Fatalf("%s: asked for %v", recordType, f.asked)
		}
	}
}

func TestCheckDNSUsesTheRecordsAddressFamily(t *testing.T) {
	for recordType, want := range map[string]string{RecordA: "ip4", RecordAAAA: "ip6"} {
		f := &fakeDNS{ips: addrs("93.184.216.34")}
		checkDNS(t, f, recordType, "")
		if f.nets[0] != want {
			t.Fatalf("%s: looked up %s", recordType, f.nets[0])
		}
	}
}

func TestCheckDNSKeepsValidRecordsReportedWithAnError(t *testing.T) {
	f := &fakeDNS{mx: []*net.MX{{Host: "mail.example.com."}}, err: errors.New("cannot unmarshal DNS message")}
	if res := checkDNS(t, f, RecordMX, "mail.example.com"); !res.Success {
		t.Fatalf("got %+v", res)
	}
}

func TestCheckDNSCanceledIsNotAResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewChecker(safehttp.Config{}).WithDNSResolver(&fakeDNS{err: context.Canceled})
	if _, err := c.CheckDNS(ctx, DNSTarget{Host: "example.com", RecordType: RecordA, Timeout: time.Second}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestNormalizeDNSExpected(t *testing.T) {
	long := strings.Repeat("a", MaxDNSExpected+1)
	cases := []struct {
		recordType, in, want string
		bad                  bool
	}{
		{RecordA, "", "", false},
		{RecordA, " 93.184.216.34 ", "93.184.216.34", false},
		{RecordA, "not an address", "", true},
		{RecordA, "2606:2800:220:1::1", "", true},
		{RecordA, "10.0.0.1", "", true},
		{RecordA, "169.254.169.254", "", true},
		{RecordAAAA, "2606:2800:0220:0001:0000:0000:0000:0001", "2606:2800:220:1::1", false},
		{RecordAAAA, "93.184.216.34", "", true},
		{RecordAAAA, "fd00::1", "", true},
		{RecordMX, "Mail.Example.com.", "mail.example.com", false},
		{RecordMX, "https://mail.example.com", "", true},
		{RecordNS, "ns1 example.com", "", true},
		{RecordTXT, "v=spf1 -all", "v=spf1 -all", false},
		{RecordTXT, "two\nlines", "", true},
		{RecordTXT, long, "", true},
	}
	for _, tc := range cases {
		got, reason := NormalizeDNSExpected(tc.recordType, tc.in)
		if (reason != "") != tc.bad || got != tc.want {
			t.Errorf("%s %q: got %q, reason %q", tc.recordType, tc.in, got, reason)
		}
	}
}
