// Package plans is the single place that decides what an organization may do.
// Handlers never compare against plan limits themselves; they call the Service.
package plans

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const FreeCode = "free"

type Plan struct {
	ID                          uuid.UUID `json:"-"`
	Code                        string    `json:"code"`
	Name                        string    `json:"name"`
	MonthlyPriceCents           int       `json:"monthly_price_cents"`
	AnnualPriceCents            *int      `json:"annual_price_cents"` // the price for a year paid at once; null when not sold yearly
	MaxMonitors                 int       `json:"max_monitors"`
	MinimumCheckIntervalSeconds int       `json:"minimum_check_interval_seconds"`
	HistoryRetentionDays        int       `json:"history_retention_days"`
	MaxTeamMembers              int       `json:"max_team_members"`
	ReportsEnabled              bool      `json:"reports_enabled"`
	BrandedReportsEnabled       bool      `json:"branded_reports_enabled"`
	MaintenanceEnabled          bool      `json:"maintenance_enabled"`
	MaxRegions                  int       `json:"max_regions"`
	MaxDomains                  int       `json:"max_domains"`
	MaxStatusPages              int       `json:"max_status_pages"`
	StatusPageBranding          bool      `json:"status_page_branding"`    // a logo, accent color and website link on status pages
	StatusPageWhiteLabel        bool      `json:"status_page_white_label"` // hide "Powered by HawkEye" on status pages
	APIWriteEnabled             bool      `json:"api_write_enabled"`       // API keys may change data (reading through the API is on every plan)
	MaxWorkflows                int       `json:"max_workflows"`           // multi-step API checks; each also counts as a monitor. 0: none
	MaxWorkflowSteps            int       `json:"max_workflow_steps"`      // steps per multi-step API check
	AIDraftsEnabled             bool      `json:"ai_drafts_enabled"`       // AI-drafted incident updates, once the owner turns them on
	Active                      bool      `json:"-"`
}

const planCols = `p.id, p.code, p.name, p.monthly_price_cents, p.annual_price_cents, p.max_monitors, p.minimum_check_interval_seconds,
	p.history_retention_days, p.max_team_members, p.reports_enabled, p.branded_reports_enabled, p.maintenance_enabled, p.max_regions, p.max_domains, p.max_status_pages,
	p.status_page_branding, p.status_page_white_label, p.api_write_enabled, p.active, p.max_workflows, p.max_workflow_steps, p.ai_drafts_enabled`

func scanPlan(row pgx.Row, extra ...any) (Plan, error) {
	var p Plan
	dest := append([]any{&p.ID, &p.Code, &p.Name, &p.MonthlyPriceCents, &p.AnnualPriceCents, &p.MaxMonitors, &p.MinimumCheckIntervalSeconds,
		&p.HistoryRetentionDays, &p.MaxTeamMembers, &p.ReportsEnabled, &p.BrandedReportsEnabled, &p.MaintenanceEnabled, &p.MaxRegions, &p.MaxDomains, &p.MaxStatusPages,
		&p.StatusPageBranding, &p.StatusPageWhiteLabel, &p.APIWriteEnabled, &p.Active, &p.MaxWorkflows, &p.MaxWorkflowSteps, &p.AIDraftsEnabled}, extra...)
	err := row.Scan(dest...)
	return p, err
}

// Subscription statuses.
const (
	StatusActive     = "active"
	StatusTrialing   = "trialing"
	StatusPastDue    = "past_due"
	StatusCanceled   = "canceled"
	StatusIncomplete = "incomplete"
)

// GrantsPaidAccess maps a subscription status to entitlements:
//
//	active, trialing  -> the subscribed plan
//	past_due          -> the subscribed plan (grace period while the provider retries payment)
//	canceled          -> free plan (existing data is preserved, new usage is limited)
//	incomplete        -> free plan (payment never completed; Checkout success alone grants nothing)
//
// Unknown statuses fail closed to the free plan.
func GrantsPaidAccess(status string) bool {
	switch status {
	case StatusActive, StatusTrialing, StatusPastDue:
		return true
	}
	return false
}

// DB is satisfied by *pgxpool.Pool and pgx.Tx, so checks can run inside a caller's transaction.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Listing returns all active plans, cheapest first (public pricing).
func List(ctx context.Context, db DB) ([]Plan, error) {
	rows, err := db.Query(ctx, `SELECT `+planCols+` FROM plans p WHERE p.active ORDER BY p.monthly_price_cents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func byCode(ctx context.Context, db DB, code string) (Plan, error) {
	p, err := scanPlan(db.QueryRow(ctx, `SELECT `+planCols+` FROM plans p WHERE p.code = $1`, code))
	if err != nil {
		return Plan{}, fmt.Errorf("load plan %q: %w", code, err)
	}
	return p, nil
}

// EffectivePlan returns the plan whose entitlements currently apply to the organization.
func EffectivePlan(ctx context.Context, db DB, orgID uuid.UUID) (Plan, error) {
	var status string
	p, err := scanPlan(db.QueryRow(ctx, `SELECT `+planCols+`, s.status
		FROM subscriptions s JOIN plans p ON p.id = s.plan_id WHERE s.organization_id = $1`, orgID), &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return byCode(ctx, db, FreeCode)
	}
	if err != nil {
		return Plan{}, err
	}
	if !GrantsPaidAccess(status) {
		return byCode(ctx, db, FreeCode)
	}
	return p, nil
}

// nextPlan is the cheapest active plan that costs more than cur, or nil.
func nextPlan(ctx context.Context, db DB, cur Plan) (*Plan, error) {
	p, err := scanPlan(db.QueryRow(ctx, `SELECT `+planCols+` FROM plans p
		WHERE p.active AND p.monthly_price_cents > $1 ORDER BY p.monthly_price_cents LIMIT 1`, cur.MonthlyPriceCents))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}
