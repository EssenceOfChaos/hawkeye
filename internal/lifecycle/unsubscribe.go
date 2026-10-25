package lifecycle

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidToken: the unsubscribe link is not one we issued.
var ErrInvalidToken = errors.New("invalid unsubscribe token")

// Service handles the recipient's side: opting out of lifecycle email.
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Unsubscribe stops lifecycle email for the person the token belongs to. It is idempotent (a second click, or a mail client's one-click POST after
// the page, succeeds and keeps the first time), and it touches nothing else: alerts, verification and security email still arrive.
func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	id, err := uuid.Parse(token)
	if err != nil {
		return ErrInvalidToken
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET lifecycle_opt_out_at = COALESCE(lifecycle_opt_out_at, now()) WHERE unsubscribe_token = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidToken
	}
	return nil
}
