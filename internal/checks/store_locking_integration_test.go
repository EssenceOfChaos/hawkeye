package checks_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/testutil"
)

// waitForLockWait returns once some other backend is waiting on a lock, so the test can make the second move of a lock cycle at the right moment.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("nothing started waiting on a lock")
}

// Recording a check updates the monitor row more than once (status, then the certificate, then speed). PostgreSQL re-checks a row's foreign keys
// when the same transaction updates it a second time, so the second update takes a KEY SHARE lock on the monitor's organization. Saving a status
// page (and creating a maintenance window) does the opposite: it locks the organization, then inserts rows whose foreign key needs a KEY SHARE lock
// on the monitor. When recording held the monitor with FOR UPDATE, which blocks KEY SHARE, the two waited for each other and PostgreSQL killed one
// ("record certificate: deadlock detected", seen in production). Recording now holds FOR NO KEY UPDATE, which still serializes results for a
// monitor but lets other transactions' foreign key checks through.
func TestRecordingACheckDoesNotDeadlockWithAStatusPageSave(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := t.Context()
	var orgID, monitorID, pageID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('Acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'Shop', 'https://shop.example.com', 60)
		RETURNING id`, orgID).Scan(&monitorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO status_pages (organization_id, slug, title, published) VALUES ($1, 'acme', 'Acme', true) RETURNING id`, orgID).Scan(&pageID); err != nil {
		t.Fatal(err)
	}

	// The status page save starts first and takes the organization lock, as statuspage.Service.Update does.
	page, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer page.Rollback(context.Background())
	if _, err := page.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
		t.Fatal(err)
	}

	// The worker records an HTTPS result: it locks the monitor, updates it, and then waits for the organization at the certificate update.
	store := checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{})
	now := time.Now()
	expires := now.Add(90 * 24 * time.Hour)
	ms := 120
	recorded := make(chan error, 1)
	go func() {
		_, err := store.Record(context.Background(), monitorID, checks.Result{CheckedAt: now, FinishedAt: now, Success: true, StatusCode: ptr(200),
			ResponseTimeMs: &ms, CertExpiresAt: &expires})
		recorded <- err
	}()
	waitForLockWait(t, pool)

	// The status page save now adds the monitor to the page. Its foreign key check must not wait for the worker, or neither can finish.
	inserted := make(chan error, 1)
	go func() {
		_, err := page.Exec(ctx, `INSERT INTO status_page_monitors (page_id, monitor_id, position) VALUES ($1, $2, 0)`, pageID, monitorID)
		inserted <- err
	}()
	select {
	case err := <-inserted:
		if err != nil {
			t.Fatalf("status page save: %v", err)
		}
	case err := <-recorded:
		t.Fatalf("recording finished first, with %v: it should be waiting for the organization", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the status page save is stuck behind the worker")
	}
	if err := page.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-recorded:
		if err != nil {
			t.Fatalf("recording the check: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recording the check never finished")
	}
	var stored *time.Time
	if err := pool.QueryRow(ctx, `SELECT ssl_expires_at FROM monitors WHERE id = $1`, monitorID).Scan(&stored); err != nil || stored == nil {
		t.Fatalf("certificate not recorded: %v %v", stored, err)
	}
}

func ptr[T any](v T) *T { return &v }
