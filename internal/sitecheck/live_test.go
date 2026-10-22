package sitecheck

import (
	"errors"
	"os"
	"testing"

	"hawkeye/internal/safehttp"
)

// SITECHECK_LIVE=1 go test ./internal/sitecheck -run Live -v
// Asks about real hosts through the production destination policy and the system trust store. Run it on Linux (as production is) after changing
// the classification: the unit tests use servers this package builds itself.
func TestLiveRealHosts(t *testing.T) {
	if os.Getenv("SITECHECK_LIVE") == "" {
		t.Skip("set SITECHECK_LIVE=1 to check real hosts on the internet")
	}
	s := New(safehttp.Config{})
	cases := []struct{ host, verdict, reason string }{
		{"example.com", VerdictUp, ReasonOK},
		{"https://www.example.com/some/missing/page?x=1", VerdictUp, ReasonOK}, // only the host is used, so the path cannot make it a 404
		{"expired.badssl.com", VerdictDown, ReasonTLS},
		{"self-signed.badssl.com", VerdictDown, ReasonTLS},
		{"no-such-host-hawkeye-test.example.com", VerdictDown, ReasonDNS},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			r, err := s.Inspect(t.Context(), tc.host)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: verdict=%s reason=%s status=%v ms=%v speed=%s final=%q https_error=%q", tc.host, r.Verdict, r.Reason, deref(r.StatusCode), deref(r.ResponseTimeMs), r.Speed, r.FinalURL, r.HTTPSError)
			if r.Verdict != tc.verdict || r.Reason != tc.reason {
				t.Errorf("got %s/%s, want %s/%s", r.Verdict, r.Reason, tc.verdict, tc.reason)
			}
		})
	}
	if _, err := s.Inspect(t.Context(), "localtest.me"); !errors.Is(err, ErrBlocked) {
		t.Errorf("localtest.me resolves to 127.0.0.1 and must be refused: %v", err)
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
