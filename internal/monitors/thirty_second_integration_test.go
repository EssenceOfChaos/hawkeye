package monitors_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

func fieldErr(t *testing.T, err error, field string) string {
	t.Helper()
	var ve *validation.Error
	if !errors.As(err, &ve) || ve.Fields[field] == "" {
		t.Fatalf("want a validation error on %s, got %v", field, err)
	}
	return ve.Fields[field]
}

// Agency may check every 30 seconds, but new monitors start at a minute unless someone asks for faster.
func TestAgencyMayCheckEveryThirtySeconds(t *testing.T) {
	svc, _, org := setup(t, "agency", "active")

	m, err := svc.Create(t.Context(), org, newIn("Default"))
	if err != nil {
		t.Fatal(err)
	}
	if m.CheckIntervalSeconds != 60 {
		t.Fatalf("default interval on Agency = %d, want 60", m.CheckIntervalSeconds)
	}

	in := newIn("Fast")
	in.CheckIntervalSeconds = ptr(30)
	fast, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}
	if fast.CheckIntervalSeconds != 30 || fast.EffectiveCheckIntervalSeconds != 30 {
		t.Fatalf("interval = %d, effective %d", fast.CheckIntervalSeconds, fast.EffectiveCheckIntervalSeconds)
	}

	dns, err := svc.Create(t.Context(), org, monitors.CreateInput{Type: monitors.TypeDNS, Name: "DNS", URL: "example.com", CheckIntervalSeconds: ptr(30)})
	if err != nil || dns.CheckIntervalSeconds != 30 {
		t.Fatalf("DNS every 30 seconds: %+v, %v", dns, err)
	}

	in = newIn("Too fast")
	in.CheckIntervalSeconds = ptr(29)
	_, err = svc.Create(t.Context(), org, in)
	fieldErr(t, err, "check_interval_seconds")
}

func TestThirtySecondsIsAgencyOnly(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	in := newIn("Fast")
	in.CheckIntervalSeconds = ptr(30)
	_, err := svc.Create(t.Context(), org, in)
	var le *plans.LimitError
	if !errors.As(err, &le) || le.Kind != plans.LimitCheckInterval || le.Limit != 60 {
		t.Fatalf("Pro at 30 seconds: %v", err)
	}
}

// A check still running when the next is due delays it, so a timeout may be at most half the interval.
func TestTimeoutAtMostHalfTheInterval(t *testing.T) {
	svc, _, org := setup(t, "agency", "active")
	in := newIn("Slow site")
	in.CheckIntervalSeconds, in.TimeoutSeconds = ptr(30), ptr(20)
	_, err := svc.Create(t.Context(), org, in)
	if msg := fieldErr(t, err, "timeout_seconds"); msg != "must be at most 15 seconds (half the check interval)" {
		t.Fatalf("message = %q", msg)
	}

	in.TimeoutSeconds = ptr(15)
	m, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}

	// Raising the timeout of an existing 30-second monitor is refused too.
	_, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{TimeoutSeconds: ptr(16)})
	fieldErr(t, err, "timeout_seconds")

	// A minute still allows the longest timeout, as before.
	slow := newIn("Minute")
	slow.CheckIntervalSeconds, slow.TimeoutSeconds = ptr(60), ptr(30)
	if _, err := svc.Create(t.Context(), org, slow); err != nil {
		t.Fatal(err)
	}
}

// Multi-step checks may take up to a minute, and heartbeat periods are how often a job runs: both keep the one-minute floor.
func TestWorkflowsAndHeartbeatsKeepTheMinute(t *testing.T) {
	svc, pool, org := setup(t, "agency", "active")
	_, err := svc.Create(t.Context(), org, monitors.CreateInput{Type: monitors.TypeWorkflow, Name: "Flow", CheckIntervalSeconds: ptr(30),
		Steps: []monitors.StepInput{{Name: "Home", Method: "GET", URL: "https://api.example.com/"}}})
	fieldErr(t, err, "check_interval_seconds")

	_, err = svc.Create(t.Context(), org, monitors.CreateInput{Type: monitors.TypeHeartbeat, Name: "Job", CheckIntervalSeconds: ptr(30)})
	fieldErr(t, err, "check_interval_seconds")

	// The database says the same, for anything that writes rows directly.
	hb, err := svc.Create(t.Context(), org, monitors.CreateInput{Type: monitors.TypeHeartbeat, Name: "Job", CheckIntervalSeconds: ptr(60)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE monitors SET check_interval_seconds = 30 WHERE id = $1`, hb.ID); err == nil {
		t.Fatal("the database accepted a 30-second heartbeat period")
	}
}

func TestBulkThirtySeconds(t *testing.T) {
	svc, _, org := setup(t, "agency", "active")
	a, err := svc.Create(t.Context(), org, newIn("A"))
	if err != nil {
		t.Fatal(err)
	}
	slow := newIn("B")
	slow.TimeoutSeconds = ptr(20)
	b, err := svc.Create(t.Context(), org, slow)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Bulk(t.Context(), org, monitors.BulkInput{Action: monitors.BulkSetInterval, MonitorIDs: []uuid.UUID{a.ID, b.ID}, CheckIntervalSeconds: ptr(30)})
	if msg := fieldErr(t, err, "check_interval_seconds"); msg != "must be at least 40 seconds: a selected monitor's timeout is 20 seconds, and a timeout may be at most half the interval" {
		t.Fatalf("message = %q", msg)
	}
	if n, err := svc.Bulk(t.Context(), org, monitors.BulkInput{Action: monitors.BulkSetInterval, MonitorIDs: []uuid.UUID{a.ID}, CheckIntervalSeconds: ptr(30)}); err != nil || n != 1 {
		t.Fatalf("bulk 30 seconds: %d, %v", n, err)
	}
}

// After a downgrade the stored 30 seconds is kept but the plan's minute applies, as for any interval below a plan's minimum.
func TestDowngradeRunsThirtySecondMonitorsEveryMinute(t *testing.T) {
	svc, pool, org := setup(t, "agency", "active")
	in := newIn("Fast")
	in.CheckIntervalSeconds = ptr(30)
	m, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'pro') WHERE organization_id = $1`, org); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(t.Context(), org, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CheckIntervalSeconds != 30 || got.EffectiveCheckIntervalSeconds != 60 {
		t.Fatalf("after downgrade: stored %d, effective %d", got.CheckIntervalSeconds, got.EffectiveCheckIntervalSeconds)
	}
}
