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

	"hawkeye/internal/account"
	"hawkeye/internal/auth"
	"hawkeye/internal/billing"
	"hawkeye/internal/branding"
	"hawkeye/internal/contact"
	"hawkeye/internal/domains"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/ratelimit"
	"hawkeye/internal/regions"
	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
	"hawkeye/internal/statuspage"
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
	Maintenance   *maintenance.Service
	Domains       *domains.Service
	Regions       regions.Set
	StatusPages   *statuspage.Service
	Billing       *billing.Service
	Account       *account.Service
	Contact       *contact.Service
	Lifecycle     *lifecycle.Service // unsubscribe from onboarding emails; nil turns the endpoint off
	Branding      *branding.Service  // report branding and PDF reports; nil turns those endpoints off
	SSLCheck      *sslcheck.Service  // free SSL checker; nil turns the endpoint off
	SiteCheck     *sitecheck.Service // free website status checker; nil turns the endpoint off
	PgPool        *pgxpool.Pool
	Pool          plans.DB

	SecureCookies       bool
	AuthRateLimitPerMin int
	TrustForwardedFor   bool

	// Production hardening. All optional: nil/zero values keep development and tests simple.
	HSTS                    bool                // send Strict-Transport-Security (serve HTTPS only)
	Metrics                 *metrics.APIMetrics // request metrics (exposed on a separate private port by the caller)
	Limiter                 *ratelimit.Limiter  // shared (database-backed) rate limits; nil falls back to per-process limits
	GlobalRateLimitPerMin   int                 // per-IP requests per minute for the whole API (per process); 0 disables
	EmailActionLimitPerHour int                 // emails per hour per target address for forgot-password/confirmations
	Errors                  errtrack.Reporter   // error tracking; nil = disabled
}

const readinessTimeout = 2 * time.Second

// New builds the Echo application.
func New(o Options) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	if o.Errors == nil {
		o.Errors = errtrack.Noop{}
	}
	e.HTTPErrorHandler = errorHandler(o.Logger, o.Errors)
	// Client IPs drive rate limiting, so X-Forwarded-For is ignored unless a trusted proxy is declared.
	if o.TrustForwardedFor {
		e.IPExtractor = echo.ExtractIPFromXFFHeader()
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}

	e.Use(middleware.RequestID())
	if o.Metrics != nil {
		e.Use(apiMetrics(o.Metrics))
	}
	e.Use(requestLogger(o.Logger))
	e.Use(recoverPanics(o.Logger, o.Metrics, o.Errors))
	e.Use(middleware.BodyLimit(o.BodyLimit))
	e.Use(securityHeaders(o.HSTS))
	e.Use(middleware.ContextTimeoutWithConfig(middleware.ContextTimeoutConfig{Timeout: o.RequestTimeout}))

	e.GET("/healthz", healthz)
	e.GET("/readyz", readyz(o.DB, o.Logger))

	if o.Billing != nil {
		// The provider calls this endpoint, not a browser: no session, no CSRF token. It is authenticated by the signature
		// over the raw body, which is checked before anything is stored. Registered before the CSRF-protected group.
		e.POST("/api/v1/webhooks/stripe", (&handlers{billing: o.Billing, log: o.Logger}).stripeWebhook)
	}
	if o.Auth != nil && o.Orgs != nil && o.Plans != nil && o.Monitors != nil && o.Notifications != nil && o.PgPool != nil && o.Billing != nil && o.Account != nil {
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
	h := &handlers{auth: o.Auth, orgs: o.Orgs, plans: o.Plans, monitors: o.Monitors, pool: o.Pool, db: o.PgPool, notifications: o.Notifications, maintenance: o.Maintenance, domains: o.Domains, regions: o.Regions, statusPages: o.StatusPages, billing: o.Billing, account: o.Account, contact: o.Contact, lifecycle: o.Lifecycle, branding: o.Branding, sslcheck: o.SSLCheck, sitecheck: o.SiteCheck, log: o.Logger, secureCookies: o.SecureCookies}

	v1 := e.Group("/api/v1", middleware.CSRFWithConfig(middleware.CSRFConfig{
		// A mail client's one-click unsubscribe is a bare POST with no cookies, so it cannot carry a CSRF token. Nothing is lost: the endpoint uses no
		// session, and the unguessable token in the URL is the only thing that authorises it (and all it can do is unsubscribe).
		Skipper:        func(c echo.Context) bool { return c.Path() == "/api/v1/lifecycle/unsubscribe" },
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
	emailPerHour := o.EmailActionLimitPerHour
	if emailPerHour < 1 {
		emailPerHour = 3
	}
	h.emailPerHour = emailPerHour
	// limit builds the per-IP limiter for one endpoint. With a shared limiter the budget holds across all API processes;
	// without one (tests, single process) an in-memory limiter is used.
	var limit func(scope string) echo.MiddlewareFunc
	if o.Limiter != nil {
		deps := limiterDeps{l: o.Limiter, m: o.Metrics, log: o.Logger}
		h.limits = deps
		limit = func(scope string) echo.MiddlewareFunc { return deps.byIP("route:"+scope, perMin, time.Minute) }
	} else {
		mem := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
				Rate: rate.Limit(float64(perMin) / 60), Burst: perMin, ExpiresIn: time.Hour,
			}),
			IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         func(echo.Context, string, error) error { return errRateLimited },
		})
		limit = func(string) echo.MiddlewareFunc { return mem }
	}
	// A generous per-process, per-IP ceiling for the whole API absorbs floods before they reach handlers.
	if o.GlobalRateLimitPerMin > 0 {
		v1.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
				Rate: rate.Limit(float64(o.GlobalRateLimitPerMin) / 60), Burst: max(20, o.GlobalRateLimitPerMin/6), ExpiresIn: time.Hour,
			}),
			IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler: func(c echo.Context, _ string, _ error) error {
				c.Response().Header().Set("Retry-After", "30")
				if o.Metrics != nil {
					o.Metrics.RateLimited.WithLabelValues("global").Inc()
				}
				return errRateLimited
			},
		}))
	}

	a := v1.Group("/auth")
	a.POST("/register", h.register, limit("register"))
	a.POST("/login", h.login, limit("login"))
	a.POST("/forgot-password", h.forgotPassword, limit("forgot"))
	a.POST("/reset-password", h.resetPassword, limit("reset"))
	a.POST("/verify-email", h.verifyEmail, limit("verify"))
	a.POST("/logout", h.logout)
	a.POST("/change-password", h.changePassword, h.requireAuth, limit("change-password"))
	a.GET("/me", h.me, h.requireAuth)
	a.POST("/resend-verification", h.resendVerification, h.requireAuth, limit("resend"))

	v1.GET("/plans", h.listPlans)
	// The public status page: unauthenticated, so it gets its own, much higher, per-IP budget (it is also cached for 30 s).
	var publicLimit echo.MiddlewareFunc
	if h.limits.l != nil {
		publicLimit = h.limits.byIP("public-status", 120, time.Minute)
	} else {
		publicLimit = middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store:               middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: rate.Limit(2), Burst: 120, ExpiresIn: time.Hour}),
			IdentifierExtractor: func(c echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         func(echo.Context, string, error) error { return errRateLimited },
		})
	}
	v1.GET("/public/status/:slug", h.publicStatus, publicLimit)
	// Public contact form: per-IP burst and hourly caps, a spam trap, and per-address limits on the acknowledgement email.
	contactLimits := []echo.MiddlewareFunc{limit("contact")}
	if h.limits.l != nil {
		contactLimits = append(contactLimits, h.limits.byIP("contact:hour", 5, time.Hour))
	}
	// Free tools. They connect to a host the visitor names, so they have the tightest per-IP budgets of any public endpoint: a short burst
	// allowance and an hourly cap each, on top of the per-host limit and concurrency cap inside the checkers (internal/toolguard).
	toolLimits := func(scope string) []echo.MiddlewareFunc {
		ms := []echo.MiddlewareFunc{limit(scope)}
		if h.limits.l != nil {
			ms = append(ms, h.limits.byIP(scope+":hour", 40, time.Hour))
		}
		return ms
	}
	v1.POST("/tools/ssl-check", h.sslCheck, toolLimits("ssl-check")...)
	v1.POST("/tools/site-check", h.siteCheck, toolLimits("site-check")...)
	v1.GET("/contact/categories", h.contactCategories)
	v1.POST("/contact", h.submitContact, contactLimits...)
	// Called from the confirmation email by a recipient who may not have an account.
	v1.POST("/notification-settings/confirm", h.confirmSetting, limit("confirm-alert"))
	// Called from the footer of an onboarding email, or by a mail client's own "Unsubscribe" button; the person need not be signed in.
	if h.lifecycle != nil {
		v1.POST("/lifecycle/unsubscribe", h.unsubscribeLifecycle, limit("unsubscribe"))
	}

	// Tenant-scoped resources act on the organization named by X-Organization-ID (membership is
	// verified) or, by default, the caller's oldest organization.
	// Account-level (not organization-level) privacy endpoints.
	acct := v1.Group("/account", h.requireAuth)
	acct.GET("/export", h.exportData, limit("account-export"))
	acct.POST("/delete", h.deleteAccount, limit("account-delete"))

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
	if h.branding != nil {
		// PDF reports and their branding (Pro and Agency; the plan is checked in each handler).
		scoped.GET("/branding", h.brandingView)
		scoped.PUT("/branding", h.putBranding, requireOwner)
		scoped.GET("/branding/logo", h.getLogo)
		scoped.PUT("/branding/logo", h.putLogo, requireOwner, limit("branding"))
		scoped.DELETE("/branding/logo", h.deleteLogo, requireOwner)
		scoped.GET("/monitors/:monitorID/report.pdf", h.monitorReportPDF, limit("report"))
	}
	scoped.GET("/monitors/:monitorID/results", h.monitorResults)
	scoped.GET("/monitors/:monitorID/uptime", h.monitorUptime)
	scoped.GET("/monitors/:monitorID/response-times", h.monitorResponseTimes)
	scoped.GET("/dashboard/summary", h.dashboardSummary)
	scoped.GET("/dashboard/recent-incidents", h.dashboardRecentIncidents)
	scoped.GET("/dashboard/response-times", h.dashboardResponseTimes)

	// Alert recipients: any member can view; only owners change them; adding a recipient needs a verified
	// email and is rate-limited because it causes mail to be sent to a third party.
	scoped.GET("/regions", h.listRegions)
	scoped.GET("/monitors/:monitorID/regions", h.monitorRegions)
	scoped.GET("/status-page", h.getStatusPage)
	scoped.PUT("/status-page", h.putStatusPage, requireOwner)
	scoped.DELETE("/status-page", h.deleteStatusPage, requireOwner)
	scoped.GET("/maintenance-windows", h.listMaintenance)
	scoped.POST("/maintenance-windows", h.createMaintenance, requireOwner)
	scoped.DELETE("/maintenance-windows/:windowID", h.deleteMaintenance, requireOwner)
	scoped.GET("/domains", h.listDomains)
	scoped.POST("/domains", h.createDomain, requireOwner)
	scoped.DELETE("/domains/:domainID", h.deleteDomain, requireOwner)
	scoped.GET("/notification-settings", h.listSettings)
	scoped.POST("/notification-settings", h.createSetting, requireOwner, requireVerifiedEmail, limit("create-alert"))
	scoped.PATCH("/notification-settings/:settingID", h.updateSetting, requireOwner)
	scoped.DELETE("/notification-settings/:settingID", h.deleteSetting, requireOwner)
	scoped.POST("/notification-settings/:settingID/test", h.testSetting, requireOwner, requireVerifiedEmail, limit("create-alert"))
	scoped.POST("/notification-settings/:settingID/resend-confirmation", h.resendSettingConfirmation, requireOwner, requireVerifiedEmail, limit("resend-alert"))

	org := v1.Group("/organizations", h.requireAuth)
	org.GET("", h.listOrgs)
	org.GET("/:organizationID", h.getOrg)
	org.PATCH("/:organizationID", h.updateOrg)
	org.GET("/:organizationID/members", h.listMembers)

	// Group middleware (authentication, CSRF) also guards each group's catch-all, which made unknown paths answer 401/403 to
	// anonymous callers. Unknown API paths are simply 404 for everyone.
	e.RouteNotFound("/api/v1/*", func(echo.Context) error {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Not found."}
	})
}
