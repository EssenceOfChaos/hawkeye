// Package config loads and validates application configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"hawkeye/internal/billing"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"

	placeholderSessionSecret = "replace_with_a_secure_random_secret"
	minSessionSecretLen      = 32
)

// Config holds all runtime settings. Later phases add email, Stripe, worker and retention settings.
type Config struct {
	AppEnv     string
	Port       int
	AppBaseURL string
	APIBaseURL string

	DatabaseURL     string
	DBMaxConns      int32
	DBConnectTimout time.Duration

	SessionSecret string

	LogLevel slog.Level

	RequestTimeout  time.Duration
	BodyLimit       string
	ShutdownTimeout time.Duration

	SessionTTL          time.Duration
	AuthRateLimitPerMin int
	TrustForwardedFor   bool

	EmailProvider    string
	EmailFromAddress string

	// Monitoring engine (worker process).
	WorkerConcurrency int
	RunScheduler      bool
	SchedulerInterval time.Duration
	SchedulerBatch    int
	QueueMaxDepth     int
	JobLease          time.Duration
	JobMaxAttempts    int
	PollInterval      time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	WorkerMetricsPort int
	NotifyMaxAttempts int
	RunRetention      bool
	RetentionInterval time.Duration
	RetentionBatch    int

	StripeSecretKey     string
	StripeWebhookSecret string
	StripePricePro      string
	StripePriceAgency   string
}

func (c Config) IsProduction() bool { return c.AppEnv == EnvProduction }

// Billing returns the payment-provider settings. Billing is simply disabled when none are set.
func (c Config) Billing() billing.Config {
	return billing.Config{SecretKey: c.StripeSecretKey, WebhookSecret: c.StripeWebhookSecret,
		PricePro: c.StripePricePro, PriceAgency: c.StripePriceAgency, AppBaseURL: c.AppBaseURL}
}

// Load reads configuration from the process environment and validates it.
func Load() (Config, error) { return load(os.Getenv) }

func load(get func(string) string) (Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	integer := func(key string, def int) int {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: must be an integer", key))
			return def
		}
		return n
	}
	duration := func(key string, def time.Duration) time.Duration {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: must be a positive duration such as 10s", key))
			return def
		}
		return d
	}

	boolean := func(key string) bool {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return false
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: must be true or false", key))
		}
		return b
	}

	c := Config{
		AppEnv:          str("APP_ENV", EnvDevelopment),
		Port:            integer("APP_PORT", 8080),
		AppBaseURL:      str("APP_BASE_URL", "http://localhost:3000"),
		APIBaseURL:      str("API_BASE_URL", "http://localhost:8080"),
		DatabaseURL:     str("DATABASE_URL", ""),
		DBMaxConns:      int32(integer("DB_MAX_CONNS", 10)),
		DBConnectTimout: duration("DB_CONNECT_TIMEOUT", 10*time.Second),
		SessionSecret:   str("SESSION_SECRET", ""),
		RequestTimeout:  duration("REQUEST_TIMEOUT", 30*time.Second),
		BodyLimit:       str("BODY_LIMIT", "1M"),
		ShutdownTimeout: duration("SHUTDOWN_TIMEOUT", 15*time.Second),

		SessionTTL:          duration("SESSION_TTL", 14*24*time.Hour),
		AuthRateLimitPerMin: integer("AUTH_RATE_LIMIT_PER_MIN", 10),
		TrustForwardedFor:   boolean("TRUST_FORWARDED_FOR"),

		WorkerConcurrency: integer("WORKER_CONCURRENCY", 10),
		RunScheduler:      !boolean("DISABLE_SCHEDULER"),
		SchedulerInterval: duration("SCHEDULER_INTERVAL", time.Second),
		SchedulerBatch:    integer("SCHEDULER_BATCH", 200),
		QueueMaxDepth:     integer("QUEUE_MAX_DEPTH", 1000),
		JobLease:          duration("JOB_LEASE", 2*time.Minute),
		JobMaxAttempts:    integer("JOB_MAX_ATTEMPTS", 3),
		PollInterval:      duration("WORKER_POLL_INTERVAL", time.Second),
		FailureThreshold:  integer("FAILURE_THRESHOLD", 2),
		RecoveryThreshold: integer("RECOVERY_THRESHOLD", 2),
		WorkerMetricsPort: integer("WORKER_METRICS_PORT", 9090),
		NotifyMaxAttempts: integer("NOTIFY_MAX_ATTEMPTS", 6),
		RunRetention:      !boolean("DISABLE_RETENTION"),
		RetentionInterval: duration("RETENTION_INTERVAL", time.Hour),
		RetentionBatch:    integer("RETENTION_BATCH", 5000),

		StripeSecretKey:     str("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: str("STRIPE_WEBHOOK_SECRET", ""),
		StripePricePro:      str("STRIPE_PRICE_PRO_ID", ""),
		StripePriceAgency:   str("STRIPE_PRICE_AGENCY_ID", ""),

		EmailProvider:    str("EMAIL_PROVIDER", "console"),
		EmailFromAddress: str("EMAIL_FROM_ADDRESS", "alerts@example.com"),
	}

	if err := c.LogLevel.UnmarshalText([]byte(str("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, errors.New("LOG_LEVEL: must be one of debug, info, warn, error"))
	}

	switch c.AppEnv {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		errs = append(errs, fmt.Errorf("APP_ENV: must be %s, %s or %s", EnvDevelopment, EnvTest, EnvProduction))
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, errors.New("APP_PORT: must be between 1 and 65535"))
	}
	if c.DBMaxConns < 1 {
		errs = append(errs, errors.New("DB_MAX_CONNS: must be at least 1"))
	}
	if c.AuthRateLimitPerMin < 1 {
		errs = append(errs, errors.New("AUTH_RATE_LIMIT_PER_MIN: must be at least 1"))
	}
	if c.EmailProvider != "console" {
		errs = append(errs, errors.New("EMAIL_PROVIDER: only \"console\" is implemented so far"))
	}
	for key, v := range map[string]int{
		"WORKER_CONCURRENCY": c.WorkerConcurrency, "SCHEDULER_BATCH": c.SchedulerBatch, "QUEUE_MAX_DEPTH": c.QueueMaxDepth,
		"JOB_MAX_ATTEMPTS": c.JobMaxAttempts, "FAILURE_THRESHOLD": c.FailureThreshold, "RECOVERY_THRESHOLD": c.RecoveryThreshold, "NOTIFY_MAX_ATTEMPTS": c.NotifyMaxAttempts, "RETENTION_BATCH": c.RetentionBatch,
	} {
		if v < 1 {
			errs = append(errs, fmt.Errorf("%s: must be at least 1", key))
		}
	}
	// A lease shorter than the longest possible check (30s timeout plus connection setup) would let a second
	// worker pick up a job that is still running.
	if c.JobLease < time.Minute {
		errs = append(errs, errors.New("JOB_LEASE: must be at least 1m (longer than the maximum check time)"))
	}
	if c.WorkerMetricsPort < 1 || c.WorkerMetricsPort > 65535 {
		errs = append(errs, errors.New("WORKER_METRICS_PORT: must be between 1 and 65535"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	}

	// Production must never fall back to a development secret.
	if c.IsProduction() {
		if c.SessionSecret == "" || c.SessionSecret == placeholderSessionSecret {
			errs = append(errs, errors.New("SESSION_SECRET: must be set to a unique random value in production"))
		} else if len(c.SessionSecret) < minSessionSecretLen {
			errs = append(errs, fmt.Errorf("SESSION_SECRET: must be at least %d characters in production", minSessionSecretLen))
		}
		if !strings.HasPrefix(c.AppBaseURL, "https://") {
			errs = append(errs, errors.New("APP_BASE_URL: must use https in production"))
		}
	}

	if err := c.Billing().Validate(c.IsProduction()); err != nil {
		errs = append(errs, err)
	}

	return c, errors.Join(errs...)
}
