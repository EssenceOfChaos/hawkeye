// Package statusbeat tells HawkEye's public status page (status.hawkeye.observer, the Worker in status/) that the monitoring engine is running.
//
// Every minute it reads how overdue the oldest queued check in its region is, and posts that to the status page with a shared secret. The status
// page marks "Monitoring and alerts" down when the heartbeats stop for 3 minutes, or when they report checks more than 5 minutes overdue. A process
// that is alive but no longer gets checks done therefore still shows as down, which an outside check of a web address could not see.
//
// It runs only in the primary region's full worker (cmd/worker), and only when STATUS_HEARTBEAT_URL and STATUS_HEARTBEAT_TOKEN are set, so local
// development and self-hosted installs send nothing.
package statusbeat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Interval is how often a heartbeat is sent; the status page allows three missed in a row.
const Interval = time.Minute

type Beat struct {
	pool     *pgxpool.Pool
	url      string
	token    string
	region   string
	client   *http.Client
	log      *slog.Logger
	interval time.Duration
}

func New(pool *pgxpool.Pool, url, token, region string, log *slog.Logger) *Beat {
	return &Beat{pool: pool, url: url, token: token, region: region, client: &http.Client{Timeout: 10 * time.Second}, log: log, interval: Interval}
}

// WithInterval changes how often heartbeats are sent (tests).
func (b *Beat) WithInterval(d time.Duration) *Beat {
	b.interval = d
	return b
}

// Run sends a heartbeat at once and then every interval until ctx ends. Failures are logged and never stop it.
func (b *Beat) Run(ctx context.Context) {
	t := time.NewTicker(b.interval)
	defer t.Stop()
	for {
		if err := b.Send(ctx); err != nil && ctx.Err() == nil {
			b.log.WarnContext(ctx, "status page heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Send posts one heartbeat. If the database can't be read the heartbeat is skipped: the status page should see the engine as down then.
func (b *Beat) Send(ctx context.Context) error {
	var overdue float64
	if err := b.pool.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM now() - min(run_at)), 0)::float8
		FROM check_jobs WHERE status = 'queued' AND region = $1 AND run_at <= now()`, b.region).Scan(&overdue); err != nil {
		return fmt.Errorf("read the queue: %w", err)
	}
	body, _ := json.Marshal(map[string]any{"region": "primary", "overdue_seconds": int(overdue)})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.token)
	req.Header.Set("User-Agent", "HawkEye-Worker")
	res, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status page answered %d", res.StatusCode)
	}
	return nil
}
