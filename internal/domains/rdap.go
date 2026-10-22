package domains

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Result is a registration expiry date read from a registry.
type Result struct {
	ExpiresAt time.Time
	Source    string // rdap or whois
}

// Lookup sources.
const (
	SourceRDAP  = "rdap"
	SourceWHOIS = "whois"
)

// UnavailableError means the registry answered but has no expiry date to give: the top-level domain offers neither RDAP nor a readable WHOIS,
// the record is redacted, or the name is not registered. The text is shown to customers. Any other error from a lookup is transient (a timeout,
// a rate limit, a registry outage) and is retried later without changing what the customer sees.
type UnavailableError struct {
	Reason string
	// Final marks an answer another protocol cannot improve on (the registry says the name does not exist).
	Final bool
	// NoRDAP marks a top-level domain without RDAP, so the WHOIS answer is the more informative one.
	NoRDAP bool
}

func (e *UnavailableError) Error() string { return "expiry unavailable: " + e.Reason }

// DefaultBootstrapURL is IANA's registry of RDAP servers by top-level domain (RFC 9224).
const DefaultBootstrapURL = "https://data.iana.org/rdap/dns.json"

const (
	maxRDAPBody   = 1 << 20
	bootstrapTTL  = 24 * time.Hour
	userAgent     = "HawkEye-DomainCheck/1"
	rdapMediaType = "application/rdap+json, application/json;q=0.9"
)

// RDAP looks up domain registrations over the Registration Data Access Protocol. It finds each top-level domain's server through the IANA
// bootstrap file, which it caches. Use a client from safehttp: server addresses come from that file and from redirects.
type RDAP struct {
	HTTP         *http.Client
	BootstrapURL string

	now       func() time.Time
	mu        sync.Mutex
	servers   map[string][]string // top-level domain -> base URLs
	fetchedAt time.Time
}

func NewRDAP(c *http.Client) *RDAP {
	return &RDAP{HTTP: c, BootstrapURL: DefaultBootstrapURL, now: time.Now}
}

type bootstrapFile struct {
	Services [][][]string `json:"services"` // [[tlds...], [urls...]]
}

// serversFor returns the RDAP base URLs of a top-level domain, https first. An empty result means the TLD has no RDAP service.
func (c *RDAP) serversFor(ctx context.Context, tld string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.servers == nil || c.now().Sub(c.fetchedAt) > bootstrapTTL {
		m, err := c.fetchBootstrap(ctx)
		switch {
		case err == nil:
			c.servers, c.fetchedAt = m, c.now()
		case c.servers == nil:
			return nil, err
		default: // keep serving the stale copy; try again on the next lookup
		}
	}
	return c.servers[strings.ToLower(tld)], nil
}

func (c *RDAP) fetchBootstrap(ctx context.Context) (map[string][]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BootstrapURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rdap bootstrap: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rdap bootstrap: HTTP %d", resp.StatusCode)
	}
	var f bootstrapFile
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&f); err != nil {
		return nil, fmt.Errorf("rdap bootstrap: %w", err)
	}
	m := map[string][]string{}
	for _, svc := range f.Services {
		if len(svc) != 2 {
			continue
		}
		urls := orderHTTPSFirst(svc[1])
		for _, tld := range svc[0] {
			m[strings.ToLower(tld)] = urls
		}
	}
	if len(m) == 0 {
		return nil, errors.New("rdap bootstrap: no services listed")
	}
	return m, nil
}

func orderHTTPSFirst(urls []string) []string {
	var https, other []string
	for _, u := range urls {
		if strings.HasPrefix(u, "https://") {
			https = append(https, u)
		} else if strings.HasPrefix(u, "http://") {
			other = append(other, u)
		}
	}
	return append(https, other...)
}

// unwrapURLError drops the URL that net/http puts in its errors.
func unwrapURLError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

type rdapDomain struct {
	Events []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
	} `json:"events"`
}

// Lookup returns the expiry date of a domain (an ASCII name, as produced by Registrable). Errors other than *UnavailableError are transient.
func (c *RDAP) Lookup(ctx context.Context, name string) (Result, error) {
	bases, err := c.serversFor(ctx, TLD(name))
	if err != nil {
		return Result{}, err
	}
	if len(bases) == 0 {
		return Result{}, &UnavailableError{Reason: "The registry for ." + TLD(name) + " domains does not publish expiry dates.", NoRDAP: true}
	}
	var lastErr error
	for _, base := range bases {
		res, err := c.query(ctx, strings.TrimRight(base, "/")+"/domain/"+url.PathEscape(name))
		if err == nil {
			return res, nil
		}
		var un *UnavailableError
		if errors.As(err, &un) {
			return Result{}, err
		}
		lastErr = err
	}
	return Result{}, lastErr
}

func (c *RDAP) query(ctx context.Context, target string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", rdapMediaType)
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("rdap: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Result{}, &UnavailableError{Reason: "The registry has no record of this domain. It may not be registered.", Final: true}
	case resp.StatusCode != http.StatusOK:
		return Result{}, fmt.Errorf("rdap: HTTP %d", resp.StatusCode)
	}
	var d rdapDomain
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRDAPBody)).Decode(&d); err != nil {
		return Result{}, fmt.Errorf("rdap: unreadable response: %w", err)
	}
	for _, e := range d.Events {
		if !strings.EqualFold(e.Action, "expiration") {
			continue
		}
		if t, err := time.Parse(time.RFC3339, e.Date); err == nil {
			return Result{ExpiresAt: t.UTC(), Source: SourceRDAP}, nil
		}
	}
	return Result{}, &UnavailableError{Reason: "The registry does not publish an expiry date for this domain."}
}
