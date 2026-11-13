// Package discovery suggests the first monitors for a domain a customer names ("set up from your domain"). It looks at the site from the outside,
// the way HawkEye's own checks will, and proposes monitors the customer then chooses from; it never creates anything itself.
//
// What it looks for, for a domain such as example.com:
//   - the home page (a website monitor, which also brings SSL certificate and domain registration warnings);
//   - a health endpoint on the same host (/readyz, /healthz, /health, /api/health), which can report trouble the home page hides;
//   - an API on api.<registered domain>, by its health endpoint or its root;
//   - mail (MX) records on the registered domain, since email can stop arriving without any sign on the website.
//
// Safety: every request goes through the monitoring engine's checker (internal/checks) and so its SSRF-safe client (internal/safehttp: public addresses only, ports 80 and 443, every
// redirect revalidated) and the shared limits of internal/toolguard (cache, concurrency cap, per-host budget). Only GET requests to fixed paths are
// sent, whatever the customer typed after the domain, and no response body is kept or returned: a response counts as a health endpoint by its status
// and content type alone.
package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/domains"
	"hawkeye/internal/publichost"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/toolguard"
)

// UserAgent identifies setup's requests to the sites they visit.
const UserAgent = "HawkEye-Setup/1.0 (+https://hawkeye.observer/docs/getting-started)"

var (
	// ErrBlocked means the name resolves to an address that may not be contacted.
	ErrBlocked = errors.New("destination not permitted")
	// ErrNotFound means the name does not exist in DNS: most likely a typo.
	ErrNotFound = errors.New("domain not found")
	// ErrBusy comes from the shared limits: too many lookups are running.
	ErrBusy = toolguard.ErrBusy
)

type TargetLimitedError = toolguard.TargetLimitedError
type InvalidHostError = publichost.InvalidHostError

// Suggestion kinds.
const (
	KindWebsite = "website"
	KindHealth  = "health"
	KindAPI     = "api"
	KindMail    = "mail"
)

// healthPaths are tried in this order of preference: a readiness check that verifies dependencies is the most useful from outside.
var healthPaths = []string{"/readyz", "/healthz", "/health", "/api/health"}

const (
	requestTimeout = 4 * time.Second
	totalTimeout   = 15 * time.Second
	mxTimeout      = 3 * time.Second
)

// Proposal is a monitor to create: the fields POST /monitors takes. Everything else keeps the defaults.
type Proposal struct {
	Type          string  `json:"type"`
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	DNSRecordType *string `json:"dns_record_type,omitempty"`
}

// Suggestion is one monitor setup proposes, with why, written for the customer.
type Suggestion struct {
	Kind        string   `json:"kind"`
	Reason      string   `json:"reason"`
	Recommended bool     `json:"recommended"` // preselected; false when the monitor would likely report a working site as down
	Monitor     Proposal `json:"monitor"`
	// AlreadyMonitored is set by the API when one of the organization's monitors already checks the same thing.
	AlreadyMonitored bool `json:"already_monitored"`
}

// Report is what setup found for one domain. Suggestions are in the order they are worth adding.
type Report struct {
	Domain      string       `json:"domain"`
	Suggestions []Suggestion `json:"suggestions"`
}

// MXResolver looks up mail records. *net.Resolver satisfies it.
type MXResolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

type Service struct {
	checker *checks.Checker
	mx      MXResolver
	guard   *toolguard.Guard[Report]
	probe   func() string // the path of the request that tells a real endpoint from a site that answers every path
}

// New builds a Service whose requests follow cfg's destination policy (the zero Config is the strict production policy).
func New(cfg safehttp.Config) *Service {
	return &Service{
		checker: checks.NewChecker(cfg),
		mx:      net.DefaultResolver,
		guard:   toolguard.New[Report](toolguard.Options{Timeout: totalTimeout, Concurrency: 4}),
		probe:   randomPath,
	}
}

// WithMXResolver replaces the system resolver for mail records. Production leaves it alone; tests use it for fake answers.
func (s *Service) WithMXResolver(r MXResolver) *Service {
	s.mx = r
	return s
}

func randomPath() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "/hawkeye-setup-" + hex.EncodeToString(b)
}

// Suggest looks at the domain a customer typed (a bare domain or any URL on it; only the host and scheme are used).
// Errors are ErrBlocked, ErrNotFound, ErrBusy, *TargetLimitedError, *InvalidHostError, or the context's error.
func (s *Service) Suggest(ctx context.Context, input string) (Report, error) {
	t, err := publichost.Parse(input)
	if err != nil {
		return Report{}, err
	}
	if t.Scheme == "" {
		switch t.Port {
		case "80":
			t.Scheme = "http"
		case "443":
			t.Scheme = "https"
		}
	}
	return s.guard.Do(ctx, t.Scheme+"://"+t.Host, func(ctx context.Context) (Report, error) { return s.run(ctx, t) })
}

func (s *Service) run(ctx context.Context, t publichost.Target) (Report, error) {
	reg, hasReg := domains.Registrable(t.Host)
	var (
		wg        sync.WaitGroup
		site      []Suggestion
		siteErr   error
		api, mail *Suggestion
	)
	wg.Go(func() { site, siteErr = s.site(ctx, t) })
	if hasReg && "api."+reg != t.Host {
		wg.Go(func() { api = s.api(ctx, "api."+reg) })
	}
	if hasReg {
		wg.Go(func() { mail = s.mail(ctx, reg) })
	}
	wg.Wait()
	if siteErr != nil {
		return Report{}, siteErr
	}
	if err := ctx.Err(); err != nil && errors.Is(err, context.Canceled) {
		return Report{}, err // the customer went away: nothing will read this
	}
	r := Report{Domain: t.Host, Suggestions: site}
	for _, x := range []*Suggestion{api, mail} {
		if x != nil {
			r.Suggestions = append(r.Suggestions, *x)
		}
	}
	return r, nil
}

// response is what setup keeps from one request: never the body.
type response struct {
	status   int    // 0 when no response arrived
	html     bool   // the response says it is a web page
	finalURL string // where redirects ended
	failure  string // why no response arrived (a checks.Cat* category)
	err      error  // set only when ctx ended
}

func (r response) ok() bool       { return r.status >= 200 && r.status < 300 }
func (r response) blocked() bool  { return r.failure == checks.CatBlocked }
func (r response) notFound() bool { return r.failure == checks.CatDNS }

// get sends one GET through the monitoring engine's checker, the one path HawkEye uses to request customer-supplied addresses: the SSRF-safe client
// (internal/safehttp), redirects revalidated, the body drained but never read.
func (s *Service) get(ctx context.Context, u string) response {
	res, err := s.checker.Check(ctx, checks.Target{URL: u, Method: http.MethodGet, Timeout: requestTimeout, UserAgent: UserAgent})
	if err != nil {
		return response{err: err}
	}
	r := response{html: res.ContentType == "text/html" || res.ContentType == "application/xhtml+xml", finalURL: res.FinalURL}
	if res.StatusCode != nil {
		r.status = *res.StatusCode
	} else {
		r.failure = res.ErrorCategory
	}
	return r
}

// site proposes the home page and, when the host has one, its health endpoint.
func (s *Service) site(ctx context.Context, t publichost.Target) ([]Suggestion, error) {
	scheme, home := t.Scheme, response{}
	if scheme != "" {
		home = s.get(ctx, scheme+"://"+t.Host+"/")
	} else {
		scheme, home = "https", s.get(ctx, "https://"+t.Host+"/")
		if home.status == 0 && !home.blocked() && !home.notFound() && ctx.Err() == nil {
			// https did not answer: if plain http does, that is the address to monitor.
			if plain := s.get(ctx, "http://"+t.Host+"/"); plain.status != 0 {
				scheme, home = "http", plain
			}
		}
	}
	switch {
	case home.blocked():
		return nil, ErrBlocked
	case home.notFound():
		return nil, ErrNotFound
	case home.status == 0 && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled):
		return nil, ctx.Err()
	}

	base := scheme + "://" + t.Host
	out := []Suggestion{websiteSuggestion(t.Host, base, scheme, home)}
	if home.status != 0 {
		if path, ok := s.healthPath(ctx, base); ok {
			out = append(out, Suggestion{Kind: KindHealth, Recommended: true,
				Monitor: Proposal{Type: "http", Name: t.Host + " health", URL: base + path},
				Reason:  fmt.Sprintf("Found a health endpoint at %s. It can report problems the home page hides, such as a database that is down.", path)})
		}
	}
	return out, nil
}

func websiteSuggestion(host, base, scheme string, home response) Suggestion {
	sg := Suggestion{Kind: KindWebsite, Recommended: true, Monitor: Proposal{Type: "http", Name: host, URL: base + "/"}}
	var b strings.Builder
	switch {
	case home.status == 0:
		b.WriteString("Your site did not answer just now. The monitor will show it as down until it does.")
	case home.status >= 500:
		fmt.Fprintf(&b, "Your home page answered with an error (%d) just now. The monitor will show it as down until that is fixed.", home.status)
	case home.status >= 400:
		sg.Recommended = false
		fmt.Fprintf(&b, "Your home page answered %d just now, which a monitor counts as down. Some sites refuse automated requests: after adding it, you can set the status code to expect.", home.status)
	default:
		b.WriteString("Checks that your site answers.")
	}
	if scheme == "https" {
		b.WriteString(" Also warns you before its SSL certificate or domain registration expires.")
	} else {
		b.WriteString(" Only plain http answered, so there is no SSL certificate to watch; domain registration is still tracked.")
	}
	if to := redirectHost(home.finalURL); to != "" && to != host {
		fmt.Fprintf(&b, " It redirects to %s, which the check follows.", to)
	}
	sg.Reason = b.String()
	return sg
}

func redirectHost(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return p.Hostname()
}

// healthPath finds the first of healthPaths that answers 2xx with something other than a web page. A site that answers a made-up path with 2xx answers
// every path that way (a single-page app, a catch-all route), so a 2xx from it proves nothing and no endpoint is reported.
func (s *Service) healthPath(ctx context.Context, base string) (string, bool) {
	paths := append([]string{s.probe()}, healthPaths...)
	got := make([]response, len(paths))
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Go(func() { got[i] = s.get(ctx, base+p) })
	}
	wg.Wait()
	if got[0].ok() {
		return "", false
	}
	for i, r := range got[1:] {
		if r.ok() && !r.html {
			return healthPaths[i], true
		}
	}
	return "", false
}

// api proposes the API host by its health endpoint or, failing that, its root when the root answers successfully. Nothing is proposed when the host does
// not exist or cannot be reached: most domains have no api subdomain, and a missing one is not worth mentioning.
func (s *Service) api(ctx context.Context, host string) *Suggestion {
	base := "https://" + host
	root := s.get(ctx, base+"/")
	if root.status == 0 {
		return nil
	}
	if path, ok := s.healthPath(ctx, base); ok {
		return &Suggestion{Kind: KindAPI, Recommended: true, Monitor: Proposal{Type: "http", Name: host + " health", URL: base + path},
			Reason: fmt.Sprintf("Found an API at %s with a health endpoint at %s.", host, path)}
	}
	if root.status < 400 {
		return &Suggestion{Kind: KindAPI, Recommended: true, Monitor: Proposal{Type: "http", Name: host, URL: base + "/"},
			Reason: fmt.Sprintf("Found an API at %s that answers at its root address.", host)}
	}
	return nil
}

// mail proposes a DNS monitor on the domain's mail records when it has any. A "null MX" (a single "." record) says the domain takes no email.
func (s *Service) mail(ctx context.Context, domain string) *Suggestion {
	ctx, cancel := context.WithTimeout(ctx, mxTimeout)
	defer cancel()
	recs, err := s.mx.LookupMX(ctx, domain)
	if err != nil || len(recs) == 0 || (len(recs) == 1 && strings.TrimSuffix(recs[0].Host, ".") == "") {
		return nil
	}
	mx := "MX"
	return &Suggestion{Kind: KindMail, Recommended: true, Monitor: Proposal{Type: "dns", Name: domain + " mail (MX)", URL: domain, DNSRecordType: &mx},
		Reason: fmt.Sprintf("Your domain receives email through %s. This alerts you if its mail (MX) records disappear, which stops email arriving without any sign on your website.",
			strings.TrimSuffix(recs[0].Host, "."))}
}
