package adconversions_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/adconversions"
	"hawkeye/internal/metrics"
	"hawkeye/internal/testutil"
)

var (
	allowed = adconversions.Browser{Allowed: true, Oppref: "click-1", Obref: "browser-1"}
	quiet   = slog.New(slog.NewTextHandler(io.Discard, nil))
)

func account(t *testing.T, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var uid, org uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, uuid.NewString()+"@example.test").Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	return uid, org
}

func count(t *testing.T, pool *pgxpool.Pool, where string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM ad_conversion_events WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecorderKeepsNothingWhenOffOrOptedOut(t *testing.T) {
	pool := testutil.NewPool(t)
	uid, org := account(t, pool)
	off := adconversions.NewRecorder(pool, false)
	on := adconversions.NewRecorder(pool, true)
	for _, err := range []error{
		off.Registration(t.Context(), allowed, uid),
		off.Checkout(t.Context(), allowed, uid, org, "chk_a", "pro", "month"),
		on.Registration(t.Context(), adconversions.Browser{}, uid),
		on.Checkout(t.Context(), adconversions.Browser{}, uid, org, "chk_b", "pro", "month"),
		(*adconversions.Recorder)(nil).Registration(t.Context(), allowed, uid),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, "true"); n != 0 {
		t.Fatalf("%d events recorded", n)
	}
}

func TestPurchaseIsRecordedOnlyAfterARecordedCheckout(t *testing.T) {
	pool := testutil.NewPool(t)
	rec := adconversions.NewRecorder(pool, true)
	uid, org := account(t, pool)
	end := time.Now().AddDate(1, 0, 0).Truncate(time.Second)
	p, err := adconversions.PlanFor(t.Context(), pool, "pro", "year")
	if err != nil || p.Cents != 19000 {
		t.Fatalf("plan %+v %v", p, err)
	}

	// No checkout recorded (opted out, or no API key at the time): nothing is reported.
	if ok, err := adconversions.RecordPurchase(t.Context(), pool, org, end, p); ok || err != nil {
		t.Fatalf("recorded without a checkout: %v %v", ok, err)
	}
	if err := rec.Checkout(t.Context(), allowed, uid, org, "chk_1", "pro", "year"); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := pool.QueryRow(t.Context(), `SELECT data FROM ad_conversion_events WHERE event_id = 'chk_1'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if data["type"] != "contents" || data["amount"] != float64(19000) {
		t.Fatalf("checkout data %v", data)
	}
	if ok, err := adconversions.RecordPurchase(t.Context(), pool, org, end, p); !ok || err != nil {
		t.Fatalf("purchase not recorded: %v %v", ok, err)
	}
	// Recording the same period again (a redelivered webhook) does nothing.
	if ok, _ := adconversions.RecordPurchase(t.Context(), pool, org, end, p); ok {
		t.Fatal("recorded twice")
	}
	var user uuid.UUID
	var oppref, obref, id string
	if err := pool.QueryRow(t.Context(), `SELECT user_id, oppref, obref, event_id FROM ad_conversion_events WHERE event_type = 'subscription_created'`).
		Scan(&user, &oppref, &obref, &id); err != nil {
		t.Fatal(err)
	}
	if user != uid || oppref != "click-1" || obref != "browser-1" || id != adconversions.PurchaseEventID(org, end) {
		t.Fatalf("purchase context: %v %q %q %q", user, oppref, obref, id)
	}

	// A checkout older than the window no longer leads to a report.
	_, org2 := account(t, pool)
	if err := rec.Checkout(t.Context(), allowed, uid, org2, "chk_2", "pro", "month"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE ad_conversion_events SET occurred_at = now() - interval '8 days' WHERE event_id = 'chk_2'`); err != nil {
		t.Fatal(err)
	}
	if ok, _ := adconversions.RecordPurchase(t.Context(), pool, org2, end, p); ok {
		t.Fatal("a stale checkout led to a purchase report")
	}
}

type fakeOpenAI struct {
	mu       sync.Mutex
	status   func(events []map[string]any) int
	requests [][]map[string]any
	headers  []http.Header
	query    []string
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct{ Events []map[string]any }
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.requests = append(f.requests, body.Events)
	f.headers = append(f.headers, r.Header.Clone())
	f.query = append(f.query, r.URL.RawQuery)
	f.mu.Unlock()
	code := http.StatusOK
	if f.status != nil {
		code = f.status(body.Events)
	}
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{}`))
}

func sender(t *testing.T, pool *pgxpool.Pool, f *fakeOpenAI) (*adconversions.Sender, *metrics.Metrics) {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	m := metrics.New()
	return adconversions.NewSender(pool, adconversions.SenderConfig{APIKey: "secret-key", PixelID: "pix1", AppBaseURL: "https://app.example.test",
		Endpoint: srv.URL + "/v1/events", Client: srv.Client()}, m, quiet), m
}

func TestSenderDeliversOnceWithTheKeyAndPixel(t *testing.T) {
	pool := testutil.NewPool(t)
	rec := adconversions.NewRecorder(pool, true)
	uid, org := account(t, pool)
	_ = rec.Registration(t.Context(), allowed, uid)
	_ = rec.Checkout(t.Context(), allowed, uid, org, "chk_1", "agency", "month")
	f := &fakeOpenAI{}
	s, _ := sender(t, pool, f)
	st, err := s.RunOnce(t.Context())
	if err != nil || st.Sent != 2 {
		t.Fatalf("stats %+v err %v", st, err)
	}
	if len(f.requests) != 1 || len(f.requests[0]) != 2 {
		t.Fatalf("requests %v", f.requests)
	}
	if f.headers[0].Get("Authorization") != "Bearer secret-key" || f.query[0] != "pid=pix1" {
		t.Fatalf("auth %q query %q", f.headers[0].Get("Authorization"), f.query[0])
	}
	got := f.requests[0][0]
	if got["type"] != "registration_completed" {
		got = f.requests[0][1] // recorded in the same instant, so either may come first
	}
	if got["type"] != "registration_completed" || got["id"] != adconversions.RegistrationEventID(uid) ||
		got["source_url"] != "https://app.example.test/register" || got["oppref"] != "click-1" {
		t.Fatalf("event %v", got)
	}
	if st, _ := s.RunOnce(t.Context()); st.Sent != 0 || len(f.requests) != 1 {
		t.Fatal("an event was sent twice")
	}
	if n := count(t, pool, "status = 'sent' AND sent_at IS NOT NULL"); n != 2 {
		t.Fatalf("%d marked sent", n)
	}
}

func TestSenderSetsAsideARejectedEventAndRetriesOutages(t *testing.T) {
	pool := testutil.NewPool(t)
	rec := adconversions.NewRecorder(pool, true)
	uid, org := account(t, pool)
	_ = rec.Checkout(t.Context(), allowed, uid, org, "chk_bad", "pro", "month")
	_ = rec.Checkout(t.Context(), allowed, uid, org, "chk_good", "pro", "month")
	f := &fakeOpenAI{status: func(ev []map[string]any) int {
		for _, e := range ev {
			if e["id"] == "chk_bad" {
				return http.StatusBadRequest
			}
		}
		return http.StatusOK
	}}
	s, _ := sender(t, pool, f)
	st, err := s.RunOnce(t.Context())
	if err != nil || st.Sent != 1 || st.Rejected != 1 {
		t.Fatalf("stats %+v err %v", st, err)
	}
	if count(t, pool, "event_id = 'chk_bad' AND status = 'failed' AND last_error LIKE 'HTTP 400%'") != 1 || count(t, pool, "event_id = 'chk_good' AND status = 'sent'") != 1 {
		t.Fatal("the bad event must be set aside and the good one sent")
	}

	// An outage leaves the event pending for a later attempt; after too many attempts it gives up.
	_ = rec.Registration(t.Context(), allowed, uid)
	f.status = func([]map[string]any) int { return http.StatusServiceUnavailable }
	if st, _ := s.RunOnce(t.Context()); st.Retried != 1 {
		t.Fatalf("stats %+v", st)
	}
	if count(t, pool, "event_type = 'registration_completed' AND status = 'pending' AND next_attempt_at > now()") != 1 {
		t.Fatal("the event must wait before its next attempt")
	}
	_, _ = pool.Exec(t.Context(), `UPDATE ad_conversion_events SET next_attempt_at = now(), attempt_count = 9 WHERE status = 'pending'`)
	_, _ = s.RunOnce(t.Context())
	if count(t, pool, "event_type = 'registration_completed' AND status = 'failed'") != 1 {
		t.Fatal("gave up after the last attempt")
	}
}

func TestSenderExpiresEventsOpenAIWouldRefuseAndDeletesOldRows(t *testing.T) {
	pool := testutil.NewPool(t)
	rec := adconversions.NewRecorder(pool, true)
	uid, _ := account(t, pool)
	_ = rec.Registration(t.Context(), allowed, uid)
	_, _ = pool.Exec(t.Context(), `UPDATE ad_conversion_events SET occurred_at = now() - interval '7 days'`)
	uid2, _ := account(t, pool)
	_ = rec.Registration(t.Context(), allowed, uid2)
	_, _ = pool.Exec(t.Context(), `UPDATE ad_conversion_events SET created_at = now() - interval '31 days' WHERE user_id = $1`, uid2)
	f := &fakeOpenAI{}
	s, _ := sender(t, pool, f)
	st, err := s.RunOnce(t.Context())
	if err != nil || st.Expired != 1 || st.Purged != 1 || st.Sent != 0 || len(f.requests) != 0 {
		t.Fatalf("stats %+v err %v requests %d", st, err, len(f.requests))
	}
}

func TestEventsAreDeletedWithTheAccountAndShownInTheExport(t *testing.T) {
	pool := testutil.NewPool(t)
	rec := adconversions.NewRecorder(pool, true)
	uid, _ := account(t, pool)
	_ = rec.Registration(t.Context(), allowed, uid)
	got, err := adconversions.ForUser(t.Context(), pool, uid)
	if err != nil || len(got) != 1 || got[0].Type != "registration_completed" || !got[0].AdClick || got[0].Status != "pending" {
		t.Fatalf("export %+v %v", got, err)
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "true"); n != 0 {
		t.Fatalf("%d events left after the account was deleted", n)
	}
}
