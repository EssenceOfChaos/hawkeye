// Package socialauth signs people in with Google, GitHub and X (OAuth 2.0 authorization code flow with PKCE): it builds the address to send the
// browser to, and turns the code that comes back into the account's identity. Which HawkEye account that identity belongs to, and what to do
// with it, is internal/auth's job (docs/plan-social-sign-in.md).
//
// Not to be confused with internal/oauth, where HawkEye is the OAuth provider that Claude and Codex connect to.
package socialauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Provider names, as stored in user_identities.provider and used in addresses (/auth/social/{provider}/...).
const (
	Google = "google"
	GitHub = "github"
	X      = "x"
)

// Identity is who the provider says signed in. Subject is the provider's stable account ID (never a name or login, which can change). Email is
// the account's address, and EmailVerified says the provider vouches that its owner controls it.
type Identity struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
}

// ErrDenied means the person canceled at the provider, or the provider refused. ErrExchange means the code couldn't be turned into an
// identity (expired, reused, or the provider answered unexpectedly).
var (
	ErrDenied   = errors.New("sign-in canceled at the provider")
	ErrExchange = errors.New("the provider's answer couldn't be used")
)

// Provider is one sign-in provider.
type Provider interface {
	Name() string
	// AuthURL is where to send the browser. fresh asks the provider to show its sign-in screen again, where it supports that (Google).
	AuthURL(state, codeChallenge, nonce string, fresh bool) string
	// Exchange turns the returned code into the identity, checking the nonce where the provider uses one.
	Exchange(ctx context.Context, code, codeVerifier, nonce string) (Identity, error)
}

// Config holds one provider's OAuth client. RedirectURL is the app's callback (APP_BASE_URL + /api/v1/auth/social/{provider}/callback).
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// Endpoints, replaced by tests with fake servers. Empty means the provider's real ones.
	AuthEndpoint  string
	TokenEndpoint string
	APIBase       string // GitHub's REST API
	HTTPClient    *http.Client
}

func (c Config) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// maxResponse bounds what is read from a provider.
const maxResponse = 1 << 20

// NewPKCE returns a PKCE verifier and its S256 challenge (RFC 7636).
func NewPKCE() (verifier, challenge string) {
	verifier = RandomString(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// RandomString is n random bytes, base64url-encoded: for states, nonces and PKCE verifiers.
func RandomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return base64.RawURLEncoding.EncodeToString(b)
}

// postForm sends a token request and decodes the JSON answer into out.
func postForm(ctx context.Context, hc *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return do(hc, req, out)
}

func do(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExchange, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExchange, err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body may name the problem, but it is the provider's text: it is neither shown nor logged.
		return fmt.Errorf("%w: HTTP %d", ErrExchange, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %v", ErrExchange, err)
	}
	return nil
}
