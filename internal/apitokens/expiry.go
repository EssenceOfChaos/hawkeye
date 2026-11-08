package apitokens

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
)

const (
	// WarnBefore is how long before a key expires its owner is warned.
	WarnBefore = 7 * 24 * time.Hour
	// warnMinLifetime skips the warning for keys made to last little more than a week: their owner just chose that date.
	warnMinLifetime = 8 * 24 * time.Hour
	// expiredNoticeWindow is how long after expiry the "has expired" email may still go out (a worker that was down catches up, within reason).
	expiredNoticeWindow = 3 * 24 * time.Hour
	// Keys that can no longer be used are deleted after these periods: an expired key stays listed (as expired) for a while so its owner can see
	// what stopped working; a revoked key is already hidden.
	KeepRevoked = 30 * 24 * time.Hour
	KeepExpired = 90 * 24 * time.Hour
)

// ExpiryJob emails people before and when their API keys expire, and deletes keys that were revoked or expired long ago, so a script that is about to stop working is noticed. Each email is claimed in the
// database before it is sent, so with several workers it goes out at most once (a crash between the two loses the email rather than repeating it).
type ExpiryJob struct {
	pool       *pgxpool.Pool
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
	interval   time.Duration
	batch      int
	now        func() time.Time
}

func NewExpiryJob(pool *pgxpool.Pool, mail email.Sender, log *slog.Logger, appBaseURL string, interval time.Duration) *ExpiryJob {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &ExpiryJob{pool: pool, mail: mail, log: log, appBaseURL: appBaseURL, interval: interval, batch: 200, now: time.Now}
}

// WithClock replaces the clock (tests).
func (j *ExpiryJob) WithClock(now func() time.Time) *ExpiryJob {
	j.now = now
	return j
}

// Run does a pass immediately and then every interval until ctx is canceled.
func (j *ExpiryJob) Run(ctx context.Context) {
	t := time.NewTicker(j.interval)
	defer t.Stop()
	for {
		sent, err := j.RunOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			j.log.Error("API key expiry email pass failed", "error", err, "sent", sent)
		case sent > 0:
			j.log.Info("API key expiry emails sent", "sent", sent)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type expiring struct {
	id        uuid.UUID
	name      string
	prefix    string
	expiresAt time.Time
	address   string
	orgName   string
}

// RunOnce sends the warnings and notices that are due and returns how many went out. It also deletes keys revoked or expired long enough ago.
func (j *ExpiryJob) RunOnce(ctx context.Context) (int, error) {
	now := j.now()
	sent := 0
	if _, err := j.pool.Exec(ctx, `DELETE FROM api_tokens WHERE revoked_at < $1::timestamptz - $2::interval OR expires_at < $1::timestamptz - $3::interval`,
		now, KeepRevoked, KeepExpired); err != nil {
		return sent, fmt.Errorf("delete old API keys: %w", err)
	}
	// Keys about to expire: claim the warning, then send it.
	warn, err := j.claim(ctx, `UPDATE api_tokens t SET expiry_warned_at = $1
		FROM users u, organizations o
		WHERE t.id IN (SELECT id FROM api_tokens WHERE revoked_at IS NULL AND expiry_warned_at IS NULL AND expires_at > $1 AND expires_at <= $1 + $2::interval
			AND expires_at - created_at > $3::interval ORDER BY expires_at LIMIT $4 FOR UPDATE SKIP LOCKED)
		  AND u.id = t.user_id AND o.id = t.organization_id AND u.disabled_at IS NULL
		RETURNING t.id, t.name, t.prefix, t.expires_at, u.email, o.name`, now, WarnBefore, warnMinLifetime, j.batch)
	if err != nil {
		return sent, fmt.Errorf("claim expiry warnings: %w", err)
	}
	for _, k := range warn {
		j.send(ctx, k, false)
		sent++
	}
	// Keys that have just expired.
	gone, err := j.claim(ctx, `UPDATE api_tokens t SET expired_notified_at = $1
		FROM users u, organizations o
		WHERE t.id IN (SELECT id FROM api_tokens WHERE revoked_at IS NULL AND expired_notified_at IS NULL AND expires_at <= $1 AND expires_at > $1 - $2::interval
			ORDER BY expires_at LIMIT $3 FOR UPDATE SKIP LOCKED)
		  AND u.id = t.user_id AND o.id = t.organization_id AND u.disabled_at IS NULL
		RETURNING t.id, t.name, t.prefix, t.expires_at, u.email, o.name`, now, expiredNoticeWindow, j.batch)
	if err != nil {
		return sent, fmt.Errorf("claim expired notices: %w", err)
	}
	for _, k := range gone {
		j.send(ctx, k, true)
		sent++
	}
	return sent, nil
}

func (j *ExpiryJob) claim(ctx context.Context, sql string, args ...any) ([]expiring, error) {
	rows, err := j.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []expiring
	for rows.Next() {
		var k expiring
		if err := rows.Scan(&k.id, &k.name, &k.prefix, &k.expiresAt, &k.address, &k.orgName); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (j *ExpiryJob) send(ctx context.Context, k expiring, expired bool) {
	date := k.expiresAt.UTC().Format("January 2, 2006")
	c := email.Content{
		Facts:       []email.Fact{{Label: "Key", Value: k.name + " (" + k.prefix + "…)"}, {Label: "Organization", Value: k.orgName}, {Label: "Expires", Value: date}},
		ButtonLabel: "Manage API keys", ButtonURL: j.appBaseURL + "/api-keys", AppBaseURL: j.appBaseURL,
		Note: "Expired keys can't be extended. Create a new key and replace the old one wherever it is used.",
	}
	subject := "Your HawkEye API key expires soon"
	if expired {
		c.Preheader, c.Heading, c.Tone = "An API key on your HawkEye account has expired.", "Your API key has expired", email.ToneAlert
		c.Paragraphs = []string{"This API key has expired, so requests made with it are now refused. Anything still using it has stopped working."}
		subject = "Your HawkEye API key has expired"
	} else {
		c.Preheader, c.Heading = "An API key on your HawkEye account expires soon.", "Your API key expires soon"
		c.Paragraphs = []string{"This API key expires on " + date + ". After that, requests made with it are refused, so replace it before then " +
			"anywhere it is still in use."}
	}
	if err := j.mail.Send(ctx, c.Message("", k.address, subject)); err != nil {
		j.log.ErrorContext(ctx, "send API key expiry email failed", "token_id", k.id, "expired", expired, "error", err)
	}
}
