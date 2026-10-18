// Package canary tells "the monitored site is down" apart from "this server cannot reach the internet".
//
// HawkEye checks customers' sites from one server. If that server's own network or DNS breaks, every monitor fails at once, and without a
// second opinion each would count toward an outage and email its owner. The canary is that second opinion: a handful of independent,
// always-up public endpoints (different companies, different networks). When a check fails in a way our own network could cause, the worker
// asks the canary; if most of those endpoints are unreachable too, the failure proves nothing about the customer's site and the worker drops
// the result instead of recording it. A site that is really down is caught on its next check once the canary recovers.
package canary

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// DefaultURLs are tiny, long-lived endpoints run by three unrelated companies, built to be hit by connectivity checks.
var DefaultURLs = []string{
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://www.gstatic.com/generate_204",
	"https://www.apple.com/library/test/success.html",
}

const (
	defaultTimeout = 5 * time.Second  // per endpoint
	defaultTTL     = 15 * time.Second // a verdict this fresh is reused, so a burst of failures causes one round of probes, not hundreds
)

// Canary probes the endpoints and remembers the latest verdict. It is safe for concurrent use.
type Canary struct {
	client  *http.Client
	urls    []string
	timeout time.Duration
	ttl     time.Duration
	now     func() time.Time
	log     *slog.Logger
	observe func(ok, total int, impaired bool)

	mu       sync.Mutex // held across a round of probes on purpose: concurrent callers share one round instead of each starting their own
	at       time.Time
	have     bool
	impaired bool
}

// New returns a Canary that probes urls with client. Use the same SSRF-safe client the checks use, so the canary exercises the same
// DNS and network path customers' checks do.
func New(client *http.Client, urls []string) *Canary {
	return &Canary{client: client, urls: urls, timeout: defaultTimeout, ttl: defaultTTL, now: time.Now, log: slog.Default()}
}

// WithLogger sets where transitions (healthy to impaired and back) are logged.
func (c *Canary) WithLogger(l *slog.Logger) *Canary { c.log = l; return c }

// WithObserver is called after every round with how many endpoints answered, how many were tried, and the verdict (for metrics).
func (c *Canary) WithObserver(fn func(ok, total int, impaired bool)) *Canary {
	c.observe = fn
	return c
}

// Impaired reports whether this server's network looks broken right now: a majority of the endpoints did not answer. It is false when
// unsure (no endpoints configured, or the caller's context was cancelled mid-probe), so real failures are never hidden by a canary fault.
func (c *Canary) Impaired(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.have && c.now().Sub(c.at) < c.ttl {
		return c.impaired
	}
	return c.refresh(ctx)
}

// Run refreshes the verdict every interval until ctx ends, so the metric and the logs stay current even when no check is failing.
func (c *Canary) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		c.mu.Lock()
		c.refresh(ctx)
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refresh runs one round of probes. The caller holds c.mu.
func (c *Canary) refresh(ctx context.Context) bool {
	total := len(c.urls)
	if total == 0 {
		return false
	}
	ok := c.round(ctx)
	if ctx.Err() != nil {
		return false // cancelled mid-round: say nothing rather than cry wolf
	}
	impaired := ok*2 <= total // a strict majority must answer: 2 of 3, and with 1 or 2 endpoints, all of them
	switch {
	case impaired && (!c.have || !c.impaired):
		c.log.Warn("canary: this server cannot reach independent public sites; check failures that look like network trouble are being discarded", "reachable", ok, "tried", total)
	case !impaired && c.have && c.impaired:
		c.log.Info("canary: the network is back; checks count again", "reachable", ok, "tried", total)
	}
	c.at, c.have, c.impaired = c.now(), true, impaired
	if c.observe != nil {
		c.observe(ok, total, impaired)
	}
	return impaired
}

// round probes every endpoint at once and counts the ones that answered at all. Any HTTP response, even an error status, proves the
// network works; only a failure to get a response does not.
func (c *Canary) round(ctx context.Context) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, u := range c.urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, c.timeout)
			defer cancel()
			req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", "Hawkeye-Canary/1.0")
			resp, err := c.client.Do(req)
			if err != nil {
				return
			}
			_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
			_ = resp.Body.Close()
			mu.Lock()
			ok++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return ok
}
