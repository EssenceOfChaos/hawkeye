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

// events collects the events sent to a stand-in for Sentry's ingest endpoint. The body is an envelope: one JSON object per line, of which the event is
// the one that has a "message" or an "exception".
func fakeSentry(t *testing.T) (dsn string, events func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var out []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		for _, line := range strings.Split(string(b), "\n") {
			var ev map[string]any
			if json.Unmarshal([]byte(line), &ev) == nil {
				if _, ok := ev["message"]; ok {
					out = append(out, ev)
				}
			}
		}
		mu.Unlock()
		w.WriteHeader(200)
		io.WriteString(w, `{"id":"1"}`)
	}))
	t.Cleanup(srv.Close)
	return "http://publickey@" + strings.TrimPrefix(srv.URL, "http://") + "/42", func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]any(nil), out...)
	}
}

func TestAnnounceStartSendsAnInfoNoticeGroupedByVersion(t *testing.T) {
	dsn, events := fakeSentry(t)
	r, shutdown, err := errtrack.New(errtrack.Config{DSN: dsn, Environment: "production", Release: "a1b2c3d4e5f6"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 29, 14, 3, 5, 0, time.UTC)
	errtrack.AnnounceStart(r, "api", "a1b2c3d4e5f6", at)
	errtrack.AnnounceStart(r, "worker", "a1b2c3d4e5f6", at.Add(2*time.Second))
	shutdown() // flushes

	evs := events()
	if len(evs) != 2 {
		t.Fatalf("want one notice per process, got %d: %v", len(evs), evs)
	}
	first := evs[0]
	if first["message"] != "Hawkeye version a1b2c3d4e5f6 is being deployed at 2026-10-29T14:03:05Z" {
		t.Errorf("message: %v", first["message"])
	}
	if first["level"] != "info" {
		t.Errorf("a notice is not an error, level: %v", first["level"])
	}
	if first["release"] != "a1b2c3d4e5f6" || first["environment"] != "production" {
		t.Errorf("release/environment: %v / %v", first["release"], first["environment"])
	}
	// Grouped by version: both processes of a deploy, and every restart of that version, share one issue.
	for _, ev := range evs {
		fp, _ := ev["fingerprint"].([]any)
		if len(fp) != 2 || fp[0] != "start" || fp[1] != "a1b2c3d4e5f6" {
			t.Errorf("fingerprint: %v", ev["fingerprint"])
		}
		// A notice has nothing to trace.
		if ev["exception"] != nil || ev["threads"] != nil {
			t.Errorf("no stack trace expected: %v", ev)
		}
	}
	tags := func(ev map[string]any) map[string]any { m, _ := ev["tags"].(map[string]any); return m }
	if tags(evs[0])["component"] == tags(evs[1])["component"] {
		t.Errorf("the two processes must be told apart by tag: %v %v", tags(evs[0]), tags(evs[1]))
	}
	if tg := tags(evs[0]); tg["event"] != "start" || tg["version"] != "a1b2c3d4e5f6" {
		t.Errorf("tags: %v", tg)
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
