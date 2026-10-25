package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaults(t *testing.T) {
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.AppEnv != EnvDevelopment || c.Port != 8080 || c.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	_, err := load(env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := load(env(map[string]string{"APP_PORT": "abc", "LOG_LEVEL": "loud", "APP_ENV": "staging"}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"APP_PORT", "LOG_LEVEL", "APP_ENV", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %s: %v", want, err)
		}
	}
}

func TestProductionRejectsWeakSecrets(t *testing.T) {
	base := map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://x", "APP_BASE_URL": "https://example.com"}
	for name, secret := range map[string]string{
		"empty":       "",
		"placeholder": placeholderSessionSecret,
		"short":       "tooshort",
	} {
		t.Run(name, func(t *testing.T) {
			m := map[string]string{}
			for k, v := range base {
				m[k] = v
			}
			m["SESSION_SECRET"] = secret
			if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
				t.Fatalf("expected SESSION_SECRET error, got %v", err)
			}
		})
	}
}

func TestProductionRequiresHTTPS(t *testing.T) {
	_, err := load(env(map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://x", "APP_BASE_URL": "http://example.com",
		"SESSION_SECRET": strings.Repeat("a", 40),
	}))
	if err == nil || !strings.Contains(err.Error(), "APP_BASE_URL") {
		t.Fatalf("expected APP_BASE_URL error, got %v", err)
	}
}

func TestEngineDefaultsAndValidation(t *testing.T) {
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.WorkerConcurrency != 10 || c.FailureThreshold != 2 || c.RecoveryThreshold != 2 || !c.RunScheduler || !c.FreeToolsEnabled || c.JobLease.Minutes() != 2 {
		t.Fatalf("%+v", c)
	}
	_, err = load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_CONCURRENCY": "0", "JOB_LEASE": "10s", "FAILURE_THRESHOLD": "0"}))
	for _, want := range []string{"WORKER_CONCURRENCY", "JOB_LEASE", "FAILURE_THRESHOLD"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("expected %s error, got %v", want, err)
		}
	}
}

func TestStripeConfigIsOptionalButAllOrNothing(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x"}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	if c, err := load(env(base)); err != nil || c.Billing().Enabled() {
		t.Fatalf("no Stripe settings must mean billing disabled, not an error: %v", err)
	}
	full := map[string]string{"STRIPE_SECRET_KEY": "sk_test_abc", "STRIPE_WEBHOOK_SECRET": "whsec_abc", "STRIPE_PRICE_PRO_ID": "price_1", "STRIPE_PRICE_AGENCY_ID": "price_2"}
	if c, err := load(env(with(full))); err != nil || !c.Billing().Enabled() {
		t.Fatalf("%v", err)
	}
	if _, err := load(env(with(map[string]string{"STRIPE_SECRET_KEY": "sk_test_abc"}))); err == nil || !strings.Contains(err.Error(), "STRIPE_WEBHOOK_SECRET") {
		t.Fatalf("a partial Stripe configuration must fail loudly: %v", err)
	}
	live := with(full)
	live["STRIPE_SECRET_KEY"] = "sk_live_abc"
	if _, err := load(env(live)); err == nil || !strings.Contains(err.Error(), "live keys") {
		t.Fatalf("live keys must be refused outside production: %v", err)
	}
}

// prodBase is a valid production configuration; tests break one thing at a time.
func prodBase() map[string]string {
	return map[string]string{
		"APP_ENV": "production", "APP_BASE_URL": "https://app.example.org", "API_BASE_URL": "https://app.example.org",
		"DATABASE_URL": "postgres://u:p@db.example.org/monitoring?sslmode=require", "SESSION_SECRET": strings.Repeat("a", 40),
		"EMAIL_PROVIDER": "smtp", "SMTP_HOST": "smtp.example.org", "SMTP_USERNAME": "u", "SMTP_PASSWORD": "p", "EMAIL_FROM_ADDRESS": "alerts@hawkeye.example.org",
		"CONTACT_TO_ADDRESS":     "support@hawkeye.example.org",
		"SECRETS_ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", // 32 bytes
	}
}

func TestProductionRequiresAValidSecretsKey(t *testing.T) {
	b := prodBase()
	delete(b, "SECRETS_ENCRYPTION_KEY")
	if _, err := load(env(b)); err == nil || !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") {
		t.Fatalf("production must refuse to start without a credentials key: %v", err)
	}
	for _, bad := range []string{"not base64!!", "c2hvcnQ="} {
		b["SECRETS_ENCRYPTION_KEY"] = bad
		if _, err := load(env(b)); err == nil || !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") {
			t.Fatalf("bad key %q accepted: %v", bad, err)
		}
	}
}

func TestDevelopmentDerivesAKeyButProductionNeverDoes(t *testing.T) {
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db", "SESSION_SECRET": "dev-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SecretBox(); err != nil {
		t.Fatalf("development should derive a key: %v", err)
	}
	c.AppEnv = EnvProduction
	c.SecretsKey = ""
	if _, err := c.SecretBox(); err == nil {
		t.Fatal("production must never fall back to a derived key")
	}
}

func TestProductionRequiresARealSupportInbox(t *testing.T) {
	b := prodBase()
	delete(b, "CONTACT_TO_ADDRESS")
	if _, err := load(env(b)); err == nil || !strings.Contains(err.Error(), "CONTACT_TO_ADDRESS") {
		t.Fatalf("the placeholder support inbox must be refused in production: %v", err)
	}
	b["CONTACT_TO_ADDRESS"] = "not an address"
	if _, err := load(env(b)); err == nil || !strings.Contains(err.Error(), "CONTACT_TO_ADDRESS") {
		t.Fatalf("an invalid support address must be refused: %v", err)
	}
}

func TestProductionBaselineIsValid(t *testing.T) {
	if _, err := load(env(prodBase())); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestProductionRefusesUnsafeSettings(t *testing.T) {
	cases := map[string]struct {
		set  map[string]string
		want string
	}{
		"console email":            {map[string]string{"EMAIL_PROVIDER": "console"}, "EMAIL_PROVIDER"},
		"unencrypted smtp":         {map[string]string{"SMTP_TLS": "none"}, "SMTP_TLS"},
		"placeholder sender":       {map[string]string{"EMAIL_FROM_ADDRESS": "alerts@example.com"}, "EMAIL_FROM_ADDRESS"},
		"database without tls":     {map[string]string{"DATABASE_URL": "postgres://u:p@db/x?sslmode=disable"}, "sslmode"},
		"database sslmode missing": {map[string]string{"DATABASE_URL": "postgres://u:p@db/x"}, "sslmode"},
		"database sslmode prefer":  {map[string]string{"DATABASE_URL": "postgres://u:p@db/x?sslmode=prefer"}, "sslmode"},
		"http api url":             {map[string]string{"API_BASE_URL": "http://api.example.org"}, "API_BASE_URL"},
		"smtp without host":        {map[string]string{"SMTP_HOST": ""}, "SMTP_HOST"},
	}
	for name, c := range cases {
		m := prodBase()
		for k, v := range c.set {
			m[k] = v
		}
		if _, err := load(env(m)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error mentioning %q, got %v", name, c.want, err)
		}
	}
}

func TestProductionAllowsInsecureDatabaseOnlyWhenExplicit(t *testing.T) {
	m := prodBase()
	m["DATABASE_URL"] = "postgres://u:p@db/x?sslmode=disable"
	m["ALLOW_INSECURE_DATABASE"] = "true"
	if _, err := load(env(m)); err != nil {
		t.Fatalf("explicit opt-out should be accepted: %v", err)
	}
}

func TestSMTPSettingsAreValidatedInDevelopmentToo(t *testing.T) {
	_, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "EMAIL_PROVIDER": "smtp"}))
	if err == nil || !strings.Contains(err.Error(), "SMTP_HOST") {
		t.Fatalf("%v", err)
	}
	if _, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "EMAIL_PROVIDER": "carrier-pigeon"})); err == nil {
		t.Fatal("unknown provider accepted")
	}
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "EMAIL_PROVIDER": "smtp", "SMTP_HOST": "localhost", "SMTP_TLS": "none", "EMAIL_FROM_ADDRESS": "dev@localhost"}))
	if err != nil || c.SMTPPort != 587 {
		t.Fatalf("%v", err)
	}
}

// Every setting that main() passes on must really be read from the environment (a declared-but-never-parsed field fails silently).
func TestSMTPSettingsAreAllReadFromTheEnvironment(t *testing.T) {
	c, err := load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "EMAIL_PROVIDER": "smtp", "SMTP_HOST": "mail.internal", "SMTP_PORT": "2525", "SMTP_USERNAME": "user",
		"SMTP_PASSWORD": "pass", "SMTP_TLS": "implicit", "SMTP_CA_FILE": "/etc/ca.pem", "EMAIL_FROM_ADDRESS": "alerts@hawkeye.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SMTPHost != "mail.internal" || c.SMTPPort != 2525 || c.SMTPUsername != "user" || c.SMTPPassword != "pass" || c.SMTPTLS != "implicit" ||
		c.SMTPCAFile != "/etc/ca.pem" || c.EmailFromAddress != "alerts@hawkeye.test" {
		t.Fatalf("%+v", c)
	}
}

func TestDBMaxConnsRejectsValuesThatWouldWrapAroundInt32(t *testing.T) {
	for _, bad := range []string{"4294967297", "2147483648", "-1", "0", "ten"} { // 4294967297 used to wrap silently to 1
		if _, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": bad})); err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS") {
			t.Errorf("DB_MAX_CONNS=%q accepted: %v", bad, err)
		}
	}
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "DB_MAX_CONNS": "25"}))
	if err != nil || c.DBMaxConns != 25 {
		t.Fatalf("valid value rejected: %v %+v", err, c.DBMaxConns)
	}
}

func TestWorkerConcurrencyHasAnUpperBound(t *testing.T) {
	if _, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_CONCURRENCY": "100000"})); err == nil || !strings.Contains(err.Error(), "WORKER_CONCURRENCY") {
		t.Fatalf("expected an error, got %v", err)
	}
}

func TestWorkerPoolConnsIsConcurrencyPlusHeadroomButNotBelowDBMaxConns(t *testing.T) {
	cases := []struct {
		conc int
		max  int32
		want int32
	}{{10, 10, 14}, {10, 50, 50}, {0, 2, 5}, {1 << 40, 5, MaxWorkerConcurrency + 4}}
	for _, c := range cases {
		got := Config{WorkerConcurrency: c.conc, DBMaxConns: c.max}.WorkerPoolConns()
		if got != c.want {
			t.Errorf("concurrency %d, max conns %d: got %d, want %d", c.conc, c.max, got, c.want)
		}
	}
}

func TestCanarySettings(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x"}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	c, err := load(env(base))
	if err != nil || len(c.CanaryURLList()) != 3 || c.CanaryInterval != time.Minute {
		t.Fatalf("defaults: %v %v %v", err, c.CanaryURLList(), c.CanaryInterval)
	}
	if c, err = load(env(with("CANARY_URLS", "off"))); err != nil || c.CanaryURLList() != nil {
		t.Fatalf("off must disable the canary: %v %v", err, c.CanaryURLList())
	}
	if c, err = load(env(with("CANARY_URLS", " https://a.example/x , https://b.example/y "))); err != nil || len(c.CanaryURLList()) != 2 {
		t.Fatalf("custom list: %v %v", err, c.CanaryURLList())
	}
	for name, kv := range map[string][]string{
		"plain http": {"CANARY_URLS", "http://a.example/"},
		"not a url":  {"CANARY_URLS", "nonsense"},
		"too fast":   {"CANARY_INTERVAL", "2s"},
	} {
		if _, err := load(env(with(kv...))); err == nil || !strings.Contains(err.Error(), "CANARY") {
			t.Errorf("%s: expected a CANARY_ error, got %v", name, err)
		}
	}
}

func TestFreeToolsCanBeSwitchedOff(t *testing.T) {
	on, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil || !on.FreeToolsEnabled {
		t.Fatalf("free tools must be on by default: %v %+v", err, on.FreeToolsEnabled)
	}
	off, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "DISABLE_FREE_TOOLS": "true"}))
	if err != nil || off.FreeToolsEnabled {
		t.Fatalf("DISABLE_FREE_TOOLS=true must turn them off: %v %+v", err, off.FreeToolsEnabled)
	}
}

func TestLifecycleEmailsAreOffUntilASenderIsSet(t *testing.T) {
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil || c.LifecycleFromAddress != "" || !c.RunLifecycle || c.LifecycleInterval != 10*time.Minute {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	c, err = load(env(map[string]string{"DATABASE_URL": "postgres://x", "LIFECYCLE_FROM_ADDRESS": "HawkEye <customer_support@hawkeye.test>",
		"LIFECYCLE_REPLY_TO": "help@hawkeye.test", "LIFECYCLE_POSTAL_ADDRESS": "1 Main St", "LIFECYCLE_INTERVAL": "5m", "DISABLE_LIFECYCLE_EMAILS": "true"}))
	if err != nil || c.LifecycleFromAddress != "HawkEye <customer_support@hawkeye.test>" || c.LifecycleReplyTo != "help@hawkeye.test" ||
		c.LifecyclePostalAddress != "1 Main St" || c.LifecycleInterval != 5*time.Minute || c.RunLifecycle {
		t.Fatalf("read from the environment: %v %+v", err, c)
	}
	for _, bad := range []map[string]string{{"LIFECYCLE_FROM_ADDRESS": "nope"}, {"LIFECYCLE_REPLY_TO": "nope"}, {"LIFECYCLE_INTERVAL": "10s"}} {
		bad["DATABASE_URL"] = "postgres://x"
		if _, err := load(env(bad)); err == nil || !strings.Contains(err.Error(), "LIFECYCLE_") {
			t.Fatalf("%v should be refused: %v", bad, err)
		}
	}
}

func TestMonthlyReportsAreOnByDefaultAndConfigurable(t *testing.T) {
	c, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil || !c.RunMonthlyReports || c.MonthlyReportInterval != time.Hour {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	c, err = load(env(map[string]string{"DATABASE_URL": "postgres://x", "DISABLE_MONTHLY_REPORTS": "true", "MONTHLY_REPORT_INTERVAL": "30m"}))
	if err != nil || c.RunMonthlyReports || c.MonthlyReportInterval != 30*time.Minute {
		t.Fatalf("read from the environment: %v %+v", err, c)
	}
	if _, err := load(env(map[string]string{"DATABASE_URL": "postgres://x", "MONTHLY_REPORT_INTERVAL": "10s"})); err == nil || !strings.Contains(err.Error(), "MONTHLY_REPORT_INTERVAL") {
		t.Fatalf("a too-short interval should be refused: %v", err)
	}
}
