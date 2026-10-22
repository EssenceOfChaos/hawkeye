package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsPublicIP(t *testing.T) {
	blocked := []string{
		// loopback, unspecified
		"127.0.0.1", "127.255.255.254", "0.0.0.0", "0.1.2.3", "::1", "::", "::2",
		// RFC 1918 and unique-local
		"10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.255", "192.168.0.1", "fc00::1", "fd12:3456::1",
		// link-local and cloud metadata
		"169.254.169.254", "169.254.0.1", "fe80::1", "fd00:ec2::254", "100.100.100.200",
		// carrier-grade NAT, test nets, benchmarking, reserved, broadcast, multicast
		"100.64.0.1", "192.0.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.7", "203.0.113.9",
		"240.0.0.1", "255.255.255.255", "224.0.0.1", "239.255.255.250", "ff02::1", "ff05::2",
		// IPv4-mapped IPv6 hiding private/loopback/metadata addresses
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:192.168.1.1", "::ffff:169.254.169.254", "::ffff:7f00:1",
		// IPv4-compatible, NAT64, 6to4, Teredo, documentation, discard
		"::127.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "2002:7f00:1::1", "2002:a9fe:a9fe::1",
		"2001:0:4136:e378:8000:63bf:3fff:fdd2", "2001:db8::1", "100::1", "fec0::1",
		// zoned
		"fe80::1%eth0", "2606:4700::1111%eth0",
	}
	for _, s := range blocked {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("bad test address %q: %v", s, err)
		}
		if IsPublicIP(a) {
			t.Errorf("%s must be blocked", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.32.0.1", "172.15.255.255", "100.63.255.255",
		"100.128.0.1", "2606:4700:4700::1111", "2001:4860:4860::8888", "::ffff:8.8.8.8", "193.0.0.1"}
	for _, s := range allowed {
		if !IsPublicIP(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
	if IsPublicIP(netip.Addr{}) {
		t.Error("invalid address must be blocked")
	}
}

func TestIsAllowedPort(t *testing.T) {
	for _, p := range []int{80, 443} {
		if !IsAllowedPort(p) {
			t.Errorf("%d", p)
		}
	}
	for _, p := range []int{0, 22, 25, 3306, 5432, 6379, 8080, 8443, 65535} {
		if IsAllowedPort(p) {
			t.Errorf("%d must be blocked", p)
		}
	}
}

// fakeResolver answers from a map, optionally with a sequence of answers per call (for rebinding tests).
type fakeResolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr
	calls   map[string]int
}

func (f *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	seq, ok := f.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := min(f.calls[host], len(seq)-1)
	f.calls[host]++
	return seq[i], nil
}

func addrs(ss ...string) []netip.Addr {
	var out []netip.Addr
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}
	return out
}

// recordingDial records every address it is asked to connect to and then connects to a real
// local test server, so the full HTTP exchange still happens.
type recordingDial struct {
	mu     sync.Mutex
	dialed []string
	target string // host:port of the real httptest server
}

func (r *recordingDial) dial(ctx context.Context, network, address string) (net.Conn, error) {
	r.mu.Lock()
	r.dialed = append(r.dialed, address)
	r.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, r.target)
}

func (r *recordingDial) addresses() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.dialed...)
}

// testPublic is a documentation address the test policy treats as public, standing in for a real
// internet host. Everything else uses the production rules.
var testPublic = netip.MustParseAddr("198.51.100.10")

func testAllowIP(a netip.Addr) bool { return a == testPublic || IsPublicIP(a) }

func get(c *http.Client, url string) (*http.Response, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	return c.Do(req)
}

func TestHostnameResolvingToPrivateAddressIsBlockedBeforeDialing(t *testing.T) {
	for name, ip := range map[string]string{
		"loopback": "127.0.0.1", "private": "10.1.2.3", "metadata": "169.254.169.254",
		"mapped": "::ffff:127.0.0.1", "ula": "fd00::1", "decimal-alias": "127.0.0.1",
	} {
		t.Run(name, func(t *testing.T) {
			rd := &recordingDial{target: "127.0.0.1:1"}
			c := New(Config{Resolver: &fakeResolver{answers: map[string][][]netip.Addr{"evil.example": {addrs(ip)}}}, Dial: rd.dial}, 2*time.Second)
			_, err := get(c, "http://evil.example/")
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("expected ErrBlocked, got %v", err)
			}
			if len(rd.addresses()) != 0 {
				t.Fatalf("a connection was attempted: %v", rd.addresses())
			}
		})
	}
}

func TestMixedAnswersAreBlocked(t *testing.T) {
	rd := &recordingDial{target: "127.0.0.1:1"}
	c := New(Config{Resolver: &fakeResolver{answers: map[string][][]netip.Addr{"mixed.example": {addrs("8.8.8.8", "10.0.0.5")}}}, Dial: rd.dial}, time.Second)
	if _, err := get(c, "http://mixed.example/"); !errors.Is(err, ErrBlocked) || len(rd.addresses()) != 0 {
		t.Fatalf("err=%v dialed=%v", err, rd.addresses())
	}
}

func TestLiteralIPsAreBlocked(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/", "http://[::1]/", "http://10.0.0.1/", "http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:127.0.0.1]/", "http://[::ffff:a9fe:a9fe]/", "http://0.0.0.0/", "http://192.168.0.1/",
		"https://[fd00:ec2::254]/", "http://100.100.100.200/",
	} {
		rd := &recordingDial{target: "127.0.0.1:1"}
		c := New(Config{Resolver: &fakeResolver{}, Dial: rd.dial}, time.Second)
		if _, err := get(c, u); !errors.Is(err, ErrBlocked) || len(rd.addresses()) != 0 {
			t.Errorf("%s: err=%v dialed=%v", u, err, rd.addresses())
		}
	}
}

// Alternate IP spellings are not parsed by net/netip; they reach the resolver as host names. Whatever
// the system resolver does with them, the resulting address is validated, so they can never connect.
func TestAlternateIPSpellingsNeverConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	// Allow the listener's port so only the IP rules can stop the request.
	c := New(Config{AllowPort: func(p int) bool { return p == port }}, 2*time.Second)
	for _, host := range []string{"127.1", "2130706433", "0x7f.0.0.1", "0177.0.0.1", "017700000001", "localhost", "127.0.0.1.", "[::1]", "[::ffff:7f00:1]"} {
		_, err := get(c, fmt.Sprintf("http://%s:%d/", host, port))
		if err == nil {
			t.Errorf("%s: request unexpectedly succeeded", host)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := accepted.Load(); n != 0 {
		t.Fatalf("listener accepted %d connections; prohibited destinations were reached", n)
	}
}

func TestPortsOtherThan80And443AreBlocked(t *testing.T) {
	rd := &recordingDial{target: "127.0.0.1:1"}
	c := New(Config{Resolver: &fakeResolver{answers: map[string][][]netip.Addr{"ok.example": {addrs("8.8.8.8")}}}, Dial: rd.dial}, time.Second)
	for _, u := range []string{"http://ok.example:22/", "http://ok.example:8080/", "https://ok.example:6379/", "http://8.8.8.8:25/"} {
		if _, err := get(c, u); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", u, err)
		}
	}
	if len(rd.addresses()) != 0 {
		t.Fatalf("dialed %v", rd.addresses())
	}
}

// DNS rebinding: the host first resolves to a public address and later to a private one. We resolve once per
// connection and dial the validated IP literal, so the second answer can never be the one used.
func TestDNSRebindingCannotChangeTheDialedAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	rd := &recordingDial{target: srv.Listener.Addr().String()}
	res := &fakeResolver{answers: map[string][][]netip.Addr{"rebind.example": {addrs("198.51.100.10"), addrs("127.0.0.1")}}}
	c := New(Config{Resolver: res, Dial: rd.dial, AllowIP: testAllowIP}, 2*time.Second)

	resp, err := get(c, "http://rebind.example/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := rd.addresses(); len(got) != 1 || got[0] != "198.51.100.10:80" {
		t.Fatalf("must dial the validated IP literal, dialed %v", got)
	}
	// The next connection sees the rebound (private) answer and is refused.
	if _, err := get(c, "http://rebind.example/"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("rebound answer must be blocked, got %v", err)
	}
	if len(rd.addresses()) != 1 {
		t.Fatalf("no further dial expected, got %v", rd.addresses())
	}
}

func TestRedirectToInternalTargetIsBlocked(t *testing.T) {
	for name, location := range map[string]string{
		"metadata":    "http://169.254.169.254/latest/meta-data/",
		"loopback":    "http://127.0.0.1/admin",
		"private-dns": "http://internal.example/",
		"mapped":      "http://[::ffff:10.0.0.1]/",
		"bad-port":    "http://public.example:8080/",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer srv.Close()
			rd := &recordingDial{target: srv.Listener.Addr().String()}
			res := &fakeResolver{answers: map[string][][]netip.Addr{
				"public.example": {addrs("198.51.100.10")}, "internal.example": {addrs("10.0.0.5")},
			}}
			c := New(Config{Resolver: res, Dial: rd.dial, AllowIP: testAllowIP}, 2*time.Second)
			_, err := get(c, "http://public.example/")
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("expected ErrBlocked, got %v", err)
			}
			if got := rd.addresses(); len(got) != 1 {
				t.Fatalf("only the first hop may be dialed, got %v", got)
			}
		})
	}
}

func TestRedirectToPublicTargetIsFollowedAndRevalidated(t *testing.T) {
	var hops atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hops.Add(1) == 1 {
			http.Redirect(w, r, "http://second.example/final", http.StatusMovedPermanently)
			return
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	rd := &recordingDial{target: srv.Listener.Addr().String()}
	res := &fakeResolver{answers: map[string][][]netip.Addr{"first.example": {addrs("8.8.8.8")}, "second.example": {addrs("1.1.1.1")}}}
	c := New(Config{Resolver: res, Dial: rd.dial}, 2*time.Second)
	resp, err := get(c, "http://first.example/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := rd.addresses(); len(got) != 2 || got[0] != "8.8.8.8:80" || got[1] != "1.1.1.1:80" {
		t.Fatalf("each hop must be dialed at its own validated address: %v", got)
	}
}

func TestRedirectLimitAndSchemes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	rd := &recordingDial{target: srv.Listener.Addr().String()}
	res := &fakeResolver{answers: map[string][][]netip.Addr{"loop.example": {addrs("8.8.8.8")}}}
	c := New(Config{Resolver: res, Dial: rd.dial, MaxRedirects: 3}, 2*time.Second)
	if _, err := get(c, "http://loop.example/"); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("got %v", err)
	}

	for _, loc := range []string{"file:///etc/passwd", "gopher://8.8.8.8/", "http://user:pw@loop.example/"} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", loc)
			w.WriteHeader(302)
		}))
		rd := &recordingDial{target: s.Listener.Addr().String()}
		c := New(Config{Resolver: res, Dial: rd.dial}, 2*time.Second)
		if _, err := get(c, "http://loop.example/"); err == nil {
			t.Errorf("redirect to %q must fail", loc)
		}
		s.Close()
	}
}

func TestEnvironmentProxyIsIgnored(t *testing.T) {
	// Go caches proxy variables process-wide, so assert the transport setting itself: a proxy would
	// open connections on our behalf and bypass every dial-time check.
	if tr := New(Config{}, time.Second).Transport.(*http.Transport); tr.Proxy != nil {
		t.Fatal("transport must not use a proxy")
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	rd := &recordingDial{target: srv.Listener.Addr().String()}
	c := New(Config{Resolver: &fakeResolver{answers: map[string][][]netip.Addr{"p.example": {addrs("8.8.8.8")}}}, Dial: rd.dial}, 2*time.Second)
	resp, err := get(c, "http://p.example/")
	if err != nil {
		t.Fatalf("proxy settings must not be used: %v", err)
	}
	resp.Body.Close()
	if got := rd.addresses(); len(got) != 1 || got[0] != "8.8.8.8:80" {
		t.Fatalf("dialed %v", got)
	}
}

func TestDNSFailureIsReportedAsDNSError(t *testing.T) {
	c := New(Config{Resolver: &fakeResolver{}}, time.Second)
	_, err := get(c, "http://does-not-exist.example/")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("got %v", err)
	}
}

// The dialer's Control hook is an independent second check on the real connection address.
func TestDefaultDialerControlBlocksEvenIfResolutionLogicIsBypassed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	// Validation in dialContext is made permissive; only the default dialer's Control hook stands guard
	// (it shares the same policy, so make a policy that only the hook sees by wrapping AllowIP).
	var calls atomic.Int32
	cfg := Config{AllowPort: func(p int) bool { return p == port }}
	cfg.AllowIP = func(a netip.Addr) bool {
		// First call (pre-dial validation) lies; later calls (Control) tell the truth.
		if calls.Add(1) == 1 {
			return true
		}
		return IsPublicIP(a)
	}
	c := New(cfg, 2*time.Second)
	_, err = get(c, fmt.Sprintf("http://127.0.0.1:%d/", port))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("Control hook must block, got %v", err)
	}
	if accepted.Load() != 0 {
		t.Fatal("connection reached the listener")
	}
}

func TestNewDialerAppliesTheSamePolicyAsTheClient(t *testing.T) {
	rd := &recordingDial{target: "127.0.0.1:1"}
	res := &fakeResolver{answers: map[string][][]netip.Addr{
		"public.example": {addrs("8.8.8.8")}, "private.example": {addrs("10.0.0.5")}, "mixed.example": {addrs("8.8.8.8", "10.0.0.5")},
	}}
	dial := NewDialer(Config{Resolver: res, Dial: rd.dial})
	for _, addr := range []string{"private.example:443", "mixed.example:443", "public.example:22", "public.example:8443", "127.0.0.1:443", "[::1]:443", "169.254.169.254:80"} {
		if _, err := dial(context.Background(), "tcp", addr); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: err = %v, want ErrBlocked", addr, err)
		}
	}
	if got := rd.addresses(); len(got) != 0 {
		t.Fatalf("a blocked destination was dialed: %v", got)
	}
	// An allowed destination is dialed by its validated IP, not by name.
	_, _ = dial(context.Background(), "tcp", "public.example:443")
	if got := rd.addresses(); len(got) != 1 || got[0] != "8.8.8.8:443" {
		t.Errorf("dialed %v, want [8.8.8.8:443]", got)
	}
}
