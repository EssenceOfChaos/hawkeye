package adconversions

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func browser(t *testing.T, cookie string, gpc bool) Browser {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if gpc {
		r.Header.Set("Sec-GPC", "1")
	}
	return FromRequest(r)
}

func TestFromRequestFollowsTheBrowsersAnalyticsChoice(t *testing.T) {
	cookies := "__oppref=click.123; __obref=b-456"
	for name, c := range map[string]struct {
		cookie string
		gpc    bool
		want   bool
	}{
		"no choice, no signal: on by default": {cookies, false, true},
		"turned off":                          {cookies + "; hawkeye_analytics=off", false, false},
		"Global Privacy Control":              {cookies, true, false},
		"turned back on despite GPC":          {cookies + "; hawkeye_analytics=on", true, true},
	} {
		b := browser(t, c.cookie, c.gpc)
		if b.Allowed != c.want {
			t.Errorf("%s: allowed = %v, want %v", name, b.Allowed, c.want)
		}
		if c.want && (b.Oppref != "click.123" || b.Obref != "b-456") {
			t.Errorf("%s: cookies not read unchanged: %+v", name, b)
		}
		if !c.want && (b.Oppref != "" || b.Obref != "") {
			t.Errorf("%s: an opted-out browser's cookies must not be kept: %+v", name, b)
		}
	}
}

func TestFromRequestDropsCookieValuesThatAreNotOpaqueTokens(t *testing.T) {
	b := browser(t, `__oppref=`+strings.Repeat("a", 513), false)
	if !b.Allowed || b.Oppref != "" {
		t.Fatalf("got %+v", b)
	}
	for _, bad := range []string{"", "a b", "a;b", `a"b`, "a,b", `a\b`, "caf\u00e9", "tab\t"} {
		if cookieValue(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	if !cookieValue("AbC.1-2_3~:/+=%") {
		t.Error("a plain token is accepted unchanged")
	}
}

func TestEventIDsAreStableOpaqueAndDistinct(t *testing.T) {
	u, o := uuid.New(), uuid.New()
	end := time.Unix(1_800_000_000, 0)
	if RegistrationEventID(u) != RegistrationEventID(u) || PurchaseEventID(o, end) != PurchaseEventID(o, end.Add(time.Millisecond)) {
		t.Fatal("ids must be the same each time (and a purchase id depends on the period end's second only)")
	}
	if strings.Contains(RegistrationEventID(u), u.String()) || strings.Contains(PurchaseEventID(o, end), o.String()) {
		t.Fatal("ids must not reveal account or organization ids")
	}
	if PurchaseEventID(o, end) == PurchaseEventID(uuid.New(), end) || PurchaseEventID(o, end) == PurchaseEventID(o, end.Add(time.Second)) {
		t.Fatal("two purchases must not share an id")
	}
	if a, b := NewCheckoutEventID(), NewCheckoutEventID(); a == b || !strings.HasPrefix(a, "chk_") || len(a) > 100 {
		t.Fatalf("checkout ids: %q %q", a, b)
	}
}

func TestPayloadHasOpenAIsShapeAndNoPersonalData(t *testing.T) {
	s := NewSender(nil, SenderConfig{APIKey: "k", PixelID: "p", AppBaseURL: "https://app.example.test/"}, nil, nil)
	uid := uuid.New()
	ref := "click.1"
	at := time.UnixMilli(1_790_000_000_123)
	data, _ := json.Marshal(subscriptionData(Plan{Code: "pro", Interval: "year", Cents: 19000}))
	body, err := s.payload([]row{{typ: Subscription, eventID: "sub_x", userID: &uid, at: at, path: "/billing", data: data, oppref: &ref}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		IntegrationSource string `json:"integration_source"`
		Events            []map[string]any
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.IntegrationSource != "hawkeye" || len(got.Events) != 1 {
		t.Fatalf("body %s", body)
	}
	e := got.Events[0]
	if e["id"] != "sub_x" || e["type"] != "subscription_created" || e["timestamp_ms"] != float64(1_790_000_000_123) || e["action_source"] != "web" ||
		e["source_url"] != "https://app.example.test/billing" || e["oppref"] != "click.1" {
		t.Fatalf("event %v", e)
	}
	user := e["user"].(map[string]any)
	ids := user["external_ids_sha256"].([]any)
	if len(ids) != 1 || len(ids[0].(string)) != 64 || ids[0] == uid.String() {
		t.Fatalf("external id must be a SHA-256 hex digest of the account id, got %v", ids)
	}
	if _, ok := user["obref"]; ok {
		t.Fatal("obref is omitted when the browser had none")
	}
	if d := e["data"].(map[string]any); d["type"] != "plan_enrollment" || d["plan_id"] != "pro_year" || d["amount"] != float64(19000) || d["currency"] != "USD" {
		t.Fatalf("data %v", d)
	}
	for _, banned := range []string{"@", "email", "ip_address", "user_agent", uid.String()} {
		if strings.Contains(string(body), banned) {
			t.Fatalf("body contains %q: %s", banned, body)
		}
	}
}
