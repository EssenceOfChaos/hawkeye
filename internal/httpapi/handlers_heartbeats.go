package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"hawkeye/internal/heartbeats"
)

// maxPingBody is how much of a ping's body is read; only the first 1000 characters are kept.
const maxPingBody = 4 << 10

// ping receives a heartbeat from a customer's job: /ping/<token> (success), /ping/<token>/fail, or /ping/<token>/<exit status> (0 is success). It is
// called by scripts (curl, wget, PowerShell), not browsers, so it answers in plain text, accepts GET, HEAD and POST alike, and needs no session: the
// unguessable token is the only thing that authorizes it, and all it can do is record a ping.
func (h *handlers) ping(c *echo.Context) error {
	p := heartbeats.Ping{Method: c.Request().Method, SourceIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	switch status := c.Param("status"); status {
	case "":
	case "fail":
		p.Fail = true
	default:
		n, err := strconv.Atoi(status)
		if err != nil || n < 0 || n > 255 || strconv.Itoa(n) != status {
			return c.String(http.StatusNotFound, "Not found: use /ping/<token>, /ping/<token>/fail or /ping/<token>/<exit status 0-255>\n")
		}
		p.ExitStatus, p.Fail = &n, n != 0
	}
	if c.Request().Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(c.Request().Body, maxPingBody))
		if err != nil {
			return c.String(http.StatusBadRequest, "Could not read the request body\n")
		}
		p.Body = string(body)
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	counted, err := h.heartbeats.Ping(c.Request().Context(), c.Param("token"), p)
	if errors.Is(err, heartbeats.ErrNotFound) {
		return c.String(http.StatusNotFound, "Not found: check the ping address in HawkEye\n")
	}
	if err != nil {
		return err
	}
	if !counted {
		return c.String(http.StatusOK, "OK (the monitor is paused, so this ping was logged but not counted)\n")
	}
	return c.String(http.StatusOK, "OK\n")
}

// heartbeatPings lists a heartbeat monitor's recent pings, newest first (?limit=, at most 100).
func (h *handlers) heartbeatPings(c *echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil {
			return &APIError{Status: http.StatusBadRequest, Code: "VALIDATION_ERROR", Message: "limit must be a number.",
				Details: map[string]any{"fields": map[string]string{"limit": "must be a number"}}}
		}
	}
	pings, err := h.heartbeats.Pings(c.Request().Context(), currentOrg(c), id, limit)
	if errors.Is(err, heartbeats.ErrNotFound) {
		return &APIError{Status: http.StatusNotFound, Code: "NOT_FOUND", Message: "Heartbeat monitor not found."}
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"pings": pings})
}

// rotatePingToken gives a heartbeat monitor a new ping address; the old one stops working at once.
func (h *handlers) rotatePingToken(c *echo.Context) error {
	id, err := monitorID(c)
	if err != nil {
		return err
	}
	m, err := h.monitors.RotatePingToken(c.Request().Context(), currentOrg(c), id)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"monitor": m})
}
