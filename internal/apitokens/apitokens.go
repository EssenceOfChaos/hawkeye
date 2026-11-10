// Package apitokens implements API keys: bearer tokens that let scripts, dashboards and other tools use the public API without a browser session.
//
// A key belongs to one organization and one person, and has one of two scopes: read, or write (which also reads). It never does more than its owner
// can: the person's role is read again on every request, and the key is deleted with their membership (a foreign key), so someone who leaves or is
// removed takes their keys with them. Changing data needs a plan that includes it (plans.CanWriteViaAPI), checked on every write, not only when the
// key was made. Only a SHA-256 hash of each token is stored; the token is shown once. Keys expire after 90 days unless their owner picks another date
// or chooses no expiry, and the worker emails the owner before and when a key expires (ExpiryJob). See docs/plan-api-mcp.md and docs/security.md.
package apitokens

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/audit"
	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/twofactor"
	"hawkeye/internal/validation"
)

const (
	ScopeRead  = "read"
	ScopeWrite = "write"

	KindKey   = "key"          // an API key a person made
	KindOAuth = "oauth_access" // an access token issued to an OAuth client (internal/oauth)

	// TokenPrefix starts every key, so leaked keys are easy to recognize (in logs, and by secret scanners).
	TokenPrefix = "hk_"
	// MaxLiveKeys caps an organization's keys that are not revoked or expired.
	MaxLiveKeys = 25
	// DefaultLifetime is how long a key lasts when its owner doesn't choose.
	DefaultLifetime = 90 * 24 * time.Hour
	// MaxLifetime is the latest expiry date a key can have (no expiry is a separate, explicit choice).
	MaxLifetime = 366 * 24 * time.Hour
	// minLifetime keeps people from making a key that is about to stop working.
	minLifetime = time.Hour

	shownPrefixLen  = 10 // "hk_" and the first 7 characters of the random part
	lastUsedRefresh = time.Minute
)

var (
	// ErrInvalid covers unknown, revoked and malformed tokens and tokens whose owner has left the organization or been disabled.
	ErrInvalid = errors.New("invalid API key")
	// ErrExpired is a real key past its expiry date, reported separately so a broken script can say why.
	ErrExpired  = errors.New("API key expired")
	ErrNotFound = errors.New("API key not found")
	// ErrTwoFactorRequired: the key's organization requires two-factor sign-in, its owner hasn't set it up, and their grace period is over.
	ErrTwoFactorRequired = errors.New("two-factor sign-in required")
	ErrTooMany           = errors.New("too many API keys")
)

// Key is a key as listed in the app. The token itself is never part of it.
type Key struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	Prefix     string     `json:"prefix"`
	OwnerEmail string     `json:"owner_email"`
	Mine       bool       `json:"mine"` // made by the person asking
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	Expired    bool       `json:"expired"`
}

// Principal is who a request made with a key acts as.
type Principal struct {
	TokenID        uuid.UUID
	Kind           string    // KindKey, or KindOAuth for an access token issued to an OAuth client (accepted only by the MCP server)
	GrantID        uuid.UUID // the OAuth grant, for KindOAuth
	OrganizationID uuid.UUID
	User           auth.User
	Role           string // the owner's role in the organization now
	Scope          string
	Name           string // the key's name, or the connected app's for KindOAuth
}

// Actor is who the security log says acted: the key (or connected app), for the person it belongs to. A connected app is named by its grant, which
// outlives the hourly access tokens.
func (p Principal) Actor() audit.Actor {
	if p.Kind == KindOAuth {
		return audit.Actor{Type: audit.ActorOAuthApp, UserID: p.User.ID, Email: p.User.Email, CredentialID: p.GrantID, CredentialName: p.Name}
	}
	return audit.Actor{Type: audit.ActorAPIKey, UserID: p.User.ID, Email: p.User.Email, CredentialID: p.TokenID, CredentialName: p.Name}
}

// CanWrite reports whether the key's scope allows changing data. The plan is checked separately (plans.CanWriteViaAPI).
func (p Principal) CanWrite() bool { return p.Scope == ScopeWrite }

// Service creates, lists, revokes and authenticates keys.
type Service struct {
	pool       *pgxpool.Pool
	plans      *plans.Service
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, planSvc *plans.Service, mail email.Sender, log *slog.Logger, appBaseURL string) *Service {
	return &Service{pool: pool, plans: planSvc, mail: mail, log: log, appBaseURL: appBaseURL, now: time.Now}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// NewInput is what a person chooses when making a key. With neither ExpiresAt nor NoExpiry the key lasts DefaultLifetime.
type NewInput struct {
	Name      string
	Scope     string
	ExpiresAt *time.Time
	NoExpiry  bool
}

func (in NewInput) validate(now time.Time) (name string, expires *time.Time, err error) {
	fields := map[string]string{}
	name = strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
		fields["name"] = "must be between 1 and 100 characters"
	}
	if in.Scope != ScopeRead && in.Scope != ScopeWrite {
		fields["scope"] = `must be "read" or "write"`
	}
	switch {
	case in.NoExpiry && in.ExpiresAt != nil:
		fields["expires_at"] = "leave this out when no_expiry is true"
	case in.NoExpiry:
	case in.ExpiresAt != nil:
		at := in.ExpiresAt.UTC()
		if at.Before(now.Add(minLifetime)) {
			fields["expires_at"] = "must be at least an hour from now"
		} else if at.After(now.Add(MaxLifetime)) {
			fields["expires_at"] = "must be within a year from now (or choose no expiry)"
		}
		expires = &at
	default:
		at := now.Add(DefaultLifetime).UTC()
		expires = &at
	}
	if len(fields) > 0 {
		return "", nil, &validation.Error{Fields: fields}
	}
	return name, expires, nil
}

// Create makes a key for the person in the organization and returns it with its token, which is never available again. A write key needs a plan
// that includes API writes. The owner of the key is emailed, so a key they didn't make is noticed.
func (s *Service) Create(ctx context.Context, u auth.User, orgID uuid.UUID, in NewInput) (Key, string, error) {
	now := s.now()
	name, expires, err := in.validate(now)
	if err != nil {
		return Key{}, "", err
	}
	if in.Scope == ScopeWrite {
		if err := s.plans.CanWriteViaAPI(ctx, s.pool, orgID); err != nil {
			return Key{}, "", err
		}
	}
	raw, err := newToken()
	if err != nil {
		return Key{}, "", err
	}
	k := Key{Name: name, Scope: in.Scope, Prefix: raw[:shownPrefixLen], OwnerEmail: u.Email, Mine: true, ExpiresAt: expires}
	var orgName string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize key creation per organization so the cap holds under concurrent requests.
		if err := tx.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&orgName); err != nil {
			return err
		}
		var live int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM api_tokens WHERE organization_id = $1 AND kind = 'key' AND revoked_at IS NULL
			AND (expires_at IS NULL OR expires_at > $2)`, orgID, now).Scan(&live); err != nil {
			return err
		}
		if live >= MaxLiveKeys {
			return ErrTooMany
		}
		return tx.QueryRow(ctx, `INSERT INTO api_tokens (organization_id, user_id, name, scope, prefix, token_hash, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`,
			orgID, u.ID, name, in.Scope, k.Prefix, auth.HashToken(raw), now, expires).Scan(&k.ID, &k.CreatedAt)
	})
	if err != nil {
		if errors.Is(err, ErrTooMany) {
			return Key{}, "", err
		}
		return Key{}, "", fmt.Errorf("create API key: %w", err)
	}
	s.sendCreated(ctx, u.Email, orgName, k)
	return k, raw, nil
}

// List returns the organization's keys that are not revoked, newest first. An owner sees everyone's keys (to revoke any of them); a member sees
// only their own.
func (s *Service) List(ctx context.Context, orgID, userID uuid.UUID, role string) ([]Key, error) {
	rows, err := s.pool.Query(ctx, `SELECT t.id, t.name, t.scope, t.prefix, u.email, t.user_id = $2, t.created_at, t.last_used_at, t.expires_at,
			t.expires_at IS NOT NULL AND t.expires_at <= $4
		FROM api_tokens t JOIN users u ON u.id = t.user_id
		WHERE t.organization_id = $1 AND t.kind = 'key' AND t.revoked_at IS NULL AND ($3 OR t.user_id = $2)
		ORDER BY t.created_at DESC, t.id`, orgID, userID, role == organizations.RoleOwner, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Name, &k.Scope, &k.Prefix, &k.OwnerEmail, &k.Mine, &k.CreatedAt, &k.LastUsedAt, &k.ExpiresAt, &k.Expired); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke stops a key at once. People revoke their own keys; an owner can revoke anyone's in the organization.
func (s *Service) Revoke(ctx context.Context, orgID, keyID, userID uuid.UUID, role string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE api_tokens SET revoked_at = now()
		WHERE id = $1 AND organization_id = $2 AND kind = 'key' AND revoked_at IS NULL AND ($4 OR user_id = $3)`, keyID, orgID, userID, role == organizations.RoleOwner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LooksLikeToken reports whether a bearer credential is meant to be an API key (as opposed to something else sent in the header).
func LooksLikeToken(raw string) bool { return strings.HasPrefix(raw, TokenPrefix) }

// Authenticate resolves a token to the person, organization, current role and scope it acts as.
func (s *Service) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if !LooksLikeToken(raw) || len(raw) > 200 {
		return Principal{}, ErrInvalid
	}
	hash := auth.HashToken(raw)
	var (
		p         Principal
		expiresAt *time.Time
		revoked   *time.Time
		lastUsed  *time.Time
		blocked   bool
		grantID   *uuid.UUID
		grantOff  bool
	)
	// blocked: the organization requires two-factor sign-in and the key's owner is past their grace period without it (internal/twofactor).
	err := s.pool.QueryRow(ctx, `SELECT t.id, t.kind, t.name, t.oauth_grant_id, COALESCE(g.revoked_at IS NOT NULL, false),
			t.organization_id, t.scope, t.expires_at, t.revoked_at, t.last_used_at, m.role,
			u.id, u.email, u.email_verified_at, u.created_at,
			o.require_two_factor_since IS NOT NULL AND u.totp_enabled_at IS NULL
				AND GREATEST(o.require_two_factor_since, m.created_at) + $2::interval <= now()
		FROM api_tokens t
		JOIN organization_members m ON m.organization_id = t.organization_id AND m.user_id = t.user_id
		JOIN organizations o ON o.id = t.organization_id
		JOIN users u ON u.id = t.user_id
		LEFT JOIN oauth_grants g ON g.id = t.oauth_grant_id
		WHERE t.token_hash = $1 AND u.disabled_at IS NULL`, hash, twofactor.GracePeriod).
		Scan(&p.TokenID, &p.Kind, &p.Name, &grantID, &grantOff, &p.OrganizationID, &p.Scope, &expiresAt, &revoked, &lastUsed, &p.Role, &p.User.ID, &p.User.Email, &p.User.EmailVerifiedAt, &p.User.CreatedAt,
			&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, ErrInvalid
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate API key: %w", err)
	}
	now := s.now()
	if revoked != nil || grantOff {
		return Principal{}, ErrInvalid
	}
	if grantID != nil {
		p.GrantID = *grantID
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return Principal{}, ErrExpired
	}
	if blocked {
		return Principal{}, ErrTwoFactorRequired
	}
	// Best effort, and at most once a minute per key, so using a key doesn't add a write to every request.
	if lastUsed == nil || now.Sub(*lastUsed) >= lastUsedRefresh {
		if _, err := s.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = $2 WHERE id = $1`, p.TokenID, now); err != nil {
			s.log.WarnContext(ctx, "could not record API key use", "token_id", p.TokenID, "error", err)
		}
	}
	return p, nil
}

// newToken returns "hk_" followed by 256 random bits, URL-safe.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func describeExpiry(at *time.Time) string {
	if at == nil {
		return "Never"
	}
	return at.UTC().Format("January 2, 2006")
}

func scopeLabel(scope string) string {
	if scope == ScopeWrite {
		return "Read and write"
	}
	return "Read only"
}

func (s *Service) keysURL() string { return s.appBaseURL + "/api-keys" }

func (s *Service) sendCreated(ctx context.Context, to, orgName string, k Key) {
	c := email.Content{
		Preheader: "A new API key was created for your HawkEye account.", Heading: "A new API key was created",
		Paragraphs: []string{fmt.Sprintf("An API key was just created for your account in %s. Anyone with the key can use HawkEye's API as you, "+
			"within the key's access.", orgName)},
		Facts: []email.Fact{{Label: "Key", Value: k.Name + " (" + k.Prefix + "…)"}, {Label: "Access", Value: scopeLabel(k.Scope)},
			{Label: "Expires", Value: describeExpiry(k.ExpiresAt)}},
		ButtonLabel: "Review your API keys", ButtonURL: s.keysURL(), Tone: email.ToneAlert,
		Note: "If this was you, no action is needed. If it was not, revoke the key and change your password.", AppBaseURL: s.appBaseURL,
	}
	if err := s.mail.Send(ctx, c.Message("", to, "A new HawkEye API key was created")); err != nil {
		s.log.ErrorContext(ctx, "send API key created email failed", "token_id", k.ID, "error", err)
	}
}

// IssueForGrant creates an access token for an OAuth grant (internal/oauth), inside the caller's transaction. It belongs to the grant's person and
// organization like an API key, but is short-lived, accepted only by the MCP server, and never listed with the person's keys.
func IssueForGrant(ctx context.Context, tx pgx.Tx, orgID, userID, grantID uuid.UUID, clientName, scope string, expiresAt time.Time) (string, error) {
	raw, err := newToken()
	if err != nil {
		return "", err
	}
	name := clientName
	if utf8.RuneCountInString(name) > 100 {
		name = string([]rune(name)[:100])
	}
	_, err = tx.Exec(ctx, `INSERT INTO api_tokens (organization_id, user_id, kind, name, scope, prefix, token_hash, expires_at, oauth_grant_id)
		VALUES ($1, $2, 'oauth_access', $3, $4, $5, $6, $7, $8)`, orgID, userID, name, scope, raw[:shownPrefixLen], auth.HashToken(raw), expiresAt, grantID)
	if err != nil {
		return "", fmt.Errorf("issue OAuth access token: %w", err)
	}
	return raw, nil
}
