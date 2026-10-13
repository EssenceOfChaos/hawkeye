package billing_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v87"

	"hawkeye/internal/billing"
)

type recorded struct {
	Method, Path, Auth, IdemKey string
	Form                        url.Values
}

// fakeStripe is a local stand-in for api.stripe.com: it records what the official SDK sends and replies in Stripe's JSON shapes.
// It proves our provider builds the right requests and reads responses correctly; it cannot prove Stripe accepts them.
type fakeStripe struct {
	mu   sync.Mutex
	reqs []recorded
	fail bool
}

func (f *fakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	f.mu.Lock()
	f.reqs = append(f.reqs, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), form})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.fail {
		w.WriteHeader(402)
		io.WriteString(w, `{"error":{"type":"card_error","message":"internal detail that must not leak"}}`)
		return
	}
	switch {
	case r.Method == http.MethodDelete && r.URL.Path == "/v1/customers/cus_gone":
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"type":"invalid_request_error","code":"resource_missing","message":"No such customer: 'cus_gone'"}}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/customers/"):
		io.WriteString(w, `{"id":"`+strings.TrimPrefix(r.URL.Path, "/v1/customers/")+`","object":"customer","deleted":true}`)
	case r.URL.Path == "/v1/customers":
		io.WriteString(w, `{"id":"cus_created_1","object":"customer","email":"owner@example.com"}`)
	case r.URL.Path == "/v1/checkout/sessions":
		io.WriteString(w, `{"id":"cs_test_1","object":"checkout.session","url":"https://checkout.stripe.test/c/pay/cs_test_1"}`)
	case r.URL.Path == "/v1/billing_portal/sessions":
		io.WriteString(w, `{"id":"bps_1","object":"billing_portal.session","url":"https://billing.stripe.test/p/session/x"}`)
	case r.URL.Path == "/v1/subscriptions/sub_1":
		cancel := r.Method == http.MethodPost && form.Get("cancel_at_period_end") == "true"
		io.WriteString(w, `{"id":"sub_1","object":"subscription","customer":"cus_1","status":"active","cancel_at_period_end":`+map[bool]string{true: "true", false: "false"}[cancel]+`,
			"metadata":{"organization_id":"2632f045-1667-4020-abe7-da7d4a2d718f"},
			"items":{"object":"list","has_more":false,"data":[{"id":"si_1","object":"subscription_item","current_period_start":1700000000,"current_period_end":1702592000,
				"price":{"id":"price_pro","object":"price"}}]}}`)
	default:
		w.WriteHeader(404)
		io.WriteString(w, `{"error":{"message":"no route"}}`)
	}
}

func (f *fakeStripe) last() recorded { f.mu.Lock(); defer f.mu.Unlock(); return f.reqs[len(f.reqs)-1] }

func newProvider(t *testing.T) (*billing.StripeProvider, *fakeStripe) {
	t.Helper()
	fs := &fakeStripe{}
	srv := httptest.NewServer(fs)
	t.Cleanup(srv.Close)
	backends := stripe.NewBackendsWithConfig(&stripe.BackendConfig{URL: stripe.String(srv.URL), MaxNetworkRetries: stripe.Int64(0)})
	return billing.NewStripeProvider("sk_test_abc123", stripe.WithBackends(backends)), fs
}

func TestStripeProviderCheckoutRequest(t *testing.T) {
	p, fs := newProvider(t)
	org := uuid.MustParse("2632f045-1667-4020-abe7-da7d4a2d718f")
	url, err := p.CreateCheckoutSession(t.Context(), billing.CheckoutParams{
		OrganizationID: org, PriceID: "price_pro", CustomerEmail: "owner@example.com",
		SuccessURL: "https://app.example.test/billing?checkout=success", CancelURL: "https://app.example.test/billing?checkout=canceled",
	})
	if err != nil || url != "https://checkout.stripe.test/c/pay/cs_test_1" {
		t.Fatalf("%q %v", url, err)
	}
	r := fs.last()
	if r.Method != "POST" || r.Path != "/v1/checkout/sessions" || r.Auth != "Bearer sk_test_abc123" {
		t.Fatalf("%+v", r)
	}
	want := map[string]string{
		"mode": "subscription", "line_items[0][price]": "price_pro", "line_items[0][quantity]": "1",
		"success_url": "https://app.example.test/billing?checkout=success", "cancel_url": "https://app.example.test/billing?checkout=canceled",
		"client_reference_id": org.String(), "metadata[organization_id]": org.String(),
		"subscription_data[metadata][organization_id]": org.String(), "customer_email": "owner@example.com",
	}
	for k, v := range want {
		if got := r.Form.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if r.Form.Get("customer") != "" {
		t.Error("customer and customer_email are mutually exclusive")
	}
}

func TestStripeProviderReusesExistingCustomer(t *testing.T) {
	p, fs := newProvider(t)
	_, err := p.CreateCheckoutSession(t.Context(), billing.CheckoutParams{
		OrganizationID: uuid.New(), PriceID: "price_agency", CustomerID: "cus_77", CustomerEmail: "ignored@example.com",
		SuccessURL: "https://a/s", CancelURL: "https://a/c",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := fs.last()
	if r.Form.Get("customer") != "cus_77" || r.Form.Get("customer_email") != "" {
		t.Fatalf("%v", r.Form)
	}
}

func TestStripeProviderPortalRequest(t *testing.T) {
	p, fs := newProvider(t)
	url, err := p.CreatePortalSession(t.Context(), "cus_1", "https://app.example.test/billing")
	if err != nil || url != "https://billing.stripe.test/p/session/x" {
		t.Fatalf("%q %v", url, err)
	}
	if r := fs.last(); r.Path != "/v1/billing_portal/sessions" || r.Form.Get("customer") != "cus_1" || r.Form.Get("return_url") != "https://app.example.test/billing" {
		t.Fatalf("%+v", r)
	}
}

func TestStripeProviderSubscriptionRoundTrip(t *testing.T) {
	p, fs := newProvider(t)
	st, err := p.GetSubscription(t.Context(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if r := fs.last(); r.Method != "GET" || r.Path != "/v1/subscriptions/sub_1" {
		t.Fatalf("%+v", r)
	}
	if st.ID != "sub_1" || st.CustomerID != "cus_1" || st.Status != "active" || st.CancelAtPeriodEnd || len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_pro" ||
		st.PeriodEnd == nil || st.PeriodEnd.Unix() != 1702592000 || st.OrganizationID == nil || st.OrganizationID.String() != "2632f045-1667-4020-abe7-da7d4a2d718f" {
		t.Fatalf("SDK response was not interpreted correctly: %+v", st)
	}

	st, err = p.CancelAtPeriodEnd(t.Context(), "sub_1")
	if err != nil || !st.CancelAtPeriodEnd {
		t.Fatalf("%+v %v", st, err)
	}
	if r := fs.last(); r.Method != "POST" || r.Path != "/v1/subscriptions/sub_1" || r.Form.Get("cancel_at_period_end") != "true" {
		t.Fatalf("cancel must set cancel_at_period_end (not delete immediately): %+v", r)
	}
}

func TestStripeProviderErrorsAreReturned(t *testing.T) {
	p, fs := newProvider(t)
	fs.fail = true
	if _, err := p.CreateCheckoutSession(t.Context(), billing.CheckoutParams{OrganizationID: uuid.New(), PriceID: "price_pro", SuccessURL: "https://a", CancelURL: "https://b"}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := p.CreatePortalSession(t.Context(), "cus_1", "https://a"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := p.GetSubscription(t.Context(), "sub_1"); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := p.CancelAtPeriodEnd(t.Context(), "sub_1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStripeProviderCreatesCustomerIdempotentlyPerOrganization(t *testing.T) {
	p, fs := newProvider(t)
	org := uuid.MustParse("2632f045-1667-4020-abe7-da7d4a2d718f")
	id, err := p.CreateCustomer(t.Context(), "owner@example.com", org)
	if err != nil || id != "cus_created_1" {
		t.Fatalf("%q %v", id, err)
	}
	r := fs.last()
	if r.Method != "POST" || r.Path != "/v1/customers" || r.Form.Get("email") != "owner@example.com" || r.Form.Get("metadata[organization_id]") != org.String() {
		t.Fatalf("%+v", r)
	}
	if r.IdemKey != "hawkeye-customer-"+org.String() {
		t.Fatalf("the idempotency key must be stable per organization so a double click cannot create two customers: %q", r.IdemKey)
	}
}

func TestStripeProviderDeleteCustomer(t *testing.T) {
	p, fs := newProvider(t)
	if err := p.DeleteCustomer(t.Context(), "cus_123"); err != nil {
		t.Fatal(err)
	}
	if r := fs.last(); r.Method != "DELETE" || r.Path != "/v1/customers/cus_123" {
		t.Fatalf("%+v", r)
	}
	// An already-deleted customer counts as success so that a retried account deletion can finish.
	if err := p.DeleteCustomer(t.Context(), "cus_gone"); err != nil {
		t.Fatalf("a missing customer must not block deletion: %v", err)
	}
	fs.fail = true
	if err := p.DeleteCustomer(t.Context(), "cus_123"); err == nil {
		t.Fatal("other provider errors must be returned so deletion is aborted")
	}
}
