// Package config loads and validates application configuration from the environment.
package config

import (
	"errors"
	"fmt"
	"hawkeye/internal/billing"
	"hawkeye/internal/canary"
	"hawkeye/internal/regions"
	"hawkeye/internal/secrets"
	"log/slog"
	"net/mail"
	"net/url"
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
	// SecretsKey (base64, 32 bytes) encrypts stored monitor credentials. Required in production; development derives a key from SESSION_SECRET.
	SecretsKey string

	LogLevel slog.Level

	RequestTimeout  time.Duration
	BodyLimit       string
	ShutdownTimeout time.Duration

	SessionTTL          time.Duration
	AuthRateLimitPerMin int
	TrustForwardedFor   bool
	// FreeToolsEnabled turns the public SSL and website checkers on (the default). DISABLE_FREE_TOOLS=true makes their endpoints answer 503: the
	// switch to reach for if they are being abused.
	FreeToolsEnabled bool

	EmailProvider    string
	EmailFromAddress string
	SupportAddress   string // where contact-form messages are delivered
	SMTPHost         string
	SMTPPort         int
	SMTPUsername     string
	SMTPPassword     string
	SMTPTLS          string
	SMTPCAFile       string

	APIMetricsPort          int
	GlobalRateLimitPerMin   int
	EmailActionLimitPerHour int
	AllowInsecureDatabase   bool

	SentryDSN  string
	AppVersion string

	// Monitoring engine (worker process).
	// CheckRegions lists where checks can run: "code[:Name],…", the first being the primary. WorkerRegion is the one this worker process serves
	// (default: the primary). Every process (API, scheduler, workers) must be given the same CheckRegions.
	CheckRegions string
	WorkerRegion string

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
	// CanaryURLs: comma-separated https endpoints the worker probes to tell its own network trouble from a customer's site being down. Empty uses the
	// built-in three (Cloudflare, Google, Apple); "off" disables the canary. CanaryInterval is how often it probes in the background.
	CanaryURLs        string
	CanaryInterval    time.Duration
	RunRetention      bool
	RetentionInterval time.Duration
	RetentionBatch    int
	// Domain expiry lookups (RDAP, WHOIS fallback). A pass looks for due domains every DomainCheckInterval; each domain is looked up again after
	// DomainRecheckInterval, with DomainLookupPace between lookups so registries are not hammered.
	RunDomainChecks       bool
	DomainCheckInterval   time.Duration
	DomainRecheckInterval time.Duration
	DomainLookupPace      time.Duration

	StripeSecretKey     string
	StripeWebhookSecret string
	StripePricePro      string
	StripePriceAgency   string
}

// dbSSLMode extracts sslmode from a PostgreSQL URL ("" if absent or unparseable).
func dbSSLMode(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Query().Get("sslmode")
}

func (c Config) IsProduction() bool { return c.AppEnv == EnvProduction }

// Billing returns the payment-provider settings. Billing is simply disabled when none are set.
// SecretBox returns the cipher used to protect stored monitor credentials. Outside production a key is derived from SESSION_SECRET when none
// is set, so a fresh checkout works; production refuses to start without a dedicated key (see validation).
// Regions returns the configured regions (a single "primary" region when CHECK_REGIONS is empty).
func (c Config) CanaryURLList() []string {
	switch strings.ToLower(strings.TrimSpace(c.CanaryURLs)) {
	case "":
		return canary.DefaultURLs
	case "off", "none", "false", "disabled":
		return nil
	}
	var out []string
	for _, u := range strings.Split(c.CanaryURLs, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func (c Config) Regions() regions.Set {
	set, _ := regions.Parse(c.CheckRegions) // validated by Load
	return set
}

// Region is the region this worker process serves: WORKER_REGION, or the primary.
func (c Config) Region() string {
	if c.WorkerRegion != "" {
		return c.WorkerRegion
	}
	return c.Regions().Primary()
}

// MaxWorkerConcurrency bounds WORKER_CONCURRENCY (validated at load); it also keeps the worker's pool size far inside int32.
const MaxWorkerConcurrency = 1000

// WorkerPoolConns is the database pool size for the worker: one connection per concurrent check, the scheduler, the sampler and some
// headroom, but never fewer than DB_MAX_CONNS. The range is checked here, next to the conversion, not only at load time.
func (c Config) WorkerPoolConns() int32 {
	n := c.WorkerConcurrency
	if n < 1 { // load() rejects values outside 1..MaxWorkerConcurrency; clamping here only keeps the conversion below provably in range
		n = 1
	}
	if n > MaxWorkerConcurrency {
		n = MaxWorkerConcurrency
	}
	return max(c.DBMaxConns, int32(n)+4)
}

func (c Config) SecretBox() (*secrets.Box, error) {
	if c.SecretsKey != "" {
		return secrets.New(c.SecretsKey)
	}
	if c.IsProduction() {
		return nil, secrets.ErrNoKey
	}
	return secrets.NewFromPassphrase("development:" + c.SessionSecret)
}

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
	// integer32 is for settings stored in an int32: ParseInt with bitSize 32 rejects values that would silently wrap (for example
	// 4294967297 becoming 1) instead of narrowing an int after the fact.
	integer32 := func(key string, def int32) int32 {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: must be an integer that fits in 32 bits", key))
			return def
		}
		return int32(n)
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
		DBMaxConns:      integer32("DB_MAX_CONNS", 10),
		DBConnectTimout: duration("DB_CONNECT_TIMEOUT", 10*time.Second),
		SessionSecret:   str("SESSION_SECRET", ""),
		SecretsKey:      str("SECRETS_ENCRYPTION_KEY", ""),
		RequestTimeout:  duration("REQUEST_TIMEOUT", 30*time.Second),
		BodyLimit:       str("BODY_LIMIT", "1M"),
		ShutdownTimeout: duration("SHUTDOWN_TIMEOUT", 15*time.Second),

		SessionTTL:          duration("SESSION_TTL", 14*24*time.Hour),
		AuthRateLimitPerMin: integer("AUTH_RATE_LIMIT_PER_MIN", 10),
		TrustForwardedFor:   boolean("TRUST_FORWARDED_FOR"),
		FreeToolsEnabled:    !boolean("DISABLE_FREE_TOOLS"),

		CheckRegions:      str("CHECK_REGIONS", ""),
		CanaryURLs:        str("CANARY_URLS", ""),
		CanaryInterval:    duration("CANARY_INTERVAL", time.Minute),
		WorkerRegion:      str("WORKER_REGION", ""),
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

		RunDomainChecks:       !boolean("DISABLE_DOMAIN_CHECKS"),
		DomainCheckInterval:   duration("DOMAIN_CHECK_INTERVAL", 10*time.Minute),
		DomainRecheckInterval: duration("DOMAIN_RECHECK_INTERVAL", 24*time.Hour),
		DomainLookupPace:      duration("DOMAIN_LOOKUP_PACE", 500*time.Millisecond),

		StripeSecretKey:     str("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: str("STRIPE_WEBHOOK_SECRET", ""),
		StripePricePro:      str("STRIPE_PRICE_PRO_ID", ""),
		StripePriceAgency:   str("STRIPE_PRICE_AGENCY_ID", ""),

		EmailProvider: str("EMAIL_PROVIDER", "console"),
		SMTPHost:      str("SMTP_HOST", ""),
		SMTPPort:      integer("SMTP_PORT", 587),
		SMTPUsername:  str("SMTP_USERNAME", ""),
		SMTPPassword:  str("SMTP_PASSWORD", ""),
		SMTPTLS:       str("SMTP_TLS", "starttls"),
		SMTPCAFile:    str("SMTP_CA_FILE", ""),

		APIMetricsPort:          integer("API_METRICS_PORT", 9091),
		GlobalRateLimitPerMin:   integer("GLOBAL_RATE_LIMIT_PER_MIN", 600),
		EmailActionLimitPerHour: integer("EMAIL_ACTION_LIMIT_PER_HOUR", 3),
		AllowInsecureDatabase:   boolean("ALLOW_INSECURE_DATABASE"),

		SentryDSN:        str("SENTRY_DSN", ""),
		AppVersion:       str("APP_VERSION", "dev"),
		EmailFromAddress: str("EMAIL_FROM_ADDRESS", "alerts@example.com"),
		SupportAddress:   str("CONTACT_TO_ADDRESS", "support@example.com"),
	}

	if c.SecretsKey != "" {
		if _, err := secrets.New(c.SecretsKey); err != nil {
			errs = append(errs, fmt.Errorf("SECRETS_ENCRYPTION_KEY: %w", err))
		}
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
	switch c.EmailProvider {
	case "console":
	case "smtp":
		if c.SMTPHost == "" {
			errs = append(errs, errors.New("SMTP_HOST: required when EMAIL_PROVIDER=smtp"))
		}
		if c.SMTPPort < 1 || c.SMTPPort > 65535 {
			errs = append(errs, errors.New("SMTP_PORT: must be between 1 and 65535"))
		}
		switch c.SMTPTLS {
		case "starttls", "implicit", "none":
		default:
			errs = append(errs, errors.New("SMTP_TLS: must be starttls, implicit or none"))
		}
		if _, err := mail.ParseAddress(c.SupportAddress); err != nil {
			errs = append(errs, errors.New("CONTACT_TO_ADDRESS: must be a valid email address"))
		}
		if _, err := mail.ParseAddress(c.EmailFromAddress); err != nil {
			errs = append(errs, errors.New("EMAIL_FROM_ADDRESS: must be a valid email address"))
		}
	default:
		errs = append(errs, errors.New("EMAIL_PROVIDER: must be console or smtp"))
	}
	if c.APIMetricsPort < 1 || c.APIMetricsPort > 65535 || c.APIMetricsPort == c.Port {
		errs = append(errs, errors.New("API_METRICS_PORT: must be a valid port different from APP_PORT"))
	}
	if c.GlobalRateLimitPerMin < 1 {
		errs = append(errs, errors.New("GLOBAL_RATE_LIMIT_PER_MIN: must be at least 1"))
	}
	if c.EmailActionLimitPerHour < 1 {
		errs = append(errs, errors.New("EMAIL_ACTION_LIMIT_PER_HOUR: must be at least 1"))
	}
	for key, v := range map[string]int{
		"WORKER_CONCURRENCY": c.WorkerConcurrency, "SCHEDULER_BATCH": c.SchedulerBatch, "QUEUE_MAX_DEPTH": c.QueueMaxDepth,
		"JOB_MAX_ATTEMPTS": c.JobMaxAttempts, "FAILURE_THRESHOLD": c.FailureThreshold, "RECOVERY_THRESHOLD": c.RecoveryThreshold, "NOTIFY_MAX_ATTEMPTS": c.NotifyMaxAttempts, "RETENTION_BATCH": c.RetentionBatch,
	} {
		if v < 1 {
			errs = append(errs, fmt.Errorf("%s: must be at least 1", key))
		}
	}
	if c.WorkerConcurrency > MaxWorkerConcurrency {
		errs = append(errs, fmt.Errorf("WORKER_CONCURRENCY: must be at most %d", MaxWorkerConcurrency))
	}
	// A lease shorter than the longest possible check (30s timeout plus connection setup) would let a second
	// worker pick up a job that is still running.
	if c.JobLease < time.Minute {
		errs = append(errs, errors.New("JOB_LEASE: must be at least 1m (longer than the maximum check time)"))
	}
	if set, err := regions.Parse(c.CheckRegions); err != nil {
		errs = append(errs, fmt.Errorf("CHECK_REGIONS: %w", err))
	} else if c.WorkerRegion != "" && !set.Has(c.WorkerRegion) {
		errs = append(errs, errors.New("WORKER_REGION: must be one of the codes listed in CHECK_REGIONS"))
	}
	for _, u := range c.CanaryURLList() {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" || p.Hostname() == "" {
			errs = append(errs, fmt.Errorf("CANARY_URLS: %q is not an https URL", u))
		}
	}
	if c.DomainCheckInterval < time.Minute {
		errs = append(errs, errors.New("DOMAIN_CHECK_INTERVAL: must be at least 1m"))
	}
	if c.DomainRecheckInterval < time.Hour {
		errs = append(errs, errors.New("DOMAIN_RECHECK_INTERVAL: must be at least 1h (registries rate-limit lookups)"))
	}
	if c.DomainLookupPace < 0 {
		errs = append(errs, errors.New("DOMAIN_LOOKUP_PACE: must not be negative"))
	}
	if c.CanaryInterval < 10*time.Second {
		errs = append(errs, errors.New("CANARY_INTERVAL: must be at least 10s"))
	}
	if c.WorkerMetricsPort < 1 || c.WorkerMetricsPort > 65535 {
		errs = append(errs, errors.New("WORKER_METRICS_PORT: must be between 1 and 65535"))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	}

	// Production must never fall back to a development secret.
	if c.IsProduction() {
		if c.SecretsKey == "" {
			errs = append(errs, errors.New("SECRETS_ENCRYPTION_KEY: required in production (generate with: openssl rand -base64 32); it encrypts stored monitor credentials"))
		}
		if c.SessionSecret == "" || c.SessionSecret == placeholderSessionSecret {
			errs = append(errs, errors.New("SESSION_SECRET: must be set to a unique random value in production"))
		} else if len(c.SessionSecret) < minSessionSecretLen {
			errs = append(errs, fmt.Errorf("SESSION_SECRET: must be at least %d characters in production", minSessionSecretLen))
		}
		if !strings.HasPrefix(c.AppBaseURL, "https://") {
			errs = append(errs, errors.New("APP_BASE_URL: must use https in production"))
		}
		if !strings.HasPrefix(c.APIBaseURL, "https://") {
			errs = append(errs, errors.New("API_BASE_URL: must use https in production"))
		}
		// Alerts and password resets must actually be delivered, and never over an unencrypted channel.
		if c.EmailProvider != "smtp" {
			errs = append(errs, errors.New("EMAIL_PROVIDER: must be smtp in production (console only logs messages)"))
		}
		if c.EmailProvider == "smtp" && c.SMTPTLS == "none" {
			errs = append(errs, errors.New("SMTP_TLS: none is not allowed in production"))
		}
		if strings.HasSuffix(strings.ToLower(c.EmailFromAddress), "@example.com") {
			errs = append(errs, errors.New("EMAIL_FROM_ADDRESS: set a real sender address you control (the default is a placeholder)"))
		}
		if strings.HasSuffix(strings.ToLower(c.SupportAddress), "@example.com") {
			errs = append(errs, errors.New("CONTACT_TO_ADDRESS: set the support inbox that should receive contact-form messages (the default is a placeholder)"))
		}
		if !c.AllowInsecureDatabase {
			if mode := dbSSLMode(c.DatabaseURL); mode == "" || mode == "disable" || mode == "allow" || mode == "prefer" {
				errs = append(errs, fmt.Errorf("DATABASE_URL: sslmode must be require, verify-ca or verify-full in production (got %q); set ALLOW_INSECURE_DATABASE=true only for a database on a private network you control", mode))
			}
		}
	}

	if err := c.Billing().Validate(c.IsProduction()); err != nil {
		errs = append(errs, err)
	}

	return c, errors.Join(errs...)
}
