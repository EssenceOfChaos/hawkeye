package config

import (
	"log/slog"
	"strings"
	"testing"
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

func TestProductionValid(t *testing.T) {
	_, err := load(env(map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://x", "APP_BASE_URL": "https://example.com",
		"SESSION_SECRET": strings.Repeat("a", 40),
	}))
	if err != nil {
		t.Fatal(err)
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
	if c.WorkerConcurrency != 10 || c.FailureThreshold != 2 || c.RecoveryThreshold != 2 || !c.RunScheduler || c.JobLease.Minutes() != 2 {
		t.Fatalf("%+v", c)
	}
	_, err = load(env(map[string]string{"DATABASE_URL": "postgres://x", "WORKER_CONCURRENCY": "0", "JOB_LEASE": "10s", "FAILURE_THRESHOLD": "0"}))
	for _, want := range []string{"WORKER_CONCURRENCY", "JOB_LEASE", "FAILURE_THRESHOLD"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("expected %s error, got %v", want, err)
		}
	}
}
