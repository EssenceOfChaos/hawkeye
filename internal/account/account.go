// Package account implements data export and account deletion.
package account

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/checks"
	"hawkeye/internal/email"
	"hawkeye/internal/validation"
)

var (
	// ErrSharedOrganization: the user owns an organization that has other members, so deleting the account would delete their data too.
	ErrSharedOrganization = errors.New("account owns an organization with other members")
)

type Service struct {
	pool     *pgxpool.Pool
	auth     *auth.Service
	provider billing.Provider // nil when billing is not configured
	mail     email.Sender
	log      *slog.Logger
	baseURL  string // for links and the logo in emails
}

func NewService(pool *pgxpool.Pool, a *auth.Service, provider billing.Provider, mail email.Sender, log *slog.Logger, appBaseURL string) *Service {
	return &Service{pool: pool, auth: a, provider: provider, mail: mail, log: log, baseURL: appBaseURL}
}

// ---- export

type Export struct {
	ExportedAt    time.Time   `json:"exported_at"`
	Notes         []string    `json:"notes"`
	User          ExportUser  `json:"user"`
	Organizations []ExportOrg `json:"organizations"`
}

type ExportUser struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type ExportOrg struct {
	ID                   uuid.UUID         `json:"id"`
	Name                 string            `json:"name"`
	Role                 string            `json:"role"`
	CreatedAt            time.Time         `json:"created_at"`
	Billing              ExportBilling     `json:"billing"`
	Monitors             []ExportMonitor   `json:"monitors"`
	Incidents            []ExportIncident  `json:"incidents"`
	AlertRecipients      []ExportRecipient `json:"alert_recipients"`
	MaintenanceWindows   []ExportWindow    `json:"maintenance_windows"`
	Domains              []ExportDomain    `json:"domains"`
	StatusPage           *ExportStatusPage `json:"status_page"`
	CheckResultsRetained int               `json:"check_results_retained_count"`
}

type ExportBilling struct {
	Plan              string     `json:"plan"`
	Status            string     `json:"status"`
	CurrentPeriodEnd  *time.Time `json:"current_period_end"`
	CancelAtPeriodEnd bool       `json:"cancel_at_period_end"`
}

type ExportMonitor struct {
	ID                   uuid.UUID `json:"id"`
	Name                 string    `json:"name"`
	URL                  string    `json:"url"`
	Method               string    `json:"method"`
	ExpectedStatusCode   *int      `json:"expected_status_code"`
	CheckIntervalSeconds int       `json:"check_interval_seconds"`
	TimeoutSeconds       int       `json:"timeout_seconds"`
	Enabled              bool      `json:"enabled"`
	CurrentStatus        string    `json:"current_status"`
	CreatedAt            time.Time `json:"created_at"`
	// Credentials are never exported; only the authentication type and its non-secret settings are.
	Headers    []checks.Header    `json:"headers"`
	Body       *string            `json:"body"`
	Auth       ExportAuth         `json:"auth"`
	Assertions []checks.Assertion `json:"assertions"`
	Regions    []string           `json:"regions"`
}

// ExportAuth names a monitor's authentication method and its non-secret settings.
type ExportAuth struct {
	Type       string `json:"type"`
	Username   string `json:"username,omitempty"`
	HeaderName string `json:"header_name,omitempty"`
}

type ExportIncident struct {
	ID            uuid.UUID  `json:"id"`
	MonitorID     uuid.UUID  `json:"monitor_id"`
	StartedAt     time.Time  `json:"started_at"`
	ResolvedAt    *time.Time `json:"resolved_at"`
	Status        string     `json:"status"`
	CauseCategory *string    `json:"cause_category"`
	Summary       *string    `json:"summary"`
}

type ExportWindow struct {
	Name            string    `json:"name"`
	StartsAt        time.Time `json:"starts_at"`
	DurationMinutes int       `json:"duration_minutes"`
	Repeat          string    `json:"repeat"`
	AllMonitors     bool      `json:"all_monitors"`
	MonitorIDs      []string  `json:"monitor_ids"`
}

// ExportDomain is a tracked domain with the registration expiry date a registry reported for it, if any.
type ExportDomain struct {
	Name          string     `json:"name"`
	AddedManually bool       `json:"added_manually"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CheckedAt     *time.Time `json:"checked_at"`
}

type ExportStatusPage struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Published   bool     `json:"published"`
	MonitorIDs  []string `json:"monitor_ids"`
}

// ExportRecipient is an alert destination. For Slack and webhook destinations the URL is a secret, so only the masked label is exported.
type ExportRecipient struct {
	Channel     string    `json:"channel"`
	Destination string    `json:"destination"`
	Enabled     bool      `json:"enabled"`
	Verified    bool      `json:"verified"`
	CreatedAt   time.Time `json:"created_at"`
}

// Export gathers everything the user can access, as portable JSON. Credentials, session tokens and payment-provider identifiers are never included.
// Check results can be very large and are retained only for your plan's window, so they are summarised by count; they stay available through the
// results endpoints for as long as they are retained.
func (s *Service) Export(ctx context.Context, userID uuid.UUID) (Export, error) {
	out := Export{ExportedAt: time.Now().UTC(), Notes: []string{
		"Passwords, session tokens and payment-provider identifiers are never included.",
		"Individual check results are not included (they can be very large and are deleted after your plan's history window); use GET /api/v1/monitors/{id}/results while they are retained.",
		"Card details are held by Stripe, not by HawkEye.",
		"For an organization you belong to as a member, only its name and your role are included: its websites, alert recipients, billing and status page belong to its owner, who can export them.",
	}}
	if err := s.pool.QueryRow(ctx, `SELECT id, email, email_verified_at, created_at FROM users WHERE id = $1`, userID).
		Scan(&out.User.ID, &out.User.Email, &out.User.EmailVerifiedAt, &out.User.CreatedAt); err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.name, m.role, o.created_at,
			COALESCE(p.code, 'free'), COALESCE(s.status, 'active'), s.current_period_end, COALESCE(s.cancel_at_period_end, false)
		FROM organization_members m JOIN organizations o ON o.id = m.organization_id
		LEFT JOIN subscriptions s ON s.organization_id = o.id LEFT JOIN plans p ON p.id = s.plan_id
		WHERE m.user_id = $1 ORDER BY o.created_at, o.id`, userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var o ExportOrg
		if err := rows.Scan(&o.ID, &o.Name, &o.Role, &o.CreatedAt, &o.Billing.Plan, &o.Billing.Status, &o.Billing.CurrentPeriodEnd, &o.Billing.CancelAtPeriodEnd); err != nil {
			return out, err
		}
		out.Organizations = append(out.Organizations, o)
	}
	rows.Close()
	for i := range out.Organizations {
		o := &out.Organizations[i]
		if o.Role != "owner" {
			// A team's data is its owner's, not the data of everyone invited to look at it.
			o.Monitors, o.Incidents, o.AlertRecipients = []ExportMonitor{}, []ExportIncident{}, []ExportRecipient{}
			o.MaintenanceWindows, o.Domains = []ExportWindow{}, []ExportDomain{}
			o.Billing = ExportBilling{}
			continue
		}
		if err := s.fillOrg(ctx, o); err != nil {
			return out, err
		}
	}
	if out.Organizations == nil {
		out.Organizations = []ExportOrg{}
	}
	return out, nil
}

func (s *Service) fillOrg(ctx context.Context, o *ExportOrg) error {
	o.Monitors, o.Incidents, o.AlertRecipients = []ExportMonitor{}, []ExportIncident{}, []ExportRecipient{}
	rows, err := s.pool.Query(ctx, `SELECT id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds, enabled, current_status, created_at,
		request_headers, request_body, auth_type, COALESCE(auth_username, ''), COALESCE(auth_header_name, ''), assertions, regions
		FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL ORDER BY created_at, id`, o.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m ExportMonitor
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.Method, &m.ExpectedStatusCode, &m.CheckIntervalSeconds, &m.TimeoutSeconds, &m.Enabled, &m.CurrentStatus, &m.CreatedAt,
			&m.Headers, &m.Body, &m.Auth.Type, &m.Auth.Username, &m.Auth.HeaderName, &m.Assertions, &m.Regions); err != nil {
			rows.Close()
			return err
		}
		o.Monitors = append(o.Monitors, m)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT i.id, i.monitor_id, i.started_at, i.resolved_at, i.status, i.cause_category, i.summary
		FROM incidents i JOIN monitors m ON m.id = i.monitor_id WHERE m.organization_id = $1 ORDER BY i.started_at, i.id`, o.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var i ExportIncident
		if err := rows.Scan(&i.ID, &i.MonitorID, &i.StartedAt, &i.ResolvedAt, &i.Status, &i.CauseCategory, &i.Summary); err != nil {
			rows.Close()
			return err
		}
		o.Incidents = append(o.Incidents, i)
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT channel, COALESCE(label, destination), enabled, verified_at IS NOT NULL, created_at FROM notification_settings
		WHERE organization_id = $1 ORDER BY created_at, id`, o.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r ExportRecipient
		if err := rows.Scan(&r.Channel, &r.Destination, &r.Enabled, &r.Verified, &r.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		o.AlertRecipients = append(o.AlertRecipients, r)
	}
	rows.Close()

	o.MaintenanceWindows = []ExportWindow{}
	rows, err = s.pool.Query(ctx, `SELECT w.name, w.starts_at, w.duration_minutes, w.repeat, w.all_monitors,
			COALESCE(array_agg(wm.monitor_id::text ORDER BY wm.monitor_id) FILTER (WHERE wm.monitor_id IS NOT NULL), '{}')
		FROM maintenance_windows w LEFT JOIN maintenance_window_monitors wm ON wm.window_id = w.id
		WHERE w.organization_id = $1 GROUP BY w.id ORDER BY w.starts_at, w.id`, o.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var w ExportWindow
		if err := rows.Scan(&w.Name, &w.StartsAt, &w.DurationMinutes, &w.Repeat, &w.AllMonitors, &w.MonitorIDs); err != nil {
			rows.Close()
			return err
		}
		o.MaintenanceWindows = append(o.MaintenanceWindows, w)
	}
	rows.Close()

	o.Domains = []ExportDomain{}
	rows, err = s.pool.Query(ctx, `SELECT name, manual, expires_at, checked_at FROM domains WHERE organization_id = $1 ORDER BY name`, o.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d ExportDomain
		if err := rows.Scan(&d.Name, &d.AddedManually, &d.ExpiresAt, &d.CheckedAt); err != nil {
			rows.Close()
			return err
		}
		o.Domains = append(o.Domains, d)
	}
	rows.Close()

	var sp ExportStatusPage
	err = s.pool.QueryRow(ctx, `SELECT p.slug, p.title, p.description, p.published,
			COALESCE((SELECT array_agg(pm.monitor_id::text ORDER BY pm.position) FROM status_page_monitors pm WHERE pm.page_id = p.id), '{}')
		FROM status_pages p WHERE p.organization_id = $1`, o.ID).Scan(&sp.Slug, &sp.Title, &sp.Description, &sp.Published, &sp.MonitorIDs)
	switch {
	case err == nil:
		o.StatusPage = &sp
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	return s.pool.QueryRow(ctx, `SELECT count(*) FROM check_results r JOIN monitors m ON m.id = r.monitor_id WHERE m.organization_id = $1`, o.ID).Scan(&o.CheckResultsRetained)
}

// ---- deletion

// Delete permanently removes the account after re-authentication and typed confirmation.
//
//   - Organizations where the user is the only member are deleted with everything in them (monitors, results, incidents, alert recipients, subscription record).
//   - If the user owns an organization that has other members, deletion is refused (ErrSharedOrganization): that would delete other people's data.
//   - Memberships in organizations owned by others are simply removed.
//   - Each deleted organization's payment-provider customer is deleted FIRST. Deleting a Stripe customer also cancels its subscriptions immediately
//     (no refund), so a paying customer cannot be left billed with no account. If that call fails, nothing is deleted locally and the user may retry.
//   - Stored webhook payloads mentioning those customers are removed. Stripe itself retains invoices/transactions it must keep by law.
func (s *Service) Delete(ctx context.Context, user auth.User, password, confirmEmail string) error {
	if !strings.EqualFold(strings.TrimSpace(confirmEmail), user.Email) {
		return validation.New("confirm_email", "must match your account email address")
	}
	if err := s.auth.VerifyPassword(ctx, user.ID, password); err != nil {
		return err
	}

	rows, err := s.pool.Query(ctx, `SELECT m.organization_id, m.role,
			(SELECT count(*) FROM organization_members x WHERE x.organization_id = m.organization_id)
		FROM organization_members m WHERE m.user_id = $1`, user.ID)
	if err != nil {
		return err
	}
	var doomed []uuid.UUID
	for rows.Next() {
		var org uuid.UUID
		var role string
		var members int
		if err := rows.Scan(&org, &role, &members); err != nil {
			rows.Close()
			return err
		}
		switch {
		case members == 1:
			doomed = append(doomed, org)
		case role == "owner":
			rows.Close()
			return ErrSharedOrganization
		}
	}
	rows.Close()

	var customers []string
	if len(doomed) > 0 {
		crow, err := s.pool.Query(ctx, `SELECT provider_customer_id FROM subscriptions WHERE organization_id = ANY($1) AND provider_customer_id IS NOT NULL`, doomed)
		if err != nil {
			return err
		}
		for crow.Next() {
			var id string
			if err := crow.Scan(&id); err != nil {
				crow.Close()
				return err
			}
			customers = append(customers, id)
		}
		crow.Close()
	}
	for _, id := range customers {
		if s.provider == nil {
			s.log.WarnContext(ctx, "billing provider not configured; a payment-provider customer record could not be deleted", "user_id", user.ID)
			break
		}
		if err := s.provider.DeleteCustomer(ctx, id); err != nil {
			return fmt.Errorf("delete payment customer: %w", err)
		}
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, id := range customers {
			if _, err := tx.Exec(ctx, `DELETE FROM billing_webhook_events WHERE payload::text LIKE '%' || $1 || '%'`, id); err != nil {
				return err
			}
		}
		if len(doomed) > 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM organizations WHERE id = ANY($1)`, doomed); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	s.log.InfoContext(ctx, "account deleted", "user_id", user.ID, "organizations_deleted", len(doomed))
	c := email.Content{
		Preheader: "Your HawkEye account and data have been deleted.", Heading: "Your account was deleted",
		Paragraphs:  []string{"Your HawkEye account and its data have been permanently deleted, and any subscription was canceled."},
		ButtonLabel: "Contact us", ButtonURL: s.baseURL + "/contact", Tone: email.ToneAlert,
		Note: "If you did not request this, contact us immediately.", AppBaseURL: s.baseURL,
	}
	if err := s.mail.Send(ctx, c.Message("", user.Email, "Your HawkEye account was deleted")); err != nil {
		s.log.WarnContext(ctx, "send account-deleted email failed", "error", err)
	}
	return nil
}
