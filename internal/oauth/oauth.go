// Package oauth is HawkEye's OAuth 2.1 authorization server for the MCP server (docs/plan-api-mcp.md, Phase 3b): it lets Claude and other MCP
// clients connect to someone's organization without an API key being copied by hand.
//
// Only what MCP clients need: the authorization code grant with PKCE (S256, required), rotating refresh tokens, and revocation (RFC 7009), with
// discovery metadata (RFC 8414, RFC 9728) served by internal/httpapi. Clients are registered by an operator (cmd/oauthclient); there is no dynamic
// registration. A grant is one connection a person approved: the client acts as them, in one organization they choose, with read or read and write
// access. Access tokens are API tokens of their own kind (internal/apitokens): an hour long and accepted only by the MCP server. Refresh tokens last
// 30 days and rotate; presenting one twice revokes the grant, since that means it leaked. Revoking a grant ends everything issued under it.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/twofactor"
)

const (
	AccessTokenTTL  = time.Hour
	RefreshTokenTTL = 30 * 24 * time.Hour
	CodeTTL         = time.Minute

	refreshPrefix = "hkr_"
)

// Error is an OAuth error response (RFC 6749 §5.2 and §4.1.2.1): Code is the standard error code; Description is safe to show.
type Error struct {
	Code        string
	Description string
	Status      int
}

func (e *Error) Error() string { return e.Code + ": " + e.Description }

func oerr(status int, code, desc string) *Error {
	return &Error{Code: code, Description: desc, Status: status}
}

var (
	ErrUnknownClient   = oerr(400, "invalid_client", "Unknown or disabled client.")
	ErrBadRedirect     = oerr(400, "invalid_request", "The redirect_uri is not registered for this client.")
	ErrClientAuth      = oerr(401, "invalid_client", "Client authentication failed.")
	ErrGrantNotFound   = errors.New("connection not found")
	ErrOrganization    = errors.New("not a member of that organization")
	ErrWriteNotAllowed = errors.New("the plan doesn't include changes through the API")
)

// Service runs the authorization server.
type Service struct {
	pool      *pgxpool.Pool
	plans     *plans.Service
	twoFactor *twofactor.Service
	mail      email.Sender
	log       *slog.Logger
	issuer    string // the app's public address, e.g. https://hawkeye.observer
	now       func() time.Time
}

func NewService(pool *pgxpool.Pool, planSvc *plans.Service, tf *twofactor.Service, mail email.Sender, log *slog.Logger, issuer string) *Service {
	return &Service{pool: pool, plans: planSvc, twoFactor: tf, mail: mail, log: log, issuer: strings.TrimRight(issuer, "/"), now: time.Now}
}

// Issuer is the authorization server's identifier (and the app's address).
func (s *Service) Issuer() string { return s.issuer }

// Resource is the protected resource tokens are for: the MCP server.
func (s *Service) Resource() string { return s.issuer + "/mcp" }

func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// ---- Clients.

// Client is a registered OAuth client.
type Client struct {
	ID           string   `json:"client_id"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	Confidential bool     `json:"confidential"` // authenticates with a secret at the token endpoint
}

func (s *Service) client(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (Client, []byte, error) {
	var c Client
	var secret []byte
	err := q.QueryRow(ctx, `SELECT id, name, redirect_uris, secret_hash FROM oauth_clients WHERE id = $1 AND disabled_at IS NULL`, id).
		Scan(&c.ID, &c.Name, &c.RedirectURIs, &secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, nil, ErrUnknownClient
	}
	c.Confidential = secret != nil
	return c, secret, err
}

// CreateClient registers a client and returns it with its secret (shown once; empty for a public client).
func (s *Service) CreateClient(ctx context.Context, name string, redirects []string, public bool) (Client, string, error) {
	for _, r := range redirects {
		u, err := url.Parse(r)
		if err != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return Client{}, "", fmt.Errorf("redirect URI %q must be https (or http on localhost) without a fragment", r)
		}
	}
	id, err := randomToken("hkc_")
	if err != nil {
		return Client{}, "", err
	}
	var secret string
	var hash []byte
	if !public {
		if secret, err = randomToken("hkcs_"); err != nil {
			return Client{}, "", err
		}
		hash = auth.HashToken(secret)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO oauth_clients (id, name, secret_hash, redirect_uris) VALUES ($1, $2, $3, $4)`, id, name, hash, redirects)
	return Client{ID: id, Name: name, RedirectURIs: redirects, Confidential: !public}, secret, err
}

// ListClients returns every client, enabled or not.
func (s *Service) ListClients(ctx context.Context) ([]Client, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, redirect_uris, secret_hash IS NOT NULL FROM oauth_clients WHERE disabled_at IS NULL ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Client
	for rows.Next() {
		var c Client
		if err := rows.Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.Confidential); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DisableClient stops a client: it can't start new connections, and every grant it holds is revoked.
func (s *Service) DisableClient(ctx context.Context, id string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE oauth_clients SET disabled_at = now() WHERE id = $1 AND disabled_at IS NULL`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrUnknownClient
		}
		_, err = tx.Exec(ctx, `UPDATE oauth_grants SET revoked_at = now() WHERE client_id = $1 AND revoked_at IS NULL`, id)
		return err
	})
}

// ClientAuth is what the token and revocation endpoints received to identify the client: from HTTP Basic or the form.
type ClientAuth struct {
	ID, Secret string
}

func (s *Service) authenticateClient(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, a ClientAuth) (Client, error) {
	c, secret, err := s.client(ctx, q, a.ID)
	if err != nil {
		if errors.Is(err, ErrUnknownClient) {
			return Client{}, ErrClientAuth
		}
		return Client{}, err
	}
	if secret != nil && subtle.ConstantTimeCompare(secret, auth.HashToken(a.Secret)) != 1 {
		return Client{}, ErrClientAuth
	}
	return c, nil
}

// ---- Authorization request.

// AuthorizeRequest is the authorization request's parameters (RFC 6749 §4.1.1 with PKCE, RFC 7636, and resource indicators, RFC 8707).
type AuthorizeRequest struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	State               string `json:"state"`
	Scope               string `json:"scope"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
}

// FromQuery reads the request from a URL query.
func FromQuery(q url.Values) AuthorizeRequest {
	return AuthorizeRequest{ResponseType: q.Get("response_type"), ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), State: q.Get("state"),
		Scope: q.Get("scope"), CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"), Resource: q.Get("resource")}
}

// Query encodes the request as URL parameters.
func (r AuthorizeRequest) Query() url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"response_type": r.ResponseType, "client_id": r.ClientID, "redirect_uri": r.RedirectURI, "state": r.State,
		"scope": r.Scope, "code_challenge": r.CodeChallenge, "code_challenge_method": r.CodeChallengeMethod, "resource": r.Resource} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return q
}

// RequestsWrite reports whether the client asked for write access.
func (r AuthorizeRequest) RequestsWrite() bool {
	return slices.Contains(strings.Fields(r.Scope), "write")
}

var challengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// Validated is a checked authorization request. Client errors (unknown client, unregistered redirect) are returned as *Error and must be shown to
// the person, never redirected; other problems come back in RedirectError, to be sent to the client's redirect URI.
type Validated struct {
	Request       AuthorizeRequest
	Client        Client
	RedirectError *Error
}

func (s *Service) resourceOK(r string) bool {
	return r == "" || strings.TrimRight(r, "/") == s.Resource()
}

// Validate checks an authorization request.
func (s *Service) Validate(ctx context.Context, r AuthorizeRequest) (Validated, error) {
	c, _, err := s.client(ctx, s.pool, r.ClientID)
	if err != nil {
		return Validated{}, err
	}
	// From here on the redirect URI is the client's registered one (from the database), not the string the request carried: everything that
	// sends a browser to it then builds on an address we registered, with only query parameters added.
	registered := ""
	switch {
	case r.RedirectURI == "" && len(c.RedirectURIs) == 1:
		registered = c.RedirectURIs[0]
	default:
		if i := slices.Index(c.RedirectURIs, r.RedirectURI); i >= 0 {
			registered = c.RedirectURIs[i]
		}
	}
	if registered == "" {
		return Validated{}, ErrBadRedirect
	}
	r.RedirectURI = registered
	v := Validated{Request: r, Client: c}
	switch {
	case r.ResponseType != "code":
		v.RedirectError = oerr(400, "unsupported_response_type", "Only the authorization code flow is supported.")
	case r.CodeChallenge == "" || r.CodeChallengeMethod != "S256":
		v.RedirectError = oerr(400, "invalid_request", "PKCE with code_challenge_method=S256 is required.")
	case !challengeRe.MatchString(r.CodeChallenge):
		v.RedirectError = oerr(400, "invalid_request", "code_challenge must be a base64url SHA-256 hash.")
	case !s.resourceOK(r.Resource):
		v.RedirectError = oerr(400, "invalid_target", "The only resource is "+s.Resource()+".")
	}
	for _, sc := range strings.Fields(r.Scope) {
		if sc != apitokens.ScopeRead && sc != apitokens.ScopeWrite && v.RedirectError == nil {
			v.RedirectError = oerr(400, "invalid_scope", "Scopes are read and write.")
		}
	}
	return v, nil
}

// RedirectWith builds the client redirect carrying params, the state and the issuer (RFC 9207).
func (s *Service) RedirectWith(r AuthorizeRequest, params url.Values) string {
	if r.State != "" {
		params.Set("state", r.State)
	}
	params.Set("iss", s.issuer)
	sep := "?"
	if strings.Contains(r.RedirectURI, "?") {
		sep = "&"
	}
	return r.RedirectURI + sep + params.Encode()
}

// ErrorRedirect sends an error back to the client.
func (s *Service) ErrorRedirect(r AuthorizeRequest, e *Error) string {
	return s.RedirectWith(r, url.Values{"error": {e.Code}, "error_description": {e.Description}})
}

// Approval is what the person chose on the consent page.
type Approval struct {
	OrganizationID uuid.UUID
	Scope          string // read or write
	Code           string // two-factor code, required when the person uses two-factor sign-in
}

// Approve records the person's consent and returns the redirect carrying a single-use authorization code. The person must belong to the
// organization (and not be shut out of it by its two-factor requirement); write access needs the client to have asked for it and the plan to
// include API writes; someone who uses two-factor sign-in confirms with a fresh code, so a session left open somewhere can't connect an app.
func (s *Service) Approve(ctx context.Context, u auth.User, v Validated, a Approval) (string, error) {
	var role string
	if err := s.pool.QueryRow(ctx, `SELECT role FROM organization_members WHERE organization_id = $1 AND user_id = $2`, a.OrganizationID, u.ID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrOrganization
		}
		return "", err
	}
	if s.twoFactor != nil {
		req, err := s.twoFactor.For(ctx, u.ID, a.OrganizationID)
		if err != nil {
			return "", err
		}
		if req.Blocked {
			return "", ErrOrganization
		}
		on, err := s.twoFactor.Enabled(ctx, u.ID)
		if err != nil {
			return "", err
		}
		if on {
			if err := s.twoFactor.CheckCode(ctx, u.ID, u.Email, a.Code); err != nil {
				return "", err
			}
		}
	}
	switch a.Scope {
	case apitokens.ScopeRead:
	case apitokens.ScopeWrite:
		if !v.Request.RequestsWrite() {
			return "", oerr(400, "invalid_scope", "This app asked only to read.")
		}
		if err := s.plans.CanWriteViaAPI(ctx, s.pool, a.OrganizationID); err != nil {
			return "", err
		}
	default:
		return "", oerr(400, "invalid_scope", "Choose read or write access.")
	}
	code, err := randomToken("")
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO oauth_codes (code_hash, client_id, organization_id, user_id, scope, redirect_uri, code_challenge, resource, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9)`, auth.HashToken(code), v.Client.ID, a.OrganizationID, u.ID, a.Scope, v.Request.RedirectURI,
		v.Request.CodeChallenge, v.Request.Resource, s.now().Add(CodeTTL))
	if err != nil {
		return "", fmt.Errorf("store authorization code: %w", err)
	}
	s.sendConnected(ctx, u.Email, v.Client.Name, a)
	return s.RedirectWith(v.Request, url.Values{"code": {code}}), nil
}

// ---- Token endpoint.

// TokenResponse is the successful token response (RFC 6749 §5.1).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// TokenRequest is the token endpoint's form.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	Resource     string
	Client       ClientAuth
}

var verifierRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func pkceMatches(verifier, challenge string) bool {
	if !verifierRe.MatchString(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

var invalidGrant = oerr(400, "invalid_grant", "The authorization code or refresh token is invalid, expired, or was already used.")

// Token handles the token endpoint.
func (s *Service) Token(ctx context.Context, r TokenRequest) (TokenResponse, error) {
	switch r.GrantType {
	case "authorization_code":
		return s.exchangeCode(ctx, r)
	case "refresh_token":
		return s.refresh(ctx, r)
	}
	return TokenResponse{}, oerr(400, "unsupported_grant_type", "Use authorization_code or refresh_token.")
}

func (s *Service) issue(ctx context.Context, tx pgx.Tx, grantID, orgID, userID uuid.UUID, clientName, scope string) (TokenResponse, error) {
	now := s.now()
	access, err := apitokens.IssueForGrant(ctx, tx, orgID, userID, grantID, clientName, scope, now.Add(AccessTokenTTL))
	if err != nil {
		return TokenResponse{}, err
	}
	refresh, err := randomToken(refreshPrefix)
	if err != nil {
		return TokenResponse{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oauth_refresh_tokens (token_hash, grant_id, expires_at) VALUES ($1, $2, $3)`,
		auth.HashToken(refresh), grantID, now.Add(RefreshTokenTTL)); err != nil {
		return TokenResponse{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_grants SET last_used_at = $2 WHERE id = $1`, grantID, now); err != nil {
		return TokenResponse{}, err
	}
	return TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: int(AccessTokenTTL / time.Second), RefreshToken: refresh, Scope: scope}, nil
}

func revokeGrantTx(ctx context.Context, tx pgx.Tx, grantID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE oauth_grants SET revoked_at = COALESCE(revoked_at, now()) WHERE id = $1`, grantID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, now()) WHERE oauth_grant_id = $1`, grantID)
	return err
}

func (s *Service) exchangeCode(ctx context.Context, r TokenRequest) (TokenResponse, error) {
	var out TokenResponse
	var reused bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := s.authenticateClient(ctx, tx, r.Client)
		if err != nil {
			return err
		}
		var (
			clientID, scope, redirect, challenge string
			resource                             *string
			orgID, userID                        uuid.UUID
			expires                              time.Time
			used                                 *time.Time
			grant                                *uuid.UUID
		)
		err = tx.QueryRow(ctx, `SELECT client_id, organization_id, user_id, scope, redirect_uri, code_challenge, resource, expires_at, used_at, grant_id
			FROM oauth_codes WHERE code_hash = $1 FOR UPDATE`, auth.HashToken(r.Code)).
			Scan(&clientID, &orgID, &userID, &scope, &redirect, &challenge, &resource, &expires, &used, &grant)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidGrant
		}
		if err != nil {
			return err
		}
		if used != nil {
			// A code used twice: someone else has it. Revoke what the first use issued (RFC 6749 §4.1.2), and commit that.
			if grant != nil {
				reused = true
				return revokeGrantTx(ctx, tx, *grant)
			}
			return invalidGrant
		}
		if clientID != c.ID || !s.now().Before(expires) || redirect != r.RedirectURI || !pkceMatches(r.CodeVerifier, challenge) {
			return invalidGrant
		}
		if r.Resource != "" && (!s.resourceOK(r.Resource) || (resource != nil && strings.TrimRight(*resource, "/") != strings.TrimRight(r.Resource, "/"))) {
			return oerr(400, "invalid_target", "The only resource is "+s.Resource()+".")
		}
		var grantID uuid.UUID
		if err := tx.QueryRow(ctx, `INSERT INTO oauth_grants (client_id, organization_id, user_id, scope) VALUES ($1, $2, $3, $4) RETURNING id`,
			c.ID, orgID, userID, scope).Scan(&grantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_codes SET used_at = now(), grant_id = $2 WHERE code_hash = $1`, auth.HashToken(r.Code), grantID); err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, grantID, orgID, userID, c.Name, scope)
		return err
	})
	if reused {
		return TokenResponse{}, invalidGrant
	}
	return out, err
}

func (s *Service) refresh(ctx context.Context, r TokenRequest) (TokenResponse, error) {
	var out TokenResponse
	var reused bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := s.authenticateClient(ctx, tx, r.Client)
		if err != nil {
			return err
		}
		var (
			grantID, orgID, userID uuid.UUID
			clientID, scope        string
			expires                time.Time
			used, revoked          *time.Time
		)
		err = tx.QueryRow(ctx, `SELECT g.id, g.organization_id, g.user_id, g.client_id, g.scope, t.expires_at, t.used_at, g.revoked_at
			FROM oauth_refresh_tokens t JOIN oauth_grants g ON g.id = t.grant_id WHERE t.token_hash = $1 FOR UPDATE OF t, g`, auth.HashToken(r.RefreshToken)).
			Scan(&grantID, &orgID, &userID, &clientID, &scope, &expires, &used, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidGrant
		}
		if err != nil {
			return err
		}
		if clientID != c.ID {
			return invalidGrant
		}
		if used != nil {
			// A rotated refresh token presented again: it was copied. End the whole connection, and commit that.
			reused = true
			return revokeGrantTx(ctx, tx, grantID)
		}
		if revoked != nil || !s.now().Before(expires) {
			return invalidGrant
		}
		if _, err := tx.Exec(ctx, `UPDATE oauth_refresh_tokens SET used_at = now() WHERE token_hash = $1`, auth.HashToken(r.RefreshToken)); err != nil {
			return err
		}
		out, err = s.issue(ctx, tx, grantID, orgID, userID, c.Name, scope)
		return err
	})
	if reused {
		return TokenResponse{}, invalidGrant
	}
	return out, err
}

// Revoke handles the revocation endpoint (RFC 7009): an access or refresh token issued to this client ends its whole connection. Unknown tokens are
// not an error.
func (s *Service) Revoke(ctx context.Context, token string, a ClientAuth) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		c, err := s.authenticateClient(ctx, tx, a)
		if err != nil {
			return err
		}
		var grantID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT g.id FROM oauth_grants g WHERE g.client_id = $2 AND (
				g.id = (SELECT grant_id FROM oauth_refresh_tokens WHERE token_hash = $1)
				OR g.id = (SELECT oauth_grant_id FROM api_tokens WHERE token_hash = $1))`, auth.HashToken(token), c.ID).Scan(&grantID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return revokeGrantTx(ctx, tx, grantID)
	})
}

// ---- Connected apps, for the API keys page.

// Grant is a connected app as the person sees it.
type Grant struct {
	ID         uuid.UUID  `json:"id"`
	ClientName string     `json:"client_name"`
	Scope      string     `json:"scope"`
	OwnerEmail string     `json:"owner_email"`
	Mine       bool       `json:"mine"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// ListGrants returns the organization's live connections: an owner sees everyone's, a member their own.
func (s *Service) ListGrants(ctx context.Context, orgID, userID uuid.UUID, role string) ([]Grant, error) {
	rows, err := s.pool.Query(ctx, `SELECT g.id, c.name, g.scope, u.email, g.user_id = $2, g.created_at,
			GREATEST(g.last_used_at, (SELECT max(t.last_used_at) FROM api_tokens t WHERE t.oauth_grant_id = g.id))
		FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id JOIN users u ON u.id = g.user_id
		WHERE g.organization_id = $1 AND g.revoked_at IS NULL AND ($3 OR g.user_id = $2)
		  AND EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.grant_id = g.id AND r.used_at IS NULL AND r.expires_at > now())
		ORDER BY g.created_at DESC`, orgID, userID, role == organizations.RoleOwner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.ClientName, &g.Scope, &g.OwnerEmail, &g.Mine, &g.CreatedAt, &g.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeGrant disconnects an app: its tokens stop working at once.
func (s *Service) RevokeGrant(ctx context.Context, orgID, grantID, userID uuid.UUID, role string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE oauth_grants SET revoked_at = now() WHERE id = $1 AND organization_id = $2 AND revoked_at IS NULL AND ($4 OR user_id = $3)`,
			grantID, orgID, userID, role == organizations.RoleOwner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrGrantNotFound
		}
		return revokeGrantTx(ctx, tx, grantID)
	})
}

// Purge deletes codes and refresh tokens that can no longer be used, and grants revoked over 30 days ago.
func (s *Service) Purge(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM oauth_codes WHERE expires_at < now() - interval '1 day';
		DELETE FROM oauth_refresh_tokens WHERE expires_at < now() OR used_at < now() - interval '1 day';
		DELETE FROM oauth_grants WHERE revoked_at < now() - interval '30 days'`)
	return err
}

func (s *Service) sendConnected(ctx context.Context, to, clientName string, a Approval) {
	var org string
	_ = s.pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, a.OrganizationID).Scan(&org)
	access := "Read only"
	if a.Scope == apitokens.ScopeWrite {
		access = "Read and write"
	}
	c := email.Content{
		Preheader: clientName + " was connected to your HawkEye account.", Heading: "An app was connected",
		Paragraphs:  []string{fmt.Sprintf("%s can now use HawkEye as you in %s, within the access below, until you disconnect it.", clientName, org)},
		Facts:       []email.Fact{{Label: "App", Value: clientName}, {Label: "Organization", Value: org}, {Label: "Access", Value: access}},
		ButtonLabel: "Review connected apps", ButtonURL: s.issuer + "/api-keys#connected-apps", Tone: email.ToneAlert, AppBaseURL: s.issuer,
		Note: "If this wasn't you, disconnect it and change your password.",
	}
	if err := s.mail.Send(ctx, c.Message("", to, "An app was connected to your HawkEye account")); err != nil {
		s.log.ErrorContext(ctx, "send app connected email failed", "error", err)
	}
}
