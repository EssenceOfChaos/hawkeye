package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v87/webhook"
)

var ErrInvalidSignature = errors.New("invalid webhook signature")

// ReceiveResult describes how an incoming webhook was accepted.
type ReceiveResult struct {
	ID        uuid.UUID
	EventID   string
	Type      string
	Duplicate bool
}

// Receive verifies the signature over the RAW request body (rejecting bad or stale signatures before anything is
// stored) and durably records the event. It does not process it, so the caller can answer 2xx promptly.
// A redelivery of an already-stored event ID is acknowledged without being stored again.
func (s *Service) Receive(ctx context.Context, payload []byte, signatureHeader string) (ReceiveResult, error) {
	if !s.cfg.WebhooksEnabled() {
		return ReceiveResult{}, ErrNotConfigured
	}
	// Stripe's default 5 minute timestamp tolerance applies. The account's API version may differ from the SDK's, so
	// the version check is skipped; we read only the few stable fields we need.
	ev, err := webhook.ConstructEventWithOptions(payload, signatureHeader, s.cfg.WebhookSecret, webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		s.m.BillingEvents.WithLabelValues("invalid_signature").Inc()
		return ReceiveResult{}, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	res := ReceiveResult{EventID: ev.ID, Type: string(ev.Type)}
	err = s.pool.QueryRow(ctx, `INSERT INTO billing_webhook_events (provider, provider_event_id, event_type, event_created_at, payload)
		VALUES ('stripe', $1, $2, to_timestamp($3), $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING RETURNING id`, ev.ID, string(ev.Type), ev.Created, payload).Scan(&res.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		res.Duplicate = true
		s.m.BillingEvents.WithLabelValues("duplicate").Inc()
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("store webhook event: %w", err)
	}
	s.m.BillingEvents.WithLabelValues("received").Inc()
	return res, nil
}

type storedEvent struct {
	id       uuid.UUID
	typ      string
	created  time.Time
	payload  []byte
	attempts int
}

const eventLease = "2 minutes"

// claim leases the oldest due event (or the given one). Oldest-first by event time keeps processing roughly in
// provider order; the stale-event guard in apply covers the rest.
func (s *Service) claim(ctx context.Context, only *uuid.UUID) (*storedEvent, error) {
	var ev storedEvent
	err := s.pool.QueryRow(ctx, `UPDATE billing_webhook_events SET attempt_count = attempt_count + 1,
			next_attempt_at = now() + $1::interval
		WHERE id = (SELECT id FROM billing_webhook_events
			WHERE status = 'received' AND next_attempt_at <= now() AND ($2::uuid IS NULL OR id = $2)
			ORDER BY event_created_at, received_at FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, event_type, event_created_at, payload, attempt_count`, eventLease, only).
		Scan(&ev.id, &ev.typ, &ev.created, &ev.payload, &ev.attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &ev, err
}

// ProcessEvent processes one stored event right away (best effort; the worker retries anything left pending).
func (s *Service) ProcessEvent(ctx context.Context, id uuid.UUID) error {
	ev, err := s.claim(ctx, &id)
	if err != nil || ev == nil {
		return err
	}
	return s.run(ctx, ev)
}

// ProcessOne processes the next due event. It reports whether there was one.
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	ev, err := s.claim(ctx, nil)
	if err != nil || ev == nil {
		return false, err
	}
	return true, s.run(ctx, ev)
}

func (s *Service) run(ctx context.Context, ev *storedEvent) error {
	var outcome string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if outcome, err = s.handle(ctx, tx, ev); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE billing_webhook_events SET status = $2, processed_at = now(), last_error = NULL WHERE id = $1`, ev.id, outcome)
		return err
	})
	if err == nil {
		s.m.BillingEvents.WithLabelValues(outcome).Inc()
		return nil
	}
	// The transaction rolled back, so no partial subscription change exists. Record the failure and schedule a retry.
	msg := truncate(err.Error())
	if ev.attempts >= s.cfg.MaxEventAttempts {
		s.m.BillingEvents.WithLabelValues("failed").Inc()
		s.log.Error("billing webhook event failed permanently", "event_id", ev.id, "type", ev.typ, "attempts", ev.attempts, "error", msg)
		s.rep.Capture(ctx, err, map[string]string{"component": "billing", "event_id": ev.id.String(), "type": ev.typ})
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE billing_webhook_events SET status = 'failed', last_error = $2 WHERE id = $1`, ev.id, msg)
		return err
	}
	s.m.BillingEvents.WithLabelValues("retry").Inc()
	s.log.Warn("billing webhook event failed; will retry", "event_id", ev.id, "type", ev.typ, "attempt", ev.attempts, "error", msg)
	_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE billing_webhook_events SET last_error = $2, next_attempt_at = now() + $3::interval WHERE id = $1`,
		ev.id, msg, eventRetryDelay(ev.attempts))
	return err
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

type envelope struct {
	Data struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// handle applies one event inside the transaction. Entitlements change only for subscription events; checkout
// completion merely links the organization to its provider customer.
func (s *Service) handle(ctx context.Context, tx pgx.Tx, ev *storedEvent) (string, error) {
	var env envelope
	if err := json.Unmarshal(ev.payload, &env); err != nil {
		return "", fmt.Errorf("parse event: %w", err)
	}
	switch ev.typ {
	case "customer.subscription.created", "customer.subscription.updated", "customer.subscription.deleted":
		fromEvent, err := ParseSubscription(env.Data.Object)
		if err != nil {
			return "", err
		}
		orgID, err := s.resolveOrg(ctx, tx, fromEvent)
		if err != nil {
			return "", err
		}
		current, err := s.currentState(ctx, fromEvent, ev.typ == "customer.subscription.deleted")
		if err != nil {
			return "", err
		}
		return s.apply(ctx, tx, orgID, current)
	case "checkout.session.completed":
		return s.linkCustomer(ctx, tx, env.Data.Object)
	case "charge.dispute.created", "charge.refunded", "radar.early_fraud_warning.created":
		s.noteRiskEvent(ctx, ev, env.Data.Object)
		return outcomeApplied, nil
	default:
		return outcomeIgnored, nil // invoices, payment intents, etc. carry no entitlement decision
	}
}

// noteRiskEvent makes money-at-risk events visible. It deliberately takes NO automatic action on entitlements (a refund or dispute may
// or may not mean the customer should lose access; that is a business decision), but it never lets them pass silently: a warning log
// with the Stripe object IDs and a counter that should page someone. See docs/billing.md for the manual runbook.
func (s *Service) noteRiskEvent(ctx context.Context, ev *storedEvent, object json.RawMessage) {
	var o struct {
		ID            string     `json:"id"`
		Customer      flexString `json:"customer"`
		Charge        flexString `json:"charge"`
		PaymentIntent flexString `json:"payment_intent"`
	}
	_ = json.Unmarshal(object, &o)
	s.m.BillingRisk.WithLabelValues(ev.typ).Inc()
	s.log.WarnContext(ctx, "billing risk event received: review in the Stripe Dashboard", "type", ev.typ, "event_id", ev.id,
		"object_id", o.ID, "customer", string(o.Customer), "charge", string(o.Charge), "payment_intent", string(o.PaymentIntent))
}

var errNoOrganization = errors.New("cannot attribute the event to an organization")

// currentState re-reads the subscription from the provider. The provider does not guarantee event order and events can share a
// timestamp, so the payload is only used to know WHICH subscription changed; the state we apply is whatever the provider says now.
// If a subscription that was deleted can no longer be retrieved, the (signed) deletion event itself is the best evidence.
func (s *Service) currentState(ctx context.Context, fromEvent SubscriptionState, deleted bool) (SubscriptionState, error) {
	if s.provider == nil {
		return SubscriptionState{}, ErrNotConfigured
	}
	cur, err := s.provider.GetSubscription(ctx, fromEvent.ID)
	if err != nil {
		if deleted {
			fromEvent.Status = "canceled"
			return fromEvent, nil
		}
		return SubscriptionState{}, err
	}
	if cur.OrganizationID == nil {
		cur.OrganizationID = fromEvent.OrganizationID
	}
	return cur, nil
}

// resolveOrg finds the organization by known subscription, then known customer, then the metadata set at checkout.
func (s *Service) resolveOrg(ctx context.Context, tx pgx.Tx, st SubscriptionState) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT organization_id FROM subscriptions WHERE provider = 'stripe' AND provider_subscription_id = $1`, st.ID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if st.CustomerID != "" {
		err = tx.QueryRow(ctx, `SELECT organization_id FROM subscriptions WHERE provider = 'stripe' AND provider_customer_id = $1`, st.CustomerID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, err
		}
	}
	if st.OrganizationID != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, *st.OrganizationID).Scan(&exists); err != nil {
			return uuid.Nil, err
		}
		if exists {
			return *st.OrganizationID, nil
		}
	}
	return uuid.Nil, errNoOrganization
}

// linkCustomer records the provider customer on the organization named by client_reference_id. It never changes the plan.
func (s *Service) linkCustomer(ctx context.Context, tx pgx.Tx, raw json.RawMessage) (string, error) {
	var cs struct {
		ClientReferenceID string     `json:"client_reference_id"`
		Customer          flexString `json:"customer"`
	}
	if err := json.Unmarshal(raw, &cs); err != nil {
		return "", fmt.Errorf("parse checkout session: %w", err)
	}
	orgID, err := uuid.Parse(cs.ClientReferenceID)
	if err != nil || cs.Customer == "" {
		return outcomeIgnored, nil // not one of our subscription checkouts
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status)
		SELECT $1, id, 'active' FROM plans WHERE code = 'free' ON CONFLICT (organization_id) DO NOTHING`, orgID); err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE subscriptions SET provider = 'stripe', provider_customer_id = $2
		WHERE organization_id = $1 AND provider_customer_id IS NULL`, orgID, string(cs.Customer))
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return outcomeIgnored, nil // already linked
	}
	return outcomeApplied, nil
}

// Pending reports unprocessed events and the age of the oldest, for metrics.
func Pending(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (n int, oldest time.Duration, err error) {
	var secs float64
	err = q.QueryRow(ctx, `SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(received_at)), 0)::float8
		FROM billing_webhook_events WHERE status = 'received'`).Scan(&n, &secs)
	return n, time.Duration(secs * float64(time.Second)), err
}

// Failed counts events that gave up and need attention.
func Failed(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (n int, err error) {
	err = q.QueryRow(ctx, `SELECT count(*) FROM billing_webhook_events WHERE status = 'failed'`).Scan(&n)
	return n, err
}
