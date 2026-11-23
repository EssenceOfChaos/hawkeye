package socialauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func idToken(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func TestGoogleChecksTheIDToken(t *testing.T) {
	now := time.Now()
	good := func() map[string]any {
		return map[string]any{"iss": "https://accounts.google.com", "aud": "client", "exp": now.Add(time.Hour).Unix(), "sub": "123",
			"email": "Person@Example.com", "email_verified": true, "nonce": "n1"}
	}
	var claims map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken(claims)})
	}))
	defer srv.Close()
	g := NewGoogle(Config{ClientID: "client", TokenEndpoint: srv.URL})

	claims = good()
	id, err := g.Exchange(t.Context(), "code", "verifier", "n1")
	if err != nil || id != (Identity{Provider: Google, Subject: "123", Email: "person@example.com", EmailVerified: true}) {
		t.Fatalf("%+v %v", id, err)
	}
	claims = good()
	claims["aud"] = []string{"other", "client"}
	if _, err := g.Exchange(t.Context(), "code", "v", "n1"); err != nil {
		t.Fatalf("audience list: %v", err)
	}
	claims = good()
	claims["email_verified"] = "true" // older tokens
	if id, _ := g.Exchange(t.Context(), "code", "v", "n1"); !id.EmailVerified {
		t.Fatal("string true")
	}
	claims = good()
	claims["email_verified"] = false
	if id, _ := g.Exchange(t.Context(), "code", "v", "n1"); id.EmailVerified {
		t.Fatal("unverified")
	}
	for name, change := range map[string]func(map[string]any){
		"issuer":   func(c map[string]any) { c["iss"] = "https://evil.example" },
		"audience": func(c map[string]any) { c["aud"] = "other" },
		"expired":  func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() },
		"nonce":    func(c map[string]any) { c["nonce"] = "n2" },
		"subject":  func(c map[string]any) { delete(c, "sub") },
	} {
		claims = good()
		change(claims)
		if _, err := g.Exchange(t.Context(), "code", "v", "n1"); !errors.Is(err, ErrExchange) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestGoogleAuthURL(t *testing.T) {
	g := NewGoogle(Config{ClientID: "client", RedirectURL: "https://app.example/cb"})
	u, _ := url.Parse(g.AuthURL("st", "ch", "no", false))
	q := u.Query()
	if u.Host != "accounts.google.com" || q.Get("scope") != "openid email" || q.Get("prompt") != "select_account" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "https://app.example/cb" || q.Get("nonce") != "no" {
		t.Fatalf("%s", u)
	}
	u, _ = url.Parse(g.AuthURL("st", "ch", "no", true))
	if u.Query().Get("prompt") != "login" || u.Query().Get("max_age") != "0" {
		t.Fatalf("fresh: %s", u)
	}
}

func TestGitHubUsesOnlyThePrimaryVerifiedAddress(t *testing.T) {
	var emails string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("code") == "bad" {
			_, _ = w.Write([]byte(`{"error":"bad_verification_code"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok"}`))
	})
	mux.HandleFunc("GET /api/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		_, _ = w.Write([]byte(`{"id": 583231, "login": "octocat"}`))
	})
	mux.HandleFunc("GET /api/user/emails", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(emails)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	g := NewGitHub(Config{ClientID: "c", TokenEndpoint: srv.URL + "/token", APIBase: srv.URL + "/api"})

	emails = `[{"email":"other@example.com","primary":false,"verified":true},{"email":"Me@Example.com","primary":true,"verified":true}]`
	id, err := g.Exchange(t.Context(), "code", "v", "")
	if err != nil || id != (Identity{Provider: GitHub, Subject: "583231", Email: "me@example.com", EmailVerified: true}) {
		t.Fatalf("%+v %v", id, err)
	}
	// A verified address that isn't primary doesn't count, nor does an unverified primary one.
	emails = `[{"email":"other@example.com","primary":false,"verified":true},{"email":"me@example.com","primary":true,"verified":false}]`
	if id, _ := g.Exchange(t.Context(), "code", "v", ""); id.EmailVerified {
		t.Fatalf("%+v", id)
	}
	if _, err := g.Exchange(t.Context(), "bad", "v", ""); !errors.Is(err, ErrExchange) {
		t.Fatalf("bad code: %v", err)
	}
	u, _ := url.Parse(g.AuthURL("st", "ch", "", true))
	if q := u.Query(); q.Get("scope") != "user:email" || q.Get("code_challenge") != "ch" || strings.Contains(u.RawQuery, "nonce") {
		t.Fatalf("%s", u)
	}
}

func TestPKCE(t *testing.T) {
	v, c := NewPKCE()
	if len(v) < 43 || c == "" || c == v {
		t.Fatalf("%q %q", v, c)
	}
	if v2, _ := NewPKCE(); v2 == v {
		t.Fatal("not random")
	}
}
