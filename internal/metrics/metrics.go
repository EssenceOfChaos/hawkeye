// Package metrics defines the Prometheus metrics of the monitoring engine.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry *prometheus.Registry

	ChecksTotal   *prometheus.CounterVec // result=success|failure|inconclusive, category (inconclusive: dropped because the canary says our own network is down)
	CanaryUp      prometheus.Gauge       // 1 while this worker can reach independent public sites, 0 while it cannot
	CheckDuration prometheus.Histogram
	Rechecks      *prometheus.CounterVec // result=failed|passed: immediate re-checks after a failure ("passed" is a false alarm avoided)

	Scheduled    prometheus.Counter // jobs enqueued
	Skipped      prometheus.Counter // monitor was due but its previous job had not finished
	Missed       prometheus.Counter // a check ran at least one full interval late
	Backpressure prometheus.Counter // ticks that scheduled nothing because the queue was full
	SchedulerLag prometheus.Gauge   // age of the most overdue monitor, seconds

	QueueDepth      *prometheus.GaugeVec // state=queued|running
	OldestQueuedAge prometheus.Gauge
	JobsReclaimed   prometheus.Counter // jobs picked up after a lease expired or a retry
	JobsDropped     prometheus.Counter // jobs discarded after exhausting attempts

	NotificationsTotal   *prometheus.CounterVec // event=outage|recovery, result=sent|retry|failed|skipped
	NotificationGroups   *prometheus.CounterVec // event: messages that combined several alerts to one destination (grouping)
	NotificationsPending prometheus.Gauge
	NotificationsOldest  prometheus.Gauge

	DomainLookups          *prometheus.CounterVec // result=known|unavailable|error
	StatusPageDomainChecks *prometheus.CounterVec // result=ok|problem|no_answer|error: re-checks of status pages' custom domains
	LifecycleEmails        *prometheus.CounterVec // kind=no_monitor_*|weekly_summary, result=sent|refused|failed
	MonthlyReportEmails    *prometheus.CounterVec // result=sent|refused|failed
	RetentionDeleted       *prometheus.CounterVec // kind=check_results|check_rollups|incidents|monitors|sessions|tokens|deploys|…
	RetentionRuns          *prometheus.CounterVec // result=ok|error|skipped
	RetentionErrors        prometheus.Counter
	RetentionLastSuccess   prometheus.Gauge
	RetentionDuration      prometheus.Gauge
	RollupLag              prometheus.Gauge // how far the hourly result totals are behind: now minus rollup_state.through

	BillingEvents        *prometheus.CounterVec // result=received|duplicate|invalid_signature|processed|ignored|retry|failed
	BillingRisk          *prometheus.CounterVec // type=charge.dispute.created|charge.refunded|radar.early_fraud_warning.created
	BillingPending       prometheus.Gauge
	BillingOldest        prometheus.Gauge
	BillingFailed        prometheus.Gauge
	BillingPriceMismatch *prometheus.GaugeVec // plan: 1 while the Stripe price does not match the plan advertised on the site (checkout for it is refused)

	AdConversions *prometheus.CounterVec // result=sent|rejected|retry|failed|expired: events sent to OpenAI Ads' Conversions API (internal/adconversions)

	WorkerErrors *prometheus.CounterVec // stage=claim|load|record|complete|sample|schedule|secrets|rollup
	LastPollUnix prometheus.Gauge
}

// New builds the application metrics on their own registry (worker process).
func New() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return newOn(r)
}

// newOn registers the application metrics (checks, queue, notifications, billing, retention) on r.
func newOn(r *prometheus.Registry) *Metrics {
	f := func(c prometheus.Collector) { r.MustRegister(c) }

	m := &Metrics{
		Registry: r,
		ChecksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_checks_total",
			Help: "Completed checks by result and error category."}, []string{"result", "category"}),
		CheckDuration: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "hawkeye_check_duration_seconds",
			Help: "Wall time of completed checks.", Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30}}),
		Rechecks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_rechecks_total",
			Help: "Immediate re-checks after a failure, by result (failed: confirmed the failure; passed: a false alarm avoided)."}, []string{"result"}),
		Scheduled:       prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_jobs_scheduled_total", Help: "Check jobs enqueued."}),
		Skipped:         prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_skipped_total", Help: "Due monitors skipped because a previous job was still outstanding."}),
		Missed:          prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_missed_checks_total", Help: "Checks scheduled at least one interval late."}),
		Backpressure:    prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_backpressure_total", Help: "Scheduler ticks that enqueued nothing because the queue was full."}),
		SchedulerLag:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_scheduler_lag_seconds", Help: "Age of the most overdue monitor."}),
		QueueDepth:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hawkeye_queue_depth", Help: "Jobs in the queue by state."}, []string{"state"}),
		OldestQueuedAge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_queue_oldest_job_age_seconds", Help: "Age of the oldest job waiting to run."}),
		JobsReclaimed:   prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_jobs_reclaimed_total", Help: "Jobs claimed again after a lease expired or a retry."}),
		JobsDropped:     prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_jobs_dropped_total", Help: "Jobs dropped after exhausting their attempts."}),
		NotificationGroups: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_notification_groups_total",
			Help: "Alert messages that combined several alerts of one kind to one destination."}, []string{"event"}),
		NotificationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_notification_deliveries_total",
			Help: "Alert delivery attempts by event and result."}, []string{"event", "result"}),
		NotificationsPending: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_notification_pending", Help: "Alert deliveries waiting to be sent."}),
		NotificationsOldest:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_notification_oldest_pending_seconds", Help: "Age of the oldest due alert delivery."}),
		DomainLookups: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_domain_lookups_total",
			Help: "Domain expiry lookups by result (known, unavailable, error)."}, []string{"result"}),
		StatusPageDomainChecks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_status_page_domain_checks_total",
			Help: "Checks of status pages' custom domains by result (ok, problem, no_answer, error)."}, []string{"result"}),
		MonthlyReportEmails:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_monthly_report_emails_total", Help: "Monthly report emails by result."}, []string{"result"}),
		LifecycleEmails:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_lifecycle_emails_total", Help: "Onboarding and weekly summary emails by kind and result."}, []string{"kind", "result"}),
		RetentionDeleted:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_retention_deleted_rows_total", Help: "Rows removed by retention, by kind."}, []string{"kind"}),
		RetentionRuns:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_retention_runs_total", Help: "Retention passes by result."}, []string{"result"}),
		RetentionErrors:      prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_retention_errors_total", Help: "Failed retention steps."}),
		RollupLag:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_rollup_lag_seconds", Help: "How far the hourly check result totals are behind (reads fall back to raw results meanwhile)."}),
		RetentionLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_retention_last_success_timestamp_seconds", Help: "Unix time of the last successful retention pass."}),
		RetentionDuration:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_retention_last_duration_seconds", Help: "Duration of the last successful retention pass."}),
		BillingEvents:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_billing_webhook_events_total", Help: "Billing webhook events by result."}, []string{"result"}),
		BillingRisk:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_billing_risk_events_total", Help: "Disputes, refunds and fraud warnings received; each needs human review."}, []string{"type"}),
		BillingPending:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_events_pending", Help: "Stored billing events awaiting processing."}),
		BillingOldest:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_oldest_pending_seconds", Help: "Age of the oldest unprocessed billing event."}),
		BillingPriceMismatch: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hawkeye_billing_price_mismatch", Help: "1 while the Stripe price for a plan does not match the advertised price, so its checkout is refused."}, []string{"plan"}),
		BillingFailed:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_events_failed", Help: "Billing events that gave up and need manual attention."}),
		AdConversions: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_ad_conversions_total",
			Help: "Conversion events sent to OpenAI Ads from the server, by result."}, []string{"result"}),
		WorkerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_worker_errors_total",
			Help: "Internal errors by stage."}, []string{"stage"}),
		CanaryUp:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_canary_up", Help: "1 if this worker reaches independent public sites (the canary), 0 if its own network looks impaired."}),
		LastPollUnix: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_worker_last_poll_timestamp_seconds", Help: "Unix time of the last queue poll."}),
	}
	for _, c := range []prometheus.Collector{m.ChecksTotal, m.CheckDuration, m.Rechecks, m.Scheduled, m.Skipped, m.Missed, m.Backpressure,
		m.SchedulerLag, m.QueueDepth, m.OldestQueuedAge, m.JobsReclaimed, m.JobsDropped, m.WorkerErrors, m.LastPollUnix, m.CanaryUp, m.NotificationsTotal, m.NotificationGroups, m.NotificationsPending, m.NotificationsOldest, m.DomainLookups, m.StatusPageDomainChecks, m.LifecycleEmails, m.MonthlyReportEmails, m.RetentionDeleted, m.RetentionRuns, m.RetentionErrors, m.RetentionLastSuccess, m.RetentionDuration, m.RollupLag, m.BillingEvents, m.BillingRisk, m.BillingPending, m.BillingOldest, m.BillingFailed, m.BillingPriceMismatch, m.AdConversions} {
		f(c)
	}
	for _, result := range []string{"sent", "rejected", "retry", "failed", "expired"} { // exported from the start, so the alert rule's series exists
		m.AdConversions.WithLabelValues(result).Add(0)
	}
	m.CanaryUp.Set(1)                                                               // no verdict yet counts as healthy
	for _, plan := range []string{"pro", "agency", "pro_annual", "agency_annual"} { // billing.PriceKeys (not imported: billing depends on metrics)
		m.BillingPriceMismatch.WithLabelValues(plan).Set(0) // a labeled gauge is not exported until it has a value; the alert rule needs it to exist
	}
	return m
}

// Handler serves the registry in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
