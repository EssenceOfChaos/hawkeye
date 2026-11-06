package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/incidents"
	"hawkeye/internal/notifications"
	"hawkeye/internal/organizations"
	"hawkeye/internal/validation"
)

const roleKey = "organization_role"

// requireOwner restricts an endpoint to organization owners.
func requireOwner(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		if c.Get(roleKey) != organizations.RoleOwner {
			return mapError(organizations.ErrForbidden)
		}
		return next(c)
	}
}

// ownOnly is nil for the owner, who manages every recipient, and the caller's own id for a member, who may only turn off or remove their own address.
func ownOnly(c *echo.Context) *uuid.UUID {
	if c.Get(roleKey) == organizations.RoleOwner {
		return nil
	}
	id := currentUser(c).ID
	return &id
}

func actor(c *echo.Context) notifications.Actor {
	u := currentUser(c)
	return notifications.Actor{ID: u.ID, Email: u.Email}
}

func settingID(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("settingID"))
	if err != nil {
		return uuid.Nil, mapError(notifications.ErrNotFound)
	}
	return id, nil
}

func (h *handlers) listSettings(c *echo.Context) error {
	list, err := h.notifications.List(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	// telegram_bot is the @name of HawkEye's bot when chats can be connected through it, otherwise "" (then only "use your own bot" is offered).
	return c.JSON(http.StatusOK, map[string]any{"notification_settings": list, "max": notifications.MaxSettingsPerOrganization,
		"telegram_bot": h.notifications.TelegramBotName()})
}

// telegramLink issues a one-time link that connects a Telegram chat through HawkEye's bot. The chat appears in the list once someone taps Start.
func (h *handlers) telegramLink(c *echo.Context) error {
	link, err := h.notifications.TelegramLink(c.Request().Context(), currentOrg(c), currentUser(c).ID)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusCreated, map[string]any{"telegram_link": link})
}

// telegramWebhook receives updates for HawkEye's Telegram bot. Telegram calls it, not a browser: no session or CSRF token. It is authenticated by
// the secret registered with setWebhook, which Telegram sends back in a header. A reply to the chat, if any, is returned as the response body
// (Telegram performs the method it names).
func (h *handlers) telegramWebhook(c *echo.Context) error {
	if !h.telegramBot.CheckSecret(c.Request().Header.Get("X-Telegram-Bot-Api-Secret-Token")) {
		h.log.Warn("rejected telegram webhook with a wrong secret", "remote", c.RealIP())
		return &APIError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: "Unauthorized."}
	}
	var u notifications.TelegramUpdate
	if err := json.NewDecoder(c.Request().Body).Decode(&u); err != nil {
		return c.NoContent(http.StatusOK) // not an update we understand; a non-2xx would only make Telegram resend it
	}
	reply, err := h.notifications.HandleTelegramUpdate(c.Request().Context(), u)
	if err != nil {
		return err // 500: Telegram retries the update
	}
	if reply == nil {
		return c.NoContent(http.StatusOK)
	}
	return c.JSON(http.StatusOK, reply)
}

func (h *handlers) createSetting(c *echo.Context) error {
	var in struct {
		Channel     string `json:"channel"`
		Destination string `json:"destination"`
		BotToken    string `json:"bot_token"` // Telegram only
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	switch {
	case in.Channel == "" || in.Channel == notifications.ChannelEmail:
	case notifications.IsURLChannel(in.Channel):
		// The test message is delivered before anything is saved; the signing secret is shown only in this response.
		s, secret, err := h.notifications.CreateURL(c.Request().Context(), currentOrg(c), in.Channel, in.Destination, in.BotToken)
		if err != nil {
			return mapError(err)
		}
		out := map[string]any{"notification_setting": s}
		if secret != "" {
			out["signing_secret"] = secret
		}
		return c.JSON(http.StatusCreated, out)
	default:
		return mapError(validation.New("channel", "must be email, webhook, slack, discord, teams or telegram"))
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

// testSetting sends a test to a recipient: a test message to a webhook or chat URL, or a test alert email to a confirmed email address.
// Emailing is capped per address, like every other action that emails a third party.
func (h *handlers) testSetting(c *echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	allow := func(address string) bool { return h.emailBudget(c, "alert-test", address) }
	if err := h.notifications.Test(c.Request().Context(), currentOrg(c), id, currentUser(c).Email, allow); err != nil {
		switch {
		case errors.Is(err, notifications.ErrTestRateLimited):
			c.Response().Header().Set("Retry-After", "3600")
			return errRateLimited
		case errors.Is(err, notifications.ErrNotVerified):
			return &APIError{Status: http.StatusConflict, Code: "NOT_VERIFIED", Message: err.Error()}
		}
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) updateSetting(c *echo.Context) error {
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
	s, err := h.notifications.SetEnabled(c.Request().Context(), currentOrg(c), id, *in.Enabled, ownOnly(c))
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"notification_setting": s})
}

// setRouting chooses which monitors a destination gets alerts for (owner only).
func (h *handlers) setRouting(c *echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	var in struct {
		MonitorScope string      `json:"monitor_scope"`
		TagIDs       []uuid.UUID `json:"tag_ids"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	s, err := h.notifications.SetRouting(c.Request().Context(), currentOrg(c), id, in.MonitorScope, in.TagIDs)
	if err != nil {
		return mapError(err)
	}
	return c.JSON(http.StatusOK, map[string]any{"notification_setting": s})
}

// alertCoverage lists monitors that no destination is routed to, and whether the fallback (team members' own email addresses) can reach anyone.
func (h *handlers) alertCoverage(c *echo.Context) error {
	uncovered, fallback, err := h.notifications.Coverage(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"uncovered_monitors": uncovered, "fallback_available": fallback})
}

func (h *handlers) deleteSetting(c *echo.Context) error {
	id, err := settingID(c)
	if err != nil {
		return err
	}
	if err := h.notifications.Delete(c.Request().Context(), currentOrg(c), id, ownOnly(c)); err != nil {
		return mapError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *handlers) resendSettingConfirmation(c *echo.Context) error {
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
func (h *handlers) confirmSetting(c *echo.Context) error {
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
func incidentFilter(c *echo.Context) (incidents.Filter, error) {
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

func (h *handlers) listIncidents(c *echo.Context) error {
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

func (h *handlers) listMonitorIncidents(c *echo.Context) error {
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
