package adconversions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
)

// DefaultEndpoint is OpenAI's Conversions API; the pixel id goes in the pid query parameter.
const DefaultEndpoint = "https://bzr.openai.com/v1/events"

// SenderConfig configures the worker job. APIKey and PixelID come from Ads Manager (Conversions); APIKey is a secret.
type SenderConfig struct {
	APIKey, PixelID string
	AppBaseURL      string // source_url is this plus the page path, as OpenAI requires for web events
	Endpoint        string
	Interval        time.Duration
	Batch           int // OpenAI accepts up to 1,000 per request
	MaxAttempts     int
	Retention       time.Duration // rows are deleted this long after they were recorded
	Client          *http.Client
}

func (c SenderConfig) withDefaults() SenderConfig {
	if c.Endpoint == "" {
		c.Endpoint = DefaultEndpoint
	}
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.Batch <= 0 || c.Batch > 1000 {
		c.Batch = 100
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 10
	}
	if c.Retention <= 0 {
		c.Retention = 30 * 24 * time.Hour
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 15 * time.Second}
	}
	c.AppBaseURL = strings.TrimRight(c.AppBaseURL, "/")
	return c
}

// Sender delivers recorded events to OpenAI. Several workers can run it at once: each claims different rows.
type Sender struct {
	pool *pgxpool.Pool
	cfg  SenderConfig
	m    *metrics.Metrics
	log  *slog.Logger
	rep  errtrack.Reporter
}

func NewSender(pool *pgxpool.Pool, cfg SenderConfig, m *metrics.Metrics, log *slog.Logger) *Sender {
	return &Sender{pool: pool, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends errors that need a human (a rejected key, events OpenAI refused) to the error tracker.
func (s *Sender) WithReporter(r errtrack.Reporter) *Sender {
	s.rep = r
	return s
}

// Stats is what one pass did.
type Stats struct{ Sent, Rejected, Retried, Expired, Purged int }

// Run sends due events until ctx ends, straight away while a full batch was due, otherwise every Interval.
func (s *Sender) Run(ctx context.Context) {
	for {
		st, err := s.RunOnce(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			s.log.Warn("ad conversion pass failed", "error", err)
		case st.Sent > 0 || st.Rejected > 0 || st.Expired > 0:
			s.log.Info("ad conversions sent", "sent", st.Sent, "rejected", st.Rejected, "retried", st.Retried, "expired", st.Expired)
		}
		wait := s.cfg.Interval
		if err == nil && st.Sent+st.Rejected >= s.cfg.Batch {
			wait = 0
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

type row struct {
	id       uuid.UUID
	typ      string
	eventID  string
	userID   *uuid.UUID
	at       time.Time
	path     string
	data     json.RawMessage
	oppref   *string
	obref    *string
	attempts int
}

// RunOnce expires events OpenAI would no longer accept, deletes old rows, and sends one batch of due events.
func (s *Sender) RunOnce(ctx context.Context) (Stats, error) {
	var st Stats
	// OpenAI refuses events older than 7 days; stop an hour short so a slow request is not refused.
	tag, err := s.pool.Exec(ctx, `UPDATE ad_conversion_events SET status = 'expired', last_error = 'older than OpenAI accepts'
		WHERE status = 'pending' AND occurred_at < now() - make_interval(secs => $1)`, (MaxAge - time.Hour).Seconds())
	if err != nil {
		return st, fmt.Errorf("expire: %w", err)
	}
	st.Expired = int(tag.RowsAffected())
	s.count("expired", st.Expired)
	if tag, err = s.pool.Exec(ctx, `DELETE FROM ad_conversion_events WHERE created_at < now() - make_interval(secs => $1)`, s.cfg.Retention.Seconds()); err != nil {
		return st, fmt.Errorf("purge: %w", err)
	}
	st.Purged = int(tag.RowsAffected())

	rows, err := s.claim(ctx)
	if err != nil || len(rows) == 0 {
		return st, err
	}
	status, msg, err := s.post(ctx, rows)
	switch {
	case err == nil && status/100 == 2:
		st.Sent = len(rows)
		return st, s.markSent(ctx, rows)
	case err == nil && (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity):
		// One bad event fails the whole request: send them one at a time so the others still count and the bad one is set aside.
		if len(rows) > 1 {
			for _, r := range rows {
				one, err := s.sendOne(ctx, r)
				if err != nil {
					return st, err
				}
				st.Sent += one.Sent
				st.Rejected += one.Rejected
				st.Retried += one.Retried
			}
			return st, nil
		}
		st.Rejected = 1
		return st, s.reject(ctx, rows[0], msg)
	default:
		if err != nil {
			msg = err.Error()
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			s.rep.Capture(ctx, errors.New("OpenAI Ads rejected the Conversions API key"), map[string]string{"component": "adconversions", "status": fmt.Sprint(status)})
		}
		st.Retried = len(rows)
		return st, s.retry(ctx, rows, msg)
	}
}

func (s *Sender) sendOne(ctx context.Context, r row) (Stats, error) {
	status, msg, err := s.post(ctx, []row{r})
	switch {
	case err == nil && status/100 == 2:
		return Stats{Sent: 1}, s.markSent(ctx, []row{r})
	case err == nil && (status == http.StatusBadRequest || status == http.StatusUnprocessableEntity):
		return Stats{Rejected: 1}, s.reject(ctx, r, msg)
	default:
		if err != nil {
			msg = err.Error()
		}
		return Stats{Retried: 1}, s.retry(ctx, []row{r}, msg)
	}
}

func (s *Sender) count(result string, n int) {
	if s.m != nil && n > 0 {
		s.m.AdConversions.WithLabelValues(result).Add(float64(n))
	}
}

// claim leases a batch of due events (oldest first) for a few minutes, so another worker does not send them too.
func (s *Sender) claim(ctx context.Context) ([]row, error) {
	rs, err := s.pool.Query(ctx, `UPDATE ad_conversion_events SET attempt_count = attempt_count + 1, next_attempt_at = now() + interval '5 minutes'
		WHERE id IN (SELECT id FROM ad_conversion_events WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY occurred_at FOR UPDATE SKIP LOCKED LIMIT $1)
		RETURNING id, event_type, event_id, user_id, occurred_at, source_path, data, oppref, obref, attempt_count`, s.cfg.Batch)
	if err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.id, &r.typ, &r.eventID, &r.userID, &r.at, &r.path, &r.data, &r.oppref, &r.obref, &r.attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rs.Err()
}

type wireUser struct {
	ExternalIDs []string `json:"external_ids_sha256,omitempty"`
	Obref       string   `json:"obref,omitempty"`
}

type wireEvent struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	TimestampMS  int64           `json:"timestamp_ms"`
	Oppref       string          `json:"oppref,omitempty"`
	SourceURL    string          `json:"source_url"`
	ActionSource string          `json:"action_source"`
	User         *wireUser       `json:"user,omitempty"`
	Data         json.RawMessage `json:"data"`
}

// payload is the Conversions API request body for rows.
func (s *Sender) payload(rows []row) ([]byte, error) {
	events := make([]wireEvent, 0, len(rows))
	for _, r := range rows {
		e := wireEvent{ID: r.eventID, Type: r.typ, TimestampMS: r.at.UnixMilli(), SourceURL: s.cfg.AppBaseURL + r.path, ActionSource: "web", Data: r.data}
		if r.oppref != nil {
			e.Oppref = *r.oppref
		}
		var u wireUser
		if r.userID != nil {
			u.ExternalIDs = []string{externalID(*r.userID)}
		}
		if r.obref != nil {
			u.Obref = *r.obref
		}
		if u.ExternalIDs != nil || u.Obref != "" {
			e.User = &u
		}
		events = append(events, e)
	}
	return json.Marshal(map[string]any{"integration_source": "hawkeye", "events": events})
}

// post sends rows in one request. It returns the HTTP status and, for a failure, a short description from the response.
func (s *Sender) post(ctx context.Context, rows []row) (int, string, error) {
	body, err := s.payload(rows)
	if err != nil {
		return 0, "", err
	}
	u := s.cfg.Endpoint + "?pid=" + url.QueryEscape(s.cfg.PixelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("send to OpenAI: %w", err)
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := ""
	if resp.StatusCode/100 != 2 {
		msg = truncate(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet))))
	}
	return resp.StatusCode, msg, nil
}

func ids(rows []row) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.id
	}
	return out
}

func (s *Sender) markSent(ctx context.Context, rows []row) error {
	s.count("sent", len(rows))
	_, err := s.pool.Exec(context.WithoutCancel(ctx), `UPDATE ad_conversion_events SET status = 'sent', sent_at = now(), last_error = NULL WHERE id = ANY($1)`, ids(rows))
	return err
}

func (s *Sender) reject(ctx context.Context, r row, msg string) error {
	s.count("rejected", 1)
	s.log.Error("OpenAI refused an ad conversion event", "type", r.typ, "event_id", r.eventID, "error", msg)
	s.rep.Capture(ctx, errors.New("OpenAI refused an ad conversion event"), map[string]string{"component": "adconversions", "type": r.typ, "error": msg})
	_, err := s.pool.Exec(context.WithoutCancel(ctx), `UPDATE ad_conversion_events SET status = 'failed', last_error = $2 WHERE id = $1`, r.id, msg)
	return err
}

// retry schedules another attempt with a growing delay (1, 4, 9 ... minutes, at most 6 hours), or gives up after MaxAttempts.
func (s *Sender) retry(ctx context.Context, rows []row, msg string) error {
	ctx = context.WithoutCancel(ctx)
	for _, r := range rows {
		if r.attempts >= s.cfg.MaxAttempts {
			s.count("failed", 1)
			if _, err := s.pool.Exec(ctx, `UPDATE ad_conversion_events SET status = 'failed', last_error = $2 WHERE id = $1`, r.id, msg); err != nil {
				return err
			}
			continue
		}
		s.count("retry", 1)
		delay := time.Duration(r.attempts*r.attempts) * time.Minute
		if delay > 6*time.Hour {
			delay = 6 * time.Hour
		}
		if _, err := s.pool.Exec(ctx, `UPDATE ad_conversion_events SET last_error = $2, next_attempt_at = now() + make_interval(secs => $3) WHERE id = $1`,
			r.id, msg, delay.Seconds()); err != nil {
			return err
		}
	}
	return nil
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}
