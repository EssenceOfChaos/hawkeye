package sslcheck

import (
	"errors"
	"os"
	"testing"

	"hawkeye/internal/safehttp"
)

// SSLCHECK_LIVE=1 go test ./internal/sslcheck -run Live -v
// Runs the checker against real hosts through the production destination policy and the system trust store. badssl.com is a public
// test bed that keeps a host for each kind of certificate fault. Run it after changing the verification logic: the unit tests use certificates
// this package builds itself, and this is the check that real-world certificates read the same way.
// Run it on Linux (for example in the golang image), which is what production is: macOS's system verifier fetches a missing intermediate
// certificate by itself, so incomplete-chain.badssl.com reads as valid there and the case below fails.
func TestLiveRealCertificates(t *testing.T) {
	if os.Getenv("SSLCHECK_LIVE") == "" {
		t.Skip("set SSLCHECK_LIVE=1 to check real hosts on the internet")
	}
	s := New(safehttp.Config{})
	cases := []struct {
		host   string
		status string
		code   string // a problem that must be reported ("" for none required)
	}{
		{"example.com", "valid", ""},
		{"github.com", "valid", ""},
		{"expired.badssl.com", "expired", "expired"},
		{"wrong.host.badssl.com", "invalid", "hostname_mismatch"},
		{"self-signed.badssl.com", "invalid", "self_signed"},
		{"untrusted-root.badssl.com", "invalid", "untrusted_chain"},
		{"incomplete-chain.badssl.com", "invalid", "untrusted_chain"},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			r, err := s.Inspect(t.Context(), tc.host)
			if err != nil {
				t.Fatalf("%v", err)
			}
			t.Logf("%s: status=%s days=%d trusted=%v match=%v tls=%s issuer=%q problems=%v", tc.host, r.Status, r.DaysRemaining, r.Trusted, r.HostnameMatch, r.TLSVersion, r.Certificate.IssuerName, codes(r))
			if r.Status != tc.status {
				t.Errorf("status = %q, want %q", r.Status, tc.status)
			}
			if tc.code != "" {
				if _, ok := codes(r)[tc.code]; !ok {
					t.Errorf("want problem %q, got %v", tc.code, codes(r))
				}
			}
		})
	}
	for host, want := range map[string]error{"no-such-host-hawkeye-test.example.com": ErrNotFound, "localtest.me": ErrBlocked} {
		if _, err := s.Inspect(t.Context(), host); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", host, err, want)
		}
	}
}
