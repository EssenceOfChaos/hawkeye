// Command worker runs the monitoring engine: the scheduler, the check workers and a metrics endpoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"hawkeye/internal/billing"
	"hawkeye/internal/branding"
	"hawkeye/internal/canary"
	"hawkeye/internal/checks"
	"hawkeye/internal/config"
	"hawkeye/internal/database"
	"hawkeye/internal/domains"
	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/healthcheck"
	"hawkeye/internal/incidents"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/logging"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monthlyreport"
	"hawkeye/internal/notifications"
	"hawkeye/internal/retention"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/worker"
)

func main() {
	if healthcheck.Requested() {
		port, _ := strconv.Atoi(os.Getenv("WORKER_METRICS_PORT"))
		if port == 0 {
			port = 9090
		}
		healthcheck.Run(port, "/healthz")
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, cfg.IsProduction())
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connections: one per concurrent check, the scheduler, the sampler and some headroom.
	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.WorkerPoolConns(), cfg.DBConnectTimout)
	if err != nil {
		return err
	}
	defer pool.Close()

	reporter, flushErrors, err := errtrack.New(errtrack.Config{DSN: cfg.SentryDSN, Environment: cfg.AppEnv, Release: cfg.AppVersion})
	if err != nil {
		return err
	}
	defer flushErrors()
	errtrack.LogStatus(log, cfg.SentryDSN, cfg.AppEnv, cfg.AppVersion)
	box, err := cfg.SecretBox()
	if err != nil {
		return fmt.Errorf("secrets key: %w", err)
	}
	m := metrics.New()
	metrics.RegisterPool(m.Registry, pool)
	w := worker.New(pool,
		checks.NewChecker(safehttp.Config{}), // strict production SSRF policy
		checks.NewStore(pool, checks.Thresholds{Failure: cfg.FailureThreshold, Recovery: cfg.RecoveryThreshold}, incidents.Engine{}).WithRegions(cfg.Regions()),
		worker.Config{Concurrency: cfg.WorkerConcurrency, Lease: cfg.JobLease, MaxAttempts: cfg.JobMaxAttempts, PollInterval: cfg.PollInterval},
		m, log).WithReporter(reporter).WithSecrets(box).WithRegion(cfg.Region())

	// The canary: independent public sites probed through the same SSRF-safe client the checks use. Without it, a network fault on this server
	// would fail every monitor at once and email every customer a false outage.
	var cn *canary.Canary
	if urls := cfg.CanaryURLList(); len(urls) > 0 {
		cn = canary.New(safehttp.New(safehttp.Config{}, 6*time.Second), urls).WithLogger(log).WithObserver(func(_, _ int, impaired bool) {
			if impaired {
				m.CanaryUp.Set(0)
			} else {
				m.CanaryUp.Set(1)
			}
		})
		w = w.WithCanary(cn)
	} else {
		log.Warn("canary disabled (CANARY_URLS=off): a network fault on this server will be reported to customers as outages")
	}

	var wg sync.WaitGroup
	spawn := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	spawn(func() { w.Run(ctx) })
	if cn != nil {
		spawn(func() { cn.Run(ctx, cfg.CanaryInterval) })
	}
	if cfg.RunScheduler {
		sched := scheduler.New(pool, scheduler.Config{Interval: cfg.SchedulerInterval, Batch: cfg.SchedulerBatch, MaxQueueDepth: cfg.QueueMaxDepth}, m, log).WithReporter(reporter).WithRegions(cfg.Regions())
		spawn(func() { sched.Run(ctx) })
	}
	if cfg.RunRetention {
		job := retention.New(pool, retention.Config{Interval: cfg.RetentionInterval, Batch: cfg.RetentionBatch, Pause: 50 * time.Millisecond}, m, log).WithReporter(reporter)
		spawn(func() { job.Run(ctx) })
	}
	if cfg.RunDomainChecks {
		// Registry servers are reached through the same SSRF-safe client the checks use; WHOIS dials only validated public addresses.
		look := domains.Lookup{RDAP: domains.NewRDAP(safehttp.New(safehttp.Config{}, 20*time.Second)), WHOIS: domains.NewWHOIS()}
		job := domains.NewJob(pool, look, domains.JobConfig{Interval: cfg.DomainCheckInterval, Recheck: cfg.DomainRecheckInterval, Pace: cfg.DomainLookupPace}, m, log).WithReporter(reporter)
		spawn(func() { job.Run(ctx) })
	}
	// Billing webhook events accepted by the API but not yet applied (the API processes most inline).
	var billingProvider billing.Provider
	if bc := cfg.Billing(); bc.Enabled() {
		billingProvider = billing.NewStripeProvider(bc.SecretKey) // events are applied by re-reading the subscription from Stripe
	}
	billingSvc := billing.NewService(pool, billingProvider, cfg.Billing(), m, log).WithReporter(reporter)
	spawn(func() {
		for ctx.Err() == nil {
			worked, err := billingSvc.ProcessOne(context.WithoutCancel(ctx))
			if err != nil {
				log.Warn("billing event processing failed", "error", err)
			}
			if worked {
				continue
			}
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	})
	// Alert emails.
	mailer, err := email.New(cfg.EmailProvider, email.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS, CAFile: cfg.SMTPCAFile, From: cfg.EmailFromAddress}, log)
	if err != nil {
		return err
	}
	dispatcher := notifications.NewDispatcher(pool, mailer,
		notifications.DispatcherConfig{MaxAttempts: cfg.NotifyMaxAttempts, AppBaseURL: cfg.AppBaseURL, From: cfg.EmailFromAddress}, m, log).WithReporter(reporter).WithSecrets(box)
	spawn(func() { dispatcher.Run(ctx) })
	// Onboarding emails to new accounts with no monitor. They are sent from their own address, so their engagement never affects the alerts' reputation.
	switch {
	case !cfg.RunLifecycle:
		log.Info("lifecycle emails disabled on this worker (DISABLE_LIFECYCLE_EMAILS)")
	case cfg.LifecycleFromAddress == "":
		log.Info("lifecycle emails off (set LIFECYCLE_FROM_ADDRESS to enable them)")
	default:
		job := lifecycle.NewJob(pool, mailer, lifecycle.Config{Interval: cfg.LifecycleInterval, From: cfg.LifecycleFromAddress, ReplyTo: cfg.LifecycleReplyTo,
			AppBaseURL: cfg.AppBaseURL, Address: cfg.LifecyclePostalAddress, Pace: time.Second}, m, log).WithReporter(reporter)
		spawn(func() { job.Run(ctx) })
	}
	// Monthly PDF reports, emailed on the first day of the month to organizations that turned that on.
	if cfg.RunMonthlyReports {
		job := monthlyreport.NewJob(pool, mailer, branding.NewService(pool), monthlyreport.Config{Interval: cfg.MonthlyReportInterval,
			AppBaseURL: cfg.AppBaseURL, Pace: time.Second}, m, log).WithReporter(reporter)
		spawn(func() { job.Run(ctx) })
	}
	spawn(func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			if err := worker.Sample(ctx, pool, m); err != nil && ctx.Err() == nil {
				log.Error("metrics sampling failed", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	mux.HandleFunc("/healthz", func(rw http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(rw, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		rw.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.WorkerMetricsPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "error", err)
		}
	}()

	log.Info("worker started", "concurrency", cfg.WorkerConcurrency, "scheduler", cfg.RunScheduler, "metrics_addr", srv.Addr)
	<-ctx.Done()
	log.Info("shutting down; waiting for in-flight checks")
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(cfg.ShutdownTimeout + 30*time.Second):
		log.Warn("in-flight checks did not finish in time; their leases will expire and be reclaimed")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
