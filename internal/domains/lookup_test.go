package domains

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
)

// loopbackHTTP lets the clients reach test servers on 127.0.0.1 while keeping the rest of the policy.
func loopbackHTTP() *http.Client {
	return safehttp.New(safehttp.Config{
		AllowIP:   func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") || safehttp.IsPublicIP(a) },
		AllowPort: func(int) bool { return true },
	}, 3*time.Second)
}

// rdapServer serves a bootstrap file that points every listed TLD at itself, plus a domain endpoint backed by handler.
func rdapServer(t *testing.T, tlds []string, domain http.HandlerFunc) (*RDAP, *int32) {
	t.Helper()
	var bootstrapHits int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/dns.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&bootstrapHits, 1)
		quoted := make([]string, len(tlds))
		for i, tld := range tlds {
			quoted[i] = fmt.Sprintf("%q", tld)
		}
		fmt.Fprintf(w, `{"version":"1.0","services":[[[%s],[%q]]]}`, strings.Join(quoted, ","), srv.URL+"/rdap/")
	})
	mux.HandleFunc("/rdap/domain/", domain)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewRDAP(loopbackHTTP())
	c.BootstrapURL = srv.URL + "/dns.json"
	return c, &bootstrapHits
}

func TestRDAPExpiry(t *testing.T) {
	c, hits := rdapServer(t, []string{"com", "net"}, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/rdap/domain/example.com" {
			t.Errorf("path = %q", got)
		}
		if !strings.Contains(r.Header.Get("Accept"), "rdap+json") {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		fmt.Fprint(w, `{"objectClassName":"domain","events":[
			{"eventAction":"registration","eventDate":"2010-03-01T04:00:00Z"},
			{"eventAction":"expiration","eventDate":"2027-03-01T04:00:00Z"},
			{"eventAction":"last changed","eventDate":"2026-02-01T04:00:00Z"}]}`)
	})
	res, err := c.Lookup(t.Context(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2027, 3, 1, 4, 0, 0, 0, time.UTC); !res.ExpiresAt.Equal(want) || res.Source != SourceRDAP {
		t.Fatalf("result = %+v", res)
	}
	if _, err := c.Lookup(t.Context(), "example.com"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("bootstrap fetched %d times, want once (cached)", n)
	}
}

func TestRDAPOffsetAndCase(t *testing.T) {
	c, _ := rdapServer(t, []string{"com"}, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"Expiration","eventDate":"2027-03-01T06:00:00+02:00"}]}`)
	})
	res, err := c.Lookup(t.Context(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2027, 3, 1, 4, 0, 0, 0, time.UTC); !res.ExpiresAt.Equal(want) || res.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expires = %v", res.ExpiresAt)
	}
}

func TestRDAPOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		unavailable bool
		final       bool
	}{
		{"no expiration event (redacted)", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"events":[{"eventAction":"registration","eventDate":"2010-03-01T04:00:00Z"}]}`)
		}, true, false},
		{"unparseable date", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"events":[{"eventAction":"expiration","eventDate":"soon"}]}`)
		}, true, false},
		{"not found", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }, true, true},
		{"rate limited", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }, false, false},
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }, false, false},
		{"not json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>") }, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := rdapServer(t, []string{"com"}, tc.handler)
			_, err := c.Lookup(t.Context(), "example.com")
			var un *UnavailableError
			switch {
			case err == nil:
				t.Fatal("expected an error")
			case errors.As(err, &un) != tc.unavailable:
				t.Fatalf("err = %v, unavailable = %v", err, errors.As(err, &un))
			case tc.unavailable && (un.Final != tc.final || un.Reason == ""):
				t.Fatalf("unavailable error = %+v", un)
			}
		})
	}
}

func TestRDAPUnsupportedTLD(t *testing.T) {
	c, _ := rdapServer(t, []string{"com"}, func(w http.ResponseWriter, r *http.Request) { t.Error("must not query") })
	_, err := c.Lookup(t.Context(), "example.zz")
	var un *UnavailableError
	if !errors.As(err, &un) || !un.NoRDAP || !strings.Contains(un.Reason, ".zz") {
		t.Fatalf("err = %v", err)
	}
}

func TestRDAPBootstrapFailureIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	c := NewRDAP(loopbackHTTP())
	c.BootstrapURL = srv.URL
	_, err := c.Lookup(t.Context(), "example.com")
	var un *UnavailableError
	if err == nil || errors.As(err, &un) {
		t.Fatalf("err = %v, want a transient error", err)
	}
}

func TestRDAPKeepsStaleBootstrap(t *testing.T) {
	var fail atomic.Bool
	c, _ := rdapServer(t, []string{"com"}, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"events":[{"eventAction":"expiration","eventDate":"2027-03-01T04:00:00Z"}]}`)
	})
	base := c.HTTP.Transport
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fail.Load() && strings.HasSuffix(r.URL.Path, "dns.json") {
			return nil, errors.New("down")
		}
		return base.RoundTrip(r)
	})
	if _, err := c.Lookup(t.Context(), "example.com"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	c.now = func() time.Time { return time.Now().Add(48 * time.Hour) } // the cache is stale now
	if _, err := c.Lookup(t.Context(), "example.com"); err != nil {
		t.Fatalf("a stale bootstrap should still be used: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRDAPRefusesPrivateServers(t *testing.T) {
	// A bootstrap entry (or a redirect) pointing inside the network must not be followed.
	c := NewRDAP(safehttp.New(safehttp.Config{}, time.Second))
	c.servers = map[string][]string{"com": {"http://127.0.0.1:9/rdap/"}}
	c.fetchedAt = time.Now()
	_, err := c.Lookup(t.Context(), "example.com")
	if err == nil || !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("err = %v, want a blocked destination", err)
	}
}

func TestOrderHTTPSFirst(t *testing.T) {
	got := orderHTTPSFirst([]string{"http://a/", "ftp://b/", "https://c/"})
	if len(got) != 2 || got[0] != "https://c/" || got[1] != "http://a/" {
		t.Fatalf("got %v", got)
	}
}

// --- WHOIS ---

func TestParseWHOISExpiry(t *testing.T) {
	cases := []struct {
		name string
		text string
		want time.Time
		ok   bool
	}{
		{"verisign style", "Domain Name: EXAMPLE.COM\r\nRegistry Expiry Date: 2027-08-13T04:00:00Z\r\nRegistrar: X\r\n", time.Date(2027, 8, 13, 4, 0, 0, 0, time.UTC), true},
		{"nominet style", "    Registered on: 01-Jan-2010\n    Expiry date:  25-Jan-2027\n    Last updated: 01-Jan-2026\n", time.Date(2027, 1, 25, 0, 0, 0, 0, time.UTC), true},
		{"paid-till", "domain:  EXAMPLE.RU\npaid-till:    2027-01-31T21:00:00Z\n", time.Date(2027, 1, 31, 21, 0, 0, 0, time.UTC), true},
		{"date with note", "Expiration Date: 2027-05-04 (YYYY-MM-DD)\n", time.Date(2027, 5, 4, 0, 0, 0, 0, time.UTC), true},
		{"space separated", "Expiry Date: 2027-05-04 10:11:12\n", time.Date(2027, 5, 4, 10, 11, 12, 0, time.UTC), true},
		{"dotted", "expires: 2027.05.04\n", time.Date(2027, 5, 4, 0, 0, 0, 0, time.UTC), true},
		{"textual", "Expires On: 04-May-2027 10:11:12 UTC\n", time.Date(2027, 5, 4, 10, 11, 12, 0, time.UTC), true},
		{"first usable wins", "Expiry Date: n/a\nRegistrar Registration Expiration Date: 2028-01-02T00:00:00Z\n", time.Date(2028, 1, 2, 0, 0, 0, 0, time.UTC), true},
		{"redacted", "Registry Expiry Date: REDACTED FOR PRIVACY\n", time.Time{}, false},
		{"no field", "Domain Name: example.com\nCreation Date: 2010-01-01\n", time.Time{}, false},
		{"creation date is not expiry", "Creation Date: 2010-01-01T00:00:00Z\n", time.Time{}, false},
		{"absurd year", "Expiry Date: 0001-01-01\n", time.Time{}, false},
		{"empty", "", time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseWHOISExpiry(c.text)
			if ok != c.ok || !got.Equal(c.want) {
				t.Fatalf("got %v, %v; want %v, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

// fakeWHOIS runs a TCP server that answers each connection with reply(query).
func fakeWHOIS(t *testing.T, reply func(q string) string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				q, _ := bufio.NewReader(conn).ReadString('\n')
				fmt.Fprint(conn, reply(strings.TrimSpace(q)))
			}()
		}
	}()
	return ln.Addr().String()
}

// testWHOIS is a client whose IANA server is the given local listener and which may reach 127.0.0.1.
func testWHOIS(iana string) *WHOIS {
	w := NewWHOIS()
	w.IANA = iana
	w.Timeout = 2 * time.Second
	w.AllowIP = func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }
	w.Resolver = staticResolver{}
	return w
}

type staticResolver struct{}

func (staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func TestWHOISLookup(t *testing.T) {
	registry := fakeWHOIS(t, func(q string) string {
		if q != "example.xx" {
			return "No match\n"
		}
		return "Domain Name: example.xx\nExpiry date: 2027-02-03\n"
	})
	_, regPort, _ := net.SplitHostPort(registry)
	iana := fakeWHOIS(t, func(q string) string {
		if q == "xx" {
			return "domain: XX\n\nwhois:        whois.nic.xx\n\nstatus: ACTIVE\n"
		}
		return "domain: " + q + "\n\n" // no WHOIS server listed
	})
	w := testWHOIS(iana)
	w.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(addr)
		if port == "43" { // the registry's WHOIS port, redirected to the test listener
			addr = net.JoinHostPort(host, regPort)
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	res, err := w.Lookup(t.Context(), "example.xx")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2027, 2, 3, 0, 0, 0, 0, time.UTC); !res.ExpiresAt.Equal(want) || res.Source != SourceWHOIS {
		t.Fatalf("result = %+v", res)
	}

	// A name the registry does not know has no expiry to report.
	_, err = w.Lookup(t.Context(), "other.xx")
	var un *UnavailableError
	if !errors.As(err, &un) || un.NoRDAP {
		t.Fatalf("err = %v", err)
	}
	// A top-level domain IANA lists no server for.
	_, err = w.Lookup(t.Context(), "example.yy")
	if !errors.As(err, &un) || !un.NoRDAP {
		t.Fatalf("err = %v", err)
	}
}

func TestWHOISRefusesPrivateServers(t *testing.T) {
	iana := fakeWHOIS(t, func(q string) string { return "whois: whois.nic.xx\n" })
	w := testWHOIS(iana)
	w.AllowIP = safehttp.IsPublicIP // the production rule: 127.0.0.1 is not allowed
	_, err := w.Lookup(t.Context(), "example.xx")
	if !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("err = %v, want a blocked destination", err)
	}
}

func TestWHOISRejectsOddServerNames(t *testing.T) {
	for _, bad := range []string{"localhost", "evil.com/x", "a b.com", "-x.com", "127.0.0.1:80"} {
		iana := fakeWHOIS(t, func(q string) string { return "whois: " + bad + "\n" })
		w := testWHOIS(iana)
		_, err := w.Lookup(t.Context(), "example.xx")
		var un *UnavailableError
		if !errors.As(err, &un) {
			t.Errorf("%q: err = %v, want unavailable", bad, err)
		}
	}
}

// --- combined ---

type stub struct {
	res Result
	err error
	n   int
}

func (s *stub) Lookup(context.Context, string) (Result, error) { s.n++; return s.res, s.err }

func TestLookupFallback(t *testing.T) {
	ok := Result{ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), Source: SourceWHOIS}
	transient := errors.New("timeout")
	noRDAP := &UnavailableError{Reason: "no rdap", NoRDAP: true}
	noExpiry := &UnavailableError{Reason: "redacted"}
	notFound := &UnavailableError{Reason: "not registered", Final: true}
	whoisNone := &UnavailableError{Reason: "no whois", NoRDAP: true}

	cases := []struct {
		name       string
		rdap       *stub
		whois      *stub
		wantErr    error
		wantSource string
		whoisCalls int
	}{
		{"rdap answers", &stub{res: Result{Source: SourceRDAP}}, &stub{}, nil, SourceRDAP, 0},
		{"unsupported tld, whois answers", &stub{err: noRDAP}, &stub{res: ok}, nil, SourceWHOIS, 1},
		{"redacted rdap, whois answers", &stub{err: noExpiry}, &stub{res: ok}, nil, SourceWHOIS, 1},
		{"rdap failing, whois answers", &stub{err: transient}, &stub{res: ok}, nil, SourceWHOIS, 1},
		{"not found is final", &stub{err: notFound}, &stub{res: ok}, notFound, "", 0},
		{"unsupported everywhere uses whois reason", &stub{err: noRDAP}, &stub{err: whoisNone}, whoisNone, "", 1},
		{"redacted keeps rdap reason", &stub{err: noExpiry}, &stub{err: whoisNone}, noExpiry, "", 1},
		{"rdap failing stays transient", &stub{err: transient}, &stub{err: whoisNone}, transient, "", 1},
		{"unsupported but whois failing stays unavailable", &stub{err: noRDAP}, &stub{err: transient}, noRDAP, "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Lookup{RDAP: c.rdap, WHOIS: c.whois}.Lookup(t.Context(), "example.com")
			if c.wantErr == nil && (err != nil || res.Source != c.wantSource) {
				t.Fatalf("res = %+v, err = %v", res, err)
			}
			if c.wantErr != nil && err != c.wantErr {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.whois.n != c.whoisCalls {
				t.Fatalf("whois called %d times, want %d", c.whois.n, c.whoisCalls)
			}
		})
	}
	// Without a WHOIS fallback the RDAP answer stands.
	if _, err := (Lookup{RDAP: &stub{err: noRDAP}}).Lookup(t.Context(), "example.com"); err != noRDAP {
		t.Fatalf("err = %v", err)
	}
}
