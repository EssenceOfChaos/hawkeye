package monitors_test

import (
	"errors"
	"testing"

	"hawkeye/internal/monitors"
	"hawkeye/internal/validation"
)

func TestRecheckSetting(t *testing.T) {
	svc, _, org := setup(t, "pro", "active")
	ctx := t.Context()
	m, err := svc.Create(ctx, org, newIn("Site"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Recheck != "auto" {
		t.Fatalf("default recheck = %q, want auto", m.Recheck)
	}

	always := " Always "
	in := newIn("Search API")
	in.Recheck = &always
	if m, err = svc.Create(ctx, org, in); err != nil || m.Recheck != "always" {
		t.Fatalf("create with always: %q %v", m.Recheck, err)
	}
	off := "off"
	if m, err = svc.Update(ctx, org, m.ID, monitors.UpdateInput{Recheck: &off}); err != nil || m.Recheck != "off" {
		t.Fatalf("update to off: %q %v", m.Recheck, err)
	}
	// An unrelated edit keeps it.
	name := "Renamed"
	if m, err = svc.Update(ctx, org, m.ID, monitors.UpdateInput{Name: &name}); err != nil || m.Recheck != "off" {
		t.Fatalf("after rename: %q %v", m.Recheck, err)
	}

	bad := "sometimes"
	in = newIn("x")
	in.Recheck = &bad
	var ve *validation.Error
	if _, err := svc.Create(ctx, org, in); !errors.As(err, &ve) || ve.Fields["recheck"] == "" {
		t.Fatalf("invalid value: %v", err)
	}

	hb := monitors.CreateInput{Type: monitors.TypeHeartbeat, Name: "Nightly backup", Recheck: &off}
	if _, err := svc.Create(ctx, org, hb); !errors.As(err, &ve) || ve.Fields["recheck"] == "" {
		t.Fatalf("heartbeat with a recheck setting: %v", err)
	}
	hb.Recheck = nil
	if m, err = svc.Create(ctx, org, hb); err != nil || m.Recheck != "auto" {
		t.Fatalf("heartbeat: %q %v", m.Recheck, err)
	}
}
