package canary

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// endpoints starts n servers; the returned func stops one of them, and hits counts every request they receive.
func endpoints(t *testing.T, n int, status int) (urls []string, stop func(i int), hits *atomic.Int32) {
	t.Helper()
	hits = &atomic.Int32{}
	srvs := make([]*httptest.Server, n)
	for i := range srvs {
		srvs[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(status) }))
		t.Cleanup(srvs[i].Close)
		urls = append(urls, srvs[i].URL)
	}
	return urls, func(i int) { srvs[i].Close() }, hits
}

func newCanary(urls []string) *Canary { return New(http.DefaultClient, urls).WithLogger(quiet) }

func TestHealthyWhenAMajorityAnswers(t *testing.T) {
	urls, stop, _ := endpoints(t, 3, 204)
	stop(0) // one endpoint down is not our problem: 2 of 3 still answer
	if newCanary(urls).Impaired(t.Context()) {
		t.Fatal("one unreachable endpoint out of three must not count as an impaired network")
	}
}

func TestImpairedWhenAMajorityIsUnreachable(t *testing.T) {
	urls, stop, _ := endpoints(t, 3, 204)
	stop(0)
	stop(1)
	if !newCanary(urls).Impaired(t.Context()) {
		t.Fatal("two of three endpoints unreachable must count as an impaired network")
	}
}

func TestAnyHTTPResponseProvesTheNetworkWorks(t *testing.T) {
	urls, _, _ := endpoints(t, 3, 503) // the endpoints are unhappy, but they answered
	if newCanary(urls).Impaired(t.Context()) {
		t.Fatal("HTTP error statuses still prove we can reach the internet")
	}
}

func TestWithTwoOrOneEndpointsAllMustAnswer(t *testing.T) {
	urls, stop, _ := endpoints(t, 2, 204)
	stop(0)
	if !newCanary(urls).Impaired(t.Context()) {
		t.Fatal("with two endpoints, losing one is half: impaired")
	}
	one, stop1, _ := endpoints(t, 1, 204)
	if newCanary(one).Impaired(t.Context()) {
		t.Fatal("the only endpoint answers: healthy")
	}
	stop1(0)
	if !newCanary(one).Impaired(t.Context()) {
		t.Fatal("the only endpoint is down: impaired")
	}
}

func TestNoEndpointsNeverImpaired(t *testing.T) {
	if newCanary(nil).Impaired(t.Context()) {
		t.Fatal("a canary with nothing to probe must stay out of the way")
	}
}

func TestAFreshVerdictIsReusedAndAStaleOneIsRefreshed(t *testing.T) {
	urls, _, hits := endpoints(t, 3, 204)
	c := newCanary(urls)
	now := time.Unix(1_000_000, 0)
	c.now = func() time.Time { return now }
	for range 50 { // a burst of failing checks
		c.Impaired(t.Context())
	}
	if got := hits.Load(); got != 3 {
		t.Fatalf("a burst of 50 callers made %d probes; want one round of 3", got)
	}
	now = now.Add(c.ttl + time.Second)
	c.Impaired(t.Context())
	if got := hits.Load(); got != 6 {
		t.Fatalf("after the verdict went stale there should be a second round (6 probes), got %d", got)
	}
}

func TestACancelledContextNeverClaimsAnImpairment(t *testing.T) {
	urls, _, _ := endpoints(t, 3, 204)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := newCanary(urls)
	if c.Impaired(ctx) {
		t.Fatal("a cancelled context must not be reported as a broken network")
	}
	if c.have {
		t.Fatal("a cancelled round must not be remembered as a verdict")
	}
}

func TestObserverSeesEveryRound(t *testing.T) {
	urls, stop, _ := endpoints(t, 3, 204)
	stop(2)
	var ok, total int
	var impaired bool
	c := newCanary(urls).WithObserver(func(o, n int, i bool) { ok, total, impaired = o, n, i })
	c.Impaired(t.Context())
	if ok != 2 || total != 3 || impaired {
		t.Fatalf("observer saw ok=%d total=%d impaired=%v", ok, total, impaired)
	}
}
