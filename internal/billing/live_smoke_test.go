package billing_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v87"

	"hawkeye/internal/billing"
	"hawkeye/internal/billing/billingtest"
	"hawkeye/internal/metrics"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

// TestLiveStripeSandboxSmoke exercises StripeProvider against the REAL Stripe API in test mode. It is skipped unless you opt in:
//
//	STRIPE_LIVE_SMOKE=1 STRIPE_SECRET_KEY=sk_test_... STRIPE_PRICE_PRO_ID=price_... go test ./internal/billing -run LiveStripe -v
//
// It refuses to run with anything but a test-mode key, creates one customer and one subscription (using Stripe's `pm_card_visa`
// test payment method), and deletes/cancels them when it finishes.
func TestLiveStripeSandboxSmoke(t *testing.T) {
	key, price := os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("STRIPE_PRICE_PRO_ID")
	if os.Getenv("STRIPE_LIVE_SMOKE") != "1" {
		t.Skip("set STRIPE_LIVE_SMOKE=1 (with a test-mode STRIPE_SECRET_KEY and STRIPE_PRICE_PRO_ID) to run against the Stripe sandbox")
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Fatal("refusing to run: the smoke test only accepts a test-mode key")
	}
	if !strings.HasPrefix(price, "price_") {
		t.Fatal("STRIPE_PRICE_PRO_ID is required")
	}
	ctx := t.Context()
	p := billing.NewStripeProvider(key)
	sdk := stripe.NewClient(key)
	org := uuid.New()

	// 1. Checkout session, attached to a customer created by our provider first (exactly what the service does).
	checkoutCustomer, err := p.CreateCustomer(ctx, "smoke+"+org.String()[:8]+"@example.com", org)
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := sdk.V1Customers.Delete(c, checkoutCustomer, nil); err != nil {
			t.Errorf("cleanup: could not delete customer %s: %v", checkoutCustomer, err)
		}
	})
	url, err := p.CreateCheckoutSession(ctx, billing.CheckoutParams{
		OrganizationID: org, PriceID: price, CustomerID: checkoutCustomer,
		SuccessURL: "https://app.example.test/billing?checkout=success", CancelURL: "https://app.example.test/billing?checkout=canceled",
	})
	if err != nil {
		t.Fatalf("checkout session: %v", err)
	}
	if !strings.HasPrefix(url, "https://checkout.stripe.com/") {
		t.Fatalf("unexpected checkout URL: %s", url)
	}
	t.Logf("checkout session OK: %.60s…", url)

	// 2. A real subscription, created the way the dashboard/Checkout would leave it.
	cust, err := sdk.V1Customers.Create(ctx, &stripe.CustomerCreateParams{
		Email: stripe.String("smoke+" + org.String()[:8] + "@example.com"), PaymentMethod: stripe.String("pm_card_visa"),
		InvoiceSettings: &stripe.CustomerCreateInvoiceSettingsParams{DefaultPaymentMethod: stripe.String("pm_card_visa")},
		Metadata:        map[string]string{"organization_id": org.String(), "created_by": "hawkeye-live-smoke-test"},
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	// t.Context() is already cancelled when cleanups run, so use a fresh context or nothing would be deleted.
	cleanupCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 20*time.Second)
	}
	t.Cleanup(func() {
		c, cancel := cleanupCtx()
		defer cancel()
		if _, err := sdk.V1Customers.Delete(c, cust.ID, nil); err != nil {
			t.Errorf("cleanup: could not delete test customer %s: %v", cust.ID, err)
		}
	})

	sub, err := sdk.V1Subscriptions.Create(ctx, &stripe.SubscriptionCreateParams{
		Customer: stripe.String(cust.ID),
		Items:    []*stripe.SubscriptionCreateItemParams{{Price: stripe.String(price)}},
		Metadata: map[string]string{"organization_id": org.String()},
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := cleanupCtx()
		defer cancel()
		if _, err := sdk.V1Subscriptions.Cancel(c, sub.ID, nil); err != nil {
			t.Errorf("cleanup: could not cancel test subscription %s: %v", sub.ID, err)
		}
	})

	// 3. Our provider must read the real object correctly.
	st, err := p.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSubscription: %v", err)
	}
	t.Logf("real subscription parsed: id=%s status=%s prices=%v periodEnd=%v cancelAtPeriodEnd=%v org=%v", st.ID, st.Status, st.PriceIDs, st.PeriodEnd, st.CancelAtPeriodEnd, st.OrganizationID)
	if st.ID != sub.ID || st.CustomerID != cust.ID || st.Status != "active" || len(st.PriceIDs) != 1 || st.PriceIDs[0] != price {
		t.Fatalf("parsed state does not match: %+v", st)
	}
	if st.PeriodEnd == nil || st.PeriodStart == nil || !st.PeriodEnd.After(*st.PeriodStart) {
		t.Fatalf("the billing period was not found in Stripe's response: %+v", st)
	}
	if st.OrganizationID == nil || *st.OrganizationID != org {
		t.Fatalf("metadata organization_id not parsed: %+v", st.OrganizationID)
	}
	if billing.MapStatus(st.Status) != "active" {
		t.Fatal("status mapping")
	}

	// 4. Scheduling cancellation at the period end.
	st, err = p.CancelAtPeriodEnd(ctx, sub.ID)
	if err != nil {
		t.Fatalf("CancelAtPeriodEnd: %v", err)
	}
	if !st.CancelAtPeriodEnd || st.Status != "active" {
		t.Fatalf("cancel at period end must keep the subscription active until the period ends: %+v", st)
	}

	// 5. The customer portal. Stripe requires a saved portal configuration (Dashboard → Settings → Billing → Customer portal).
	portal, err := p.CreatePortalSession(ctx, cust.ID, "https://app.example.test/billing")
	switch {
	case err != nil && strings.Contains(err.Error(), "configuration"):
		t.Logf("portal: NOT CONFIGURED in the sandbox yet (%v): save a configuration in Dashboard → Settings → Billing → Customer portal", err)
	case err != nil:
		t.Fatalf("portal session: %v", err)
	default:
		if !strings.HasPrefix(portal, "https://billing.stripe.com/") {
			t.Fatalf("unexpected portal URL %s", portal)
		}
		t.Log("portal session OK")
	}
}

// TestLiveStripeLifecycleThroughOurWebhookPipeline is the end-to-end proof against real Stripe: it creates a real subscription whose
// metadata points at a real organization row, fetches the REAL events Stripe generated, and replays their exact bytes through our
// webhook pipeline (signed locally with a test secret, because Stripe's own signing needs a registered endpoint or the CLI).
// Our StripeProvider then re-reads the subscription from Stripe, as in production, and the organization's plan changes.
func TestLiveStripeLifecycleThroughOurWebhookPipeline(t *testing.T) {
	key, pro, agency := os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("STRIPE_PRICE_PRO_ID"), os.Getenv("STRIPE_PRICE_AGENCY_ID")
	if os.Getenv("STRIPE_LIVE_SMOKE") != "1" {
		t.Skip("set STRIPE_LIVE_SMOKE=1 (with test-mode Stripe settings and TEST_DATABASE_URL) to run against the Stripe sandbox")
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Fatal("refusing to run: the smoke test only accepts a test-mode key")
	}
	ctx := t.Context()
	pool := testutil.NewPool(t)
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('live smoke') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code='free'`, org)

	liveCfg := billing.Config{SecretKey: key, WebhookSecret: billingtest.Secret, PricePro: pro, PriceAgency: agency, AppBaseURL: "https://app.example.test"}
	svc := billing.NewService(pool, billing.NewStripeProvider(key), liveCfg, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	sdk := stripe.NewClient(key)
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 20*time.Second)
	}

	cust, err := sdk.V1Customers.Create(ctx, &stripe.CustomerCreateParams{
		Email: stripe.String("smoke+" + org.String()[:8] + "@example.com"), PaymentMethod: stripe.String("pm_card_visa"),
		InvoiceSettings: &stripe.CustomerCreateInvoiceSettingsParams{DefaultPaymentMethod: stripe.String("pm_card_visa")},
		Metadata:        map[string]string{"created_by": "hawkeye-live-smoke-test"},
	})
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	t.Cleanup(func() {
		c, cancel := bg()
		defer cancel()
		if _, err := sdk.V1Customers.Delete(c, cust.ID, nil); err != nil {
			t.Errorf("cleanup: could not delete test customer %s: %v", cust.ID, err)
		}
	})
	sub, err := sdk.V1Subscriptions.Create(ctx, &stripe.SubscriptionCreateParams{
		Customer: stripe.String(cust.ID), Items: []*stripe.SubscriptionCreateItemParams{{Price: stripe.String(pro)}},
		Metadata: map[string]string{"organization_id": org.String()},
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	ended := false
	endSubscription := func() {
		if ended {
			return
		}
		ended = true
		c, cancel := bg()
		defer cancel()
		if _, err := sdk.V1Subscriptions.Cancel(c, sub.ID, nil); err != nil {
			t.Errorf("cleanup: could not cancel test subscription %s: %v", sub.ID, err)
		}
	}
	t.Cleanup(endSubscription)

	// replay fetches Stripe's real events of one type for our subscription and pushes their exact bytes through the pipeline.
	seen := map[string]bool{}
	replay := func(eventType string) int {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.stripe.com/v1/events?limit=100&type="+eventType, nil)
			req.SetBasicAuth(key, "")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var list struct {
				Data []json.RawMessage `json:"data"`
			}
			err = json.NewDecoder(res.Body).Decode(&list)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, raw := range list.Data {
				var ev struct {
					ID   string `json:"id"`
					Data struct {
						Object struct {
							ID string `json:"id"`
						} `json:"object"`
					} `json:"data"`
				}
				if json.Unmarshal(raw, &ev) != nil || ev.Data.Object.ID != sub.ID || seen[ev.ID] {
					continue
				}
				seen[ev.ID] = true
				res, err := svc.Receive(ctx, raw, billingtest.Sign(raw, billingtest.Secret, time.Now()))
				if err != nil {
					t.Fatalf("our pipeline rejected a real Stripe %s event: %v", eventType, err)
				}
				if err := svc.ProcessEvent(ctx, res.ID); err != nil {
					t.Fatalf("processing real %s event %s: %v", eventType, ev.ID, err)
				}
				n++
			}
			if n > 0 || time.Now().After(deadline) {
				return n
			}
			time.Sleep(2 * time.Second) // events are generated asynchronously
		}
	}
	plan := func() string {
		p, err := plans.EffectivePlan(ctx, pool, org)
		if err != nil {
			t.Fatal(err)
		}
		return p.Code
	}

	// 1. The real "created" event, then the real subscription it names: the plan becomes Pro.
	if replay("customer.subscription.created") == 0 {
		t.Fatal("Stripe produced no customer.subscription.created event within 30s")
	}
	if got := plan(); got != "pro" {
		t.Fatalf("after the real created event the plan is %q, want pro", got)
	}
	v, err := svc.Subscription(ctx, org)
	if err != nil || !v.PaidAccess || v.Status != "active" || v.CurrentPeriodEnd == nil || !v.CanManage || !v.HasSubscription {
		t.Fatalf("%+v %v", v, err)
	}
	t.Logf("real created event applied: plan=pro status=%s renews=%s customer linked=%v", v.Status, v.CurrentPeriodEnd.Format("2006-01-02"), v.CanManage)

	// 2. Cancel from our app: goes to the real API; access continues until the period ends.
	if err := svc.Cancel(ctx, org); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	v, _ = svc.Subscription(ctx, org)
	if !v.CancelAtPeriodEnd || !v.PaidAccess || plan() != "pro" {
		t.Fatalf("cancellation must keep access until the period ends: %+v", v)
	}
	if n := replay("customer.subscription.updated"); n == 0 {
		t.Log("no updated event surfaced in time (non-fatal: the state was already applied directly)")
	}
	if v, _ = svc.Subscription(ctx, org); !v.CancelAtPeriodEnd || plan() != "pro" {
		t.Fatalf("real updated events must be harmless duplicates of the applied state: %+v", v)
	}

	// 3. The subscription actually ends: the real "deleted" event removes paid access, nothing else.
	endSubscription()
	if replay("customer.subscription.deleted") == 0 {
		t.Fatal("Stripe produced no customer.subscription.deleted event within 30s")
	}
	if v, _ = svc.Subscription(ctx, org); v.PaidAccess || v.Status != "canceled" || plan() != "free" {
		t.Fatalf("after the real deleted event: %+v plan=%s", v, plan())
	}
	t.Log("real deleted event applied: access ended, organization is back on the Free plan")
}

// TestLiveStripeDeletingACustomerCancelsItsSubscription verifies, against real Stripe, the behaviour account deletion relies on:
// deleting a customer immediately cancels its subscriptions, and deleting it again is not an error.
func TestLiveStripeDeletingACustomerCancelsItsSubscription(t *testing.T) {
	key, price := os.Getenv("STRIPE_SECRET_KEY"), os.Getenv("STRIPE_PRICE_PRO_ID")
	if os.Getenv("STRIPE_LIVE_SMOKE") != "1" {
		t.Skip("set STRIPE_LIVE_SMOKE=1 to run against the Stripe sandbox")
	}
	if !strings.HasPrefix(key, "sk_test_") && !strings.HasPrefix(key, "rk_test_") {
		t.Fatal("refusing to run: the smoke test only accepts a test-mode key")
	}
	ctx := t.Context()
	p, sdk := billing.NewStripeProvider(key), stripe.NewClient(key)
	cust, err := sdk.V1Customers.Create(ctx, &stripe.CustomerCreateParams{
		Email: stripe.String("smoke+delete@example.com"), PaymentMethod: stripe.String("pm_card_visa"),
		InvoiceSettings: &stripe.CustomerCreateInvoiceSettingsParams{DefaultPaymentMethod: stripe.String("pm_card_visa")},
		Metadata:        map[string]string{"created_by": "hawkeye-live-smoke-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = sdk.V1Customers.Delete(c, cust.ID, nil)
	})
	sub, err := sdk.V1Subscriptions.Create(ctx, &stripe.SubscriptionCreateParams{
		Customer: stripe.String(cust.ID), Items: []*stripe.SubscriptionCreateItemParams{{Price: stripe.String(price)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := p.GetSubscription(ctx, sub.ID); err != nil || st.Status != "active" {
		t.Fatalf("setup: %+v %v", st, err)
	}

	if err := p.DeleteCustomer(ctx, cust.ID); err != nil {
		t.Fatalf("DeleteCustomer: %v", err)
	}
	deleted = true
	st, err := p.GetSubscription(ctx, sub.ID)
	if err != nil {
		t.Fatalf("a cancelled subscription should still be retrievable: %v", err)
	}
	if billing.MapStatus(st.Status) != "canceled" {
		t.Fatalf("deleting the customer left the subscription %q: paying customers must not be left billed", st.Status)
	}
	t.Logf("real Stripe: subscription status after customer deletion = %s", st.Status)
	if err := p.DeleteCustomer(ctx, cust.ID); err != nil {
		t.Fatalf("deleting an already-deleted customer must succeed so a retry can finish: %v", err)
	}
}
