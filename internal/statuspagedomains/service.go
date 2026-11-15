package statuspagedomains

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
)

var (
	ErrNotFound = errors.New("this status page has no custom domain")
	ErrNoPage   = errors.New("status page not found")
	// ErrHostnameInUse: another status page already serves this name (verified). A pending claim elsewhere does not block anyone.
	ErrHostnameInUse = errors.New("this domain is already in use by another HawkEye status page")
	// ErrHostnameOnOtherPage: the same organization has claimed this name for one of its other pages.
	ErrHostnameOnOtherPage = errors.New("this domain is already set on another of your status pages")
)

// Record is a DNS record the owner adds.
type Record struct {
	Type  string `json:"type"`  // CNAME or TXT
	Name  string `json:"name"`  // the full name
	Value string `json:"value"` // what it holds
}

// Domain is a page's custom domain as the owner sees it.
type Domain struct {
	ID            uuid.UUID  `json:"id"`
	PageID        uuid.UUID  `json:"page_id"`
	Hostname      string     `json:"hostname"`
	State         string     `json:"state"`
	Records       []Record   `json:"records"`
	LastError     string     `json:"last_error"`
	LastCheckedAt *time.Time `json:"last_checked_at"`
	NextCheckAt   *time.Time `json:"next_check_at"`
	VerifiedAt    *time.Time `json:"verified_at"`
	FailingSince  *time.Time `json:"failing_since"`
	CreatedAt     time.Time  `json:"created_at"`

	token           string
	verifyStartedAt time.Time
}

func (d *Domain) status() Status {
	return Status{State: d.State, LastError: d.LastError, LastCheckedAt: d.LastCheckedAt, NextCheckAt: d.NextCheckAt,
		VerifyStartedAt: d.verifyStartedAt, VerifiedAt: d.VerifiedAt, FailingSince: d.FailingSince}
}

// Change is a state change a check made, for emails and the audit log.
type Change struct {
	Domain Domain
	From   string
}

// Service stores claims and applies verification results. Reserved lists HawkEye's own names (ParseHostname).
type Service struct {
	pool     *pgxpool.Pool
	plans    *plans.Service
	verifier *Verifier
	reserved []string
	now      func() time.Time
}

func NewService(pool *pgxpool.Pool, p *plans.Service, v *Verifier, reserved ...string) *Service {
	return &Service{pool: pool, plans: p, verifier: v, reserved: append([]string{v.Target}, reserved...), now: time.Now}
}

// Target is the name customers point their CNAME at.
func (s *Service) Target() string { return strings.TrimSuffix(s.verifier.Target, ".") }

const domainCols = `id, page_id, hostname, verify_token, state, COALESCE(last_error, ''), last_checked_at, next_check_at, verify_started_at,
	verified_at, failing_since, created_at`

func (s *Service) scan(row pgx.Row) (*Domain, error) {
	var d Domain
	if err := row.Scan(&d.ID, &d.PageID, &d.Hostname, &d.token, &d.State, &d.LastError, &d.LastCheckedAt, &d.NextCheckAt, &d.verifyStartedAt,
		&d.VerifiedAt, &d.FailingSince, &d.CreatedAt); err != nil {
		return nil, err
	}
	d.Records = []Record{
		{Type: "CNAME", Name: d.Hostname, Value: s.Target()},
		{Type: "TXT", Name: TXTPrefix + d.Hostname, Value: TXTValue(d.token)},
	}
	return &d, nil
}

// Get returns a page's domain, or ErrNotFound.
func (s *Service) Get(ctx context.Context, orgID, pageID uuid.UUID) (*Domain, error) {
	d, err := s.scan(s.pool.QueryRow(ctx, `SELECT `+domainCols+` FROM status_page_domains WHERE organization_id = $1 AND page_id = $2`, orgID, pageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// Claim sets the custom domain of a page. Claiming the name the page already has changes nothing (the records stay the same); a different name
// replaces the old claim, which stops being served at once, and gets a new token.
func (s *Service) Claim(ctx context.Context, orgID, pageID uuid.UUID, raw string) (*Domain, error) {
	host, err := ParseHostname(raw, s.reserved...)
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize per organization so two claims cannot both pass the limit check.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM status_pages WHERE id = $1 AND organization_id = $2)`, pageID, orgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNoPage
		}
		var current string
		err := tx.QueryRow(ctx, `SELECT hostname FROM status_page_domains WHERE page_id = $1`, pageID).Scan(&current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if current == host {
			return nil
		}
		if err := s.plans.CanAddStatusPageDomain(ctx, tx, orgID, pageID); err != nil {
			return err
		}
		var otherPage, served bool
		if err := tx.QueryRow(ctx, `SELECT
				EXISTS (SELECT 1 FROM status_page_domains WHERE organization_id = $1 AND hostname = $2 AND page_id <> $3),
				EXISTS (SELECT 1 FROM status_page_domains WHERE hostname = $2 AND state IN ('active', 'failing') AND organization_id <> $1)`,
			orgID, host, pageID).Scan(&otherPage, &served); err != nil {
			return err
		}
		if otherPage {
			return ErrHostnameOnOtherPage
		}
		if served {
			return ErrHostnameInUse
		}
		token, err := newToken()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM status_page_domains WHERE page_id = $1`, pageID); err != nil {
			return err
		}
		now := s.now()
		_, err = tx.Exec(ctx, `INSERT INTO status_page_domains (page_id, organization_id, hostname, verify_token, verify_started_at, next_check_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $5, $5)`, pageID, orgID, host, token, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, pageID)
}

// Remove deletes a page's domain. It stops being served at once, and the name is free for anyone to claim.
func (s *Service) Remove(ctx context.Context, orgID, pageID uuid.UUID) (*Domain, error) {
	d, err := s.scan(s.pool.QueryRow(ctx, `DELETE FROM status_page_domains WHERE organization_id = $1 AND page_id = $2 RETURNING `+domainCols, orgID, pageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// CheckNow verifies a page's domain at once ("Check now"). A claim that is not being checked automatically starts a new attempt (Restart).
func (s *Service) CheckNow(ctx context.Context, orgID, pageID uuid.UUID) (*Domain, *Change, error) {
	d, err := s.Get(ctx, orgID, pageID)
	if err != nil {
		return nil, nil, err
	}
	return s.check(ctx, d, true)
}

// check verifies d and stores the outcome. The lookups run outside any transaction; the update applies only if the row is still the same claim.
func (s *Service) check(ctx context.Context, d *Domain, restart bool) (*Domain, *Change, error) {
	r, err := s.verifier.Verify(ctx, d.Hostname, d.token)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	cur := d.status()
	if restart {
		cur = Restart(cur, now)
	}
	next := Next(cur, r, now)
	updated, err := s.scan(s.pool.QueryRow(ctx, `UPDATE status_page_domains SET state = $2, last_error = NULLIF($3, ''), last_checked_at = $4,
			next_check_at = $5, verify_started_at = $6, verified_at = $7, failing_since = $8
		WHERE id = $1 RETURNING `+domainCols,
		d.ID, next.State, next.LastError, next.LastCheckedAt, next.NextCheckAt, next.VerifyStartedAt, next.VerifiedAt, next.FailingSince))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound // removed or replaced while the lookups ran
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// Verified, but another organization's claim of the same name was verified first (the zone holds both tokens). Keep this one pending.
		updated, err = s.scan(s.pool.QueryRow(ctx, `UPDATE status_page_domains SET last_error = $2, last_checked_at = $3, next_check_at = $4
			WHERE id = $1 RETURNING `+domainCols, d.ID, ErrHostnameInUse.Error()+".", now, pendingNext(cur.VerifyStartedAt, now)))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
	}
	if err != nil {
		return nil, nil, err
	}
	var ch *Change
	if updated.State != d.State {
		ch = &Change{Domain: *updated, From: d.State}
	}
	return updated, ch, nil
}

// ServedPage returns the status page a host name serves, for the certificate check (Caddy's on-demand "ask") and the public page on that name.
// ok is false for any name that is not a verified, served claim.
func (s *Service) ServedPage(ctx context.Context, host string) (pageID uuid.UUID, ok bool, err error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	err = s.pool.QueryRow(ctx, `SELECT page_id FROM status_page_domains WHERE hostname = $1 AND state IN ('active', 'failing')`, host).Scan(&pageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return pageID, true, nil
}

// tokenEncoding gives lower-case letters and digits 2 to 7, which survive every DNS provider's TXT form (the migration's CHECK).
var tokenEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

func newToken() (string, error) {
	b := make([]byte, 20) // 160 bits, 32 characters
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenEncoding.EncodeToString(b), nil
}
