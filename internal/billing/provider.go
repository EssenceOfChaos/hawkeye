package billing

import (
	"context"

	"github.com/google/uuid"
)

// CheckoutParams describes a subscription Checkout session. Exactly one of CustomerID/CustomerEmail is used.
type CheckoutParams struct {
	OrganizationID uuid.UUID
	PriceID        string
	CustomerID     string // existing provider customer, if the organization has one
	CustomerEmail  string
	SuccessURL     string
	CancelURL      string
}

// Provider is the part of the payment provider this application calls. Everything else arrives by webhook.
type Provider interface {
	// CreateCustomer creates the provider customer for an organization. It must be idempotent per organization (a double click must not
	// create two customers). The returned ID is stored, and all later events are attributed through it.
	CreateCustomer(ctx context.Context, email string, organizationID uuid.UUID) (customerID string, err error)
	// DeleteCustomer permanently deletes the provider customer (used when an account is deleted). Per Stripe's API, this also immediately cancels the
	// customer's active subscriptions. A customer that no longer exists counts as success so a retried deletion can finish.
	DeleteCustomer(ctx context.Context, customerID string) error
	// CreateCheckoutSession returns the hosted-page URL. It grants nothing: access follows verified subscription events.
	CreateCheckoutSession(ctx context.Context, p CheckoutParams) (url string, err error)
	// CreatePortalSession returns a customer-portal URL for managing payment method, plan and cancellation.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (url string, err error)
	// GetSubscription fetches the provider's current view (used to re-sync after missed webhooks).
	GetSubscription(ctx context.Context, subscriptionID string) (SubscriptionState, error)
	// CancelAtPeriodEnd schedules cancellation at the end of the paid period and returns the updated subscription.
	CancelAtPeriodEnd(ctx context.Context, subscriptionID string) (SubscriptionState, error)
}
