package billing

import (
	"slices"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/metrics"
)

func TestMapStatus(t *testing.T) {
	want := map[string]string{
		"active": "active", "trialing": "trialing", "past_due": "past_due", "incomplete": "incomplete",
		// Per Stripe: revoke access for canceled and unpaid; the rest never grant it either.
		"canceled": "canceled", "unpaid": "canceled", "incomplete_expired": "canceled", "paused": "canceled",
		"": "canceled", "some_future_status": "canceled",
	}
	for in, out := range want {
		if got := MapStatus(in); got != out {
			t.Errorf("%q -> %q, want %q", in, got, out)
		}
	}
}

func TestParseSubscriptionNewAPIShapePeriodOnItems(t *testing.T) {
	st, err := ParseSubscription([]byte(`{
		"id": "sub_1", "object": "subscription", "customer": "cus_1", "status": "active", "cancel_at_period_end": true,
		"metadata": {"organization_id": "2632f045-1667-4020-abe7-da7d4a2d718f"},
		"items": {"data": [{"price": {"id": "price_pro"}, "current_period_start": 1700000000, "current_period_end": 1702592000}]}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != "sub_1" || st.CustomerID != "cus_1" || st.Status != "active" || !st.CancelAtPeriodEnd || len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_pro" {
		t.Fatalf("%+v", st)
	}
	if st.PeriodStart == nil || !st.PeriodStart.Equal(time.Unix(1700000000, 0)) || st.PeriodEnd == nil || !st.PeriodEnd.Equal(time.Unix(1702592000, 0)) {
		t.Fatalf("period: %v %v", st.PeriodStart, st.PeriodEnd)
	}
	if st.OrganizationID == nil || st.OrganizationID.String() != "2632f045-1667-4020-abe7-da7d4a2d718f" {
		t.Fatalf("org: %v", st.OrganizationID)
	}
}

func TestParseSubscriptionOldShapeAndExpandedCustomer(t *testing.T) {
	st, err := ParseSubscription([]byte(`{"id":"sub_2","customer":{"id":"cus_2","object":"customer"},"status":"past_due",
		"current_period_start":1700000000,"current_period_end":1702592000,"items":{"data":[{"price":{"id":"price_a"}},{"price":{"id":"price_b"}}]}}`))
	if err != nil || st.CustomerID != "cus_2" || st.PeriodEnd == nil || len(st.PriceIDs) != 2 || st.OrganizationID != nil {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestParseSubscriptionRejectsIncompleteObjects(t *testing.T) {
	for _, bad := range []string{`{}`, `{"id":"sub"}`, `{"status":"active"}`, `not json`} {
		if _, err := ParseSubscription([]byte(bad)); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	// A malformed organization_id is ignored rather than trusted.
	st, _ := ParseSubscription([]byte(`{"id":"s","status":"active","metadata":{"organization_id":"nope"}}`))
	if st.OrganizationID != nil {
		t.Fatal("malformed org id accepted")
	}
}

func TestConfigValidation(t *testing.T) {
	ok := Config{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"}
	if err := ok.Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := (Config{}).Validate(false); err != nil {
		t.Fatalf("no settings = billing disabled, not an error: %v", err)
	}
	cases := map[string]Config{
		"partial: key only":     {SecretKey: "sk_test_x"},
		"partial: webhook only": {WebhookSecret: "whsec_x"},
		"bad key prefix":        {SecretKey: "pk_test_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"},
		"bad webhook prefix":    {SecretKey: "sk_test_x", WebhookSecret: "secret", PricePro: "price_1", PriceAgency: "price_2"},
		"bad price prefix":      {SecretKey: "sk_test_x", WebhookSecret: "whsec_x", PricePro: "prod_1", PriceAgency: "price_2"},
		"same price twice":      {SecretKey: "sk_test_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_1"},
		"live key outside prod": {SecretKey: "sk_live_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"},
		"restricted live key":   {SecretKey: "rk_live_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"},
	}
	for name, c := range cases {
		if err := c.Validate(false); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	live := Config{SecretKey: "sk_live_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"}
	if err := live.Validate(true); err != nil {
		t.Fatalf("live keys are fine in production: %v", err)
	}
}

func TestAnnualConfigValidation(t *testing.T) {
	base := func() Config {
		return Config{SecretKey: "sk_test_x", WebhookSecret: "whsec_x", PricePro: "price_1", PriceAgency: "price_2"}
	}
	both := base()
	both.PriceProAnnual, both.PriceAgencyAnnual = "price_3", "price_4"
	if err := both.Validate(false); err != nil || !both.AnnualEnabled() {
		t.Fatalf("both yearly prices: %v", err)
	}
	if base().AnnualEnabled() {
		t.Fatal("yearly billing must be off without yearly prices")
	}
	for name, mutate := range map[string]func(*Config){
		"pro only":              func(c *Config) { c.PriceProAnnual = "price_3" },
		"agency only":           func(c *Config) { c.PriceAgencyAnnual = "price_4" },
		"bad prefix":            func(c *Config) { c.PriceProAnnual, c.PriceAgencyAnnual = "prod_3", "price_4" },
		"yearly equals monthly": func(c *Config) { c.PriceProAnnual, c.PriceAgencyAnnual = "price_1", "price_4" },
		"yearly prices equal":   func(c *Config) { c.PriceProAnnual, c.PriceAgencyAnnual = "price_3", "price_3" },
	} {
		c := base()
		mutate(&c)
		err := c.Validate(false)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if name == "pro only" && !strings.Contains(err.Error(), EnvPriceAgencyAnnual) {
			t.Errorf("%s: the error should name the setting: %v", name, err)
		}
	}
}

// The mismatch gauge is exported for every price key from startup (the alert rule needs it to exist), and metrics cannot import billing to ask.
func TestEveryPriceKeyHasAGauge(t *testing.T) {
	m := metrics.New()
	mfs, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	for _, mf := range mfs {
		if mf.GetName() == "hawkeye_billing_price_mismatch" {
			for _, mm := range mf.GetMetric() {
				have = append(have, mm.GetLabel()[0].GetValue())
			}
		}
	}
	for _, k := range PriceKeys() {
		if !slices.Contains(have, k) {
			t.Errorf("no hawkeye_billing_price_mismatch{plan=%q} at startup (have %v)", k, have)
		}
	}
}

func TestPriceMapping(t *testing.T) {
	c := Config{PricePro: "price_p", PriceAgency: "price_a", PriceProAnnual: "price_py", PriceAgencyAnnual: "price_ay"}
	if p, ok := c.PriceForPlan("pro", IntervalMonth); !ok || p != "price_p" {
		t.Fatal("pro price")
	}
	if p, ok := c.PriceForPlan("agency", IntervalYear); !ok || p != "price_ay" {
		t.Fatal("agency yearly price")
	}
	if _, ok := c.PriceForPlan("free", IntervalMonth); ok {
		t.Fatal("free has no price")
	}
	if _, ok := c.PriceForPlan("enterprise", IntervalMonth); ok {
		t.Fatal("unknown plan")
	}
	if _, ok := c.PriceForPlan("pro", "week"); ok {
		t.Fatal("unknown interval")
	}
	if _, ok := (Config{PricePro: "price_p"}).PriceForPlan("pro", IntervalYear); ok {
		t.Fatal("no yearly price configured")
	}
	for price, want := range map[string][2]string{
		"price_p": {"pro", "month"}, "price_a": {"agency", "month"}, "price_py": {"pro", "year"}, "price_ay": {"agency", "year"},
	} {
		if code, interval, ok := c.PlanForPrice(price); !ok || code != want[0] || interval != want[1] {
			t.Errorf("%s -> %s %s", price, code, interval)
		}
	}
	if _, _, ok := c.PlanForPrice("price_other"); ok {
		t.Fatal("unmapped price must not map")
	}
	if _, _, ok := (Config{}).PlanForPrice(""); ok {
		t.Fatal("empty price must not map even when prices are unset")
	}
}

func TestRetryDelayGrowsAndSaturates(t *testing.T) {
	if eventRetryDelay(1) >= eventRetryDelay(2) || eventRetryDelay(2) >= eventRetryDelay(3) || eventRetryDelay(50) != eventRetryDelay(4) || eventRetryDelay(0) != eventRetryDelay(1) {
		t.Fatal("unexpected backoff")
	}
}
