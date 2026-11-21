package checks

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// slowResolver answers 127.0.0.1 for every name after a delay.
type slowResolver struct{ delay time.Duration }

func (r slowResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	select {
	case <-time.After(r.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

// slowFirstRead delays a connection's first read. On a TLS connection that read is the server's hello, inside the handshake, so the
// handshake measurably takes time even on loopback (where it otherwise rounds down to 0 ms).
type slowFirstRead struct {
	net.Conn
	once sync.Once
}

func (c *slowFirstRead) Read(b []byte) (int, error) {
	c.once.Do(func() { time.Sleep(40 * time.Millisecond) })
	return c.Conn.Read(b)
}

func sleepy(d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(d) })
}

// The phases are measured where they happen and add up to (about) the total.
func TestTimingsSplitTheResponseTime(t *testing.T) {
	url, cfg := tlsServer(t, time.Now().Add(-time.Hour), time.Now().Add(30*24*time.Hour), sleepy(150*time.Millisecond))
	cfg.Resolver = slowResolver{delay: 60 * time.Millisecond}
	cfg.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &slowFirstRead{Conn: c}, nil
	}
	r := check(t, cfg, Target{URL: url})
	if !r.Success || r.Timings == nil {
		t.Fatalf("%+v", r)
	}
	tm := *r.Timings
	if tm.DNSMs < 60 || tm.WaitMs < 150 || tm.TLSMs < 40 || tm.Redirects != 0 {
		t.Fatalf("timings %+v: want DNS ≥ 60 ms, wait ≥ 150 ms, TLS ≥ 40 ms, no redirects", tm)
	}
	sum := tm.DNSMs + tm.ConnectMs + tm.TLSMs + tm.WaitMs
	if total := *r.ResponseTimeMs; sum > total || total-sum > 50 {
		t.Fatalf("phases add up to %d ms, total %d ms", sum, total)
	}
}

func TestTimingsForPlainHTTPAndAddresses(t *testing.T) {
	srv := httptest.NewServer(sleepy(0))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL}) // an IP address: no lookup
	if r.Timings == nil || r.Timings.DNSMs != 0 || r.Timings.TLSMs != 0 {
		t.Fatalf("timings %+v", r.Timings)
	}
}

// Redirects: every hop's phases are added up, and the hops are counted.
func TestTimingsSumRedirectHops(t *testing.T) {
	var final *httptest.Server
	final = httptest.NewServer(sleepy(80 * time.Millisecond))
	defer final.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		http.Redirect(w, r, final.URL, http.StatusFound)
	}))
	defer first.Close()
	r := check(t, loopbackCfg(), Target{URL: first.URL})
	if r.Timings == nil || r.Timings.Redirects != 1 || r.Timings.WaitMs < 160 {
		t.Fatalf("timings %+v: want 1 redirect and both waits (≥ 160 ms)", r.Timings)
	}
}

// A check that times out waiting for the server keeps the phases it got through, and the wait counts up to the timeout.
func TestTimingsOfAFailedCheck(t *testing.T) {
	srv := httptest.NewServer(sleepy(2 * time.Second))
	defer srv.Close()
	r := check(t, loopbackCfg(), Target{URL: srv.URL, Timeout: 300 * time.Millisecond})
	if r.Success || r.ErrorCategory != CatTimeout || r.Timings == nil || r.Timings.WaitMs < 250 {
		t.Fatalf("%s %+v", r.ErrorCategory, r.Timings)
	}
}

// No connection attempted (a refused destination): no timings rather than zeros.
func TestNoTimingsWithoutAConnection(t *testing.T) {
	r := check(t, loopbackCfg(), Target{URL: "http://10.0.0.1/"})
	if r.Timings != nil && r.Timings.ConnectMs+r.Timings.WaitMs > 0 {
		t.Fatalf("timings %+v", r.Timings)
	}
	if r.ErrorCategory != CatBlocked {
		t.Fatalf("category %q", r.ErrorCategory)
	}
}

func TestWorkflowStepsCarryTheirOwnTimings(t *testing.T) {
	srv := httptest.NewServer(sleepy(50 * time.Millisecond))
	defer srv.Close()
	step := func(name string) Step {
		return Step{Name: name, Target: Target{Method: "GET", URL: srv.URL, Timeout: 2 * time.Second}}
	}
	res, err := NewChecker(loopbackCfg()).RunWorkflow(t.Context(), Workflow{Steps: []Step{step("one"), step("two")}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Timings != nil {
		t.Fatal("a run has no timings of its own")
	}
	for _, s := range res.Steps {
		if s.Timings == nil || s.Timings.WaitMs < 50 {
			t.Fatalf("step %s: %+v", s.Name, s.Timings)
		}
	}
}
