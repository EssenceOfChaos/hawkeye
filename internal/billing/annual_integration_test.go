package billing_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"hawkeye/internal/billing"
	"hawkeye/internal/billing/billingtest"
	"hawkeye/internal/metrics"
	"hawkeye/internal/validation"
)

func TestYearlyCheckoutUsesTheYearlyPrice(t *testing.T) {
	e := newEnv(t)
	if !e.svc.AnnualEnabled() {
		t.Fatal("yearly billing should be on with both yearly prices set")
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency", "year"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro", "month"); err != nil {
		t.Fatal(err)
	}
	if len(e.fake.Checkouts) != 2 || e.fake.Checkouts[0].PriceID != "price_agency_year" || e.fake.Checkouts[1].PriceID != "price_pro" {
		t.Fatalf("checkouts: %+v", e.fake.Checkouts)
	}
	var ve *validation.Error
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro", "week"); !errors.As(err, &ve) || ve.Fields["interval"] == "" {
		t.Fatalf("unknown interval: %v", err)
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "free", "year"); !errors.As(err, &ve) || ve.Fields["plan"] == "" {
		t.Fatalf("free yearly: %v", err)
	}
}

func TestYearlyCheckoutIsRefusedWithoutYearlyPrices(t *testing.T) {
	e := newEnv(t)
	monthlyOnly := cfg
	monthlyOnly.PriceProAnnual, monthlyOnly.PriceAgencyAnnual = "", ""
	svc := billing.NewService(e.pool, e.fake, monthlyOnly, metrics.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if svc.AnnualEnabled() {
		t.Fatal("yearly billing must be off")
	}
	var ve *validation.Error
	if _, err := svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro", "year"); !errors.As(err, &ve) || ve.Fields["interval"] == "" {
		t.Fatalf("want an interval validation error, got %v", err)
	}
	if len(e.fake.Checkouts) != 0 {
		t.Fatal("no session should have been created")
	}
	v, err := svc.Subscription(t.Context(), e.org)
	if err != nil || v.AnnualBillingEnabled {
		t.Fatalf("view: %+v %v", v, err)
	}
}

func TestAYearlySubscriptionGrantsThePlanAndRecordsTheInterval(t *testing.T) {
	e := newEnv(t)
	v, err := e.svc.Subscription(t.Context(), e.org)
	if err != nil || v.BillingInterval != nil || !v.AnnualBillingEnabled {
		t.Fatalf("free view: %+v %v", v, err)
	}

	e.deliver(t, billingtest.SubscriptionEvent("evt_1", "customer.subscription.created", at(0), e.sub("active", "price_agency_year")))
	v, err = e.svc.Subscription(t.Context(), e.org)
	if err != nil || v.Plan.Code != "agency" || !v.PaidAccess || v.BillingInterval == nil || *v.BillingInterval != "year" {
		t.Fatalf("after yearly agency: %+v %v", v, err)
	}
	if v.Plan.AnnualPriceCents == nil || *v.Plan.AnnualPriceCents != 59000 || v.Plan.MonthlyPriceCents != 5900 {
		t.Fatalf("advertised prices: %+v", v.Plan)
	}

	// Switching to monthly in the customer portal changes the interval, and the plan stays.
	e.deliver(t, billingtest.SubscriptionEvent("evt_2", "customer.subscription.updated", at(10), e.sub("active", "price_agency")))
	v, _ = e.svc.Subscription(t.Context(), e.org)
	if v.Plan.Code != "agency" || v.BillingInterval == nil || *v.BillingInterval != "month" {
		t.Fatalf("after switching to monthly: %+v", v)
	}

	// Once canceled, the organization is free again and no interval is shown.
	e.deliver(t, billingtest.SubscriptionEvent("evt_3", "customer.subscription.deleted", at(20), e.sub("canceled", "price_agency")))
	v, _ = e.svc.Subscription(t.Context(), e.org)
	if v.PaidAccess || v.BillingInterval != nil {
		t.Fatalf("after cancellation: %+v", v)
	}
}

func TestAWrongYearlyPriceRefusesOnlyYearlyCheckout(t *testing.T) {
	cases := map[string]func(p *billing.PriceInfo){
		"wrong amount":   func(p *billing.PriceInfo) { p.UnitAmount = 70800 }, // twelve months of $59: no discount
		"monthly price":  func(p *billing.PriceInfo) { p.Interval = "month" },
		"every 2 years":  func(p *billing.PriceInfo) { p.IntervalCount = 2 },
		"archived price": func(p *billing.PriceInfo) { p.Active = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.fake.Prices = goodPrices()
			p := e.fake.Prices["price_agency_year"]
			mutate(&p)
			e.fake.Prices["price_agency_year"] = p

			got := e.svc.VerifyPrices(t.Context())
			if len(got) != 1 || got[0].Plan != "agency_annual" {
				t.Fatalf("expected one agency_annual problem, got %+v", got)
			}
			t.Log(got[0].Message)
			if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency", "year"); !errors.Is(err, billing.ErrPriceMismatch) {
				t.Fatalf("yearly agency checkout must be refused: %v", err)
			}
			if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "agency", "month"); err != nil {
				t.Fatalf("monthly agency matches and must stay open: %v", err)
			}
			if e.priceGauge(t, "agency_annual") != 1 || e.priceGauge(t, "agency") != 0 || e.priceGauge(t, "pro_annual") != 0 {
				t.Fatal("gauge")
			}
		})
	}
}

func TestAYearlyPriceTheSiteDoesNotAdvertiseIsRefused(t *testing.T) {
	e := newEnv(t)
	e.fake.Prices = goodPrices()
	if _, err := e.pool.Exec(t.Context(), `UPDATE plans SET annual_price_cents = NULL WHERE code = 'pro'`); err != nil {
		t.Fatal(err)
	}
	got := e.svc.VerifyPrices(t.Context())
	if len(got) != 1 || got[0].Plan != "pro_annual" {
		t.Fatalf("got %+v", got)
	}
	if _, err := e.svc.CreateCheckout(t.Context(), e.org, "owner@example.com", "pro", "year"); !errors.Is(err, billing.ErrPriceMismatch) {
		t.Fatalf("got %v", err)
	}
}
