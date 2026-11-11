package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/audit"
)

type apiKeyInput struct {
	Name      string     `json:"name"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expires_at"` // leave out for the 90-day default
	NoExpiry  bool       `json:"no_expiry"`
}

func (h *handlers) listAPIKeys(c *echo.Context) error {
	if h.apiTokens == nil {
		return errTokensDisabled
	}
	keys, err := h.apiTokens.List(c.Request().Context(), currentOrg(c), currentUser(c).ID, c.Get(roleKey).(string))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"keys": keys, "max_keys": apitokens.MaxLiveKeys})
}

func (h *handlers) createAPIKey(c *echo.Context) error {
	if h.apiTokens == nil {
		return errTokensDisabled
	}
	var in apiKeyInput
	if err := bind(c, &in); err != nil {
		return err
	}
	k, token, err := h.apiTokens.Create(c.Request().Context(), currentUser(c), currentOrg(c),
		apitokens.NewInput{Name: in.Name, Scope: in.Scope, ExpiresAt: in.ExpiresAt, NoExpiry: in.NoExpiry})
	if err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.APIKeyCreated, target{"api_key", k.ID.String(), k.Name}, map[string]any{"scope": k.Scope, "prefix": k.Prefix, "expires_at": k.ExpiresAt})
	// The token is in this response only; HawkEye keeps just its hash.
	return c.JSON(http.StatusCreated, map[string]any{"key": k, "token": token})
}

func (h *handlers) revokeAPIKey(c *echo.Context) error {
	if h.apiTokens == nil {
		return errTokensDisabled
	}
	id, err := uuid.Parse(c.Param("keyID"))
	if err != nil {
		return mapError(apitokens.ErrNotFound)
	}
	name := h.nameOf(c, `SELECT name FROM api_tokens WHERE id = $1 AND organization_id = $2 AND kind = 'key'`, id)
	if err := h.apiTokens.Revoke(c.Request().Context(), currentOrg(c), id, currentUser(c).ID, c.Get(roleKey).(string)); err != nil {
		return mapError(err)
	}
	h.recordOrg(c, audit.APIKeyRevoked, target{"api_key", id.String(), name}, nil)
	return c.NoContent(http.StatusNoContent)
}
