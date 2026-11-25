package incidents

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/notifications"
)

// Flap suppression. A monitor that keeps going down and coming back is "flapping": instead of an outage and a recovery alert every few minutes, its
// team gets one "flapping" alert, then silence while it lasts, then one message when it settles. A flapping monitor that goes down and stays down
// is never kept quiet: once the outage has lasted FlapStayedDownMin (or longer for slow intervals), it is alerted as an ordinary outage.
//
// Every incident is still recorded, and uptime is unchanged: only the alerts are held back. The monitor's flap_suppression setting (on by default)
// turns this off; an episode in progress then ends, and an outage still open is alerted at once.
const (
	FlapOutages = 3 // outages within FlapWindow that make a monitor flapping (counting the one just confirmed)
	FlapWindow  = time.Hour
	FlapSettle  = notifications.FlapSettle // up this long, with no new outage, and the episode ends ("settled")
	// FlapStayedDownMin is the shortest an outage of a flapping monitor lasts before it is alerted anyway. It is at least FlapStayedDownChecks of the
	// monitor's intervals, so a monitor checked every 5 minutes (whose every outage lasts 10 minutes or so) isn't alerted on every outage again.
	FlapStayedDownMin    = 15 * time.Minute
	FlapStayedDownChecks = 4
)

// Episode statuses.
const (
	FlapOpen       = "open"
	FlapSettled    = "settled"     // stayed up for FlapSettle
	FlapStayedDown = "stayed_down" // an outage lasted long enough to be alerted as an ordinary one
	FlapEnded      = "ended"       // flap suppression was turned off, or the monitor paused
)

// flapOutage runs when an outage has just been confirmed (incident incidentID, already inserted). It reports whether the monitor is flapping, in
// which case the incident's own alerts are paused and the caller sends no outage alert: the outage joins the open episode, or it is the one that
// starts an episode, whose "flapping" alert then goes out instead (outside quiet hours; the alert delay doesn't apply, as the pattern is already
// confirmed by several outages).
func flapOutage(ctx context.Context, tx pgx.Tx, orgID, monitorID, incidentID uuid.UUID, timing notifications.Timing) (bool, error) {
	var on bool
	var episode *uuid.UUID
	var recent int
	err := tx.QueryRow(ctx, `SELECT m.flap_suppression,
			(SELECT id FROM flap_episodes WHERE monitor_id = m.id AND status = 'open'),
			(SELECT count(*) FROM incidents WHERE monitor_id = m.id AND started_at > now() - make_interval(secs => $2))
		FROM monitors m WHERE m.id = $1`, monitorID, FlapWindow.Seconds()).Scan(&on, &episode, &recent)
	if err != nil || !on {
		return false, err
	}
	if episode != nil {
		if _, err := tx.Exec(ctx, `UPDATE flap_episodes SET outages = outages + 1 WHERE id = $1`, *episode); err != nil {
			return false, err
		}
		_, err := tx.Exec(ctx, `UPDATE incidents SET flap_episode_id = $2, alerts_paused = true WHERE id = $1`, incidentID, *episode)
		return true, err
	}
	if recent < FlapOutages {
		return false, nil
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO flap_episodes (monitor_id, started_at, outages) VALUES ($1, now(), $2)
		ON CONFLICT (monitor_id) WHERE status = 'open' DO NOTHING RETURNING id`, monitorID, recent).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // cannot happen while the monitor row is locked; alert the outage normally rather than lose it
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE incidents SET flap_episode_id = $2, alerts_paused = true WHERE id = $1`, incidentID, id); err != nil {
		return false, err
	}
	_, err = notifications.EnqueueFlap(ctx, tx, orgID, monitorID, id, notifications.EventFlapping, timing.At(time.Now()))
	return true, err
}

// stayedDownAfter is how long an outage of a flapping monitor checked every interval lasts before it is alerted anyway.
func stayedDownAfter(interval time.Duration) time.Duration {
	return max(FlapStayedDownMin, FlapStayedDownChecks*interval)
}

// FlapJob ends flap episodes: it alerts an outage that has lasted (stayed_down), says so once a monitor has been up for FlapSettle (settled), and
// ends episodes whose monitor no longer suppresses flapping or is paused. It decides nothing during a maintenance window. Several instances can run:
// each episode is handled under its monitor's row lock, the same lock that recording a check result takes.
type FlapJob struct {
	pool     *pgxpool.Pool
	interval time.Duration
	log      *slog.Logger
	rep      errtrack.Reporter
}

func NewFlapJob(pool *pgxpool.Pool, interval time.Duration, log *slog.Logger) *FlapJob {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &FlapJob{pool: pool, interval: interval, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends failures to an error tracker.
func (j *FlapJob) WithReporter(r errtrack.Reporter) *FlapJob {
	j.rep = r
	return j
}

// Run sweeps every interval until ctx is canceled.
func (j *FlapJob) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if _, err := j.Sweep(ctx); err != nil && ctx.Err() == nil {
			j.log.Error("flap sweep failed", "error", err)
			j.rep.Capture(ctx, err, map[string]string{"component": "flap"})
		}
		select {
		case <-ctx.Done():
		case <-time.After(j.interval + time.Duration(rand.Float64()*float64(j.interval)/4)):
		}
	}
}

// Sweep looks at every open episode once and reports how many it closed.
func (j *FlapJob) Sweep(ctx context.Context) (int, error) {
	rows, err := j.pool.Query(ctx, `SELECT id, monitor_id FROM flap_episodes WHERE status = 'open' ORDER BY started_at LIMIT 500`)
	if err != nil {
		return 0, err
	}
	type open struct{ id, monitor uuid.UUID }
	var list []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.monitor); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	closed := 0
	for _, o := range list {
		var done bool
		err := pgx.BeginFunc(ctx, j.pool, func(tx pgx.Tx) (err error) {
			done, err = closeEpisode(ctx, tx, o.id, o.monitor, time.Now())
			return err
		})
		if err != nil {
			return closed, err
		}
		if done {
			closed++
		}
	}
	return closed, nil
}

// closeEpisode decides whether one open episode ends now, and ends it. It locks the monitor first, as recording a check result does, so an
// outage can't join the episode while it is being closed.
func closeEpisode(ctx context.Context, tx pgx.Tx, episodeID, monitorID uuid.UUID, now time.Time) (bool, error) {
	var orgID uuid.UUID
	var active, on, maintenance bool
	var intervalSec int
	err := tx.QueryRow(ctx, `SELECT organization_id, enabled AND deleted_at IS NULL, flap_suppression, in_maintenance(organization_id, id, $2),
			check_interval_seconds
		FROM monitors WHERE id = $1 FOR NO KEY UPDATE`, monitorID, now).Scan(&orgID, &active, &on, &maintenance, &intervalSec)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // deleted: the episode goes with it
	}
	if err != nil {
		return false, err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM flap_episodes WHERE id = $1 FOR UPDATE`, episodeID).Scan(&status); err != nil || status != FlapOpen {
		return false, err
	}
	// The monitor's open incident, if any, and when its last outage ended.
	var openID *uuid.UUID
	var openPaused bool
	var openSince, lastResolved *time.Time
	if err := tx.QueryRow(ctx, `SELECT
			(SELECT id FROM incidents WHERE monitor_id = $1 AND status = 'open'),
			COALESCE((SELECT alerts_paused FROM incidents WHERE monitor_id = $1 AND status = 'open'), false),
			(SELECT started_at FROM incidents WHERE monitor_id = $1 AND status = 'open'),
			(SELECT max(resolved_at) FROM incidents WHERE monitor_id = $1)`, monitorID).Scan(&openID, &openPaused, &openSince, &lastResolved); err != nil {
		return false, err
	}

	var end string
	switch {
	case !active || !on:
		end = FlapEnded
	case maintenance:
		return false, nil // a maintenance window raises no alerts and changes no state; decide when it ends
	case openID != nil && openPaused && now.Sub(*openSince) >= stayedDownAfter(time.Duration(intervalSec)*time.Second):
		end = FlapStayedDown
	case openID == nil && lastResolved != nil && now.Sub(*lastResolved) >= FlapSettle:
		end = FlapSettled
	default:
		return false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE flap_episodes SET status = $2, ended_at = GREATEST(started_at, $3) WHERE id = $1`, episodeID, end, now); err != nil {
		return false, err
	}
	timing, err := notifications.MonitorTiming(ctx, tx, monitorID)
	if err != nil {
		return false, err
	}
	// Still down with its alerts paused (it stayed down, or flap suppression was turned off mid-outage): alert it now as an ordinary outage, so a
	// real outage is never kept quiet. A paused monitor isn't alerted: it is no longer checked. The recovery is then alerted as usual.
	if openID != nil && openPaused && active {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET alerts_paused = false WHERE id = $1`, *openID); err != nil {
			return false, err
		}
		_, err = notifications.Enqueue(ctx, tx, orgID, monitorID, *openID, notifications.EventOutage, timing.At(now))
		return true, err
	}
	_, err = notifications.EnqueueFlap(ctx, tx, orgID, monitorID, episodeID, notifications.EventFlappingEnded, timing.At(now))
	return true, err
}
