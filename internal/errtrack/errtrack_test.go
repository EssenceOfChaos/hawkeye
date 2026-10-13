package errtrack_test

import (
	"errors"
	"io"
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
