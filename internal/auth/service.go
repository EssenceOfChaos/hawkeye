// Package auth implements registration, sessions, password reset and email verification.
package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/email"
	"hawkeye/internal/validation"
)

var (
	ErrWrongPassword      = errors.New("current password is incorrect")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrUnauthenticated    = errors.New("not authenticated")
)

type User struct {
	ID              uuid.UUID  `json:"id"`
	Email           string     `json:"email"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

type Config struct {
	SessionTTL      time.Duration
	ResetTokenTTL   time.Duration
	VerifyTokenTTL  time.Duration
	AppBaseURL      string
	Hasher          Hasher
	lastSeenRefresh time.Duration
}

func (c Config) withDefaults() Config {
	if c.SessionTTL == 0 {
		c.SessionTTL = 14 * 24 * time.Hour
	}
	if c.ResetTokenTTL == 0 {
		c.ResetTokenTTL = time.Hour
	}
	if c.VerifyTokenTTL == 0 {
		c.VerifyTokenTTL = 48 * time.Hour
	}
	if c.Hasher == (Hasher{}) {
		c.Hasher = DefaultHasher()
	}
	c.lastSeenRefresh = time.Minute
	return c
}

type Service struct {
	pool      *pgxpool.Pool
	mail      email.Sender
	log       *slog.Logger
	cfg       Config
	dummyHash string // verified when the account is unknown, to equalise login timing
}

func NewService(pool *pgxpool.Pool, mail email.Sender, log *slog.Logger, cfg Config) (*Service, error) {
	cfg = cfg.withDefaults()
	dummy, err := cfg.Hasher.Hash("dummy-password-for-timing")
	if err != nil {
		return nil, err
	}
	return &Service{pool: pool, mail: mail, log: log, cfg: cfg, dummyHash: dummy}, nil
}

func (s *Service) SessionTTL() time.Duration { return s.cfg.SessionTTL }

const userCols = `id, email, email_verified_at, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.CreatedAt)
	return u, err
}

// Register creates a user, a personal organization (owner membership) on the free plan and a
// verification token atomically, then starts a session. The verification email is sent after commit.
func (s *Service) Register(ctx context.Context, emailAddr, password string) (User, string, error) {
	addr, err := NormalizeEmail(emailAddr)
	if err != nil {
		return User{}, "", err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, "", err
	}
	hash, err := s.cfg.Hasher.Hash(password)
	if err != nil {
		return User{}, "", err
	}
	verifyRaw, verifyHash, err := newToken()
	if err != nil {
		return User{}, "", err
	}

	var user User
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		user, err = scanUser(tx.QueryRow(ctx,
			`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING `+userCols, addr, hash))
		if err != nil {
			return err
		}
		var orgID uuid.UUID
		name := strings.SplitN(addr, "@", 2)[0] + "'s organization"
		if err := tx.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ($1) RETURNING id`, name).Scan(&orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, user.ID); err != nil {
			return err
		}
		// The owner receives alerts at their own address; it becomes active once the address is verified.
		if _, err := tx.Exec(ctx, `INSERT INTO notification_settings (organization_id, user_id, channel, destination)
			VALUES ($1, $2, 'email', $3)`, orgID, user.ID, user.Email); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status)
			SELECT $1, id, 'active' FROM plans WHERE code = 'free'`, orgID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO email_verification_tokens (user_id, token_hash, expires_at) VALUES ($1, $2, now() + $3::interval)`,
			user.ID, verifyHash, s.cfg.VerifyTokenTTL)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "users_email_key" {
			return User{}, "", ErrEmailTaken
		}
		return User{}, "", fmt.Errorf("register: %w", err)
	}

	s.sendVerification(ctx, user.Email, verifyRaw)
	session, err := s.createSession(ctx, user.ID)
	if err != nil {
		return User{}, "", err
	}
	return user, session, nil
}

// Login verifies credentials and starts a session. Unknown, disabled and wrong-password
// cases are indistinguishable to the caller.
func (s *Service) Login(ctx context.Context, emailAddr, password string) (User, string, error) {
	addr := strings.ToLower(strings.TrimSpace(emailAddr))
	var (
		u        User
		hash     string
		disabled *time.Time
	)
	err := s.pool.QueryRow(ctx, `SELECT `+userCols+`, password_hash, disabled_at FROM users WHERE email = $1`, addr).
		Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.CreatedAt, &hash, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		_, _ = Verify(password, s.dummyHash)
		return User{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return User{}, "", fmt.Errorf("login lookup: %w", err)
	}
	ok, verr := Verify(password, hash)
	if verr != nil {
		s.log.ErrorContext(ctx, "stored password hash unreadable", "user_id", u.ID)
	}
	if !ok || disabled != nil {
		return User{}, "", ErrInvalidCredentials
	}
	session, err := s.createSession(ctx, u.ID)
	if err != nil {
		return User{}, "", err
	}
	return u, session, nil
}

func (s *Service) createSession(ctx context.Context, userID uuid.UUID) (string, error) {
	raw, hash, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, now() + $3::interval)`,
		userID, hash, s.cfg.SessionTTL)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return raw, nil
}

// Authenticate resolves a raw session token to its user.
func (s *Service) Authenticate(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrUnauthenticated
	}
	hash := HashToken(token)
	u, err := scanUser(s.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.email_verified_at, u.created_at
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL`, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthenticated
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate: %w", err)
	}
	// Best-effort activity timestamp, written at most once per refresh window.
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now()
		WHERE token_hash = $1 AND last_seen_at < now() - $2::interval`, hash, s.cfg.lastSeenRefresh)
	return u, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, HashToken(token))
	return err
}

// ForgotPassword always succeeds from the caller's point of view so that account existence is not revealed.
func (s *Service) ForgotPassword(ctx context.Context, emailAddr string) error {
	addr := strings.ToLower(strings.TrimSpace(emailAddr))
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM users WHERE email = $1 AND disabled_at IS NULL`, addr).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("forgot password lookup: %w", err)
	}
	raw, hash, err := newToken()
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Only the newest reset link is valid.
		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, id, hash, s.cfg.ResetTokenTTL)
		return err
	})
	if err != nil {
		return fmt.Errorf("create reset token: %w", err)
	}
	link := fmt.Sprintf("%s/reset-password?token=%s", s.cfg.AppBaseURL, raw)
	if err := s.mail.Send(ctx, email.Message{To: addr, Subject: "Reset your HawkEye password",
		Text: "Use this link to choose a new password (valid for 1 hour):\n\n" + link +
			"\n\nIf you did not request this, you can ignore this email."}); err != nil {
		s.log.ErrorContext(ctx, "send password reset email failed", "user_id", id, "error", err)
	}
	return nil
}

// ResetPassword consumes a reset token, sets the new password and revokes every session.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.cfg.Hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		userID, err := consumeToken(ctx, tx, "password_reset_tokens", token)
		if err != nil {
			return err
		}
		// Receiving the reset email also proves ownership of the address.
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2,
			email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, userID, hash); err != nil {
			return err
		}
		if err := activateOwnAlertAddress(ctx, tx, userID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
		return err
	})
}

// VerifyEmail consumes a verification token and marks the address verified.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		userID, err := consumeToken(ctx, tx, "email_verification_tokens", token)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1`, userID); err != nil {
			return err
		}
		return activateOwnAlertAddress(ctx, tx, userID)
	})
}

// activateOwnAlertAddress verifies the alert destinations that are the user's own email address.
func activateOwnAlertAddress(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE notification_settings s SET verified_at = now()
		FROM users u WHERE u.id = $1 AND s.user_id = u.id AND s.destination = u.email AND s.verified_at IS NULL`, userID)
	return err
}

// ResendVerification issues a fresh verification email (older links are invalidated).
func (s *Service) ResendVerification(ctx context.Context, u User) error {
	if u.EmailVerified() {
		return nil
	}
	raw, hash, err := newToken()
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE email_verification_tokens SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`, u.ID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, u.ID, hash, s.cfg.VerifyTokenTTL)
		return err
	})
	if err != nil {
		return fmt.Errorf("create verification token: %w", err)
	}
	s.sendVerification(ctx, u.Email, raw)
	return nil
}

func (s *Service) sendVerification(ctx context.Context, to, raw string) {
	link := fmt.Sprintf("%s/verify-email?token=%s", s.cfg.AppBaseURL, raw)
	if err := s.mail.Send(ctx, email.Message{To: to, Subject: "Verify your HawkEye email address",
		Text: "Confirm your email address to enable billing and alert delivery:\n\n" + link}); err != nil {
		s.log.ErrorContext(ctx, "send verification email failed", "error", err)
	}
}

// consumeToken atomically marks a valid, unused, unexpired token as used and returns its user.
// table is always a package constant, never user input.
func consumeToken(ctx context.Context, tx pgx.Tx, table, raw string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `UPDATE `+table+` SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING user_id`, HashToken(raw)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrInvalidToken
	}
	return id, err
}

// VerifyPassword checks a user's current password (used to re-authenticate sensitive actions such as account deletion).
func (s *Service) VerifyPassword(ctx context.Context, userID uuid.UUID, password string) error {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1 AND disabled_at IS NULL`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthenticated
	}
	if err != nil {
		return err
	}
	ok, verr := Verify(password, hash)
	if verr != nil || !ok {
		return ErrWrongPassword
	}
	return nil
}

// ChangePassword sets a new password after re-checking the current one, signs out every OTHER session (the caller stays signed in)
// and emails the account owner so an unexpected change is noticed.
func (s *Service) ChangePassword(ctx context.Context, user User, currentSessionToken, current, newPassword string) error {
	if err := ValidatePassword(newPassword); err != nil {
		return validation.New("new_password", "must be between 10 and 128 characters") // name the field the client sent
	}
	if err := s.VerifyPassword(ctx, user.ID, current); err != nil {
		return err
	}
	if newPassword == current {
		return validation.New("new_password", "must be different from your current password")
	}
	hash, err := s.cfg.Hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, user.ID, hash); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, user.ID, HashToken(currentSessionToken))
		return err
	})
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	if err := s.mail.Send(ctx, email.Message{To: user.Email, Subject: "Your HawkEye password was changed",
		Text: "The password for your HawkEye account was just changed, and all other devices were signed out.\n\n" +
			"If this was not you, reset your password immediately: " + s.cfg.AppBaseURL + "/forgot-password"}); err != nil {
		s.log.ErrorContext(ctx, "send password-changed email failed", "user_id", user.ID, "error", err)
	}
	return nil
}
