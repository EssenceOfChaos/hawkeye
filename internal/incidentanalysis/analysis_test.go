package incidentanalysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"hawkeye/internal/checks"
	"hawkeye/internal/incidents"
	"hawkeye/internal/monitors"
)

// fakeAPI answers POST /v1/messages with the given stop reason and text, and keeps the request body.
func fakeAPI(t *testing.T, status int, stop, text string) (*Service, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		if status != 200 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": Model, "stop_reason": stop,
			"content": []map[string]any{{"type": "text", "text": text}},
			"usage":   map[string]any{"input_tokens": 1500, "output_tokens": 300},
		})
	}))
	t.Cleanup(srv.Close)
	return New("test-key", option.WithBaseURL(srv.URL), option.WithMaxRetries(0)), &got
}

func snapshot() incidents.Snapshot {
	step := 1
	name := "Fetch the account"
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	return incidents.Snapshot{
		CheckedAt: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC), Region: "primary", StepIndex: &step, StepName: &name,
		Facts: checks.ResponseFacts{StatusCode: 502, FinalURL: "https://shop.example.com/health", ContentType: "text/html", SizeBytes: 1200,
			ServerAddr: "198.51.100.9", CertExpiresAt: &exp, CertIssuer: "R11", Timings: &checks.Timings{DNSMs: 12, ConnectMs: 31, TLSMs: 48, WaitMs: 2410},
			Headers: []checks.Header{{Name: "Server", Value: "cloudflare"}, {Name: "Location", Value: "https://shop.example.com/down"}}},
		Changes: []checks.Change{
			{Field: "status", Before: "200", After: "502"},
			{Field: "server_addr", Before: "203.0.113.5", After: "198.51.100.9"},
			{Field: "final_url", Before: "https://shop.example.com/health", After: "https://other.example.net/"},
			{Field: "header", Name: "Location", Before: "https://a.example.com/", After: "https://b.example.com/"},
			{Field: "timing", Name: "wait", Before: "180 ms", After: "2.4 s"},
		},
		Locations: []checks.LocationState{{Region: "ams", Name: "Amsterdam", Status: "down", Error: "Received HTTP 502"}},
	}
}

func TestFactsLeaveOutAddressesAndExpectedValues(t *testing.T) {
	expected := 200
	f := FactsFrom(monitors.TypeWorkflow, "http_status", "Received HTTP 502", &expected,
		[]checks.Assertion{{Type: "contains", Value: "secret page text"}, {Type: "json", Path: "status", Operator: "eq", Value: "ok"}, {Type: "contains", Value: "x"}},
		snapshot())
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, never := range []string{"example.com", "example.net", "198.51.100.9", "203.0.113.5", "secret page text", `"ok"`} {
		if strings.Contains(s, never) {
			t.Errorf("facts sent to the model contain %q: %s", never, s)
		}
	}
	if f.MonitorKind != "multi-step API check" || f.FailingStep == nil || f.FailingStep.Number != 2 || f.FailingStep.Name != "Fetch the account" {
		t.Errorf("kind or step: %+v %+v", f.MonitorKind, f.FailingStep)
	}
	if strings.Join(f.ContentChecks, ",") != "contains,json" {
		t.Errorf("content checks = %v, want each kind once", f.ContentChecks)
	}
	if len(f.Changes) != 5 || f.Changes[1].After != "an address on another network" || f.Changes[4].After != "2.4 s" {
		t.Errorf("changes = %+v", f.Changes)
	}
	if !f.Response.Received || f.Response.StatusCode != 502 || f.Timings.WaitMs != 2410 || f.Locations[0].Name != "Amsterdam" {
		t.Errorf("response facts lost: %+v", f)
	}
}

func TestAnalyzeSendsTheFactsAsDataAndAsksForTheSchema(t *testing.T) {
	answer := `{"category":"cdn_or_proxy","likely_cause":"Cloudflare can't reach the origin server.","evidence":["502 from Cloudflare","Server wait 2.4 s, usually 180 ms"],"next_step":"Check the origin server's health.","confidence":"medium"}`
	svc, got := fakeAPI(t, 200, "end_turn", answer)
	r, u, err := svc.Analyze(context.Background(), FactsFrom(monitors.TypeHTTP, "", "", nil, nil, snapshot()))
	if err != nil {
		t.Fatal(err)
	}
	if r.Category != "cdn_or_proxy" || r.Confidence != "medium" || len(r.Evidence) != 2 || u.InputTokens != 1500 || u.OutputTokens != 300 {
		t.Errorf("result %+v usage %+v", r, u)
	}
	body := *got
	if body["model"] != Model {
		t.Errorf("model = %v", body["model"])
	}
	oc, _ := body["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if oc["effort"] != "low" || format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config = %v", oc)
	}
	msgs, _ := body["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	content, _ := first["content"].([]any)
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	if !strings.HasPrefix(text, "<failure>\n{") || !strings.Contains(text, "</failure>") || strings.Contains(text, "example.com") {
		t.Errorf("message = %s", text)
	}
}

func TestAnalyzeErrors(t *testing.T) {
	svc, _ := fakeAPI(t, 200, "refusal", "")
	if _, _, err := svc.Analyze(context.Background(), Facts{}); !errors.Is(err, ErrRefused) {
		t.Errorf("refusal: err = %v", err)
	}
	svc, _ = fakeAPI(t, 500, "", "")
	if _, _, err := svc.Analyze(context.Background(), Facts{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("server error: err = %v", err)
	}
	svc, _ = fakeAPI(t, 200, "max_tokens", `{"category":"dns","likely`)
	if _, _, err := svc.Analyze(context.Background(), Facts{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("cut-off answer: err = %v", err)
	}
	svc, _ = fakeAPI(t, 200, "end_turn", `{"category":"dns","likely_cause":"  ","evidence":[],"next_step":"x","confidence":"low"}`)
	if _, _, err := svc.Analyze(context.Background(), Facts{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("empty cause: err = %v", err)
	}
}

func TestCleanKeepsLengthsAndKnownValues(t *testing.T) {
	long := strings.Repeat("word ", 100)
	r, ok := clean(Result{Category: "aliens", LikelyCause: long, NextStep: long, Confidence: "certain",
		Evidence: []string{"a", "", long, "b", "c"}})
	if !ok {
		t.Fatal("clean refused a result with a cause")
	}
	if r.Category != "unknown" || r.Confidence != "low" {
		t.Errorf("unknown values kept: %+v", r)
	}
	if n := len([]rune(r.LikelyCause)); n > MaxCauseLength || !strings.HasSuffix(r.LikelyCause, "…") {
		t.Errorf("cause %d characters: %q", n, r.LikelyCause)
	}
	if len(r.Evidence) != MaxEvidence || r.Evidence[0] != "a" || len([]rune(r.Evidence[1])) > MaxEvidenceLength {
		t.Errorf("evidence = %q", r.Evidence)
	}
}
