package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/account"
	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/contact"
	"hawkeye/internal/domains"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/regions"
	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/validation"
)

type handlers struct {
	auth          *auth.Service
	orgs          *organizations.Service
	plans         *plans.Service
	monitors      *monitors.Service
	pool          plans.DB
	db            *pgxpool.Pool
	notifications *notifications.Service
	maintenance   *maintenance.Service
	domains       *domains.Service
	statusPages   *statuspage.Service
	regions       regions.Set
	billing       *billing.Service
	account       *account.Service
	contact       *contact.Service
	lifecycle     *lifecycle.Service
	sslcheck      *sslcheck.Service
	sitecheck     *sitecheck.Service
	log           *slog.Logger
	limits        limiterDeps
	emailPerHour  int
	secureCookies bool
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// bind decodes a JSON body, rejecting malformed input with a 400.
func bind(c echo.Context, dst any) error {
	if err := c.Bind(dst); err != nil {
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_JSON", Message: "Request body must be valid JSON."}
	}
	return nil
}

// mapError converts domain errors into API errors; anything unrecognised stays internal.
func mapError(err error) error {
	var ve *validation.Error
	var le *plans.LimitError
	switch {
	case errors.As(err, &le):
		return limitAPIError(le)
	case errors.Is(err, monitors.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Monitor not found."}
	case errors.As(err, &ve):
		return &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR",
			Message: "One or more fields are invalid.", Details: map[string]any{"fields": ve.Fields}}
	case errors.Is(err, auth.ErrWrongPassword):
		return &APIError{Status: http.StatusForbidden, Code: "WRONG_PASSWORD", Message: "That password is not correct."}
	case errors.Is(err, account.ErrSharedOrganization):
		return &APIError{Status: http.StatusConflict, Code: "OWNS_SHARED_ORGANIZATION",
			Message: "You own an organization that has other members. Remove them first, so their data is not deleted with your account."}
	case errors.Is(err, auth.ErrInvalidCredentials):
		return &APIError{Status: http.StatusUnauthorized, Code: "INVALID_CREDENTIALS", Message: "Invalid email or password."}
	case errors.Is(err, auth.ErrEmailTaken):
		return &APIError{Status: http.StatusConflict, Code: "EMAIL_TAKEN", Message: "An account with this email already exists."}
	case errors.Is(err, auth.ErrInvalidToken), errors.Is(err, lifecycle.ErrInvalidToken):
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_TOKEN", Message: "This link is invalid or has expired."}
	case errors.Is(err, billing.ErrNotConfigured):
		return &APIError{Status: http.StatusServiceUnavailable, Code: "BILLING_NOT_CONFIGURED", Message: "Billing is not available on this server yet."}
	case errors.Is(err, billing.ErrPriceMismatch):
		return &APIError{Status: http.StatusServiceUnavailable, Code: "PLAN_UNAVAILABLE", Message: "This plan can't be purchased right now. We've been notified and are fixing it; please try again later."}
	case errors.Is(err, billing.ErrAlreadySubscribed):
		return &APIError{Status: http.StatusConflict, Code: "ALREADY_SUBSCRIBED", Message: "This organization already has a paid plan. Use Manage billing to change it."}
	case errors.Is(err, billing.ErrNoCustomer):
		return &APIError{Status: http.StatusConflict, Code: "NO_BILLING_ACCOUNT", Message: "There is no billing account yet. Choose a plan first."}
	case errors.Is(err, billing.ErrNoSubscription):
		return &APIError{Status: http.StatusConflict, Code: "NO_ACTIVE_SUBSCRIPTION", Message: "There is no active subscription."}
	case errors.Is(err, statuspage.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Status page not found."}
	case errors.Is(err, statuspage.ErrSlugTaken):
		return &APIError{Status: http.StatusConflict, Code: "SLUG_TAKEN", Message: "That status page address is already taken. Choose another.",
			Details: map[string]any{"fields": map[string]string{"slug": "is already taken"}}}
	case errors.Is(err, maintenance.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Maintenance window not found."}
	case errors.Is(err, maintenance.ErrTooMany):
		return &APIError{Status: http.StatusForbidden, Code: "MAINTENANCE_WINDOWS_LIMIT",
			Message: "You can have at most 50 maintenance windows. Delete one to add another.",
			Details: map[string]any{"limit": maintenance.MaxWindowsPerOrganization}}
	case errors.Is(err, domains.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Domain not found."}
	case errors.Is(err, domains.ErrInUse):
		return &APIError{Status: http.StatusConflict, Code: "DOMAIN_IN_USE",
			Message: "Your monitors use this domain, so it is tracked automatically. It disappears when no monitor uses it any more."}
	case errors.Is(err, notifications.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Notification setting not found."}
	case errors.Is(err, notifications.ErrDuplicate):
		return &APIError{Status: http.StatusConflict, Code: "DESTINATION_EXISTS", Message: "This destination is already receiving alerts."}
	case errors.Is(err, notifications.ErrTooMany):
		return &APIError{Status: http.StatusForbidden, Code: "NOTIFICATION_SETTINGS_LIMIT",
			Message: "You can have at most 10 alert destinations. Remove one to add another.",
			Details: map[string]any{"limit": notifications.MaxSettingsPerOrganization}}
	case errors.Is(err, notifications.ErrInvalidToken):
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_TOKEN", Message: "This link is invalid or has expired."}
	case errors.Is(err, organizations.ErrNotFound):
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Organization not found."}
	case errors.Is(err, organizations.ErrForbidden):
		return &APIError{Status: http.StatusForbidden, Code: "FORBIDDEN", Message: "You do not have permission to do this."}
	}
	return err
}

func (h *handlers) register(c echo.Context) error {
	var in credentials
	if err := bind(c, &in); err != nil {
		return err
	}
	u, session, err := h.auth.Register(c.Request().Context(), in.Email, in.Password)
	if err != nil {
		return mapError(err)
	}
	h.setSessionCookie(c, session)
	return c.JSON(http.StatusCreated, map[string]any{"user": u})
}

func (h *handlers) login(c echo.Context) error {
	var in credentials
	if err := bind(c, &in); err != nil {
		return err
	}
	if h.limits.l != nil && !h.limits.allow(c, "login-account", in.Email, loginAttemptsPerHour, time.Hour) {
		return errRateLimited
	}
	u, session, err := h.auth.Login(c.Request().Context(), in.Email, in.Password)
	if err != nil {
		return mapError(err)
	}
	h.setSessionCookie(c, session)
	return c.JSON(http.StatusOK, map[string]any{"user": u})
}

func (h *handlers) logout(c echo.Context) error {
	if cookie, err := c.Cookie(sessionCookie); err == nil {
		if err := h.auth.Logout(c.Request().Context(), cookie.Value); err != nil {
			return err
		}
	}
	h.clearSessionCookie(c)
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) forgotPassword(c echo.Context) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	// Each address may trigger only a few reset emails per hour (no mail-bombing a victim). The response is identical either way.
	if h.emailBudget(c, "forgot", in.Email) {
		if err := h.auth.ForgotPassword(c.Request().Context(), in.Email); err != nil {
			return err
		}
	}
	// Same response whether or not the account exists.
	return c.JSON(http.StatusAccepted, map[string]string{"status": "If the account exists, a reset email has been sent."})
}

func (h *handlers) resetPassword(c echo.Context) error {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.auth.ResetPassword(c.Request().Context(), in.Token, in.Password); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) verifyEmail(c echo.Context) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.auth.VerifyEmail(c.Request().Context(), in.Token); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) resendVerification(c echo.Context) error {
	if !h.emailBudget(c, "verify", currentUser(c).Email) {
		c.Response().Header().Set("Retry-After", "3600")
		return errRateLimited
	}
	if err := h.auth.ResendVerification(c.Request().Context(), currentUser(c)); err != nil {
		return err
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *handlers) me(c echo.Context) error {
	u := currentUser(c)
	orgs, err := h.orgs.List(c.Request().Context(), u.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"user": u, "organizations": orgs})
}
