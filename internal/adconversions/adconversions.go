// Package adconversions reports sign-ups, checkouts and purchases to OpenAI Ads from our server, through its Conversions API. The browser pixel
// (web/src/analytics.ts) reports the same events, but an ad blocker, a closed tab or a redirect to Stripe can stop it; OpenAI keeps whichever copy
// arrives first, because both carry the same event id.
//
// Privacy rules, the same as the pixel's:
//   - nothing is recorded unless the visitor had analytics on: the hawkeye_analytics cookie is not "off", and the browser sends no Global Privacy
//     Control signal unless the visitor turned analytics back on (analyticsEnabled in analytics.ts);
//   - what is sent: the event, its time, the page path, plan and price for a checkout or purchase, OpenAI's own two cookies (__oppref, __obref) and
//     a one-way hash of the account id. Never an email, name, IP address, Stripe id or monitoring data;
//   - a purchase has no browser behind it (Stripe's webhook), so it is reported only when the checkout that led to it was recorded, and it reuses
//     that checkout's context.
//
// Events are written to ad_conversion_events and sent by the worker (Sender). Recording never fails the request that triggered it.
package adconversions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// OpenAI's standard event names.
const (
	Registration    = "registration_completed"
	CheckoutStarted = "checkout_started"
	Subscription    = "subscription_created"
)

// The analytics choice cookie (web/src/analytics.ts) and OpenAI's own first-party cookies.
const (
	choiceCookie = "hawkeye_analytics"
	opprefCookie = "__oppref"
	obrefCookie  = "__obref"
)

// MaxAge is how long a checkout's context can lead to a purchase report, matching how long the browser waits for a purchase to be confirmed
// (PENDING_MAX_AGE_MS in purchaseConversion.ts). OpenAI also refuses events older than 7 days.
const MaxAge = 7 * 24 * time.Hour

// Browser is what a request tells us about measurement: whether the visitor allows it, and OpenAI's cookies when the pixel set them.
type Browser struct {
	Allowed       bool
	Oppref, Obref string
}

// FromRequest reads the visitor's analytics choice and OpenAI's cookies, the same way the browser decides whether the pixel may run.
func FromRequest(r *http.Request) Browser {
	b := Browser{Allowed: r.Header.Get("Sec-GPC") != "1"}
	if c, err := r.Cookie(choiceCookie); err == nil {
		switch c.Value {
		case "on":
			b.Allowed = true
		case "off":
			b.Allowed = false
		}
	}
	if !b.Allowed {
		return Browser{}
	}
	if c, err := r.Cookie(opprefCookie); err == nil && cookieValue(c.Value) {
		b.Oppref = c.Value
	}
	if c, err := r.Cookie(obrefCookie); err == nil && cookieValue(c.Value) {
		b.Obref = c.Value
	}
	return b
}

// cookieValue accepts an opaque token of visible ASCII up to 512 bytes. OpenAI asks for the value unmodified, so it is checked, never rewritten.
func cookieValue(v string) bool {
	if v == "" || len(v) > 512 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c <= ' ' || c >= 0x7f || c == '"' || c == ';' || c == ',' || c == '\\' {
			return false
		}
	}
	return true
}

func opaqueID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil))[:32]
}

// RegistrationEventID is the event id shared by the pixel and the server for an account's sign-up. It is a hash, so it reveals no account id.
func RegistrationEventID(userID uuid.UUID) string {
	return opaqueID("reg_", "hawkeye registration", userID.String())
}

// PurchaseEventID is the event id for the paid period an organization first bought, shared by the pixel and the server.
func PurchaseEventID(orgID uuid.UUID, periodEnd time.Time) string {
	return opaqueID("sub_", "hawkeye subscription", orgID.String(), strconv.FormatInt(periodEnd.Unix(), 10))
}

// NewCheckoutEventID is a random event id for one checkout, returned to the browser so the pixel sends the same one.
func NewCheckoutEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "chk_" + hex.EncodeToString(b[:])
}

// externalID is the stable customer id OpenAI matches on: a SHA-256 of the account id (never the email). OpenAI hashes nothing itself.
func externalID(userID uuid.UUID) string {
	sum := sha256.Sum256([]byte(userID.String()))
	return hex.EncodeToString(sum[:])
}

// Plan is the plan in a checkout or purchase: its code, "month" or "year", and the price of that period in cents.
type Plan struct {
	Code     string
	Interval string
	Cents    int
}

func (p Plan) id() string { return p.Code + "_" + p.Interval }

// checkoutData is OpenAI's "contents" shape: the plan as the one item bought.
func checkoutData(p Plan) map[string]any {
	return map[string]any{"type": "contents", "amount": p.Cents, "currency": "USD",
		"contents": []map[string]any{{"id": p.id(), "content_type": "plan", "quantity": 1, "amount": p.Cents, "currency": "USD"}}}
}

// subscriptionData is OpenAI's "plan_enrollment" shape, matching trackSubscription in analytics.ts.
func subscriptionData(p Plan) map[string]any {
	return map[string]any{"type": "plan_enrollment", "plan_id": p.id(), "amount": p.Cents, "currency": "USD"}
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// DB is a pool or a transaction.
type DB interface {
	execer
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PlanFor looks up what a plan costs for one billing period ("month" or "year"), in cents, as the plans table advertises it.
func PlanFor(ctx context.Context, q DB, code, interval string) (Plan, error) {
	if interval != "year" {
		interval = "month"
	}
	p := Plan{Code: code, Interval: interval}
	err := q.QueryRow(ctx, `SELECT CASE WHEN $2 = 'year' THEN COALESCE(annual_price_cents, monthly_price_cents) ELSE monthly_price_cents END
		FROM plans WHERE code = $1`, code, interval).Scan(&p.Cents)
	return p, err
}

type event struct {
	typ, id string
	userID  uuid.UUID
	orgID   *uuid.UUID
	at      time.Time
	path    string
	data    map[string]any
	b       Browser
}

func insert(ctx context.Context, q execer, e event) error {
	data, err := json.Marshal(e.data)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO ad_conversion_events (event_type, event_id, user_id, organization_id, occurred_at, source_path, data, oppref, obref)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, '')) ON CONFLICT (event_type, event_id) DO NOTHING`,
		e.typ, e.id, e.userID, e.orgID, e.at, e.path, data, e.b.Oppref, e.b.Obref)
	if err != nil {
		return fmt.Errorf("record %s: %w", e.typ, err)
	}
	return nil
}

// Recorder writes the events the API sees. A disabled recorder (no OPENAI_ADS_API_KEY) records nothing, so no data is kept that will never be sent.
type Recorder struct {
	db      DB
	enabled bool
}

func NewRecorder(db DB, enabled bool) *Recorder { return &Recorder{db: db, enabled: enabled} }

// Enabled reports whether events are recorded. A nil recorder is disabled.
func (r *Recorder) Enabled() bool { return r != nil && r.enabled }

// Registration records a completed sign-up, from the sign-up page.
func (r *Recorder) Registration(ctx context.Context, b Browser, userID uuid.UUID) error {
	if !r.Enabled() || !b.Allowed {
		return nil
	}
	return insert(ctx, r.db, event{typ: Registration, id: RegistrationEventID(userID), userID: userID, at: time.Now(), path: "/register",
		data: map[string]any{"type": "customer_action"}, b: b})
}

// Checkout records that an owner was sent to Checkout for a plan, from the billing page. Its context is what a later purchase is reported with.
func (r *Recorder) Checkout(ctx context.Context, b Browser, userID, orgID uuid.UUID, eventID, planCode, interval string) error {
	if !r.Enabled() || !b.Allowed {
		return nil
	}
	p, err := PlanFor(ctx, r.db, planCode, interval)
	if err != nil {
		return fmt.Errorf("record %s: %w", CheckoutStarted, err)
	}
	return insert(ctx, r.db, event{typ: CheckoutStarted, id: eventID, userID: userID, orgID: &orgID, at: time.Now(), path: "/billing",
		data: checkoutData(p), b: b})
}

// RecordPurchase records an organization's first paid period, in the billing transaction that granted it, when a checkout for the organization
// was recorded within MaxAge: that is the evidence the buyer allowed measurement, and it supplies the buyer and OpenAI's cookies. Without one
// nothing is written. It reports whether an event was written.
func RecordPurchase(ctx context.Context, q execer, orgID uuid.UUID, periodEnd time.Time, p Plan) (bool, error) {
	data, err := json.Marshal(subscriptionData(p))
	if err != nil {
		return false, err
	}
	tag, err := q.Exec(ctx, `INSERT INTO ad_conversion_events (event_type, event_id, user_id, organization_id, occurred_at, source_path, data, oppref, obref)
		SELECT $1, $2, user_id, organization_id, now(), '/billing', $3, oppref, obref FROM ad_conversion_events
		WHERE organization_id = $4 AND event_type = $5 AND occurred_at > now() - make_interval(secs => $6)
		ORDER BY occurred_at DESC LIMIT 1
		ON CONFLICT (event_type, event_id) DO NOTHING`,
		Subscription, PurchaseEventID(orgID, periodEnd), data, orgID, CheckoutStarted, MaxAge.Seconds())
	if err != nil {
		return false, fmt.Errorf("record %s: %w", Subscription, err)
	}
	return tag.RowsAffected() > 0, nil
}

// Exported is one recorded event, as the account's data export shows it.
type Exported struct {
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Page       string          `json:"page"`
	Data       json.RawMessage `json:"data"`
	AdClick    bool            `json:"ad_click_reference"` // whether OpenAI's ad-click cookie (__oppref) was included
	BrowserRef bool            `json:"browser_reference"`  // whether OpenAI's browser cookie (__obref) was included
	Status     string          `json:"status"`             // pending, sent, failed or expired
	SentAt     *time.Time      `json:"sent_at,omitempty"`
}

// ForUser lists the events recorded for an account (kept for 30 days), for the data export.
func ForUser(ctx context.Context, q interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, userID uuid.UUID) ([]Exported, error) {
	rows, err := q.Query(ctx, `SELECT event_type, occurred_at, source_path, data, oppref IS NOT NULL, obref IS NOT NULL, status, sent_at
		FROM ad_conversion_events WHERE user_id = $1 ORDER BY occurred_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exported{}
	for rows.Next() {
		var e Exported
		if err := rows.Scan(&e.Type, &e.OccurredAt, &e.Page, &e.Data, &e.AdClick, &e.BrowserRef, &e.Status, &e.SentAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
