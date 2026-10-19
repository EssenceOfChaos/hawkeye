package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

var (
	ErrNotConfigured     = errors.New("billing is not configured")
	ErrAlreadySubscribed = errors.New("organization already has a paid subscription")
	ErrNoCustomer        = errors.New("no billing account exists yet")
	ErrNoSubscription    = errors.New("no active subscription")
	// ErrUnknownPrice: a subscription that would grant paid access uses a price we do not map to a plan.
	ErrUnknownPrice = errors.New("subscription price does not map to a plan")
	// ErrConflict: a second paid subscription arrived for an organization that already has one.
	ErrConflict = errors.New("organization already has a different paid subscription")
)

type Service struct {
	pool     *pgxpool.Pool
	provider Provider // nil when billing is not configured
	cfg      Config
	m        *metrics.Metrics
	log      *slog.Logger
	rep      errtrack.Reporter

	priceMu       sync.RWMutex
	priceProblems map[string]string // plan code -> why its Stripe price does not match the advertised one (see VerifyPrices)
}

func NewService(pool *pgxpool.Pool, provider Provider, cfg Config, m *metrics.Metrics, log *slog.Logger) *Service {
	return &Service{pool: pool, provider: provider, cfg: cfg.withDefaults(), m: m, log: log, rep: errtrack.Noop{}}
}

// WithReporter sends billing events that failed permanently (and so need a human) to an error tracker.
func (s *Service) WithReporter(r errtrack.Reporter) *Service {
	s.rep = r
	return s
}

// Enabled reports whether Checkout and the portal can be offered.
func (s *Service) Enabled() bool { return s.provider != nil && s.cfg.Enabled() }

// View is the billing summary for an organization.
type View struct {
	BillingEnabled    bool       `json:"billing_enabled"`
	Plan              plans.Plan `json:"plan"` // the plan whose limits apply right now
	Status            string     `json:"status"`
	PaidAccess        bool       `json:"paid_access"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end"`
	CanManage         bool       `json:"can_manage"` // a billing account exists, so the portal can be opened
	HasSubscription   bool       `json:"has_subscription"`
}

type row struct {
	planCode, status           string
	customerID, subscriptionID *string
	periodEnd                  *time.Time
	cancelAtPeriodEnd          bool
	lastEventAt                *time.Time
}

func loadRow(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orgID uuid.UUID, lock bool) (row, error) {
	var r row
	sql := `SELECT p.code, s.status, s.provider_customer_id, s.provider_subscription_id, s.current_period_end, s.cancel_at_period_end
		FROM subscriptions s JOIN plans p ON p.id = s.plan_id WHERE s.organization_id = $1`
	if lock {
		sql += ` FOR UPDATE OF s`
	}
	err := q.QueryRow(ctx, sql, orgID).Scan(&r.planCode, &r.status, &r.customerID, &r.subscriptionID, &r.periodEnd, &r.cancelAtPeriodEnd)
	return r, err
}

func (r row) hasPaidAccess() bool {
	return r.planCode != plans.FreeCode && plans.GrantsPaidAccess(r.status)
}

// Subscription returns the billing summary shown on the billing page.
func (s *Service) Subscription(ctx context.Context, orgID uuid.UUID) (View, error) {
	v := View{BillingEnabled: s.Enabled()}
	p, err := plans.EffectivePlan(ctx, s.pool, orgID)
	if err != nil {
		return v, err
	}
	v.Plan = p
	r, err := loadRow(ctx, s.pool, orgID, false)
	if errors.Is(err, pgx.ErrNoRows) {
		v.Status = plans.StatusActive
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.Status, v.PaidAccess = r.status, r.hasPaidAccess()
	v.CancelAtPeriodEnd, v.CurrentPeriodEnd = r.cancelAtPeriodEnd, r.periodEnd
	v.CanManage = r.customerID != nil && v.BillingEnabled
	v.HasSubscription = r.subscriptionID != nil && r.planCode != plans.FreeCode
	if !v.PaidAccess {
		v.CancelAtPeriodEnd, v.CurrentPeriodEnd = false, nil // stale paid-period details are not shown for a free organization
	}
	return v, nil
}

// CreateCheckout starts a hosted Checkout for a plan. The price comes from configuration, never from the caller.
// Nothing is granted here: entitlements change only when the provider reports an active subscription.
func (s *Service) CreateCheckout(ctx context.Context, orgID uuid.UUID, userEmail, planCode string) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	price, ok := s.cfg.PriceForPlan(planCode)
	if !ok {
		return "", validation.New("plan", "must be pro or agency")
	}
	var active bool
	if err := s.pool.QueryRow(ctx, `SELECT active FROM plans WHERE code = $1`, planCode).Scan(&active); err != nil || !active {
		return "", validation.New("plan", "must be pro or agency")
	}
	if s.priceProblem(planCode) != "" {
		return "", ErrPriceMismatch // never charge something other than the price shown (the details were logged and reported when it was detected)
	}
	cur, err := plans.EffectivePlan(ctx, s.pool, orgID)
	if err != nil {
		return "", err
	}
	if cur.Code != plans.FreeCode {
		return "", ErrAlreadySubscribed // plan changes go through the customer portal
	}
	customerID, err := s.ensureCustomer(ctx, orgID, userEmail)
	if err != nil {
		return "", err
	}
	return s.provider.CreateCheckoutSession(ctx, CheckoutParams{
		OrganizationID: orgID, PriceID: price, CustomerID: customerID,
		SuccessURL: s.cfg.AppBaseURL + "/billing?checkout=success",
		CancelURL:  s.cfg.AppBaseURL + "/billing?checkout=canceled",
	})
}

// ensureCustomer returns the organization's provider customer, creating and storing it first if needed. Creating it BEFORE
// Checkout means every later subscription event can be attributed through the customer ID (a first-class provider object that we
// map in our own database) instead of depending on metadata or on the order in which events arrive.
func (s *Service) ensureCustomer(ctx context.Context, orgID uuid.UUID, email string) (string, error) {
	r, err := loadRow(ctx, s.pool, orgID, false)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if r.customerID != nil {
		return *r.customerID, nil
	}
	created, err := s.provider.CreateCustomer(ctx, email, orgID)
	if err != nil {
		return "", err
	}
	// Store it only if nobody else did first (two simultaneous requests): the first stored customer wins, the other is simply unused.
	var stored string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status)
			SELECT $1, id, 'active' FROM plans WHERE code = 'free' ON CONFLICT (organization_id) DO NOTHING`, orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET provider = 'stripe', provider_customer_id = $2
			WHERE organization_id = $1 AND provider_customer_id IS NULL`, orgID, created); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT provider_customer_id FROM subscriptions WHERE organization_id = $1`, orgID).Scan(&stored)
	})
	if err != nil {
		return "", err
	}
	return stored, nil
}

// CreatePortal opens the provider's customer portal (payment method, plan changes, cancellation).
func (s *Service) CreatePortal(ctx context.Context, orgID uuid.UUID) (string, error) {
	if !s.Enabled() {
		return "", ErrNotConfigured
	}
	r, err := loadRow(ctx, s.pool, orgID, false)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if r.customerID == nil {
		return "", ErrNoCustomer
	}
	return s.provider.CreatePortalSession(ctx, *r.customerID, s.cfg.AppBaseURL+"/billing")
}

// Cancel schedules cancellation at the end of the paid period. The customer keeps access until then.
func (s *Service) Cancel(ctx context.Context, orgID uuid.UUID) error {
	if !s.Enabled() {
		return ErrNotConfigured
	}
	r, err := loadRow(ctx, s.pool, orgID, false)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (r.subscriptionID == nil || !r.hasPaidAccess()) {
		return ErrNoSubscription
	}
	if err != nil {
		return err
	}
	st, err := s.provider.CancelAtPeriodEnd(ctx, *r.subscriptionID)
	if err != nil {
		return err
	}
	return s.applyDirect(ctx, orgID, st)
}

// Resync re-reads the subscription from the provider and applies it: the recovery path when webhooks were missed.
func (s *Service) Resync(ctx context.Context, orgID uuid.UUID) error {
	if !s.Enabled() {
		return ErrNotConfigured
	}
	r, err := loadRow(ctx, s.pool, orgID, false)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && r.subscriptionID == nil {
		return ErrNoSubscription
	}
	if err != nil {
		return err
	}
	st, err := s.provider.GetSubscription(ctx, *r.subscriptionID)
	if err != nil {
		return err
	}
	return s.applyDirect(ctx, orgID, st)
}

func (s *Service) applyDirect(ctx context.Context, orgID uuid.UUID, st SubscriptionState) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := s.apply(ctx, tx, orgID, st)
		return err
	})
}

// Outcomes of applying provider state.
const (
	outcomeApplied = "processed"
	outcomeIgnored = "ignored"
)

// apply writes a subscription's state to the organization's subscription row, in the caller's transaction. The state passed in
// must be the provider's CURRENT state (freshly read), never an event payload: that is what makes delivery order irrelevant.
//
// Safety rules:
//   - the row is locked, so concurrent updates for one organization are applied one at a time;
//   - a subscription whose price is not mapped to a plan never grants access;
//   - a second subscription never overwrites an organization's current paid subscription: stray cancellations of an
//     abandoned/duplicate subscription are ignored, and a second *paid* one is reported as a conflict to resolve manually;
//   - the provider status is mapped by MapStatus; entitlements then follow plans.GrantsPaidAccess.
func (s *Service) apply(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, st SubscriptionState) (string, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status)
		SELECT $1, id, 'active' FROM plans WHERE code = 'free' ON CONFLICT (organization_id) DO NOTHING`, orgID); err != nil {
		return "", err
	}
	cur, err := loadRow(ctx, tx, orgID, true)
	if err != nil {
		return "", err
	}
	mapped := MapStatus(st.Status)
	if cur.subscriptionID != nil && *cur.subscriptionID != st.ID && cur.hasPaidAccess() {
		if plans.GrantsPaidAccess(mapped) {
			return "", fmt.Errorf("%w (current %s, incoming %s)", ErrConflict, *cur.subscriptionID, st.ID)
		}
		return outcomeIgnored, nil // an unrelated, non-paying subscription changed; the current one is untouched
	}

	planCode := cur.planCode
	var mappedPlan string
	for _, pid := range st.PriceIDs {
		if code, ok := s.cfg.PlanForPrice(pid); ok {
			mappedPlan = code
			break
		}
	}
	switch {
	case mappedPlan != "":
		planCode = mappedPlan
	case plans.GrantsPaidAccess(mapped):
		return "", fmt.Errorf("%w (prices %v)", ErrUnknownPrice, st.PriceIDs)
	}

	_, err = tx.Exec(ctx, `UPDATE subscriptions SET
			plan_id = (SELECT id FROM plans WHERE code = $2), provider = 'stripe',
			provider_customer_id = COALESCE(NULLIF($3, ''), provider_customer_id), provider_subscription_id = $4,
			status = $5, current_period_start = $6, current_period_end = $7, cancel_at_period_end = $8, last_synced_at = now()
		WHERE organization_id = $1`,
		orgID, planCode, st.CustomerID, st.ID, mapped, st.PeriodStart, st.PeriodEnd, st.CancelAtPeriodEnd)
	if err != nil {
		return "", err
	}
	return outcomeApplied, nil
}
