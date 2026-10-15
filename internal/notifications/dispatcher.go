package notifications

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
)

// Delivery statuses.
const (
	StatusPending = "pending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

type DispatcherConfig struct {
	MaxAttempts  int
	Lease        time.Duration // how long a claimed delivery is invisible to others while being sent
	PollInterval time.Duration
	Backoff      func(attempt int) time.Duration // delay before retry after the given failed attempt
	AppBaseURL   string
	From         string
}

// DefaultBackoff retries after 1m, 5m, 15m, 1h, 3h.
func DefaultBackoff(attempt int) time.Duration {
	steps := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour}
	return steps[min(max(attempt, 1), len(steps))-1]
}

func (c DispatcherConfig) withDefaults() DispatcherConfig {
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 6
	}
	if c.Lease <= 0 {
		c.Lease = 5 * time.Minute
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.Backoff == nil {
		c.Backoff = DefaultBackoff
	}
	return c
}

// Dispatcher sends queued alert emails. Delivery is at-least-once: a delivery is claimed with a visibility
// timeout, sent, then marked sent. A crash between send and mark leads to one retry after the lease, which is
// the right trade-off for alerts (a duplicate email is better than a lost one).
type Dispatcher struct {
	pool *pgxpool.Pool
	mail email.Sender
	cfg  DispatcherConfig
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
}

func NewDispatcher(pool *pgxpool.Pool, mail email.Sender, cfg DispatcherConfig, m *metrics.Metrics, log *slog.Logger) *Dispatcher {
	return &Dispatcher{pool: pool, mail: mail, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends alert deliveries that failed permanently to an error tracker.
func (d *Dispatcher) WithReporter(r errtrack.Reporter) *Dispatcher {
	d.rep = r
	return d
}

// Run polls until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := d.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil {
			d.log.Error("notification dispatch failed", "error", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(d.cfg.PollInterval + time.Duration(rand.Float64()*float64(d.cfg.PollInterval)/2)):
		}
	}
}

type claimed struct {
	id       uuid.UUID
	attempts int
	alert    Alert
	enabled  bool
	verified bool
}

// ProcessOne claims and attempts at most one due delivery. It reports whether one was found.
func (d *Dispatcher) ProcessOne(ctx context.Context) (bool, error) {
	var c claimed
	err := d.pool.QueryRow(ctx, `WITH picked AS (
			UPDATE notification_deliveries SET attempt_count = attempt_count + 1, last_attempt_at = now(),
				next_attempt_at = now() + $1::interval
			WHERE id = (SELECT id FROM notification_deliveries WHERE status = 'pending' AND next_attempt_at <= now()
				ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1)
			RETURNING id, incident_id, monitor_id, notification_setting_id, event_type, attempt_count, ssl_expires_at, ssl_bucket)
		SELECT p.id, p.attempt_count, p.event_type, s.destination, s.enabled, s.verified_at IS NOT NULL,
			COALESCE(i.started_at, now()), i.resolved_at, COALESCE(i.summary, ''), m.id::text, m.name, m.url,
			COALESCE(p.ssl_expires_at, now()), COALESCE(p.ssl_bucket, 0)
		FROM picked p
		JOIN notification_settings s ON s.id = p.notification_setting_id
		LEFT JOIN incidents i ON i.id = p.incident_id
		JOIN monitors m ON m.id = COALESCE(i.monitor_id, p.monitor_id)`, d.cfg.Lease).
		Scan(&c.id, &c.attempts, &c.alert.Event, &c.alert.To, &c.enabled, &c.verified,
			&c.alert.StartedAt, &c.alert.ResolvedAt, &c.alert.Cause, &c.alert.MonitorID, &c.alert.MonitorName, &c.alert.MonitorURL,
			&c.alert.SSLExpiresAt, &c.alert.SSLBucket)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	// Settings may have changed since the alert was queued.
	if !c.enabled || !c.verified {
		d.finish(ctx, c, StatusSkipped, "")
		return true, nil
	}
	c.alert.AppBaseURL, c.alert.From = d.cfg.AppBaseURL, d.cfg.From
	sendErr := d.mail.Send(ctx, Render(c.alert))
	switch {
	case sendErr == nil:
		d.finish(ctx, c, StatusSent, "")
	case errors.Is(sendErr, email.ErrPermanent) || c.attempts >= d.cfg.MaxAttempts:
		d.log.Warn("alert delivery failed permanently", "delivery_id", c.id, "attempts", c.attempts)
		d.rep.Capture(ctx, sendErr, map[string]string{"component": "notifications", "delivery_id": c.id.String(), "event": c.alert.Event})
		d.finish(ctx, c, StatusFailed, sendErr.Error())
	default:
		d.log.Warn("alert delivery failed; will retry", "delivery_id", c.id, "attempt", c.attempts)
		d.m.NotificationsTotal.WithLabelValues(c.alert.Event, "retry").Inc()
		_, err := d.pool.Exec(context.WithoutCancel(ctx), `UPDATE notification_deliveries
			SET next_attempt_at = now() + $2::interval, last_error = $3 WHERE id = $1`,
			c.id, d.cfg.Backoff(c.attempts), truncate(sendErr.Error()))
		return true, err
	}
	return true, nil
}

func (d *Dispatcher) finish(ctx context.Context, c claimed, status, errText string) {
	// Use a fresh context: the email may have been sent and the outcome must still be recorded on shutdown.
	_, err := d.pool.Exec(context.WithoutCancel(ctx), `UPDATE notification_deliveries SET status = $2,
			sent_at = CASE WHEN $2 = 'sent' THEN now() ELSE sent_at END, last_error = NULLIF($3, '') WHERE id = $1`,
		c.id, status, truncate(errText))
	if err != nil {
		d.log.Error("record delivery outcome failed; it may be retried", "delivery_id", c.id, "error", err)
		return
	}
	d.m.NotificationsTotal.WithLabelValues(c.alert.Event, status).Inc()
}

// truncate bounds stored error text. Provider errors can echo addresses and content, so only a prefix is kept.
func truncate(s string) string {
	s = clean(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Pending reports the number of deliveries waiting and the age of the oldest due one, for metrics.
func Pending(ctx context.Context, pool *pgxpool.Pool) (int, time.Duration, error) {
	var n int
	var secs float64
	err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(next_attempt_at) FILTER (WHERE next_attempt_at <= now())), 0)::float8
		FROM notification_deliveries WHERE status = 'pending'`).Scan(&n, &secs)
	return n, time.Duration(secs * float64(time.Second)), err
}
