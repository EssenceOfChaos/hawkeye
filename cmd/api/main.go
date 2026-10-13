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
	"syscall"
	"time"

	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/config"
	"hawkeye/internal/database"
	"hawkeye/internal/email"
	"hawkeye/internal/httpapi"
	"hawkeye/internal/logging"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
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

	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBConnectTimout)
	if err != nil {
		return err
	}
	defer pool.Close()

	mailer := email.Console{Log: log}
	if cfg.IsProduction() {
		log.Warn("EMAIL_PROVIDER=console: emails are logged, not delivered; no real provider is implemented yet")
	}
	authSvc, err := auth.NewService(pool, mailer, log, auth.Config{SessionTTL: cfg.SessionTTL, AppBaseURL: cfg.AppBaseURL})
	if err != nil {
		return err
	}

	planSvc := plans.NewService(cfg.AppBaseURL)

	var provider billing.Provider
	if bc := cfg.Billing(); bc.Enabled() {
		provider = billing.NewStripeProvider(bc.SecretKey)
	} else {
		log.Info("billing disabled: set STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET and the price IDs to enable it")
	}
	billingSvc := billing.NewService(pool, provider, cfg.Billing(), metrics.New(), log)

	e := httpapi.New(httpapi.Options{
		Logger: log, DB: pool, RequestTimeout: cfg.RequestTimeout, BodyLimit: cfg.BodyLimit,
		Auth: authSvc, Orgs: organizations.NewService(pool), Plans: planSvc, Monitors: monitors.NewService(pool, planSvc), Pool: pool, PgPool: pool,
		Billing:       billingSvc,
		Notifications: notifications.NewService(pool, mailer, log, cfg.AppBaseURL, cfg.EmailFromAddress),
		SecureCookies: cfg.IsProduction(), AuthRateLimitPerMin: cfg.AuthRateLimitPerMin, TrustForwardedFor: cfg.TrustForwardedFor,
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           e,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", srv.Addr, "env", cfg.AppEnv)
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
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}
