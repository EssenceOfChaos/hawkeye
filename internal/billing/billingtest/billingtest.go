// Package billingtest builds signed Stripe-shaped webhook payloads for tests. Signing uses the official SDK helper,
// so it is the provider's algorithm, not ours.
package billingtest

import (
	"encoding/json"
	"strings"
	"time"

	"hawkeye/internal/billing"

	"github.com/stripe/stripe-go/v86/webhook"
)

const Secret = "whsec_test_secret_for_unit_tests"

// Sub describes a subscription object.
type Sub struct {
	ID, Customer, Status, Price string
	OrgID                       string // metadata.organization_id; empty omits it
	CancelAtPeriodEnd           bool
	PeriodStart, PeriodEnd      int64
}

// Object renders a subscription in the current API shape (period on the subscription item).
func (s Sub) Object() map[string]any {
	md := map[string]string{}
	if s.OrgID != "" {
		md["organization_id"] = s.OrgID
	}
	item := map[string]any{"id": "si_1", "price": map[string]any{"id": s.Price, "object": "price"}, "quantity": 1}
	if s.PeriodEnd != 0 {
		item["current_period_start"], item["current_period_end"] = s.PeriodStart, s.PeriodEnd
	}
	return map[string]any{
		"id": s.ID, "object": "subscription", "customer": s.Customer, "status": s.Status,
		"cancel_at_period_end": s.CancelAtPeriodEnd, "metadata": md, "livemode": false,
		"items": map[string]any{"object": "list", "data": []any{item}},
	}
}

// Event wraps an object in a Stripe event envelope.
func Event(id, typ string, created time.Time, object any) []byte {
	b, err := json.Marshal(map[string]any{
		"id": id, "object": "event", "api_version": "2025-03-31.basil", "created": created.Unix(), "type": typ,
		"livemode": false, "pending_webhooks": 1, "data": map[string]any{"object": object},
	})
	if err != nil {
		panic(err)
	}
	return b
}

// SubscriptionEvent builds a customer.subscription.* event.
func SubscriptionEvent(id, typ string, created time.Time, s Sub) []byte {
	return Event(id, typ, created, s.Object())
}

// CheckoutCompleted builds a checkout.session.completed event for a subscription checkout.
func CheckoutCompleted(id string, created time.Time, orgID, customer, subscription string) []byte {
	return Event(id, "checkout.session.completed", created, map[string]any{
		"id": "cs_test_1", "object": "checkout.session", "mode": "subscription", "client_reference_id": orgID,
		"customer": customer, "subscription": subscription, "payment_status": "paid", "status": "complete",
	})
}

// Sign returns a Stripe-Signature header for payload, signed at ts with the given secret.
func Sign(payload []byte, secret string, ts time.Time) string {
	return webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret, Timestamp: ts}).Header
}

// Mirror makes a FakeProvider report, as its current state, the subscription carried by a customer.subscription.* event payload.
// That models the usual case where the event is the latest change; tests simulating reordering set the truth explicitly instead.
func Mirror(f *billing.FakeProvider, payload []byte) {
	var env struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(payload, &env) != nil || !strings.HasPrefix(env.Type, "customer.subscription.") {
		return
	}
	if st, err := billing.ParseSubscription(env.Data.Object); err == nil {
		f.SetSubscription(st)
	}
}
