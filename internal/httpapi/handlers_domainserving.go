package httpapi

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/statuspage"
)

// TLSAllowPath is where Caddy's on-demand TLS asks whether it may get a certificate for a name (deploy/Caddyfile, on_demand_tls).
const TLSAllowPath = "/internal/tls/allow"

// requestHost is the name the visitor asked for, without a port.
func requestHost(c *echo.Context) string {
	host := c.Request().Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// tlsAllow answers Caddy: 200 for a name that serves a status page, 404 for anything else. Caddy calls it directly on the private network, so a
// request that came through a proxy (it carries X-Forwarded-For) is refused whatever it asks.
func (h *handlers) tlsAllow(c *echo.Context) error {
	if c.Request().Header.Get("X-Forwarded-For") != "" {
		return c.NoContent(http.StatusForbidden)
	}
	_, ok, err := h.pageDomains.Serving(c.Request().Context(), c.QueryParam("domain"))
	if err != nil {
		return err // 500: Caddy refuses the certificate, and asks again on a later handshake
	}
	if !ok {
		return c.NoContent(http.StatusNotFound)
	}
	return c.NoContent(http.StatusOK)
}

// domainStatus is the public page as served on its own domain. HomeURL is HawkEye's address, for "Powered by HawkEye" (a relative link would
// point at the customer's domain).
type domainStatus struct {
	statuspage.Public
	HomeURL string `json:"home_url"`
}

// publicDomainStatus serves the status page for the requested host name. Unknown names, drafts and domains not (or no longer) served all answer 404.
func (h *handlers) publicDomainStatus(c *echo.Context) error {
	ctx := c.Request().Context()
	slug, ok, err := h.pageDomains.Serving(ctx, requestHost(c))
	if err != nil {
		return err
	}
	if !ok {
		return mapError(statuspage.ErrNotFound)
	}
	p, err := h.statusPages.Public(ctx, slug)
	if err != nil {
		return mapError(err)
	}
	if p.Branding.LogoURL != "" {
		// The same logo, at the address this domain serves it from (the slug's own address is not served there).
		if _, v, found := strings.Cut(p.Branding.LogoURL, "?"); found {
			p.Branding.LogoURL = "/api/v1/public/domain-status/logo?" + v
		} else {
			p.Branding.LogoURL = "/api/v1/public/domain-status/logo"
		}
	}
	p.RedirectToDomain = false // already there
	c.Response().Header().Set("Cache-Control", "public, max-age=30")
	c.Response().Header().Set("Vary", "Host")
	return c.JSON(http.StatusOK, domainStatus{Public: p, HomeURL: h.appBaseURL})
}

func (h *handlers) publicDomainStatusLogo(c *echo.Context) error {
	ctx := c.Request().Context()
	slug, ok, err := h.pageDomains.Serving(ctx, requestHost(c))
	if err != nil {
		return err
	}
	if !ok {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "No logo."}
	}
	data, ctype, err := h.statusPages.PublicLogo(ctx, slug)
	c.Response().Header().Set("Vary", "Host")
	return h.sendLogo(c, data, ctype, err, fmt.Sprintf("public, max-age=%d", 3600))
}
