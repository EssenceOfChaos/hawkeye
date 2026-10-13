// Package ratelimit provides abuse limits shared by every API process, using PostgreSQL as the counter store.
//
// Algorithm: fixed windows. A request increments the counter for (key, current window) with a single atomic upsert; it is allowed
// while the counter is within the limit. Fixed windows can allow up to twice the limit across a window boundary: acceptable for abuse
// protection, and far simpler and cheaper than sliding windows. Subjects (IP addresses, email addresses) are hashed before storage.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Limiter struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Limiter { return &Limiter{pool: pool} }

// Result describes one Allow decision.
type Result struct {
	Allowed    bool
	Hits       int
	Remaining  int
	RetryAfter time.Duration // until the window resets (meaningful when !Allowed)
}

// Key builds a storage key from a scope ("auth:login") and a subject. The subject is lower-cased and hashed.
func Key(scope, subject string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(subject))))
	return scope + "|" + hex.EncodeToString(sum[:12])
}

// Allow counts one hit against key and reports whether it is within limit for the current window.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	secs := int64(window / time.Second)
	if secs < 1 {
		secs = 1
	}
	var hits int
	var retry float64
	err := l.pool.QueryRow(ctx, `WITH w AS (SELECT to_timestamp(floor(extract(epoch FROM now()) / $2::float8) * $2::float8) AS start)
		INSERT INTO rate_limits (key, window_start, hits) SELECT $1, w.start, 1 FROM w
		ON CONFLICT (key, window_start) DO UPDATE SET hits = rate_limits.hits + 1
		RETURNING hits, GREATEST(0, EXTRACT(epoch FROM window_start + ($2::float8 * interval '1 second') - now()))::float8`, key, float64(secs)).Scan(&hits, &retry)
	if err != nil {
		return Result{}, err
	}
	r := Result{Allowed: hits <= limit, Hits: hits, Remaining: max(0, limit-hits), RetryAfter: time.Duration(retry * float64(time.Second))}
	return r, nil
}

// Purge deletes counters from windows that ended more than olderThan ago and returns how many rows went.
func (l *Limiter) Purge(ctx context.Context, olderThan time.Duration) (int, error) {
	tag, err := l.pool.Exec(ctx, `DELETE FROM rate_limits WHERE window_start < now() - $1::interval`, olderThan)
	return int(tag.RowsAffected()), err
}
