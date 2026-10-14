package httpapi

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"hawkeye/internal/contact"
)

func (h *handlers) contactCategories(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]any{"categories": contact.Categories})
}

// submitContact accepts the public contact form. Anyone may use it (signed in or not); a signed-in sender's account and plan are
// attached on the server so support can triage without asking. Nothing about the account is taken from the request body.
func (h *handlers) submitContact(c echo.Context) error {
	if h.contact == nil {
		return &APIError{Status: http.StatusServiceUnavailable, Code: "CONTACT_UNAVAILABLE", Message: "The contact form is not available right now."}
	}
	var in struct {
		Category string `json:"category"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Subject  string `json:"subject"`
		Message  string `json:"message"`
		URL      string `json:"url"`
		Company  string `json:"company"` // honeypot: hidden from people, filled in by form-spamming bots
	}
	if err := bind(c, &in); err != nil {
		return err
	}
	// Bots get the same success response, and nothing is sent.
	if in.Company != "" {
		return c.JSON(http.StatusAccepted, map[string]string{"reference": "HK-000000"})
	}
	sub := contact.Submission{Category: in.Category, Name: in.Name, Email: in.Email, Subject: in.Subject, Message: in.Message, URL: in.URL}
	// Reject bad input before it spends any budget or reaches the mail provider.
	check := sub
	if err := contact.Validate(&check); err != nil {
		return mapError(err)
	}
	h.attachSender(c, &sub)

	ctx := c.Request().Context()
	ref, err := h.contact.Submit(ctx, sub)
	if err != nil {
		h.log.ErrorContext(ctx, "contact form delivery failed", "error", err)
		return &APIError{Status: http.StatusServiceUnavailable, Code: "CONTACT_UNAVAILABLE",
			Message: "We couldn't send your message just now. Please try again in a few minutes."}
	}
	// The acknowledgement goes to an address the visitor typed, so it has its own hourly budget (no mail-bombing a third party).
	if h.emailBudget(c, "contact-ack", sub.Email) {
		h.contact.Acknowledge(ctx, sub, ref)
	}
	return c.JSON(http.StatusAccepted, map[string]string{"reference": ref})
}

// attachSender adds the signed-in user's id and plan (best effort; an anonymous or failing lookup simply adds nothing).
func (h *handlers) attachSender(c echo.Context, sub *contact.Submission) {
	cookie, err := c.Cookie(sessionCookie)
	if err != nil || h.auth == nil {
		return
	}
	ctx := c.Request().Context()
	u, err := h.auth.Authenticate(ctx, cookie.Value)
	if err != nil {
		return
	}
	sub.UserID = u.ID.String()
	sub.Plan = "unknown"
	if h.orgs == nil || h.plans == nil {
		return
	}
	if list, err := h.orgs.List(ctx, u.ID); err == nil && len(list) > 0 {
		if p, err := h.plans.Plan(ctx, h.pool, list[0].ID); err == nil {
			sub.Plan = p.Name
		}
	}
}
