// Package monitors manages monitor definitions. Every query is scoped by organization ID, which
// callers must have authorised; a monitor in another organization is indistinguishable from a missing one.
package monitors

import (
	"bytes"
	"context"
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
	"hawkeye/internal/regions"
	"hawkeye/internal/secrets"
	"hawkeye/internal/validation"
)

var ErrNotFound = errors.New("monitor not found")

const (
	DefaultTimeoutSeconds = 10
	MaxIntervalSeconds    = 86400
	MinIntervalSeconds    = 60

	MaxHeaders     = 20
	MaxAssertions  = 10
	MaxBodyBytes   = 64 << 10
	maxSecretChars = 2048
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
	CreatedAt                     time.Time          `json:"created_at"`
	UpdatedAt                     time.Time          `json:"updated_at"`
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
}

type UpdateInput struct {
	Name                 *string             `json:"name"`
	URL                  *string             `json:"url"`
	Method               *string             `json:"method"`
	ExpectedStatusCode   Nullable[int]       `json:"expected_status_code"`
	CheckIntervalSeconds *int                `json:"check_interval_seconds"`
	TimeoutSeconds       *int                `json:"timeout_seconds"`
	Headers              *[]checks.Header    `json:"headers"`
	Body                 Nullable[string]    `json:"body"`
	Auth                 *AuthInput          `json:"auth"`
	Assertions           *[]checks.Assertion `json:"assertions"`
	Regions              *[]string           `json:"regions"`
}

type ListFilter struct {
	Status  string // unknown|up|down, or empty
	Enabled *bool
	Query   string
	Limit   int
	Offset  int
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
	box   *secrets.Box // encrypts stored credentials; without it, saving a credential fails rather than storing plain text
	regs  regions.Set  // the regions a monitor may be checked from
}

// Option configures a Service.
type Option func(*Service)

// WithSecretBox sets the cipher that protects stored monitor credentials.
func WithSecretBox(b *secrets.Box) Option { return func(s *Service) { s.box = b } }

// WithRegions sets the regions monitors may choose from (the default is a single "primary" region).
func WithRegions(r regions.Set) Option { return func(s *Service) { s.regs = r } }

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
	assertions, ssl_expires_at, ssl_checked_at, in_maintenance(organization_id, id, now()), regions`

func scan(row pgx.Row, minInterval int) (Monitor, error) {
	var m Monitor
	var sslExpires, sslChecked *time.Time
	err := row.Scan(&m.ID, &m.OrganizationID, &m.Name, &m.URL, &m.Method, &m.ExpectedStatusCode, &m.CheckIntervalSeconds,
		&m.TimeoutSeconds, &m.Enabled, &m.CurrentStatus, &m.LastCheckedAt, &m.LastSuccessAt, &m.ConsecutiveFailures,
		&m.ConsecutiveSuccesses, &m.CreatedAt, &m.UpdatedAt, &m.Headers, &m.Body, &m.Auth.Type, &m.Auth.Username,
		&m.Auth.HeaderName, &m.Auth.SecretSet, &m.Assertions, &sslExpires, &sslChecked, &m.InMaintenance, &m.Regions)
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

// fields are the validated, normalised editable values of a monitor.
type fields struct {
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
	if f.interval < MinIntervalSeconds || f.interval > MaxIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be between %d and %d", MinIntervalSeconds, MaxIntervalSeconds)
	}
	if f.timeout < 1 || f.timeout > 30 {
		bad["timeout_seconds"] = "must be between 1 and 30"
	}
	if IsWriteMethod(f.method) && f.interval < MinWriteIntervalSeconds {
		bad["check_interval_seconds"] = fmt.Sprintf("must be at least %d minutes for %s, because the request is repeated on every check", MinWriteIntervalSeconds/60, f.method)
	}
	f.validateRequest(bad)
	if len(bad) > 0 {
		return &validation.Error{Fields: bad}
	}
	return nil
}

// lockOrg serialises monitor creation per organization so concurrent requests cannot both
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
		f := fields{name: in.Name, url: in.URL, method: "GET", expected: in.ExpectedStatusCode,
			interval: minInterval, timeout: DefaultTimeoutSeconds}
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
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
				return err
			}
		}
		if err := s.plans.CheckRegions(ctx, tx, orgID, len(f.regions)); err != nil {
			return err
		}
		id := uuid.New() // chosen here so the credential can be encrypted for exactly this monitor
		sealed, err := s.sealSecret(f.auth.Secret, id)
		if err != nil {
			return fmt.Errorf("protect credentials: %w", err)
		}
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO monitors
			(id, organization_id, name, url, method, expected_status_code, check_interval_seconds, timeout_seconds,
			 request_headers, request_body, auth_type, auth_username, auth_header_name, auth_secret, assertions, regions)
			VALUES ($15,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),$14,$16) RETURNING `+cols,
			orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout,
			f.headers, f.body, f.auth.Type, f.auth.Username, f.auth.HeaderName, sealed, f.assertions, id, f.regions), minInterval)
		return err
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
	return m, err
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
	return out, total, rows.Err()
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
		f := fields{name: cur.Name, url: cur.URL, method: cur.Method, expected: cur.ExpectedStatusCode,
			interval: cur.CheckIntervalSeconds, timeout: cur.TimeoutSeconds, headers: slices.Clone(cur.Headers),
			body: cur.Body, assertions: slices.Clone(cur.Assertions), regions: slices.Clone(cur.Regions),
			auth: checks.Auth{Type: cur.Auth.Type, Username: cur.Auth.Username, HeaderName: cur.Auth.HeaderName}}
		f.setAuth(in.Auth, cur.Auth.Type, cur.Auth.SecretSet)
		if in.Headers != nil {
			f.headers = slices.Clone(*in.Headers)
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
		if err := f.validate(); err != nil {
			return err
		}
		// Only a requested interval change is checked: after a downgrade, unrelated edits still work.
		if in.CheckIntervalSeconds != nil {
			if err := s.plans.CheckInterval(ctx, tx, orgID, f.interval); err != nil {
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
		// A changed request (target, headers, body or credentials) invalidates the previous health state.
		reset := f.url != cur.URL || f.method != cur.Method || !slices.Equal(f.headers, cur.Headers) ||
			!equalPtr(f.body, cur.Body) || f.auth.Type != cur.Auth.Type || f.auth.Username != cur.Auth.Username || f.auth.HeaderName != cur.Auth.HeaderName ||
			f.auth.Secret != ""
		sealed, err := s.sealSecret(f.auth.Secret, id)
		if err != nil {
			return fmt.Errorf("protect credentials: %w", err)
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
			assertions=$17, regions=$19,
			ssl_expires_at = CASE WHEN $18 THEN NULL ELSE ssl_expires_at END, ssl_checked_at = CASE WHEN $18 THEN NULL ELSE ssl_checked_at END,
			ssl_notified_bucket = CASE WHEN $18 THEN NULL ELSE ssl_notified_bucket END, ssl_notified_for = CASE WHEN $18 THEN NULL ELSE ssl_notified_for END
			WHERE id = $1 AND organization_id = $2 RETURNING `+cols,
			id, orgID, f.name, f.url, f.method, f.expected, f.interval, f.timeout, reset,
			f.headers, f.body, f.auth.Type, f.auth.Username, f.auth.HeaderName, f.keepSecret, sealed, f.assertions, f.url != cur.URL, f.regions), minInterval)
		if err != nil {
			return err
		}
		// Streaks recorded by each region no longer describe the monitor after its request or its regions changed.
		if reset || regionsChanged {
			_, err = tx.Exec(ctx, `DELETE FROM monitor_region_state WHERE monitor_id = $1`, id)
		}
		return err
	})
	return out, err
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// SetEnabled pauses or resumes a monitor.
func (s *Service) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool) (Monitor, error) {
	min, err := s.plans.GetMinimumCheckInterval(ctx, s.pool, orgID)
	if err != nil {
		return Monitor{}, err
	}
	// Resuming makes the monitor due immediately instead of waiting out a stale schedule.
	m, err := scan(s.pool.QueryRow(ctx, `UPDATE monitors SET enabled = $3,
		next_check_at = CASE WHEN $3 AND NOT enabled THEN now() ELSE next_check_at END
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL RETURNING `+cols, id, orgID, enabled), min)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	return m, err
}

// Delete soft-deletes a monitor: it stops counting toward limits and is no longer checked.
// An open incident is closed without notifications: nobody needs a recovery email for a monitor they removed.
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// A deleted monitor keeps its history for a while, but never its credentials, headers or request body.
		tag, err := tx.Exec(ctx, `UPDATE monitors SET deleted_at = now(), enabled = false,
			auth_type = 'none', auth_secret = NULL, auth_username = NULL, auth_header_name = NULL,
			request_headers = '[]', request_body = NULL
			WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, id, orgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE incidents SET status = 'resolved', resolved_at = GREATEST(started_at, now())
			WHERE monitor_id = $1 AND status = 'open'`, id)
		return err
	})
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
