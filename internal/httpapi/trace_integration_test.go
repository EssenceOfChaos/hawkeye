package httpapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/heartbeats"
	"hawkeye/internal/incidents"
)

func TestTraceContextIsOptInAndItsTraceIDsAreListed(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "trace@example.com")

	plain := c.call("POST", "/api/v1/monitors", monitorBody("Plain")).Body["monitor"].(map[string]any)
	if plain["trace_context"] != false {
		t.Fatalf("off by default: %v", plain["trace_context"])
	}
	r := c.call("POST", "/api/v1/monitors", map[string]any{"name": "API", "url": "https://api.example.com/health", "trace_context": true})
	if r.Status != 201 || r.Body["monitor"].(map[string]any)["trace_context"] != true {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	id := r.Body["monitor"].(map[string]any)["id"].(string)

	// Only website and API monitors make a request to carry it, and a traceparent header of the customer's own would conflict.
	for name, body := range map[string]map[string]any{
		"heartbeat":  {"type": "heartbeat", "name": "Job", "trace_context": true},
		"own header": {"name": "Own", "url": "https://x.example.com", "trace_context": true, "headers": []map[string]string{{"name": "Traceparent", "value": "00-x"}}},
	} {
		if r := c.call("POST", "/api/v1/monitors", body); r.Status != 400 || r.Body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)["trace_context"] == nil {
			t.Errorf("%s: %d %v", name, r.Status, r.Body)
		}
	}
	if r := c.call("PATCH", "/api/v1/monitors/"+plain["id"].(string), map[string]any{"trace_context": true}); r.Status != 200 || r.Body["monitor"].(map[string]any)["trace_context"] != true {
		t.Fatalf("turned on by editing: %d %v", r.Status, r.Body)
	}

	// A recorded check keeps its trace ID, and the results list returns it.
	traceID := strings.Repeat("ab", 16)
	now := time.Now()
	store := checks.NewStore(e.pool, heartbeats.Thresholds, incidents.Engine{})
	if _, err := store.Record(t.Context(), uuid.MustParse(id), checks.Result{CheckedAt: now, FinishedAt: now, Success: false, ErrorCategory: checks.CatHTTPStatus, TraceID: traceID}); err != nil {
		t.Fatal(err)
	}
	res := c.call("GET", "/api/v1/monitors/"+id+"/results?range=24h", nil)
	list, _ := res.Body["results"].([]any)
	if res.Status != 200 || len(list) != 1 || list[0].(map[string]any)["trace_id"] != traceID {
		t.Fatalf("%d %v", res.Status, res.Body)
	}
}
