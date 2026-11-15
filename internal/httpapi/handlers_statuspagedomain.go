package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/statuspagedomains"
)

// A status page's custom domain (Agency; docs/plan-status-page-domains.md). Any member may look; only the owner sets, checks or removes it.

// domainView is what every domain endpoint returns: the domain (null when the page has none), the name CNAME records point at, and how many
// domains the plan allows (0: not included), so the app can show the right form or upgrade prompt.
func (h *handlers) domainView(c *echo.Context, d *statuspagedomains.Domain) error {
	p, err := h.plans.Plan(c.Request().Context(), h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"domain": d, "target": h.pageDomains.Target(), "max": p.MaxStatusPageDomains})
}

// pageTitle names the page in the security log; it also answers 404 for a page that is not the organization's.
func (h *handlers) pageTitle(c *echo.Context, pageID uuid.UUID) (string, error) {
	cfg, err := h.statusPages.Get(c.Request().Context(), currentOrg(c), pageID)
	if err != nil {
		return "", mapError(err)
	}
	return cfg.Title, nil
}

func (h *handlers) getStatusPageDomain(c *echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	if _, err := h.pageTitle(c, id); err != nil {
		return err
	}
	d, err := h.pageDomains.Get(c.Request().Context(), currentOrg(c), id)
	if errors.Is(err, statuspagedomains.ErrNotFound) {
		return h.domainView(c, nil)
	}
	if err != nil {
		return err
	}
	return h.domainView(c, d)
}

func (h *handlers) putStatusPageDomain(c *echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	var in struct {
		Hostname string `json:"hostname"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	title, err := h.pageTitle(c, id)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	before, _ := h.pageDomains.Get(ctx, currentOrg(c), id)
	d, err := h.pageDomains.Claim(ctx, currentOrg(c), id, in.Hostname)
	if err != nil {
		return mapError(err)
	}
	if before == nil || before.ID != d.ID {
		if before != nil {
			h.recordOrg(c, audit.StatusPageDomainRemoved, target{"status_page", id.String(), title}, map[string]any{"hostname": before.Hostname})
		}
		h.recordOrg(c, audit.StatusPageDomainSet, target{"status_page", id.String(), title}, map[string]any{"hostname": d.Hostname})
		h.statusPages.Forget()
	}
	return h.domainView(c, d)
}

// checkStatusPageDomain looks the records up at once ("Check now").
func (h *handlers) checkStatusPageDomain(c *echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	title, err := h.pageTitle(c, id)
	if err != nil {
		return err
	}
	d, ch, err := h.pageDomains.CheckNow(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	if ch != nil {
		action := map[string]string{statuspagedomains.StateActive: audit.StatusPageDomainVerified, statuspagedomains.StateFailing: audit.StatusPageDomainFailing,
			statuspagedomains.StateDisabled: audit.StatusPageDomainDisabled}[d.State]
		if action != "" {
			details := map[string]any{"hostname": d.Hostname}
			if d.State == statuspagedomains.StateFailing {
				details["problem"] = d.LastError
			}
			h.recordOrg(c, action, target{"status_page", id.String(), title}, details)
		}
		h.statusPages.Forget()
	}
	return h.domainView(c, d)
}

func (h *handlers) deleteStatusPageDomain(c *echo.Context) error {
	id, err := statusPageID(c)
	if err != nil {
		return err
	}
	title, err := h.pageTitle(c, id)
	if err != nil {
		return err
	}
	d, err := h.pageDomains.Remove(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.StatusPageDomainRemoved, target{"status_page", id.String(), title}, map[string]any{"hostname": d.Hostname})
	h.statusPages.Forget()
	return c.NoContent(http.StatusNoContent)
}
