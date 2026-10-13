// Package httpapi wires the Echo application: middleware, error handling and routes.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
)

// Pinger reports whether a dependency (PostgreSQL) is usable.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Options struct {
	Logger         *slog.Logger
	DB             Pinger
	RequestTimeout time.Duration
	BodyLimit      string

	// Auth and Orgs are optional so the foundation can be tested without a database.
	Auth          *auth.Service
	Orgs          *organizations.Service
	Plans         *plans.Service
	Monitors      *monitors.Service
	Notifications *notifications.Service
	Billing       *billing.Service
	PgPool        *pgxpool.Pool
	Pool          plans.DB

	SecureCookies       bool
	AuthRateLimitPerMin int
	TrustForwardedFor   bool
}

const readinessTimeout = 2 * time.Second

// New builds the Echo application.
func New(o Options) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = errorHandler(o.Logger)
	// Client IPs drive rate limiting, so X-Forwarded-For is ignored unless a trusted proxy is declared.
	if o.TrustForwardedFor {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}

	e.Use(middleware.RequestID())
	e.Use(requestLogger(o.Logger))
	e.Use(middleware.Recover())
	e.Use(middleware.BodyLimit(o.BodyLimit))
	e.Use(securityHeaders)
	e.Use(middleware.ContextTimeoutWithConfig(middleware.ContextTimeoutConfig{Timeout: o.RequestTimeout}))

	e.GET("/healthz", healthz)
	e.GET("/readyz", readyz(o.DB, o.Logger))

	if o.Billing != nil {
		// The provider calls this endpoint, not a browser: no session, no CSRF token. It is authenticated by the signature
		// over the raw body, which is checked before anything is stored. Registered before the CSRF-protected group.
		e.POST("/api/v1/webhooks/stripe", (&handlers{billing: o.Billing, log: o.Logger}).stripeWebhook)
	}
	if o.Auth != nil && o.Orgs != nil && o.Plans != nil && o.Monitors != nil && o.Notifications != nil && o.PgPool != nil && o.Billing != nil {
		registerAPI(e, o)
	}
	return e
}

// healthz is a liveness probe: it does not touch dependencies.
func healthz(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// readyz verifies that required dependencies are usable. Failure details are logged, not returned.
func readyz(db Pinger, log *slog.Logger) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), readinessTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			log.WarnContext(ctx, "readiness: database ping failed", "error", err)
			return c.JSON(http.StatusServiceUnavailable, map[string]any{
				"status": "unavailable",
				"checks": map[string]string{"database": "unavailable"},
			})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"status": "ok",
			"checks": map[string]string{"database": "ok"},
		})
	}
}

func securityHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		h := c.Response().Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		return next(c)
	}
}

func requestLogger(log *slog.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogMethod: true, LogURIPath: true, LogStatus: true, LogLatency: true, LogRequestID: true, LogError: true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			attrs := []any{"method", v.Method, "path", v.URIPath, "status", v.Status,
				"latency_ms", v.Latency.Milliseconds(), "request_id", v.RequestID}
			level := slog.LevelInfo
			if v.Status >= 500 {
				level = slog.LevelError
			}
			if v.Error != nil && v.Status >= 500 {
				attrs = append(attrs, "error", v.Error.Error())
			}
			log.Log(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}

// registerAPI mounts /api/v1. Cookie-authenticated requests are protected by double-submit CSRF
// tokens: the csrf_token cookie (readable by the frontend) must be echoed in X-CSRF-Token on
// unsafe methods. Webhooks (Phase 7) are mounted outside this group because they use signatures.
func registerAPI(e *echo.Echo, o Options) {
	h := &handlers{auth: o.Auth, orgs: o.Orgs, plans: o.Plans, monitors: o.Monitors, pool: o.Pool, db: o.PgPool, notifications: o.Notifications, billing: o.Billing, log: o.Logger, secureCookies: o.SecureCookies}

	v1 := e.Group("/api/v1", middleware.CSRFWithConfig(middleware.CSRFConfig{
		TokenLookup:    "header:X-CSRF-Token",
		CookieName:     "csrf_token",
		CookiePath:     "/",
		CookieSecure:   o.SecureCookies,
		CookieHTTPOnly: false,
		CookieSameSite: http.SameSiteLaxMode,
		CookieMaxAge:   86400,
		ErrorHandler: func(error, echo.Context) error {
			return &APIError{Status: http.StatusForbidden, Code: "CSRF_INVALID", Message: "Missing or invalid CSRF token."}
		},
	}))

	perMin := o.AuthRateLimitPerMin
	if perMin < 1 {
		perMin = 10
	}
	limit := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
			Rate: rate.Limit(float64(perMin) / 60), Burst: perMin, ExpiresIn: time.Hour,
		}),
		IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
		DenyHandler: func(echo.Context, string, error) error {
			return &APIError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Too many requests. Try again later."}
		},
	})

	a := v1.Group("/auth")
	a.POST("/register", h.register, limit)
	a.POST("/login", h.login, limit)
	a.POST("/forgot-password", h.forgotPassword, limit)
	a.POST("/reset-password", h.resetPassword, limit)
	a.POST("/verify-email", h.verifyEmail, limit)
	a.POST("/logout", h.logout)
	a.GET("/me", h.me, h.requireAuth)
	a.POST("/resend-verification", h.resendVerification, h.requireAuth, limit)

	v1.GET("/plans", h.listPlans)
	// Called from the confirmation email by a recipient who may not have an account.
	v1.POST("/notification-settings/confirm", h.confirmSetting, limit)

	// Tenant-scoped resources act on the organization named by X-Organization-ID (membership is
	// verified) or, by default, the caller's oldest organization.
	scoped := v1.Group("", h.requireAuth, h.resolveOrg)
	scoped.GET("/usage", h.usage)
	scoped.GET("/monitors", h.listMonitors)
	scoped.POST("/monitors", h.createMonitor)
	scoped.GET("/monitors/:monitorID", h.getMonitor)
	scoped.PATCH("/monitors/:monitorID", h.updateMonitor)
	scoped.DELETE("/monitors/:monitorID", h.deleteMonitor)
	scoped.POST("/monitors/:monitorID/pause", h.pauseMonitor)
	scoped.POST("/monitors/:monitorID/resume", h.resumeMonitor)
	scoped.GET("/monitors/:monitorID/incidents", h.listMonitorIncidents)
	scoped.GET("/incidents", h.listIncidents)

	// Billing: any member can see the plan; changes need the owner and a verified email.
	scoped.GET("/billing/subscription", h.billingSubscription)
	scoped.POST("/billing/checkout-session", h.billingCheckout, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/portal-session", h.billingPortal, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/cancel-subscription", h.billingCancel, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/sync", h.billingSync, requireOwner)
	scoped.GET("/monitors/:monitorID/results", h.monitorResults)
	scoped.GET("/monitors/:monitorID/uptime", h.monitorUptime)
	scoped.GET("/monitors/:monitorID/response-times", h.monitorResponseTimes)
	scoped.GET("/dashboard/summary", h.dashboardSummary)
	scoped.GET("/dashboard/recent-incidents", h.dashboardRecentIncidents)
	scoped.GET("/dashboard/response-times", h.dashboardResponseTimes)

	// Alert recipients: any member can view; only owners change them; adding a recipient needs a verified
	// email and is rate-limited because it causes mail to be sent to a third party.
	scoped.GET("/notification-settings", h.listSettings)
	scoped.POST("/notification-settings", h.createSetting, requireOwner, requireVerifiedEmail, limit)
	scoped.PATCH("/notification-settings/:settingID", h.updateSetting, requireOwner)
	scoped.DELETE("/notification-settings/:settingID", h.deleteSetting, requireOwner)
	scoped.POST("/notification-settings/:settingID/resend-confirmation", h.resendSettingConfirmation, requireOwner, requireVerifiedEmail, limit)

	org := v1.Group("/organizations", h.requireAuth)
	org.GET("", h.listOrgs)
	org.GET("/:organizationID", h.getOrg)
	org.PATCH("/:organizationID", h.updateOrg)
	org.GET("/:organizationID/members", h.listMembers)
}
