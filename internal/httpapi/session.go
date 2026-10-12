package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/auth"
)

const (
	sessionCookie = "hawkeye_session"
	userKey       = "user"
)

func (h *handlers) setSessionCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		MaxAge:   int(h.auth.SessionTTL() / time.Second),
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
}

func (h *handlers) clearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookies, SameSite: http.SameSiteLaxMode,
	})
}

var errUnauthenticated = &APIError{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED", Message: "Authentication required."}

// requireAuth resolves the session cookie to a user and stores it on the context.
func (h *handlers) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		cookie, err := c.Cookie(sessionCookie)
		if err != nil {
			return errUnauthenticated
		}
		u, err := h.auth.Authenticate(c.Request().Context(), cookie.Value)
		if err != nil {
			if err == auth.ErrUnauthenticated {
				h.clearSessionCookie(c)
				return errUnauthenticated
			}
			return err
		}
		c.Set(userKey, u)
		return next(c)
	}
}

func currentUser(c echo.Context) auth.User { return c.Get(userKey).(auth.User) }

// requireVerifiedEmail guards sensitive operations (billing, alert delivery settings). Used from Phase 3 on.
func requireVerifiedEmail(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !currentUser(c).EmailVerified() {
			return &APIError{Status: http.StatusForbidden, Code: "EMAIL_NOT_VERIFIED",
				Message: "Verify your email address to use this feature."}
		}
		return next(c)
	}
}
