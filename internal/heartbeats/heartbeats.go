// Package heartbeats receives the pings of heartbeat (cron job) monitors and notices the ones that stop arriving.
//
// A heartbeat monitor has a secret ping address, /ping/<token>. Each ping is recorded as a successful check result (or a failed one when the job
// reports a failure), and a deadline that passes without a ping is recorded as a failed one (sweeper.go). Everything after that is shared with website
// monitors: incidents, alert routing, alert delay and quiet hours, maintenance windows, uptime, status pages, badges and reports.
package heartbeats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/plans"
)

// ErrNotFound means no live heartbeat monitor has that ping token (or, for Pings, that the monitor is not a heartbeat of the organization).
var ErrNotFound = errors.New("heartbeat not found")

const (
	// KeptPings is how many recent pings are kept per monitor for the ping log.
	KeptPings = 100
	// Error categories of the failed results heartbeats record.
	CatMissed    = "missed_ping"
	CatJobFailed = "job_failed"

	maxBody      = 1000
	maxUserAgent = 200
)

// Thresholds confirm a heartbeat's state on the first result: a missed deadline already includes the grace time, and a job that reports its own
// failure or success is not guessing.
var Thresholds = checks.Thresholds{Failure: 1, Recovery: 1}

// Ping is one request to a ping address.
type Ping struct {
	Fail       bool // the job reported a failure (/fail, or a non-zero exit status)
	ExitStatus *int // the exit status the job reported (/ping/<token>/<status>), if any
	Method     string
	SourceIP   string
	UserAgent  string
	Body       string // the start of the request body; only the first 1000 characters are kept
}

// Service records pings. store must be a checks.Store built with Thresholds.
type Service struct {
	pool  *pgxpool.Pool
	store *checks.Store
}

func NewService(pool *pgxpool.Pool, store *checks.Store) *Service {
	return &Service{pool: pool, store: store}
}

// Ping records a ping for the monitor with this token. A paused monitor's ping is logged (so the customer can see the job reaching HawkEye) but
// changes nothing. It returns whether the ping counted.
func (s *Service) Ping(ctx context.Context, token string, p Ping) (counted bool, err error) {
	if !validToken(token) {
		return false, ErrNotFound
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var id, orgID uuid.UUID
		var enabled bool
		var period, grace int
		var now time.Time
		err := tx.QueryRow(ctx, `SELECT id, organization_id, enabled, check_interval_seconds, heartbeat_grace_seconds, now() FROM monitors
			WHERE ping_token = $1 AND kind = 'heartbeat' AND deleted_at IS NULL FOR UPDATE`, token).Scan(&id, &orgID, &enabled, &period, &grace, &now)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		kind := "success"
		if p.Fail {
			kind = "fail"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO heartbeat_pings (monitor_id, received_at, kind, exit_status, method, source_ip, user_agent, body, ignored)
			VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9)`,
			id, now, kind, p.ExitStatus, truncate(p.Method, 10), truncate(p.SourceIP, 64), truncate(clean(p.UserAgent), maxUserAgent),
			truncate(strings.ToValidUTF8(strings.ReplaceAll(p.Body, "\x00", ""), "�"), maxBody), !enabled); err != nil {
			return fmt.Errorf("log ping: %w", err)
		}
		// Keep the most recent pings only.
		if _, err := tx.Exec(ctx, `DELETE FROM heartbeat_pings WHERE monitor_id = $1 AND (received_at, id) < (
				SELECT received_at, id FROM heartbeat_pings WHERE monitor_id = $1 ORDER BY received_at DESC, id DESC OFFSET $2 LIMIT 1)`,
			id, KeptPings-1); err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		plan, err := plans.EffectivePlan(ctx, tx, orgID)
		if err != nil {
			return err
		}
		// The next ping is due a period after this one; the plan's minimum interval has the last word, as for website checks.
		deadline := now.Add(time.Duration(max(period, plan.MinimumCheckIntervalSeconds)+grace) * time.Second)
		if _, err := tx.Exec(ctx, `UPDATE monitors SET last_ping_at = $2, ping_deadline = $3 WHERE id = $1`, id, now, deadline); err != nil {
			return err
		}
		r := checks.Result{CheckedAt: now, FinishedAt: now, Success: !p.Fail}
		if p.Fail {
			r.ErrorCategory, r.ErrorSummary = CatJobFailed, "The job reported a failure"
			if p.ExitStatus != nil {
				r.ErrorSummary = fmt.Sprintf("The job exited with status %d", *p.ExitStatus)
			}
		}
		if _, err := s.store.RecordTx(ctx, tx, id, r); err != nil {
			return err
		}
		counted = true
		return nil
	})
	return counted, err
}

// LoggedPing is one entry of a monitor's ping log.
type LoggedPing struct {
	ID         int64     `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	Kind       string    `json:"kind"` // success or fail
	ExitStatus *int      `json:"exit_status"`
	Method     string    `json:"method"`
	SourceIP   string    `json:"source_ip"`
	UserAgent  *string   `json:"user_agent"`
	Body       *string   `json:"body"`
	Ignored    bool      `json:"ignored"` // received while the monitor was paused
}

// Pings returns a heartbeat monitor's most recent pings, newest first.
func (s *Service) Pings(ctx context.Context, orgID, monitorID uuid.UUID, limit int) ([]LoggedPing, error) {
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM monitors WHERE id = $1 AND organization_id = $2 AND kind = 'heartbeat' AND deleted_at IS NULL`,
		monitorID, orgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, received_at, kind, exit_status, method, source_ip, user_agent, body, ignored FROM heartbeat_pings
		WHERE monitor_id = $1 ORDER BY received_at DESC, id DESC LIMIT $2`, monitorID, min(max(limit, 1), KeptPings))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoggedPing{}
	for rows.Next() {
		var p LoggedPing
		if err := rows.Scan(&p.ID, &p.ReceivedAt, &p.Kind, &p.ExitStatus, &p.Method, &p.SourceIP, &p.UserAgent, &p.Body, &p.Ignored); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// validToken reports whether s can be a ping token (22 URL-safe base64 characters), so other requests never reach the database.
func validToken(s string) bool {
	if len(s) != 22 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// clean removes control characters from a header value.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, ""))
}

// truncate cuts s to at most n characters.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
