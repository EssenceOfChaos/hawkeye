package monitors

import (
	"net/netip"
	"net/url"
	"strings"
)

const maxURLLen = 2048

// NormalizeURL validates and canonicalises a monitor URL. It returns a human-readable reason on failure.
//
// This is input hygiene and early feedback only. It is NOT the SSRF control: hostnames are resolved
// and every dialed address is validated by the safe HTTP transport at check time (Phase 4).
func NormalizeURL(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLen {
		return "", "must be a URL of at most 2048 characters"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "must be a valid URL"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "must start with http:// or https://"
	}
	if u.User != nil {
		return "", "must not contain credentials"
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || u.Opaque != "" {
		return "", "must include a host name"
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return "", "only the default ports 80 and 443 are supported"
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "", "must be a publicly reachable host"
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
			ip.IsMulticast() || ip.IsUnspecified() {
			return "", "must be a publicly reachable address"
		}
	}
	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	return u.String(), ""
}
