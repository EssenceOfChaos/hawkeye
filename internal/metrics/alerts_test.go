package metrics_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"hawkeye/internal/metrics"
)

// An alert rule that names a metric nobody exports never fires and never errors. This test fails when the rules file and the code drift apart.
func TestEveryMetricInTheAlertRulesIsActuallyExported(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/prometheus-alerts.yml")
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{}
	for _, m := range regexp.MustCompile(`hawkeye_[a-z0-9_]+`).FindAllString(string(raw), -1) {
		wanted[strings.TrimSuffix(m, "_bucket")] = true
	}
	if len(wanted) < 15 {
		t.Fatalf("suspiciously few metrics found in the rules file: %d", len(wanted))
	}

	app, api := metrics.New(), metrics.NewAPI()
	// Vector metrics are only exported once a label combination exists; touch one of each.
	app.ChecksTotal.WithLabelValues("failure", "dns")
	app.QueueDepth.WithLabelValues("queued")
	app.NotificationsTotal.WithLabelValues("outage", "sent")
	app.RetentionDeleted.WithLabelValues("check_results")
	app.RetentionRuns.WithLabelValues("ok")
	app.BillingEvents.WithLabelValues("received")
	app.BillingRisk.WithLabelValues("charge.refunded")
	app.WorkerErrors.WithLabelValues("claim")
	api.Observe("GET", "/x", 200, 0)
	api.RateLimited.WithLabelValues("global")
	api.EmailSkips.WithLabelValues("forgot")
	pool, err := pgxpool.New(t.Context(), "postgres://u:p@127.0.0.1:1/none") // connects lazily, so this needs no database
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	metrics.RegisterPool(api.Registry, pool)
	app.LastPollUnix.Set(1)
	app.RetentionLastSuccess.Set(1)
	api.App.BillingFailed.Set(0)

	exported := map[string]bool{}
	for _, reg := range []*prometheus.Registry{app.Registry, api.Registry} {
		fams, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fams {
			exported[f.GetName()] = true
		}
	}
	for name := range wanted {
		if !exported[name] {
			t.Errorf("alert rules reference %q, which is not exported by the API or the worker", name)
		}
	}
}
