// Package queue is a durable, PostgreSQL-backed work queue for monitor checks.
//
// Claiming uses FOR UPDATE SKIP LOCKED so concurrent workers never receive the same job. A claimed job
// holds a lease; if the worker dies the lease expires and the job becomes claimable again. Jobs that keep
// failing are dropped after MaxAttempts (the scheduler enqueues the monitor again on its next interval).
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Job struct {
	ID        int64
	MonitorID uuid.UUID
	Attempts  int
	Reclaimed bool // the previous holder's lease expired
}

// Claim leases the oldest runnable job, or returns nil when there is none. dropped counts jobs
// discarded because they exhausted their attempts.
func Claim(ctx context.Context, db DB, lease time.Duration, maxAttempts int) (job *Job, dropped int, err error) {
	tag, err := db.Exec(ctx, `DELETE FROM check_jobs
		WHERE attempts >= $1 AND (status = 'queued' OR locked_until < now())`, maxAttempts)
	if err != nil {
		return nil, 0, err
	}
	dropped = int(tag.RowsAffected())

	var j Job
	err = db.QueryRow(ctx, `UPDATE check_jobs j SET
			status = 'running', locked_until = now() + $1::interval, attempts = j.attempts + 1
		WHERE j.id = (
			SELECT id FROM check_jobs
			WHERE (status = 'queued' AND run_at <= now()) OR (status = 'running' AND locked_until < now())
			ORDER BY run_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1)
		RETURNING j.id, j.monitor_id, j.attempts, (j.attempts > 1)`, lease).
		Scan(&j.ID, &j.MonitorID, &j.Attempts, &j.Reclaimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, dropped, nil
	}
	if err != nil {
		return nil, dropped, err
	}
	return &j, dropped, nil
}

// Complete removes a finished job.
func Complete(ctx context.Context, db DB, id int64) error {
	_, err := db.Exec(ctx, `DELETE FROM check_jobs WHERE id = $1`, id)
	return err
}

// Release returns a job to the queue to run again after delay (transient failure, or shutdown mid-job).
func Release(ctx context.Context, db DB, id int64, delay time.Duration) error {
	_, err := db.Exec(ctx, `UPDATE check_jobs SET status = 'queued', locked_until = NULL, run_at = now() + $2::interval
		WHERE id = $1`, id, delay)
	return err
}

// Stats is a snapshot for metrics and backpressure.
type Stats struct {
	Queued, Running int
	OldestRunnable  time.Duration // age of the oldest job that is waiting to run
}

func Snapshot(ctx context.Context, db DB) (Stats, error) {
	var s Stats
	var oldest float64
	err := db.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE status = 'queued'),
			count(*) FILTER (WHERE status = 'running'),
			COALESCE(EXTRACT(EPOCH FROM now() - min(run_at) FILTER (WHERE status = 'queued' AND run_at <= now())), 0)::float8
		FROM check_jobs`).Scan(&s.Queued, &s.Running, &oldest)
	s.OldestRunnable = time.Duration(oldest * float64(time.Second))
	return s, err
}
