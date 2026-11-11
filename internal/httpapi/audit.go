package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/audit"
	"hawkeye/internal/auth"
)

// The security log (internal/audit). Handlers record an event after a change has succeeded; recording never fails the request.

// auditRequest puts where the request came from on its context, for every event recorded while serving it (including inside services such as
// internal/auth and the MCP tools). It runs after the request ID middleware.
func auditRequest(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		req := c.Request()
		ctx := audit.WithRequest(req.Context(), audit.Request{
			IP: c.RealIP(), UserAgent: req.UserAgent(), RequestID: c.Response().Header().Get(echo.HeaderXRequestID),
		})
		c.SetRequest(req.WithContext(ctx))
		return next(c)
	}
}

// auditActor is who the request acts as: an API key (for its owner), or the signed-in person.
func auditActor(c *echo.Context) audit.Actor {
	if p, ok := currentPrincipal(c); ok {
		return p.Actor()
	}
	if u, ok := c.Get(userKey).(auth.User); ok {
		return audit.User(u.ID, u.Email)
	}
	return audit.Anonymous()
}

// target names what an event acted on.
type target struct{ kind, id, name string }

// recordOrg writes an event to the log of the organization the request acts on (X-Organization-ID, or the key's).
func (h *handlers) recordOrg(c *echo.Context, action string, t target, details map[string]any) {
	h.recordIn(c, currentOrg(c), action, t, details)
}

// recordIn writes an event to orgID's log.
func (h *handlers) recordIn(c *echo.Context, orgID uuid.UUID, action string, t target, details map[string]any) {
	h.audit.Record(c.Request().Context(), audit.Event{Action: action, OrganizationID: orgID, Actor: auditActor(c),
		TargetType: t.kind, TargetID: t.id, TargetName: t.name, Details: details})
}

// recordAccount writes an event to the signed-in person's own log.
func (h *handlers) recordAccount(c *echo.Context, action string, details map[string]any) {
	u := currentUser(c)
	h.audit.Record(c.Request().Context(), audit.Event{Action: action, AccountID: u.ID, Actor: audit.User(u.ID, u.Email), Details: details})
}

// bindFields is bind for a partial update that also reports which settings the body set (its top-level JSON keys), so the log can say what changed
// without copying values, which may include credentials.
func bindFields(c *echo.Context, dst any) ([]string, error) {
	raw, err := io.ReadAll(c.Request().Body) // already capped by the body limit
	if err != nil {
		return nil, err
	}
	c.Request().Body = io.NopCloser(bytes.NewReader(raw))
	if err := bind(c, dst); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m) // bind has already accepted the body
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

var errBadLogQuery = &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "One or more fields are invalid.",
	Details: map[string]any{"fields": map[string]string{"query": "before must be a positive id, limit between 1 and 200, and category one of the log's categories"}}}

func logQuery(c *echo.Context) (audit.Query, error) {
	q, err := audit.ParseQuery(c.QueryParam("before"), c.QueryParam("limit"), c.QueryParam("category"))
	if err != nil {
		return audit.Query{}, errBadLogQuery
	}
	return q, nil
}

func (h *handlers) logPage(c *echo.Context, read func(audit.Query) (audit.Page, error), categories []string) error {
	q, err := logQuery(c)
	if err != nil {
		return err
	}
	page, err := read(q)
	if errors.Is(err, audit.ErrBadQuery) {
		return errBadLogQuery
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"events": page.Events, "next_before": page.NextBefore, "categories": categories,
		"retention_days": int(audit.Retention.Hours() / 24)})
}

// organizationLog is the organization's security log, for its owner.
func (h *handlers) organizationLog(c *echo.Context) error {
	return h.logPage(c, func(q audit.Query) (audit.Page, error) {
		return h.audit.Organization(c.Request().Context(), currentOrg(c), q)
	}, audit.OrganizationCategories)
}

// accountLog is the signed-in person's own security log.
func (h *handlers) accountLog(c *echo.Context) error {
	return h.logPage(c, func(q audit.Query) (audit.Page, error) {
		return h.audit.Account(c.Request().Context(), currentUser(c).ID, q)
	}, audit.AccountCategories)
}

// nameOf reads one name for the log with query ($1 the id, $2 the organization), before the thing is deleted. It gives "" if that fails: the
// event is still recorded, by id.
func (h *handlers) nameOf(c *echo.Context, query string, id uuid.UUID) string {
	return h.nameIn(c, currentOrg(c), query, id)
}

// nameIn is nameOf for routes that name the organization in their path.
func (h *handlers) nameIn(c *echo.Context, orgID uuid.UUID, query string, id uuid.UUID) string {
	var name string
	if err := h.db.QueryRow(c.Request().Context(), query, id, orgID).Scan(&name); err != nil {
		return ""
	}
	return name
}

const memberEmailQuery = `SELECT u.email FROM organization_members m JOIN users u ON u.id = m.user_id WHERE m.user_id = $1 AND m.organization_id = $2`

// monitorTargetByID names a monitor for the log by its id.
func (h *handlers) monitorTargetByID(c *echo.Context, id uuid.UUID) target {
	return target{"monitor", id.String(), h.nameOf(c, `SELECT name FROM monitors WHERE id = $1 AND organization_id = $2`, id)}
}

// excerpt is the first n characters of s.
func excerpt(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
