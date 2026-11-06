package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"hawkeye/internal/errtrack"
	"hawkeye/internal/metrics"
	"hawkeye/internal/ratelimit"
)

// apiMetrics records request counts and latency using the route TEMPLATE (e.g. /api/v1/monitors/:monitorID), so label cardinality stays bounded.
func apiMetrics(m *metrics.APIMetrics) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			start := time.Now()
			m.InFlight.Inc()
			err := next(c)
			m.InFlight.Dec()
			_, status := echo.ResolveResponseStatus(c.Response(), err)
			m.Observe(c.Request().Method, c.Path(), status, time.Since(start))
			return err
		}
	}
}

// panicError marks an error that came from a recovered panic, which has already been logged and reported.
type panicError struct{ error }

// recoverPanics turns a panic into a 500 (via the normal error handler), counts it, logs the stack and reports it.
func recoverPanics(log *slog.Logger, m *metrics.APIMetrics, rep errtrack.Reporter) echo.MiddlewareFunc {
	recoverer := middleware.Recover()
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		guarded := recoverer(next)
		return func(c *echo.Context) error {
			err := guarded(c)
			var p *middleware.PanicStackError
			if !errors.As(err, &p) {
				return err
			}
			if m != nil {
				m.Panics.Inc()
			}
			rid := c.Response().Header().Get(echo.HeaderXRequestID)
			log.ErrorContext(c.Request().Context(), "panic recovered in request handler", "error", p.Err, "request_id", rid, "stack", string(p.Stack))
			rep.Capture(c.Request().Context(), fmt.Errorf("panic: %w", p.Err), map[string]string{"request_id": rid, "route": c.Path(), "kind": "panic"})
			return panicError{p.Err}
		}
	}
}

// securityHeaders sets headers appropriate for a JSON API. HSTS is only sent when the deployment serves HTTPS.
func securityHeaders(hsts bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			h := c.Response().Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Cache-Control", "no-store")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			return next(c)
		}
	}
}

var errRateLimited = &APIError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Too many requests. Try again later."}

// limiterDeps groups what the shared rate limiter needs.
type limiterDeps struct {
	l   *ratelimit.Limiter
	m   *metrics.APIMetrics
	log *slog.Logger
}

// byIP limits each client IP to `limit` requests per window within `scope`, across every API process.
// If the limiter's database is unavailable the request is allowed (fail open) and the failure is counted, because
// refusing all logins during a database blip would be worse than briefly losing the limit.
func (d limiterDeps) byIP(scope string, limit int, window time.Duration) echo.MiddlewareFunc {
	return d.by(scope, limit, window, func(c *echo.Context) string { return c.RealIP() })
}

func (d limiterDeps) by(scope string, limit int, window time.Duration, subject func(*echo.Context) string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if !d.allow(c, scope, subject(c), limit, window) {
				return errRateLimited
			}
			return next(c)
		}
	}
}

// allow records a hit and reports whether it is within the limit, setting Retry-After when it is not.
func (d limiterDeps) allow(c *echo.Context, scope, subject string, limit int, window time.Duration) bool {
	res, err := d.l.Allow(c.Request().Context(), ratelimit.Key(scope, subject), limit, window)
	if err != nil {
		d.log.WarnContext(c.Request().Context(), "rate limiter unavailable; allowing request", "scope", scope, "error", err)
		if d.m != nil {
			d.m.LimiterErrs.Inc()
		}
		return true
	}
	if res.Allowed {
		return true
	}
	secs := int(res.RetryAfter/time.Second) + 1
	c.Response().Header().Set("Retry-After", strconv.Itoa(secs))
	if d.m != nil {
		d.m.RateLimited.WithLabelValues(scope).Inc()
	}
	return false
}

// emailBudget consumes one unit of an address's hourly budget for an email-sending action. It returns false when the address has
// used its budget. It never sets response headers, so callers can stay silent (no account enumeration) or return 429 as they prefer.
// Without a shared limiter it allows everything (single-process development).
func (h *handlers) emailBudget(c *echo.Context, action, address string) bool {
	if h.limits.l == nil {
		return true
	}
	res, err := h.limits.l.Allow(c.Request().Context(), ratelimit.Key("email:"+action, address), h.emailPerHour, time.Hour)
	if err != nil {
		h.limits.log.WarnContext(c.Request().Context(), "email limiter unavailable; allowing", "action", action, "error", err)
		if h.limits.m != nil {
			h.limits.m.LimiterErrs.Inc()
		}
		return true
	}
	if !res.Allowed && h.limits.m != nil {
		h.limits.m.EmailSkips.WithLabelValues(action).Inc()
	}
	return res.Allowed
}

// loginAttemptsPerHour caps login attempts per account across all IPs, to slow credential stuffing that rotates addresses.
// Trade-off (documented in docs/security.md): someone can make a victim wait by hammering their address; password reset still works.
const loginAttemptsPerHour = 30
