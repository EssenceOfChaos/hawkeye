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
	"sync"
	"syscall"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/config"
	"hawkeye/internal/database"
	"hawkeye/internal/email"
	"hawkeye/internal/incidents"
	"hawkeye/internal/logging"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/scheduler"
	"hawkeye/internal/worker"
)

func main() {
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
	pool, err := database.Connect(ctx, cfg.DatabaseURL, max(cfg.DBMaxConns, int32(cfg.WorkerConcurrency)+4), cfg.DBConnectTimout)
	if err != nil {
		return err
	}
	defer pool.Close()

	m := metrics.New()
	w := worker.New(pool,
		checks.NewChecker(safehttp.Config{}), // strict production SSRF policy
		checks.NewStore(pool, checks.Thresholds{Failure: cfg.FailureThreshold, Recovery: cfg.RecoveryThreshold}, incidents.Engine{}),
		worker.Config{Concurrency: cfg.WorkerConcurrency, Lease: cfg.JobLease, MaxAttempts: cfg.JobMaxAttempts, PollInterval: cfg.PollInterval},
		m, log)

	var wg sync.WaitGroup
	spawn := func(fn func()) {
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	spawn(func() { w.Run(ctx) })
	if cfg.RunScheduler {
		sched := scheduler.New(pool, scheduler.Config{Interval: cfg.SchedulerInterval, Batch: cfg.SchedulerBatch, MaxQueueDepth: cfg.QueueMaxDepth}, m, log)
		spawn(func() { sched.Run(ctx) })
	}
	// Alert emails. Only the console provider exists so far; swap the sender here when a real one is added.
	dispatcher := notifications.NewDispatcher(pool, email.Console{Log: log},
		notifications.DispatcherConfig{MaxAttempts: cfg.NotifyMaxAttempts, AppBaseURL: cfg.AppBaseURL, From: cfg.EmailFromAddress}, m, log)
	spawn(func() { dispatcher.Run(ctx) })
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
