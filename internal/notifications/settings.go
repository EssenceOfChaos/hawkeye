package notifications

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/auth"
	"hawkeye/internal/email"
)

const MaxSettingsPerOrganization = 10

var (
	ErrNotFound     = errors.New("notification setting not found")
	ErrDuplicate    = errors.New("destination already configured")
	ErrTooMany      = errors.New("too many notification destinations")
	ErrInvalidToken = errors.New("invalid or expired token")
)

type Setting struct {
	ID          uuid.UUID  `json:"id"`
	UserID      *uuid.UUID `json:"user_id"`
	Channel     string     `json:"channel"`
	Destination string     `json:"destination"`
	Enabled     bool       `json:"enabled"`
	Verified    bool       `json:"verified"`
	CreatedAt   time.Time  `json:"created_at"`
}

// Actor is the authenticated user performing an action.
type Actor struct {
	ID    uuid.UUID
	Email string
}

type Service struct {
	pool       *pgxpool.Pool
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
	from       string
	tokenTTL   time.Duration
}

func NewService(pool *pgxpool.Pool, mail email.Sender, log *slog.Logger, appBaseURL, from string) *Service {
	return &Service{pool: pool, mail: mail, log: log, appBaseURL: appBaseURL, from: from, tokenTTL: 48 * time.Hour}
}

const cols = `id, user_id, channel, destination, enabled, verified_at IS NOT NULL, created_at`

func scan(row pgx.Row) (Setting, error) {
	var s Setting
	err := row.Scan(&s.ID, &s.UserID, &s.Channel, &s.Destination, &s.Enabled, &s.Verified, &s.CreatedAt)
	return s, err
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Setting, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM notification_settings WHERE organization_id = $1 ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		st, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Create adds an email destination. The caller's own address is trusted (their email is already verified);
// any other address receives a confirmation link and gets no alerts until it is confirmed, so the product
// cannot be used to send alert mail to people who never agreed to it.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, actor Actor, destination string) (Setting, error) {
	dest, err := auth.NormalizeEmail(destination)
	if err != nil {
		return Setting{}, err
	}
	own := dest == actor.Email

	var st Setting
	var rawToken string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialise per organization so the count check cannot be raced.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxSettingsPerOrganization {
			return ErrTooMany
		}
		var userID *uuid.UUID
		if own {
			userID = &actor.ID
		}
		st, err = scan(tx.QueryRow(ctx, `INSERT INTO notification_settings (organization_id, user_id, channel, destination, verified_at)
			VALUES ($1, $2, 'email', $3, CASE WHEN $4 THEN now() END) RETURNING `+cols, orgID, userID, dest, own))
		if err != nil {
			return err
		}
		if own {
			return nil
		}
		var hash []byte
		rawToken, hash = newToken()
		_, err = tx.Exec(ctx, `INSERT INTO notification_setting_tokens (setting_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, st.ID, hash, s.tokenTTL)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Setting{}, ErrDuplicate
		}
		return Setting{}, err
	}
	if rawToken != "" {
		s.sendConfirmation(ctx, dest, actor.Email, rawToken)
	}
	return st, nil
}

// SetEnabled turns alerts to a destination on or off.
func (s *Service) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Setting, error) {
	st, err := scan(s.pool.QueryRow(ctx, `UPDATE notification_settings SET enabled = $3
		WHERE id = $1 AND organization_id = $2 RETURNING `+cols, id, orgID, enabled))
	if errors.Is(err, pgx.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	return st, err
}

func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_settings WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResendConfirmation issues a fresh confirmation link for an unverified destination (older links stop working).
func (s *Service) ResendConfirmation(ctx context.Context, orgID, id uuid.UUID, actor Actor) error {
	var dest string
	var verified bool
	err := s.pool.QueryRow(ctx, `SELECT destination, verified_at IS NOT NULL FROM notification_settings
		WHERE id = $1 AND organization_id = $2`, id, orgID).Scan(&dest, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil || verified {
		return err
	}
	raw, hash := newToken()
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE notification_setting_tokens SET used_at = now() WHERE setting_id = $1 AND used_at IS NULL`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO notification_setting_tokens (setting_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, id, hash, s.tokenTTL)
		return err
	})
	if err != nil {
		return err
	}
	s.sendConfirmation(ctx, dest, actor.Email, raw)
	return nil
}

// Confirm consumes a confirmation token (clicked from the destination's mailbox; no login required).
func (s *Service) Confirm(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var settingID uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE notification_setting_tokens SET used_at = now()
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING setting_id`, hashToken(token)).Scan(&settingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE notification_settings SET verified_at = COALESCE(verified_at, now()) WHERE id = $1`, settingID)
		return err
	})
}

func (s *Service) sendConfirmation(ctx context.Context, to, requestedBy, raw string) {
	link := fmt.Sprintf("%s/confirm-alert-address?token=%s", s.appBaseURL, raw)
	c := email.Content{
		Preheader: "Confirm this address to receive HawkEye alerts.", Heading: "Confirm your alert address",
		Paragraphs:  []string{fmt.Sprintf("%s asked to send HawkEye website-outage alerts to this address. To start receiving them, confirm below.", clean(requestedBy))},
		ButtonLabel: "Confirm alert address", ButtonURL: link,
		Note: "If you did not expect this, ignore this email: no alerts will be sent.", AppBaseURL: s.appBaseURL,
	}
	err := s.mail.Send(ctx, c.Message(s.from, to, "Confirm your HawkEye alert address"))
	if err != nil {
		s.log.ErrorContext(ctx, "send alert-address confirmation failed", "error", err)
	}
}

func newToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system CSPRNG failing is unrecoverable
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw)
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
