// Package billing integrates Stripe subscriptions. Entitlements change only when a verified provider event
// (or an explicit re-sync from the provider) says so; a Checkout redirect or session creation never grants anything.
package billing

import (
	"errors"
	"strings"
	"time"
)

// Config holds provider credentials and the mapping from provider prices to plans. Prices come from configuration,
// never from the client.
type Config struct {
	SecretKey     string
	WebhookSecret string
	PricePro      string
	PriceAgency   string
	AppBaseURL    string

	// MaxEventAttempts bounds retries of a stored event before it is marked failed.
	MaxEventAttempts int
}

func (c Config) withDefaults() Config {
	if c.MaxEventAttempts < 1 {
		c.MaxEventAttempts = 8
	}
	return c
}

// Enabled reports whether checkout and portal can be offered (API credentials and prices present).
func (c Config) Enabled() bool {
	return c.SecretKey != "" && c.PricePro != "" && c.PriceAgency != ""
}

// WebhooksEnabled reports whether webhooks can be verified.
func (c Config) WebhooksEnabled() bool { return c.WebhookSecret != "" }

// PriceForPlan returns the configured provider price for a plan code.
func (c Config) PriceForPlan(code string) (string, bool) {
	switch code {
	case "pro":
		return c.PricePro, c.PricePro != ""
	case "agency":
		return c.PriceAgency, c.PriceAgency != ""
	}
	return "", false
}

// PlanForPrice maps a provider price ID back to a plan code.
func (c Config) PlanForPrice(price string) (string, bool) {
	switch {
	case price == "":
		return "", false
	case price == c.PricePro:
		return "pro", true
	case price == c.PriceAgency:
		return "agency", true
	}
	return "", false
}

// Validate checks credential shape and the test/live safety rule. isProduction allows live keys.
func (c Config) Validate(isProduction bool) error {
	var errs []error
	any := c.SecretKey != "" || c.WebhookSecret != "" || c.PricePro != "" || c.PriceAgency != ""
	if !any {
		return nil // billing simply disabled
	}
	if c.SecretKey == "" {
		errs = append(errs, errors.New("STRIPE_SECRET_KEY: required when any Stripe setting is present"))
	} else if !strings.HasPrefix(c.SecretKey, "sk_") && !strings.HasPrefix(c.SecretKey, "rk_") {
		errs = append(errs, errors.New("STRIPE_SECRET_KEY: must start with sk_ or rk_"))
	} else if !isProduction && (strings.HasPrefix(c.SecretKey, "sk_live_") || strings.HasPrefix(c.SecretKey, "rk_live_")) {
		errs = append(errs, errors.New("STRIPE_SECRET_KEY: live keys are only allowed with APP_ENV=production; use a test-mode key (sk_test_...) for development"))
	}
	if !strings.HasPrefix(c.WebhookSecret, "whsec_") {
		errs = append(errs, errors.New("STRIPE_WEBHOOK_SECRET: required and must start with whsec_"))
	}
	for name, v := range map[string]string{"STRIPE_PRICE_PRO_ID": c.PricePro, "STRIPE_PRICE_AGENCY_ID": c.PriceAgency} {
		if !strings.HasPrefix(v, "price_") {
			errs = append(errs, errors.New(name+": required and must start with price_"))
		}
	}
	if c.PricePro != "" && c.PricePro == c.PriceAgency {
		errs = append(errs, errors.New("STRIPE_PRICE_PRO_ID and STRIPE_PRICE_AGENCY_ID must differ"))
	}
	return errors.Join(errs...)
}

// eventRetryDelay is the wait before retrying a stored event that failed to process.
func eventRetryDelay(attempt int) time.Duration {
	steps := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}
	return steps[min(max(attempt, 1), len(steps))-1]
}
