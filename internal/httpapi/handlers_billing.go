package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/billing"
)

// stripeWebhook accepts provider events. The body is read raw (the signature covers its exact bytes). A valid event is stored
// durably and acknowledged at once; processing happens right after (best effort) and, failing that, in the worker.
func (h *handlers) stripeWebhook(c echo.Context) error {
	payload, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return &APIError{Status: http.StatusBadRequest, Code: "BAD_REQUEST", Message: "Could not read the request body."}
	}
	res, err := h.billing.Receive(c.Request().Context(), payload, c.Request().Header.Get("Stripe-Signature"))
	switch {
	case errors.Is(err, billing.ErrNotConfigured):
		return &APIError{Status: http.StatusServiceUnavailable, Code: "BILLING_NOT_CONFIGURED", Message: "Webhooks are not configured."}
	case errors.Is(err, billing.ErrInvalidSignature):
		h.log.Warn("rejected billing webhook with invalid signature", "remote", c.RealIP())
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_SIGNATURE", Message: "Invalid signature."}
	case err != nil:
		return err // not durably stored: 500 so the provider retries
	}
	if !res.Duplicate {
		if perr := h.billing.ProcessEvent(c.Request().Context(), res.ID); perr != nil {
			h.log.Warn("inline billing event processing failed; the worker will retry", "event_id", res.EventID, "type", res.Type, "error", perr)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"received": true, "duplicate": res.Duplicate})
}

func (h *handlers) billingSubscription(c echo.Context) error {
	v, err := h.billing.Subscription(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	u, err := h.plans.Usage(c.Request().Context(), h.pool, currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"subscription": v, "usage": u})
}

func (h *handlers) billingCheckout(c echo.Context) error {
	var in struct {
		Plan string `json:"plan"`
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	url, err := h.billing.CreateCheckout(c.Request().Context(), currentOrg(c), currentUser(c).Email, in.Plan)
	if err != nil {
		return h.billingError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"url": url})
}

func (h *handlers) billingPortal(c echo.Context) error {
	url, err := h.billing.CreatePortal(c.Request().Context(), currentOrg(c))
	if err != nil {
		return h.billingError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]string{"url": url})
}

func (h *handlers) billingCancel(c echo.Context) error {
	if err := h.billing.Cancel(c.Request().Context(), currentOrg(c)); err != nil {
		return h.billingError(c, err)
	}
	return h.billingSubscriptionAccepted(c)
}

func (h *handlers) billingSync(c echo.Context) error {
	if err := h.billing.Resync(c.Request().Context(), currentOrg(c)); err != nil {
		return h.billingError(c, err)
	}
	return h.billingSubscriptionAccepted(c)
}

func (h *handlers) billingSubscriptionAccepted(c echo.Context) error {
	v, err := h.billing.Subscription(c.Request().Context(), currentOrg(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"subscription": v})
}

// billingError maps domain errors; provider failures are logged and reported generically.
func (h *handlers) billingError(c echo.Context, err error) error {
	mapped := mapError(err)
	if _, isAPI := mapped.(*APIError); isAPI {
		return mapped
	}
	h.log.ErrorContext(c.Request().Context(), "billing provider call failed", "error", err)
	return &APIError{Status: http.StatusBadGateway, Code: "BILLING_PROVIDER_ERROR", Message: "The payment provider could not be reached. Please try again."}
}

// accountError maps account errors; a failed payment-provider call is reported generically (details are logged).
func (h *handlers) accountError(c echo.Context, err error) error {
	mapped := mapError(err)
	if _, isAPI := mapped.(*APIError); isAPI {
		return mapped
	}
	if _, isValidation := mapped.(*APIError); !isValidation && errors.Unwrap(err) != nil && strings.Contains(err.Error(), "delete payment customer") {
		h.log.ErrorContext(c.Request().Context(), "account deletion blocked: payment provider call failed", "error", err)
		return &APIError{Status: http.StatusBadGateway, Code: "BILLING_PROVIDER_ERROR",
			Message: "We could not cancel your subscription with the payment provider, so nothing was deleted. Please try again."}
	}
	return mapped
}
