package statuspagedomains

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"
)

// TXTPrefix is the label in front of the claimed name that holds the verification record: _hawkeye.status.clientbrand.com.
const TXTPrefix = "_hawkeye."

// txtValuePrefix begins the TXT record's value, so the record says what it is for when someone reads the zone later.
const txtValuePrefix = "hawkeye-verify="

// Resolver looks up the records verification needs. *net.Resolver satisfies it.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Verifier checks a claimed host name's two records. Target is the name customers point their CNAME at (domains.hawkeye.observer).
type Verifier struct {
	Resolver Resolver
	Target   string
	Timeout  time.Duration // for each lookup; 5 seconds when zero
}

// Result is one verification. Owned: the TXT record holds the claim's token. Routed: the name reaches HawkEye. Problem says, for the owner, what is
// wrong (empty when both are fine). Transient means the lookups failed rather than answered (a timeout or a resolver error), so it says nothing about
// the records and must not count against an active domain.
type Result struct {
	Owned     bool
	Routed    bool
	Transient bool
	Problem   string
}

// OK reports whether the domain may be served.
func (r Result) OK() bool { return r.Owned && r.Routed }

// TXTValue is the value the owner puts in the TXT record for a claim.
func TXTValue(token string) string { return txtValuePrefix + token }

// Verify looks up both records of host. The returned error is non-nil only when ctx itself ended; every DNS outcome is in the Result.
func (v *Verifier) Verify(ctx context.Context, host, token string) (Result, error) {
	var r Result
	owned, ownErr := v.owned(ctx, host, token)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	routed, routeErr := v.routed(ctx, host)
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	r.Owned, r.Routed = owned, routed
	target := strings.TrimSuffix(v.Target, ".")
	// The ownership record is reported first: it is the one people forget, and until it is right the routing record does not matter.
	switch {
	case isTransient(ownErr) || isTransient(routeErr):
		r.Transient = true
		r.Problem = "DNS lookups for " + host + " didn't get an answer. HawkEye will try again."
	case !owned && isNotFound(ownErr):
		r.Problem = "The TXT record " + TXTPrefix + host + " wasn't found. DNS changes can take up to an hour to appear."
	case !owned:
		r.Problem = "The TXT record " + TXTPrefix + host + " doesn't contain this domain's verification value."
	case !routed && isNotFound(routeErr):
		r.Problem = host + " doesn't point anywhere yet. Add a CNAME record pointing to " + target + "."
	case !routed:
		r.Problem = host + " points somewhere other than HawkEye. Its CNAME record should point to " + target + "."
	}
	return r, nil
}

func (v *Verifier) lookupCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	d := v.Timeout
	if d <= 0 {
		d = 5 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// owned reports whether any TXT record at _hawkeye.<host> is this claim's value. A name commonly has several TXT records, so one match is enough.
func (v *Verifier) owned(ctx context.Context, host, token string) (bool, error) {
	c, cancel := v.lookupCtx(ctx)
	defer cancel()
	records, err := v.Resolver.LookupTXT(c, TXTPrefix+host)
	if err != nil {
		return false, err
	}
	want := []byte(TXTValue(token))
	for _, rec := range records {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(rec)), want) == 1 {
			return true, nil
		}
	}
	return false, nil
}

// routed reports whether host reaches HawkEye: its CNAME chain ends at the target, or, for DNS providers that flatten CNAMEs into addresses
// (Cloudflare's proxy off, some apex-style setups), every address it resolves to is one of the target's. Every address, not some: otherwise part
// of the visitors would reach another server.
func (v *Verifier) routed(ctx context.Context, host string) (bool, error) {
	target := strings.ToLower(strings.TrimSuffix(v.Target, "."))
	c, cancel := v.lookupCtx(ctx)
	defer cancel()
	if cname, err := v.Resolver.LookupCNAME(c, host); err == nil && strings.ToLower(strings.TrimSuffix(cname, ".")) == target {
		return true, nil
	}
	c2, cancel2 := v.lookupCtx(ctx)
	defer cancel2()
	got, err := v.Resolver.LookupNetIP(c2, "ip", host)
	if err != nil {
		return false, err
	}
	c3, cancel3 := v.lookupCtx(ctx)
	defer cancel3()
	ours, err := v.Resolver.LookupNetIP(c3, "ip", target)
	if err != nil {
		return false, err
	}
	if len(got) == 0 {
		return false, nil
	}
	set := make(map[netip.Addr]bool, len(ours))
	for _, a := range ours {
		set[a.Unmap()] = true
	}
	for _, a := range got {
		if !set[a.Unmap()] {
			return false, nil
		}
	}
	return true, nil
}

func isNotFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

func isTransient(err error) bool {
	if err == nil || isNotFound(err) {
		return false
	}
	var de *net.DNSError
	if errors.As(err, &de) {
		return de.IsTimeout || de.IsTemporary
	}
	return errors.Is(err, context.DeadlineExceeded)
}
