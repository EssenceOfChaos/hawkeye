package monitors

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/checks"
)

// RequestDraft is a website or API monitor's request sent to be tried once without saving it (POST /monitors/test-request), so its response
// can be seen and rules written from it. MonitorID, when editing a saved monitor, lets the stored credential be used when no secret is sent, as
// saving the form would; nothing else is read from the saved monitor.
type RequestDraft struct {
	MonitorID          *uuid.UUID         `json:"monitor_id"`
	URL                string             `json:"url"`
	Method             *string            `json:"method"`
	ExpectedStatusCode *int               `json:"expected_status_code"`
	TimeoutSeconds     *int               `json:"timeout_seconds"`
	Headers            []checks.Header    `json:"headers"`
	Body               *string            `json:"body"`
	Auth               *AuthInput         `json:"auth"`
	Assertions         []checks.Assertion `json:"assertions"`
}

// TryRequest validates an unsaved request with the same rules as saving a website or API monitor and returns what the checker runs. Field errors
// use the monitor form's names (url, headers[0].name, assertions[1].value, ...).
func (s *Service) TryRequest(ctx context.Context, orgID uuid.UUID, in RequestDraft) (checks.Target, error) {
	// Name and interval aren't part of a test; they are set to values that pass so only the request's own fields are judged.
	f := fields{kind: TypeHTTP, name: "Test request", url: in.URL, method: "GET", expected: in.ExpectedStatusCode, interval: MinWriteIntervalSeconds,
		timeout: DefaultTimeoutSeconds, headers: slices.Clone(in.Headers), body: in.Body, assertions: slices.Clone(in.Assertions),
		slowChecks: DefaultSlowChecks, recheck: checks.RecheckAuto}
	if in.Method != nil {
		f.method = *in.Method
	}
	if in.TimeoutSeconds != nil {
		f.timeout = *in.TimeoutSeconds
	}

	var curType, sealed string
	if in.MonitorID != nil {
		err := s.pool.QueryRow(ctx, `SELECT auth_type, COALESCE(auth_secret, '') FROM monitors
			WHERE id = $1 AND organization_id = $2 AND kind = $3 AND deleted_at IS NULL`, *in.MonitorID, orgID, TypeHTTP).Scan(&curType, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return checks.Target{}, ErrNotFound
		}
		if err != nil {
			return checks.Target{}, err
		}
	}
	f.setAuth(in.Auth, curType, sealed != "")
	if in.Auth == nil {
		f.auth.Type, f.keepSecret = checks.AuthNone, false
	}
	if err := f.validate(); err != nil {
		return checks.Target{}, err
	}

	auth := f.auth
	if f.keepSecret {
		plain, err := s.box.Open(sealed, in.MonitorID.String())
		if err != nil {
			return checks.Target{}, fmt.Errorf("open credentials: %w", err)
		}
		auth.Secret = plain
	}
	body := ""
	if f.body != nil {
		body = *f.body
	}
	return checks.Target{URL: f.url, Method: f.method, ExpectedStatus: f.expected, Timeout: time.Duration(f.timeout) * time.Second,
		Headers: f.headers, Body: body, Auth: auth, Assertions: f.assertions}, nil
}
