package lifecycle

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidToken: the unsubscribe link is not one we issued.
var ErrInvalidToken = errors.New("invalid unsubscribe token")

// Lists a person can unsubscribe from. Each email names its own in its unsubscribe link, so unsubscribing from one stops only that one.
const (
	ListOnboarding    = "onboarding"     // the getting-started emails (the default, for links sent before lists existed)
	ListWeeklySummary = "weekly_summary" // the Monday summary (internal/weeklysummary)
)

// Service handles the recipient's side of optional email: unsubscribe links and the email preferences in their account.
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Unsubscribe stops one list of optional email for the person the token belongs to. It is idempotent (a second click, or a mail client's one-click
// POST after the page, succeeds and keeps the first time), and it touches nothing else: alerts, verification and security email still arrive.
func (s *Service) Unsubscribe(ctx context.Context, token, list string) error {
	id, err := uuid.Parse(token)
	if err != nil {
		return ErrInvalidToken
	}
	column := "lifecycle_opt_out_at"
	switch list {
	case "", ListOnboarding:
	case ListWeeklySummary:
		column = "weekly_summary_opt_out_at"
	default:
		return ErrInvalidToken
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET `+column+` = COALESCE(`+column+`, now()) WHERE unsubscribe_token = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidToken
	}
	return nil
}

// Preferences are the optional emails a person receives. Alerts, verification and security email are not optional and are not listed.
type Preferences struct {
	WeeklySummary bool `json:"weekly_summary"`
	Onboarding    bool `json:"onboarding"`
}

// Preferences returns what the person receives.
func (s *Service) Preferences(ctx context.Context, userID uuid.UUID) (Preferences, error) {
	var p Preferences
	err := s.pool.QueryRow(ctx, `SELECT weekly_summary_opt_out_at IS NULL, lifecycle_opt_out_at IS NULL FROM users WHERE id = $1`, userID).
		Scan(&p.WeeklySummary, &p.Onboarding)
	return p, err
}

// SetPreferences turns optional emails on or off; a nil field is left as it is.
func (s *Service) SetPreferences(ctx context.Context, userID uuid.UUID, weekly, onboarding *bool) (Preferences, error) {
	_, err := s.pool.Exec(ctx, `UPDATE users SET
			weekly_summary_opt_out_at = CASE WHEN $2::boolean IS NULL THEN weekly_summary_opt_out_at WHEN $2 THEN NULL ELSE COALESCE(weekly_summary_opt_out_at, now()) END,
			lifecycle_opt_out_at = CASE WHEN $3::boolean IS NULL THEN lifecycle_opt_out_at WHEN $3 THEN NULL ELSE COALESCE(lifecycle_opt_out_at, now()) END
		WHERE id = $1`, userID, weekly, onboarding)
	if err != nil {
		return Preferences{}, err
	}
	return s.Preferences(ctx, userID)
}
