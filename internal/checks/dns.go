package checks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"hawkeye/internal/safehttp"
)

// Record types a DNS monitor can look up. CNAME is left out on purpose: the resolver only reports where a chain of CNAMEs ends, so a comparison with the
// name a customer configured would fail whenever a CDN adds a hop of its own. An A or AAAA check covers those names.
const (
	RecordA    = "A"
	RecordAAAA = "AAAA"
	RecordMX   = "MX"
	RecordNS   = "NS"
	RecordTXT  = "TXT"
)

// RecordTypes lists the accepted record types.
var RecordTypes = []string{RecordA, RecordAAAA, RecordMX, RecordNS, RecordTXT}

// MaxDNSExpected bounds the value a DNS monitor compares answers with.
const MaxDNSExpected = 255

// DNSTarget describes one DNS check. Host has passed publichost.ParseDNSName. An empty Expected means any answer will do.
type DNSTarget struct {
	Host       string
	RecordType string
	Expected   string
	Timeout    time.Duration
}

// DNSResolver looks up DNS records. *net.Resolver satisfies it.
type DNSResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupNS(ctx context.Context, name string) ([]*net.NS, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// WithDNSResolver replaces the system resolver. Production leaves it alone; tests use it for fake answers.
func (c *Checker) WithDNSResolver(r DNSResolver) *Checker {
	c.dns = r
	return c
}

// CheckDNS looks up a record, with the same contract as Check: a failed lookup or a wrong answer returns a Result with Success=false, and the error is
// non-nil only when ctx itself was canceled.
//
// Success rule: the lookup must return at least one answer, and with an expected value at least one answer must match it, because a host name commonly has
// several addresses, mail servers or TXT records. ResponseTimeMs is the lookup time, which is short when the resolver has the answer cached.
func (c *Checker) CheckDNS(ctx context.Context, t DNSTarget) (Result, error) {
	res := Result{CheckedAt: c.now()}
	lookupCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	answers, err := c.lookupRecords(lookupCtx, t)
	res.FinishedAt = c.now()
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err() // caller shut down; the outcome says nothing about the record
		}
		res.ErrorCategory, res.ErrorSummary = classifyDNS(err, t)
		return res, nil
	}
	elapsed := int(res.FinishedAt.Sub(res.CheckedAt) / time.Millisecond)
	res.ResponseTimeMs = &elapsed
	if t.Expected != "" && !answerMatches(t.RecordType, t.Expected, answers) {
		// Summaries name the customer's own expected value, never the answers: they are stored and emailed, like assertion failures.
		res.ErrorCategory = CatAssertion
		res.ErrorSummary = truncate(fmt.Sprintf("%s record for %s does not match %q", t.RecordType, t.Host, t.Expected), maxSummaryLen)
		return res, nil
	}
	res.Success = true
	return res, nil
}

// lookupRecords returns the answers in the form NormalizeDNSExpected gives expected values. The trailing dot makes the name fully qualified, so the
// resolver's search domains can never turn it into a name on this server's own network.
func (c *Checker) lookupRecords(ctx context.Context, t DNSTarget) ([]string, error) {
	name := t.Host + "."
	var answers []string
	switch t.RecordType {
	case RecordA, RecordAAAA:
		network := "ip4"
		if t.RecordType == RecordAAAA {
			network = "ip6"
		}
		ips, err := c.dns.LookupNetIP(ctx, network, name)
		if err != nil {
			return nil, err
		}
		allowed := c.cfg.AllowIP
		if allowed == nil {
			allowed = safehttp.IsPublicIP
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			// A public name that points at a private address is treated as it is for a website: the check says no more than that, so a monitor cannot be
			// used to learn what exists on this server's network.
			if !allowed(ip) {
				return nil, safehttp.ErrBlocked
			}
			answers = append(answers, ip.String())
		}
	case RecordMX:
		// A record with an invalid name is dropped and reported as an error alongside the valid ones, which are still a usable answer.
		records, err := c.dns.LookupMX(ctx, name)
		if err != nil && len(records) == 0 {
			return nil, err
		}
		for _, r := range records {
			answers = append(answers, normalizeDNSName(r.Host))
		}
	case RecordNS:
		records, err := c.dns.LookupNS(ctx, name)
		if err != nil && len(records) == 0 {
			return nil, err
		}
		for _, r := range records {
			answers = append(answers, normalizeDNSName(r.Host))
		}
	case RecordTXT:
		records, err := c.dns.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		answers = records
	default:
		return nil, fmt.Errorf("unknown record type %q", t.RecordType)
	}
	if len(answers) == 0 {
		return nil, &net.DNSError{Err: "no records", Name: t.Host, IsNotFound: true}
	}
	return answers, nil
}

// classifyDNS is Classify, with a summary that says which record is missing.
func classifyDNS(err error, t DNSTarget) (category, summary string) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return CatDNS, truncate(fmt.Sprintf("No %s record found for %s", t.RecordType, t.Host), maxSummaryLen)
	}
	return Classify(err)
}

// answerMatches compares answers with the expected value: exactly for addresses and host names, and as a part of the text for TXT records, which often
// carry more than the one setting being watched.
func answerMatches(recordType, expected string, answers []string) bool {
	if recordType == RecordTXT {
		return slices.ContainsFunc(answers, func(a string) bool { return strings.Contains(a, expected) })
	}
	return slices.Contains(answers, expected)
}

func normalizeDNSName(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// NormalizeDNSExpected validates a DNS monitor's expected value for its record type and returns its stored form, or a reason it is not acceptable. An empty
// value is valid and means any answer will do.
func NormalizeDNSExpected(recordType, value string) (normalized, reason string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if utf8.RuneCountInString(value) > MaxDNSExpected {
		return "", fmt.Sprintf("must be at most %d characters", MaxDNSExpected)
	}
	switch recordType {
	case RecordA, RecordAAAA:
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Is4() != (recordType == RecordA) || ip.Zone() != "" {
			if recordType == RecordA {
				return "", "must be an IPv4 address such as 203.0.113.10"
			}
			return "", "must be an IPv6 address such as 2001:db8::1"
		}
		if !safehttp.IsPublicIP(ip) {
			return "", "must be a public address: private addresses are never accepted as an answer"
		}
		return ip.String(), ""
	case RecordMX, RecordNS:
		name := normalizeDNSName(value)
		if name == "" || strings.ContainsAny(name, " \t/:@") {
			return "", "must be a host name such as mail.example.com"
		}
		return name, ""
	}
	if !validDNSText(value) {
		return "", "must be text without line breaks"
	}
	return value, ""
}

func validDNSText(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < ' ' || r == 0x7f })
}
