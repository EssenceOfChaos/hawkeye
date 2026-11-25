package notifications

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/secrets"
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
	// GroupWindow: after an alert of one kind reaches a destination, more of the same kind within this window are held and sent as one message
	// (see "Grouping" below). 0 uses DefaultGroupWindow; a negative value turns grouping off everywhere.
	GroupWindow time.Duration
}

// DefaultGroupWindow is how long further alerts of one kind wait to be sent together after the first.
const DefaultGroupWindow = 2 * time.Minute

// maxGroup bounds the alerts claimed for one grouped message. The message lists the first ones and counts the rest (Group).
const maxGroup = 100

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
	if c.GroupWindow == 0 {
		c.GroupWindow = DefaultGroupWindow
	}
	return c
}

// Grouping. When many monitors fail together (one server hosting twenty client sites), a destination gets one message listing them instead of
// twenty. It never delays or hides the first alert:
//
//   - The first alert of a kind (outage, recovery, flapping, stopped flapping) to a destination goes out as soon as it is due, as always.
//   - An alert of the same kind that falls due within GroupWindow of the last one sent there is held until GroupWindow after that one
//     (group_held), then sent with every other alert of that kind due for that destination, as one message. An alert is held at most once, so
//     none waits longer than GroupWindow, and a held alert is always sent, even if its problem ends meanwhile (cancelHeld leaves it alone).
//   - Alerts that fall due at the same moment (for example several held by the same quiet hours) are sent together too.
//
// Grouping is per destination, so routing still decides who hears about what: an agency's client channel only ever lists that client's monitors.
// Webhooks and PagerDuty always get one event per alert, as integrations expect (PagerDuty groups on its side). The owner can turn grouping off for
// the organization (organizations.alert_grouping).

// groupable reports whether alerts of this kind to this channel may be combined.
func groupable(event, channel string) bool {
	switch event {
	case EventOutage, EventRecovery, EventFlapping, EventFlappingEnded:
		return channel != ChannelWebhook && channel != ChannelPagerDuty
	}
	return false
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
	http *http.Client // reaches webhook and chat URLs
	box  *secrets.Box // opens stored webhook and chat URLs and signing secrets
	bot  *TelegramBot // sends to chats connected through HawkEye's own Telegram bot
}

func NewDispatcher(pool *pgxpool.Pool, mail email.Sender, cfg DispatcherConfig, m *metrics.Metrics, log *slog.Logger) *Dispatcher {
	return &Dispatcher{pool: pool, mail: mail, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}, http: NewHTTPClient()}
}

// WithSecrets sets the key that opens stored webhook and chat URLs. Without it those deliveries fail permanently.
func (d *Dispatcher) WithSecrets(b *secrets.Box) *Dispatcher {
	d.box = b
	return d
}

// WithTelegramBot sets HawkEye's own Telegram bot. Without it, deliveries to chats connected through the bot fail permanently.
func (d *Dispatcher) WithTelegramBot(b *TelegramBot) *Dispatcher {
	d.bot = b
	return d
}

// WithHTTPClient replaces the client used for webhook and chat deliveries (tests use a local server).
func (d *Dispatcher) WithHTTPClient(c *http.Client) *Dispatcher {
	d.http = c
	return d
}

// WithReporter sends alert deliveries that failed permanently to an error tracker.
func (d *Dispatcher) WithReporter(r errtrack.Reporter) *Dispatcher {
	d.rep = r
	return d
}

// Run polls until ctx is canceled.
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
	id        uuid.UUID
	settingID uuid.UUID
	attempts  int
	alert     Alert
	channel   string
	secret    string // webhook signing secret, sealed
	enabled   bool
	verified  bool
	// hawkeyeBot: a Telegram chat reached through HawkEye's own bot; alert.To is then the chat ID in clear.
	hawkeyeBot bool
}

// ProcessOne claims and attempts at most one due delivery, with the deliveries grouped with it. It reports whether a due delivery was found
// (also when it was only held back to be grouped).
func (d *Dispatcher) ProcessOne(ctx context.Context) (bool, error) {
	var batch []claimed
	found := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var list []uuid.UUID
		var err error
		found, list, err = d.claim(ctx, tx)
		if err != nil || len(list) == 0 {
			return err
		}
		batch, err = d.load(ctx, tx, list)
		return err
	})
	if err != nil || len(batch) == 0 {
		return found, err
	}
	c := batch[0]

	// Settings may have changed since the alert was queued.
	if !c.enabled || !c.verified {
		d.finish(ctx, batch, StatusSkipped, "")
		return true, nil
	}
	attempts := 0
	for i := range batch {
		batch[i].alert.AppBaseURL, batch[i].alert.From = d.cfg.AppBaseURL, d.cfg.From
		attempts = max(attempts, batch[i].attempts)
	}
	sendErr := d.send(ctx, batch)
	switch {
	case sendErr == nil:
		d.finish(ctx, batch, StatusSent, "")
		if len(batch) > 1 {
			d.m.NotificationGroups.WithLabelValues(c.alert.Event).Inc()
		}
	case errors.Is(sendErr, email.ErrPermanent) || errors.Is(sendErr, errPermanent) || attempts >= d.cfg.MaxAttempts:
		d.log.Warn("alert delivery failed permanently", "delivery_id", c.id, "alerts", len(batch), "attempts", attempts)
		d.rep.Capture(ctx, sendErr, map[string]string{"component": "notifications", "delivery_id": c.id.String(), "event": c.alert.Event})
		d.finish(ctx, batch, StatusFailed, sendErr.Error())
	default:
		d.log.Warn("alert delivery failed; will retry", "delivery_id", c.id, "alerts", len(batch), "attempt", attempts)
		d.m.NotificationsTotal.WithLabelValues(c.alert.Event, "retry").Add(float64(len(batch)))
		_, err := d.pool.Exec(context.WithoutCancel(ctx), `UPDATE notification_deliveries
			SET next_attempt_at = now() + $2::interval, last_error = $3 WHERE id = ANY($1)`,
			ids(batch), d.cfg.Backoff(attempts), truncate(sendErr.Error()))
		return true, err
	}
	return true, nil
}

func ids(batch []claimed) []uuid.UUID {
	out := make([]uuid.UUID, len(batch))
	for i, c := range batch {
		out[i] = c.id
	}
	return out
}

// claim picks the next due delivery and leases it, together with the deliveries it is grouped with. It reports whether a delivery was due, and
// which ones to send now: none when the one found was held back to be grouped.
func (d *Dispatcher) claim(ctx context.Context, tx pgx.Tx) (bool, []uuid.UUID, error) {
	var id, settingID uuid.UUID
	var event, channel string
	var attempts int
	var held, grouping bool
	err := tx.QueryRow(ctx, `SELECT nd.id, nd.notification_setting_id, nd.event_type, nd.attempt_count, nd.group_held, s.channel, o.alert_grouping
		FROM notification_deliveries nd
		JOIN notification_settings s ON s.id = nd.notification_setting_id
		JOIN organizations o ON o.id = s.organization_id
		WHERE nd.status = 'pending' AND nd.next_attempt_at <= now()
		ORDER BY nd.next_attempt_at FOR UPDATE OF nd SKIP LOCKED LIMIT 1`).Scan(&id, &settingID, &event, &attempts, &held, &channel, &grouping)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	list := []uuid.UUID{id}
	if grouping && d.cfg.GroupWindow > 0 && groupable(event, channel) {
		// One dispatcher at a time decides for a destination, so two alerts claimed at once can't both think they are the first, or both wait.
		// NO KEY UPDATE doesn't block the foreign key checks of new deliveries being queued for the destination.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM notification_settings WHERE id = $1 FOR NO KEY UPDATE`, settingID); err != nil {
			return false, nil, err
		}
		if attempts == 0 && !held {
			// When the last alert of this kind was sent (or is being sent) to this destination, if that was within the window.
			var last *time.Time
			if err := tx.QueryRow(ctx, `SELECT max(last_attempt_at) FROM notification_deliveries
				WHERE notification_setting_id = $1 AND event_type = $2 AND id <> $3 AND status IN ('sent', 'pending')
					AND last_attempt_at > now() - $4::interval`, settingID, event, id, d.cfg.GroupWindow).Scan(&last); err != nil {
				return false, nil, err
			}
			if last != nil {
				_, err := tx.Exec(ctx, `UPDATE notification_deliveries SET group_held = true, next_attempt_at = $2::timestamptz + $3::interval
					WHERE id = $1`, id, *last, d.cfg.GroupWindow)
				return true, nil, err
			}
		}
		rows, err := tx.Query(ctx, `SELECT id FROM notification_deliveries
			WHERE notification_setting_id = $1 AND event_type = $2 AND id <> $3 AND status = 'pending' AND next_attempt_at <= now()
			ORDER BY next_attempt_at, created_at FOR UPDATE SKIP LOCKED LIMIT $4`, settingID, event, id, maxGroup-1)
		if err != nil {
			return false, nil, err
		}
		more, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return false, nil, err
		}
		list = append(list, more...)
	}
	_, err = tx.Exec(ctx, `UPDATE notification_deliveries SET attempt_count = attempt_count + 1, last_attempt_at = now(),
		next_attempt_at = now() + $2::interval WHERE id = ANY($1)`, list, d.cfg.Lease)
	return true, list, err
}

// load reads what each claimed delivery's alert is built from, in the order claimed (the first is the one picked).
func (d *Dispatcher) load(ctx context.Context, tx pgx.Tx, list []uuid.UUID) ([]claimed, error) {
	rows, err := tx.Query(ctx, `SELECT p.id, s.id, s.channel, s.hawkeye_bot, COALESCE(s.signing_secret, ''), p.attempt_count, p.event_type, s.destination,
			s.enabled, s.verified_at IS NOT NULL,
			COALESCE(i.started_at, sd.started_at, dc.detected_at, fe.started_at, now()), COALESCE(i.resolved_at, sd.resolved_at, dc.closed_at, fe.ended_at),
			COALESCE(i.summary, fl.summary, ''), COALESCE(m.id::text, ''), COALESCE(m.name, ''), COALESCE(m.url, ''), COALESCE(m.kind, 'http'),
			COALESCE(p.ssl_expires_at, now()), COALESCE(p.ssl_bucket, 0),
			COALESCE(dm.name, ''), COALESCE(p.domain_expires_at, now()), COALESCE(p.domain_bucket, 0),
			COALESCE(sd.threshold_ms, 0), COALESCE(sd.peak_ms, 0), COALESCE(sd.cause, ''),
			COALESCE(dc.record_type, ''), COALESCE(dc.before, '{}'), COALESCE(dc.after, '{}'), COALESCE(dc.status, ''), COALESCE(au.email, ''),
			COALESCE(fe.id::text, ''), fe.started_at, COALESCE(fe.outages, 0), COALESCE(fe.status, '')
		FROM unnest($1::uuid[]) WITH ORDINALITY AS c(id, n)
		JOIN notification_deliveries p ON p.id = c.id
		JOIN notification_settings s ON s.id = p.notification_setting_id
		LEFT JOIN incidents i ON i.id = p.incident_id
		LEFT JOIN slowdowns sd ON sd.id = p.slowdown_id
		LEFT JOIN dns_changes dc ON dc.id = p.dns_change_id
		LEFT JOIN users au ON au.id = dc.accepted_by
		LEFT JOIN flap_episodes fe ON fe.id = COALESCE(p.flap_episode_id, i.flap_episode_id)
		LEFT JOIN LATERAL (SELECT li.summary FROM incidents li WHERE p.flap_episode_id IS NOT NULL AND li.flap_episode_id = p.flap_episode_id
			ORDER BY li.started_at DESC LIMIT 1) fl ON true -- the cause of the episode's latest outage, for the "flapping" alert
		LEFT JOIN monitors m ON m.id = COALESCE(i.monitor_id, p.monitor_id, sd.monitor_id, dc.monitor_id, fe.monitor_id)
		LEFT JOIN domains dm ON dm.id = p.domain_id
		WHERE COALESCE(m.id, dm.id) IS NOT NULL
		ORDER BY c.n`, list)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.settingID, &c.channel, &c.hawkeyeBot, &c.secret, &c.attempts, &c.alert.Event, &c.alert.To, &c.enabled, &c.verified,
			&c.alert.StartedAt, &c.alert.ResolvedAt, &c.alert.Cause, &c.alert.MonitorID, &c.alert.MonitorName, &c.alert.MonitorURL, &c.alert.MonitorType,
			&c.alert.SSLExpiresAt, &c.alert.SSLBucket, &c.alert.DomainName, &c.alert.DomainExpiresAt, &c.alert.DomainBucket,
			&c.alert.SlowThresholdMs, &c.alert.SlowPeakMs, &c.alert.SlowCause,
			&c.alert.DNSRecordType, &c.alert.DNSBefore, &c.alert.DNSAfter, &c.alert.DNSStatus, &c.alert.DNSAcceptedBy,
			&c.alert.FlapEpisodeID, &c.alert.FlappingSince, &c.alert.FlapOutages, &c.alert.FlapStatus); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// send delivers one alert, or several grouped into one message, over the destination's channel. For the other channels, alert.To holds the sealed
// URL. Only groupable alerts to one destination are ever claimed together.
func (d *Dispatcher) send(ctx context.Context, batch []claimed) error {
	c := batch[0]
	if len(batch) > 1 {
		alerts := make([]Alert, len(batch))
		for i, b := range batch {
			alerts[i] = b.alert
		}
		c.alert = Group(alerts)
	}
	if c.channel == ChannelEmail {
		return d.mail.Send(ctx, Render(c.alert))
	}
	var target string
	var err error
	switch {
	case c.hawkeyeBot && d.bot == nil:
		return permanent("HawkEye's Telegram bot is not configured")
	case c.hawkeyeBot:
		target = d.bot.sendURL(c.alert.To)
	default:
		if target, err = d.box.Open(c.alert.To, c.settingID.String()); err != nil {
			return permanent("stored destination cannot be opened")
		}
	}
	secret := ""
	if c.channel == ChannelWebhook {
		if secret, err = d.box.Open(c.secret, c.settingID.String()); err != nil {
			return permanent("stored signing secret cannot be opened")
		}
	}
	to, body, header, err := buildRequest(c.channel, target, c.alert, secret, c.id.String(), time.Now())
	if err != nil {
		return permanent("message could not be built")
	}
	return post(ctx, d.http, to, body, header)
}

func (d *Dispatcher) finish(ctx context.Context, batch []claimed, status, errText string) {
	// Use a fresh context: the email may have been sent and the outcome must still be recorded on shutdown.
	_, err := d.pool.Exec(context.WithoutCancel(ctx), `UPDATE notification_deliveries SET status = $2,
			sent_at = CASE WHEN $2 = 'sent' THEN now() ELSE sent_at END, last_error = NULLIF($3, '') WHERE id = ANY($1)`,
		ids(batch), status, truncate(errText))
	if err != nil {
		d.log.Error("record delivery outcome failed; it may be retried", "delivery_id", batch[0].id, "alerts", len(batch), "error", err)
		return
	}
	d.m.NotificationsTotal.WithLabelValues(batch[0].alert.Event, status).Add(float64(len(batch)))
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
