// Package runcheck runs one saved monitor's check: it loads the monitor's request (opening its stored credentials), and performs the website,
// DNS or multi-step check. The worker uses it for scheduled checks and internal/deploys for the checks a pipeline asks for after a deploy, so both
// check a monitor in exactly the same way.
package runcheck

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/monitors"
	"hawkeye/internal/secrets"
)

var (
	// ErrInactive means the monitor was deleted or paused: there is nothing to check.
	ErrInactive = errors.New("monitor deleted or paused")
	// ErrSetup means the monitor's stored credentials or workflow can't be opened: an operator problem, never a failing website.
	ErrSetup = errors.New("monitor can't be prepared for checking")
	// ErrHeartbeat means the monitor is a heartbeat: HawkEye waits for its pings instead of checking it.
	ErrHeartbeat = errors.New("heartbeat monitors are not checked")
)

// Run checks the monitor now. As with checks.Checker, a failing website is a Result with Success false; the error is for a monitor that can't
// be checked (ErrInactive, ErrHeartbeat, ErrSetup), a database error, or ctx ending first. timeout, when above zero, caps the monitor's own
// timeout (a deploy check has less time than a scheduled one).
func Run(ctx context.Context, pool *pgxpool.Pool, checker *checks.Checker, box *secrets.Box, monitorID uuid.UUID, timeout time.Duration) (checks.Result, error) {
	var t checks.Target
	var enabled bool
	var timeoutSecs int
	var dnsType, dnsExpected, kind string // dnsType is set for DNS monitors, whose url is the host name to look up
	err := pool.QueryRow(ctx, `SELECT url, method, expected_status_code, timeout_seconds, enabled,
		request_headers, COALESCE(request_body, ''), auth_type, COALESCE(auth_username, ''), COALESCE(auth_header_name, ''),
		COALESCE(auth_secret, ''), assertions, COALESCE(dns_record_type, ''), COALESCE(dns_expected, ''), kind, trace_context
		FROM monitors WHERE id = $1 AND deleted_at IS NULL`, monitorID).
		Scan(&t.URL, &t.Method, &t.ExpectedStatus, &timeoutSecs, &enabled, &t.Headers, &t.Body, &t.Auth.Type,
			&t.Auth.Username, &t.Auth.HeaderName, &t.Auth.Secret, &t.Assertions, &dnsType, &dnsExpected, &kind, &t.TraceContext)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && !enabled:
		return checks.Result{}, ErrInactive
	case err != nil:
		return checks.Result{}, err
	case kind == monitors.TypeHeartbeat:
		return checks.Result{}, ErrHeartbeat
	}
	if t.Auth.Secret != "" {
		plain, err := box.Open(t.Auth.Secret, monitorID.String())
		if err != nil {
			return checks.Result{}, fmt.Errorf("%w: open credentials: %w", ErrSetup, err)
		}
		t.Auth.Secret = plain
	}
	t.Timeout = time.Duration(timeoutSecs) * time.Second
	if timeout > 0 {
		t.Timeout = min(t.Timeout, timeout)
	}
	switch {
	case kind == monitors.TypeWorkflow:
		wf, err := monitors.Runnable(ctx, pool, box, monitorID)
		if err != nil {
			return checks.Result{}, fmt.Errorf("%w: load workflow: %w", ErrSetup, err)
		}
		return checker.RunWorkflow(ctx, wf)
	case dnsType != "":
		return checker.CheckDNS(ctx, checks.DNSTarget{Host: t.URL, RecordType: dnsType, Expected: dnsExpected, Timeout: t.Timeout})
	default:
		return checker.Check(ctx, t)
	}
}
