package reports_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"hawkeye/internal/reports"
)

// A fixed "now" and fixed seeded times, so nothing depends on the clock of the machine running the test.
var (
	now       = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	sept      = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	retention = 90
)

// hourly adds one check an hour from..to (exclusive); the ones in `failing` fail.
func (e *env) hourly(t *testing.T, mon uuid.UUID, from, to time.Time, failing map[time.Time]bool) {
	t.Helper()
	for ts := from; ts.Before(to); ts = ts.Add(time.Hour) {
		ok := !failing[ts]
		var code, ms any
		var cat, sum any
		if ok {
			code, ms = 200, 100
		} else {
			code, cat, sum = 503, "http_status", "Received HTTP 503"
		}
		if _, err := e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary)
			VALUES ($1, $2, $2, $3, $4, $5, $6, $7)`, mon, ts, ok, code, ms, cat, sum); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) incident(t *testing.T, mon uuid.UUID, start time.Time, end *time.Time, cause, summary string) {
	t.Helper()
	status := "open"
	if end != nil {
		status = "resolved"
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO incidents (monitor_id, started_at, resolved_at, status, cause_category, summary) VALUES ($1, $2, $3, $4, $5, $6)`,
		mon, start, end, status, cause, summary); err != nil {
		t.Fatal(err)
	}
}

func at(day, hour, min int) time.Time { return time.Date(2026, 9, day, hour, min, 0, 0, time.UTC) }

func TestMonthlyReportCountsOnlyThatMonthsChecksAndOutages(t *testing.T) {
	e := newEnv(t)
	mon := e.monitor(t, e.org, "Marketing site")
	e.pool.Exec(t.Context(), `UPDATE monitors SET url = 'https://user:hunter2@www.example.com/status?token=secret#frag' WHERE id = $1`, mon)

	// Checks every hour through September, plus a few either side that must not be counted. Two failures on the 7th.
	failing := map[time.Time]bool{at(7, 13, 0): true, at(7, 14, 0): true}
	e.hourly(t, mon, sept.Add(-5*time.Hour), sept.AddDate(0, 1, 0).Add(5*time.Hour), failing)
	// An outage inside the month, one that began in August and ended on the 1st, and one that began on the 30th and is still open.
	end := at(7, 14, 42)
	e.incident(t, mon, at(7, 13, 0), &end, "http_status", "Received HTTP 503")
	augEnd := at(1, 2, 0)
	e.incident(t, mon, time.Date(2026, 8, 31, 22, 0, 0, 0, time.UTC), &augEnd, "timeout", "No answer")
	e.incident(t, mon, at(30, 20, 0), nil, "connect", "Connection refused")

	m, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, sept, now, retention)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Marketing site" || m.URL != "https://www.example.com/status" {
		t.Fatalf("name/url = %q %q: credentials, query and fragment must not be printed", m.Name, m.URL)
	}
	if m.Partial || !m.From.Equal(sept) || !m.To.Equal(sept.AddDate(0, 1, 0)) {
		t.Fatalf("period = %v..%v partial=%v", m.From, m.To, m.Partial)
	}
	if m.Uptime.TotalChecks != 720 || m.Uptime.FailedChecks != 2 {
		t.Fatalf("checks = %d (failed %d), want 720 (2): checks outside September must not count", m.Uptime.TotalChecks, m.Uptime.FailedChecks)
	}
	if m.IncidentCount != 3 || len(m.Incidents) != 3 {
		t.Fatalf("incidents = %d listed %d", m.IncidentCount, len(m.Incidents))
	}
	// 2 h (from 00:00 to 02:00 on the 1st, the part of the August outage inside September) + 1 h 42 min + 4 h (30th 20:00 to midnight, still open).
	want := int64(2*3600 + (60+42)*60 + 4*3600)
	if m.DowntimeSeconds != want {
		t.Fatalf("downtime = %d s, want %d: only the part of an outage inside the month counts", m.DowntimeSeconds, want)
	}
	// The outage that began in August is listed first, and clamped to the start of September.
	if m.Incidents[0].Seconds != 2*3600 || m.Incidents[2].ResolvedAt != nil {
		t.Fatalf("incident rows: %+v", m.Incidents)
	}
	if len(m.Days) != 30 || m.Days[6].Failed != 2 || m.Days[6].Checks != 24 {
		t.Fatalf("days = %d, 7th = %+v", len(m.Days), m.Days[6])
	}
	if !m.HasData() {
		t.Fatal("HasData")
	}
}

func TestMonthInProgressIsPartialAndEndsNow(t *testing.T) {
	e := newEnv(t)
	mon := e.monitor(t, e.org, "S")
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e.hourly(t, mon, oct, now.Add(3*time.Hour), nil) // some "future" checks that must not be counted
	m, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, oct, now, retention)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Partial || !m.To.Equal(now) || m.Uptime.TotalChecks != 24*24+12 {
		t.Fatalf("partial=%v to=%v checks=%d", m.Partial, m.To, m.Uptime.TotalChecks)
	}
}

func TestMonthlyReportRefusesWhatItCannotDoHonestly(t *testing.T) {
	e := newEnv(t)
	mon := e.monitor(t, e.org, "S")
	other := uuid.New()
	e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('other') RETURNING id`).Scan(&other)

	if _, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), now, retention); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Fatalf("a future month: %v", err)
	}
	if _, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), now, retention); err == nil || !strings.Contains(err.Error(), "older than the history") {
		t.Fatalf("a month whose results were partly deleted by retention: %v", err)
	}
	if _, err := reports.BuildMonthly(t.Context(), e.pool, other, mon, sept, now, retention); !errors.Is(err, reports.ErrMonitorNotFound) {
		t.Fatalf("another organization's monitor: %v", err)
	}
	e.pool.Exec(t.Context(), `UPDATE monitors SET deleted_at = now() WHERE id = $1`, mon)
	if _, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, sept, now, retention); !errors.Is(err, reports.ErrMonitorNotFound) {
		t.Fatalf("a deleted monitor: %v", err)
	}
}

func TestMaintenanceChecksAreNotCountedAndNoChecksMeansNoData(t *testing.T) {
	e := newEnv(t)
	mon := e.monitor(t, e.org, "S")
	m, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, sept, now, retention)
	if err != nil || m.HasData() || m.Uptime.UptimePercent != nil {
		t.Fatalf("empty month: %v %+v", err, m.Uptime)
	}
	e.hourly(t, mon, sept, sept.Add(10*time.Hour), nil)
	e.pool.Exec(t.Context(), `UPDATE check_results SET in_maintenance = true WHERE monitor_id = $1 AND checked_at < $2`, mon, sept.Add(4*time.Hour))
	m, _ = reports.BuildMonthly(t.Context(), e.pool, e.org, mon, sept, now, retention)
	if m.Uptime.TotalChecks != 6 {
		t.Fatalf("checks = %d, want 6: checks during maintenance are outside uptime", m.Uptime.TotalChecks)
	}
}

func TestIncidentListIsCappedButTotalsCountEverything(t *testing.T) {
	e := newEnv(t)
	mon := e.monitor(t, e.org, "S")
	e.hourly(t, mon, sept, sept.Add(48*time.Hour), nil)
	for i := 0; i < reports.MaxListedIncidents+7; i++ {
		s := sept.Add(time.Duration(i) * 20 * time.Hour)
		end := s.Add(10 * time.Minute)
		e.incident(t, mon, s, &end, "http_status", "x")
	}
	m, err := reports.BuildMonthly(t.Context(), e.pool, e.org, mon, sept, now, retention)
	if err != nil {
		t.Fatal(err)
	}
	if m.IncidentCount != reports.MaxListedIncidents+7 || len(m.Incidents) != reports.MaxListedIncidents || m.DowntimeSeconds != int64(reports.MaxListedIncidents+7)*600 {
		t.Fatalf("count=%d listed=%d downtime=%d", m.IncidentCount, len(m.Incidents), m.DowntimeSeconds)
	}
}

func TestParseMonthAndMonthStart(t *testing.T) {
	m, err := reports.ParseMonth("2026-09")
	if err != nil || !m.Equal(sept) {
		t.Fatalf("%v %v", m, err)
	}
	for _, bad := range []string{"", "2026", "2026-13", "september", "2026-9-1", "2026-09-01"} {
		if _, err := reports.ParseMonth(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got := reports.MonthStart(time.Date(2026, 12, 31, 23, 59, 0, 0, time.FixedZone("x", -5*3600))); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("MonthStart converts to UTC first: %v", got)
	}
}
