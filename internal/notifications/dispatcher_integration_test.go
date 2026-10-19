package notifications_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/metrics"
	"hawkeye/internal/notifications"
	"hawkeye/internal/testutil"
)

// scriptedSender fails according to a script, then succeeds.
type scriptedSender struct {
	mu    sync.Mutex
	errs  []error // consumed one per call; nil entries and an exhausted script mean success
	calls int
	sent  []email.Message
}

func (s *scriptedSender) Send(_ context.Context, m email.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		if err != nil {
			return err
		}
	}
	s.sent = append(s.sent, m)
	return nil
}

func (s *scriptedSender) callCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

type env struct {
	pool    *pgxpool.Pool
	org     uuid.UUID
	monitor uuid.UUID
	inc     uuid.UUID
}

func newEnv(t *testing.T, conns int32) *env {
	t.Helper()
	e := &env{pool: testutil.NewPoolSize(t, conns)}
	ctx := t.Context()
	e.pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&e.org)
	e.pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds)
		VALUES ($1, 'Site', 'https://example.com', 60) RETURNING id`, e.org).Scan(&e.monitor)
	e.pool.QueryRow(ctx, `INSERT INTO incidents (monitor_id, started_at, cause_category, summary)
		VALUES ($1, now() - interval '10 minutes', 'http_status', 'Received HTTP 503') RETURNING id`, e.monitor).Scan(&e.inc)
	return e
}

func (e *env) delivery(t *testing.T, addr, event string) (setting, delivery uuid.UUID) {
	t.Helper()
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at)
		VALUES ($1, 'email', $2, now()) RETURNING id`, e.org, addr).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		VALUES ($1, $2, $3) RETURNING id`, e.inc, setting, event).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	return
}

type row struct {
	Status   string
	Attempts int
	SentAt   *time.Time
	LastErr  *string
}

func (e *env) row(t *testing.T, id uuid.UUID) row {
	t.Helper()
	var r row
	if err := e.pool.QueryRow(t.Context(), `SELECT status, attempt_count, sent_at, last_error FROM notification_deliveries WHERE id=$1`, id).
		Scan(&r.Status, &r.Attempts, &r.SentAt, &r.LastErr); err != nil {
		t.Fatal(err)
	}
	return r
}

func dispatcher(e *env, s email.Sender, cfg notifications.DispatcherConfig) *notifications.Dispatcher {
	if cfg.Backoff == nil {
		cfg.Backoff = func(int) time.Duration { return 0 } // retry immediately in tests
	}
	cfg.AppBaseURL, cfg.From = "https://app.example.com", "alerts@example.com"
	return notifications.NewDispatcher(e.pool, s, cfg, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestOutageEmailIsSentOnce(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "owner@example.com", "outage")
	s := &scriptedSender{}
	d := dispatcher(e, s, notifications.DispatcherConfig{})

	if worked, err := d.ProcessOne(t.Context()); !worked || err != nil {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	r := e.row(t, id)
	if r.Status != "sent" || r.Attempts != 1 || r.SentAt == nil || r.LastErr != nil {
		t.Fatalf("%+v", r)
	}
	if len(s.sent) != 1 || s.sent[0].To != "owner@example.com" || s.sent[0].From != "alerts@example.com" || s.sent[0].Subject != "HawkEye alert: Site may be offline" {
		t.Fatalf("%+v", s.sent)
	}
	if worked, _ := d.ProcessOne(t.Context()); worked || s.callCount() != 1 {
		t.Fatal("a sent delivery must never be sent again")
	}
}

// Phase 5 acceptance: temporary email failures are retried safely.
func TestTransientFailureIsRetriedAndDeliveredOnce(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "owner@example.com", "outage")
	s := &scriptedSender{errs: []error{errors.New("smtp: connection reset"), errors.New("smtp: 451 try later")}}
	d := dispatcher(e, s, notifications.DispatcherConfig{MaxAttempts: 5})

	d.ProcessOne(t.Context())
	if r := e.row(t, id); r.Status != "pending" || r.Attempts != 1 || r.LastErr == nil {
		t.Fatalf("after first failure: %+v", r)
	}
	d.ProcessOne(t.Context())
	d.ProcessOne(t.Context())
	r := e.row(t, id)
	if r.Status != "sent" || r.Attempts != 3 || len(s.sent) != 1 {
		t.Fatalf("%+v sent=%d", r, len(s.sent))
	}
	if worked, _ := d.ProcessOne(t.Context()); worked {
		t.Fatal("nothing left to do")
	}
}

func TestRetryWaitsForBackoff(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "owner@example.com", "outage")
	s := &scriptedSender{errs: []error{errors.New("temporary")}}
	d := dispatcher(e, s, notifications.DispatcherConfig{Backoff: func(int) time.Duration { return time.Hour }})
	d.ProcessOne(t.Context())
	if worked, _ := d.ProcessOne(t.Context()); worked || e.row(t, id).Status != "pending" {
		t.Fatal("a failed delivery must wait for its backoff before the next attempt")
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "owner@example.com", "outage")
	errs := make([]error, 10)
	for i := range errs {
		errs[i] = errors.New("provider down")
	}
	s := &scriptedSender{errs: errs}
	d := dispatcher(e, s, notifications.DispatcherConfig{MaxAttempts: 3})
	for range 6 {
		d.ProcessOne(t.Context())
	}
	r := e.row(t, id)
	if r.Status != "failed" || r.Attempts != 3 || s.callCount() != 3 || r.LastErr == nil {
		t.Fatalf("%+v calls=%d", r, s.callCount())
	}
}

func TestPermanentFailureIsNotRetried(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "bad@example.com", "outage")
	s := &scriptedSender{errs: []error{fmt.Errorf("%w: mailbox does not exist", email.ErrPermanent)}}
	dispatcher(e, s, notifications.DispatcherConfig{MaxAttempts: 5}).ProcessOne(t.Context())
	if r := e.row(t, id); r.Status != "failed" || r.Attempts != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestDisabledOrUnverifiedAtSendTimeIsSkipped(t *testing.T) {
	e := newEnv(t, 5)
	sid, id := e.delivery(t, "owner@example.com", "outage")
	e.pool.Exec(t.Context(), `UPDATE notification_settings SET enabled = false WHERE id = $1`, sid)
	s := &scriptedSender{}
	dispatcher(e, s, notifications.DispatcherConfig{}).ProcessOne(t.Context())
	if r := e.row(t, id); r.Status != "skipped" || s.callCount() != 0 {
		t.Fatalf("%+v calls=%d", r, s.callCount())
	}
}

func TestIdempotencyConstraintBlocksDuplicates(t *testing.T) {
	e := newEnv(t, 5)
	sid, _ := e.delivery(t, "owner@example.com", "outage")
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		VALUES ($1, $2, 'outage')`, e.inc, sid); err == nil {
		t.Fatal("a second outage delivery for the same incident and recipient must be rejected")
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		VALUES ($1, $2, 'recovery')`, e.inc, sid); err != nil {
		t.Fatalf("recovery is a different event and must be allowed: %v", err)
	}

	tx, _ := e.pool.Begin(t.Context())
	n, err := notifications.Enqueue(t.Context(), tx, e.org, e.inc, notifications.EventOutage)
	tx.Commit(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("re-enqueueing must be a no-op, inserted %d (%v)", n, err)
	}
}

func TestConcurrentDispatchersSendEachDeliveryExactlyOnce(t *testing.T) {
	e := newEnv(t, 30)
	const n = 30
	for i := range n {
		e.delivery(t, fmt.Sprintf("user%d@example.com", i), "outage")
	}
	s := &scriptedSender{}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := dispatcher(e, s, notifications.DispatcherConfig{})
			<-start
			for {
				worked, err := d.ProcessOne(t.Context())
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	seen := map[string]int{}
	for _, m := range s.sent {
		seen[m.To]++
	}
	if len(seen) != n || s.callCount() != n {
		t.Fatalf("recipients=%d calls=%d, want %d each", len(seen), s.callCount(), n)
	}
}

func TestClaimedButUnfinishedDeliveryIsRetriedAfterLease(t *testing.T) {
	e := newEnv(t, 5)
	_, id := e.delivery(t, "owner@example.com", "outage")
	// A dispatcher claimed it and crashed before recording the outcome: attempt counted, lease running.
	e.pool.Exec(t.Context(), `UPDATE notification_deliveries SET attempt_count = 1, next_attempt_at = now() + interval '5 minutes' WHERE id = $1`, id)
	s := &scriptedSender{}
	d := dispatcher(e, s, notifications.DispatcherConfig{})
	if worked, _ := d.ProcessOne(t.Context()); worked {
		t.Fatal("must not steal a delivery whose lease is still running")
	}
	e.pool.Exec(t.Context(), `UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id)
	if worked, _ := d.ProcessOne(t.Context()); !worked || e.row(t, id).Status != "sent" || e.row(t, id).Attempts != 2 {
		t.Fatalf("%+v", e.row(t, id))
	}
}

func TestPendingMetric(t *testing.T) {
	e := newEnv(t, 5)
	e.delivery(t, "a@example.com", "outage")
	e.pool.Exec(t.Context(), `UPDATE notification_deliveries SET next_attempt_at = now() - interval '2 minutes'`)
	n, age, err := notifications.Pending(t.Context(), e.pool)
	if err != nil || n != 1 || age < time.Minute {
		t.Fatalf("n=%d age=%v err=%v", n, age, err)
	}
}
