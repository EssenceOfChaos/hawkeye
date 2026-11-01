package monitors_test

import (
	"encoding/json"
	"errors"
	"testing"

	"hawkeye/internal/monitors"
	"hawkeye/internal/validation"
)

func ptr[T any](v T) *T { return &v }

func TestSlowResponseSettingsAreValidated(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	m, err := svc.Create(t.Context(), org, newIn("Site"))
	if err != nil || m.SlowThresholdMs != nil || m.SlowChecks != monitors.DefaultSlowChecks || m.SlowSince != nil {
		t.Fatalf("off by default, three checks when turned on: %+v %v", m, err)
	}
	in := newIn("Slow")
	in.SlowThresholdMs, in.SlowChecks = ptr(1500), ptr(5)
	if m, err := svc.Create(t.Context(), org, in); err != nil || *m.SlowThresholdMs != 1500 || m.SlowChecks != 5 {
		t.Fatalf("%+v %v", m, err)
	}
	for name, c := range map[string]struct {
		threshold, checks, timeout *int
		field                      string
	}{
		"too low":             {ptr(50), nil, nil, "slow_threshold_ms"},
		"not under timeout":   {ptr(10000), nil, nil, "slow_threshold_ms"}, // the default timeout is 10 s
		"under short timeout": {ptr(3000), nil, ptr(3), "slow_threshold_ms"},
		"one check":           {ptr(1000), ptr(1), nil, "slow_checks"},
		"too many checks":     {ptr(1000), ptr(11), nil, "slow_checks"},
	} {
		in := newIn(name)
		in.SlowThresholdMs, in.SlowChecks, in.TimeoutSeconds = c.threshold, c.checks, c.timeout
		_, err := svc.Create(t.Context(), org, in)
		var verr *validation.Error
		if !errors.As(err, &verr) || verr.Fields[c.field] == "" {
			t.Errorf("%s: want an error on %s, got %v", name, c.field, err)
		}
	}
}

func TestChangingTheThresholdQuietlyClosesASlowdown(t *testing.T) {
	svc, pool, org := setup(t, "pro", "active")
	in := newIn("Site")
	in.SlowThresholdMs = ptr(1000)
	m, _ := svc.Create(t.Context(), org, in)
	pool.Exec(t.Context(), `UPDATE monitors SET slow_streak = 2 WHERE id = $1`, m.ID)
	pool.Exec(t.Context(), `INSERT INTO slowdowns (monitor_id, started_at, threshold_ms, peak_ms) VALUES ($1, now(), 1000, 2500)`, m.ID)
	if got, _ := svc.Get(t.Context(), org, m.ID); got.SlowSince == nil {
		t.Fatal("slow_since shows an open slowdown")
	}

	// Renaming the monitor changes nothing about its speed.
	if got, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: ptr("Shop")}); err != nil || got.SlowSince == nil {
		t.Fatalf("%+v %v", got.SlowSince, err)
	}
	// A new threshold starts over, without a "back to normal" alert.
	var up monitors.UpdateInput
	if err := json.Unmarshal([]byte(`{"slow_threshold_ms": 3000}`), &up); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Update(t.Context(), org, m.ID, up)
	if err != nil || got.SlowSince != nil || *got.SlowThresholdMs != 3000 {
		t.Fatalf("%+v %v", got, err)
	}
	var streak, open, alerts int
	pool.QueryRow(t.Context(), `SELECT slow_streak FROM monitors WHERE id = $1`, m.ID).Scan(&streak)
	pool.QueryRow(t.Context(), `SELECT count(*) FROM slowdowns WHERE status = 'open'`).Scan(&open)
	pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_deliveries`).Scan(&alerts)
	if streak != 0 || open != 0 || alerts != 0 {
		t.Fatalf("streak=%d open=%d alerts=%d", streak, open, alerts)
	}
	// null turns the alerts off.
	json.Unmarshal([]byte(`{"slow_threshold_ms": null}`), &up)
	if got, err := svc.Update(t.Context(), org, m.ID, up); err != nil || got.SlowThresholdMs != nil {
		t.Fatalf("%+v %v", got.SlowThresholdMs, err)
	}
}
