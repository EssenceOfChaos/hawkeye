package httpapi

import (
	"net/http"

	"hawkeye/internal/audit"
	"hawkeye/internal/organizations"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"
)

// orgID parses the path parameter. A malformed ID is reported as not found, like an unknown one.
func orgID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("organizationID"))
	if err != nil {
		return uuid.Nil, mapError(organizations.ErrNotFound)
	}
	return id, nil
}

func (h *handlers) listOrgs(c *echo.Context) error {
	orgs, err := h.orgs.List(c.Request().Context(), currentUser(c).ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"organizations": orgs})
}

func (h *handlers) getOrg(c *echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	o, err := h.orgs.Get(c.Request().Context(), currentUser(c).ID, id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"organization": o})
}

func (h *handlers) updateOrg(c *echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	before, err := h.orgs.Get(ctx, currentUser(c).ID, id)
	if err != nil {
		return mapError(err)
	}
	o, err := h.orgs.Rename(ctx, currentUser(c).ID, id, in.Name)
	if err != nil {
		return mapError(err)
	}
	if before.Name != o.Name {
		h.recordIn(c, id, audit.OrganizationRenamed, target{"organization", id.String(), o.Name}, map[string]any{"from": before.Name, "to": o.Name})
	}
	return c.JSON(http.StatusOK, map[string]any{"organization": o})
}

func (h *handlers) listMembers(c *echo.Context) error {
	id, err := orgID(c)
	if err != nil {
		return err
	}
	members, err := h.orgs.Members(c.Request().Context(), currentUser(c).ID, id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"members": members})
}
