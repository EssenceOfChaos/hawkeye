// Package twofactor implements two-factor sign-in with an authenticator app (TOTP), recovery codes, and the option for an organization's owner to
// require it of everyone in the organization.
//
// It is optional for each person and off by default for every organization. The secret is sealed with SECRETS_ENCRYPTION_KEY and bound to the
// user; each code works once (totp_last_step); recovery codes are single-use and stored hashed. Turning it on or off, using a recovery code and
// making new codes all email the person, so a change they didn't make is noticed. Sign-in itself (the step after the password) lives in
// internal/auth, which calls VerifyCode. See docs/security.md.
package twofactor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"rsc.io/qr"

	"hawkeye/internal/email"
	"hawkeye/internal/secrets"
)

const (
	// Issuer is the name authenticator apps show next to the code.
	Issuer = "HawkEye"
	// RecoveryCodeCount is how many recovery codes a person gets at a time.
	RecoveryCodeCount = 10
	// GracePeriod is how long a member has to set up two-factor sign-in once their organization requires it (from the later of the requirement
	// and their joining) before the organization is closed to them.
	GracePeriod = 7 * 24 * time.Hour
)

var (
	ErrAlreadyEnabled = errors.New("two-factor sign-in is already on")
	ErrNotEnabled     = errors.New("two-factor sign-in is not on")
	ErrNoSetup        = errors.New("no two-factor setup in progress")
	ErrBadCode        = errors.New("that code is not correct")
	// ErrRequiredByOrganization: the person owns an organization that requires two-factor sign-in, so they can't turn theirs off.
	ErrRequiredByOrganization = errors.New("an organization you own requires two-factor sign-in")
	// ErrOwnerNeedsTwoFactor: an owner must use two-factor sign-in before requiring it of others.
	ErrOwnerNeedsTwoFactor = errors.New("turn on two-factor sign-in for yourself first")
	ErrNotOwner            = errors.New("only the owner can change this")
)

// Service manages two-factor sign-in.
type Service struct {
	pool       *pgxpool.Pool
	box        *secrets.Box
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, box *secrets.Box, mail email.Sender, log *slog.Logger, appBaseURL string) *Service {
	return &Service{pool: pool, box: box, mail: mail, log: log, appBaseURL: appBaseURL, now: time.Now}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

func sealContext(userID uuid.UUID) string { return "totp:" + userID.String() }

// Status is a person's two-factor sign-in, as the account page shows it.
type Status struct {
	Enabled           bool       `json:"enabled"`
	EnabledAt         *time.Time `json:"enabled_at"`
	RecoveryCodesLeft int        `json:"recovery_codes_left"`
}

func (s *Service) Status(ctx context.Context, userID uuid.UUID) (Status, error) {
	var st Status
	err := s.pool.QueryRow(ctx, `SELECT u.totp_enabled_at,
			(SELECT count(*) FROM recovery_codes r WHERE r.user_id = u.id AND r.used_at IS NULL)
		FROM users u WHERE u.id = $1`, userID).Scan(&st.EnabledAt, &st.RecoveryCodesLeft)
	st.Enabled = st.EnabledAt != nil
	return st, err
}

// Setup is what the person needs to add HawkEye to their authenticator app.
type Setup struct {
	Secret    string `json:"secret"`      // to type in by hand
	URI       string `json:"otpauth_uri"` // what the QR code holds
	QRDataURL string `json:"qr_code"`     // a PNG data URL of the QR code
}

// BeginSetup makes a new secret and keeps it aside until Enable proves the person's app produces the right codes. Starting again replaces it.
func (s *Service) BeginSetup(ctx context.Context, userID uuid.UUID, account string) (Setup, error) {
	secret, err := newSecret()
	if err != nil {
		return Setup{}, err
	}
	sealed, err := s.box.Seal(secret, sealContext(userID))
	if err != nil {
		return Setup{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET totp_pending_secret = $2 WHERE id = $1 AND totp_enabled_at IS NULL`, userID, sealed)
	if err != nil {
		return Setup{}, err
	}
	if tag.RowsAffected() == 0 {
		return Setup{}, ErrAlreadyEnabled
	}
	uri := otpauthURI(Issuer, account, secret)
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		return Setup{}, err
	}
	code.Scale = 6
	return Setup{Secret: secret, URI: uri, QRDataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())}, nil
}

// Enable checks a code from the secret being set up, turns two-factor sign-in on and returns the person's recovery codes (shown once).
func (s *Service) Enable(ctx context.Context, userID uuid.UUID, address, code string) ([]string, error) {
	var codes []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var pending *string
		var enabled *time.Time
		if err := tx.QueryRow(ctx, `SELECT totp_pending_secret, totp_enabled_at FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&pending, &enabled); err != nil {
			return err
		}
		if enabled != nil {
			return ErrAlreadyEnabled
		}
		if pending == nil {
			return ErrNoSetup
		}
		secret, err := s.box.Open(*pending, sealContext(userID))
		if err != nil {
			return err
		}
		step, ok := verify(secret, code, s.now(), nil)
		if !ok {
			return ErrBadCode
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret = totp_pending_secret, totp_pending_secret = NULL, totp_enabled_at = $2, totp_last_step = $3
			WHERE id = $1`, userID, s.now(), step); err != nil {
			return err
		}
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, address, "Two-factor sign-in is on", "Two-factor sign-in was turned on",
		"Two-factor sign-in was just turned on for your HawkEye account. From now on, signing in asks for a code from your authenticator app after your password.",
		"Keep your recovery codes somewhere safe: they are the way back in if you lose your phone.")
	return codes, nil
}

// Disable turns two-factor sign-in off after checking a current code (or a recovery code). The caller has already checked the password. An owner
// of an organization that requires it can't turn theirs off.
func (s *Service) Disable(ctx context.Context, userID uuid.UUID, address, code string) error {
	var owns bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_members m JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1 AND m.role = 'owner' AND o.require_two_factor_since IS NOT NULL)`, userID).Scan(&owns); err != nil {
		return err
	}
	if owns {
		return ErrRequiredByOrganization
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := s.verifyTx(ctx, tx, userID, address, code); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET totp_secret = NULL, totp_pending_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL
			WHERE id = $1`, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID)
		return err
	})
	if err != nil {
		return err
	}
	s.notify(ctx, address, "Two-factor sign-in is off", "Two-factor sign-in was turned off",
		"Two-factor sign-in was just turned off for your HawkEye account. Signing in now needs only your password.",
		"If this wasn't you, reset your password now and turn two-factor sign-in back on.")
	return nil
}

// RegenerateRecoveryCodes replaces the person's recovery codes after checking a current code, and returns the new ones (shown once).
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, address, code string) ([]string, error) {
	var codes []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := s.verifyTx(ctx, tx, userID, address, code); err != nil {
			return err
		}
		var err error
		codes, err = replaceRecoveryCodes(ctx, tx, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, address, "New recovery codes", "New recovery codes were made",
		"New recovery codes were just made for your HawkEye account. The old ones no longer work.", "If this wasn't you, reset your password now.")
	return codes, nil
}

// VerifyCode checks a sign-in code inside the caller's transaction: an authenticator code (each works once) or an unused recovery code (used up,
// and the person is emailed). It reports whether a recovery code was used.
func (s *Service) VerifyCode(ctx context.Context, tx pgx.Tx, userID uuid.UUID, address, code string) (bool, error) {
	return s.verifyTx(ctx, tx, userID, address, code)
}

func (s *Service) verifyTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, address, code string) (bool, error) {
	var sealed *string
	var last *int64
	if err := tx.QueryRow(ctx, `SELECT totp_secret, totp_last_step FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&sealed, &last); err != nil {
		return false, err
	}
	if sealed == nil {
		return false, ErrNotEnabled
	}
	if len(normalizeCode(code)) == digits && len(strings.TrimSpace(code)) <= digits+2 {
		secret, err := s.box.Open(*sealed, sealContext(userID))
		if err != nil {
			return false, err
		}
		if step, ok := verify(secret, code, s.now(), last); ok {
			_, err := tx.Exec(ctx, `UPDATE users SET totp_last_step = $2 WHERE id = $1`, userID, step)
			return false, err
		}
		return false, ErrBadCode
	}
	tag, err := tx.Exec(ctx, `UPDATE recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, hashRecoveryCode(code))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, ErrBadCode
	}
	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID).Scan(&left); err != nil {
		return false, err
	}
	s.notify(ctx, address, "A recovery code was used", "A recovery code was used to sign in",
		fmt.Sprintf("A recovery code was just used to sign in to your HawkEye account. You have %d left. If you've lost your authenticator app, set up two-factor sign-in again on your account page.", left),
		"If this wasn't you, reset your password now.")
	return true, nil
}

// ---- Recovery codes: 80 random bits each, shown as xxxx-xxxx-xxxx-xxxx (base32, lowercase), stored as SHA-256.

func newRecoveryCode() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := strings.ToLower(b32.EncodeToString(b))
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16], nil
}

// hashRecoveryCode ignores case, spaces and dashes, so the code works however it is typed.
func hashRecoveryCode(code string) []byte {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= 'a' && r <= 'z') || (r >= '2' && r <= '7') {
			b.WriteRune(r)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return sum[:]
}

func replaceRecoveryCodes(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]string, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM recovery_codes WHERE user_id = $1`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, RecoveryCodeCount)
	for i := range codes {
		c, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes[i] = c
		if _, err := tx.Exec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, hashRecoveryCode(c)); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// ---- Organization requirement.

// Requirement is what an organization's two-factor rule means for one member.
type Requirement struct {
	Required    bool       `json:"require_two_factor"`
	GraceEndsAt *time.Time `json:"two_factor_grace_ends_at,omitempty"` // when a member without it is shut out; null when they have it or it isn't required
	Blocked     bool       `json:"two_factor_blocked"`                 // shut out now, until they set it up
}

func (s *Service) requirement(since, joined, enabledAt *time.Time) Requirement {
	if since == nil {
		return Requirement{}
	}
	r := Requirement{Required: true}
	if enabledAt != nil {
		return r
	}
	start := *since
	if joined != nil && joined.After(start) {
		start = *joined
	}
	end := start.Add(GracePeriod)
	r.GraceEndsAt = &end
	r.Blocked = !s.now().Before(end)
	return r
}

// ForUser returns the requirement of every organization the person belongs to, by organization ID.
func (s *Service) ForUser(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]Requirement, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.require_two_factor_since, m.created_at, u.totp_enabled_at
		FROM organization_members m JOIN organizations o ON o.id = m.organization_id JOIN users u ON u.id = m.user_id WHERE m.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]Requirement{}
	for rows.Next() {
		var id uuid.UUID
		var since, enabled *time.Time
		var joined time.Time
		if err := rows.Scan(&id, &since, &joined, &enabled); err != nil {
			return nil, err
		}
		out[id] = s.requirement(since, &joined, enabled)
	}
	return out, rows.Err()
}

// For returns one organization's requirement for one member.
func (s *Service) For(ctx context.Context, userID, orgID uuid.UUID) (Requirement, error) {
	var since, enabled *time.Time
	var joined time.Time
	err := s.pool.QueryRow(ctx, `SELECT o.require_two_factor_since, m.created_at, u.totp_enabled_at
		FROM organization_members m JOIN organizations o ON o.id = m.organization_id JOIN users u ON u.id = m.user_id
		WHERE m.user_id = $1 AND m.organization_id = $2`, userID, orgID).Scan(&since, &joined, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return Requirement{}, nil
	}
	if err != nil {
		return Requirement{}, err
	}
	return s.requirement(since, &joined, enabled), nil
}

// MemberStatus is one member's two-factor sign-in, for the owner's team page.
type MemberStatus struct {
	UserID      uuid.UUID  `json:"user_id"`
	Email       string     `json:"email"`
	Enabled     bool       `json:"two_factor_enabled"`
	GraceEndsAt *time.Time `json:"grace_ends_at,omitempty"`
	Blocked     bool       `json:"blocked"`
}

// Members lists every member's two-factor status in the organization.
func (s *Service) Members(ctx context.Context, orgID uuid.UUID) ([]MemberStatus, error) {
	rows, err := s.pool.Query(ctx, `SELECT u.id, u.email, o.require_two_factor_since, m.created_at, u.totp_enabled_at
		FROM organization_members m JOIN organizations o ON o.id = m.organization_id JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 ORDER BY m.created_at, u.email`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemberStatus{}
	for rows.Next() {
		var m MemberStatus
		var since, enabled *time.Time
		var joined time.Time
		if err := rows.Scan(&m.UserID, &m.Email, &since, &joined, &enabled); err != nil {
			return nil, err
		}
		r := s.requirement(since, &joined, enabled)
		m.Enabled, m.GraceEndsAt, m.Blocked = enabled != nil, r.GraceEndsAt, r.Blocked
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetRequired turns the organization's requirement on or off. Only the owner can, and only once they use two-factor sign-in themselves. Turning
// it on emails every member who doesn't use it yet, with the date they have until.
func (s *Service) SetRequired(ctx context.Context, ownerID, orgID uuid.UUID, on bool) error {
	var role, orgName string
	var enabled *time.Time
	err := s.pool.QueryRow(ctx, `SELECT m.role, o.name, u.totp_enabled_at FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id JOIN users u ON u.id = m.user_id WHERE m.user_id = $1 AND m.organization_id = $2`,
		ownerID, orgID).Scan(&role, &orgName, &enabled)
	if err != nil {
		return err
	}
	if role != "owner" {
		return ErrNotOwner
	}
	if !on {
		_, err := s.pool.Exec(ctx, `UPDATE organizations SET require_two_factor_since = NULL WHERE id = $1`, orgID)
		return err
	}
	if enabled == nil {
		return ErrOwnerNeedsTwoFactor
	}
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET require_two_factor_since = $2 WHERE id = $1 AND require_two_factor_since IS NULL`, orgID, s.now())
	if err != nil || tag.RowsAffected() == 0 {
		return err // already on: nothing changes, nobody is emailed again
	}
	members, err := s.Members(ctx, orgID)
	if err != nil {
		return err
	}
	for _, m := range members {
		if m.Enabled || m.GraceEndsAt == nil {
			continue
		}
		c := email.Content{
			Preheader: orgName + " now requires two-factor sign-in.", Heading: "Set up two-factor sign-in",
			Paragraphs: []string{fmt.Sprintf("The owner of %s on HawkEye now requires everyone in it to use two-factor sign-in with an authenticator app. "+
				"Set it up by %s to keep access to %s. Your API keys for it stop working after that date too, until you do.",
				orgName, m.GraceEndsAt.UTC().Format("January 2, 2006"), orgName)},
			ButtonLabel: "Set up two-factor sign-in", ButtonURL: s.appBaseURL + "/account#two-factor", AppBaseURL: s.appBaseURL,
			Note: "It takes a minute: scan a QR code with an authenticator app, then type the code it shows.",
		}
		if err := s.mail.Send(ctx, c.Message("", m.Email, orgName+" requires two-factor sign-in on HawkEye")); err != nil {
			s.log.ErrorContext(ctx, "send two-factor requirement email failed", "organization_id", orgID, "error", err)
		}
	}
	return nil
}

func (s *Service) notify(ctx context.Context, to, preheader, heading, body, note string) {
	c := email.Content{Preheader: preheader + ".", Heading: heading, Paragraphs: []string{body}, Tone: email.ToneAlert,
		ButtonLabel: "Review your account", ButtonURL: s.appBaseURL + "/account#two-factor", Note: note, AppBaseURL: s.appBaseURL}
	if err := s.mail.Send(ctx, c.Message("", to, "HawkEye: "+strings.ToLower(heading[:1])+heading[1:])); err != nil {
		s.log.ErrorContext(ctx, "send two-factor email failed", "error", err)
	}
}
