// Package statuspagedomains serves status pages on a customer's own domain (Agency): the owner claims a subdomain for a page, proves control of
// it with a TXT record, routes it to HawkEye with a CNAME, and the page is served there once both are seen. Design and threat model:
// docs/plan-status-page-domains.md.
package statuspagedomains

import (
	"errors"
	"strings"

	"golang.org/x/net/publicsuffix"

	"hawkeye/internal/publichost"
	"hawkeye/internal/validation"
)

// ParseHostname reads the host name an owner typed ("status.clientbrand.com", or a URL of it) and decides whether it can be claimed. It must be a
// public name below a registered domain: an apex (clientbrand.com) cannot have a CNAME, so it is refused with a suggestion. reserved lists HawkEye's
// own names; they and every name below them are refused.
func ParseHostname(raw string, reserved ...string) (string, error) {
	t, err := publichost.Parse(raw)
	if err != nil {
		reason := "That does not look like a domain name."
		if ih := (*publichost.InvalidHostError)(nil); errors.As(err, &ih) {
			reason = ih.Reason
		}
		return "", validation.New("hostname", reason)
	}
	host := t.Host
	for _, r := range reserved {
		r = strings.ToLower(strings.TrimSuffix(r, "."))
		if r != "" && (host == r || strings.HasSuffix(host, "."+r)) {
			return "", validation.New("hostname", "That name belongs to HawkEye. Use a name on your own domain, such as status.yourbrand.com.")
		}
	}
	registered, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		// The name is itself a public suffix (com, co.uk, github.io): nobody can own it.
		return "", validation.New("hostname", "Enter a name on a domain you own, such as status.yourbrand.com.")
	}
	if registered == host {
		return "", validation.New("hostname", "Use a subdomain, such as status."+host+". A domain's own name can't point to HawkEye with a CNAME record.")
	}
	return host, nil
}
