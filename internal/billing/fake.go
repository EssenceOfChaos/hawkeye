package billing

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// FakeProvider is an in-memory Provider for tests and local development without Stripe credentials.
type FakeProvider struct {
	mu               sync.Mutex
	CustomersCreated []string
	CustomersDeleted []string
	Checkouts        []CheckoutParams
	PortalCustomers  []string
	CanceledSubs     []string
	Subscriptions    map[string]SubscriptionState // returned by GetSubscription / updated by CancelAtPeriodEnd
	Prices           map[string]PriceInfo         // returned by GetPrice; an ID that is not here is "no such price"
	Err              error                        // returned by every call when set
}

// CreateCustomer returns a distinct ID per call (real Stripe would return the same one for a repeated idempotency key), so tests can
// detect any code path that creates more customers than it keeps.
func (f *FakeProvider) CreateCustomer(_ context.Context, email string, orgID uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	f.CustomersCreated = append(f.CustomersCreated, email)
	return fmt.Sprintf("cus_fake_%s_%d", orgID.String()[:8], len(f.CustomersCreated)), nil
}

func (f *FakeProvider) DeleteCustomer(_ context.Context, customerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.CustomersDeleted = append(f.CustomersDeleted, customerID)
	return nil
}

func (f *FakeProvider) CreateCheckoutSession(_ context.Context, p CheckoutParams) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	f.Checkouts = append(f.Checkouts, p)
	return "https://checkout.example.test/session/" + p.PriceID, nil
}

func (f *FakeProvider) CreatePortalSession(_ context.Context, customerID, returnURL string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	f.PortalCustomers = append(f.PortalCustomers, customerID)
	return "https://portal.example.test/" + customerID + "?return=" + returnURL, nil
}

func (f *FakeProvider) GetPrice(_ context.Context, id string) (PriceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return PriceInfo{}, f.Err
	}
	p, ok := f.Prices[id]
	if !ok {
		return PriceInfo{}, fmt.Errorf("retrieve price: no such price %q", id)
	}
	return p, nil
}

func (f *FakeProvider) GetSubscription(_ context.Context, id string) (SubscriptionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return SubscriptionState{}, f.Err
	}
	st, ok := f.Subscriptions[id]
	if !ok {
		return SubscriptionState{}, ErrNoSubscription
	}
	return st, nil
}

func (f *FakeProvider) CancelAtPeriodEnd(_ context.Context, id string) (SubscriptionState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return SubscriptionState{}, f.Err
	}
	st, ok := f.Subscriptions[id]
	if !ok {
		return SubscriptionState{}, ErrNoSubscription
	}
	st.CancelAtPeriodEnd = true
	f.Subscriptions[id] = st
	f.CanceledSubs = append(f.CanceledSubs, id)
	return st, nil
}

// SetSubscription sets the provider-side truth returned by GetSubscription.
func (f *FakeProvider) SetSubscription(st SubscriptionState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Subscriptions == nil {
		f.Subscriptions = map[string]SubscriptionState{}
	}
	f.Subscriptions[st.ID] = st
}
