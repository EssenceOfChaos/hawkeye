package billing_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"

	"hawkeye/internal/billing"
	"hawkeye/internal/metrics"
	"hawkeye/internal/testutil"
)

func goodPrices() map[string]billing.PriceInfo {
	return map[string]billing.PriceInfo{
		"price_pro":    {ID: "price_pro", UnitAmount: 1900, Currency: "usd", Recurring: true, Interval: "month", IntervalCount: 1, Active: true},
		"price_agency": {ID: "price_agency", UnitAmount: 5900, Currency: "usd", Recurring: true, Interval: "month", IntervalCount: 1, Active: true},
	}
}

type countingReporter struct{ n atomic.Int32 }

func (c *countingReporter) Capture(context.Context, error, map[string]string) { c.n.Add(1) }

func (e *env) priceGauge(t *testing.T, plan string) float64 {
	t.Helper()
	mfs, err := e.m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "hawkeye_billing_price_mismatch" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "plan" && l.GetValue() == plan {
					return m.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}

func TestPricesThatMatchLeaveCheckoutOpen(t *testing.T) {
	e := newEnv(t)
	e.fake.Prices = goodPrices()
	if got := e.svc.VerifyPrices(t.Context()); len(got) != 0 {
		t.Fatalf("matching prices reported a problem: %+v", got)
	}
	for _, plan := range []string{"pro", "agency"} {
		if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", plan); err != nil {
			t.Fatalf("%s checkout should be open: %v", plan, err)
		}
	}
}

func TestAWrongStripeAmountRefusesThatPlanOnlyAndClearsWhenFixed(t *testing.T) {
	e := newEnv(t)
	rep := &countingReporter{}
	e.svc.WithReporter(rep)
	e.fake.Prices = goodPrices()
	pro := e.fake.Prices["price_pro"]
	pro.UnitAmount = 2900 // someone created the Stripe price at $29, the site says $19
	e.fake.Prices["price_pro"] = pro

	got := e.svc.VerifyPrices(t.Context())
	if len(got) != 1 || got[0].Plan != "pro" || got[0].Message == "" {
		t.Fatalf("expected one problem for pro, got %+v", got)
	}
	t.Log(got[0].Message)
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); !errors.Is(err, billing.ErrPriceMismatch) {
		t.Fatalf("pro checkout must be refused, got %v", err)
	}
	if len(e.fake.Checkouts) != 0 {
		t.Fatal("a Checkout session was created for a mismatched price")
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency"); err != nil {
		t.Fatalf("agency matches and must stay open: %v", err)
	}
	if e.priceGauge(t, "pro") != 1 || e.priceGauge(t, "agency") != 0 {
		t.Fatalf("gauge pro=%v agency=%v", e.priceGauge(t, "pro"), e.priceGauge(t, "agency"))
	}

	e.svc.VerifyPrices(t.Context()) // the next hourly check finds the same problem: it is not reported to the tracker again
	if rep.n.Load() != 1 {
		t.Fatalf("the problem was reported %d times; want once", rep.n.Load())
	}

	pro.UnitAmount = 1900 // fixed in Stripe
	e.fake.Prices["price_pro"] = pro
	if got := e.svc.VerifyPrices(t.Context()); len(got) != 0 {
		t.Fatalf("still reported after the fix: %+v", got)
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); err != nil {
		t.Fatalf("checkout must reopen after the fix: %v", err)
	}
	if e.priceGauge(t, "pro") != 0 {
		t.Fatal("gauge must clear")
	}
}

func TestOtherWaysAPriceCanBeWrongAreCaughtToo(t *testing.T) {
	cases := map[string]func(p *billing.PriceInfo){
		"archived":       func(p *billing.PriceInfo) { p.Active = false },
		"yearly":         func(p *billing.PriceInfo) { p.Interval = "year" },
		"every 3 months": func(p *billing.PriceInfo) { p.IntervalCount = 3 },
		"one-time":       func(p *billing.PriceInfo) { p.Recurring, p.Interval, p.IntervalCount = false, "", 0 },
		"euros":          func(p *billing.PriceInfo) { p.Currency = "eur" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.fake.Prices = goodPrices()
			p := e.fake.Prices["price_agency"]
			mutate(&p)
			e.fake.Prices["price_agency"] = p
			got := e.svc.VerifyPrices(t.Context())
			if len(got) != 1 || got[0].Plan != "agency" {
				t.Fatalf("expected an agency problem, got %+v", got)
			}
			if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency"); !errors.Is(err, billing.ErrPriceMismatch) {
				t.Fatalf("checkout must be refused: %v", err)
			}
		})
	}
}

func TestAnUnreachableStripeNeverBlocksAndNeverHidesAKnownProblem(t *testing.T) {
	e := newEnv(t)
	e.fake.Prices = goodPrices()
	e.fake.Err = errors.New("stripe is unreachable")
	if got := e.svc.VerifyPrices(t.Context()); len(got) != 0 {
		t.Fatalf("a failed lookup must not invent a problem: %+v", got)
	}
	e.fake.Err = nil // let the checkout itself work
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); err != nil {
		t.Fatalf("an unverified price must not block checkout: %v", err)
	}

	// Now a real problem is known; a later failed lookup must not clear it.
	p := e.fake.Prices["price_pro"]
	p.UnitAmount = 100
	e.fake.Prices["price_pro"] = p
	e.svc.VerifyPrices(t.Context())
	e.fake.Err = errors.New("stripe is unreachable")
	e.svc.VerifyPrices(t.Context())
	e.fake.Err = nil
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro"); !errors.Is(err, billing.ErrPriceMismatch) {
		t.Fatalf("a failed lookup cleared a known problem: %v", err)
	}
}

// STRIPE_LIVE_PRICE_CHECK=1 STRIPE_SECRET_KEY=... STRIPE_PRICE_PRO_ID=... STRIPE_PRICE_AGENCY_ID=... go test ./internal/billing -run LivePrices -v
// Read-only: it only retrieves the two prices from Stripe (works with live or test keys) and compares them with the plans table, exactly as the API does at
// startup and hourly. Run it after creating or changing prices in Stripe.
func TestLivePricesMatchThePlansTable(t *testing.T) {
	if os.Getenv("STRIPE_LIVE_PRICE_CHECK") != "1" {
		t.Skip("set STRIPE_LIVE_PRICE_CHECK=1 with STRIPE_SECRET_KEY, STRIPE_PRICE_PRO_ID and STRIPE_PRICE_AGENCY_ID to compare the real Stripe prices")
	}
	c := billing.Config{SecretKey: os.Getenv("STRIPE_SECRET_KEY"), WebhookSecret: "whsec_unused", PricePro: os.Getenv("STRIPE_PRICE_PRO_ID"), PriceAgency: os.Getenv("STRIPE_PRICE_AGENCY_ID")}
	svc := billing.NewService(testutil.NewPoolSize(t, 5), billing.NewStripeProvider(c.SecretKey), c, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if problems := svc.VerifyPrices(t.Context()); len(problems) != 0 {
		t.Fatalf("Stripe and the plans table disagree: %+v", problems)
	}
	t.Log("Stripe's Pro and Agency prices match the plans table (USD, monthly, active)")
}
