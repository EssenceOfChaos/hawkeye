package incidentanalysis

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Analysis is an incident's analysis as the app, the API and MCP show it.
type Analysis struct {
	// Status is pending or running (not written yet), done (the fields below are set), declined, failed, over_limit or off.
	Status      string     `json:"status"`
	Category    *string    `json:"category"`
	LikelyCause *string    `json:"likely_cause"`
	Evidence    []string   `json:"evidence"`
	NextStep    *string    `json:"next_step"`
	Confidence  *string    `json:"confidence"`
	Model       *string    `json:"model"`
	RequestedAt time.Time  `json:"requested_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

// Querier is satisfied by *pgxpool.Pool and pgx.Tx.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Load returns an incident's analysis, or nil when it has none. The caller has already checked that the incident is the organization's.
func Load(ctx context.Context, q Querier, incidentID uuid.UUID) (*Analysis, error) {
	var a Analysis
	err := q.QueryRow(ctx, `SELECT status, category, likely_cause, COALESCE(evidence, '[]'), next_step, confidence, model, requested_at, finished_at
		FROM incident_analyses WHERE incident_id = $1`, incidentID).
		Scan(&a.Status, &a.Category, &a.LikelyCause, &a.Evidence, &a.NextStep, &a.Confidence, &a.Model, &a.RequestedAt, &a.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

var (
	// ErrInProgress means an analysis of the incident is already waiting or running.
	ErrInProgress = errors.New("an analysis of this incident is already in progress")
	// ErrNoSnapshot means the incident has nothing to analyze: heartbeat and DNS incidents, and incidents from before snapshots existed.
	ErrNoSnapshot = errors.New("this incident has no failure snapshot to analyze")
)

// Request asks for a new analysis of an incident: the first one, or one replacing the last. It runs in the background (Job), like the automatic
// one, and counts against the allowance when it runs. The caller has checked the organization, the plan and the setting.
func Request(ctx context.Context, q Querier, orgID, incidentID uuid.UUID) error {
	var status *string
	var snapshot bool
	if err := q.QueryRow(ctx, `SELECT (SELECT status FROM incident_analyses WHERE incident_id = $1),
			EXISTS (SELECT 1 FROM incident_snapshots WHERE incident_id = $1)`, incidentID).Scan(&status, &snapshot); err != nil {
		return err
	}
	if !snapshot {
		return ErrNoSnapshot
	}
	if status != nil && (*status == "pending" || *status == "running") {
		return ErrInProgress
	}
	// The status condition makes this safe against a second request at the same moment: only one of them replaces a finished run.
	tag, err := q.Exec(ctx, `INSERT INTO incident_analyses (incident_id, organization_id) VALUES ($1, $2)
		ON CONFLICT (incident_id) DO UPDATE SET status = 'pending', attempts = 0, run_at = now(), locked_until = NULL, requested_at = now(),
			category = NULL, likely_cause = NULL, evidence = NULL, next_step = NULL, confidence = NULL, model = NULL,
			input_tokens = NULL, output_tokens = NULL, finished_at = NULL
		WHERE incident_analyses.status NOT IN ('pending', 'running')`, incidentID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInProgress
	}
	return nil
}

// UsedThisMonth is how many analyses the organization has used this calendar month (UTC).
func UsedThisMonth(ctx context.Context, q Querier, orgID uuid.UUID) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM ai_analysis_usage WHERE organization_id = $1 AND created_at >= `+monthStart, orgID).Scan(&n)
	return n, err
}
