// Package safehttp builds HTTP clients that refuse to reach private, loopback, link-local,
// metadata and other non-public destinations. It is the SSRF boundary of the monitoring engine.
package safehttp

import "net/netip"

// blockedPrefixes lists ranges that are not valid public unicast destinations even though the
// standard library does not classify them as private/loopback/link-local.
var blockedPrefixes = mustPrefixes(
	// IPv4
	"0.0.0.0/8",       // "this network"
	"100.64.0.0/10",   // carrier-grade NAT; also used by some cloud metadata services (e.g. 100.100.100.200)
	"169.254.0.0/16",  // link-local, includes the 169.254.169.254 cloud metadata endpoint
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay anycast
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes the broadcast address
	// IPv6
	"::/96",          // unspecified, loopback and deprecated IPv4-compatible addresses
	"64:ff9b::/96",   // NAT64: embeds an IPv4 address that could be internal
	"64:ff9b:1::/48", // local-use NAT64
	"100::/64",       // discard-only
	"2001::/23",      // IETF protocol assignments, includes Teredo (2001::/32)
	"2001:db8::/32",  // documentation
	"2002::/16",      // 6to4: embeds an IPv4 address
	"fc00::/7",       // unique local, includes fd00:ec2::254 (AWS IPv6 metadata)
	"fe80::/10",      // link-local
	"fec0::/10",      // deprecated site-local
	"ff00::/8",       // multicast
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// IsPublicIP reports whether addr is a globally routable unicast address that may be contacted.
// IPv4-mapped IPv6 addresses (::ffff:a.b.c.d) are judged by their embedded IPv4 address.
func IsPublicIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsMulticast() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() {
		return false
	}
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// IsAllowedPort permits only the default web ports; other ports are an extra abuse path
// (port scanning of public hosts, reaching non-HTTP services).
func IsAllowedPort(port int) bool { return port == 80 || port == 443 }
