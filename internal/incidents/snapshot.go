package incidents

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/checks"
	"hawkeye/internal/secrets"
)

// ExcerptRetention is how long a failure snapshot's body excerpt is kept after its incident resolves; the rest of the snapshot stays with the
// incident (internal/retention clears it).
const ExcerptRetention = 30 * 24 * time.Hour

// ExcerptContext binds a sealed excerpt to its incident, so it can't be opened as another incident's (or as any other sealed value).
func ExcerptContext(incidentID uuid.UUID) string { return "incident_snapshot:" + incidentID.String() }

// Snapshot is an incident's failure snapshot as the app and the API show it. The body excerpt is never part of it: it is fetched on its own,
// by members, in the app (Excerpt).
type Snapshot struct {
	CheckedAt  time.Time              `json:"checked_at"`
	Region     string                 `json:"region"`
	Facts      checks.ResponseFacts   `json:"facts"`
	StepIndex  *int                   `json:"step_index"`
	StepName   *string                `json:"step_name"`
	Changes    []checks.Change        `json:"changes"`
	Locations  []checks.LocationState `json:"locations"`
	HasExcerpt bool                   `json:"has_body_excerpt"`
	// ExcerptCleared is when retention removed the excerpt (ExcerptRetention after the incident resolved).
	ExcerptCleared *time.Time `json:"body_excerpt_cleared_at"`
}

// saveSnapshot stores what the confirming check saw, in the transaction that opens the incident. Checks that saw no connection at all
// (heartbeats, DNS monitors, a refused destination) have no snapshot.
func saveSnapshot(ctx context.Context, tx pgx.Tx, box *secrets.Box, incidentID, monitorID uuid.UUID, o checks.Outage) error {
	r := o.Result
	if r.Facts == nil {
		return nil
	}
	var raw []byte
	var capture bool
	if err := tx.QueryRow(ctx, `SELECT last_good, capture_failure_body FROM monitors WHERE id = $1`, monitorID).Scan(&raw, &capture); err != nil {
		return err
	}
	var usual *checks.Timings
	if len(r.Steps) == 0 && r.Facts.Timings != nil { // a workflow's steps hit different hosts: no monitor-wide baseline
		bl, err := usualTimings(ctx, tx, monitorID, r.FinishedAt)
		if err != nil {
			return err
		}
		if bl != nil {
			usual = &checks.Timings{DNSMs: bl.DNSMs, ConnectMs: bl.ConnectMs, TLSMs: bl.TLSMs, WaitMs: bl.WaitMs}
		}
	}
	changes := checks.Compare(checks.LastGoodFor(raw, r.FailedStep), *r.Facts, usual)
	if changes == nil {
		changes = []checks.Change{}
	}
	var stepIndex *int
	var stepName *string
	if r.FailedStep >= 0 && r.FailedStep < len(r.Steps) {
		i, n := r.FailedStep, r.Steps[r.FailedStep].Name
		stepIndex, stepName = &i, &n
	}
	var sealed *string
	truncated, binary := false, false
	// The monitor's setting is checked again here: it may have been turned off while the check ran.
	if e := r.Excerpt; e != nil && capture {
		truncated, binary = e.Truncated, e.Binary
		if !e.Binary && e.Text != "" && box != nil {
			s, err := box.Seal(e.Text, ExcerptContext(incidentID))
			if err != nil && !errors.Is(err, secrets.ErrNoKey) {
				return err
			}
			if err == nil {
				sealed = &s
			}
		}
	}
	var locations any
	if len(o.Locations) > 0 {
		locations = o.Locations
	}
	_, err := tx.Exec(ctx, `INSERT INTO incident_snapshots (incident_id, checked_at, region, facts, step_index, step_name, changes, locations,
			body_excerpt, body_truncated, body_binary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		incidentID, r.CheckedAt, o.Region, r.Facts, stepIndex, stepName, changes, locations, sealed, truncated, binary)
	return err
}

// LoadSnapshot returns an incident's snapshot, or nil when it has none. The caller has already checked that the incident is the organization's.
func LoadSnapshot(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, incidentID uuid.UUID) (*Snapshot, error) {
	var s Snapshot
	err := q.QueryRow(ctx, `SELECT checked_at, region, facts, step_index, step_name, changes, COALESCE(locations, '[]'), body_excerpt IS NOT NULL,
			body_cleared_at
		FROM incident_snapshots WHERE incident_id = $1`, incidentID).
		Scan(&s.CheckedAt, &s.Region, &s.Facts, &s.StepIndex, &s.StepName, &s.Changes, &s.Locations, &s.HasExcerpt, &s.ExcerptCleared)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Excerpt is a snapshot's body excerpt, opened.
type Excerpt struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// ErrNoExcerpt means the incident has no excerpt to show: the monitor didn't keep one, the response was binary, or retention cleared it.
var ErrNoExcerpt = errors.New("no response excerpt")

// LoadExcerpt opens an incident's body excerpt. The caller has already checked that the incident is the organization's.
func LoadExcerpt(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, box *secrets.Box, incidentID uuid.UUID) (Excerpt, error) {
	var sealed *string
	var e Excerpt
	err := q.QueryRow(ctx, `SELECT body_excerpt, body_truncated FROM incident_snapshots WHERE incident_id = $1`, incidentID).Scan(&sealed, &e.Truncated)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && sealed == nil {
		return Excerpt{}, ErrNoExcerpt
	}
	if err != nil {
		return Excerpt{}, err
	}
	if e.Text, err = box.Open(*sealed, ExcerptContext(incidentID)); err != nil {
		return Excerpt{}, err
	}
	return e, nil
}
