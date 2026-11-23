package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"hawkeye/internal/attribution"
	"hawkeye/internal/audit"
	"hawkeye/internal/email"
	"hawkeye/internal/socialauth"
)

// Signing in with Google and GitHub (docs/plan-social-sign-in.md). It is an addition to email and password, which work as before; one account can
// use any mix of the three.

// Social sign-in intents: sign in or create an account; connect a provider to the signed-in account (settings); confirm it's the signed-in
// person before a sensitive action, for an account without a password.
const (
	IntentSignIn  = "signin"
	IntentConnect = "connect"
	IntentConfirm = "confirm"
)

const (
	socialStateTTL = 10 * time.Minute
	// ConfirmWindow is how long a confirmation through the provider covers sensitive actions.
	ConfirmWindow = 10 * time.Minute
)

// Social sign-in errors. Each has a short code the sign-in page turns into a sentence (see SocialErrorCode).
var (
	ErrSocialState         = errors.New("sign-in expired or was started in another browser")
	ErrSocialEmail         = errors.New("the provider did not vouch for an email address")
	ErrSocialDisabled      = errors.New("account disabled")
	ErrIdentityInUse       = errors.New("this Google or GitHub account is connected to another HawkEye account")
	ErrProviderConnected   = errors.New("another account from this provider is already connected")
	ErrSocialWrongAccount  = errors.New("that is not the account connected to this HawkEye account")
	ErrLastSignInMethod    = errors.New("the last way to sign in can't be removed")
	ErrIdentityNotFound    = errors.New("not connected")
	ErrConfirmationNeeded  = errors.New("confirm it's you first")
	ErrPasswordAlreadySet  = errors.New("this account already has a password")
	errUnknownSocialIntent = errors.New("unknown intent")
)

// SocialErrorCode is the short code for err shown on the sign-in page (?error=...), or "failed" for anything else.
func SocialErrorCode(err error) string {
	switch {
	case errors.Is(err, socialauth.ErrDenied):
		return "canceled"
	case errors.Is(err, ErrSocialState):
		return "expired"
	case errors.Is(err, ErrSocialEmail):
		return "email_not_verified"
	case errors.Is(err, ErrSocialDisabled):
		return "disabled"
	case errors.Is(err, ErrIdentityInUse):
		return "identity_in_use"
	case errors.Is(err, ErrProviderConnected):
		return "provider_connected"
	case errors.Is(err, ErrSocialWrongAccount):
		return "wrong_account"
	}
	return "failed"
}

// SocialStart is a sign-in about to be sent to a provider.
type SocialStart struct {
	Intent      string
	UserID      *uuid.UUID // connect and confirm: the signed-in person
	Next        string     // where to go afterward: a path inside the app, checked by SafeNext
	Attribution *attribution.Attribution
}

// SafeNext keeps only a path inside the app ("/monitors?x=1"), without its fragment; anything that could leave the site (an absolute address, "//host", a backslash)
// becomes "".
func SafeNext(p string) string {
	p, _, _ = strings.Cut(p, "#") // the callback adds its own fragment
	if len(p) > 512 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n\t") {
		return ""
	}
	return p
}

// BeginSocial records a sign-in about to start and returns the provider's address to send the browser to, and the state the browser keeps
// (in a cookie) to finish it. Expired states are tidied away on the way.
func (s *Service) BeginSocial(ctx context.Context, p socialauth.Provider, st SocialStart) (redirect, state string, err error) {
	switch st.Intent {
	case IntentSignIn:
		st.UserID = nil
	case IntentConnect, IntentConfirm:
		if st.UserID == nil {
			return "", "", ErrUnauthenticated
		}
		st.Attribution = nil
	default:
		return "", "", errUnknownSocialIntent
	}
	state = socialauth.RandomString(32)
	nonce := socialauth.RandomString(16)
	verifier, challenge := socialauth.NewPKCE()
	if _, err := s.pool.Exec(ctx, `DELETE FROM social_auth_states WHERE expires_at < now()`); err != nil {
		return "", "", err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO social_auth_states (state_hash, provider, intent, user_id, pkce_verifier, nonce, next_path, attribution, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now() + $9::interval)`,
		HashToken(state), p.Name(), st.Intent, st.UserID, verifier, nonce, SafeNext(st.Next), st.Attribution, socialStateTTL); err != nil {
		return "", "", fmt.Errorf("store sign-in state: %w", err)
	}
	return p.AuthURL(state, challenge, nonce, st.Intent == IntentConfirm), state, nil
}

// SocialResult is how a social sign-in ended.
type SocialResult struct {
	Intent   string
	Provider string
	User     User
	Session  string // signin: the new session ("" when two-factor is needed: FinishSocial returned *SecondFactorRequired)
	Created  bool   // signin: a new account was created
	Next     string
}

// FinishSocial completes a sign-in when the provider sends the browser back. state is the one in the callback's query, which must equal
// cookieState (the browser that started it). sessionToken is the browser's current session, for confirm. For an account with two-factor on, the
// error is *SecondFactorRequired (the result still says where to go next).
func (s *Service) FinishSocial(ctx context.Context, p socialauth.Provider, state, cookieState, code, sessionToken string) (SocialResult, error) {
	if state == "" || state != cookieState {
		return SocialResult{}, ErrSocialState
	}
	var (
		intent, verifier, nonce, next string
		userID                        *uuid.UUID
		attr                          *attribution.Attribution
	)
	err := s.pool.QueryRow(ctx, `DELETE FROM social_auth_states WHERE state_hash = $1 AND provider = $2 AND expires_at > now()
		RETURNING intent, user_id, pkce_verifier, nonce, next_path, attribution`, HashToken(state), p.Name()).
		Scan(&intent, &userID, &verifier, &nonce, &next, &attr)
	if errors.Is(err, pgx.ErrNoRows) {
		return SocialResult{}, ErrSocialState
	}
	if err != nil {
		return SocialResult{}, err
	}
	res := SocialResult{Intent: intent, Provider: p.Name(), Next: next}
	id, err := p.Exchange(ctx, code, verifier, nonce)
	if err != nil {
		return res, err
	}

	switch intent {
	case IntentConfirm:
		return res, s.confirmWith(ctx, id, *userID, sessionToken)
	case IntentConnect:
		res.User, err = s.connect(ctx, id, *userID)
		return res, err
	}
	res.User, res.Created, err = s.signInWith(ctx, id, attr)
	if err != nil {
		return res, err
	}
	if s.twoFactorOn(ctx, res.User.ID) {
		return res, s.challenge(ctx, res.User.ID, id.Provider)
	}
	if res.Session, err = s.createSession(ctx, res.User.ID); err != nil {
		return res, err
	}
	s.record(ctx, audit.SignedIn, audit.User(res.User.ID, res.User.Email), res.User.ID, map[string]any{"method": id.Provider})
	return res, nil
}

func (s *Service) twoFactorOn(ctx context.Context, userID uuid.UUID) bool {
	var on bool
	_ = s.pool.QueryRow(ctx, `SELECT totp_enabled_at IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&on)
	return on
}

// signInWith finds the account for an identity, linking or creating one when needed:
//   - an identity already connected signs in to its account;
//   - otherwise the provider must vouch for the email address. An account with that address gets the identity connected (and is told by
//     email); a new address gets a new account, verified and without a password.
//
// Linking to an account whose address was never verified also wipes what its unknown registrant set up (password, two-factor, sessions, API keys
// and connected apps): the provider has just shown who really holds the address, and a stranger who registered it first must not keep a way in.
func (s *Service) signInWith(ctx context.Context, id socialauth.Identity, attr *attribution.Attribution) (u User, created bool, err error) {
	var disabled *time.Time
	// The provider's address is refreshed for settings; the account's own address never changes because of it.
	err = s.pool.QueryRow(ctx, `UPDATE user_identities i SET last_used_at = now(), email = COALESCE(NULLIF($3, ''), i.email) FROM users u
		WHERE i.provider = $1 AND i.subject = $2 AND u.id = i.user_id RETURNING `+prefixed("u", userCols)+`, u.disabled_at`, id.Provider, id.Subject, id.Email).
		Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.CreatedAt, &disabled)
	switch {
	case err == nil && disabled != nil:
		return User{}, false, ErrSocialDisabled
	case err == nil:
		return u, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return User{}, false, err
	}
	if !id.EmailVerified || id.Email == "" {
		return User{}, false, ErrSocialEmail
	}
	addr, err := NormalizeEmail(id.Email)
	if err != nil {
		return User{}, false, ErrSocialEmail
	}

	wiped := false
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT `+userCols+`, disabled_at FROM users WHERE email = $1 FOR UPDATE`, addr).
			Scan(&u.ID, &u.Email, &u.EmailVerifiedAt, &u.CreatedAt, &disabled)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if u, err = createAccount(ctx, tx, addr, nil, true, attr); err != nil {
				return err
			}
			created = true
		case err != nil:
			return err
		case disabled != nil:
			return ErrSocialDisabled
		case u.EmailVerifiedAt == nil:
			if err := wipeUnverified(ctx, tx, u.ID); err != nil {
				return err
			}
			wiped = true
			fallthrough
		default:
			if err := tx.QueryRow(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1 RETURNING email_verified_at`,
				u.ID).Scan(&u.EmailVerifiedAt); err != nil {
				return err
			}
			if err := activateOwnAlertAddress(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO user_identities (user_id, provider, subject, email) VALUES ($1, $2, $3, $4)`,
			u.ID, id.Provider, id.Subject, id.Email)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrSocialDisabled) {
			return User{}, false, err
		}
		if isUniqueViolation(err, "user_identities_user_id_provider_key") {
			// The account already has a different account from this provider connected.
			return User{}, false, ErrProviderConnected
		}
		return User{}, false, fmt.Errorf("social sign-in: %w", err)
	}
	if created {
		s.record(ctx, audit.SignedUp, audit.User(u.ID, u.Email), u.ID, map[string]any{"method": id.Provider})
		return u, true, nil
	}
	details := map[string]any{"provider": id.Provider}
	if wiped {
		details["unverified_account_reset"] = true
	}
	s.record(ctx, audit.IdentityConnected, audit.User(u.ID, u.Email), u.ID, details)
	s.sendIdentityEmail(ctx, u.Email, id.Provider, true)
	return u, false, nil
}

// wipeUnverified removes every way into an account whose address was never verified, before connecting the provider that proved who holds it.
func wipeUnverified(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	for _, q := range []string{
		`UPDATE users SET password_hash = NULL, totp_secret = NULL, totp_pending_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE id = $1`,
		`DELETE FROM recovery_codes WHERE user_id = $1`,
		`DELETE FROM sessions WHERE user_id = $1`,
		`DELETE FROM login_challenges WHERE user_id = $1`,
		`DELETE FROM user_identities WHERE user_id = $1`,
		`UPDATE api_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		`UPDATE oauth_grants SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
	} {
		if _, err := tx.Exec(ctx, q, userID); err != nil {
			return err
		}
	}
	return nil
}

// connect adds an identity to the signed-in account (settings → Sign-in methods). The provider's email may differ from the account's: it isn't
// used to find anyone, only shown.
func (s *Service) connect(ctx context.Context, id socialauth.Identity, userID uuid.UUID) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1 AND disabled_at IS NULL`, userID))
	if err != nil {
		return User{}, ErrUnauthenticated
	}
	var owner uuid.UUID
	err = s.pool.QueryRow(ctx, `SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2`, id.Provider, id.Subject).Scan(&owner)
	switch {
	case err == nil && owner == userID:
		return u, nil // already connected: nothing to do
	case err == nil:
		return User{}, ErrIdentityInUse
	case !errors.Is(err, pgx.ErrNoRows):
		return User{}, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO user_identities (user_id, provider, subject, email) VALUES ($1, $2, $3, $4)`,
		userID, id.Provider, id.Subject, id.Email); err != nil {
		switch {
		case isUniqueViolation(err, "user_identities_user_id_provider_key"):
			return User{}, ErrProviderConnected
		case isUniqueViolation(err, "user_identities_pkey"):
			return User{}, ErrIdentityInUse
		}
		return User{}, err
	}
	s.record(ctx, audit.IdentityConnected, audit.User(u.ID, u.Email), u.ID, map[string]any{"provider": id.Provider})
	s.sendIdentityEmail(ctx, u.Email, id.Provider, true)
	return u, nil
}

// confirmWith marks the session as confirmed when the identity is one connected to its account.
func (s *Service) confirmWith(ctx context.Context, id socialauth.Identity, userID uuid.UUID, sessionToken string) error {
	var owner uuid.UUID
	err := s.pool.QueryRow(ctx, `UPDATE user_identities SET last_used_at = now() WHERE provider = $1 AND subject = $2 RETURNING user_id`,
		id.Provider, id.Subject).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && owner != userID {
		return ErrSocialWrongAccount
	}
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE sessions SET confirmed_at = now() WHERE token_hash = $1 AND user_id = $2 AND expires_at > now()`,
		HashToken(sessionToken), userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUnauthenticated
	}
	return nil
}

// Reauthenticate confirms it's really the signed-in person before a sensitive action: their password, or, for an account without one, a
// confirmation through Google or GitHub in this session within ConfirmWindow (ErrConfirmationNeeded otherwise).
func (s *Service) Reauthenticate(ctx context.Context, userID uuid.UUID, sessionToken, password string) error {
	has, err := s.HasPassword(ctx, userID)
	if err != nil {
		return err
	}
	if has {
		return s.VerifyPassword(ctx, userID, password)
	}
	var ok bool
	err = s.pool.QueryRow(ctx, `SELECT COALESCE(confirmed_at > now() - $3::interval, false) FROM sessions WHERE token_hash = $1 AND user_id = $2`,
		HashToken(sessionToken), userID, ConfirmWindow).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !ok {
		return ErrConfirmationNeeded
	}
	return err
}

// HasPassword reports whether the account has a password (accounts created with Google or GitHub don't, until one is set).
func (s *Service) HasPassword(ctx context.Context, userID uuid.UUID) (bool, error) {
	var has bool
	err := s.pool.QueryRow(ctx, `SELECT password_hash IS NOT NULL FROM users WHERE id = $1`, userID).Scan(&has)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrUnauthenticated
	}
	return has, err
}

// IdentityInfo is a connected provider as settings show it.
type IdentityInfo struct {
	Provider    string    `json:"provider"`
	Email       string    `json:"email"`
	ConnectedAt time.Time `json:"connected_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

// SignInMethods lists how the account can sign in: whether it has a password, and its connected providers.
func (s *Service) SignInMethods(ctx context.Context, userID uuid.UUID) (hasPassword bool, ids []IdentityInfo, err error) {
	if hasPassword, err = s.HasPassword(ctx, userID); err != nil {
		return false, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT provider, email, created_at, last_used_at FROM user_identities WHERE user_id = $1 ORDER BY provider`, userID)
	if err != nil {
		return false, nil, err
	}
	ids, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (IdentityInfo, error) {
		var i IdentityInfo
		err := r.Scan(&i.Provider, &i.Email, &i.ConnectedAt, &i.LastUsedAt)
		return i, err
	})
	if ids == nil {
		ids = []IdentityInfo{} // a list in JSON, even when empty
	}
	return hasPassword, ids, err
}

// Disconnect removes a provider from the account, unless it is the last way to sign in.
func (s *Service) Disconnect(ctx context.Context, u User, provider string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var hasPassword bool
		var others int
		if err := tx.QueryRow(ctx, `SELECT u.password_hash IS NOT NULL, (SELECT count(*) FROM user_identities WHERE user_id = u.id AND provider <> $2)
			FROM users u WHERE u.id = $1 FOR UPDATE`, u.ID, provider).Scan(&hasPassword, &others); err != nil {
			return err
		}
		if !hasPassword && others == 0 {
			return ErrLastSignInMethod
		}
		tag, err := tx.Exec(ctx, `DELETE FROM user_identities WHERE user_id = $1 AND provider = $2`, u.ID, provider)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrIdentityNotFound
		}
		return err
	})
	if err != nil {
		return err
	}
	s.record(ctx, audit.IdentityDisconnected, audit.User(u.ID, u.Email), u.ID, map[string]any{"provider": provider})
	s.sendIdentityEmail(ctx, u.Email, provider, false)
	return nil
}

// SetPassword gives a password to an account that has none, after the person confirmed it's them (Reauthenticate). Changing an existing password
// goes through ChangePassword.
func (s *Service) SetPassword(ctx context.Context, u User, sessionToken, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	has, err := s.HasPassword(ctx, u.ID)
	if err != nil {
		return err
	}
	if has {
		return ErrPasswordAlreadySet
	}
	if err := s.Reauthenticate(ctx, u.ID, sessionToken, ""); err != nil {
		return err
	}
	hash, err := s.cfg.Hasher.Hash(password)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1 AND password_hash IS NULL`, u.ID, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPasswordAlreadySet
	}
	s.record(ctx, audit.PasswordChanged, audit.User(u.ID, u.Email), u.ID, map[string]any{"first_password": true})
	return nil
}

func providerName(p string) string {
	if p == socialauth.GitHub {
		return "GitHub"
	}
	return "Google"
}

// sendIdentityEmail tells the account's owner a provider was connected or disconnected, so an unexpected change is noticed.
func (s *Service) sendIdentityEmail(ctx context.Context, to, provider string, connected bool) {
	name := providerName(provider)
	verb, past := "connected to", "connected"
	if !connected {
		verb, past = "disconnected from", "disconnected"
	}
	c := email.Content{
		Preheader: fmt.Sprintf("%s was %s your HawkEye account.", name, verb), Heading: fmt.Sprintf("%s %s", name, past),
		Paragraphs:  []string{fmt.Sprintf("A %s account was just %s your HawkEye account.", name, verb)},
		ButtonLabel: "Review sign-in methods", ButtonURL: s.cfg.AppBaseURL + "/account#sign-in-methods", Tone: email.ToneAlert,
		Note: "If this was you, no action is needed. If it was not, reset your password and review your sign-in methods.", AppBaseURL: s.cfg.AppBaseURL,
	}
	subject := fmt.Sprintf("%s was %s your HawkEye account", name, verb)
	if err := s.mail.Send(ctx, c.Message("", to, subject)); err != nil {
		s.log.ErrorContext(ctx, "send sign-in method email failed", "error", err)
	}
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
