// Package worker claims check jobs from the queue, runs them and records the results.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/billing"
	"hawkeye/internal/canary"
	"hawkeye/internal/checks"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
	"hawkeye/internal/queue"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/secrets"
)

type Config struct {
	Concurrency  int           // bounded number of simultaneous checks
	Lease        time.Duration // must exceed the longest possible check
	MaxAttempts  int
	PollInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Concurrency < 1 {
		c.Concurrency = 10
	}
	if c.Lease <= 0 {
		c.Lease = 2 * time.Minute
	}
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 3
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	return c
}

type Worker struct {
	pool    *pgxpool.Pool
	checker *checks.Checker
	store   *checks.Store
	cfg     Config
	m       *metrics.Metrics
	log     *slog.Logger
	rep     errtrack.Reporter
	box     *secrets.Box   // opens stored monitor credentials
	region  string         // only jobs queued for this region are run
	canary  *canary.Canary // optional: tells our own network trouble apart from a customer's site being down
}

func New(pool *pgxpool.Pool, checker *checks.Checker, store *checks.Store, cfg Config, m *metrics.Metrics, log *slog.Logger) *Worker {
	return &Worker{pool: pool, checker: checker, store: store, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}, region: queue.DefaultRegion}
}

// WithRegion makes the worker run only the jobs queued for region, and label its results with it.
func (w *Worker) WithRegion(region string) *Worker {
	w.region = region
	return w
}

// WithCanary makes the worker double-check suspicious failures: a failed check whose cause could be this server's own network (DNS, connection,
// timeout) is dropped, not recorded, when the canary says the server cannot reach independent public sites either.
func (w *Worker) WithCanary(c *canary.Canary) *Worker {
	w.canary = c
	return w
}

// WithSecrets sets the cipher used to open stored monitor credentials. Without it, checks that need a credential fail and are retried.
func (w *Worker) WithSecrets(b *secrets.Box) *Worker {
	w.box = b
	return w
}

// WithReporter sends unexpected worker errors to an error tracker.
func (w *Worker) WithReporter(r errtrack.Reporter) *Worker {
	w.rep = r
	return w
}

// Run starts Concurrency polling loops and returns after ctx is cancelled and in-flight checks have
// finished. In-flight checks are not cancelled by shutdown, so results are not lost.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range w.cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				worked, err := w.ProcessOne(context.WithoutCancel(ctx))
				if err != nil {
					w.log.Error("worker iteration failed", "error", err)
					w.rep.Capture(ctx, err, map[string]string{"component": "worker"})
					worked = false
				}
				if !worked {
					select {
					case <-ctx.Done():
					case <-time.After(w.cfg.PollInterval + time.Duration(rand.Float64()*float64(w.cfg.PollInterval)/2)):
					}
				}
			}
		}()
	}
	wg.Wait()
}

// ProcessOne claims and executes at most one job. It reports whether a job was found.
func (w *Worker) ProcessOne(ctx context.Context) (bool, error) {
	w.m.LastPollUnix.SetToCurrentTime()
	job, dropped, err := queue.ClaimRegion(ctx, w.pool, w.region, w.cfg.Lease, w.cfg.MaxAttempts)
	if dropped > 0 {
		w.m.JobsDropped.Add(float64(dropped))
		w.log.Warn("dropped jobs that exhausted their attempts", "count", dropped)
	}
	if err != nil {
		w.m.WorkerErrors.WithLabelValues("claim").Inc()
		return false, err
	}
	if job == nil {
		return false, nil
	}
	if job.Reclaimed {
		w.m.JobsReclaimed.Inc()
		w.log.Warn("reclaimed job", "job_id", job.ID, "monitor_id", job.MonitorID, "attempt", job.Attempts)
	}

	var t checks.Target
	var enabled bool
	var timeoutSecs int
	err = w.pool.QueryRow(ctx, `SELECT url, method, expected_status_code, timeout_seconds, enabled,
		request_headers, COALESCE(request_body, ''), auth_type, COALESCE(auth_username, ''), COALESCE(auth_header_name, ''),
		COALESCE(auth_secret, ''), assertions
		FROM monitors WHERE id = $1 AND deleted_at IS NULL`, job.MonitorID).
		Scan(&t.URL, &t.Method, &t.ExpectedStatus, &timeoutSecs, &enabled, &t.Headers, &t.Body, &t.Auth.Type,
			&t.Auth.Username, &t.Auth.HeaderName, &t.Auth.Secret, &t.Assertions)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled:
		return true, w.complete(ctx, job) // deleted or paused since it was scheduled
	case err != nil:
		w.m.WorkerErrors.WithLabelValues("load").Inc()
		w.retry(ctx, job)
		return true, err
	}
	if t.Auth.Secret != "" {
		// A credential that cannot be opened (missing or wrong key) is an operator problem, not a failing website: retry and report it, and
		// never record a check result, so no false incident is opened for the customer.
		plain, err := w.box.Open(t.Auth.Secret, job.MonitorID.String())
		if err != nil {
			w.m.WorkerErrors.WithLabelValues("secrets").Inc()
			w.log.ErrorContext(ctx, "cannot open monitor credentials", "monitor_id", job.MonitorID, "error", err)
			w.rep.Capture(ctx, fmt.Errorf("monitor %s: open credentials: %w", job.MonitorID, err), map[string]string{"component": "worker"})
			w.retry(ctx, job)
			return true, nil
		}
		t.Auth.Secret = plain
	}
	t.Timeout = time.Duration(timeoutSecs) * time.Second

	res, err := w.checker.Check(ctx, t)
	if err != nil { // only when ctx was cancelled
		w.retry(ctx, job)
		return true, nil
	}
	res.Region = w.region
	if !res.Success && w.canary != nil && mayBeOurNetwork(res.ErrorCategory) && w.canary.Impaired(ctx) {
		// Not recorded at all: no failed row in the history, no failure streak, no incident, no alert. The monitor's next scheduled check counts
		// normally once the network is back.
		w.m.ChecksTotal.WithLabelValues("inconclusive", res.ErrorCategory).Inc()
		w.log.WarnContext(ctx, "check result discarded: this server cannot reach independent public sites, so the failure says nothing about the monitored site",
			"monitor_id", job.MonitorID, "category", res.ErrorCategory)
		return true, w.complete(ctx, job)
	}
	_, err = w.store.Record(ctx, job.MonitorID, res)
	switch {
	case errors.Is(err, checks.ErrDiscarded):
		return true, w.complete(ctx, job)
	case err != nil:
		w.m.WorkerErrors.WithLabelValues("record").Inc()
		w.retry(ctx, job)
		return true, err
	}

	result, category := "success", ""
	if !res.Success {
		result, category = "failure", res.ErrorCategory
	}
	w.m.ChecksTotal.WithLabelValues(result, category).Inc()
	w.m.CheckDuration.Observe(res.FinishedAt.Sub(res.CheckedAt).Seconds())
	return true, w.complete(ctx, job)
}

// mayBeOurNetwork is true for failures this server's own network trouble could cause. A response from the site (an error status, a failed
// assertion), a certificate problem or a blocked destination show the network worked, so they are never second-guessed.
func mayBeOurNetwork(category string) bool {
	switch category {
	case checks.CatDNS, checks.CatConnect, checks.CatTimeout, checks.CatOther:
		return true
	}
	return false
}

func (w *Worker) complete(ctx context.Context, job *queue.Job) error {
	if err := queue.Complete(ctx, w.pool, job.ID); err != nil {
		w.m.WorkerErrors.WithLabelValues("complete").Inc()
		return err
	}
	return nil
}

// retry puts a job back after a transient failure; if even that fails the lease expiry reclaims it.
func (w *Worker) retry(ctx context.Context, job *queue.Job) {
	delay := time.Duration(job.Attempts) * 5 * time.Second
	if err := queue.Release(ctx, w.pool, job.ID, delay); err != nil {
		w.log.Error("release job failed; lease expiry will recover it", "job_id", job.ID, "error", err)
	}
}

// Sample publishes queue and scheduler gauges. Call it periodically.
func Sample(ctx context.Context, pool *pgxpool.Pool, m *metrics.Metrics) error {
	s, err := queue.Snapshot(ctx, pool)
	if err != nil {
		m.WorkerErrors.WithLabelValues("sample").Inc()
		return err
	}
	m.QueueDepth.WithLabelValues("queued").Set(float64(s.Queued))
	m.QueueDepth.WithLabelValues("running").Set(float64(s.Running))
	m.OldestQueuedAge.Set(s.OldestRunnable.Seconds())
	lag, err := scheduler.Lag(ctx, pool)
	if err != nil {
		m.WorkerErrors.WithLabelValues("sample").Inc()
		return err
	}
	m.SchedulerLag.Set(lag.Seconds())
	n, oldest, err := notifications.Pending(ctx, pool)
	if err != nil {
		m.WorkerErrors.WithLabelValues("sample").Inc()
		return err
	}
	if bn, boldest, err := billing.Pending(ctx, pool); err == nil {
		m.BillingPending.Set(float64(bn))
		m.BillingOldest.Set(boldest.Seconds())
	}
	if bf, err := billing.Failed(ctx, pool); err == nil {
		m.BillingFailed.Set(float64(bf))
	}
	m.NotificationsPending.Set(float64(n))
	m.NotificationsOldest.Set(oldest.Seconds())
	return nil
}
