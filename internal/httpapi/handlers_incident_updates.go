package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/incidents"
)

func incidentID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("incidentID"))
	if err != nil {
		return uuid.Nil, mapError(incidents.ErrIncidentNotFound)
	}
	return id, nil
}

// listIncidentUpdates returns an incident's updates and the published status pages that show them, so the team knows who reads what it posts.
func (h *handlers) listIncidentUpdates(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	list, err := incidents.ListUpdates(ctx, h.db, currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	pages, err := incidents.PagesShowing(ctx, h.db, currentOrg(c), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"updates": list, "status_pages": pages})
}

func (h *handlers) postIncidentUpdate(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	var in struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	u, err := incidents.PostUpdate(c.Request().Context(), h.db, currentOrg(c), id, currentUser(c).ID, in.Status, in.Message)
	if err != nil {
		return mapError(err)
	}
	h.statusPages.Forget() // public pages show it on their next request, not after their cache expires
	h.recordOrg(c, audit.IncidentUpdatePosted, target{"incident", id.String(), ""},
		map[string]any{"update_id": u.ID, "status": u.Status, "message": excerpt(u.Message, 200)})
	return c.JSON(http.StatusCreated, map[string]any{"update": u})
}

func (h *handlers) deleteIncidentUpdate(c *echo.Context) error {
	id, err := incidentID(c)
	if err != nil {
		return err
	}
	updateID, err := uuid.Parse(c.Param("updateID"))
	if err != nil {
		return mapError(incidents.ErrUpdateNotFound)
	}
	if err := incidents.DeleteUpdate(c.Request().Context(), h.db, currentOrg(c), id, updateID); err != nil {
		return mapError(err)
	}
	h.statusPages.Forget()
	h.recordOrg(c, audit.IncidentUpdateDeleted, target{"incident", id.String(), ""}, map[string]any{"update_id": updateID})
	return c.NoContent(http.StatusNoContent)
}
