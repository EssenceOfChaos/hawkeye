package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/mcpserver"
)

// The MCP endpoint (POST /mcp): HawkEye's tools for Claude and other MCP clients (internal/mcpserver).
//
// It sits outside /api/v1, so outside its CSRF check and session handling: it only ever takes an API key, authenticated by keyPrincipal exactly as
// the REST API does (same errors, same per-key budgets, same kill switch). The SDK runs stateless (no MCP sessions are kept) and answers in JSON.
// A browser page on another site can't call it with someone's key unless it has the key, but as a second guard, requests that carry an Origin
// header must come from an allowed origin (MCP's DNS-rebinding guidance); MCP clients that are not browsers send no Origin.

// DefaultMCPOrigins are the browser origins allowed to call /mcp (Claude's web apps). Other clients send no Origin header.
var DefaultMCPOrigins = []string{"https://claude.ai", "https://claude.com"}

var errMCPOrigin = &APIError{Status: http.StatusForbidden, Code: "ORIGIN_NOT_ALLOWED", Message: "Requests from this site are not allowed."}

type principalCtxKey struct{}

// verifyFromContext hands the principal keyPrincipal found to the SDK, which passes it to every tool call (mcpserver.Caller).
func verifyFromContext(_ context.Context, _ string, req *http.Request) (*auth.TokenInfo, error) {
	p, ok := req.Context().Value(principalCtxKey{}).(apitokens.Principal)
	if !ok {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{UserID: p.User.ID.String(), Extra: map[string]any{mcpserver.PrincipalKey: p}}, nil
}

func (h *handlers) mcpEndpoint(o Options) echo.HandlerFunc {
	srv := mcpserver.New(mcpserver.Deps{Pool: o.PgPool, Plans: o.Plans, Monitors: o.Monitors, Maintenance: o.Maintenance, StatusPages: o.StatusPages,
		Limiter: o.Limiter, Metrics: o.Metrics, Log: o.Logger, AppBaseURL: o.AppBaseURL, Version: o.Version, Audit: o.Audit,
		WorkflowChecker: o.WorkflowChecker, Discovery: o.Discovery})
	sdk := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	// Keys may have no expiry date (the person chose that), so the SDK must not insist on one; keyPrincipal has already refused expired keys.
	withCaller := auth.RequireBearerToken(verifyFromContext, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})(sdk)
	origins := o.MCPOrigins
	if origins == nil {
		origins = DefaultMCPOrigins
	}
	return func(c *echo.Context) error {
		if origin := c.Request().Header.Get("Origin"); origin != "" && !slices.Contains(origins, strings.TrimRight(origin, "/")) {
			return errMCPOrigin
		}
		// Every MCP call is a POST, so the per-key write budget is spent per tool call instead (mcpserver), not per request.
		p, err := h.keyPrincipal(c, false)
		if err != nil {
			return err
		}
		req := c.Request()
		withCaller.ServeHTTP(c.Response(), req.WithContext(context.WithValue(req.Context(), principalCtxKey{}, p)))
		return nil
	}
}
