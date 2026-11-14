// Package monitors manages monitor definitions. Every query is scoped by organization ID, which
// callers must have authorized; a monitor in another organization is indistinguishable from a missing one.
package monitors

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/checks"
	"hawkeye/internal/plans"
	"hawkeye/internal/publichost"
	"hawkeye/internal/regions"
	"hawkeye/internal/secrets"
	"hawkeye/internal/validation"
)

var ErrNotFound = errors.New("monitor not found")

const (
	DefaultTimeoutSeconds = 10
	// Slow-response alerts: the threshold range (ms) and how many checks in a row confirm a slowdown (and its end).
	MinSlowThresholdMs = 100
	MaxSlowThresholdMs = 60000
	DefaultSlowChecks  = 3
	MinSlowChecks      = 2
	MaxSlowChecks      = 10
	MaxIntervalSeconds = 86400
	MinIntervalSeconds = 60

	// Heartbeat monitors: the expected time between pings (stored as the check interval, so the plan's minimum applies) and the grace time.
	MaxPeriodSeconds     = 30 * 86400
	DefaultPeriodSeconds = 86400
	MinGraceSeconds      = 60
	MaxGraceSeconds      = 7 * 86400
	DefaultGraceSeconds  = 3600

	MaxHeaders     = 20
	MaxAssertions  = 10
	MaxBodyBytes   = 64 << 10
	maxSecretChars = 2048
)

// Monitor types. A website (HTTP) monitor is checked by HawkEye, a DNS monitor has a record looked up by HawkEye, and a heartbeat monitor waits for the
// customer's job to ping it.
const (
	TypeHTTP      = "http"
	TypeHeartbeat = "heartbeat"
	TypeDNS       = "dns"
)

// Methods lists the accepted request methods. Only some of them may carry a body.
var Methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// IsWriteMethod reports whether a method changes state on the target. Such monitors repeat a state-changing request on every check, so they
// need a verified email address (enforced by the API layer) and a slower minimum interval.
func IsWriteMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// MinWriteIntervalSeconds is the fastest a monitor using a write method may run, whatever the plan allows.
const MinWriteIntervalSeconds = 300

func bodyAllowed(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
}

// reservedHeaders are set by the checker or the HTTP stack, or would change how the response is read.
// Authorization is reserved so credentials go through auth, which is never returned by the API.
var reservedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Upgrade": true,
	"Te": true, "Trailer": true, "Keep-Alive": true, "Proxy-Authorization": true, "Proxy-Connection": true,
	"Accept-Encoding": true, "User-Agent": true, "Authorization": true, "Cookie2": true, "Expect": true,
}

type Monitor struct {
	ID                   uuid.UUID `json:"id"`
	OrganizationID       uuid.UUID `json:"organization_id"`
	Type                 string    `json:"type"` // http, heartbeat or dns; fixed when the monitor is created
	Name                 string    `json:"name"`
	URL                  string    `json:"url"`
	Method               string    `json:"method"`
	ExpectedStatusCode   *int      `json:"expected_status_code"`
	CheckIntervalSeconds int       `json:"check_interval_seconds"`
	// EffectiveCheckIntervalSeconds is what the scheduler uses: the stored value raised to the
	// plan minimum, so a downgrade takes effect without rewriting data.
	EffectiveCheckIntervalSeconds int                `json:"effective_check_interval_seconds"`
	TimeoutSeconds                int                `json:"timeout_seconds"`
	Headers                       []checks.Header    `json:"headers"`
	Body                          *string            `json:"body"`
	Auth                          AuthInfo           `json:"auth"`
	Assertions                    []checks.Assertion `json:"assertions"`
	SSL                           *SSLInfo           `json:"ssl"` // null until a check has seen the monitor's TLS certificate
	Enabled                       bool               `json:"enabled"`
	CurrentStatus                 string             `json:"current_status"`
	Regions                       []string           `json:"regions"`        // where it is checked from; empty means the primary region only
	InMaintenance                 bool               `json:"in_maintenance"` // a maintenance window covers it right now: checks run but raise no alerts
	LastCheckedAt                 *time.Time         `json:"last_checked_at"`
	LastSuccessAt                 *time.Time         `json:"last_success_at"`
	ConsecutiveFailures           int                `json:"consecutive_failures"`
	ConsecutiveSuccesses          int                `json:"consecutive_successes"`
	// Slow-response alerts: alert when successful checks stay above SlowThresholdMs (nil: off) for SlowChecks checks in a row.
	SlowThresholdMs *int       `json:"slow_threshold_ms"`
	SlowChecks      int        `json:"slow_checks"`
	SlowSince       *time.Time `json:"slow_since"` // set while a slowdown is open: responses are confirmed slow
	// TraceContext: each check sends a W3C traceparent header with a new trace ID (website and API monitors only; off by default).
	TraceContext bool `json:"trace_context"`
	// BadgeToken addresses the monitor's public uptime badge (/badge/<token>.svg); nil while the badge is off.
	BadgeToken *string `json:"badge_token"`
	Tags       []Tag   `json:"tags"` // sorted by name
	// Alert timing: announce an outage only once it has lasted this many minutes (0: as soon as it is confirmed), and hold this monitor's alerts during
	// quiet hours (nil: none).
	AlertDelayMinutes int         `json:"alert_delay_minutes"`
	QuietHours        *QuietHours `json:"quiet_hours"`
	// Heartbeat is set for heartbeat monitors only. For them check_interval_seconds is the expected time between pings, and URL and the request
	// fields are unused.
	Heartbeat *Heartbeat `json:"heartbeat"`
	// DNS is set for DNS monitors only. For them URL is the host name looked up, and the request fields are unused.
	DNS *DNSCheck `json:"dns"`
	// Workflow is set for multi-step API checks only: their steps and the names of their secrets. URL is the first step's address.
	Workflow  *Workflow `json:"workflow"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DNSCheck is what a DNS monitor looks up. Expected is what an answer must match; null means any answer will do.
type DNSCheck struct {
	RecordType string  `json:"record_type"`
	Expected   *string `json:"expected"`
}

// Heartbeat is the state of a heartbeat monitor. The job pings /ping/<PingToken> each time it runs; DueBy is when HawkEye will call the job missed
// (the last ping, plus the period, plus the grace time), and is null until the first ping.
type Heartbeat struct {
	PingToken    string     `json:"ping_token"`
	GraceSeconds int        `json:"grace_seconds"`
	LastPingAt   *time.Time `json:"last_ping_at"`
	DueBy        *time.Time `json:"due_by"`
}

// QuietHours is a daily window, in local time, during which a monitor's alerts are held until it ends. Start and End are "HH:MM"; a window whose end
// is earlier than its start crosses midnight.
type QuietHours struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Timezone string `json:"timezone"` // an IANA time zone such as America/New_York
}

const MaxAlertDelayMinutes = 1440

// parseClock reads "HH:MM" as minutes after midnight.
func parseClock(v string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func formatClock(m int) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// cleanQuiet validates quiet hours and returns the stored form (minutes after midnight and the zone name).
func cleanQuiet(q *QuietHours, bad map[string]string) (start, end *int, tz *string) {
	if q == nil {
		return nil, nil, nil
	}
	s, okS := parseClock(q.Start)
	e, okE := parseClock(q.End)
	zone := strings.TrimSpace(q.Timezone)
	switch {
	case !okS:
		bad["quiet_hours.start"] = "must be a time like 22:00"
	case !okE:
		bad["quiet_hours.end"] = "must be a time like 07:00"
	case s == e:
		bad["quiet_hours.end"] = "must differ from the start"
	}
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" || len(zone) > 64 {
		bad["quiet_hours.timezone"] = "must be a time zone such as America/New_York"
	}
	return &s, &e, &zone
}

// SSLInfo describes the TLS certificate of an HTTPS monitor as last seen by a check.
type SSLInfo struct {
	ExpiresAt     time.Time `json:"expires_at"`
	CheckedAt     time.Time `json:"checked_at"`
	DaysRemaining int       `json:"days_remaining"` // whole days; negative once expired
	Status        string    `json:"status"`         // valid, expiring (30 days or fewer) or expired
}

// AuthInfo describes a monitor's authentication without its secret, which the API never returns.
type AuthInfo struct {
	Type       string `json:"type"`
	Username   string `json:"username,omitempty"`
	HeaderName string `json:"header_name,omitempty"`
	SecretSet  bool   `json:"secret_set"`
}

// AuthInput sets a monitor's authentication. Secret is the Basic password, the Bearer token or the header
// value. When it is omitted or empty and the type is unchanged, the stored secret is kept.
type AuthInput struct {
	Type       string  `json:"type"`
	Username   string  `json:"username"`
	HeaderName string  `json:"header_name"`
	Secret     *string `json:"secret"`
}

// Nullable distinguishes "field absent" from "field set to null" in PATCH bodies.
type Nullable[T any] struct {
	Set   bool
	Value *T
}

func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	n.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		n.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	n.Value = &v
	return nil
}

// Only client-editable fields exist here; state fields (status, counters, timestamps) cannot be set.
type CreateInput struct {
	Type                 string             `json:"type"` // http (the default), heartbeat or dns
	GraceSeconds         *int               `json:"grace_seconds"`
	DNSRecordType        *string            `json:"dns_record_type"`
	DNSExpected          *string            `json:"dns_expected"`
	Name                 string             `json:"name"`
	URL                  string             `json:"url"`
	Method               *string            `json:"method"`
	ExpectedStatusCode   *int               `json:"expected_status_code"`
	CheckIntervalSeconds *int               `json:"check_interval_seconds"`
	TimeoutSeconds       *int               `json:"timeout_seconds"`
	Headers              []checks.Header    `json:"headers"`
	Body                 *string            `json:"body"`
	Auth                 *AuthInput         `json:"auth"`
	Assertions           []checks.Assertion `json:"assertions"`
	Regions              []string           `json:"regions"`
	SlowThresholdMs      *int               `json:"slow_threshold_ms"`
	SlowChecks           *int               `json:"slow_checks"`
	TraceContext         *bool              `json:"trace_context"`
	TagIDs               []uuid.UUID        `json:"tag_ids"`
	AlertDelayMinutes    *int               `json:"alert_delay_minutes"`
	QuietHours           *QuietHours        `json:"quiet_hours"`
	// Multi-step API checks (type workflow) only: the steps, the secrets they use (name -> value), and whether cookies carry from step to step.
	Steps   []StepInput        `json:"steps"`
	Secrets map[string]*string `json:"secrets"`
	Cookies *bool              `json:"cookies"`
	// SecretNames declares secrets without values: the workflow is created paused, and a person enters the values in the app. Only the MCP server
	// sets it, so an assistant never handles a password; it is not part of the REST API.
	SecretNames []string `json:"-"`
}

type UpdateInput struct {
	Name                 *string              `json:"name"`
	URL                  *string              `json:"url"`
	Method               *string              `json:"method"`
	ExpectedStatusCode   Nullable[int]        `json:"expected_status_code"`
	CheckIntervalSeconds *int                 `json:"check_interval_seconds"`
	TimeoutSeconds       *int                 `json:"timeout_seconds"`
	Headers              *[]checks.Header     `json:"headers"`
	Body                 Nullable[string]     `json:"body"`
	Auth                 *AuthInput           `json:"auth"`
	Assertions           *[]checks.Assertion  `json:"assertions"`
	Regions              *[]string            `json:"regions"`
	SlowThresholdMs      Nullable[int]        `json:"slow_threshold_ms"`
	SlowChecks           *int                 `json:"slow_checks"`
	TraceContext         *bool                `json:"trace_context"`
	TagIDs               *[]uuid.UUID         `json:"tag_ids"` // replaces the monitor's tags
	AlertDelayMinutes    *int                 `json:"alert_delay_minutes"`
	QuietHours           Nullable[QuietHours] `json:"quiet_hours"` // null turns quiet hours off
	GraceSeconds         *int                 `json:"grace_seconds"`
	DNSRecordType        *string              `json:"dns_record_type"`
	DNSExpected          Nullable[string]     `json:"dns_expected"` // null or empty: any answer will do
	// Multi-step API checks only. Steps replace all the steps (a step's credentials are kept by position when not sent again). Secrets set the
	// listed values; null removes one; names left out are kept.
	Steps   *[]StepInput       `json:"steps"`
	Secrets map[string]*string `json:"secrets"`
	Cookies *bool              `json:"cookies"`
}

type ListFilter struct {
	Status  string // unknown|up|down, or empty
	Enabled *bool
	Query   string
	TagID   *uuid.UUID // only monitors with this tag
	Limit   int
	Offset  int
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
	box   *secrets.Box // encrypts stored credentials; without it, saving a credential fails rather than storing plain text
	regs  regions.Set  // the regions a monitor may be checked from
	// domainSync runs inside the transaction of every change that can alter which domains the organization's monitors use.
	domainSync func(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error
}

// Option configures a Service.
type Option func(*Service)

// WithSecretBox sets the cipher that protects stored monitor credentials.
func WithSecretBox(b *secrets.Box) Option { return func(s *Service) { s.box = b } }

// WithRegions sets the regions monitors may choose from (the default is a single "primary" region).
func WithRegions(r regions.Set) Option { return func(s *Service) { s.regs = r } }

// WithDomainSync registers the function that keeps the organization's tracked domains in step with its monitors (domains.SyncTx).
func WithDomainSync(fn func(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error) Option {
	return func(s *Service) { s.domainSync = fn }
}

// syncDomains runs the registered domain sync, if any.
func (s *Service) syncDomains(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	if s.domainSync == nil {
		return nil
	}
	return s.domainSync(ctx, tx, orgID)
}

func NewService(pool *pgxpool.Pool, p *plans.Service, opts ...Option) *Service {
	s := &Service{pool: pool, plans: p, regs: regions.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// sealSecret encrypts a credential for storage, bound to the monitor it belongs to. An empty secret stays empty.
func (s *Service) sealSecret(secret string, monitorID uuid.UUID) (string, error) {
	if secret == "" {
		return "", nil
	}
	return s.box.Seal(secret, monitorID.String())
}

// EncryptLegacySecrets encrypts any credential stored in plain text by an earlier version. It is idempotent and safe to run from several
// processes at once, and returns how many values it converted.
func (s *Service) EncryptLegacySecrets(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, auth_secret FROM monitors WHERE auth_secret IS NOT NULL AND auth_secret NOT LIKE 'enc:v1:%'`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id     uuid.UUID
		secret string
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.secret); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range todo {
		sealed, err := s.sealSecret(r.secret, r.id)
		if err != nil {
			return n, err
		}
		tag, err := s.pool.Exec(ctx, `UPDATE monitors SET auth_secret = $2 WHERE id = $1 AND auth_secret = $3`, r.id, sealed, r.secret)
		if err != nil {
			return n, err
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}

const cols = `id, organization_id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds,
	enabled, current_status, last_checked_at, last_success_at, consecutive_failures, consecutive_successes, created_at, updated_at,
	request_headers, request_body, auth_type, COALESCE(auth_username, ''), COALESCE(auth_header_name, ''), auth_secret IS NOT NULL,
	assertions, ssl_expires_at, ssl_checked_at, in_maintenance(organization_id, id, now()), regions,
	slow_threshold_ms, slow_checks, (SELECT started_at FROM slowdowns WHERE monitor_id = monitors.id AND status = 'open'), badge_token, ` + tagsCol + `,
	alert_delay_minutes, quiet_start, quiet_end, quiet_timezone, kind, COALESCE(ping_token, ''), COALESCE(heartbeat_grace_seconds, 0), last_ping_at, ping_deadline,
	COALESCE(dns_record_type, ''), dns_expected, trace_context`

func scan(row pgx.Row, minInterval int) (Monitor, error) {
	var m Monitor
	var sslExpires, sslChecked *time.Time
	var quietStart, quietEnd *int
	var quietTZ *string
	var hb Heartbeat
	var dns DNSCheck
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.URL, &m.Method, &m.ExpectedStatusCode, &m.CheckIntervalSeconds,
		&m.TimeoutSeconds, &m.Enabled, &m.CurrentStatus, &m.LastCheckedAt, &m.LastSuccessAt, &m.ConsecutiveFailures,
		&m.ConsecutiveSuccesses, &m.CreatedAt, &m.UpdatedAt, &m.Headers, &m.Body, &m.Auth.Type, &m.Auth.Username,
		&m.Auth.HeaderName, &m.Auth.SecretSet, &m.Assertions, &sslExpires, &sslChecked, &m.InMaintenance, &m.Regions,
		&m.SlowThresholdMs, &m.SlowChecks, &m.SlowSince, &m.BadgeToken, &m.Tags, &m.AlertDelayMinutes, &quietStart, &quietEnd, &quietTZ,
		&m.Type, &hb.PingToken, &hb.GraceSeconds, &hb.LastPingAt, &hb.DueBy, &dns.RecordType, &dns.Expected, &m.TraceContext)
	if quietStart != nil && quietEnd != nil && quietTZ != nil {
		m.QuietHours = &QuietHours{Start: formatClock(*quietStart), End: formatClock(*quietEnd), Timezone: *quietTZ}
	}
	if m.Type == TypeHeartbeat {
		m.Heartbeat = &hb
	}
	if m.Type == TypeDNS {
		m.DNS = &dns
	}
	m.EffectiveCheckIntervalSeconds = max(m.CheckIntervalSeconds, minInterval)
	if sslExpires != nil {
		now := time.Now()
		m.SSL = &SSLInfo{ExpiresAt: *sslExpires, DaysRemaining: checks.SSLDaysRemaining(*sslExpires, now), Status: checks.SSLStatus(*sslExpires, now)}
		if sslChecked != nil {
			m.SSL.CheckedAt = *sslChecked
		}
	}
	return m, err
}

// fields are the validated, normalized editable values of a monitor.
type fields struct {
	kind              string // TypeHTTP, TypeHeartbeat or TypeDNS
	grace             int    // heartbeats: seconds allowed after the period before a ping counts as missed
	dnsType           string // DNS monitors: the record to look up
	dnsExpected       string // DNS monitors: what an answer must match; "": any answer will do
	name, url, method string
	expected          *int
	interval, timeout int
	headers           []checks.Header
	body              *string
	auth              checks.Auth // Secret is "" when no new secret was supplied
	keepSecret        bool        // the stored secret stays: auth type unchanged and no new secret
	hasSecret         bool        // a secret is stored for the current auth type
	assertions        []checks.Assertion
	regions           []string
	slowThreshold     *int // ms; nil: slow-response alerts off
	slowChecks        int
	traceContext      bool // website and API monitors: send a traceparent header
	alertDelay        int
	quiet             *QuietHours // nil: no quiet hours
	quietStart        *int        // stored form of quiet, set by validate
	quietEnd          *int
	quietTZ           *string
	// Multi-step API checks: what was sent, the stored workflow on update, and the validated result (set by validate).
	steps       []StepInput
	newSecrets  map[string]*string
	declared    []string // secret names without values (MCP)
	cookies     bool
	curWorkflow *storedWorkflow
	draft       workflowDraft
}

// dnsColumns is the stored form of the DNS fields: NULL unless a DNS monitor, and NULL for "any answer".
func (f *fields) dnsColumns() (recordType, expected any) {
	if f.kind != TypeDNS {
		return nil, nil
	}
	if f.dnsExpected == "" {
		return f.dnsType, nil
	}
	return f.dnsType, f.dnsExpected
}

// setAuth applies an auth input on top of the current type, keeping the stored secret when allowed.
func (f *fields) setAuth(in *AuthInput, curType string, curHasSecret bool) {
	if in == nil {
		f.keepSecret = curHasSecret
		return
	}
	f.auth = checks.Auth{Type: strings.ToLower(strings.TrimSpace(in.Type)), Username: in.Username,
		HeaderName: strings.TrimSpace(in.HeaderName)}
	if f.auth.Type == "" {
		f.auth.Type = checks.AuthNone
	}
	if in.Secret != nil && *in.Secret != "" {
		f.auth.Secret = *in.Secret
	} else {
		f.keepSecret = f.auth.Type == curType && curHasSecret
	}
}

// validToken reports whether s is a valid HTTP header field name (RFC 9110 token).
func validToken(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if r > 0x7e || r <= ' ' || strings.ContainsRune(`"(),/:;<=>?@[\]{}`, r) {
			return false
		}
	}
	return true
}

// validHeaderValue rejects control characters, which could split or corrupt the request.
func validHeaderValue(s string) bool {
	for _, r := range s {
		if r == 0x7f || (r < ' ' && r != '\t') {
			return false
		}
	}
	return true
}

func (f *fields) validateRequest(bad map[string]string) {
	if f.headers == nil {
		f.headers = []checks.Header{}
	}
	if len(f.headers) > MaxHeaders {
		bad["headers"] = fmt.Sprintf("at most %d headers", MaxHeaders)
	}
	seen := map[string]bool{}
	for i, h := range f.headers {
		h.Name = strings.TrimSpace(h.Name)
		f.headers[i].Name = h.Name
		canon := http.CanonicalHeaderKey(h.Name)
		switch {
		case !validToken(h.Name):
			bad[fmt.Sprintf("headers[%d].name", i)] = "must be a valid header name"
		case canon == "Authorization":
			bad[fmt.Sprintf("headers[%d].name", i)] = "use authentication instead, so the credentials stay hidden"
		case reservedHeaders[canon]:
			bad[fmt.Sprintf("headers[%d].name", i)] = "is set by HawkEye and cannot be changed"
		case seen[canon]:
			bad[fmt.Sprintf("headers[%d].name", i)] = "is already set"
		}
		seen[canon] = true
		if !validHeaderValue(h.Value) || len(h.Value) > 1024 {
			bad[fmt.Sprintf("headers[%d].value", i)] = "must be at most 1024 characters without line breaks"
		}
	}

	if f.body != nil && *f.body == "" {
		f.body = nil
	}
	if f.body != nil {
		switch {
		case !bodyAllowed(f.method):
			bad["body"] = "can only be sent with POST, PUT, PATCH or DELETE"
		case len(*f.body) > MaxBodyBytes:
			bad["body"] = fmt.Sprintf("must be at most %d KB", MaxBodyBytes>>10)
		case !utf8.ValidString(*f.body):
			bad["body"] = "must be text"
		}
	}

	a := &f.auth
	switch a.Type {
	case checks.AuthNone:
		*a = checks.Auth{Type: checks.AuthNone}
		f.keepSecret = false
	case checks.AuthBasic, checks.AuthBearer, checks.AuthHeader:
		if a.Type != checks.AuthBasic {
			a.Username = ""
		}
		if a.Type != checks.AuthHeader {
			a.HeaderName = ""
		}
		if a.Type == checks.AuthBasic && (a.Username == "" || utf8.RuneCountInString(a.Username) > 200 ||
			strings.Contains(a.Username, ":") || !validHeaderValue(a.Username)) {
			bad["auth.username"] = "is required, at most 200 characters, without a colon"
		}
		if a.Type == checks.AuthHeader {
			canon := http.CanonicalHeaderKey(a.HeaderName)
			switch {
			case !validToken(a.HeaderName):
				bad["auth.header_name"] = "must be a valid header name"
			case reservedHeaders[canon] && canon != "Authorization":
				bad["auth.header_name"] = "is set by HawkEye and cannot be changed"
			case seen[canon]:
				bad["auth.header_name"] = "is also set as a custom header"
			}
		}
		switch {
		case a.Secret == "" && !f.keepSecret:
			bad["auth.secret"] = "is required"
		case a.Secret != "" && (utf8.RuneCountInString(a.Secret) > maxSecretChars || !validHeaderValue(a.Secret)):
			bad["auth.secret"] = fmt.Sprintf("must be at most %d characters without line breaks", maxSecretChars)
		}
	default:
		bad["auth.type"] = "must be none, basic, bearer or header"
	}

	if f.assertions == nil {
		f.assertions = []checks.Assertion{}
	}
	if len(f.assertions) > MaxAssertions {
		bad["assertions"] = fmt.Sprintf("at most %d assertions", MaxAssertions)
	}
	if len(f.assertions) > 0 && f.method == "HEAD" {
		bad["assertions"] = "HEAD responses have no body; use GET to check the content"
	}
	for i, as := range f.assertions {
		key := func(k string) string { return fmt.Sprintf("assertions[%d].%s", i, k) }
		as.Type = strings.TrimSpace(as.Type)
		as.Path = strings.TrimSpace(as.Path)
		as.Operator = strings.TrimSpace(as.Operator)
		if utf8.RuneCountInString(as.Value) > 1000 {
			bad[key("value")] = "must be at most 1000 characters"
		}
		switch as.Type {
		case checks.AssertBodyContains, checks.AssertBodyNotContains:
			as.Path, as.Operator = "", ""
			if as.Value == "" {
				bad[key("value")] = "is required"
			}
		case checks.AssertJSONPath:
			if err := checks.ValidatePath(as.Path); err != nil || len(as.Path) > 200 {
				bad[key("path")] = "must be a path like data.items[0].status"
			}
			switch as.Operator {
			case checks.OpExists, checks.OpNotExists:
				as.Value = ""
			case checks.OpEquals, checks.OpNotEquals, checks.OpContains:
			default:
				bad[key("operator")] = "must be equals, not_equals, contains, exists or not_exists"
			}
		default:
			bad[key("type")] = "must be body_contains, body_not_contains or json_path"
		}
		f.assertions[i] = as
	}
}

func (f *fields) validate() error {
	bad := map[string]string{}
	f.name = strings.TrimSpace(f.name)
	if n := utf8.RuneCountInString(f.name); n < 1 || n > 100 {
		bad["name"] = "must be between 1 and 100 characters"
	}
	if f.alertDelay < 0 || f.alertDelay > MaxAlertDelayMinutes {
		bad["alert_delay_minutes"] = fmt.Sprintf("must be between 0 and %d minutes", MaxAlertDelayMinutes)
	}
	f.quietStart, f.quietEnd, f.quietTZ = cleanQuiet(f.quiet, bad)
	if f.traceContext && f.kind != TypeHTTP {
		bad["trace_context"] = "is only used by website and API monitors"
	}
	switch f.kind {
	case TypeHeartbeat:
		f.validateHeartbeat(bad)
	case TypeDNS:
		f.validateDNS(bad)
	case TypeWorkflow:
		f.validateWorkflowMonitor(bad)
	default:
		f.validateHTTP(bad)
	}
	if len(bad) > 0 {
		return &validation.Error{Fields: bad}
	}
	return nil
}

// validateHeartbeat checks a heartbeat's period (the interval) and grace time. A heartbeat makes no request, so the website fields must be left out.
func (f *fields) validateHeartbeat(bad map[string]string) {
	if f.interval < MinIntervalSeconds || f.interval > MaxPeriodSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be between %d seconds and %d days", MinIntervalSeconds, MaxPeriodSeconds/86400)
	}
	if f.grace < MinGraceSeconds || f.grace > MaxGraceSeconds {
		bad["grace_seconds"] = fmt.Sprintf("must be between %d seconds and %d days", MinGraceSeconds, MaxGraceSeconds/86400)
	}
	notUsed := "is not used by heartbeat monitors"
	if strings.TrimSpace(f.url) != "" {
		bad["url"] = notUsed
	}
	if len(f.regions) > 0 {
		bad["regions"] = notUsed
	}
	if f.slowThreshold != nil {
		bad["slow_threshold_ms"] = notUsed
	}
	if len(f.headers) > 0 || f.body != nil && *f.body != "" || len(f.assertions) > 0 || f.auth.Type != checks.AuthNone || f.expected != nil {
		bad["type"] = "heartbeat monitors make no request: leave out the headers, body, authentication, assertions and expected status"
	}
	f.url, f.method, f.headers, f.body, f.assertions, f.regions = "", "GET", []checks.Header{}, nil, []checks.Assertion{}, []string{}
}

// validateDNS checks the host name, the record type and the expected value. A DNS monitor makes no web request, so the website fields must be left out.
func (f *fields) validateDNS(bad map[string]string) {
	host, err := publichost.ParseDNSName(f.url)
	var invalid *publichost.InvalidHostError
	switch {
	case errors.As(err, &invalid):
		bad["url"] = invalid.Reason
	case err != nil:
		bad["url"] = "must be a public host name"
	default:
		f.url = host
	}
	f.dnsType = strings.ToUpper(strings.TrimSpace(f.dnsType))
	if slices.Contains(checks.RecordTypes, f.dnsType) {
		expected, reason := checks.NormalizeDNSExpected(f.dnsType, f.dnsExpected)
		if reason != "" {
			bad["dns_expected"] = reason
		}
		f.dnsExpected = expected
	} else {
		bad["dns_record_type"] = "must be one of " + strings.Join(checks.RecordTypes, ", ")
	}
	f.validateTiming(bad)
	if f.slowThreshold != nil {
		bad["slow_threshold_ms"] = "is not used by DNS monitors"
	}
	if len(f.headers) > 0 || f.body != nil && *f.body != "" || len(f.assertions) > 0 || f.auth.Type != checks.AuthNone || f.expected != nil {
		bad["type"] = "DNS monitors make no web request: leave out the headers, body, authentication, assertions and expected status"
	}
	f.method, f.headers, f.body, f.assertions = "GET", []checks.Header{}, nil, []checks.Assertion{}
}

// validateWorkflowMonitor checks a multi-step API check: its steps and secrets, and the settings of the monitor itself. Its requests come from its
// steps, so the website fields must be left out; the monitor's address becomes the first step's.
func (f *fields) validateWorkflowMonitor(bad map[string]string) {
	if strings.TrimSpace(f.url) != "" {
		bad["url"] = "is not used by multi-step API checks: each step has its own address"
	}
	if len(f.headers) > 0 || f.body != nil && *f.body != "" || len(f.assertions) > 0 || f.auth.Type != checks.AuthNone || f.expected != nil {
		bad["type"] = "multi-step API checks send their requests from their steps: leave out the headers, body, authentication, assertions and expected status"
	}
	f.draft = validateWorkflow(f.steps, f.newSecrets, f.declared, f.cookies, f.curWorkflow, bad)
	f.timeout = workflowTimeoutSeconds
	f.validateTiming(bad)
	if t := f.slowThreshold; t != nil && (*t < MinSlowThresholdMs || *t > MaxSlowThresholdMs) {
		bad["slow_threshold_ms"] = fmt.Sprintf("must be between %d and %d milliseconds (for all the steps together), or null", MinSlowThresholdMs, MaxSlowThresholdMs)
	}
	if f.slowChecks < MinSlowChecks || f.slowChecks > MaxSlowChecks {
		bad["slow_checks"] = fmt.Sprintf("must be between %d and %d", MinSlowChecks, MaxSlowChecks)
	}
	if f.draft.usesWriteMethod() && f.interval < MinWriteIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be at least %d minutes when a step uses POST, PUT, PATCH or DELETE, because the requests are repeated on every check", MinWriteIntervalSeconds/60)
	}
	f.url = ""
	if len(f.draft.steps) > 0 {
		f.url = f.draft.steps[0].URL
	}
	f.method, f.headers, f.body, f.assertions, f.auth = "GET", []checks.Header{}, nil, []checks.Assertion{}, checks.Auth{Type: checks.AuthNone}
}

// validateTiming checks the two settings every monitor HawkEye checks itself has.
func (f *fields) validateTiming(bad map[string]string) {
	if f.interval < MinIntervalSeconds || f.interval > MaxIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be between %d and %d", MinIntervalSeconds, MaxIntervalSeconds)
	}
	if f.timeout < 1 || f.timeout > 30 {
		bad["timeout_seconds"] = "must be between 1 and 30"
	}
}

func (f *fields) validateHTTP(bad map[string]string) {
	if u, reason := NormalizeURL(f.url); reason != "" {
		bad["url"] = reason
	} else {
		f.url = u
	}
	f.method = strings.ToUpper(strings.TrimSpace(f.method))
	if !slices.Contains(Methods, f.method) {
		bad["method"] = "must be one of " + strings.Join(Methods, ", ")
	}
	if f.expected != nil && (*f.expected < 100 || *f.expected > 599) {
		bad["expected_status_code"] = "must be between 100 and 599, or null"
	}
	f.validateTiming(bad)
	if f.slowThreshold != nil {
		switch t := *f.slowThreshold; {
		case t < MinSlowThresholdMs || t > MaxSlowThresholdMs:
			bad["slow_threshold_ms"] = fmt.Sprintf("must be between %d and %d milliseconds, or null to turn slow-response alerts off", MinSlowThresholdMs, MaxSlowThresholdMs)
		case t >= f.timeout*1000:
			// A response slower than the timeout is a failed check, so a threshold at or above it could never be reached.
			bad["slow_threshold_ms"] = fmt.Sprintf("must be lower than the timeout (%d seconds)", f.timeout)
		}
	}
	if f.slowChecks < MinSlowChecks || f.slowChecks > MaxSlowChecks {
		bad["slow_checks"] = fmt.Sprintf("must be between %d and %d", MinSlowChecks, MaxSlowChecks)
	}
	if IsWriteMethod(f.method) && f.interval < MinWriteIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be at least %d minutes for %s, because the request is repeated on every check", MinWriteIntervalSeconds/60, f.method)
	}
	if f.traceContext && slices.ContainsFunc(f.headers, func(h checks.Header) bool { return strings.EqualFold(strings.TrimSpace(h.Name), "traceparent") }) {
		bad["trace_context"] = "can't be used with a traceparent header of your own: remove the header, or leave trace context off"
	}
	f.validateRequest(bad)
}

// lockOrg serializes monitor creation per organization so concurrent requests cannot both
// pass the limit check.
func lockOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// cleanRegions validates a requested list of regions: each must be configured, with no repeats. An empty list means the primary region.
func (s *Service) cleanRegions(chosen []string) ([]string, error) {
	out := []string{}
	for _, c := range chosen {
		c = strings.ToLower(strings.TrimSpace(c))
		switch {
		case !s.regs.Has(c):
			return nil, validation.New("regions", fmt.Sprintf("%q is not an available region", c))
		case slices.Contains(out, c):
			return nil, validation.New("regions", fmt.Sprintf("%q is listed twice", c))
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in CreateInput) (Monitor, error) {
	var out Monitor
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		minInterval, err := s.plans.GetMinimumCheckInterval(ctx, tx, orgID)
		if err != nil {
			return err
		}
		f := fields{kind: TypeHTTP, name: in.Name, url: in.URL, method: "GET", expected: in.ExpectedStatusCode,
			interval: minInterval, timeout: DefaultTimeoutSeconds}
		switch strings.TrimSpace(in.Type) {
		case "", TypeHTTP:
		case TypeHeartbeat:
			f.kind, f.interval, f.grace = TypeHeartbeat, max(DefaultPeriodSeconds, minInterval), DefaultGraceSeconds
			if in.GraceSeconds != nil {
				f.grace = *in.GraceSeconds
			}
		case TypeDNS:
			f.kind, f.dnsType = TypeDNS, checks.RecordA
			if in.DNSRecordType != nil {
				f.dnsType = *in.DNSRecordType
			}
			if in.DNSExpected != nil {
				f.dnsExpected = *in.DNSExpected
			}
		case TypeWorkflow:
			f.kind, f.steps, f.newSecrets, f.declared = TypeWorkflow, in.Steps, in.Secrets, in.SecretNames
			if in.Cookies != nil {
				f.cookies = *in.Cookies
			}
			if in.CheckIntervalSeconds == nil && slices.ContainsFunc(in.Steps, func(st StepInput) bool { return IsWriteMethod(st.Method) }) {
				f.interval = max(f.interval, MinWriteIntervalSeconds)
			}
		default:
			return validation.New("type", "must be http, heartbeat, dns or workflow")
		}
		if f.kind != TypeWorkflow && (in.Steps != nil || in.Secrets != nil || in.Cookies != nil) {
			return validation.New("steps", "is only used by multi-step API checks (type workflow)")
		}
		if in.Method != nil {
			f.method = *in.Method
		}
		if in.CheckIntervalSeconds != nil {
			f.interval = *in.CheckIntervalSeconds
		} else if IsWriteMethod(f.method) {
			f.interval = max(f.interval, MinWriteIntervalSeconds)
		}
		if in.TimeoutSeconds != nil {
			f.timeout = *in.TimeoutSeconds
		}
		f.headers, f.body, f.assertions = slices.Clone(in.Headers), in.Body, slices.Clone(in.Assertions)
		f.slowThreshold, f.slowChecks = in.SlowThresholdMs, DefaultSlowChecks
		if in.TraceContext != nil {
			f.traceContext = *in.TraceContext
		}
		if in.SlowChecks != nil {
			f.slowChecks = *in.SlowChecks
		}
		if in.AlertDelayMinutes != nil {
			f.alertDelay = *in.AlertDelayMinutes
		}
		f.quiet = in.QuietHours
		if f.regions, err = s.cleanRegions(in.Regions); err != nil {
			return err
		}
		f.setAuth(in.Auth, checks.AuthNone, false)
		if in.Auth == nil {
			f.auth.Type = checks.AuthNone
		}
		if err := f.validate(); err != nil {
			return err
		}
		if err := s.plans.CanCreateMonitor(ctx, tx, orgID); err != nil {
			return err
		}
		if f.kind == TypeWorkflow {
			if err := s.plans.CanCreateWorkflow(ctx, tx, orgID); err != nil {
				return err
			}
			if err := s.plans.CheckWorkflowSteps(ctx, tx, orgID, len(f.draft.steps)); err != nil {
				return err
			}
		}
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
				return err
			}
		}
		if err := s.plans.CheckRegions(ctx, tx, orgID, len(f.regions)); err != nil {
			return err
		}
		tagIDs, err := resolveTags(ctx, tx, orgID, in.TagIDs)
		if err != nil {
			return err
		}
		if len(tagIDs) > MaxTagsPerMonitor {
			return validation.New("tag_ids", fmt.Sprintf("at most %d tags per monitor", MaxTagsPerMonitor))
		}
		id := uuid.New() // chosen here so the credential can be encrypted for exactly this monitor
		sealed, err := s.sealSecret(f.auth.Secret, id)
		if err != nil {
			return fmt.Errorf("protect credentials: %w", err)
		}
		var pingToken, grace any // NULL unless a heartbeat
		if f.kind == TypeHeartbeat {
			pingToken, grace = NewPingToken(), f.grace
		}
		dnsType, dnsExpected := f.dnsColumns()
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO monitors
			(id, organization_id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds,
			 request_headers, request_body, auth_type, auth_username, auth_header_name, auth_secret, assertions, regions, slow_threshold_ms, slow_checks,
			 alert_delay_minutes, quiet_start, quiet_end, quiet_timezone, kind, ping_token, heartbeat_grace_seconds, dns_record_type, dns_expected, trace_context, next_check_at)
			VALUES ($15,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),$14,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,
				CASE WHEN $23 = 'heartbeat' THEN 'infinity'::timestamptz ELSE now() END) RETURNING `+cols,
			orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout,
			f.headers, f.body, f.auth.Type, f.auth.Username, f.auth.HeaderName, sealed, f.assertions, id, f.regions, f.slowThreshold, f.slowChecks,
			f.alertDelay, f.quietStart, f.quietEnd, f.quietTZ, f.kind, pingToken, grace, dnsType, dnsExpected, f.traceContext), minInterval)
		if err != nil {
			return err
		}
		if f.kind == TypeWorkflow {
			if err := s.saveWorkflow(ctx, tx, id, f.draft, nil); err != nil {
				return err
			}
			// Secrets still to be entered: it can't run yet, so it starts paused.
			if len(f.draft.declared) > 0 {
				if err := tx.QueryRow(ctx, `UPDATE monitors SET enabled = false WHERE id = $1 RETURNING enabled`, id).Scan(&out.Enabled); err != nil {
					return err
				}
			}
			one := []Monitor{out}
			if err := attachWorkflowsTx(ctx, tx, one); err != nil {
				return err
			}
			out = one[0]
		}
		if len(tagIDs) > 0 {
			// The row had to exist before it could be tagged, so its tags are read back separately.
			if err := setTags(ctx, tx, id, tagIDs); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT `+tagsCol+` FROM monitors WHERE id = $1`, id).Scan(&out.Tags); err != nil {
				return err
			}
		}
		return s.syncDomains(ctx, tx, orgID)
	})
	return out, err
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	m, err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM monitors
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, id, orgID), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, err
	}
	one := []Monitor{m}
	if err := attachWorkflows(ctx, s.pool, one); err != nil {
		return Monitor{}, err
	}
	return one[0], nil
}

// List returns one page of monitors and the total matching count.
func (s *Service) List(ctx context.Context, orgID uuid.UUID, f ListFilter) ([]Monitor, int, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return nil, 0, err
	}
	where := `organization_id = $1 AND deleted_at IS NULL`
	args := []any{orgID}
	add := func(cond string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(" AND "+cond, len(args))
	}
	if f.Status != "" {
		add("current_status = $%d", f.Status)
	}
	if f.Enabled != nil {
		add("enabled = $%d", *f.Enabled)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		add(`(name ILIKE $%[1]d OR url ILIKE $%[1]d)`, "%"+esc+"%")
	}
	if f.TagID != nil {
		add("EXISTS (SELECT 1 FROM monitor_tags mt WHERE mt.monitor_id = monitors.id AND mt.tag_id = $%d)", *f.TagID)
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT %s FROM monitors WHERE %s
		ORDER BY created_at DESC, id LIMIT $%d OFFSET $%d`, cols, where, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Monitor{}
	for rows.Next() {
		m, err := scan(rows, min)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if err := attachWorkflows(ctx, s.pool, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (s *Service) Update(ctx context.Context, orgID, id uuid.UUID, in UpdateInput) (Monitor, error) {
	var out Monitor
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		minInterval, err := s.plans.GetMinimumCheckInterval(ctx, tx, orgID)
		if err != nil {
			return err
		}
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM monitors
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL FOR UPDATE`, id, orgID), minInterval)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		f := fields{kind: cur.Type, name: cur.Name, url: cur.URL, method: cur.Method, expected: cur.ExpectedStatusCode,
			interval: cur.CheckIntervalSeconds, timeout: cur.TimeoutSeconds, headers: slices.Clone(cur.Headers),
			body: cur.Body, assertions: slices.Clone(cur.Assertions), regions: slices.Clone(cur.Regions),
			slowThreshold: cur.SlowThresholdMs, slowChecks: cur.SlowChecks, alertDelay: cur.AlertDelayMinutes, quiet: cur.QuietHours, traceContext: cur.TraceContext,
			auth: checks.Auth{Type: cur.Auth.Type, Username: cur.Auth.Username, HeaderName: cur.Auth.HeaderName}}
		f.setAuth(in.Auth, cur.Auth.Type, cur.Auth.SecretSet)
		if in.Headers != nil {
			f.headers = slices.Clone(*in.Headers)
		}
		if in.TraceContext != nil {
			f.traceContext = *in.TraceContext
		}
		if in.Body.Set {
			f.body = in.Body.Value
		}
		if in.Assertions != nil {
			f.assertions = slices.Clone(*in.Assertions)
		}
		if in.Regions != nil {
			if f.regions, err = s.cleanRegions(*in.Regions); err != nil {
				return err
			}
		}
		if in.Name != nil {
			f.name = *in.Name
		}
		if in.URL != nil {
			f.url = *in.URL
		}
		if in.Method != nil {
			f.method = *in.Method
		}
		if in.ExpectedStatusCode.Set {
			f.expected = in.ExpectedStatusCode.Value
		}
		if in.CheckIntervalSeconds != nil {
			f.interval = *in.CheckIntervalSeconds
		}
		if in.TimeoutSeconds != nil {
			f.timeout = *in.TimeoutSeconds
		}
		if in.SlowThresholdMs.Set {
			f.slowThreshold = in.SlowThresholdMs.Value
		}
		if in.SlowChecks != nil {
			f.slowChecks = *in.SlowChecks
		}
		if in.AlertDelayMinutes != nil {
			f.alertDelay = *in.AlertDelayMinutes
		}
		if in.QuietHours.Set {
			f.quiet = in.QuietHours.Value
		}
		var grace any // NULL unless a heartbeat
		if cur.Heartbeat != nil {
			f.grace = cur.Heartbeat.GraceSeconds
			if in.GraceSeconds != nil {
				f.grace = *in.GraceSeconds
			}
			grace = f.grace
		} else if in.GraceSeconds != nil {
			return validation.New("grace_seconds", "is only used by heartbeat monitors")
		}
		if cur.Type == TypeWorkflow {
			if f.curWorkflow, err = loadWorkflow(ctx, tx, id); err != nil {
				return err
			}
			f.url, f.cookies, f.newSecrets = "", f.curWorkflow.cookies, in.Secrets
			f.steps = make([]StepInput, len(f.curWorkflow.steps))
			for i, st := range f.curWorkflow.steps {
				f.steps[i] = st.input()
			}
			if in.Steps != nil {
				f.steps = *in.Steps
			}
			if in.Cookies != nil {
				f.cookies = *in.Cookies
			}
			if in.URL != nil && *in.URL != "" {
				return validation.New("url", "is not used by multi-step API checks: each step has its own address")
			}
		} else if in.Steps != nil || in.Secrets != nil || in.Cookies != nil {
			return validation.New("steps", "is only used by multi-step API checks (type workflow)")
		}
		var curExpected string
		if cur.DNS != nil {
			f.dnsType = cur.DNS.RecordType
			if cur.DNS.Expected != nil {
				curExpected = *cur.DNS.Expected
			}
			f.dnsExpected = curExpected
			if in.DNSRecordType != nil {
				f.dnsType = *in.DNSRecordType
				if !in.DNSExpected.Set {
					f.dnsExpected = "" // the old value was written for another kind of record
				}
			}
			if in.DNSExpected.Set {
				f.dnsExpected = ""
				if in.DNSExpected.Value != nil {
					f.dnsExpected = *in.DNSExpected.Value
				}
			}
		} else if in.DNSRecordType != nil || in.DNSExpected.Set {
			return validation.New("dns_record_type", "is only used by DNS monitors")
		}
		if err := f.validate(); err != nil {
			return err
		}
		// Only a requested interval change is checked: after a downgrade, unrelated edits still work.
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
				return err
			}
		}
		// Only a requested change of steps is checked against the plan's step limit, like the interval.
		if in.Steps != nil {
			if err := s.plans.CheckWorkflowSteps(ctx, tx, orgID, len(f.draft.steps)); err != nil {
				return err
			}
		}
		// Only a requested change of regions is checked against the plan, like the interval.
		regionsChanged := in.Regions != nil && !slices.Equal(f.regions, cur.Regions)
		if regionsChanged {
			if err := s.plans.CheckRegions(ctx, tx, orgID, len(f.regions)); err != nil {
				return err
			}
		}
		// A changed request (target, headers, body, credentials or the DNS record looked up) invalidates the previous health state.
		reset := f.url != cur.URL || f.method != cur.Method || !slices.Equal(f.headers, cur.Headers) ||
			!equalPtr(f.body, cur.Body) || f.auth.Type != cur.Auth.Type || f.auth.Username != cur.Auth.Username || f.auth.HeaderName != cur.Auth.HeaderName ||
			f.auth.Secret != "" || cur.DNS != nil && (f.dnsType != cur.DNS.RecordType || f.dnsExpected != curExpected) ||
			cur.Type == TypeWorkflow && (in.Steps != nil || len(in.Secrets) > 0 || in.Cookies != nil && *in.Cookies != f.curWorkflow.cookies)
		dnsType, dnsExpected := f.dnsColumns()
		sealed, err := s.sealSecret(f.auth.Secret, id)
		if err != nil {
			return fmt.Errorf("protect credentials: %w", err)
		}
		// Streaks measured against another threshold, or another request, say nothing about the new one: start counting again, and close an
		// open slowdown without a "back to normal" alert, since nothing got faster.
		speedReset := reset || regionsChanged || !equalPtr(f.slowThreshold, cur.SlowThresholdMs) || f.slowChecks != cur.SlowChecks
		if speedReset {
			if err := resetSpeed(ctx, tx, id); err != nil {
				return err
			}
		}
		// Written before the UPDATE below, so its RETURNING sees the new tags.
		if in.TagIDs != nil {
			tagIDs, err := resolveTags(ctx, tx, orgID, *in.TagIDs)
			if err != nil {
				return err
			}
			if err := setTags(ctx, tx, id, tagIDs); err != nil {
				return err
			}
		}
		out, err = scan(tx.QueryRow(ctx, `UPDATE monitors SET name=$3, url=$4, method=$5, expected_status_code=$6,
			check_interval_seconds=$7, timeout_seconds=$8,
			next_check_at = CASE WHEN $9 THEN now() ELSE next_check_at END,
			current_status = CASE WHEN $9 THEN 'unknown' ELSE current_status END,
			consecutive_failures = CASE WHEN $9 THEN 0 ELSE consecutive_failures END,
			consecutive_successes = CASE WHEN $9 THEN 0 ELSE consecutive_successes END,
			request_headers=$10, request_body=$11, auth_type=$12, auth_username=NULLIF($13,''),
			auth_header_name=NULLIF($14,''),
			auth_secret = CASE WHEN $12 = 'none' THEN NULL WHEN $15 THEN auth_secret ELSE NULLIF($16,'') END,
			assertions=$17, regions=$19, slow_threshold_ms=$20, slow_checks=$21,
			alert_delay_minutes=$22, quiet_start=$23, quiet_end=$24, quiet_timezone=$25, heartbeat_grace_seconds=$26,
			dns_record_type=$28, dns_expected=$29, trace_context=$30,
			ping_deadline = CASE WHEN ping_deadline IS NOT NULL AND last_ping_at IS NOT NULL AND (check_interval_seconds <> $7 OR heartbeat_grace_seconds <> $26)
				THEN last_ping_at + make_interval(secs => GREATEST($7::int, $27::int) + $26::int) ELSE ping_deadline END,
			ssl_expires_at = CASE WHEN $18 THEN NULL ELSE ssl_expires_at END, ssl_checked_at = CASE WHEN $18 THEN NULL ELSE ssl_checked_at END,
			ssl_notified_bucket = CASE WHEN $18 THEN NULL ELSE ssl_notified_bucket END, ssl_notified_for = CASE WHEN $18 THEN NULL ELSE ssl_notified_for END
			WHERE id = $1 AND organization_id = $2 RETURNING `+cols,
			id, orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout, reset,
			f.headers, f.body, f.auth.Type, f.auth.Username, f.auth.HeaderName, f.keepSecret, sealed, f.assertions, f.url != cur.URL, f.regions,
			f.slowThreshold, f.slowChecks, f.alertDelay, f.quietStart, f.quietEnd, f.quietTZ, grace, minInterval, dnsType, dnsExpected, f.traceContext), minInterval)
		if err != nil {
			return err
		}
		if cur.Type == TypeWorkflow {
			if err := s.saveWorkflow(ctx, tx, id, f.draft, f.curWorkflow); err != nil {
				return err
			}
			one := []Monitor{out}
			if err := attachWorkflowsTx(ctx, tx, one); err != nil {
				return err
			}
			out = one[0]
		}
		// Streaks recorded by each region no longer describe the monitor after its request or its regions changed.
		if reset || regionsChanged {
			if _, err = tx.Exec(ctx, `DELETE FROM monitor_region_state WHERE monitor_id = $1`, id); err != nil {
				return err
			}
		}
		if f.url != cur.URL {
			return s.lockedDomainSync(ctx, tx, orgID)
		}
		return nil
	})
	return out, err
}

// resetSpeed clears a monitor's slow-response streaks (its own and every location's) and quietly closes its open slowdown.
func resetSpeed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if _, err := tx.Exec(ctx, `UPDATE monitors SET slow_streak = 0, normal_streak = 0 WHERE id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE monitor_region_state SET slow = false, slow_streak = 0, normal_streak = 0 WHERE monitor_id = $1`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE slowdowns SET status = 'resolved', resolved_at = GREATEST(started_at, now()) WHERE monitor_id = $1 AND status = 'open'`, id)
	return err
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// resumeDeadline is the SET clause that restarts a heartbeat's clock when it is resumed ($3: the new enabled value, $4: the plan's minimum interval).
// A heartbeat that has never been pinged keeps waiting for its first ping.
const resumeDeadline = `ping_deadline = CASE WHEN $3 AND NOT enabled AND last_ping_at IS NOT NULL
	THEN now() + make_interval(secs => GREATEST(check_interval_seconds, $4::int) + heartbeat_grace_seconds) ELSE ping_deadline END`

// NewPingToken returns a new random heartbeat ping token (128 bits, URL-safe).
func NewPingToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system CSPRNG failing is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RotatePingToken gives a heartbeat monitor a new ping address. The old one stops working at once, so the job must be updated to the new one.
func (s *Service) RotatePingToken(ctx context.Context, orgID, id uuid.UUID) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	m, err := scan(s.pool.QueryRow(ctx, `UPDATE monitors SET ping_token = $3
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL AND kind = 'heartbeat' RETURNING `+cols, id, orgID, NewPingToken()), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return m, err
}

// SetEnabled pauses or resumes a monitor. A workflow with secrets not entered yet can't be resumed (*ErrSecretsNotSet).
func (s *Service) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	if enabled {
		if names, err := unsetSecrets(ctx, s.pool, []uuid.UUID{id}); err != nil {
			return Monitor{}, err
		} else if len(names) > 0 {
			return Monitor{}, &ErrSecretsNotSet{Names: names}
		}
	}
	// Resuming makes the monitor due immediately instead of waiting out a stale schedule; a heartbeat gets a full period (and its grace time) to ping.
	m, err := scan(s.pool.QueryRow(ctx, `UPDATE monitors SET enabled = $3,
		next_check_at = CASE WHEN $3 AND NOT enabled AND kind <> 'heartbeat' THEN now() ELSE next_check_at END, `+resumeDeadline+`
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL RETURNING `+cols, id, orgID, enabled, min), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, err
	}
	one := []Monitor{m}
	if err := attachWorkflows(ctx, s.pool, one); err != nil {
		return Monitor{}, err
	}
	return one[0], nil
}

// Delete soft-deletes a monitor: it stops counting toward limits and is no longer checked.
// An open incident is closed without notifications: nobody needs a recovery email for a monitor they removed.
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := deleteTx(ctx, tx, orgID, []uuid.UUID{id})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		return s.lockedDomainSync(ctx, tx, orgID)
	})
}

// deleteTx soft-deletes the listed monitors of the organization and returns how many it deleted.
func deleteTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, ids []uuid.UUID) (int64, error) {
	// A deleted monitor keeps its history for a while, but never its credentials, headers, request body, tags or a working ping address.
	tag, err := tx.Exec(ctx, `UPDATE monitors SET deleted_at = now(), enabled = false,
		auth_type = 'none', auth_secret = NULL, auth_username = NULL, auth_header_name = NULL,
		request_headers = '[]', request_body = NULL, ping_token = NULL
		WHERE id = ANY($1) AND organization_id = $2 AND deleted_at IS NULL`, ids, orgID)
	if err != nil || tag.RowsAffected() == 0 {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM monitor_tags WHERE monitor_id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	// A workflow's steps (their headers, bodies and credentials) and secrets go at once too, like a monitor's own request.
	if _, err = tx.Exec(ctx, `DELETE FROM monitor_steps WHERE monitor_id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM monitor_secrets WHERE monitor_id = ANY($1)`, ids); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE incidents SET status = 'resolved', resolved_at = GREATEST(started_at, now())
		WHERE monitor_id = ANY($1) AND status = 'open'`, ids); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// lockedDomainSync is syncDomains for changes that did not already hold the organization row lock.
func (s *Service) lockedDomainSync(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	if s.domainSync == nil {
		return nil
	}
	if err := lockOrg(ctx, tx, orgID); err != nil {
		return err
	}
	return s.domainSync(ctx, tx, orgID)
}

// RegionStatus is how one of a monitor's regions currently sees it.
type RegionStatus struct {
	Region        string     `json:"region"`
	Name          string     `json:"name"`
	Status        string     `json:"status"` // unknown, up or down
	LastCheckedAt *time.Time `json:"last_checked_at"`
	LastError     *string    `json:"last_error"`
}

// RegionStatuses lists the regions the monitor runs in now, in the order chosen, with each region's own status.
func (s *Service) RegionStatuses(ctx context.Context, orgID, id uuid.UUID) ([]RegionStatus, error) {
	m, err := s.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	p, err := s.plans.Plan(ctx, s.pool, orgID)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT region, status, last_checked_at, last_error FROM monitor_region_state WHERE monitor_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]RegionStatus{}
	for rows.Next() {
		var r RegionStatus
		if err := rows.Scan(&r.Region, &r.Status, &r.LastCheckedAt, &r.LastError); err != nil {
			return nil, err
		}
		known[r.Region] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []RegionStatus{}
	for _, code := range s.regs.Effective(m.Regions, p.MaxRegions) {
		r, ok := known[code]
		if !ok {
			r = RegionStatus{Region: code, Status: "unknown"}
		}
		r.Name = s.regs.Name(code)
		out = append(out, r)
	}
	return out, nil
}
