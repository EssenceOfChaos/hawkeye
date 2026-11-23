package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/incidents"
)

// DNS change alerts (docs/plan-dns-change-alerts.md): a DNS monitor's record changed. Any member can see the changes and accept one as intended
// (members can edit monitors too); the security log records who accepted it.

func (h *handlers) listDNSChanges(c *echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	// Existence (and tenant) check first, so another organization's monitor yields 404 rather than an empty list.
	if _, err := h.monitors.Get(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return h.dnsChangesBody(c, id)
}

func (h *handlers) acceptDNSChange(c *echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	changeID, err := uuid.Parse(c.Param("changeID"))
	if err != nil {
		return mapError(incidents.ErrDNSChangeNotFound)
	}
	m, err := h.monitors.Get(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	if err := incidents.AcceptDNSChange(c.Request().Context(), h.db, currentOrg(c), id, changeID, currentUser(c).ID); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.DNSChangeAccepted, target{"monitor", id.String(), m.Name}, map[string]any{"change_id": changeID.String()})
	return h.dnsChangesBody(c, id)
}

// dnsChangesBody is the monitor's changes, newest first, and its record's current answers.
func (h *handlers) dnsChangesBody(c *echo.Context, id uuid.UUID) error {
	ctx := c.Request().Context()
	list, err := incidents.DNSChanges(ctx, h.db, currentOrg(c), id, 50)
	if err != nil {
		return err
	}
	current, err := incidents.DNSCurrent(ctx, h.db, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"dns_changes": list, "current": current})
}
