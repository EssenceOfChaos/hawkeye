package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v87"
)

// StripeProvider talks to the Stripe API with the official Go SDK. Use test-mode keys outside production.
type StripeProvider struct {
	client *stripe.Client
}

const apiTimeout = 20 * time.Second

func NewStripeProvider(secretKey string, opts ...stripe.ClientOption) *StripeProvider {
	return &StripeProvider{client: stripe.NewClient(secretKey, opts...)}
}

func (p *StripeProvider) CreateCustomer(ctx context.Context, email string, orgID uuid.UUID) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	params := &stripe.CustomerCreateParams{
		Email:    stripe.String(email),
		Metadata: map[string]string{"organization_id": orgID.String()}, // informational/fallback; our database maps customer ID -> organization
	}
	// One customer per organization even if two requests race: Stripe returns the first response for a repeated key (keys last 24 h).
	params.SetIdempotencyKey("hawkeye-customer-" + orgID.String())
	c, err := p.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("create customer: %w", err)
	}
	return c.ID, nil
}

func (p *StripeProvider) DeleteCustomer(ctx context.Context, customerID string) error {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	if _, err := p.client.V1Customers.Delete(ctx, customerID, nil); err != nil {
		var se *stripe.Error
		if errors.As(err, &se) && se.Code == stripe.ErrorCodeResourceMissing {
			return nil // already gone
		}
		return fmt.Errorf("delete customer: %w", err)
	}
	return nil
}

func (p *StripeProvider) CreateCheckoutSession(ctx context.Context, c CheckoutParams) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	org := c.OrganizationID.String()
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String("subscription"),
		SuccessURL:        stripe.String(c.SuccessURL),
		CancelURL:         stripe.String(c.CancelURL),
		ClientReferenceID: stripe.String(org),
		LineItems:         []*stripe.CheckoutSessionCreateLineItemParams{{Price: stripe.String(c.PriceID), Quantity: stripe.Int64(1)}},
		Metadata:          map[string]string{"organization_id": org},
		// The subscription carries the organization too, so subscription events can be attributed on their own,
		// whatever order events arrive in.
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: map[string]string{"organization_id": org}},
	}
	if c.CustomerID != "" {
		params.Customer = stripe.String(c.CustomerID)
	} else if c.CustomerEmail != "" {
		params.CustomerEmail = stripe.String(c.CustomerEmail)
	}
	s, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("create checkout session: %w", err)
	}
	if s.URL == "" {
		return "", fmt.Errorf("create checkout session: provider returned no URL")
	}
	return s.URL, nil
}

func (p *StripeProvider) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	s, err := p.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer: stripe.String(customerID), ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", fmt.Errorf("create portal session: %w", err)
	}
	return s.URL, nil
}

func (p *StripeProvider) GetSubscription(ctx context.Context, id string) (SubscriptionState, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	sub, err := p.client.V1Subscriptions.Retrieve(ctx, id, nil)
	if err != nil {
		return SubscriptionState{}, fmt.Errorf("retrieve subscription: %w", err)
	}
	return stateFromSDK(sub)
}

func (p *StripeProvider) CancelAtPeriodEnd(ctx context.Context, id string) (SubscriptionState, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	sub, err := p.client.V1Subscriptions.Update(ctx, id, &stripe.SubscriptionUpdateParams{CancelAtPeriodEnd: stripe.Bool(true)})
	if err != nil {
		return SubscriptionState{}, fmt.Errorf("cancel subscription: %w", err)
	}
	return stateFromSDK(sub)
}

// stateFromSDK reuses the JSON parser so API responses and webhook payloads are interpreted identically.
func stateFromSDK(sub *stripe.Subscription) (SubscriptionState, error) {
	raw, err := json.Marshal(sub)
	if err != nil {
		return SubscriptionState{}, err
	}
	return ParseSubscription(raw)
}
