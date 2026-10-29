package errtrack_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hawkeye/internal/errtrack"
)

func TestScrubRemovesPersonalDataAndCredentials(t *testing.T) {
	cases := map[string]string{
		`ERROR: duplicate key value violates unique constraint "users_email_key" Key (email)=(Someone.Else@Example.com) already exists.`: "Someone.Else",
		`stripe: request failed with key sk_test_51UPoU2AntR4Uchv9L2esg0KdVtThmmMWZPGLh0DiBx5vY8ou7pb`:                                   "51UPoU2",
		`verify failed for whsec_abcdef1234567890`:                 "abcdef1234",
		`dial postgres://u:p@db failed: password=hunter2 rejected`: "hunter2",
		`Authorization: Bearer abc.def.ghi`:                        "abc.def.ghi",
		`cookie=hawkeye_session=Zm9vYmFy`:                          "Zm9vYmFy",
	}
	for in, secret := range cases {
		out := errtrack.Scrub(in)
		if strings.Contains(out, secret) {
			t.Errorf("secret %q survived scrubbing: %s", secret, out)
		}
	}
	if got := errtrack.Scrub("connection refused to db:5432"); got != "connection refused to db:5432" {
		t.Errorf("harmless text must be untouched: %q", got)
	}
}

func TestNoDSNMeansDisabled(t *testing.T) {
	r, shutdown, err := errtrack.New(errtrack.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown()
	if _, ok := r.(errtrack.Noop); !ok {
		t.Fatalf("expected Noop, got %T", r)
	}
	r.Capture(t.Context(), errors.New("ignored"), nil) // must not panic
}

func TestInvalidDSNIsAnError(t *testing.T) {
	if _, _, err := errtrack.New(errtrack.Config{DSN: "not a dsn"}); err == nil {
		t.Fatal("a malformed DSN must fail at startup, not silently disable error tracking")
	}
}

// A local stand-in for Sentry's ingest endpoint verifies what actually goes over the wire.
func TestSentryReporterSendsScrubbedTaggedEvents(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.URL.Path+"\n"+string(b))
		mu.Unlock()
		w.WriteHeader(200)
		io.WriteString(w, `{"id":"1"}`)
	}))
	defer srv.Close()
	dsn := "http://publickey@" + strings.TrimPrefix(srv.URL, "http://") + "/42"

	r, shutdown, err := errtrack.New(errtrack.Config{DSN: dsn, Environment: "production", Release: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	r.Capture(t.Context(), errors.New(`insert failed: Key (email)=(victim@example.com) already exists; token=abc123secret`),
		map[string]string{"request_id": "req-1", "route": "/api/v1/monitors"})
	shutdown() // flushes

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) == 0 {
		t.Fatal("no event reached the (fake) Sentry server")
	}
	all := strings.Join(bodies, "\n")
	if !strings.Contains(all, "/api/42/envelope") {
		t.Fatalf("unexpected ingest path: %.200s", all)
	}
	for _, leaked := range []string{"victim@example.com", "abc123secret"} {
		if strings.Contains(all, leaked) {
			t.Fatalf("%q was sent to the error tracker", leaked)
		}
	}
	for _, want := range []string{"[email]", "req-1", "/api/v1/monitors", "production", "v1.2.3"} {
		if !strings.Contains(all, want) {
			t.Errorf("event missing %q", want)
		}
	}
}

func TestDescribeNamesTheProjectButNeverTheKey(t *testing.T) {
	host, project, ok := errtrack.Describe("https://0123456789abcdef@o4500.ingest.us.sentry.io/4509999")
	if !ok || host != "o4500.ingest.us.sentry.io" || project != "4509999" {
		t.Fatalf("%q %q %v", host, project, ok)
	}
	for _, bad := range []string{"", "not a dsn", "https://"} {
		if _, _, ok := errtrack.Describe(bad); ok {
			t.Errorf("%q must not be accepted", bad)
		}
	}
}

func TestLogStatusSaysWhetherAndWhereButNotTheKey(t *testing.T) {
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	errtrack.LogStatus(log, "", "production", "abc")
	errtrack.LogStatus(log, "https://0123456789abcdef@o4500.ingest.us.sentry.io/4509999", "production", "abc123")
	errtrack.LogStatus(log, "garbage", "production", "abc")
	out := buf.String()
	for _, want := range []string{"error tracking disabled", "error tracking enabled", "sentry_project=4509999", "sentry_host=o4500.ingest.us.sentry.io", "environment=production", "release=abc123", "does not look like a DSN"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "0123456789abcdef") {
		t.Errorf("the DSN key was logged:\n%s", out)
	}
}

func TestSendTestReportsDeliveryAndRefusal(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		code := status
		mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()
	dsn := "http://secretkey123@" + strings.TrimPrefix(srv.URL, "http://") + "/1"

	rep, err := errtrack.SendTest(errtrack.Config{DSN: dsn, Environment: "production", Release: "r1"}, "test event for someone@example.com", 5*time.Second)
	if err != nil || !rep.Delivered || rep.EventID == "" || len(rep.Problems) != 0 {
		t.Fatalf("%+v, %v", rep, err)
	}
	mu.Lock()
	sent := strings.Join(bodies, "\n")
	bodies = nil
	status = http.StatusForbidden
	mu.Unlock()
	if !strings.Contains(sent, "test event for [email]") || strings.Contains(sent, "someone@example.com") || !strings.Contains(sent, "sentry-test") || !strings.Contains(sent, `"production"`) {
		t.Fatalf("what went over the wire:\n%s", sent)
	}

	// A refusal (wrong key, deleted project) must not be reported as delivered, and the key must not appear in what is shown.
	rep, err = errtrack.SendTest(errtrack.Config{DSN: dsn}, "test", 5*time.Second)
	if err != nil || rep.Delivered || len(rep.Problems) == 0 {
		t.Fatalf("a refused event reported as delivered: %+v, %v", rep, err)
	}
	if strings.Contains(strings.Join(rep.Problems, "\n"), "secretkey123") {
		t.Fatalf("the DSN key leaked into the report: %v", rep.Problems)
	}

	// Nothing listening at all.
	dead := "http://secretkey123@127.0.0.1:1/1"
	rep, err = errtrack.SendTest(errtrack.Config{DSN: dead}, "test", 3*time.Second)
	if err != nil || rep.Delivered || len(rep.Problems) == 0 {
		t.Fatalf("an unreachable Sentry reported as delivered: %+v, %v", rep, err)
	}

	if _, err := errtrack.SendTest(errtrack.Config{}, "test", time.Second); err == nil {
		t.Fatal("no DSN must be an error")
	}
}

func TestStartMessageIsTheAgreedTextInUTC(t *testing.T) {
	// A time in another zone is shown in UTC, so the message reads the same wherever the server is.
	zone := time.FixedZone("EDT", -4*60*60)
	at := time.Date(2026, 10, 29, 10, 3, 5, 0, zone)
	if got, want := errtrack.StartMessage("a1b2c3d4e5f6", at), "Hawkeye version a1b2c3d4e5f6 is being deployed at 2026-10-29T14:03:05Z"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// fakeSentry stands in for Sentry's ingest endpoint. The body is an envelope: one JSON object per line. An error event has a "message" or an "exception";
// logs travel in an item whose payload is {"items": [...]}. They are kept apart because they end up in different places in Sentry: events are issues.
func fakeSentry(t *testing.T) (dsn string, events func() []map[string]any, logs func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var evs, lgs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		for _, line := range strings.Split(string(b), "\n") {
			var obj map[string]any
			if json.Unmarshal([]byte(line), &obj) != nil {
				continue
			}
			if items, ok := obj["items"].([]any); ok {
				for _, it := range items {
					if m, ok := it.(map[string]any); ok {
						lgs = append(lgs, m)
					}
				}
			} else if _, ok := obj["message"]; ok {
				evs = append(evs, obj)
			} else if _, ok := obj["exception"]; ok {
				evs = append(evs, obj)
			}
		}
		mu.Unlock()
		w.WriteHeader(200)
		io.WriteString(w, `{"id":"1"}`)
	}))
	t.Cleanup(srv.Close)
	get := func(src *[]map[string]any) func() []map[string]any {
		return func() []map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return append([]map[string]any(nil), *src...)
		}
	}
	return "http://publickey@" + strings.TrimPrefix(srv.URL, "http://") + "/42", get(&evs), get(&lgs)
}

// attr reads a log attribute, which is sent as {"value": ..., "type": ...}.
func attr(l map[string]any, key string) any {
	a, _ := l["attributes"].(map[string]any)
	m, _ := a[key].(map[string]any)
	return m["value"]
}

func TestAnnounceStartSendsAnInfoLogNotAnIssue(t *testing.T) {
	dsn, events, logs := fakeSentry(t)
	r, shutdown, err := errtrack.New(errtrack.Config{DSN: dsn, Environment: "production", Release: "a1b2c3d4e5f6"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 29, 14, 3, 5, 0, time.UTC)
	errtrack.AnnounceStart(r, "api", "a1b2c3d4e5f6", at)
	errtrack.AnnounceStart(r, "worker", "a1b2c3d4e5f6", at.Add(2*time.Second))
	shutdown() // flushes the batch of logs

	ls := logs()
	if len(ls) != 2 {
		t.Fatalf("want one log per process, got %d: %v", len(ls), ls)
	}
	// The whole point of using logs: nothing that creates an issue, raises an issue alert or uses the error quota is sent.
	if evs := events(); len(evs) != 0 {
		t.Fatalf("a start notice must not be an error event, got %v", evs)
	}
	first := ls[0]
	if first["body"] != "Hawkeye version a1b2c3d4e5f6 is being deployed at 2026-10-29T14:03:05Z" {
		t.Errorf("body: %v", first["body"])
	}
	if first["level"] != "info" {
		t.Errorf("level: %v", first["level"])
	}
	if attr(first, "sentry.release") != "a1b2c3d4e5f6" || attr(first, "sentry.environment") != "production" {
		t.Errorf("release/environment: %v / %v", attr(first, "sentry.release"), attr(first, "sentry.environment"))
	}
	for _, l := range ls {
		if attr(l, "event") != "start" || attr(l, "version") != "a1b2c3d4e5f6" {
			t.Errorf("attributes to search by: %v", l["attributes"])
		}
		// Events carry no server name; neither do logs (the SDK would add the machine's host name).
		if attr(l, "sentry.server.address") != nil {
			t.Errorf("host name was sent: %v", attr(l, "sentry.server.address"))
		}
	}
	if attr(ls[0], "component") == attr(ls[1], "component") {
		t.Errorf("the two processes must be told apart: %v %v", attr(ls[0], "component"), attr(ls[1], "component"))
	}
}

func TestNoticesAreScrubbedLikeErrors(t *testing.T) {
	dsn, _, logs := fakeSentry(t)
	r, shutdown, err := errtrack.New(errtrack.Config{DSN: dsn, Environment: "production", Release: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	n, ok := r.(errtrack.Notifier)
	if !ok {
		t.Fatalf("%T cannot send notices", r)
	}
	n.Notify(t.Context(), "sent to victim@example.com with token=abc123secret", map[string]string{"who": "victim@example.com"})
	shutdown()
	ls := logs()
	if len(ls) != 1 {
		t.Fatalf("got %v", ls)
	}
	all, _ := json.Marshal(ls[0])
	for _, leaked := range []string{"victim@example.com", "abc123secret"} {
		if strings.Contains(string(all), leaked) {
			t.Errorf("%q was sent: %s", leaked, all)
		}
	}
	if !strings.Contains(string(all), "[email]") {
		t.Errorf("expected the address to be replaced: %s", all)
	}
}

// fakeReporter reports errors only, like the stand-ins used by other tests.
type fakeReporter struct{ n int }

func (f *fakeReporter) Capture(context.Context, error, map[string]string) { f.n++ }

func TestAnnounceStartDoesNothingWithoutAReporterThatCanSendNotices(t *testing.T) {
	at := time.Now()
	errtrack.AnnounceStart(errtrack.Noop{}, "api", "v1", at) // error tracking off
	f := &fakeReporter{}
	errtrack.AnnounceStart(f, "api", "v1", at) // a reporter that only handles errors
	if f.n != 0 {
		t.Fatal("a start notice must never be reported as an error")
	}
}
