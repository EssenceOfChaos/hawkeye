// Package domains tracks when the registrations of an organization's domains expire. Domains are derived from the hosts of its monitors (and may
// be added by hand); a daily job looks each one up (RDAP, falling back to WHOIS) and queues warnings as the expiry date approaches.
package domains

import (
	"net/netip"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"hawkeye/internal/checks"
)

// Registrable returns the registrable domain of a host name or URL ("www.example.co.uk" -> "example.co.uk"), in lower-case ASCII (punycode) form.
// ok is false for IP addresses, single labels, public suffixes themselves, and names under a suffix that is not run by a registry (a name under
// "vercel.app" or "github.io" is not something the customer registered, so its registration is not theirs to renew).
func Registrable(hostOrURL string) (name string, ok bool) {
	host := strings.TrimSpace(hostOrURL)
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil {
			return "", false
		}
		host = u.Hostname()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.ContainsAny(host, "/:@ \t") {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", false
	}
	if _, icann := publicsuffix.PublicSuffix(ascii); !icann {
		return "", false
	}
	name, err = publicsuffix.EffectiveTLDPlusOne(ascii)
	if err != nil {
		return "", false
	}
	return name, true
}

// TLD is the last label of a domain name.
func TLD(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// Warnings use the same schedule as certificate warnings (checks.SSLBuckets): 30, 14, 7 and 1 day(s) before expiry, and bucket 0 once expired.

// Bucket returns the smallest warning bucket the registration is inside at now; ok is false when more than 30 days remain.
func Bucket(expiresAt, now time.Time) (bucket int, ok bool) { return checks.SSLBucket(expiresAt, now) }

// DaysRemaining is the number of whole days left (negative once expired).
func DaysRemaining(expiresAt, now time.Time) int { return checks.SSLDaysRemaining(expiresAt, now) }

// Statuses reported to API clients for a domain.
const (
	StatusPending     = "pending"     // not looked up yet
	StatusUnavailable = "unavailable" // the registry does not publish an expiry date
	StatusValid       = checks.SSLValid
	StatusExpiring    = checks.SSLExpiring
	StatusExpired     = checks.SSLExpired
)

// Status classifies a domain with a known expiry date at now.
func Status(expiresAt, now time.Time) string { return checks.SSLStatus(expiresAt, now) }
