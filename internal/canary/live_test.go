package canary_test

import (
	"os"
	"testing"
	"time"

	"hawkeye/internal/canary"
	"hawkeye/internal/safehttp"
)

// CANARY_LIVE=1 go test ./internal/canary -run Live -v
// Probes the built-in endpoints for real, through the production SSRF-safe client. Run it now and then (and after changing DefaultURLs): the canary is only
// as good as the endpoints it asks, and a retired one would quietly weaken it.
func TestLiveDefaultEndpointsAnswer(t *testing.T) {
	if os.Getenv("CANARY_LIVE") == "" {
		t.Skip("set CANARY_LIVE=1 to probe the real internet")
	}
	var ok, total int
	c := canary.New(safehttp.New(safehttp.Config{}, 6*time.Second), canary.DefaultURLs).WithObserver(func(o, n int, _ bool) { ok, total = o, n })
	if c.Impaired(t.Context()) || ok != total {
		t.Fatalf("%d of %d default canary endpoints answered; replace the ones that do not (DefaultURLs)", ok, total)
	}
	t.Logf("all %d default endpoints answered", total)
}
