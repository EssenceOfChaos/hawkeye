package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/incidents"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/validation"
)

const roleKey = "organization_role"

// requireOwner restricts an endpoint to organization owners.
func requireOwner(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if c.Get(roleKey) != organizations.RoleOwner {
			return mapError(organizations.ErrForbidden)
		}
		return next(c)
	}
}

func actor(c echo.Context) notifications.Actor {
	u := currentUser(c)
	return notifications.Actor{ID: u.ID, Email: u.Email}
}

func settingID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("settingID"))
	if err != nil {
		return uuid.Nil, mapError(notifications.ErrNotFound)
	}
	return id, nil
}

func (h *handlers) listSettings(c echo.Context) error {
	list, err := h.notifications.List(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"notification_settings": list, "max": notifications.MaxSettingsPerOrganization})
}

func (h *handlers) createSetting(c echo.Context) error {
	var in struct {
		Channel     string `json:"channel"`
		Destination string `json:"destination"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Channel != "" && in.Channel != "email" {
		return mapError(validation.New("channel", "only \"email\" is supported"))
	}
	// Adding a recipient emails a third party: cap how often any one address can be targeted.
	if !h.emailBudget(c, "alert-confirm", in.Destination) {
		c.Response().Header().Set("Retry-After", "3600")
		return errRateLimited
	}
	s, err := h.notifications.Create(c.Request().Context(), currentOrg(c), actor(c), in.Destination)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"notification_setting": s})
}

func (h *handlers) updateSetting(c echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if in.Enabled == nil {
		return mapError(validation.New("enabled", "is required"))
	}
	s, err := h.notifications.SetEnabled(c.Request().Context(), currentOrg(c), id, *in.Enabled)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"notification_setting": s})
}

func (h *handlers) deleteSetting(c echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	if err := h.notifications.Delete(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) resendSettingConfirmation(c echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	if !h.emailBudget(c, "alert-confirm-resend", id.String()) {
		c.Response().Header().Set("Retry-After", "3600")
		return errRateLimited
	}
	if err := h.notifications.ResendConfirmation(c.Request().Context(), currentOrg(c), id, actor(c)); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusAccepted)
}

// confirmSetting is called from the link in the confirmation email; the recipient need not have an account.
func (h *handlers) confirmSetting(c echo.Context) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := h.notifications.Confirm(c.Request().Context(), in.Token); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// incidentFilter parses status, from, to, limit and offset.
func incidentFilter(c echo.Context) (incidents.Filter, error) {
	f := incidents.Filter{Limit: 25, Status: c.QueryParam("status")}
	bad := map[string]string{}
	if f.Status != "" && f.Status != "open" && f.Status != "resolved" {
		bad["status"] = "must be open or resolved"
	}
	parseTime := func(name string) *time.Time {
		v := c.QueryParam(name)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			bad[name] = "must be an RFC 3339 timestamp"
			return nil
		}
		return &t
	}
	f.From, f.To = parseTime("from"), parseTime("to")
	if v := c.QueryParam("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			bad["limit"] = "must be between 1 and 100"
		}
		f.Limit = n
	}
	if v := c.QueryParam("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			bad["offset"] = "must be 0 or greater"
		}
		f.Offset = n
	}
	if len(bad) > 0 {
		return f, mapError(&validation.Error{Fields: bad})
	}
	return f, nil
}

func (h *handlers) listIncidents(c echo.Context) error {
	f, err := incidentFilter(c)
	if err != nil {
		return err
	}
	list, total, err := incidents.List(c.Request().Context(), h.db, currentOrg(c), f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"incidents": list, "total": total, "limit": f.Limit, "offset": f.Offset})
}

func (h *handlers) listMonitorIncidents(c echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	// Existence (and tenant) check first, so another organization's monitor yields 404 rather than an empty list.
	if _, err := h.monitors.Get(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	f, err := incidentFilter(c)
	if err != nil {
		return err
	}
	f.MonitorID = &id
	list, total, err := incidents.List(c.Request().Context(), h.db, currentOrg(c), f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"incidents": list, "total": total, "limit": f.Limit, "offset": f.Offset})
}
