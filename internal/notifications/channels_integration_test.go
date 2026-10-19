package notifications_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/notifications"
	"hawkeye/internal/testutil"
)

// receiver is a fake webhook/Slack endpoint that records what it is sent.
type receiver struct {
	*httptest.Server
	mu     sync.Mutex
	status []int // consumed one per request; the last one repeats
	got    []request
}

type request struct {
	Path   string
	Header http.Header
	Body   []byte
}

func newReceiver(t *testing.T, status ...int) *receiver {
	t.Helper()
	r := &receiver{status: status}
	r.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.got = append(r.got, request{req.URL.Path, req.Header.Clone(), body})
		code := r.status[min(len(r.got), len(r.status))-1]
		r.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *receiver) requests() []request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]request(nil), r.got...)
}

// client sends every request to the receiver whatever its host, so tests can use realistic public host names
// (the production client would refuse 127.0.0.1) while the URL validation and sealing code stays unchanged.
func (r *receiver) client() *http.Client {
	base := r.Server.Client().Transport
	u, _ := url.Parse(r.URL)
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Host = u.Host
		return base.RoundTrip(req)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// urlDelivery stores a sealed webhook/Slack destination for the env's organization and queues a delivery to it.
func (e *env) urlDelivery(t *testing.T, channel, target, event string) (setting, delivery uuid.UUID, secret string) {
	t.Helper()
	box := testutil.SecretBox()
	setting = uuid.New()
	sealed, err := box.Seal(target, setting.String())
	if err != nil {
		t.Fatal(err)
	}
	var sealedSecret *string
	if channel == notifications.ChannelWebhook {
		secret = notifications.NewSigningSecret()
		v, _ := box.Seal(secret, setting.String())
		sealedSecret = &v
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO notification_settings (id, organization_id, channel, destination, label, fingerprint, signing_secret, verified_at)
		VALUES ($1, $2, $3, $4, 'hooks.example.com/…', $5, $6, now())`, setting, e.org, channel, sealed, uuid.NewString(), sealedSecret); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type)
		VALUES ($1, $2, $3) RETURNING id`, e.inc, setting, event).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	return
}

func TestWebhookDeliveryIsSignedAndSentOnce(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 200)
	_, id, secret := e.urlDelivery(t, "webhook", "https://hooks.example.com/in/token123", "outage")
	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{}).WithSecrets(testutil.SecretBox()).WithHTTPClient(rcv.client())

	if worked, err := d.ProcessOne(t.Context()); !worked || err != nil {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if r := e.row(t, id); r.Status != "sent" || r.Attempts != 1 || r.LastErr != nil {
		t.Fatalf("%+v", r)
	}
	got := rcv.requests()
	if len(got) != 1 || got[0].Path != "/in/token123" {
		t.Fatalf("%+v", got)
	}
	h := got[0].Header
	ts, _ := json.Number(h.Get("X-HawkEye-Timestamp")).Int64()
	if want := notifications.Sign(secret, ts, got[0].Body); h.Get("X-HawkEye-Signature") != want || h.Get("X-HawkEye-Event") != "outage" || h.Get("X-HawkEye-Delivery") != id.String() {
		t.Fatalf("headers %v, want signature %s", h, want)
	}
	var p notifications.WebhookPayload
	if err := json.Unmarshal(got[0].Body, &p); err != nil || p.Event != "outage" || p.Monitor == nil || p.Monitor.Name != "Site" || p.Incident == nil || p.Incident.Cause != "Received HTTP 503" {
		t.Fatalf("%s %v", got[0].Body, err)
	}
	if worked, _ := d.ProcessOne(t.Context()); worked || len(rcv.requests()) != 1 {
		t.Fatal("a sent delivery must never be sent again")
	}
}

func TestSlackDeliverySendsASlackMessage(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 200)
	_, id, _ := e.urlDelivery(t, "slack", "https://hooks.slack.com/services/T0/B0/XXXX", "recovery")
	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{}).WithSecrets(testutil.SecretBox()).WithHTTPClient(rcv.client())
	d.ProcessOne(t.Context())
	got := rcv.requests()
	if e.row(t, id).Status != "sent" || len(got) != 1 || got[0].Path != "/services/T0/B0/XXXX" {
		t.Fatalf("%+v %+v", e.row(t, id), got)
	}
	var m map[string]any
	if err := json.Unmarshal(got[0].Body, &m); err != nil || m["blocks"] == nil || !strings.Contains(m["text"].(string), "back online") {
		t.Fatalf("%s %v", got[0].Body, err)
	}
	if got[0].Header.Get("X-HawkEye-Signature") != "" {
		t.Fatal("Slack messages are not signed")
	}
}

func TestWebhookServerErrorIsRetriedWithTheSameDeliveryID(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 503, 502, 200)
	_, id, _ := e.urlDelivery(t, "webhook", "https://hooks.example.com/in/token", "outage")
	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{MaxAttempts: 5}).WithSecrets(testutil.SecretBox()).WithHTTPClient(rcv.client())
	for range 3 {
		d.ProcessOne(t.Context())
	}
	got := rcv.requests()
	if r := e.row(t, id); r.Status != "sent" || r.Attempts != 3 || len(got) != 3 {
		t.Fatalf("%+v requests=%d", r, len(got))
	}
	for _, g := range got {
		if g.Header.Get("X-HawkEye-Delivery") != id.String() {
			t.Fatal("receivers dedupe on the delivery id, so it must be stable across retries")
		}
	}
}

func TestWebhookRejectionFailsPermanentlyWithoutLeakingTheURL(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 410)
	_, id, _ := e.urlDelivery(t, "slack", "https://hooks.slack.com/services/T0/B0/TOPSECRET", "outage")
	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{MaxAttempts: 5}).WithSecrets(testutil.SecretBox()).WithHTTPClient(rcv.client())
	d.ProcessOne(t.Context())
	r := e.row(t, id)
	if r.Status != "failed" || r.Attempts != 1 || r.LastErr == nil || !strings.Contains(*r.LastErr, "410") || strings.Contains(*r.LastErr, "TOPSECRET") {
		t.Fatalf("%+v %v", r, r.LastErr)
	}
	if worked, _ := d.ProcessOne(t.Context()); worked {
		t.Fatal("a permanent failure must not be retried")
	}
}

func TestUnopenableDestinationFailsPermanently(t *testing.T) {
	e := newEnv(t, 5)
	_, id, _ := e.urlDelivery(t, "webhook", "https://hooks.example.com/in/token", "outage")
	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{}) // no secret box configured
	d.ProcessOne(t.Context())
	if r := e.row(t, id); r.Status != "failed" || r.Attempts != 1 {
		t.Fatalf("%+v", r)
	}
}

func newService(t *testing.T, e *env, rcv *receiver) *notifications.Service {
	t.Helper()
	return notifications.NewService(e.pool, &scriptedSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "https://app.example.com", "alerts@example.com",
		notifications.WithSecretBox(testutil.SecretBox()), notifications.WithHTTPClient(rcv.client()))
}

func TestCreateURLSealsTheURLSendsATestAndShowsTheSecretOnce(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 200)
	svc := newService(t, e, rcv)

	st, secret, err := svc.CreateURL(t.Context(), e.org, "webhook", "https://hooks.example.com/in/token123")
	if err != nil || !st.Verified || !st.Enabled || st.Channel != "webhook" || st.Destination != "hooks.example.com/…" || !strings.HasPrefix(secret, "whsec_") {
		t.Fatalf("%+v %q %v", st, secret, err)
	}
	got := rcv.requests()
	if len(got) != 1 || got[0].Header.Get("X-HawkEye-Event") != "test" || got[0].Header.Get("X-HawkEye-Signature") != notifications.Sign(secret, mustInt(t, got[0].Header.Get("X-HawkEye-Timestamp")), got[0].Body) {
		t.Fatalf("test message: %+v", got)
	}

	var dest string
	var sealedSecret *string
	e.pool.QueryRow(t.Context(), `SELECT destination, signing_secret FROM notification_settings WHERE id = $1`, st.ID).Scan(&dest, &sealedSecret)
	if !strings.HasPrefix(dest, "enc:v1:") || strings.Contains(dest, "token123") || sealedSecret == nil || strings.Contains(*sealedSecret, secret) {
		t.Fatalf("URL and secret must be stored sealed: %q %v", dest, sealedSecret)
	}
	list, _ := svc.List(t.Context(), e.org)
	if b, _ := json.Marshal(list); strings.Contains(string(b), "token123") || strings.Contains(string(b), secret) || strings.Contains(string(b), "enc:v1") {
		t.Fatalf("listing must show only the masked label: %s", b)
	}

	if _, _, err := svc.CreateURL(t.Context(), e.org, "webhook", "https://hooks.example.com/in/token123"); !errors.Is(err, notifications.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := svc.Test(t.Context(), e.org, st.ID); err != nil || len(rcv.requests()) != 3 { // create test, duplicate's test, this one
		t.Fatalf("%v %d", err, len(rcv.requests()))
	}
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	n, err := json.Number(s).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateURLIsRefusedWhenTheTestMessageFails(t *testing.T) {
	e := newEnv(t, 5)
	svc := newService(t, e, newReceiver(t, 404))
	if _, _, err := svc.CreateURL(t.Context(), e.org, "slack", "https://hooks.slack.com/services/T0/B0/XXXX"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want a validation error naming the status, got %v", err)
	}
	var n int
	e.pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, e.org).Scan(&n)
	if n != 0 {
		t.Fatalf("a destination that cannot receive must not be saved (%d rows)", n)
	}
}

func TestCreateURLRespectsTheDestinationLimitAndNeedsAKey(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 200)
	svc := newService(t, e, rcv)
	for i := range notifications.MaxSettingsPerOrganization {
		if _, _, err := svc.CreateURL(t.Context(), e.org, "webhook", "https://hooks.example.com/in/"+string(rune('a'+i))); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, _, err := svc.CreateURL(t.Context(), e.org, "webhook", "https://hooks.example.com/in/z"); !errors.Is(err, notifications.ErrTooMany) {
		t.Fatalf("limit: %v", err)
	}

	e2 := newEnv(t, 5)
	keyless := notifications.NewService(e2.pool, &scriptedSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "https://app", "a@b", notifications.WithHTTPClient(rcv.client()))
	if _, _, err := keyless.CreateURL(t.Context(), e2.org, "webhook", "https://hooks.example.com/in/x"); err == nil {
		t.Fatal("without an encryption key a URL must not be stored in clear")
	}
}

func TestTestIsNotAvailableForEmail(t *testing.T) {
	e := newEnv(t, 5)
	svc := newService(t, e, newReceiver(t, 200))
	setting, _ := e.delivery(t, "a@example.com", "outage")
	if err := svc.Test(t.Context(), e.org, setting); !errors.Is(err, notifications.ErrNotTestable) {
		t.Fatalf("%v", err)
	}
	if err := svc.Test(t.Context(), e.org, uuid.New()); !errors.Is(err, notifications.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}
