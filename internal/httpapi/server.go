// Package httpapi wires the Echo application: middleware, error handling and routes.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"hawkeye/internal/account"
	"hawkeye/internal/apitokens"
	"hawkeye/internal/auth"
	"hawkeye/internal/badges"
	"hawkeye/internal/billing"
	"hawkeye/internal/branding"
	"hawkeye/internal/contact"
	"hawkeye/internal/domains"
	"hawkeye/internal/errtrack"
	"hawkeye/internal/heartbeats"
	"hawkeye/internal/invitations"
	"hawkeye/internal/lifecycle"
	"hawkeye/internal/maintenance"
	"hawkeye/internal/metrics"
	"hawkeye/internal/monitors"
	"hawkeye/internal/notifications"
	"hawkeye/internal/oauth"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/ratelimit"
	"hawkeye/internal/regions"
	"hawkeye/internal/sitecheck"
	"hawkeye/internal/sslcheck"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/twofactor"
)

// Pinger reports whether a dependency (PostgreSQL) is usable.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Options struct {
	Logger         *slog.Logger
	DB             Pinger
	RequestTimeout time.Duration
	BodyLimit      int64 // bytes

	// Auth and Orgs are optional so the foundation can be tested without a database.
	Auth          *auth.Service
	Orgs          *organizations.Service
	Plans         *plans.Service
	Monitors      *monitors.Service
	Notifications *notifications.Service
	TelegramBot   *notifications.TelegramBot // HawkEye's own Telegram bot; nil turns its webhook off
	Maintenance   *maintenance.Service
	Domains       *domains.Service
	Regions       regions.Set
	StatusPages   *statuspage.Service
	Badges        *badges.Service     // public uptime badges; nil turns them off
	Heartbeats    *heartbeats.Service // heartbeat pings (/ping/<token>) and ping logs; nil turns them off
	Billing       *billing.Service
	Account       *account.Service
	Contact       *contact.Service
	Invitations   *invitations.Service // team invitations; nil turns those endpoints off
	Lifecycle     *lifecycle.Service   // unsubscribe from onboarding emails; nil turns the endpoint off
	Branding      *branding.Service    // report branding and PDF reports; nil turns those endpoints off
	SSLCheck      *sslcheck.Service    // free SSL checker; nil turns the endpoint off
	SiteCheck     *sitecheck.Service   // free website status checker; nil turns the endpoint off
	APITokens     *apitokens.Service   // API keys; nil makes key requests answer 503 (DISABLE_API_KEYS) and sessions work as before
	TwoFactor     *twofactor.Service   // two-factor sign-in settings and organization requirements
	OAuth         *oauth.Service       // OAuth for the MCP server (connecting Claude); nil turns it off
	AppBaseURL    string               // public address of the app (status page and ping addresses in MCP tool results)
	Version       string               // reported by the MCP server
	MCPOrigins    []string             // browser origins allowed to call /mcp; nil means DefaultMCPOrigins
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
	if o.TelegramBot != nil && o.Notifications != nil {
		// Called by Telegram with the bot's updates; authenticated by the webhook secret. Registered outside the CSRF-protected group.
		e.POST(notifications.TelegramWebhookPath, (&handlers{notifications: o.Notifications, telegramBot: o.TelegramBot, log: o.Logger}).telegramWebhook)
	}
	if o.Auth != nil && o.Orgs != nil && o.Plans != nil && o.Monitors != nil && o.Notifications != nil && o.PgPool != nil && o.Billing != nil && o.Account != nil {
		registerAPI(e, o)
	}
	return e
}

// healthz is a liveness probe: it does not touch dependencies.
func healthz(c *echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// readyz verifies that required dependencies are usable. Failure details are logged, not returned.
func readyz(db Pinger, log *slog.Logger) echo.HandlerFunc {
	return func(c *echo.Context) error {
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
		LogMethod: true, LogURIPath: true, LogStatus: true, LogLatency: true, LogRequestID: true,
		HandleError: true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
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
	h := &handlers{auth: o.Auth, orgs: o.Orgs, plans: o.Plans, monitors: o.Monitors, pool: o.Pool, db: o.PgPool, notifications: o.Notifications, maintenance: o.Maintenance, domains: o.Domains, regions: o.Regions, statusPages: o.StatusPages, badges: o.Badges, heartbeats: o.Heartbeats, billing: o.Billing, account: o.Account, contact: o.Contact, invitations: o.Invitations, lifecycle: o.Lifecycle, branding: o.Branding, sslcheck: o.SSLCheck, sitecheck: o.SiteCheck, log: o.Logger, secureCookies: o.SecureCookies,
		apiTokens: o.APITokens, twoFactor: o.TwoFactor, oauth: o.OAuth, tokenRoutes: map[string]bool{}}

	v1 := e.Group("/api/v1", middleware.CSRFWithConfig(middleware.CSRFConfig{
		// A mail client's one-click unsubscribe is a bare POST with no cookies, so it cannot carry a CSRF token. Nothing is lost: the endpoint uses no
		// session, and the unguessable token in the URL is the only thing that authorizes it (and all it can do is unsubscribe).
		// API requests authenticate with a key in the Authorization header, which a browser never attaches on its own, so they need no CSRF token. They
		// skip it only on routes open to keys: anywhere else the header gets 403 SESSION_REQUIRED and the cookie is never used (see tokens.go).
		Skipper: func(c *echo.Context) bool {
			return c.Path() == "/api/v1/lifecycle/unsubscribe" || (hasAuthorization(c) && h.opensToTokens(c))
		},
		TokenLookup:    "header:X-CSRF-Token",
		CookieName:     "csrf_token",
		CookiePath:     "/",
		CookieSecure:   o.SecureCookies,
		CookieHTTPOnly: false,
		CookieSameSite: http.SameSiteLaxMode,
		CookieMaxAge:   86400,
		ErrorHandler: func(c *echo.Context, _ error) error {
			if hasAuthorization(c) {
				return errSessionRequired // an API key on a route not open to keys: say so, rather than asking a script for a CSRF token
			}
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
				Rate: float64(perMin) / 60, Burst: perMin, ExpiresIn: time.Hour,
			}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         func(*echo.Context, string, error) error { return errRateLimited },
		})
		limit = func(string) echo.MiddlewareFunc { return mem }
	}
	// A generous per-process, per-IP ceiling for the whole API absorbs floods before they reach handlers.
	if o.GlobalRateLimitPerMin > 0 {
		v1.Use(middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store: middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{
				Rate: float64(o.GlobalRateLimitPerMin) / 60, Burst: max(20, o.GlobalRateLimitPerMin/6), ExpiresIn: time.Hour,
			}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler: func(c *echo.Context, _ string, _ error) error {
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
	a.POST("/login/two-factor", h.loginTwoFactor, limit("login-two-factor"))
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
			Store:               middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: 2, Burst: 120, ExpiresIn: time.Hour}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         func(*echo.Context, string, error) error { return errRateLimited },
		})
	}
	v1.GET("/public/status/:slug", h.publicStatus, publicLimit)
	v1.GET("/public/status/:slug/logo", h.publicStatusLogo, publicLimit)
	if h.badges != nil {
		// Uptime badges are embedded on other sites and in READMEs, so they live at a short address outside /api and outside its CSRF and global
		// limits. Requests come largely from a few image proxies (GitHub's camo, for one), so the per-IP budget is generous and kept in memory: a
		// badge is served from a one-minute cache and never costs a rate-limiter query.
		e.GET("/badge/:file", h.publicBadge, middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store:               middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: 20, Burst: 600, ExpiresIn: time.Hour}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         func(*echo.Context, string, error) error { return errRateLimited },
		}))
	}
	if h.heartbeats != nil {
		// Heartbeat pings come from customers' scheduled jobs, so like the badges they live at a short address outside /api, its CSRF check and its
		// global limit. Many jobs may share one server (and one address), so the per-IP budget is generous; one ping address may not be hit more than
		// about once a second, which no real schedule needs. Both are kept in memory so a ping never costs a rate-limiter query.
		deny := func(c *echo.Context, _ string, _ error) error {
			c.Response().Header().Set("Retry-After", "10")
			return c.String(http.StatusTooManyRequests, "Too many pings\n")
		}
		perIP := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store:               middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: 20, Burst: 600, ExpiresIn: time.Hour}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.RealIP(), nil },
			DenyHandler:         deny,
		})
		perToken := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
			Store:               middleware.NewRateLimiterMemoryStoreWithConfig(middleware.RateLimiterMemoryStoreConfig{Rate: 1, Burst: 30, ExpiresIn: time.Hour}),
			IdentifierExtractor: func(c *echo.Context) (string, error) { return c.Param("token"), nil },
			DenyHandler:         deny,
		})
		methods := []string{http.MethodGet, http.MethodHead, http.MethodPost}
		e.Match(methods, "/ping/:token", h.ping, perIP, perToken)
		e.Match(methods, "/ping/:token/:status", h.ping, perIP, perToken)
	}
	// Public contact form: per-IP burst and hourly caps, a spam trap, and per-address limits on the acknowledgment email.
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
	acct.GET("/two-factor", h.twoFactorStatus)
	acct.POST("/two-factor/setup", h.twoFactorSetup, limit("two-factor"))
	acct.POST("/two-factor/enable", h.twoFactorEnable, limit("two-factor"))
	acct.POST("/two-factor/disable", h.twoFactorDisable, limit("two-factor"))
	acct.POST("/two-factor/recovery-codes", h.twoFactorRecoveryCodes, limit("two-factor"))
	if h.lifecycle != nil {
		acct.GET("/email-preferences", h.emailPreferences)
		acct.PUT("/email-preferences", h.putEmailPreferences)
	}

	// Tenant-scoped routes take a session or an API key. Routes registered through api are open to keys; routes registered on scoped directly are
	// session-only (billing changes, alert recipients, API key management), as is every other group.
	scoped := v1.Group("", h.authenticate, h.resolveOrg)
	api := tokenGroup{g: scoped, h: h}
	api.GET("/usage", h.usage)
	api.GET("/monitors", h.listMonitors)
	api.POST("/monitors", h.createMonitor)
	api.POST("/monitors/bulk", h.bulkMonitors)
	api.GET("/tags", h.listTags)
	api.POST("/tags", h.createTag)
	api.PATCH("/tags/:tagID", h.updateTag)
	api.DELETE("/tags/:tagID", h.deleteTag)
	api.GET("/monitors/:monitorID", h.getMonitor)
	api.PATCH("/monitors/:monitorID", h.updateMonitor)
	api.DELETE("/monitors/:monitorID", h.deleteMonitor)
	api.POST("/monitors/:monitorID/pause", h.pauseMonitor)
	api.POST("/monitors/:monitorID/resume", h.resumeMonitor)
	if h.badges != nil {
		api.POST("/monitors/:monitorID/badge", h.enableBadge, requireOwner)
		api.DELETE("/monitors/:monitorID/badge", h.disableBadge, requireOwner)
	}
	if h.heartbeats != nil {
		api.GET("/monitors/:monitorID/pings", h.heartbeatPings)
		api.POST("/monitors/:monitorID/ping-token", h.rotatePingToken)
	}
	api.GET("/monitors/:monitorID/incidents", h.listMonitorIncidents)
	api.GET("/incidents", h.listIncidents)
	api.GET("/incidents/:incidentID/updates", h.listIncidentUpdates)
	api.POST("/incidents/:incidentID/updates", h.postIncidentUpdate, requireOwner, limit("incident-update"))
	api.DELETE("/incidents/:incidentID/updates/:updateID", h.deleteIncidentUpdate, requireOwner)

	// Billing: any member can see the plan; changes need the owner and a verified email.
	api.GET("/billing/subscription", h.billingSubscription)
	scoped.POST("/billing/checkout-session", h.billingCheckout, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/portal-session", h.billingPortal, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/cancel-subscription", h.billingCancel, requireOwner, requireVerifiedEmail)
	scoped.POST("/billing/sync", h.billingSync, requireOwner)
	if h.branding != nil {
		// PDF reports and their branding (Pro and Agency; the plan is checked in each handler).
		api.GET("/branding", h.brandingView)
		api.PUT("/branding", h.putBranding, requireOwner)
		api.GET("/branding/logo", h.getLogo)
		api.PUT("/branding/logo", h.putLogo, requireOwner, limit("branding"))
		api.DELETE("/branding/logo", h.deleteLogo, requireOwner)
		api.GET("/monitors/:monitorID/report.pdf", h.monitorReportPDF, limit("report"))
	}
	api.GET("/monitors/:monitorID/results", h.monitorResults)
	api.GET("/monitors/:monitorID/uptime", h.monitorUptime)
	api.GET("/monitors/:monitorID/response-times", h.monitorResponseTimes)
	api.GET("/dashboard/summary", h.dashboardSummary)
	api.GET("/dashboard/recent-incidents", h.dashboardRecentIncidents)
	api.GET("/dashboard/response-times", h.dashboardResponseTimes)

	// Alert recipients: any member can view; only owners change them; adding a recipient needs a verified
	// email and is rate-limited because it causes mail to be sent to a third party.
	api.GET("/regions", h.listRegions)
	api.GET("/monitors/:monitorID/regions", h.monitorRegions)
	api.GET("/status-pages", h.listStatusPages)
	api.POST("/status-pages", h.createStatusPage, requireOwner)
	api.GET("/status-pages/:pageID", h.getStatusPage)
	api.PUT("/status-pages/:pageID", h.updateStatusPage, requireOwner)
	api.DELETE("/status-pages/:pageID", h.deleteStatusPage, requireOwner)
	api.GET("/status-pages/:pageID/logo", h.getStatusPageLogo)
	api.PUT("/status-pages/:pageID/logo", h.putStatusPageLogo, requireOwner, limit("branding"))
	api.DELETE("/status-pages/:pageID/logo", h.deleteStatusPageLogo, requireOwner)
	api.GET("/maintenance-windows", h.listMaintenance)
	api.POST("/maintenance-windows", h.createMaintenance, requireOwner)
	api.DELETE("/maintenance-windows/:windowID", h.deleteMaintenance, requireOwner)
	api.GET("/domains", h.listDomains)
	api.POST("/domains", h.createDomain, requireOwner)
	api.DELETE("/domains/:domainID", h.deleteDomain, requireOwner)
	scoped.GET("/notification-settings", h.listSettings) // lists alert destinations, so it stays session-only
	scoped.POST("/notification-settings", h.createSetting, requireOwner, requireVerifiedEmail, limit("create-alert"))
	scoped.POST("/notification-settings/telegram-link", h.telegramLink, requireOwner, requireVerifiedEmail, limit("create-alert"))
	// A member may turn off or remove their own address (the handlers restrict them to it); everything else about recipients is the owner's.
	scoped.PATCH("/notification-settings/:settingID", h.updateSetting)
	scoped.PUT("/notification-settings/:settingID/routing", h.setRouting, requireOwner)
	api.GET("/notification-settings/coverage", h.alertCoverage)
	scoped.DELETE("/notification-settings/:settingID", h.deleteSetting)
	scoped.POST("/notification-settings/:settingID/test", h.testSetting, requireOwner, requireVerifiedEmail, limit("create-alert"))
	scoped.POST("/notification-settings/:settingID/resend-confirmation", h.resendSettingConfirmation, requireOwner, requireVerifiedEmail, limit("resend-alert"))

	// API key management needs a session: a key can't make, list or revoke keys. With keys turned off these answer 503, so the page can say why.
	scoped.GET("/api-keys", h.listAPIKeys)
	scoped.POST("/api-keys", h.createAPIKey, requireVerifiedEmail, limit("api-key"))
	scoped.DELETE("/api-keys/:keyID", h.revokeAPIKey)

	org := v1.Group("/organizations", h.requireAuth)
	org.GET("", h.listOrgs)
	org.GET("/:organizationID", h.getOrg)
	org.PATCH("/:organizationID", h.updateOrg)
	org.GET("/:organizationID/members", h.listMembers)
	org.DELETE("/:organizationID/members/:userID", h.removeMember)
	org.POST("/:organizationID/leave", h.leaveOrg)
	org.GET("/:organizationID/two-factor", h.orgTwoFactor)
	org.PUT("/:organizationID/two-factor", h.setOrgTwoFactor, requireVerifiedEmail)
	if h.invitations != nil {
		// Team invitations. The owner sends them (a verified address, and capped per target address because each one emails a third party);
		// the invitee previews a link without signing in and accepts it with the account that has the invited address.
		org.GET("/:organizationID/invitations", h.listInvitations)
		org.POST("/:organizationID/invitations", h.createInvitation, requireVerifiedEmail, limit("invite"))
		org.POST("/:organizationID/invitations/:invitationID/resend", h.resendInvitation, requireVerifiedEmail, limit("invite-resend"))
		org.DELETE("/:organizationID/invitations/:invitationID", h.revokeInvitation)
		org.POST("/:organizationID/transfer", h.transferOwnership, requireVerifiedEmail, limit("transfer"))
		v1.POST("/invitations/preview", h.previewInvitation, limit("invite-preview"))
		v1.POST("/invitations/accept", h.acceptInvitation, h.requireAuth, limit("invite-accept"))
	}

	// The MCP server (mcp.go): API keys and OAuth access tokens only, outside /api/v1 and its CSRF check. Stateless, so GET and DELETE answer 405.
	e.Match([]string{http.MethodPost, http.MethodGet, http.MethodDelete}, "/mcp", h.mcpEndpoint(o))

	// OAuth for the MCP server (oauth.go). Protocol endpoints are outside /api/v1: clients send forms and follow redirects, with no session or CSRF
	// token. The token and revocation endpoints get their own per-IP budget.
	e.GET("/.well-known/oauth-authorization-server", h.authorizationServerMetadata)
	e.GET("/.well-known/oauth-protected-resource", h.protectedResourceMetadata)
	e.GET("/.well-known/oauth-protected-resource/mcp", h.protectedResourceMetadata)
	e.GET("/oauth/authorize", h.authorize, limit("oauth-authorize"))
	var tokenLimit echo.MiddlewareFunc = limit("oauth-token")
	if h.limits.l != nil {
		tokenLimit = h.limits.byIP("oauth-token", oauthTokenPerMin, time.Minute)
	}
	e.POST("/oauth/token", h.token, tokenLimit)
	e.POST("/oauth/revoke", h.revokeToken, tokenLimit)
	// The consent page's API and connected apps: session only.
	v1.GET("/oauth/authorize", h.oauthAuthorizeInfo, h.requireAuth)
	v1.POST("/oauth/authorize", h.oauthApprove, h.requireAuth, limit("oauth-consent"))
	scoped.GET("/oauth/connected-apps", h.listGrants)
	scoped.DELETE("/oauth/connected-apps/:grantID", h.revokeGrant)

	// Group middleware (authentication, CSRF) also guards each group's catch-all, which made unknown paths answer 401/403 to
	// anonymous callers. Unknown API paths are simply 404 for everyone.
	e.RouteNotFound("/api/v1/*", func(*echo.Context) error {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Not found."}
	})
}
