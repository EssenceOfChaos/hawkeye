// Package audit keeps the security log: a record of who did what, when, from where and with which credential.
//
// Every event belongs to one of two logs. An organization's log (changes to its monitors, status pages, maintenance windows, incident updates,
// alert recipients, API keys, connected apps and team) is read by the organization's owner. An account's log (sign-ins, failed sign-ins, password
// and two-factor changes, data exports) is read by that person. Both are shown in the app and kept for Retention, after which the retention job
// removes them (internal/retention). Rows are never changed (the database refuses UPDATE on the table).
//
// Recording is best effort, after the change has been made: a failure to write an event is logged and counted (hawkeye_audit_writes_total, with an
// alert), but never undoes or fails the action it describes. Record copies what it needs, so the request that caused the event may already be over.
// A nil *Recorder records nothing, which keeps tests and tools that don't care about the log simple.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// Retention is how long events are kept.
const Retention = 365 * 24 * time.Hour

// Who acted.
const (
	ActorUser      = "user"      // a person, signed in (or following a link only they received, such as a password reset)
	ActorAPIKey    = "api_key"   // a script or tool using a person's API key
	ActorOAuthApp  = "oauth_app" // a connected app (Claude) acting for a person
	ActorAnonymous = "anonymous" // someone not signed in, such as a failed sign-in
	ActorSystem    = "system"    // HawkEye itself
)

// Actor is who an event is attributed to. For keys and connected apps, UserID and Email are the person the credential acts for.
type Actor struct {
	Type           string
	UserID         uuid.UUID
	Email          string
	CredentialID   uuid.UUID // the API key or access token
	CredentialName string    // the key's name, or the connected app's
}

// User is a person acting for themselves.
func User(id uuid.UUID, email string) Actor { return Actor{Type: ActorUser, UserID: id, Email: email} }

// Anonymous is someone who is not signed in.
func Anonymous() Actor { return Actor{Type: ActorAnonymous} }

// Event is one thing that happened. Exactly one of OrganizationID and AccountID is set: it decides whose log the event is in.
type Event struct {
	Action         string
	OrganizationID uuid.UUID
	AccountID      uuid.UUID
	Actor          Actor
	TargetType     string // what was acted on: "monitor", "api_key", "member"...
	TargetID       string
	TargetName     string         // a readable name for the target, copied now so the log still reads well after it is deleted
	Details        map[string]any // a few facts about the change, never secrets, passwords, tokens or request bodies
}

// Request is where a request came from. The API puts it on the request's context (WithRequest), so Record can attach it without every caller
// passing it along.
type Request struct {
	IP        string
	UserAgent string
	RequestID string
}

type requestKey struct{}

// WithRequest returns ctx carrying r.
func WithRequest(ctx context.Context, r Request) context.Context {
	return context.WithValue(ctx, requestKey{}, r)
}

func requestFrom(ctx context.Context) Request {
	r, _ := ctx.Value(requestKey{}).(Request)
	return r
}

// Recorder writes and reads the log.
type Recorder struct {
	pool   *pgxpool.Pool
	log    *slog.Logger
	writes *prometheus.CounterVec // outcome: ok, error
}

func New(pool *pgxpool.Pool, log *slog.Logger) *Recorder {
	if log == nil {
		log = slog.Default()
	}
	return &Recorder{pool: pool, log: log}
}

// WithMetrics counts writes by outcome (ok, error) on c, which must have one label.
func (r *Recorder) WithMetrics(c *prometheus.CounterVec) *Recorder {
	r.writes = c
	return r
}

const writeTimeout = 5 * time.Second

var errBadEvent = errors.New("audit event needs exactly one of an organization and an account")

// Record writes e. It never fails the caller: problems are logged and counted.
func (r *Recorder) Record(ctx context.Context, e Event) {
	if r == nil {
		return
	}
	// The change has already happened: write it down even if the client has gone away or the request has run out of time.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	err := r.insert(ctx, e)
	outcome := "ok"
	if err != nil {
		outcome = "error"
		r.log.ErrorContext(ctx, "could not write to the security log", "action", e.Action, "organization_id", e.OrganizationID,
			"account_id", e.AccountID, "error", err)
	}
	if r.writes != nil {
		r.writes.WithLabelValues(outcome).Inc()
	}
}

func (r *Recorder) insert(ctx context.Context, e Event) error {
	if (e.OrganizationID == uuid.Nil) == (e.AccountID == uuid.Nil) {
		return errBadEvent
	}
	// An action unknown to this log is a bug in the caller: refuse it rather than store something the app can't describe.
	known, log := OrganizationActions, "organization"
	if e.OrganizationID == uuid.Nil {
		known, log = AccountActions, "account"
	}
	if !contains(known, e.Action) {
		return fmt.Errorf("%q is not an %s action", e.Action, log)
	}
	if e.Actor.Type == "" {
		e.Actor.Type = ActorSystem
	}
	details := []byte("{}")
	if len(e.Details) > 0 {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("details: %w", err)
		}
		details = b
	}
	req := requestFrom(ctx)
	var ip *netip.Addr
	if a, err := netip.ParseAddr(req.IP); err == nil {
		a = a.Unmap()
		ip = &a
	}
	_, err := r.pool.Exec(ctx, `INSERT INTO audit_events (organization_id, account_id, action, actor_type, actor_user_id, actor_email, credential_id,
			credential_name, target_type, target_id, target_name, details, ip, user_agent, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		nilUUID(e.OrganizationID), nilUUID(e.AccountID), e.Action, e.Actor.Type, nilUUID(e.Actor.UserID), clip(e.Actor.Email, 254),
		nilUUID(e.Actor.CredentialID), clip(e.Actor.CredentialName, 200), clip(e.TargetType, 40), clip(e.TargetID, 100), clip(e.TargetName, 300),
		details, ip, clip(req.UserAgent, 300), clip(req.RequestID, 100))
	return err
}

func nilUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// clip shortens s to at most n characters, and turns "" into NULL.
func clip(s string, n int) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return &s
}

// Entry is an event as the app shows it.
type Entry struct {
	ID         int64          `json:"id"`
	OccurredAt time.Time      `json:"occurred_at"`
	Action     string         `json:"action"`
	Actor      EntryActor     `json:"actor"`
	Target     *EntryTarget   `json:"target"`
	Details    map[string]any `json:"details"`
	IP         *string        `json:"ip"`
	UserAgent  *string        `json:"user_agent"`
	RequestID  *string        `json:"request_id"`
}

type EntryActor struct {
	Type           string  `json:"type"`
	Email          *string `json:"email"`
	CredentialName *string `json:"credential_name"`
}

type EntryTarget struct {
	Type string  `json:"type"`
	ID   *string `json:"id"`
	Name *string `json:"name"`
}

// Query selects a page of a log, newest first.
type Query struct {
	Before   int64  // only events older than this id (from the previous page's NextBefore); 0 starts at the newest
	Limit    int    // 1 to MaxLimit; 0 means DefaultLimit
	Category string // only actions in this category (the part before the dot, such as "monitor"); "" means all
}

const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Page is one page of a log. NextBefore is set when there may be more.
type Page struct {
	Events     []Entry `json:"events"`
	NextBefore *int64  `json:"next_before"`
}

// ErrBadQuery is a query the log can't answer (an unknown category, or a negative cursor).
var ErrBadQuery = errors.New("invalid security log query")

// Organization returns a page of the organization's log.
func (r *Recorder) Organization(ctx context.Context, orgID uuid.UUID, q Query) (Page, error) {
	return r.page(ctx, "organization_id", orgID, q, OrganizationCategories)
}

// Account returns a page of the person's own log.
func (r *Recorder) Account(ctx context.Context, userID uuid.UUID, q Query) (Page, error) {
	return r.page(ctx, "account_id", userID, q, AccountCategories)
}

// page reads one log. column is a constant from this file, never request data.
func (r *Recorder) page(ctx context.Context, column string, id uuid.UUID, q Query, categories []string) (Page, error) {
	if q.Before < 0 || (q.Category != "" && !contains(categories, q.Category)) {
		return Page{}, ErrBadQuery
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	before := q.Before
	if before == 0 {
		before = 1<<63 - 1
	}
	prefix := ""
	if q.Category != "" {
		prefix = q.Category + "."
	}
	rows, err := r.pool.Query(ctx, `SELECT id, occurred_at, action, actor_type, actor_email, credential_name, target_type, target_id, target_name,
			details, host(ip), user_agent, request_id
		FROM audit_events WHERE `+column+` = $1 AND id < $2 AND ($3 = '' OR starts_with(action, $3))
		ORDER BY id DESC LIMIT $4`, id, before, prefix, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	out := Page{Events: []Entry{}}
	for rows.Next() {
		var (
			e          Entry
			targetType *string
			details    []byte
		)
		e.Target = &EntryTarget{}
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Action, &e.Actor.Type, &e.Actor.Email, &e.Actor.CredentialName, &targetType, &e.Target.ID,
			&e.Target.Name, &details, &e.IP, &e.UserAgent, &e.RequestID); err != nil {
			return Page{}, err
		}
		if targetType == nil {
			e.Target = nil
		} else {
			e.Target.Type = *targetType
		}
		if err := json.Unmarshal(details, &e.Details); err != nil {
			return Page{}, fmt.Errorf("event %d details: %w", e.ID, err)
		}
		out.Events = append(out.Events, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(out.Events) > limit {
		out.Events = out.Events[:limit]
		next := out.Events[limit-1].ID
		out.NextBefore = &next
	}
	return out, nil
}

// AllForAccount is the person's whole account log, newest first (for their data export).
func (r *Recorder) AllForAccount(ctx context.Context, userID uuid.UUID) ([]Entry, error) {
	out := []Entry{}
	q := Query{Limit: MaxLimit}
	for {
		p, err := r.Account(ctx, userID, q)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Events...)
		if p.NextBefore == nil {
			return out, nil
		}
		q.Before = *p.NextBefore
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ParseQuery reads a Query from the strings of a URL query (before, limit, category).
func ParseQuery(before, limit, category string) (Query, error) {
	var q Query
	var err error
	if before != "" {
		if q.Before, err = strconv.ParseInt(before, 10, 64); err != nil || q.Before < 1 {
			return Query{}, ErrBadQuery
		}
	}
	if limit != "" {
		if q.Limit, err = strconv.Atoi(limit); err != nil || q.Limit < 1 || q.Limit > MaxLimit {
			return Query{}, ErrBadQuery
		}
	}
	q.Category = category
	return q, nil
}
