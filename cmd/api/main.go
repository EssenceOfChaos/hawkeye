// Command api runs the REST API server.
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
	"syscall"
	"time"

	"hawkeye/internal/account"
	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/config"
	"hawkeye/internal/contact"
	"hawkeye/internal/database"
	"hawkeye/internal/domains"
	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/healthcheck"
	"hawkeye/internal/httpapi"
	"hawkeye/internal/logging"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/ratelimit"
	"hawkeye/internal/statuspage"
)

func main() {
	if healthcheck.Requested() { // used by the container HEALTHCHECK: this image has no shell or curl
		port, _ := strconv.Atoi(os.Getenv("APP_PORT"))
		if port == 0 {
			port = 8080
		}
		healthcheck.Run(port, "/readyz")
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

	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBConnectTimout)
	if err != nil {
		return err
	}
	defer pool.Close()

	reporter, flushErrors, err := errtrack.New(errtrack.Config{DSN: cfg.SentryDSN, Environment: cfg.AppEnv, Release: cfg.AppVersion})
	if err != nil {
		return err
	}
	defer flushErrors()
	if cfg.SentryDSN == "" {
		log.Info("error tracking disabled (set SENTRY_DSN to enable it)")
	}
	apiMetrics := metrics.NewAPI()
	metrics.RegisterPool(apiMetrics.Registry, pool)

	mailer, err := email.New(cfg.EmailProvider, email.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword, TLS: cfg.SMTPTLS, CAFile: cfg.SMTPCAFile, From: cfg.EmailFromAddress}, log)
	if err != nil {
		return err
	}
	if cfg.EmailProvider == "console" {
		log.Warn("EMAIL_PROVIDER=console: emails are logged, not delivered (development only)")
	}
	authSvc, err := auth.NewService(pool, mailer, log, auth.Config{SessionTTL: cfg.SessionTTL, AppBaseURL: cfg.AppBaseURL})
	if err != nil {
		return err
	}

	planSvc := plans.NewService(cfg.AppBaseURL)
	box, err := cfg.SecretBox()
	if err != nil {
		return fmt.Errorf("secrets key: %w", err)
	}
	monitorSvc := monitors.NewService(pool, planSvc, monitors.WithSecretBox(box), monitors.WithRegions(cfg.Regions()), monitors.WithDomainSync(domains.SyncTx))
	// Credentials saved in plain text by an earlier version are encrypted once, here, before any request is served.
	if n, err := monitorSvc.EncryptLegacySecrets(ctx); err != nil {
		return fmt.Errorf("encrypt stored credentials: %w", err)
	} else if n > 0 {
		log.InfoContext(ctx, "encrypted stored monitor credentials", "count", n)
	}

	var provider billing.Provider
	if bc := cfg.Billing(); bc.Enabled() {
		provider = billing.NewStripeProvider(bc.SecretKey)
	} else {
		log.Info("billing disabled: set STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET and the price IDs to enable it")
	}
	billingSvc := billing.NewService(pool, provider, cfg.Billing(), apiMetrics.App, log).WithReporter(reporter)
	// Compare Stripe's prices with the advertised plan prices now and hourly; a mismatch refuses that plan's checkout (internal/billing/prices.go).
	go billingSvc.WatchPrices(ctx, time.Hour)

	e := httpapi.New(httpapi.Options{
		Logger: log, DB: pool, RequestTimeout: cfg.RequestTimeout, BodyLimit: cfg.BodyLimit,
		Auth: authSvc, Orgs: organizations.NewService(pool), Plans: planSvc, Monitors: monitorSvc, Pool: pool, PgPool: pool,
		Billing:       billingSvc,
		Account:       account.NewService(pool, authSvc, provider, mailer, log, cfg.AppBaseURL),
		Contact:       contact.NewService(mailer, log, cfg.SupportAddress, cfg.EmailFromAddress, cfg.AppBaseURL),
		Maintenance:   maintenance.NewService(pool, planSvc),
		Domains:       domains.NewService(pool, planSvc),
		Regions:       cfg.Regions(),
		StatusPages:   statuspage.NewService(pool),
		Notifications: notifications.NewService(pool, mailer, log, cfg.AppBaseURL, cfg.EmailFromAddress, notifications.WithSecretBox(box)),
		SecureCookies: cfg.IsProduction(), AuthRateLimitPerMin: cfg.AuthRateLimitPerMin, TrustForwardedFor: cfg.TrustForwardedFor,
		HSTS: cfg.IsProduction(), Metrics: apiMetrics, Limiter: ratelimit.New(pool), Errors: reporter,
		GlobalRateLimitPerMin: cfg.GlobalRateLimitPerMin, EmailActionLimitPerHour: cfg.EmailActionLimitPerHour,
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Metrics are served on a separate port that must not be published or proxied: they describe internals.
	mux := http.NewServeMux()
	mux.Handle("/metrics", apiMetrics.Handler())
	metricsSrv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.APIMetricsPort), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics server failed", "error", err)
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", srv.Addr, "metrics_addr", metricsSrv.Addr, "env", cfg.AppEnv, "version", cfg.AppVersion)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
