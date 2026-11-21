package worker_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hawkeye/internal/reports"
)

// A check's timing breakdown is stored with its result and comes back in the check history and the uptime averages.
func TestTimingsAreStoredAndReported(t *testing.T) {
	e := newEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(60 * time.Millisecond) }))
	defer srv.Close()
	id := e.monitor(t, srv.URL)
	e.round(t, e.worker(loopbackCfg()))

	r := reports.Range{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Minute), RetentionDays: 90}
	list, _, err := reports.Results(t.Context(), e.pool, e.org, id, r, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %d", err, len(list))
	}
	tm := list[0].Timings
	if tm == nil || tm.WaitMs < 60 || tm.TLSMs != 0 || tm.Redirects != 0 {
		t.Fatalf("timings %+v", tm)
	}
	u, err := reports.ComputeUptime(t.Context(), e.pool, e.org, &id, r)
	if err != nil {
		t.Fatal(err)
	}
	if u.AvgTimings == nil || u.AvgTimings.Checks != 1 || u.AvgTimings.WaitMs != tm.WaitMs || u.AvgTimings.OtherMs == nil {
		t.Fatalf("averages %+v, check %+v", u.AvgTimings, tm)
	}
}
