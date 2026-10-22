package domains

import (
	"context"
	"errors"
)

// Looker finds when a domain's registration expires. A *UnavailableError means no expiry date can be had; any other error is transient.
type Looker interface {
	Lookup(ctx context.Context, name string) (Result, error)
}

// Lookup asks the top-level domain's RDAP server and falls back to WHOIS where RDAP has nothing to offer (no RDAP service, no expiry date in
// the record, or the RDAP server failing).
type Lookup struct {
	RDAP  Looker
	WHOIS Looker // nil disables the fallback
}

func (l Lookup) Lookup(ctx context.Context, name string) (Result, error) {
	res, rerr := l.RDAP.Lookup(ctx, name)
	if rerr == nil {
		return res, nil
	}
	var un *UnavailableError
	rdapUnavailable := errors.As(rerr, &un)
	if (rdapUnavailable && un.Final) || l.WHOIS == nil {
		return Result{}, rerr
	}
	res, werr := l.WHOIS.Lookup(ctx, name)
	if werr == nil {
		return res, nil
	}
	if !rdapUnavailable {
		return Result{}, rerr // RDAP failed; "unavailable" would be a guess
	}
	var wun *UnavailableError
	if un.NoRDAP && errors.As(werr, &wun) {
		return Result{}, werr // WHOIS is the only protocol this registry offers, so its reason is the useful one
	}
	return Result{}, rerr
}
