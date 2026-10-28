package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"hawkeye/internal/monitors"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

const orgKey = "organization_id"

// resolveOrg picks the organization a request acts on and verifies the caller belongs to it.
func (h *handlers) resolveOrg(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		u := currentUser(c)
		if raw := c.Request().Header.Get("X-Organization-ID"); raw != "" {
			id, err := uuid.Parse(raw)
			if err != nil {
				return mapError(organizations.ErrNotFound)
			}
			o, err := h.orgs.Get(ctx, u.ID, id)
			if err != nil {
				return mapError(err)
			}
			c.Set(orgKey, id)
			c.Set(roleKey, o.Role)
			return next(c)
		}
		list, err := h.orgs.List(ctx, u.ID)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return mapError(organizations.ErrNotFound)
		}
		// Without a header the default is the organization the person owns (their own), not simply the oldest one: someone invited to an older team
		// would otherwise land in it instead of in their own account.
		def := list[0]
		for _, o := range list {
			if o.Role == organizations.RoleOwner {
				def = o
				break
			}
		}
		c.Set(orgKey, def.ID)
		c.Set(roleKey, def.Role)
		return next(c)
	}
}

func currentOrg(c echo.Context) uuid.UUID { return c.Get(orgKey).(uuid.UUID) }

func monitorID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("monitorID"))
	if err != nil {
		return uuid.Nil, mapError(monitors.ErrNotFound)
	}
	return id, nil
}

// limitAPIError renders a plan limit with enough detail for the UI to explain and offer an upgrade.
func limitAPIError(e *plans.LimitError) *APIError {
	d := map[string]any{
		"limit_type":  e.Kind,
		"limit":       e.Limit,
		"used":        e.Used,
		"plan":        e.Plan.Code,
		"upgrade_url": e.UpgradeURL,
	}
	if e.Next != nil {
		d["next_plan"] = e.Next
	}
	return &APIError{Status: http.StatusForbidden, Code: "PLAN_LIMIT_REACHED", Message: e.Message(), Details: d}
}

func (h *handlers) listPlans(c echo.Context) error {
	list, err := plans.List(c.Request().Context(), h.pool)
	if err != nil {
		return err
	}
	// A plan cannot offer more locations than this installation has.
	installed := max(1, len(h.regions.List()))
	for i := range list {
		list[i].MaxRegions = min(list[i].MaxRegions, installed)
	}
	return c.JSON(http.StatusOK, map[string]any{"plans": list, "billing_enabled": h.billing != nil && h.billing.Enabled()})
}

func (h *handlers) usage(c echo.Context) error {
	u, err := h.plans.Usage(c.Request().Context(), h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"usage": u})
}

func (h *handlers) listMonitors(c echo.Context) error {
	f := monitors.ListFilter{Limit: 25, Query: c.QueryParam("q"), Status: c.QueryParam("status")}
	bad := map[string]string{}
	switch f.Status {
	case "", "unknown", "up", "down":
	default:
		bad["status"] = "must be unknown, up or down"
	}
	if v := c.QueryParam("enabled"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			bad["enabled"] = "must be true or false"
		}
		f.Enabled = &b
	}
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
		return mapError(&validation.Error{Fields: bad})
	}
	list, total, err := h.monitors.List(c.Request().Context(), currentOrg(c), f)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitors": list, "total": total, "limit": f.Limit, "offset": f.Offset})
}

func (h *handlers) createMonitor(c echo.Context) error {
	var in monitors.CreateInput
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := requireVerifiedForWriteMethod(c, in.Method); err != nil {
		return err
	}
	m, err := h.monitors.Create(c.Request().Context(), currentOrg(c), in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"monitor": m})
}

// requireVerifiedForWriteMethod keeps state-changing request methods (POST, PUT, PATCH, DELETE) behind a verified email address: a monitor
// repeats the request on every check, so an anonymous throwaway account must not be able to aim it at someone else's API.
func requireVerifiedForWriteMethod(c echo.Context, method *string) error {
	if method == nil || !monitors.IsWriteMethod(*method) || currentUser(c).EmailVerified() {
		return nil
	}
	return &APIError{Status: http.StatusForbidden, Code: "EMAIL_NOT_VERIFIED",
		Message: "Verify your email address to monitor with POST, PUT, PATCH or DELETE requests."}
}

func (h *handlers) getMonitor(c echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	m, err := h.monitors.Get(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitor": m})
}

func (h *handlers) updateMonitor(c echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	var in monitors.UpdateInput
	if err := bind(c, &in); err != nil {
		return err
	}
	if err := requireVerifiedForWriteMethod(c, in.Method); err != nil {
		return err
	}
	m, err := h.monitors.Update(c.Request().Context(), currentOrg(c), id, in)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitor": m})
}

func (h *handlers) deleteMonitor(c echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	if err := h.monitors.Delete(c.Request().Context(), currentOrg(c), id); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) setEnabled(c echo.Context, enabled bool) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	m, err := h.monitors.SetEnabled(c.Request().Context(), currentOrg(c), id, enabled)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitor": m})
}

func (h *handlers) pauseMonitor(c echo.Context) error  { return h.setEnabled(c, false) }
func (h *handlers) resumeMonitor(c echo.Context) error { return h.setEnabled(c, true) }
