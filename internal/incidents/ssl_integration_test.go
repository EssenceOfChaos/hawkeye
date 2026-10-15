package incidents_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/email"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
)

// certResult records one successful check that saw a certificate expiring at expiry, finishing at now.
func (e *env) certResult(t *testing.T, monitor uuid.UUID, expiry, now time.Time) {
	t.Helper()
	r := checks.Result{CheckedAt: now, FinishedAt: now.Add(100 * time.Millisecond), Success: true, CertExpiresAt: &expiry}
	if _, err := e.store.Record(t.Context(), monitor, r); err != nil {
		t.Fatal(err)
	}
}

func (e *env) sslDeliveries(t *testing.T) int {
	return e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type = 'ssl_expiry'`)
}

func TestCertificateWarningsAreSentOncePerBucketAndRecipient(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.recipient(t, "b@example.com", true, true)
	e.recipient(t, "off@example.com", false, true) // disabled: never warned
	e.recipient(t, "new@example.com", true, false) // unverified: never warned
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(45 * 24 * time.Hour)

	e.certResult(t, e.monitor, expiry, now)
	if n := e.sslDeliveries(t); n != 0 {
		t.Fatalf("a certificate with 45 days left must not warn (got %d)", n)
	}

	// Walk the certificate through every bucket, checking several times inside each one.
	steps := []struct {
		daysLeft float64
		want     int // total deliveries so far (2 recipients per bucket)
	}{
		{29, 2}, {28, 2}, {13, 4}, {12, 4}, {6, 6}, {5, 6}, {0.5, 8}, {0.25, 8},
	}
	for _, s := range steps {
		at := expiry.Add(-time.Duration(s.daysLeft * 24 * float64(time.Hour)))
		e.certResult(t, e.monitor, expiry, at)
		if n := e.sslDeliveries(t); n != s.want {
			t.Fatalf("%.2f days left: %d deliveries, want %d", s.daysLeft, n, s.want)
		}
	}
	// Once expired: one more warning (bucket 0), however many checks follow.
	e.certResult(t, e.monitor, expiry, expiry.Add(time.Hour))
	e.certResult(t, e.monitor, expiry, expiry.Add(2*time.Hour))
	if n := e.sslDeliveries(t); n != 10 {
		t.Fatalf("after expiry: %d deliveries, want 10 (30, 14, 7, 1 and expired for two recipients)", n)
	}
	for _, addr := range []string{"off@example.com", "new@example.com"} {
		if e.count(t, `SELECT count(*) FROM notification_deliveries d JOIN notification_settings s ON s.id = d.notification_setting_id
			WHERE d.event_type = 'ssl_expiry' AND s.destination = $1`, addr) != 0 {
			t.Fatalf("%s must not receive certificate warnings", addr)
		}
	}
	var stored *time.Time
	e.pool.QueryRow(t.Context(), `SELECT ssl_expires_at FROM monitors WHERE id = $1`, e.monitor).Scan(&stored)
	if stored == nil || !stored.Equal(expiry) {
		t.Fatalf("stored expiry %v, want %v", stored, expiry)
	}
}

func TestARenewedCertificateStartsTheScheduleAgain(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(5 * 24 * time.Hour)
	e.certResult(t, e.monitor, old, now) // inside the 7-day bucket
	if n := e.sslDeliveries(t); n != 1 {
		t.Fatalf("%d", n)
	}
	renewed := now.Add(80 * 24 * time.Hour)
	e.certResult(t, e.monitor, renewed, now.Add(time.Hour))
	if n := e.sslDeliveries(t); n != 1 {
		t.Fatalf("renewing must not send anything (%d deliveries)", n)
	}
	// ...but when the new certificate nears expiry it is warned about from the 30-day bucket again, not skipped.
	e.certResult(t, e.monitor, renewed, renewed.Add(-20*24*time.Hour))
	if n := e.sslDeliveries(t); n != 2 {
		t.Fatalf("%d deliveries after the renewed certificate entered the 30-day bucket, want 2", n)
	}
}

func TestChangingARecipientLaterDoesNotResendOldBuckets(t *testing.T) {
	e := newEnv(t, 5)
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(10 * 24 * time.Hour)
	e.certResult(t, e.monitor, expiry, now) // no recipient yet: bucket 14 is marked as handled
	e.recipient(t, "late@example.com", true, true)
	e.certResult(t, e.monitor, expiry, now.Add(time.Hour))
	if n := e.sslDeliveries(t); n != 0 {
		t.Fatalf("a recipient added after the bucket was handled must not get a backlog (%d)", n)
	}
}

func TestPausedAndDeletedMonitorsAreNotWarnedAbout(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	e.pool.Exec(t.Context(), `UPDATE monitors SET enabled = false WHERE id = $1`, e.monitor)
	now := time.Now().UTC()
	expiry := now.Add(2 * 24 * time.Hour)
	r := checks.Result{CheckedAt: now, FinishedAt: now, Success: true, CertExpiresAt: &expiry}
	if _, err := e.store.Record(t.Context(), e.monitor, r); err != checks.ErrDiscarded {
		t.Fatalf("paused monitor result must be discarded, got %v", err)
	}
	if n := e.sslDeliveries(t); n != 0 {
		t.Fatalf("%d", n)
	}
}

func TestCertificateWarningEmailIsDeliveredAndBranded(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "owner@example.com", true, true)
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(5 * 24 * time.Hour)
	e.certResult(t, e.monitor, expiry, now)

	mail := &email.Memory{}
	disp := notifications.NewDispatcher(e.pool, mail, notifications.DispatcherConfig{AppBaseURL: "https://app.example.com", From: "alerts@mail.example.com"}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if worked, err := disp.ProcessOne(t.Context()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	msgs := mail.Messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	m := msgs[0]
	if m.To != "owner@example.com" || !strings.HasPrefix(m.Subject, "HawkEye reminder: SSL certificate for Site expires in ") {
		t.Fatalf("%q to %q", m.Subject, m.To)
	}
	for _, want := range []string{"https://example.com", expiry.Format("2006-01-02 15:04 UTC"), "/monitors/" + e.monitor.String()} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text missing %q:\n%s", want, m.Text)
		}
	}
	if !strings.Contains(m.HTML, "#c62828") {
		t.Error("a warning inside 7 days should use the alert colour")
	}
	if n := e.count(t, `SELECT count(*) FROM notification_deliveries WHERE event_type='ssl_expiry' AND status='sent'`); n != 1 {
		t.Fatalf("delivery not marked sent (%d)", n)
	}
}

func TestExpiredCertificateEmail(t *testing.T) {
	m := notifications.Render(notifications.Alert{Event: notifications.EventSSLExpiry, MonitorID: "abc", MonitorName: "Shop", MonitorURL: "https://shop.example.com/",
		SSLExpiresAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), SSLBucket: 0, AppBaseURL: "https://app.example.com", To: "a@b.co", From: "x@y.co"})
	if m.Subject != "HawkEye alert: SSL certificate for Shop has expired" || !strings.Contains(m.Text, "has expired") || !strings.Contains(m.Text, "/monitors/abc") {
		t.Fatalf("%q\n%s", m.Subject, m.Text)
	}
}

func TestDeletingAMonitorRemovesItsCertificateWarnings(t *testing.T) {
	e := newEnv(t, 5)
	e.recipient(t, "a@example.com", true, true)
	now := time.Now().UTC().Truncate(time.Second)
	e.certResult(t, e.monitor, now.Add(3*24*time.Hour), now)
	if e.sslDeliveries(t) != 1 {
		t.Fatal("setup")
	}
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM monitors WHERE id = $1`, e.monitor); err != nil {
		t.Fatal(err)
	}
	if n := e.sslDeliveries(t); n != 0 {
		t.Fatalf("%d orphaned certificate deliveries", n)
	}
}
