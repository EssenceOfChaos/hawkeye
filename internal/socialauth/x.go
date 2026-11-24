package socialauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// XProvider signs in with X (OAuth 2.0 with PKCE, as a confidential client: the app's type in X's developer console is "Web app"). It asks for
// users.read and tweet.read, which X requires to read the signed-in account, and users.email for its confirmed address. The app also needs
// "Request email from users" turned on in X's developer console, or the address never comes back.
type XProvider struct{ cfg Config }

func NewX(cfg Config) *XProvider { return &XProvider{cfg: cfg} }

func (x *XProvider) Name() string { return X }

// AuthURL ignores nonce (X isn't OpenID Connect; state and PKCE protect the flow) and fresh (X has no way to ask for the password again: a fresh
// authorization proves the person still controls the X account).
func (x *XProvider) AuthURL(state, challenge, _ string, _ bool) string {
	q := url.Values{"response_type": {"code"}, "client_id": {x.cfg.ClientID}, "redirect_uri": {x.cfg.RedirectURL},
		"scope": {"users.read tweet.read users.email"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return or(x.cfg.AuthEndpoint, "https://x.com/i/oauth2/authorize") + "?" + q.Encode()
}

// Exchange swaps the code for an access token (the client authenticates with HTTP Basic, as X requires of confidential clients), reads the
// account's ID and confirmed address, and drops the token. X sends confirmed_email only for an address its owner has confirmed, so that is the
// one X vouches for; an account without one (signed up with a phone number, say) comes back with none.
func (x *XProvider) Exchange(ctx context.Context, code, verifier, _ string) (Identity, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {x.cfg.RedirectURL}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, or(x.cfg.TokenEndpoint, "https://api.x.com/2/oauth2/token"), strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, err
	}
	req.SetBasicAuth(url.QueryEscape(x.cfg.ClientID), url.QueryEscape(x.cfg.ClientSecret)) // RFC 6749 2.3.1: each part form-encoded first
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := do(x.cfg.client(), req, &tok); err != nil {
		return Identity{}, err
	}
	if tok.AccessToken == "" {
		return Identity{}, fmt.Errorf("%w: no access token", ErrExchange)
	}

	api := strings.TrimSuffix(or(x.cfg.APIBase, "https://api.x.com"), "/")
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, api+"/2/users/me?user.fields=confirmed_email", nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var me struct {
		Data struct {
			ID             string `json:"id"` // the stable ID; the @username can be changed
			ConfirmedEmail string `json:"confirmed_email"`
		} `json:"data"`
	}
	if err := do(x.cfg.client(), req, &me); err != nil {
		return Identity{}, err
	}
	if me.Data.ID == "" {
		return Identity{}, fmt.Errorf("%w: no account ID", ErrExchange)
	}
	email := strings.ToLower(strings.TrimSpace(me.Data.ConfirmedEmail))
	return Identity{Provider: X, Subject: me.Data.ID, Email: email, EmailVerified: email != ""}, nil
}
