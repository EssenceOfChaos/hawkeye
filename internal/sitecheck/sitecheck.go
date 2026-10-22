// Package sitecheck answers "is this website down?" for the free website status checker: it requests the home page of a public domain once
// and reports whether it answered, with the HTTP status, how long it took and where redirects ended.
//
// It reuses the monitoring engine's checker, and so its SSRF-safe client (internal/safehttp: public addresses only, ports 80 and 443,
// every redirect hop revalidated), and the shared limits of internal/toolguard. On top of that, because anyone can use it:
//   - only the host is used: the path and query a visitor typed are ignored and the request is always GET / (so a pasted link with a token
//     in it is never sent anywhere, and the tool cannot be used to trigger an action on someone's site);
//   - the response body is never returned, only the status code, timing and the final address (without its query);
//   - the request says who it is (User-Agent) and links to the page that explains it.
package sitecheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/publichost"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/toolguard"
)

// UserAgent identifies the checker to the sites it visits.
const UserAgent = "HawkEye-StatusChecker/1.0 (+https://hawkeye.observer/tools/is-website-down)"

// ErrBlocked means the name resolves to an address that may not be contacted. The visitor is not told which.
var ErrBlocked = errors.New("destination not permitted")

// ErrBusy and TargetLimitedError come from the shared limits.
var ErrBusy = toolguard.ErrBusy

type TargetLimitedError = toolguard.TargetLimitedError
type InvalidHostError = publichost.InvalidHostError

// Verdicts.
const (
	VerdictUp            = "up"             // answered with a success or redirect status
	VerdictErrorResponse = "error_response" // answered with a 4xx status: the site is reachable, but this request was refused or not found
	VerdictDown          = "down"           // no usable answer (could not connect, timed out, TLS failed) or a 5xx status
)

// Reasons: why the verdict is what it is.
const (
	ReasonOK       = "ok"
	ReasonHTTP4xx  = "http_4xx"
	ReasonHTTP5xx  = "http_5xx"
	ReasonDNS      = "dns"
	ReasonConnect  = "connect"
	ReasonTLS      = "tls"
	ReasonTimeout  = "timeout"
	ReasonRedirect = "redirect"
	ReasonOther    = "other"
)

// Speeds, from time to first byte guidance (web.dev/ttfb: good up to 0.8 s, poor above 1.8 s). The measurement here includes every
// redirect, DNS, the connection and TLS, which is also what that guidance counts.
const (
	SpeedFast     = "fast"
	SpeedModerate = "moderate"
	SpeedSlow     = "slow"

	fastMs = 800
	slowMs = 1800
)

// Report is the answer for one host. It is built only from the status line and timings of one request.
type Report struct {
	Host           string    `json:"host"`
	URL            string    `json:"url"` // what was requested
	CheckedAt      time.Time `json:"checked_at"`
	Verdict        string    `json:"verdict"`
	Reason         string    `json:"reason"`
	StatusCode     *int      `json:"status_code,omitempty"`
	ResponseTimeMs *int      `json:"response_time_ms,omitempty"`
	Speed          string    `json:"speed,omitempty"`
	// FinalURL is where redirects ended, when that is not the address requested.
	FinalURL string `json:"final_url,omitempty"`
	// HTTPSError is why https failed, when the visitor gave no scheme and the answer comes from plain http instead (reason as above).
	HTTPSError string `json:"https_error,omitempty"`
}

// Checker performs one request. *checks.Checker is the production one.
type Checker interface {
	Check(ctx context.Context, t checks.Target) (checks.Result, error)
}

const (
	attemptTimeout = 6 * time.Second
	totalTimeout   = 14 * time.Second // two attempts, plus a little
	// Short, because the visitor who fixed an outage will check again: an answer is reused for no longer than this, and says when it was made.
	cacheTTL = 30 * time.Second
)

type Service struct {
	checker Checker
	now     func() time.Time
	guard   *toolguard.Guard[Report]
}

// New builds a Service whose requests follow cfg's destination policy (the zero Config is the strict production policy).
func New(cfg safehttp.Config) *Service {
	return &Service{checker: checks.NewChecker(cfg), now: time.Now, guard: toolguard.New[Report](toolguard.Options{Timeout: totalTimeout, CacheTTL: cacheTTL})}
}

// Inspect checks the host a visitor typed. A bare domain is tried over https, then over http if https could not connect or negotiate TLS.
// Errors are ErrBlocked, ErrBusy, *TargetLimitedError, *InvalidHostError, or the context's error.
func (s *Service) Inspect(ctx context.Context, input string) (Report, error) {
	t, err := publichost.Parse(input)
	if err != nil {
		return Report{}, err
	}
	return s.guard.Do(ctx, t.Host, func(ctx context.Context) (Report, error) { return s.run(ctx, t) })
}

func (s *Service) run(ctx context.Context, t publichost.Target) (Report, error) {
	scheme := t.Scheme
	if scheme == "" {
		switch t.Port {
		case "80":
			scheme = "http"
		case "443":
			scheme = "https"
		}
	}
	if scheme != "" {
		return s.attempt(ctx, scheme, t.Host)
	}
	r, err := s.attempt(ctx, "https", t.Host)
	if err != nil || r.StatusCode != nil || (r.Reason != ReasonConnect && r.Reason != ReasonTLS) {
		return r, err
	}
	// https could not connect or negotiate TLS: see whether plain http answers, which tells the visitor the site is up but not secure.
	plain, err := s.attempt(ctx, "http", t.Host)
	if err != nil {
		return Report{}, err
	}
	if plain.StatusCode == nil {
		return r, nil // neither works: report the https failure
	}
	plain.HTTPSError = r.Reason
	return plain, nil
}

func (s *Service) attempt(ctx context.Context, scheme, host string) (Report, error) {
	url := fmt.Sprintf("%s://%s/", scheme, host)
	res, err := s.checker.Check(ctx, checks.Target{URL: url, Method: "GET", Timeout: attemptTimeout, UserAgent: UserAgent})
	if err != nil {
		return Report{}, err // the visitor went away or the deadline passed
	}
	if res.ErrorCategory == checks.CatBlocked {
		return Report{}, ErrBlocked
	}
	r := Report{Host: host, URL: url, CheckedAt: s.now().UTC(), StatusCode: res.StatusCode, ResponseTimeMs: res.ResponseTimeMs}
	r.Verdict, r.Reason = classify(res)
	if r.ResponseTimeMs != nil {
		r.Speed = speed(*r.ResponseTimeMs)
	}
	if res.FinalURL != "" && res.FinalURL != url {
		r.FinalURL = shorten(res.FinalURL)
	}
	return r, nil
}

// shorten keeps a long redirect address from filling the page (and from carrying whatever a site puts in a long path).
func shorten(u string) string {
	const max = 120
	if r := []rune(u); len(r) > max {
		return string(r[:max]) + "…"
	}
	return u
}

func classify(res checks.Result) (verdict, reason string) {
	switch res.ErrorCategory {
	case "":
		return VerdictUp, ReasonOK
	case checks.CatHTTPStatus:
		if res.StatusCode != nil && *res.StatusCode >= 500 {
			return VerdictDown, ReasonHTTP5xx
		}
		return VerdictErrorResponse, ReasonHTTP4xx
	case checks.CatDNS:
		return VerdictDown, ReasonDNS
	case checks.CatConnect:
		return VerdictDown, ReasonConnect
	case checks.CatTLS:
		return VerdictDown, ReasonTLS
	case checks.CatTimeout:
		return VerdictDown, ReasonTimeout
	case checks.CatRedirect:
		return VerdictDown, ReasonRedirect
	}
	return VerdictDown, ReasonOther
}

func speed(ms int) string {
	switch {
	case ms <= fastMs:
		return SpeedFast
	case ms <= slowMs:
		return SpeedModerate
	}
	return SpeedSlow
}
