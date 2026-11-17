package checks_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/heartbeats"
	"hawkeye/internal/incidents"
	"hawkeye/internal/testutil"
)

// Every failure category the code can produce must be one check_results accepts. A category the database refuses can't be recorded: the worker
// retries and then drops the job, so the failure never opens an incident or alerts anyone (this happened to workflow_config until migration 000046).
// Add new categories here.
func TestEveryFailureCategoryCanBeRecorded(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := t.Context()
	var org, monitor uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1, 'm', 'https://example.com', 300)
		RETURNING id`, org).Scan(&monitor); err != nil {
		t.Fatal(err)
	}
	store := checks.NewStore(pool, checks.Thresholds{Failure: 2, Recovery: 2}, incidents.Engine{})
	at := time.Now().Add(-time.Hour)
	for _, cat := range []string{
		checks.CatDNS, checks.CatConnect, checks.CatTLS, checks.CatTimeout, checks.CatRedirect, checks.CatBlocked, checks.CatHTTPStatus,
		checks.CatAssertion, checks.CatOther, checks.CatWorkflowConfig, heartbeats.CatMissed, heartbeats.CatJobFailed,
	} {
		at = at.Add(time.Second)
		if _, err := store.Record(ctx, monitor, checks.Result{CheckedAt: at, FinishedAt: at, ErrorCategory: cat, ErrorSummary: "x"}); err != nil {
			t.Errorf("a %q failure can't be recorded: %v", cat, err)
		}
	}
}
