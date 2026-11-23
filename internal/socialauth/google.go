package socialauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GoogleProvider signs in with Google (OpenID Connect). It asks only for openid and email: the account ID and address, nothing else.
type GoogleProvider struct {
	cfg Config
	now func() time.Time
}

func NewGoogle(cfg Config) *GoogleProvider { return &GoogleProvider{cfg: cfg, now: time.Now} }

func (g *GoogleProvider) Name() string { return Google }

func (g *GoogleProvider) AuthURL(state, challenge, nonce string, fresh bool) string {
	q := url.Values{
		"client_id": {g.cfg.ClientID}, "redirect_uri": {g.cfg.RedirectURL}, "response_type": {"code"}, "scope": {"openid email"},
		"state": {state}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"prompt": {"select_account"}, // people often have several Google accounts: let them pick
	}
	if fresh {
		q.Set("prompt", "login") // confirming a sensitive action: Google asks for the password again
		q.Set("max_age", "0")
	}
	return or(g.cfg.AuthEndpoint, "https://accounts.google.com/o/oauth2/v2/auth") + "?" + q.Encode()
}

// googleClaims are the ID token claims HawkEye reads.
type googleClaims struct {
	Iss           string          `json:"iss"`
	Aud           json.RawMessage `json:"aud"` // a string, or a list of them
	Exp           int64           `json:"exp"`
	Sub           string          `json:"sub"`
	Email         string          `json:"email"`
	EmailVerified any             `json:"email_verified"` // true, or "true" in some older tokens
	Nonce         string          `json:"nonce"`
}

// Exchange swaps the code for tokens and reads the ID token. The token comes straight from Google's token endpoint over TLS, which Google's
// OpenID Connect guide says may be trusted without checking its signature; its claims are still checked: issuer, audience, expiry and nonce.
func (g *GoogleProvider) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	var tok struct {
		IDToken string `json:"id_token"`
	}
	form := url.Values{"code": {code}, "client_id": {g.cfg.ClientID}, "client_secret": {g.cfg.ClientSecret}, "redirect_uri": {g.cfg.RedirectURL},
		"grant_type": {"authorization_code"}, "code_verifier": {verifier}}
	if err := postForm(ctx, g.cfg.client(), or(g.cfg.TokenEndpoint, "https://oauth2.googleapis.com/token"), form, &tok); err != nil {
		return Identity{}, err
	}
	c, err := g.claims(tok.IDToken)
	if err != nil {
		return Identity{}, err
	}
	if c.Nonce == "" || c.Nonce != nonce {
		return Identity{}, fmt.Errorf("%w: nonce does not match", ErrExchange)
	}
	verified := c.EmailVerified == true || c.EmailVerified == "true"
	return Identity{Provider: Google, Subject: c.Sub, Email: strings.ToLower(strings.TrimSpace(c.Email)), EmailVerified: verified && c.Email != ""}, nil
}

func (g *GoogleProvider) claims(idToken string) (googleClaims, error) {
	var c googleClaims
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return c, fmt.Errorf("%w: no ID token", ErrExchange)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, fmt.Errorf("%w: ID token is not base64url", ErrExchange)
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, fmt.Errorf("%w: ID token is not JSON", ErrExchange)
	}
	if c.Iss != "https://accounts.google.com" && c.Iss != "accounts.google.com" {
		return c, fmt.Errorf("%w: issuer %q", ErrExchange, c.Iss)
	}
	if !audienceIncludes(c.Aud, g.cfg.ClientID) {
		return c, fmt.Errorf("%w: ID token is for another client", ErrExchange)
	}
	if time.Unix(c.Exp, 0).Before(g.now()) {
		return c, fmt.Errorf("%w: ID token expired", ErrExchange)
	}
	if c.Sub == "" {
		return c, fmt.Errorf("%w: ID token has no subject", ErrExchange)
	}
	return c, nil
}

func audienceIncludes(raw json.RawMessage, clientID string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == clientID
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == clientID {
				return true
			}
		}
	}
	return false
}
