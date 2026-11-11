package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/audit"
	"hawkeye/internal/oauth"
	"hawkeye/internal/plans"
	"hawkeye/internal/twofactor"
)

// OAuth for the MCP server (internal/oauth). The protocol endpoints (/oauth/*, /.well-known/*) sit outside /api/v1: clients call them with forms and
// redirects, not with a session and CSRF token. The consent step is a page in the app (/connect) that uses two session-only API routes.

// defaultOAuthTokenPerMin is the per-IP budget for the token and revocation endpoints when OAUTH_TOKEN_RATE_LIMIT_PER_MIN isn't set. Claude calls
// them from Anthropic's servers for all of its users, so the limit is generous: codes and refresh tokens are 256-bit random values, so it only has
// to stop floods, not guessing.
const defaultOAuthTokenPerMin = 1200

var errOAuthOff = &APIError{Status: http.StatusServiceUnavailable, Code: "OAUTH_UNAVAILABLE", Message: "Connecting apps isn't available on this server right now."}

// oauthJSON writes an OAuth error response (RFC 6749 §5.2).
func oauthJSON(c *echo.Context, e *oauth.Error) error {
	if e.Code == "invalid_client" && e.Status == http.StatusUnauthorized {
		c.Response().Header().Set("WWW-Authenticate", `Basic realm="HawkEye OAuth"`)
	}
	return c.JSON(e.Status, map[string]string{"error": e.Code, "error_description": e.Description})
}

func (h *handlers) oauthError(c *echo.Context, err error) error {
	var oe *oauth.Error
	if errors.As(err, &oe) {
		return oauthJSON(c, oe)
	}
	h.log.ErrorContext(c.Request().Context(), "OAuth endpoint failed", "path", c.Path(), "error", err)
	return oauthJSON(c, &oauth.Error{Code: "server_error", Description: "Something went wrong on HawkEye's side.", Status: http.StatusInternalServerError})
}

// authorizationServerMetadata is RFC 8414 metadata.
func (h *handlers) authorizationServerMetadata(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	iss := h.oauth.Issuer()
	return c.JSON(http.StatusOK, map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + "/oauth/authorize",
		"token_endpoint":                                 iss + "/oauth/token",
		"revocation_endpoint":                            iss + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post", "none"},
		"revocation_endpoint_auth_methods_supported":     []string{"client_secret_basic", "client_secret_post", "none"},
		"scopes_supported":                               []string{apitokens.ScopeRead, apitokens.ScopeWrite},
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          iss + "/docs/claude",
	})
}

// protectedResourceMetadata is RFC 9728 metadata for the MCP server.
func (h *handlers) protectedResourceMetadata(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	return c.JSON(http.StatusOK, map[string]any{
		"resource":                 h.oauth.Resource(),
		"authorization_servers":    []string{h.oauth.Issuer()},
		"scopes_supported":         []string{apitokens.ScopeRead, apitokens.ScopeWrite},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "HawkEye",
		"resource_documentation":   h.oauth.Issuer() + "/docs/claude",
	})
}

// resourceMetadataURL is where /mcp's 401 responses point clients to start OAuth.
func (h *handlers) resourceMetadataURL() string {
	return h.oauth.Issuer() + "/.well-known/oauth-protected-resource/mcp"
}

// authorize is the authorization endpoint. It checks the request and sends the person to the consent page in the app; problems with the client or
// its redirect URI are shown here (never redirected to an unverified address), anything else goes back to the client.
func (h *handlers) authorize(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	req := oauth.FromQuery(c.Request().URL.Query())
	v, err := h.oauth.Validate(c.Request().Context(), req)
	if err != nil {
		var oe *oauth.Error
		if errors.As(err, &oe) {
			return c.JSON(oe.Status, map[string]string{"error": oe.Code, "error_description": oe.Description})
		}
		return err
	}
	if v.RedirectError != nil {
		return c.Redirect(http.StatusFound, h.oauth.ErrorRedirect(v, v.RedirectError))
	}
	return c.Redirect(http.StatusFound, h.oauth.Issuer()+"/connect?"+v.Request.Query().Encode())
}

func clientAuth(c *echo.Context) oauth.ClientAuth {
	if id, secret, ok := c.Request().BasicAuth(); ok {
		return oauth.ClientAuth{ID: id, Secret: secret}
	}
	return oauth.ClientAuth{ID: c.FormValue("client_id"), Secret: c.FormValue("client_secret")}
}

func (h *handlers) token(c *echo.Context) error {
	if h.oauth == nil || h.apiTokens == nil {
		return oauthJSON(c, &oauth.Error{Code: "temporarily_unavailable", Description: "Connecting apps is turned off right now.", Status: http.StatusServiceUnavailable})
	}
	resp, err := h.oauth.Token(c.Request().Context(), oauth.TokenRequest{
		GrantType: c.FormValue("grant_type"), Code: c.FormValue("code"), RedirectURI: c.FormValue("redirect_uri"), CodeVerifier: c.FormValue("code_verifier"),
		RefreshToken: c.FormValue("refresh_token"), Resource: c.FormValue("resource"), Client: clientAuth(c),
	})
	if err != nil {
		return h.oauthError(c, err)
	}
	c.Response().Header().Set("Pragma", "no-cache")
	return c.JSON(http.StatusOK, resp)
}

func (h *handlers) revokeToken(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	if err := h.oauth.Revoke(c.Request().Context(), c.FormValue("token"), clientAuth(c)); err != nil {
		return h.oauthError(c, err)
	}
	return c.NoContent(http.StatusOK)
}

// ---- The consent page's API (session only).

// consentOrganization is an organization the person could connect the app to.
type consentOrganization struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	CanWrite bool      `json:"can_write"` // the plan includes changes through the API
	Blocked  bool      `json:"blocked"`   // shut out by the organization's two-factor requirement
}

// oauthAuthorizeInfo tells the consent page what is being asked and what the person can choose.
func (h *handlers) oauthAuthorizeInfo(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	ctx := c.Request().Context()
	v, err := h.oauth.Validate(ctx, oauth.FromQuery(c.Request().URL.Query()))
	if err != nil {
		var oe *oauth.Error
		if errors.As(err, &oe) {
			return &APIError{Status: http.StatusBadRequest, Code: "INVALID_AUTHORIZATION_REQUEST", Message: "This connection request isn't valid: " + oe.Description}
		}
		return err
	}
	if v.RedirectError != nil {
		return c.JSON(http.StatusOK, map[string]any{"error_redirect": h.oauth.ErrorRedirect(v, v.RedirectError)})
	}
	u := currentUser(c)
	list, err := h.orgs.List(ctx, u.ID)
	if err != nil {
		return err
	}
	var reqs map[uuid.UUID]twofactor.Requirement
	needCode := false
	if h.twoFactor != nil {
		if reqs, err = h.twoFactor.ForUser(ctx, u.ID); err != nil {
			return err
		}
		if needCode, err = h.twoFactor.Enabled(ctx, u.ID); err != nil {
			return err
		}
	}
	orgs := make([]consentOrganization, len(list))
	for i, o := range list {
		p, err := h.plans.Plan(ctx, h.pool, o.ID)
		if err != nil {
			return err
		}
		orgs[i] = consentOrganization{ID: o.ID, Name: o.Name, Role: o.Role, CanWrite: p.APIWriteEnabled, Blocked: reqs[o.ID].Blocked}
	}
	return c.JSON(http.StatusOK, map[string]any{
		"client":                 map[string]string{"name": v.Client.Name},
		"requests_write":         v.Request.RequestsWrite(),
		"organizations":          orgs,
		"two_factor_code_needed": needCode,
		"deny_redirect":          h.oauth.ErrorRedirect(v, &oauth.Error{Code: "access_denied", Description: "The person declined."}),
		// The MCP authorization spec asks the consent screen to show where the person is sent back to, and to warn when that is only a local
		// address (any program on the computer could be listening there).
		"redirect_host":     redirectHost(v.RedirectURI),
		"redirect_loopback": isLoopback(v.RedirectURI),
	})
}

// oauthApprove records consent and returns where to send the browser: back to the client, with a code.
func (h *handlers) oauthApprove(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	var in struct {
		oauth.AuthorizeRequest
		OrganizationID uuid.UUID `json:"organization_id"`
		Access         string    `json:"access"`
		Code           string    `json:"two_factor_code"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	v, err := h.oauth.Validate(ctx, in.AuthorizeRequest)
	if err != nil || v.RedirectError != nil {
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_AUTHORIZATION_REQUEST", Message: "This connection request isn't valid. Start again from the app."}
	}
	redirect, err := h.oauth.Approve(ctx, currentUser(c), v, oauth.Approval{OrganizationID: in.OrganizationID, Scope: in.Access, Code: in.Code})
	var oe *oauth.Error
	var le *plans.LimitError
	switch {
	case errors.Is(err, oauth.ErrOrganization):
		return &APIError{Status: http.StatusForbidden, Code: "ORGANIZATION_UNAVAILABLE", Message: "You can't connect apps to that organization."}
	case errors.As(err, &le):
		return mapError(err)
	case errors.As(err, &oe):
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_SCOPE", Message: oe.Description}
	case errors.Is(err, twofactor.ErrBadCode), errors.Is(err, twofactor.ErrNotEnabled):
		return mapTwoFactorError(twofactor.ErrBadCode)
	case err != nil:
		return err
	}
	h.recordIn(c, in.OrganizationID, audit.AppConnected, target{"connected_app", v.Client.ID, v.Client.Name}, map[string]any{"scope": in.Access})
	return c.JSON(http.StatusOK, map[string]string{"redirect": redirect})
}

// ---- Connected apps (session only, on the API keys page).

func (h *handlers) listGrants(c *echo.Context) error {
	if h.oauth == nil {
		return c.JSON(http.StatusOK, map[string]any{"connected_apps": []any{}})
	}
	list, err := h.oauth.ListGrants(c.Request().Context(), currentOrg(c), currentUser(c).ID, c.Get(roleKey).(string))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"connected_apps": list})
}

func (h *handlers) revokeGrant(c *echo.Context) error {
	if h.oauth == nil {
		return errOAuthOff
	}
	notFound := &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Connected app not found."}
	id, err := uuid.Parse(c.Param("grantID"))
	if err != nil {
		return notFound
	}
	name := h.nameOf(c, `SELECT c.name FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id WHERE g.id = $1 AND g.organization_id = $2`, id)
	err = h.oauth.RevokeGrant(c.Request().Context(), currentOrg(c), id, currentUser(c).ID, c.Get(roleKey).(string))
	if errors.Is(err, oauth.ErrGrantNotFound) {
		return notFound
	}
	if err != nil {
		return err
	}
	h.recordOrg(c, audit.AppDisconnected, target{"connected_app", id.String(), name}, nil)
	return c.NoContent(http.StatusNoContent)
}

func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func isLoopback(raw string) bool {
	switch redirectHost(raw) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
