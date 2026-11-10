package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/domains"
)

func (h *handlers) listDomains(c *echo.Context) error {
	l, err := h.domains.List(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"domains": l.Domains, "limit": l.Limit})
}

func (h *handlers) createDomain(c *echo.Context) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	d, err := h.domains.Add(c.Request().Context(), currentOrg(c), in.Name)
	if err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.DomainAdded, target{"domain", d.ID.String(), d.Name}, nil)
	return c.JSON(http.StatusCreated, map[string]any{"domain": d})
}

func (h *handlers) deleteDomain(c *echo.Context) error {
	id, err := uuid.Parse(c.Param("domainID"))
	if err != nil {
		return mapError(domains.ErrNotFound)
	}
	name := h.nameOf(c, `SELECT name FROM domains WHERE id = $1 AND organization_id = $2`, id)
	if err := h.domains.Remove(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.DomainRemoved, target{"domain", id.String(), name}, nil)
	return c.NoContent(http.StatusNoContent)
}
