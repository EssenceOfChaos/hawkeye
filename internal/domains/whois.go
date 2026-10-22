package domains

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"hawkeye/internal/safehttp"
)

// WHOIS looks up domain registrations over port 43, for top-level domains that have no RDAP service. Output is free text that differs by registry,
// so only the commonly used expiry fields are understood; anything else is reported as unavailable rather than guessed at.
//
// The WHOIS server of a top-level domain is learned from IANA's own server, so its name is untrusted input: it is resolved here, every address must
// be public, and the connection goes to the validated address (the same rules as the HTTP checks).
type WHOIS struct {
	IANA     string // host:port of the IANA WHOIS server
	Timeout  time.Duration
	Resolver safehttp.Resolver
	AllowIP  func(netip.Addr) bool
	// Dial opens a connection to an already-validated "ip:port" address; nil uses a plain dialer.
	Dial safehttp.DialFunc

	now     func() time.Time
	mu      sync.Mutex
	servers map[string]whoisServer
}

type whoisServer struct {
	host    string // empty: IANA lists none
	fetched time.Time
}

func NewWHOIS() *WHOIS {
	return &WHOIS{IANA: "whois.iana.org:43", Timeout: 10 * time.Second, Resolver: net.DefaultResolver, AllowIP: safehttp.IsPublicIP, now: time.Now}
}

const maxWHOISResponse = 64 << 10

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// query sends one line to a WHOIS server and returns what it answers.
func (w *WHOIS) query(ctx context.Context, hostPort, q string) (string, error) {
	host, port, err := net.SplitHostPort(hostPort)
	if err != nil {
		return "", err
	}
	var addrs []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{ip}
	} else if addrs, err = w.Resolver.LookupNetIP(ctx, "ip", host); err != nil {
		return "", fmt.Errorf("whois: %w", err)
	}
	if len(addrs) == 0 {
		return "", errors.New("whois: server has no address")
	}
	for _, a := range addrs { // one prohibited answer blocks the server
		if !w.AllowIP(a) {
			return "", fmt.Errorf("whois: %w", safehttp.ErrBlocked)
		}
	}
	dial := w.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: w.Timeout}).DialContext
	}
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	conn, err := dial(ctx, "tcp", net.JoinHostPort(addrs[0].String(), port))
	if err != nil {
		return "", fmt.Errorf("whois: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(w.Timeout))
	if _, err := io.WriteString(conn, q+"\r\n"); err != nil {
		return "", fmt.Errorf("whois: %w", err)
	}
	b, err := io.ReadAll(io.LimitReader(bufio.NewReader(conn), maxWHOISResponse))
	if err != nil && len(b) == 0 {
		return "", fmt.Errorf("whois: %w", err)
	}
	return string(b), nil
}

// serverFor asks IANA which server answers for a top-level domain. The answer is cached for a day.
func (w *WHOIS) serverFor(ctx context.Context, tld string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s, ok := w.servers[tld]; ok && w.now().Sub(s.fetched) < bootstrapTTL {
		return s.host, nil
	}
	out, err := w.query(ctx, w.IANA, tld)
	if err != nil {
		return "", err
	}
	host := ""
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "whois") {
			host = strings.ToLower(strings.TrimSpace(v))
			break
		}
	}
	if host != "" && !hostnameRE.MatchString(host) {
		host = ""
	}
	if w.servers == nil {
		w.servers = map[string]whoisServer{}
	}
	w.servers[tld] = whoisServer{host: host, fetched: w.now()}
	return host, nil
}

// Lookup returns the expiry date of a domain. Errors other than *UnavailableError are transient.
func (w *WHOIS) Lookup(ctx context.Context, name string) (Result, error) {
	tld := TLD(name)
	host, err := w.serverFor(ctx, tld)
	if err != nil {
		return Result{}, err
	}
	if host == "" {
		return Result{}, &UnavailableError{Reason: "The registry for ." + tld + " domains does not publish expiry dates.", NoRDAP: true}
	}
	out, err := w.query(ctx, host+":43", name)
	if err != nil {
		return Result{}, err
	}
	if t, ok := ParseWHOISExpiry(out); ok {
		return Result{ExpiresAt: t, Source: SourceWHOIS}, nil
	}
	return Result{}, &UnavailableError{Reason: "The registry does not publish an expiry date for this domain."}
}

// whoisExpiryKeys are the field names registries use for the expiry date, compared case-insensitively.
var whoisExpiryKeys = map[string]bool{
	"registry expiry date":                   true,
	"registrar registration expiration date": true,
	"registrar expiration date":              true,
	"domain expiration date":                 true,
	"expiration date":                        true,
	"expiry date":                            true,
	"expires on":                             true,
	"expires":                                true,
	"expire date":                            true,
	"record expires on":                      true,
	"paid-till":                              true,
	"expire":                                 true,
	"expiry":                                 true,
}

var whoisLayouts = []string{
	time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05 MST", "2006-01-02 15:04:05",
	"2006-01-02", "2006.01.02", "2006/01/02", "02-Jan-2006", "02-Jan-2006 15:04:05 MST", "02-Jan-2006 15:04:05", "2 January 2006", "January 2 2006",
	"Jan 2 2006", "2006-Jan-02", "20060102",
}

// ParseWHOISExpiry finds the expiry date in a WHOIS response. It reads "Key: value" lines and takes the first expiry field that holds a date.
func ParseWHOISExpiry(text string) (time.Time, bool) {
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || !whoisExpiryKeys[strings.ToLower(strings.TrimSpace(k))] {
			continue
		}
		if t, ok := parseWHOISDate(strings.TrimSpace(v)); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

func parseWHOISDate(v string) (time.Time, bool) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "."))
	if v == "" {
		return time.Time{}, false
	}
	// Some registries append notes: "2027-01-31 (YYYY-MM-DD)" or "31-Jan-2027 00:00:00 UTC".
	candidates := []string{v}
	if f := strings.Fields(v); len(f) > 1 {
		candidates = append(candidates, f[0], f[0]+" "+f[1])
		if len(f) > 2 {
			candidates = append(candidates, f[0]+" "+f[1]+" "+f[2])
		}
	}
	for _, c := range candidates {
		for _, layout := range whoisLayouts {
			if t, err := time.Parse(layout, c); err == nil {
				if t.Year() < 1990 || t.Year() > 2200 {
					continue
				}
				return t.UTC(), true
			}
		}
	}
	return time.Time{}, false
}
