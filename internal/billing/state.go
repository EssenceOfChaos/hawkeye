package billing

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Provider subscription statuses (Stripe) and how they map to ours. Per Stripe's guidance, access is revoked for
// canceled and unpaid subscriptions; past_due keeps access while Stripe retries payment.
//
//	active, trialing   -> active, trialing   (paid access)
//	past_due           -> past_due           (paid access during the retry grace period)
//	incomplete         -> incomplete         (first payment not completed: NO access)
//	canceled, incomplete_expired, unpaid, paused, anything unknown -> canceled (NO access; fails closed)
func MapStatus(providerStatus string) string {
	switch providerStatus {
	case "active":
		return "active"
	case "trialing":
		return "trialing"
	case "past_due":
		return "past_due"
	case "incomplete":
		return "incomplete"
	default:
		return "canceled"
	}
}

// SubscriptionState is the provider's view of one subscription, reduced to what entitlements need.
type SubscriptionState struct {
	ID                string
	CustomerID        string
	Status            string // provider status
	PriceIDs          []string
	CancelAtPeriodEnd bool
	PeriodStart       *time.Time
	PeriodEnd         *time.Time
	OrganizationID    *uuid.UUID // from metadata we set at checkout
}

// flexString decodes either a string ID or an expanded object with an "id".
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = flexString(s)
		return nil
	}
	var o struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	*f = flexString(o.ID)
	return nil
}

type rawSubscription struct {
	ID                string            `json:"id"`
	Customer          flexString        `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	Metadata          map[string]string `json:"metadata"`
	// Older API versions carry the period on the subscription itself.
	CurrentPeriodStart int64 `json:"current_period_start"`
	CurrentPeriodEnd   int64 `json:"current_period_end"`
	Items              struct {
		Data []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
			// Newer API versions (2025-03-31.basil onward) carry it on each subscription item.
			CurrentPeriodStart int64 `json:"current_period_start"`
			CurrentPeriodEnd   int64 `json:"current_period_end"`
		} `json:"data"`
	} `json:"items"`
}

// ParseSubscription extracts a SubscriptionState from a Stripe subscription JSON object.
func ParseSubscription(raw []byte) (SubscriptionState, error) {
	var r rawSubscription
	if err := json.Unmarshal(raw, &r); err != nil {
		return SubscriptionState{}, fmt.Errorf("parse subscription: %w", err)
	}
	if r.ID == "" || r.Status == "" {
		return SubscriptionState{}, fmt.Errorf("parse subscription: missing id or status")
	}
	st := SubscriptionState{ID: r.ID, CustomerID: string(r.Customer), Status: r.Status, CancelAtPeriodEnd: r.CancelAtPeriodEnd}
	start, end := r.CurrentPeriodStart, r.CurrentPeriodEnd
	for _, it := range r.Items.Data {
		if it.Price.ID != "" {
			st.PriceIDs = append(st.PriceIDs, it.Price.ID)
		}
		if start == 0 && it.CurrentPeriodStart != 0 {
			start = it.CurrentPeriodStart
		}
		if end == 0 && it.CurrentPeriodEnd != 0 {
			end = it.CurrentPeriodEnd
		}
	}
	if start != 0 {
		t := time.Unix(start, 0).UTC()
		st.PeriodStart = &t
	}
	if end != 0 {
		t := time.Unix(end, 0).UTC()
		st.PeriodEnd = &t
	}
	if id, err := uuid.Parse(r.Metadata["organization_id"]); err == nil {
		st.OrganizationID = &id
	}
	return st, nil
}
