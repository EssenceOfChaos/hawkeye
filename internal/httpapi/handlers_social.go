package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/adconversions"
	"hawkeye/internal/attribution"
	"hawkeye/internal/auth"
	"hawkeye/internal/socialauth"
)

// Sign in with Google and GitHub (docs/plan-social-sign-in.md), next to email and password, which are unchanged.

// socialStateCookie holds the state of a sign-in in progress, so only the browser that started it can finish it. It is sent only to the
// callback, lives 10 minutes, and is cleared on return.
const (
	socialStateCookie = "hawkeye_social_state"
	socialCookiePath  = "/api/v1/auth/social"
)

func (h *handlers) provider(c *echo.Context) (socialauth.Provider, error) {
	p, ok := h.social[c.Param("provider")]
	if !ok {
		return nil, &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "That sign-in provider isn't available."}
	}
	return p, nil
}

// socialProviders lists the providers offered, so the app shows only their buttons.
func (h *handlers) socialProviders(c *echo.Context) error {
	names := make([]string, 0, len(h.social))
	for n := range h.social {
		names = append(names, n)
	}
	sort.Strings(names) // github, google: the app orders the buttons itself
	return c.JSON(http.StatusOK, map[string]any{"providers": names})
}

// socialStart records a sign-in about to start and answers with the provider's address for the browser to go to. intent is signin (sign in or
// create an account), or, signed in, connect (settings) or confirm (before a sensitive action, for an account without a password).
func (h *handlers) socialStart(c *echo.Context) error {
	p, err := h.provider(c)
	if err != nil {
		return err
	}
	var in struct {
		Intent      string                   `json:"intent"`
		Next        string                   `json:"next"`
		Attribution *attribution.Attribution `json:"attribution"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Intent == "" {
		in.Intent = auth.IntentSignIn
	}
	if !slices.Contains([]string{auth.IntentSignIn, auth.IntentConnect, auth.IntentConfirm}, in.Intent) {
		return &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "intent must be signin, connect or confirm"}
	}
	st := auth.SocialStart{Intent: in.Intent, Next: in.Next, Attribution: in.Attribution}
	if in.Intent != auth.IntentSignIn {
		u, err := h.auth.Authenticate(c.Request().Context(), sessionToken(c))
		if err != nil {
			return errUnauthenticated
		}
		st.UserID = &u.ID
	}
	redirect, state, err := h.auth.BeginSocial(c.Request().Context(), p, st)
	if err != nil {
		return err
	}
	c.SetCookie(&http.Cookie{Name: socialStateCookie, Value: state, Path: socialCookiePath, MaxAge: 600,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode}) // Lax: sent on the provider's top-level redirect back
	return c.JSON(http.StatusOK, map[string]any{"url": redirect})
}

// socialCallback is where the provider sends the browser back. It always answers with a redirect into the app: on success to where the person was
// going (with #signed-in=<provider>, so the app can remember the method for "Last used"), on a two-factor account to the code page, and on
// failure back to sign-in or settings with a short error code. Nothing the provider sends is shown as-is.
func (h *handlers) socialCallback(c *echo.Context) error {
	ctx := c.Request().Context()
	c.SetCookie(&http.Cookie{Name: socialStateCookie, Value: "", Path: socialCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode})
	p, err := h.provider(c)
	if err != nil {
		return c.Redirect(http.StatusSeeOther, "/login?error=failed")
	}
	var cookieState string
	if ck, err := c.Cookie(socialStateCookie); err == nil {
		cookieState = ck.Value
	}
	q := c.Request().URL.Query()
	code := q.Get("code")
	var res auth.SocialResult
	if q.Get("error") != "" || code == "" {
		err = socialauth.ErrDenied
		// Still use up the state, so it can't be replayed; the intent tells where to send the person.
		res, _ = h.auth.FinishSocial(ctx, deniedProvider{p}, q.Get("state"), cookieState, "", "")
	} else {
		res, err = h.auth.FinishSocial(ctx, p, q.Get("state"), cookieState, code, sessionToken(c))
	}

	var need *auth.SecondFactorRequired
	switch {
	case errors.As(err, &need):
		// The code page continues the sign-in. The challenge goes in the fragment, which browsers never send to a server or in a Referer.
		frag := url.Values{"challenge": {need.Challenge}, "via": {p.Name()}}
		if res.Next != "" {
			frag.Set("next", res.Next)
		}
		return c.Redirect(http.StatusSeeOther, "/login#"+frag.Encode())
	case err != nil:
		if !errors.Is(err, socialauth.ErrDenied) && !errors.Is(err, auth.ErrSocialState) {
			h.log.WarnContext(ctx, "social sign-in failed", "provider", p.Name(), "intent", res.Intent, "error", err)
		}
		code := auth.SocialErrorCode(err)
		if res.Intent == auth.IntentConnect || res.Intent == auth.IntentConfirm {
			return c.Redirect(http.StatusSeeOther, "/account?social_error="+code+"#sign-in-methods")
		}
		return c.Redirect(http.StatusSeeOther, "/login?error="+code)
	}

	switch res.Intent {
	case auth.IntentConnect:
		return c.Redirect(http.StatusSeeOther, "/account?connected="+p.Name()+"#sign-in-methods")
	case auth.IntentConfirm:
		next := res.Next
		if next == "" {
			next = "/account"
		}
		return c.Redirect(http.StatusSeeOther, next+"#confirmed="+p.Name())
	}
	h.setSessionCookie(c, res.Session)
	frag := url.Values{"signed-in": {p.Name()}}
	if res.Created {
		// As after the sign-up form: the server reports the sign-up to OpenAI Ads, and the browser does too with the same id.
		if err := h.adConversions.Registration(ctx, adconversions.FromRequest(c.Request()), res.User.ID); err != nil {
			h.log.WarnContext(ctx, "could not record the sign-up for OpenAI Ads", "error", err)
		}
		frag.Set("signed-up", adconversions.RegistrationEventID(res.User.ID))
	}
	next := res.Next
	if next == "" {
		next = "/dashboard"
	}
	return c.Redirect(http.StatusSeeOther, next+"#"+frag.Encode())
}

// deniedProvider stands in for a provider when the person canceled: finishing with it only uses up the state.
type deniedProvider struct{ socialauth.Provider }

func (deniedProvider) Exchange(context.Context, string, string, string) (socialauth.Identity, error) {
	return socialauth.Identity{}, socialauth.ErrDenied
}

// signInMethods lists how the signed-in person can sign in, and which providers they could connect.
func (h *handlers) signInMethods(c *echo.Context) error {
	hasPassword, ids, err := h.auth.SignInMethods(c.Request().Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	available := make([]string, 0, len(h.social))
	for n := range h.social {
		available = append(available, n)
	}
	sort.Strings(available)
	return c.JSON(http.StatusOK, map[string]any{"has_password": hasPassword, "identities": ids, "providers": available})
}

func (h *handlers) disconnectSignInMethod(c *echo.Context) error {
	if err := h.auth.Disconnect(c.Request().Context(), currentUser(c), c.Param("provider")); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// setPassword gives a password to an account created with Google or GitHub, once the person has confirmed it's them through their provider.
func (h *handlers) setPassword(c *echo.Context) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.auth.SetPassword(c.Request().Context(), currentUser(c), sessionToken(c), in.Password); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
