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

	ChecksTotal   *prometheus.CounterVec // result=success|failure, category
	CheckDuration prometheus.Histogram

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
	NotificationsPending prometheus.Gauge
	NotificationsOldest  prometheus.Gauge

	RetentionDeleted     *prometheus.CounterVec // kind=check_results|incidents|monitors|sessions|tokens
	RetentionRuns        *prometheus.CounterVec // result=ok|error|skipped
	RetentionErrors      prometheus.Counter
	RetentionLastSuccess prometheus.Gauge
	RetentionDuration    prometheus.Gauge

	BillingEvents  *prometheus.CounterVec // result=received|duplicate|invalid_signature|processed|ignored|retry|failed
	BillingRisk    *prometheus.CounterVec // type=charge.dispute.created|charge.refunded|radar.early_fraud_warning.created
	BillingPending prometheus.Gauge
	BillingOldest  prometheus.Gauge
	BillingFailed  prometheus.Gauge

	WorkerErrors *prometheus.CounterVec // stage=claim|load|record|complete|sample|schedule|secrets
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
		Scheduled:       prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_jobs_scheduled_total", Help: "Check jobs enqueued."}),
		Skipped:         prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_skipped_total", Help: "Due monitors skipped because a previous job was still outstanding."}),
		Missed:          prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_missed_checks_total", Help: "Checks scheduled at least one interval late."}),
		Backpressure:    prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_scheduler_backpressure_total", Help: "Scheduler ticks that enqueued nothing because the queue was full."}),
		SchedulerLag:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_scheduler_lag_seconds", Help: "Age of the most overdue monitor."}),
		QueueDepth:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "hawkeye_queue_depth", Help: "Jobs in the queue by state."}, []string{"state"}),
		OldestQueuedAge: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_queue_oldest_job_age_seconds", Help: "Age of the oldest job waiting to run."}),
		JobsReclaimed:   prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_jobs_reclaimed_total", Help: "Jobs claimed again after a lease expired or a retry."}),
		JobsDropped:     prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_jobs_dropped_total", Help: "Jobs dropped after exhausting their attempts."}),
		NotificationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_notification_deliveries_total",
			Help: "Alert delivery attempts by event and result."}, []string{"event", "result"}),
		NotificationsPending: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_notification_pending", Help: "Alert deliveries waiting to be sent."}),
		NotificationsOldest:  prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_notification_oldest_pending_seconds", Help: "Age of the oldest due alert delivery."}),
		RetentionDeleted:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_retention_deleted_rows_total", Help: "Rows removed by retention, by kind."}, []string{"kind"}),
		RetentionRuns:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_retention_runs_total", Help: "Retention passes by result."}, []string{"result"}),
		RetentionErrors:      prometheus.NewCounter(prometheus.CounterOpts{Name: "hawkeye_retention_errors_total", Help: "Failed retention steps."}),
		RetentionLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_retention_last_success_timestamp_seconds", Help: "Unix time of the last successful retention pass."}),
		RetentionDuration:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_retention_last_duration_seconds", Help: "Duration of the last successful retention pass."}),
		BillingEvents:        prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_billing_webhook_events_total", Help: "Billing webhook events by result."}, []string{"result"}),
		BillingRisk:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_billing_risk_events_total", Help: "Disputes, refunds and fraud warnings received; each needs human review."}, []string{"type"}),
		BillingPending:       prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_events_pending", Help: "Stored billing events awaiting processing."}),
		BillingOldest:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_oldest_pending_seconds", Help: "Age of the oldest unprocessed billing event."}),
		BillingFailed:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_billing_events_failed", Help: "Billing events that gave up and need manual attention."}),
		WorkerErrors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hawkeye_worker_errors_total",
			Help: "Internal errors by stage."}, []string{"stage"}),
		LastPollUnix: prometheus.NewGauge(prometheus.GaugeOpts{Name: "hawkeye_worker_last_poll_timestamp_seconds", Help: "Unix time of the last queue poll."}),
	}
	for _, c := range []prometheus.Collector{m.ChecksTotal, m.CheckDuration, m.Scheduled, m.Skipped, m.Missed, m.Backpressure,
		m.SchedulerLag, m.QueueDepth, m.OldestQueuedAge, m.JobsReclaimed, m.JobsDropped, m.WorkerErrors, m.LastPollUnix, m.NotificationsTotal, m.NotificationsPending, m.NotificationsOldest, m.RetentionDeleted, m.RetentionRuns, m.RetentionErrors, m.RetentionLastSuccess, m.RetentionDuration, m.BillingEvents, m.BillingRisk, m.BillingPending, m.BillingOldest, m.BillingFailed} {
		f(c)
	}
	return m
}

// Handler serves the registry in Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
