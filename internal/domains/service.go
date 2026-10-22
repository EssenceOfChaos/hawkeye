package domains

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/validation"
)

var (
	ErrNotFound = errors.New("domain not found")
	// ErrInUse means a domain cannot be removed because the organization's monitors use it; it goes when they stop using it.
	ErrInUse = errors.New("domain is used by monitors")
)

// Domain is the API representation of a tracked domain.
type Domain struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Manual       bool      `json:"manual"`        // added by hand (otherwise derived from monitors)
	MonitorCount int       `json:"monitor_count"` // monitors on this domain
	// Tracked is false for domains beyond the plan's limit: they are listed but not looked up and raise no warnings.
	Tracked bool `json:"tracked"`
	// Status is pending (not looked up yet), unavailable (the registry gives no expiry date), or valid, expiring (30 days or fewer) or expired.
	Status            string     `json:"status"`
	ExpiresAt         *time.Time `json:"expires_at"`
	DaysRemaining     *int       `json:"days_remaining"` // whole days; negative once expired
	Source            *string    `json:"source"`         // rdap or whois
	UnavailableReason *string    `json:"unavailable_reason"`
	CheckedAt         *time.Time `json:"checked_at"`
	// LookupError is set while the latest lookups are failing (a registry timeout, say); the last known expiry date is still shown.
	LookupError *string   `json:"lookup_error"`
	CreatedAt   time.Time `json:"created_at"`
}

type Service struct {
	pool  *pgxpool.Pool
	plans *plans.Service
	now   func() time.Time
}

func NewService(pool *pgxpool.Pool, p *plans.Service) *Service {
	return &Service{pool: pool, plans: p, now: time.Now}
}

// monitorDomains counts the organization's live monitors per registrable domain.
func monitorDomains(ctx context.Context, db interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, orgID uuid.UUID) (map[string]int, error) {
	rows, err := db.Query(ctx, `SELECT url FROM monitors WHERE organization_id = $1 AND deleted_at IS NULL`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if name, ok := Registrable(raw); ok {
			out[name]++
		}
	}
	return out, rows.Err()
}

// SyncTx makes the organization's domains match its monitors: every domain a live monitor uses is tracked, and a domain nobody added by hand is
// dropped once no monitor uses it. The caller holds the organization row lock, so two syncs cannot interleave.
func SyncTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	used, err := monitorDomains(ctx, tx, orgID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO domains (organization_id, name) SELECT $1, unnest($2::text[]) ON CONFLICT (organization_id, name) DO NOTHING`,
		orgID, names); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM domains WHERE organization_id = $1 AND NOT manual AND NOT (name = ANY($2::text[]))`, orgID, names)
	return err
}

// Sync is SyncTx in a transaction of its own, taking the organization row lock first.
func Sync(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		return SyncTx(ctx, tx, orgID)
	})
}

func lockOrg(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

const domainCols = `d.id, d.name, d.manual, d.status, d.expires_at, d.source, d.unavailable_reason, d.checked_at,
	CASE WHEN d.failure_count > 0 THEN d.last_error END, d.created_at, row_number() OVER (ORDER BY d.created_at, d.id)`

// Listing is the organization's domains with what its plan allows.
type Listing struct {
	Domains []Domain
	Limit   int // domains the plan looks up
}

func scanDomain(row pgx.Row, used map[string]int, limit int, now time.Time) (Domain, error) {
	var d Domain
	var rank int
	if err := row.Scan(&d.ID, &d.Name, &d.Manual, &d.Status, &d.ExpiresAt, &d.Source, &d.UnavailableReason, &d.CheckedAt, &d.LookupError, &d.CreatedAt, &rank); err != nil {
		return d, err
	}
	d.MonitorCount = used[d.Name]
	d.Tracked = rank <= limit
	if d.ExpiresAt != nil {
		days := DaysRemaining(*d.ExpiresAt, now)
		d.DaysRemaining, d.Status = &days, Status(*d.ExpiresAt, now)
	}
	return d, nil
}

// List returns the organization's domains by name.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) (Listing, error) {
	p, err := s.plans.Plan(ctx, s.pool, orgID)
	if err != nil {
		return Listing{}, err
	}
	used, err := monitorDomains(ctx, s.pool, orgID)
	if err != nil {
		return Listing{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+domainCols+` FROM domains d WHERE d.organization_id = $1 ORDER BY d.name`, orgID)
	if err != nil {
		return Listing{}, err
	}
	defer rows.Close()
	out := Listing{Domains: []Domain{}, Limit: p.MaxDomains}
	now := s.now()
	for rows.Next() {
		d, err := scanDomain(rows, used, p.MaxDomains, now)
		if err != nil {
			return Listing{}, err
		}
		out.Domains = append(out.Domains, d)
	}
	return out, rows.Err()
}

// hostOf accepts what people paste: a bare name, a host with a path, or a URL.
func hostOf(input string) string {
	in := strings.TrimSpace(input)
	if !strings.Contains(in, "://") {
		in = "https://" + in
	}
	u, err := url.Parse(in)
	if err != nil || u.User != nil {
		return ""
	}
	return u.Hostname()
}

// Add starts tracking a domain by hand. The name may be any host or URL under it ("https://www.example.com/shop" tracks example.com). Adding a
// domain that is already tracked only marks it as added by hand, so it stays when the monitors using it go away; it needs no free place in the plan.
func (s *Service) Add(ctx context.Context, orgID uuid.UUID, input string) (Domain, error) {
	name, ok := Registrable(hostOf(input))
	if !ok {
		return Domain{}, validation.New("name", "must be a domain name you can register, such as example.com")
	}
	var id uuid.UUID
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `UPDATE domains SET manual = true WHERE organization_id = $1 AND name = $2 RETURNING id`, orgID, name).Scan(&id)
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := s.plans.CanAddDomain(ctx, tx, orgID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO domains (organization_id, name, manual) VALUES ($1, $2, true) RETURNING id`, orgID, name).Scan(&id)
	})
	if err != nil {
		return Domain{}, err
	}
	return s.get(ctx, orgID, id)
}

func (s *Service) get(ctx context.Context, orgID, id uuid.UUID) (Domain, error) {
	p, err := s.plans.Plan(ctx, s.pool, orgID)
	if err != nil {
		return Domain{}, err
	}
	used, err := monitorDomains(ctx, s.pool, orgID)
	if err != nil {
		return Domain{}, err
	}
	// The rank is among all of the organization's domains, so the window runs before the row is picked.
	d, err := scanDomain(s.pool.QueryRow(ctx, `SELECT * FROM (SELECT `+domainCols+` FROM domains d WHERE d.organization_id = $1) x WHERE x.id = $2`,
		orgID, id), used, p.MaxDomains, s.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNotFound
	}
	return d, err
}

// Remove stops tracking a domain that was added by hand. A domain that monitors use cannot be removed (ErrInUse); it goes when they stop using
// it. A domain added by hand that monitors also use stays, as one derived from them.
func (s *Service) Remove(ctx context.Context, orgID, id uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lockOrg(ctx, tx, orgID); err != nil {
			return err
		}
		var manual bool
		err := tx.QueryRow(ctx, `SELECT manual FROM domains WHERE id = $1 AND organization_id = $2 FOR UPDATE`, id, orgID).Scan(&manual)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !manual {
			return ErrInUse
		}
		if _, err := tx.Exec(ctx, `UPDATE domains SET manual = false WHERE id = $1`, id); err != nil {
			return err
		}
		return SyncTx(ctx, tx, orgID) // deletes it unless a monitor uses it
	})
}
