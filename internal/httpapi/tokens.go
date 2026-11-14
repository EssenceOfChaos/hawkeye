package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/mcpserver"
)

// API keys (internal/apitokens). A request that sends an Authorization header is an API request: it authenticates with the key alone, never with the
// session cookie, and may only reach routes registered as open to keys (tokenGroup). Every other route answers 403 SESSION_REQUIRED, so a new route is
// closed to keys unless someone opens it on purpose (TestEveryRouteIsClassifiedForAPIKeys checks the whole table).

const principalKey = "api_principal"

// Per-key budgets (with the shared limiter), on top of the per-IP limits every request has.
const (
	tokenRequestsPerMin = 120
	tokenWritesPerMin   = mcpserver.WritesPerMinute // one budget for REST writes and MCP tool writes (same limiter key)
)

var (
	errSessionRequired = &APIError{Status: http.StatusForbidden, Code: "SESSION_REQUIRED",
		Message: "This endpoint can't be used with an API key. Sign in to HawkEye to use it."}
	errTokensDisabled = &APIError{Status: http.StatusServiceUnavailable, Code: "API_KEYS_DISABLED",
		Message: "API keys are turned off on this server right now. Try again later."}
	errNoCredentials = &APIError{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED",
		Message: "This is HawkEye's MCP server for Claude and other MCP clients, not a web page. Add this address as a connector in Claude, or send an API key in the Authorization header."}
	errTokenExpired = &APIError{Status: http.StatusUnauthorized, Code: "TOKEN_EXPIRED",
		Message: "This API key has expired. Create a new key in HawkEye and use that instead."}
	errTokenInvalid = &APIError{Status: http.StatusUnauthorized, Code: "UNAUTHENTICATED",
		Message: "This API key is not valid. Check that it was copied in full and hasn't been revoked."}
	errTokenAudience = &APIError{Status: http.StatusForbidden, Code: "TOKEN_AUDIENCE",
		Message: "This token was issued to a connected app for HawkEye's MCP server and can't be used with the API. Create an API key instead."}
	errTokenScope = &APIError{Status: http.StatusForbidden, Code: "TOKEN_SCOPE",
		Message: "This API key is read-only. Create a key with write access to make changes."}
	errTokenOrganization = &APIError{Status: http.StatusForbidden, Code: "TOKEN_ORGANIZATION_MISMATCH",
		Message: "This API key belongs to a different organization than the one in X-Organization-ID. Leave the header out, or use that organization's key."}
)

// hasAuthorization reports whether the request carries credentials in the Authorization header (of any scheme).
func hasAuthorization(c *echo.Context) bool {
	return c.Request().Header.Get(echo.HeaderAuthorization) != ""
}

func bearerToken(c *echo.Context) (string, bool) {
	scheme, token, ok := strings.Cut(c.Request().Header.Get(echo.HeaderAuthorization), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func currentPrincipal(c *echo.Context) (apitokens.Principal, bool) {
	p, ok := c.Get(principalKey).(apitokens.Principal)
	return p, ok
}

func tokenRouteKey(method, path string) string { return method + " " + path }

// createRoutes are the writes a key may make whatever the plan: creating monitors, and the suggestions that come before it. Everything else that
// changes data needs a plan with API writes. MCP's create_monitor and create_workflow follow the same rule (mcpserver, access.create).
var createRoutes = map[string]bool{
	tokenRouteKey(http.MethodPost, "/api/v1/monitors"):             true,
	tokenRouteKey(http.MethodPost, "/api/v1/monitors/suggestions"): true,
}

// opensToTokens reports whether the matched route accepts API keys.
func (h *handlers) opensToTokens(c *echo.Context) bool {
	return h.tokenRoutes[tokenRouteKey(c.Request().Method, c.Path())]
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// authenticate accepts an API key (Authorization: Bearer hk_…) on routes open to keys, and otherwise the session cookie exactly as requireAuth does.
func (h *handlers) authenticate(next echo.HandlerFunc) echo.HandlerFunc {
	session := h.requireAuth(next)
	return func(c *echo.Context) error {
		if !hasAuthorization(c) {
			return session(c)
		}
		if !h.opensToTokens(c) {
			return errSessionRequired
		}
		write := unsafeMethod(c.Request().Method)
		p, err := h.keyPrincipal(c, write)
		if err != nil {
			return err
		}
		// Tokens issued to OAuth clients (Claude's connector) are for the MCP server only, so a token handed to an assistant can't be replayed
		// against the wider REST API.
		if p.Kind == apitokens.KindOAuth {
			return errTokenAudience
		}
		ctx := c.Request().Context()
		if write {
			if !p.CanWrite() {
				return errTokenScope
			}
			// Checked on every write, so a key stops changing data after a downgrade and works again after an upgrade. Creating monitors (and asking for
			// suggestions first) is open on every plan, within the plan's own limits, so a coding agent can set up a Free account.
			if !createRoutes[tokenRouteKey(c.Request().Method, c.Path())] {
				if err := h.plans.CanWriteViaAPI(ctx, h.pool, p.OrganizationID); err != nil {
					return mapError(err)
				}
			}
		}
		c.Set(userKey, p.User)
		c.Set(principalKey, p)
		return next(c)
	}
}

// keyPrincipal authenticates the API key in the Authorization header and spends one unit of its request budget (and of its write budget when
// write is set). It is shared by the REST API and the MCP endpoint, so both refuse the same keys with the same errors.
func (h *handlers) keyPrincipal(c *echo.Context, write bool) (apitokens.Principal, error) {
	if h.apiTokens == nil {
		return apitokens.Principal{}, errTokensDisabled
	}
	if !hasAuthorization(c) {
		// Nothing was sent: most likely someone opened /mcp in a browser. Say what the address is for; the 401 and its header still start sign-in
		// for an MCP client.
		c.Response().Header().Set("WWW-Authenticate", h.bearerChallenge(c, ""))
		return apitokens.Principal{}, errNoCredentials
	}
	raw, ok := bearerToken(c)
	if !ok {
		c.Response().Header().Set("WWW-Authenticate", h.bearerChallenge(c, ""))
		return apitokens.Principal{}, errTokenInvalid
	}
	p, err := h.apiTokens.Authenticate(c.Request().Context(), raw)
	switch {
	case errors.Is(err, apitokens.ErrExpired):
		c.Response().Header().Set("WWW-Authenticate", h.bearerChallenge(c, `error="invalid_token", error_description="The API key has expired"`))
		return apitokens.Principal{}, errTokenExpired
	case errors.Is(err, apitokens.ErrInvalid):
		c.Response().Header().Set("WWW-Authenticate", h.bearerChallenge(c, `error="invalid_token"`))
		return apitokens.Principal{}, errTokenInvalid
	case errors.Is(err, apitokens.ErrTwoFactorRequired):
		return apitokens.Principal{}, mapError(err)
	case err != nil:
		return apitokens.Principal{}, err
	}
	if h.limits.l != nil {
		id := p.TokenID.String()
		if !h.limits.allow(c, "token", id, tokenRequestsPerMin, time.Minute) {
			return apitokens.Principal{}, errRateLimited
		}
		if write && !h.limits.allow(c, "token-write", id, tokenWritesPerMin, time.Minute) {
			return apitokens.Principal{}, errRateLimited
		}
	}
	return p, nil
}

// bearerChallenge is the WWW-Authenticate value for a refused key, with extra parameters (error=…) appended.
func (h *handlers) bearerChallenge(c *echo.Context, extra string) string {
	v := `Bearer realm="HawkEye API"`
	// On the MCP server, point OAuth clients at the metadata that tells them how to get a token (RFC 9728, MCP authorization).
	if h.oauth != nil && c.Path() == "/mcp" {
		v += `, resource_metadata="` + h.resourceMetadataURL() + `"`
	}
	if extra != "" {
		v += ", " + extra
	}
	return v
}

// resolveToken sets the organization and role for a request made with an API key: the key's organization, and the owner's role in it now.
// It reports false for session requests, which resolveOrg handles from X-Organization-ID.
func resolveToken(c *echo.Context) (bool, error) {
	p, ok := currentPrincipal(c)
	if !ok {
		return false, nil
	}
	if raw := c.Request().Header.Get("X-Organization-ID"); raw != "" {
		if id, err := uuid.Parse(raw); err != nil || id != p.OrganizationID {
			return true, errTokenOrganization
		}
	}
	c.Set(orgKey, p.OrganizationID)
	c.Set(roleKey, p.Role)
	return true, nil
}

// tokenGroup registers routes on a group and opens them to API keys. Routes registered on the group directly stay session-only.
type tokenGroup struct {
	g *echo.Group
	h *handlers
}

func (t tokenGroup) add(method, path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	ri := t.g.Add(method, path, fn, m...)
	t.h.tokenRoutes[tokenRouteKey(ri.Method, ri.Path)] = true
}

func (t tokenGroup) GET(path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	t.add(http.MethodGet, path, fn, m...)
}

func (t tokenGroup) POST(path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	t.add(http.MethodPost, path, fn, m...)
}

func (t tokenGroup) PUT(path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	t.add(http.MethodPut, path, fn, m...)
}

func (t tokenGroup) PATCH(path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	t.add(http.MethodPatch, path, fn, m...)
}

func (t tokenGroup) DELETE(path string, fn echo.HandlerFunc, m ...echo.MiddlewareFunc) {
	t.add(http.MethodDelete, path, fn, m...)
}
