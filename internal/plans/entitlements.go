package plans

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Limit kinds reported in LimitError.
const (
	LimitMonitors      = "monitors"
	LimitCheckInterval = "check_interval"
	LimitTeamMembers   = "team_members"
	LimitMaintenance   = "maintenance_windows"
	LimitRegions       = "regions"
	LimitDomains       = "domains"
)

// LimitError is returned when an operation exceeds the organization's plan.
type LimitError struct {
	Kind       string
	Limit      int
	Used       int
	Plan       Plan
	Next       *Plan
	UpgradeURL string
}

func (e *LimitError) Error() string { return "plan limit reached: " + e.Kind }

// Message explains the limit and, after a downgrade, how to get back within it.
func (e *LimitError) Message() string {
	switch e.Kind {
	case LimitMonitors:
		if e.Used > e.Limit {
			return fmt.Sprintf("Your %s plan includes %d monitors but you have %d. Your existing monitors are untouched; "+
				"delete %d or upgrade to add more.", e.Plan.Name, e.Limit, e.Used, e.Used-e.Limit)
		}
		return fmt.Sprintf("Your %s plan includes up to %d monitors and all of them are in use.", e.Plan.Name, e.Limit)
	case LimitCheckInterval:
		return fmt.Sprintf("Your %s plan supports a check interval of at least %d seconds.", e.Plan.Name, e.Limit)
	case LimitTeamMembers:
		return fmt.Sprintf("Your %s plan includes up to %d team members.", e.Plan.Name, e.Limit)
	case LimitRegions:
		return fmt.Sprintf("Your %s plan can check each monitor from up to %d location%s.", e.Plan.Name, e.Limit, map[bool]string{true: "", false: "s"}[e.Limit == 1])
	case LimitDomains:
		return fmt.Sprintf("Your %s plan includes up to %d tracked domains and all of them are in use.", e.Plan.Name, e.Limit)
	case LimitMaintenance:
		return fmt.Sprintf("Maintenance windows are not included in your %s plan. Upgrade to silence alerts during planned work.", e.Plan.Name)
	}
	return "Your plan limit has been reached."
}

// Service answers entitlement questions. Every method takes the DB to use so callers can run
// the check inside the same transaction as the change it guards.
type Service struct {
	upgradeURL string
}

func NewService(appBaseURL string) *Service { return &Service{upgradeURL: appBaseURL + "/billing"} }

func (s *Service) limitError(ctx context.Context, db DB, kind string, limit, used int, p Plan) error {
	next, err := nextPlan(ctx, db, p)
	if err != nil {
		return err
	}
	return &LimitError{Kind: kind, Limit: limit, Used: used, Plan: p, Next: next, UpgradeURL: s.upgradeURL}
}

func (s *Service) Plan(ctx context.Context, db DB, orgID uuid.UUID) (Plan, error) {
	return EffectivePlan(ctx, db, orgID)
}

func (s *Service) GetMonitorLimit(ctx context.Context, db DB, orgID uuid.UUID) (int, error) {
	p, err := EffectivePlan(ctx, db, orgID)
	return p.MaxMonitors, err
}

func (s *Service) GetMinimumCheckInterval(ctx context.Context, db DB, orgID uuid.UUID) (int, error) {
	p, err := EffectivePlan(ctx, db, orgID)
	return p.MinimumCheckIntervalSeconds, err
}

func (s *Service) GetHistoryRetention(ctx context.Context, db DB, orgID uuid.UUID) (days int, err error) {
	p, err := EffectivePlan(ctx, db, orgID)
	return p.HistoryRetentionDays, err
}

func (s *Service) CanExportReports(ctx context.Context, db DB, orgID uuid.UUID) (bool, error) {
	p, err := EffectivePlan(ctx, db, orgID)
	return p.ReportsEnabled, err
}

// CanUseMaintenance returns a *LimitError when the plan does not include maintenance windows. It guards creating one;
// listing and deleting stay available after a downgrade.
func (s *Service) CanUseMaintenance(ctx context.Context, db DB, orgID uuid.UUID) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	if !p.MaintenanceEnabled {
		return s.limitError(ctx, db, LimitMaintenance, 0, 0, p)
	}
	return nil
}

// CheckRegions returns a *LimitError when a monitor asks for more regions than the plan allows.
func (s *Service) CheckRegions(ctx context.Context, db DB, orgID uuid.UUID, count int) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	if count > p.MaxRegions {
		return s.limitError(ctx, db, LimitRegions, p.MaxRegions, count, p)
	}
	return nil
}

// CanAddDomain returns a *LimitError when the organization already tracks as many domains as its plan includes. It guards adding a domain by hand
// only: domains that come from monitors are always recorded, and those beyond the limit are simply not looked up until the plan allows them.
// To be safe under concurrency, call it in a transaction that holds the organization row lock.
func (s *Service) CanAddDomain(ctx context.Context, db DB, orgID uuid.UUID) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	var used int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM domains WHERE organization_id = $1`, orgID).Scan(&used); err != nil {
		return err
	}
	if used >= p.MaxDomains {
		return s.limitError(ctx, db, LimitDomains, p.MaxDomains, used, p)
	}
	return nil
}

func countMonitors(ctx context.Context, db DB, orgID uuid.UUID) (n int, err error) {
	err = db.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL`, orgID).Scan(&n)
	return n, err
}

// CanCreateMonitor returns a *LimitError when the organization has no monitor capacity left.
// An organization above its limit (after a downgrade) keeps its monitors but cannot add more.
// To be safe under concurrency, call it in a transaction that holds the organization row lock.
func (s *Service) CanCreateMonitor(ctx context.Context, db DB, orgID uuid.UUID) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	used, err := countMonitors(ctx, db, orgID)
	if err != nil {
		return err
	}
	if used >= p.MaxMonitors {
		return s.limitError(ctx, db, LimitMonitors, p.MaxMonitors, used, p)
	}
	return nil
}

// CheckInterval returns a *LimitError when seconds is below the plan minimum.
func (s *Service) CheckInterval(ctx context.Context, db DB, orgID uuid.UUID, seconds int) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	if seconds < p.MinimumCheckIntervalSeconds {
		return s.limitError(ctx, db, LimitCheckInterval, p.MinimumCheckIntervalSeconds, seconds, p)
	}
	return nil
}

func (s *Service) CanAddTeamMember(ctx context.Context, db DB, orgID uuid.UUID) error {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return err
	}
	var used int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM organization_members WHERE organization_id = $1`, orgID).Scan(&used); err != nil {
		return err
	}
	if used >= p.MaxTeamMembers {
		return s.limitError(ctx, db, LimitTeamMembers, p.MaxTeamMembers, used, p)
	}
	return nil
}

// Usage summarises consumption against the plan for the UI.
type Usage struct {
	Plan         Plan  `json:"plan"`
	MonitorsUsed int   `json:"monitors_used"`
	MonitorLimit int   `json:"monitor_limit"`
	OverLimit    bool  `json:"over_limit"`
	NextPlan     *Plan `json:"next_plan"`
}

func (s *Service) Usage(ctx context.Context, db DB, orgID uuid.UUID) (Usage, error) {
	p, err := EffectivePlan(ctx, db, orgID)
	if err != nil {
		return Usage{}, err
	}
	used, err := countMonitors(ctx, db, orgID)
	if err != nil {
		return Usage{}, err
	}
	next, err := nextPlan(ctx, db, p)
	if err != nil {
		return Usage{}, err
	}
	return Usage{Plan: p, MonitorsUsed: used, MonitorLimit: p.MaxMonitors, OverLimit: used > p.MaxMonitors, NextPlan: next}, nil
}
