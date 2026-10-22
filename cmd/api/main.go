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
	"hawkeye/internal/safehttp"
	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
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
	if len(os.Args) == 2 && os.Args[1] == "sentry-test" { // docker compose exec api /usr/local/bin/api sentry-test
		os.Exit(sentryTest())
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
	errtrack.LogStatus(log, cfg.SentryDSN, cfg.AppEnv, cfg.AppVersion)
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

	freeTools := newFreeTools(cfg.FreeToolsEnabled, log)
	e := httpapi.New(httpapi.Options{
		Logger: log, DB: pool, RequestTimeout: cfg.RequestTimeout, BodyLimit: cfg.BodyLimit,
		Auth: authSvc, Orgs: organizations.NewService(pool), Plans: planSvc, Monitors: monitorSvc, Pool: pool, PgPool: pool,
		Billing:       billingSvc,
		Account:       account.NewService(pool, authSvc, provider, mailer, log, cfg.AppBaseURL),
		SSLCheck:      freeTools.ssl,
		SiteCheck:     freeTools.site,
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

// sentryTest sends one test event to the configured Sentry project and says whether it was accepted. It exits 0 only when it was.
func sentryTest() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration:\n%v\n", err)
		return 1
	}
	if cfg.SentryDSN == "" {
		fmt.Fprintln(os.Stderr, "SENTRY_DSN is not set in this container's environment, so error tracking is off. Set it in .env.production and redeploy.")
		return 1
	}
	if host, project, ok := errtrack.Describe(cfg.SentryDSN); ok {
		fmt.Printf("sending a test event to project %s at %s (environment %q, release %q)\n", project, host, cfg.AppEnv, cfg.AppVersion)
	}
	rep, err := errtrack.SendTest(errtrack.Config{DSN: cfg.SentryDSN, Environment: cfg.AppEnv, Release: cfg.AppVersion},
		"HawkEye test event: sent on purpose by `api sentry-test`. Nothing is wrong.", 15*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sentry-test:", err)
		return 1
	}
	if !rep.Delivered {
		fmt.Fprintln(os.Stderr, "the test event was NOT delivered:")
		for _, p := range rep.Problems {
			fmt.Fprintln(os.Stderr, "  -", p)
		}
		fmt.Fprintln(os.Stderr, "check that SENTRY_DSN is the DSN of a Go project (Project Settings > Client Keys) and that this server can reach it.")
		return 1
	}
	fmt.Printf("delivered (event %s). It appears under Issues in Sentry within a minute, tagged component=sentry-test.\n", rep.EventID)
	return 0
}

// freeToolSet holds the public checkers. Both stay nil when DISABLE_FREE_TOOLS is set, and their endpoints then answer 503.
type freeToolSet struct {
	ssl  *sslcheck.Service
	site *sitecheck.Service
}

func newFreeTools(enabled bool, log *slog.Logger) freeToolSet {
	if !enabled {
		log.Info("free tools disabled (DISABLE_FREE_TOOLS): the SSL and website checkers answer 503")
		return freeToolSet{}
	}
	// The zero safehttp.Config is the strict production destination policy: public addresses only, ports 80 and 443.
	return freeToolSet{ssl: sslcheck.New(safehttp.Config{}), site: sitecheck.New(safehttp.Config{})}
}
