package socialauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// GitHubProvider signs in with GitHub. It asks only for user:email: the account ID and its addresses. It works with an OAuth App, or a GitHub
// App with the "Email addresses: read" account permission.
type GitHubProvider struct{ cfg Config }

func NewGitHub(cfg Config) *GitHubProvider { return &GitHubProvider{cfg: cfg} }

func (g *GitHubProvider) Name() string { return GitHub }

// AuthURL ignores nonce (GitHub isn't OpenID Connect; state and PKCE protect the flow) and fresh (GitHub has no way to ask for the password again:
// a fresh authorization proves the person still controls the GitHub account).
func (g *GitHubProvider) AuthURL(state, challenge, _ string, _ bool) string {
	q := url.Values{"client_id": {g.cfg.ClientID}, "redirect_uri": {g.cfg.RedirectURL}, "scope": {"user:email"}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return or(g.cfg.AuthEndpoint, "https://github.com/login/oauth/authorize") + "?" + q.Encode()
}

// Exchange swaps the code for an access token, reads the account's numeric ID and its primary, verified address, and drops the token.
func (g *GitHubProvider) Exchange(ctx context.Context, code, verifier, _ string) (Identity, error) {
	var tok struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"` // GitHub answers 200 with an error field for a bad code
	}
	form := url.Values{"code": {code}, "client_id": {g.cfg.ClientID}, "client_secret": {g.cfg.ClientSecret}, "redirect_uri": {g.cfg.RedirectURL},
		"code_verifier": {verifier}}
	if err := postForm(ctx, g.cfg.client(), or(g.cfg.TokenEndpoint, "https://github.com/login/oauth/access_token"), form, &tok); err != nil {
		return Identity{}, err
	}
	if tok.AccessToken == "" {
		return Identity{}, fmt.Errorf("%w: no access token (%s)", ErrExchange, tok.Error)
	}
	api := strings.TrimSuffix(or(g.cfg.APIBase, "https://api.github.com"), "/")

	var user struct {
		ID int64 `json:"id"` // the stable ID; the login name can be renamed
	}
	if err := g.get(ctx, api+"/user", tok.AccessToken, &user); err != nil {
		return Identity{}, err
	}
	if user.ID <= 0 {
		return Identity{}, fmt.Errorf("%w: no account ID", ErrExchange)
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.get(ctx, api+"/user/emails", tok.AccessToken, &emails); err != nil {
		return Identity{}, err
	}
	id := Identity{Provider: GitHub, Subject: strconv.FormatInt(user.ID, 10)}
	// Only the primary address counts, and only when GitHub has verified it: that is the one GitHub vouches for.
	for _, e := range emails {
		if e.Primary {
			id.Email, id.EmailVerified = strings.ToLower(strings.TrimSpace(e.Email)), e.Verified && e.Email != ""
		}
	}
	return id, nil
}

func (g *GitHubProvider) get(ctx context.Context, endpoint, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return do(g.cfg.client(), req, out)
}
