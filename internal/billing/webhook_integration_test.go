package billing_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/billing"
	"hawkeye/internal/billing/billingtest"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

var cfg = billing.Config{
	SecretKey: "sk_test_x", WebhookSecret: billingtest.Secret, PricePro: "price_pro", PriceAgency: "price_agency",
	AppBaseURL: "https://app.example.test", MaxEventAttempts: 3,
}

type env struct {
	pool *pgxpool.Pool
	svc  *billing.Service
	fake *billing.FakeProvider
	m    *metrics.Metrics
	org  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{pool: testutil.NewPoolSize(t, 20), fake: &billing.FakeProvider{Subscriptions: map[string]billing.SubscriptionState{}}}
	e.m = metrics.New()
	e.svc = billing.NewService(e.pool, e.fake, cfg, e.m, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.org = e.newOrg(t)
	return e
}

func (e *env) newOrg(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code='free'`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

// deliver receives a signed payload and processes it inline, like the HTTP handler does. The provider's current state is set to
// the subscription in the payload (the usual case: the event describes the latest change).
func (e *env) deliver(t *testing.T, payload []byte) billing.ReceiveResult {
	t.Helper()
	billingtest.Mirror(e.fake, payload)
	return e.deliverAsIs(t, payload)
}

// deliverWithTruth delivers an event while the provider's current state is something else: how reordered or delayed events look.
func (e *env) deliverWithTruth(t *testing.T, payload []byte, truth billingtest.Sub) billing.ReceiveResult {
	t.Helper()
	st, err := billing.ParseSubscription(mustJSON(truth.Object()))
	if err != nil {
		t.Fatal(err)
	}
	e.fake.SetSubscription(st)
	return e.deliverAsIs(t, payload)
}

func (e *env) deliverAsIs(t *testing.T, payload []byte) billing.ReceiveResult {
	t.Helper()
	res, err := e.svc.Receive(t.Context(), payload, billingtest.Sign(payload, billingtest.Secret, time.Now()))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if !res.Duplicate {
		_ = e.svc.ProcessEvent(t.Context(), res.ID) // errors are recorded on the event and inspected by tests
	}
	return res
}

func (e *env) effective(t *testing.T) plans.Plan {
	t.Helper()
	p, err := plans.EffectivePlan(t.Context(), e.pool, e.org)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) eventStatus(t *testing.T, eventID string) (status string, attempts int, lastErr *string) {
	t.Helper()
	if err := e.pool.QueryRow(t.Context(), `SELECT status, attempt_count, last_error FROM billing_webhook_events WHERE provider_event_id = $1`, eventID).
		Scan(&status, &attempts, &lastErr); err != nil {
		t.Fatalf("event %s: %v", eventID, err)
	}
	return
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) sub(status, price string) billingtest.Sub {
	return billingtest.Sub{ID: "sub_1", Customer: "cus_1", Status: status, Price: price, OrgID: e.org.String(),
		PeriodStart: time.Now().Add(-24 * time.Hour).Unix(), PeriodEnd: time.Now().Add(29 * 24 * time.Hour).Unix()}
}

var t0 = time.Now().Add(-time.Hour).Truncate(time.Second)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// ---- signature verification

func TestReceiveRejectsBadSignaturesAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	good := billingtest.Sign(payload, billingtest.Secret, time.Now())

	cases := map[string]struct {
		payload []byte
		header  string
	}{
		"wrong secret":     {payload, billingtest.Sign(payload, "whsec_someone_else", time.Now())},
		"tampered body":    {append(append([]byte{}, payload...), ' '), good},
		"tampered content": {[]byte(string(payload[:len(payload)-5]) + "XXXXX"), good},
		"no header":        {payload, ""},
		"garbage header":   {payload, "v1=abc"},
		"stale timestamp":  {payload, billingtest.Sign(payload, billingtest.Secret, time.Now().Add(-10*time.Minute))},
		// Note: Stripe's library rejects old timestamps (replay protection) but not future ones; forging either needs the secret.
	}
	for name, c := range cases {
		if _, err := e.svc.Receive(t.Context(), c.payload, c.header); !errors.Is(err, billing.ErrInvalidSignature) {
			t.Errorf("%s: want ErrInvalidSignature, got %v", name, err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM billing_webhook_events`); n != 0 {
		t.Fatalf("rejected events must not be stored, have %d", n)
	}
	if e.effective(t).Code != "free" {
		t.Fatal("a forged event granted access")
	}
	if _, err := e.svc.Receive(t.Context(), payload, good); err != nil {
		t.Fatalf("the genuine event is accepted: %v", err)
	}
}

func TestReceiveWithoutWebhookSecretIsRefused(t *testing.T) {
	e := newEnv(t)
	svc := billing.NewService(e.pool, nil, billing.Config{}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := billingtest.SubscriptionEvent("evt_x", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	if _, err := svc.Receive(t.Context(), payload, billingtest.Sign(payload, billingtest.Secret, time.Now())); !errors.Is(err, billing.ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

func TestDuplicateDeliveryIsAcknowledgedOnceAndAppliedOnce(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_dup", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	first := e.deliver(t, payload)
	second := e.deliver(t, payload)
	third := e.deliver(t, payload)
	if first.Duplicate || !second.Duplicate || !third.Duplicate {
		t.Fatalf("%v %v %v", first.Duplicate, second.Duplicate, third.Duplicate)
	}
	if e.count(t, `SELECT count(*) FROM billing_webhook_events WHERE provider_event_id = 'evt_dup'`) != 1 {
		t.Fatal("duplicate stored")
	}
	if e.count(t, `SELECT count(*) FROM subscriptions WHERE organization_id = $1`, e.org) != 1 || e.effective(t).Code != "pro" {
		t.Fatal("state wrong after duplicates")
	}
}

func TestConcurrentProcessingOfOneEventHasOneEffect(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_race", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	billingtest.Mirror(e.fake, payload)
	res, err := e.svc.Receive(t.Context(), payload, billingtest.Sign(payload, billingtest.Secret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = e.svc.ProcessEvent(t.Context(), res.ID)
			_, _ = e.svc.ProcessOne(t.Context())
		}()
	}
	close(start)
	wg.Wait()
	status, attempts, _ := e.eventStatus(t, "evt_race")
	if status != "processed" || attempts != 1 || e.effective(t).Code != "pro" {
		t.Fatalf("status=%s attempts=%d plan=%s", status, attempts, e.effective(t).Code)
	}
}

// ---- entitlements follow verified subscription state

func TestSubscriptionLifecycleDrivesEntitlements(t *testing.T) {
	e := newEnv(t)
	steps := []struct {
		typ, status, price string
		wantPlan           string
		wantStatus         string
	}{
		{"customer.subscription.created", "incomplete", "price_pro", "free", "incomplete"}, // first payment not done: nothing granted
		{"customer.subscription.updated", "active", "price_pro", "pro", "active"},
		{"customer.subscription.updated", "past_due", "price_pro", "pro", "past_due"},   // grace while Stripe retries
		{"customer.subscription.updated", "active", "price_agency", "agency", "active"}, // plan change
		{"customer.subscription.updated", "unpaid", "price_agency", "free", "canceled"}, // access revoked
		{"customer.subscription.updated", "active", "price_pro", "pro", "active"},       // paid up again
		{"customer.subscription.updated", "trialing", "price_pro", "pro", "trialing"},
		{"customer.subscription.updated", "paused", "price_pro", "free", "canceled"},
		{"customer.subscription.updated", "incomplete_expired", "price_pro", "free", "canceled"},
		{"customer.subscription.updated", "active", "price_pro", "pro", "active"},
		{"customer.subscription.deleted", "canceled", "price_pro", "free", "canceled"},
	}
	for i, s := range steps {
		payload := billingtest.SubscriptionEvent(fmt.Sprintf("evt_%d", i), s.typ, at(i*10), e.sub(s.status, s.price))
		e.deliver(t, payload)
		var status string
		e.pool.QueryRow(t.Context(), `SELECT status FROM subscriptions WHERE organization_id = $1`, e.org).Scan(&status)
		if got := e.effective(t); got.Code != s.wantPlan || status != s.wantStatus {
			t.Fatalf("step %d (%s %s): plan=%s status=%s, want %s/%s", i, s.typ, s.status, got.Code, status, s.wantPlan, s.wantStatus)
		}
	}
}

func TestPaidPlanLimitsAndPeriodAreStored(t *testing.T) {
	e := newEnv(t)
	sub := e.sub("active", "price_pro")
	sub.CancelAtPeriodEnd = true
	e.deliver(t, billingtest.SubscriptionEvent("evt_a", "customer.subscription.created", at(0), sub))
	p := e.effective(t)
	if p.MaxMonitors != 50 || p.MinimumCheckIntervalSeconds != 60 || p.HistoryRetentionDays != 90 {
		t.Fatalf("%+v", p)
	}
	v, err := e.svc.Subscription(t.Context(), e.org)
	if err != nil || !v.PaidAccess || !v.CancelAtPeriodEnd || v.CurrentPeriodEnd == nil || !v.CanManage || !v.HasSubscription || v.Plan.Code != "pro" {
		t.Fatalf("%+v %v", v, err)
	}
	var cust, subID string
	e.pool.QueryRow(t.Context(), `SELECT provider_customer_id, provider_subscription_id FROM subscriptions WHERE organization_id=$1`, e.org).Scan(&cust, &subID)
	if cust != "cus_1" || subID != "sub_1" {
		t.Fatalf("%s %s", cust, subID)
	}
}

func TestCheckoutCompletionAloneGrantsNothing(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.CheckoutCompleted("evt_co", at(0), e.org.String(), "cus_1", "sub_1"))
	if e.effective(t).Code != "free" {
		t.Fatal("checkout.session.completed must not grant paid access")
	}
	var cust *string
	e.pool.QueryRow(t.Context(), `SELECT provider_customer_id FROM subscriptions WHERE organization_id=$1`, e.org).Scan(&cust)
	if cust == nil || *cust != "cus_1" {
		t.Fatal("the customer should be linked")
	}
	if s, _, _ := e.eventStatus(t, "evt_co"); s != "processed" {
		t.Fatalf("status %s", s)
	}
	// A not-ours checkout (no client_reference_id) is ignored.
	e.deliver(t, billingtest.CheckoutCompleted("evt_co2", at(1), "", "cus_9", "sub_9"))
	if s, _, _ := e.eventStatus(t, "evt_co2"); s != "ignored" {
		t.Fatalf("status %s", s)
	}
}

func TestSubscriptionIsAttributedByCustomerWhenMetadataIsMissing(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.CheckoutCompleted("evt_1", at(0), e.org.String(), "cus_1", "sub_1")) // links the customer
	sub := e.sub("active", "price_pro")
	sub.OrgID = "" // e.g. a subscription changed in the dashboard, with no metadata
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.updated", at(5), sub))
	if e.effective(t).Code != "pro" {
		t.Fatal("event should be attributed through the linked customer")
	}
}

func TestInvoiceAndOtherEventsAreIgnored(t *testing.T) {
	e := newEnv(t)
	for i, typ := range []string{"invoice.payment_failed", "invoice.paid", "payment_intent.succeeded", "customer.updated"} {
		id := fmt.Sprintf("evt_i%d", i)
		e.deliver(t, billingtest.Event(id, typ, at(i), map[string]any{"id": "in_1", "customer": "cus_1"}))
		if s, _, _ := e.eventStatus(t, id); s != "ignored" {
			t.Errorf("%s: %s", typ, s)
		}
	}
	if e.effective(t).Code != "free" {
		t.Fatal("invoice events must not change entitlements")
	}
}

// ---- ordering, conflicts and failure handling

// Stripe does not guarantee delivery order and events can share a timestamp, so we never trust an event's content over the provider's
// current state: every subscription event triggers a re-read.
func TestReorderedAndStaleEventsCannotRegressState(t *testing.T) {
	e := newEnv(t)
	active := e.sub("active", "price_pro")

	// The provider's truth is "active". A delayed, older "incomplete" creation event arrives afterwards.
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_new", "customer.subscription.updated", at(100), active), active)
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_old", "customer.subscription.created", at(10), e.sub("incomplete", "price_pro")), active)
	if e.effective(t).Code != "pro" {
		t.Fatal("a stale event downgraded the organization")
	}

	// A cancellation is not undone by a late "active" renewal event that carries an old snapshot.
	canceled := e.sub("canceled", "price_pro")
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_del", "customer.subscription.deleted", at(300), canceled), canceled)
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_late", "customer.subscription.updated", at(250), active), canceled)
	if e.effective(t).Code != "free" {
		t.Fatal("a late event resurrected a cancelled subscription")
	}
}

func TestEventsSharingATimestampCannotBeMisordered(t *testing.T) {
	e := newEnv(t)
	same := at(50) // identical `created` second for two distinct changes
	active, past := e.sub("active", "price_pro"), e.sub("past_due", "price_pro")
	// Truth: it ended up past_due. The two events arrive in the "wrong" order; the payload content is irrelevant.
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_a", "customer.subscription.updated", same, past), past)
	e.deliverWithTruth(t, billingtest.SubscriptionEvent("evt_b", "customer.subscription.updated", same, active), past)
	var status string
	e.pool.QueryRow(t.Context(), `SELECT status FROM subscriptions WHERE organization_id = $1`, e.org).Scan(&status)
	if status != "past_due" {
		t.Fatalf("state must follow the provider's current state, got %s", status)
	}
}

func TestProviderOutageDuringProcessingIsRetried(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_out", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	billingtest.Mirror(e.fake, payload)
	e.fake.Err = errors.New("stripe unreachable")
	res := e.deliverAsIs(t, payload)
	if e.effective(t).Code != "free" {
		t.Fatal("nothing may be applied without the provider's confirmation")
	}
	if status, attempts, lastErr := e.eventStatus(t, "evt_out"); status != "received" || attempts != 1 || lastErr == nil {
		t.Fatalf("%s %d %v", status, attempts, lastErr)
	}
	// The provider recovers; the worker's retry applies it.
	e.fake.Err = nil
	e.pool.Exec(t.Context(), `UPDATE billing_webhook_events SET next_attempt_at = now() WHERE id = $1`, res.ID)
	if worked, err := e.svc.ProcessOne(t.Context()); !worked || err != nil {
		t.Fatalf("%v %v", worked, err)
	}
	if status, _, _ := e.eventStatus(t, "evt_out"); status != "processed" || e.effective(t).Code != "pro" {
		t.Fatalf("status %s plan %s", status, e.effective(t).Code)
	}
}

func TestDeletedEventStillAppliesWhenTheSubscriptionCannotBeRetrieved(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	delete(e.fake.Subscriptions, "sub_1") // e.g. test-mode data was purged: the API no longer returns it
	e.deliverAsIs(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.deleted", at(10), e.sub("canceled", "price_pro")))
	if e.effective(t).Code != "free" {
		t.Fatal("the signed deletion event is sufficient evidence to end paid access")
	}
	// Any other event type needs the provider; without it nothing is granted.
	e.deliverAsIs(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.updated", at(20), e.sub("active", "price_pro")))
	if e.effective(t).Code != "free" {
		t.Fatal("an update must not grant access without re-reading the provider")
	}
}

func TestSubscriptionEventsNeedAConfiguredProvider(t *testing.T) {
	e := newEnv(t)
	noProvider := billing.NewService(e.pool, nil, cfg, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	payload := billingtest.SubscriptionEvent("evt_np", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	res, err := noProvider.Receive(t.Context(), payload, billingtest.Sign(payload, billingtest.Secret, time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	_ = noProvider.ProcessEvent(t.Context(), res.ID)
	if e.effective(t).Code != "free" {
		t.Fatal("granted without a provider")
	}
}

func TestUnknownPriceNeverGrantsAndIsRetriedThenFailed(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_unk", "customer.subscription.created", at(0), e.sub("active", "price_mystery"))
	res := e.deliver(t, payload)
	if e.effective(t).Code != "free" {
		t.Fatal("unmapped price granted access")
	}
	status, attempts, lastErr := e.eventStatus(t, "evt_unk")
	if status != "received" || attempts != 1 || lastErr == nil {
		t.Fatalf("first failure should be retryable: %s %d %v", status, attempts, lastErr)
	}
	// Retries wait for backoff...
	if worked, _ := e.svc.ProcessOne(t.Context()); worked {
		t.Fatal("a failed event must wait for its retry time")
	}
	// ...and after MaxEventAttempts it is parked as failed (needs a human).
	for range 3 {
		e.pool.Exec(t.Context(), `UPDATE billing_webhook_events SET next_attempt_at = now() WHERE id = $1`, res.ID)
		e.svc.ProcessOne(t.Context())
	}
	if status, attempts, _ := e.eventStatus(t, "evt_unk"); status != "failed" || attempts != 3 {
		t.Fatalf("%s %d", status, attempts)
	}
	if n, err := billing.Failed(t.Context(), e.pool); err != nil || n != 1 {
		t.Fatalf("failed count %d %v", n, err)
	}
}

func TestUnmappedPriceMayStillRecordACancellation(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	// Price was since removed from our configuration, but the cancellation must still take effect.
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.deleted", at(10), e.sub("canceled", "price_retired")))
	if e.effective(t).Code != "free" {
		t.Fatal("cancellation should apply even with an unmapped price")
	}
}

func TestUnattributableEventFailsRatherThanGuessing(t *testing.T) {
	e := newEnv(t)
	sub := e.sub("active", "price_pro")
	sub.OrgID, sub.Customer = "", "cus_nobody_knows"
	e.deliver(t, billingtest.SubscriptionEvent("evt_x", "customer.subscription.created", at(0), sub))
	if status, _, lastErr := e.eventStatus(t, "evt_x"); status != "received" || lastErr == nil {
		t.Fatalf("%s %v", status, lastErr)
	}
	// An event naming a nonexistent organization is not trusted either.
	sub.OrgID = uuid.NewString()
	e.deliver(t, billingtest.SubscriptionEvent("evt_y", "customer.subscription.created", at(1), sub))
	if status, _, _ := e.eventStatus(t, "evt_y"); status != "received" {
		t.Fatalf("%s", status)
	}
}

func TestSecondSubscriptionNeverOverwritesTheCurrentOne(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))

	// A stray duplicate subscription is cancelled/expired: must not downgrade the organization.
	stray := e.sub("incomplete_expired", "price_pro")
	stray.ID = "sub_stray"
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.updated", at(10), stray))
	if e.effective(t).Code != "pro" {
		t.Fatal("an unrelated subscription's cancellation downgraded the organization")
	}
	if s, _, _ := e.eventStatus(t, "evt_2"); s != "ignored" {
		t.Fatalf("status %s", s)
	}

	// A second PAID subscription is a conflict for a human to resolve; it is not silently adopted.
	second := e.sub("active", "price_agency")
	second.ID = "sub_second"
	e.deliver(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.created", at(20), second))
	_, _, lastErr := e.eventStatus(t, "evt_3")
	if e.effective(t).Code != "pro" || lastErr == nil {
		t.Fatalf("plan=%s err=%v", e.effective(t).Code, lastErr)
	}
	var subID string
	e.pool.QueryRow(t.Context(), `SELECT provider_subscription_id FROM subscriptions WHERE organization_id=$1`, e.org).Scan(&subID)
	if subID != "sub_1" {
		t.Fatalf("subscription id overwritten: %s", subID)
	}
}

func TestResubscribingAfterCancellationAdoptsTheNewSubscription(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.deleted", at(10), e.sub("canceled", "price_pro")))
	again := e.sub("active", "price_agency")
	again.ID = "sub_2"
	e.deliver(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.created", at(20), again))
	if e.effective(t).Code != "agency" {
		t.Fatalf("plan %s", e.effective(t).Code)
	}
}

func TestTwoOrganizationsCannotShareACustomer(t *testing.T) {
	e := newEnv(t)
	other := e.newOrg(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	hijack := e.sub("active", "price_pro")
	hijack.ID, hijack.OrgID = "sub_other", other.String() // same customer id cus_1, different organization
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.created", at(5), hijack))
	var plan string
	e.pool.QueryRow(t.Context(), `SELECT p.code FROM subscriptions s JOIN plans p ON p.id=s.plan_id WHERE s.organization_id=$1`, other).Scan(&plan)
	if plan != "free" {
		t.Fatalf("a customer ID already linked to another organization granted access: %s", plan)
	}
}

// Downgrade e2e: nothing is deleted, creation is blocked and the explanation says how to get back within the limit.
func TestCancellationDowngradePreservesMonitors(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	for range 8 {
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds) VALUES ($1,'m','https://example.com',60)`, e.org); err != nil {
			t.Fatal(err)
		}
	}
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.deleted", at(10), e.sub("canceled", "price_pro")))

	if e.count(t, `SELECT count(*) FROM monitors WHERE organization_id=$1 AND deleted_at IS NULL`, e.org) != 8 {
		t.Fatal("monitors must survive a downgrade")
	}
	err := plans.NewService("https://app.example.test").CanCreateMonitor(t.Context(), e.pool, e.org)
	var le *plans.LimitError
	if !errors.As(err, &le) || le.Used != 8 || le.Limit != 5 {
		t.Fatalf("expected an over-limit error, got %v", err)
	}
	if min, _ := plans.NewService("").GetMinimumCheckInterval(t.Context(), e.pool, e.org); min != 300 {
		t.Fatalf("free interval after downgrade: %d", min)
	}
}

// ---- service operations with the fake provider

func TestCreateCheckoutUsesServerSidePriceAndOrganization(t *testing.T) {
	e := newEnv(t)
	url, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency")
	if err != nil || url == "" {
		t.Fatalf("%q %v", url, err)
	}
	if len(e.fake.Checkouts) != 1 {
		t.Fatal("expected one checkout")
	}
	c := e.fake.Checkouts[0]
	// The Stripe customer is created first and stored, so Checkout attaches to it (not to an email) and later events resolve through it.
	var stored string
	e.pool.QueryRow(t.Context(), `SELECT provider_customer_id FROM subscriptions WHERE organization_id = $1`, e.org).Scan(&stored)
	if c.PriceID != "price_agency" || c.OrganizationID != e.org || c.CustomerID == "" || c.CustomerID != stored || c.CustomerEmail != "" ||
		c.SuccessURL != "https://app.example.test/billing?checkout=success" || c.CancelURL != "https://app.example.test/billing?checkout=canceled" {
		t.Fatalf("%+v", c)
	}
	// Creating a session grants nothing.
	if e.effective(t).Code != "free" {
		t.Fatal("creating a checkout session must not change the plan")
	}
	// An organization with a stored customer reuses it instead of creating a duplicate.
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); err != nil {
		t.Fatal(err)
	}
	t.Logf("checkout reused the stored customer: %v", e.fake.Checkouts[1].CustomerID)
}

func TestCreateCheckoutRejectsBadPlansAndExistingSubscribers(t *testing.T) {
	e := newEnv(t)
	for _, plan := range []string{"", "free", "enterprise", "price_pro", "../pro"} {
		var ve *validation.Error
		if _, err := e.svc.CreateCheckout(t.Context(), e.org, "a@b.co", plan); !errors.As(err, &ve) {
			t.Errorf("plan %q: want validation error, got %v", plan, err)
		}
	}
	if len(e.fake.Checkouts) != 0 {
		t.Fatal("no session should have been created")
	}
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "a@b.co", "agency"); !errors.Is(err, billing.ErrAlreadySubscribed) {
		t.Fatalf("got %v", err)
	}
	// Past due still counts as subscribed (fix the payment in the portal instead of starting a second subscription).
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.updated", at(10), e.sub("past_due", "price_pro")))
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "a@b.co", "pro"); !errors.Is(err, billing.ErrAlreadySubscribed) {
		t.Fatalf("got %v", err)
	}
}

func TestBillingDisabledWithoutCredentials(t *testing.T) {
	e := newEnv(t)
	off := billing.NewService(e.pool, nil, billing.Config{}, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if off.Enabled() {
		t.Fatal("must be disabled")
	}
	if _, err := off.CreateCheckout(t.Context(), e.org, "a@b.co", "pro"); !errors.Is(err, billing.ErrNotConfigured) {
		t.Fatal(err)
	}
	if _, err := off.CreatePortal(t.Context(), e.org); !errors.Is(err, billing.ErrNotConfigured) {
		t.Fatal(err)
	}
	if err := off.Cancel(t.Context(), e.org); !errors.Is(err, billing.ErrNotConfigured) {
		t.Fatal(err)
	}
	v, err := off.Subscription(t.Context(), e.org)
	if err != nil || v.BillingEnabled || v.Plan.Code != "free" {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestPortalNeedsABillingAccount(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CreatePortal(t.Context(), e.org); !errors.Is(err, billing.ErrNoCustomer) {
		t.Fatalf("got %v", err)
	}
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	url, err := e.svc.CreatePortal(t.Context(), e.org)
	if err != nil || url == "" || e.fake.PortalCustomers[0] != "cus_1" {
		t.Fatalf("%q %v %v", url, err, e.fake.PortalCustomers)
	}
}

func TestCancelKeepsAccessUntilPeriodEnd(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.Cancel(t.Context(), e.org); !errors.Is(err, billing.ErrNoSubscription) {
		t.Fatalf("free org: %v", err)
	}
	sub := e.sub("active", "price_pro")
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), sub))
	state, _ := billing.ParseSubscription(mustJSON(sub.Object()))
	e.fake.Subscriptions["sub_1"] = state

	if err := e.svc.Cancel(t.Context(), e.org); err != nil {
		t.Fatal(err)
	}
	v, _ := e.svc.Subscription(t.Context(), e.org)
	if !v.CancelAtPeriodEnd || !v.PaidAccess || v.Plan.Code != "pro" || len(e.fake.CanceledSubs) != 1 {
		t.Fatalf("cancelling must schedule the end but keep access: %+v", v)
	}
	// The provider's later webhook for the same change is harmless.
	canceling := sub
	canceling.CancelAtPeriodEnd = true
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.updated", time.Now(), canceling))
	if v, _ := e.svc.Subscription(t.Context(), e.org); !v.CancelAtPeriodEnd || v.Plan.Code != "pro" {
		t.Fatalf("%+v", v)
	}
	// At the period end the provider deletes it and access ends, data stays.
	ended := sub
	ended.Status = "canceled"
	e.deliver(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.deleted", time.Now().Add(time.Second), ended))
	if v, _ := e.svc.Subscription(t.Context(), e.org); v.PaidAccess || v.Plan.Code != "free" || v.CancelAtPeriodEnd {
		t.Fatalf("%+v", v)
	}
}

func TestProviderFailuresSurfaceWithoutChangingState(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	e.fake.Err = errors.New("stripe: 503")
	if err := e.svc.Cancel(t.Context(), e.org); err == nil {
		t.Fatal("expected the provider error")
	}
	if _, err := e.svc.CreatePortal(t.Context(), e.org); err == nil {
		t.Fatal("expected the provider error")
	}
	if v, _ := e.svc.Subscription(t.Context(), e.org); v.CancelAtPeriodEnd || v.Plan.Code != "pro" {
		t.Fatalf("a failed provider call must not change state: %+v", v)
	}
}

// Recovery path for missed webhooks: re-read the subscription from the provider.
func TestResyncRepairsDriftFromMissedWebhooks(t *testing.T) {
	e := newEnv(t)
	sub := e.sub("active", "price_pro")
	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), sub))
	if e.effective(t).Code != "pro" {
		t.Fatal("setup")
	}
	// The cancellation webhook never reached us; the provider says it is canceled.
	gone := sub
	gone.Status = "canceled"
	st, _ := billing.ParseSubscription(mustJSON(gone.Object()))
	e.fake.Subscriptions["sub_1"] = st
	if err := e.svc.Resync(t.Context(), e.org); err != nil {
		t.Fatal(err)
	}
	if e.effective(t).Code != "free" {
		t.Fatal("resync should have applied the provider's state")
	}
	if err := e.svc.Resync(t.Context(), e.newOrg(t)); !errors.Is(err, billing.ErrNoSubscription) {
		t.Fatalf("an organization without a subscription: %v", err)
	}
}

func TestPendingMetric(t *testing.T) {
	e := newEnv(t)
	payload := billingtest.SubscriptionEvent("evt_p", "customer.subscription.created", at(0), e.sub("active", "price_pro"))
	if _, err := e.svc.Receive(t.Context(), payload, billingtest.Sign(payload, billingtest.Secret, time.Now())); err != nil {
		t.Fatal(err)
	}
	n, age, err := billing.Pending(t.Context(), e.pool)
	if err != nil || n != 1 || age < 0 {
		t.Fatalf("%d %v %v", n, age, err)
	}
}

func TestCheckoutCreatesOneCustomerPerOrganizationEvenUnderRaces(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	var stored string
	e.pool.QueryRow(t.Context(), `SELECT provider_customer_id FROM subscriptions WHERE organization_id = $1`, e.org).Scan(&stored)
	if stored == "" || len(e.fake.Checkouts) != 10 {
		t.Fatalf("stored=%q checkouts=%d", stored, len(e.fake.Checkouts))
	}
	for i, c := range e.fake.Checkouts {
		if c.CustomerID != stored {
			t.Fatalf("checkout %d used customer %s, but the stored one is %s: every session must use the single stored customer", i, c.CustomerID, stored)
		}
	}
	// A later call creates nothing new.
	before := len(e.fake.CustomersCreated)
	e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency")
	if len(e.fake.CustomersCreated) != before {
		t.Fatal("an existing customer must be reused")
	}
}

func TestSubscriptionsAreAttributedThroughTheStoredCustomerNotMetadata(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); err != nil {
		t.Fatal(err)
	}
	var cust string
	e.pool.QueryRow(t.Context(), `SELECT provider_customer_id FROM subscriptions WHERE organization_id = $1`, e.org).Scan(&cust)
	// The subscription event carries NO organization metadata and arrives before any checkout.session.completed.
	sub := e.sub("active", "price_pro")
	sub.OrgID, sub.Customer = "", cust
	e.deliver(t, billingtest.SubscriptionEvent("evt_c", "customer.subscription.created", at(0), sub))
	if e.effective(t).Code != "pro" {
		t.Fatal("the stored customer should be enough to attribute the subscription")
	}
}

func TestRiskEventsAreSurfacedButNeverChangeEntitlements(t *testing.T) {
	e := newEnv(t)
	e.deliver(t, billingtest.SubscriptionEvent("evt_s", "customer.subscription.created", at(0), e.sub("active", "price_pro")))
	for i, typ := range []string{"charge.dispute.created", "charge.refunded", "radar.early_fraud_warning.created"} {
		id := fmt.Sprintf("evt_risk_%d", i)
		e.deliver(t, billingtest.Event(id, typ, at(10+i), map[string]any{"id": "obj_1", "customer": "cus_1", "charge": "ch_1", "payment_intent": "pi_1"}))
		if status, _, _ := e.eventStatus(t, id); status != "processed" {
			t.Errorf("%s: status %s", typ, status)
		}
	}
	if e.effective(t).Code != "pro" {
		t.Fatal("risk events must not silently change a customer's plan (that is a human decision)")
	}
	fams, _ := e.m.Registry.Gather()
	var total float64
	for _, f := range fams {
		if f.GetName() == "hawkeye_billing_risk_events_total" {
			for _, m := range f.GetMetric() {
				total += m.GetCounter().GetValue()
			}
		}
	}
	if total != 3 {
		t.Fatalf("risk event counter = %v, want 3", total)
	}
}
