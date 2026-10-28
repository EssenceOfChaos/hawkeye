// Package organizations implements tenant access. Every query is scoped by the caller's
// membership, so an organization the user does not belong to is indistinguishable from a missing one.
package organizations

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/validation"
)

const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

var (
	ErrNotFound  = errors.New("organization not found")
	ErrForbidden = errors.New("insufficient role")
	// ErrMemberNotFound: the person is not a member of this organization.
	ErrMemberNotFound = errors.New("member not found")
	// ErrOwnerProtected: an owner can neither be removed nor leave. An organization always has its owner, who is the one billed and who alone can
	// delete it by deleting their account.
	ErrOwnerProtected = errors.New("the owner cannot be removed")
)

type Organization struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"` // the caller's role
	CreatedAt time.Time `json:"created_at"`
}

type Member struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const orgSelect = `SELECT o.id, o.name, m.role, o.created_at
	FROM organizations o JOIN organization_members m ON m.organization_id = o.id`

func scanOrg(row pgx.Row) (Organization, error) {
	var o Organization
	err := row.Scan(&o.ID, &o.Name, &o.Role, &o.CreatedAt)
	return o, err
}

func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, orgSelect+` WHERE m.user_id = $1 ORDER BY o.created_at, o.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Organization{}
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, userID, orgID uuid.UUID) (Organization, error) {
	o, err := scanOrg(s.pool.QueryRow(ctx, orgSelect+` WHERE m.user_id = $1 AND o.id = $2`, userID, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	return o, err
}

// Rename changes the organization name. Only owners may do so.
func (s *Service) Rename(ctx context.Context, userID, orgID uuid.UUID, name string) (Organization, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
		return Organization{}, validation.New("name", "must be between 1 and 100 characters")
	}
	o, err := s.Get(ctx, userID, orgID)
	if err != nil {
		return Organization{}, err
	}
	if o.Role != RoleOwner {
		return Organization{}, ErrForbidden
	}
	if _, err := s.pool.Exec(ctx, `UPDATE organizations SET name = $2 WHERE id = $1`, orgID, name); err != nil {
		return Organization{}, err
	}
	o.Name = name
	return o, nil
}

func (s *Service) Members(ctx context.Context, userID, orgID uuid.UUID) ([]Member, error) {
	if _, err := s.Get(ctx, userID, orgID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT u.id, u.email, m.role, m.created_at
		FROM organization_members m JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 ORDER BY m.created_at, u.id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RemoveMember takes a member out of the organization. Only the owner may, and the owner cannot be removed. The person keeps their own account and
// their own organization; they simply stop seeing this one, from their next request on, because every request checks membership.
func (s *Service) RemoveMember(ctx context.Context, actorID, orgID, memberID uuid.UUID) error {
	o, err := s.Get(ctx, actorID, orgID)
	if err != nil {
		return err
	}
	if o.Role != RoleOwner {
		return ErrForbidden
	}
	return s.remove(ctx, orgID, memberID)
}

// Leave removes the caller from an organization they were invited to.
func (s *Service) Leave(ctx context.Context, userID, orgID uuid.UUID) error {
	if _, err := s.Get(ctx, userID, orgID); err != nil {
		return err
	}
	return s.remove(ctx, orgID, userID)
}

func (s *Service) remove(ctx context.Context, orgID, memberID uuid.UUID) error {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM organization_members WHERE organization_id = $1 AND user_id = $2`, orgID, memberID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMemberNotFound
	}
	if err != nil {
		return err
	}
	if role == RoleOwner {
		return ErrOwnerProtected
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2 AND role <> $3`, orgID, memberID, RoleOwner)
	return err
}
