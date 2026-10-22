// Package publichost reads what a visitor typed into a free tool ("Example.com", "https://example.com/path?x=1") and decides whether it is a host
// name the tool may look at. It accepts only registered-looking public host names: no IP addresses, no credentials, no ports other than the
// web ports, no names that only exist inside private networks.
//
// This is input hygiene and early feedback, not the SSRF control: the connection itself goes through safehttp, which resolves the name and
// refuses any non-public address whatever the name looks like.
package publichost

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

const maxInputLen = 2048

// InvalidHostError means the input is not a host name a tool will look at. Reason is written for the visitor.
type InvalidHostError struct{ Reason string }

func (e *InvalidHostError) Error() string { return "invalid host: " + e.Reason }

var labelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Name suffixes that only exist on private networks. This is early feedback, not the SSRF control: the dialer refuses any host that
// resolves to a non-public address, whatever it is called.
var privateSuffixes = []string{".localhost", ".local", ".internal", ".localdomain", ".lan", ".home.arpa"}

// Target is what a visitor asked about: the host, the scheme and port they gave (empty when they gave none).
type Target struct {
	Host   string // lower-case ASCII (punycode for international names), no trailing dot
	Scheme string // "http", "https", or "" when none was typed
	Port   string // "80", "443" or "" when none was typed
}

// Parse reads a visitor's input: a bare domain, or a URL (whose path, query and fragment are ignored).
func Parse(raw string) (Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Target{}, &InvalidHostError{"Enter a domain name, for example example.com."}
	}
	if len(raw) > maxInputLen {
		return Target{}, &InvalidHostError{"That is too long to be a domain name."}
	}
	u, err := url.Parse(withScheme(raw))
	if err != nil || u.Host == "" {
		return Target{}, &InvalidHostError{"That does not look like a domain name."}
	}
	if u.User != nil {
		return Target{}, &InvalidHostError{"Enter the domain name only, without a user name or password."}
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return Target{}, &InvalidHostError{"Enter a domain name, for example example.com."}
	}
	if !strings.Contains(raw, "://") {
		scheme = "" // withScheme added one; the visitor did not choose it
	}
	port := u.Port()
	if port != "" && port != "80" && port != "443" {
		return Target{}, &InvalidHostError{"Only the standard web ports (80 and 443) can be checked."}
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if strings.ContainsAny(host, "*%[]:") {
		return Target{}, &InvalidHostError{"Enter a single domain name, without wildcards."}
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return Target{}, &InvalidHostError{"That does not look like a valid domain name."}
	}
	if len(ascii) > 253 {
		return Target{}, &InvalidHostError{"That is too long to be a domain name."}
	}
	labels := strings.Split(ascii, ".")
	if len(labels) < 2 {
		return Target{}, &InvalidHostError{"Enter a full domain name, for example example.com."}
	}
	for _, l := range labels {
		if !labelRE.MatchString(l) {
			return Target{}, &InvalidHostError{"That does not look like a valid domain name."}
		}
	}
	// A top-level label with no letter means an IP address (1.2.3.4, or a decimal or hex form), which is not a name.
	if !strings.ContainsAny(labels[len(labels)-1], "abcdefghijklmnopqrstuvwxyz") {
		return Target{}, &InvalidHostError{"Enter a domain name, not an IP address."}
	}
	for _, sfx := range privateSuffixes {
		if strings.HasSuffix(ascii, sfx) || ascii == sfx[1:] {
			return Target{}, &InvalidHostError{"That name is only used inside private networks, so it cannot be checked from the internet."}
		}
	}
	return Target{Host: ascii, Scheme: scheme, Port: port}, nil
}

// withScheme lets "example.com/path" parse as a host. A "scheme://" prefix of any kind is kept, so "ftp://example.com" is refused above
// rather than being reinterpreted.
func withScheme(raw string) string {
	if strings.Contains(raw, "://") {
		return raw
	}
	return fmt.Sprintf("https://%s", raw)
}
