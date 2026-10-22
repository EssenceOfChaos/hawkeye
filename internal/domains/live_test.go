package domains_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"hawkeye/internal/domains"
	"hawkeye/internal/safehttp"
)

// DOMAINS_LIVE=1 go test ./internal/domains -run Live -v
// Asks real registries through the production SSRF-safe client. Registries change what they publish, so run it now and then, and when a customer reports a
// domain whose expiry HawkEye cannot find. A name that has no date is logged, not failed: "unavailable" is a legitimate answer, a transient error is not.
func TestLiveLookups(t *testing.T) {
	if os.Getenv("DOMAINS_LIVE") == "" {
		t.Skip("set DOMAINS_LIVE=1 to ask real registries")
	}
	look := domains.Lookup{RDAP: domains.NewRDAP(safehttp.New(safehttp.Config{}, 20*time.Second)), WHOIS: domains.NewWHOIS()}
	names := []string{"google.com", "example.org", "wikipedia.org", "bbc.co.uk", "github.io", "nic.io", "yandex.ru", "denic.de", "afnic.fr", "sidn.nl", "nic.ch", "nic.br", "iana.org", "nonexistent-hawkeye-test-12345.com"}
	for _, n := range names {
		name, ok := domains.Registrable(n)
		if !ok {
			t.Logf("%-40s not a registrable domain", n)
			continue
		}
		res, err := look.Lookup(t.Context(), name)
		var un *domains.UnavailableError
		switch {
		case err == nil:
			t.Logf("%-40s expires %s (%s)", name, res.ExpiresAt.Format("2006-01-02"), res.Source)
		case errors.As(err, &un):
			t.Logf("%-40s unavailable: %s", name, un.Reason)
		default:
			t.Errorf("%-40s lookup failed: %v", name, err)
		}
	}
}
