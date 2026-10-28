// Package invitations lets an organization's owner bring colleagues in. An invitation is an emailed link bound to one address: whoever follows it must
// be signed in with a verified account that has that address, and joins the organization as a member.
//
// A row exists only while an invitation is pending. Accepting or revoking it deletes the row, and one past its expiry is ignored (and replaced if the same
// address is invited again), so there is no status to keep in step. A pending invitation holds a seat, so an owner cannot send more invitations than the
// plan has seats. Owners cannot be invited or demoted: an organization has the one owner it was created with.
package invitations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
)

// TTL is how long an invitation link works. A week is long enough to survive a holiday and short enough that an old email is not a standing key.
const TTL = 7 * 24 * time.Hour

var (
	// ErrNotFound: no such pending invitation in this organization.
	ErrNotFound = errors.New("invitation not found")
	// ErrInvalidToken: the link is not one we issued, was already used or revoked, or has expired. The cases are not told apart.
	ErrInvalidToken = errors.New("invalid or expired invitation")
	// ErrAlreadyMember: the address already belongs to a member of the organization.
	ErrAlreadyMember = errors.New("already a member of this organization")
	// ErrWrongAccount: the signed-in account's address is not the one the invitation was sent to.
	ErrWrongAccount = errors.New("invitation was sent to a different address")
	// ErrEmailNotVerified: the account has the invited address but has not confirmed it yet.
	ErrEmailNotVerified = errors.New("email address not verified")
	// ErrSendFailed: the invitation was not saved because the email could not be handed to the mail server.
	ErrSendFailed = errors.New("invitation email could not be sent")
)

// Invitation is a pending invitation as the organization's owner sees it.
type Invitation struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Preview is what the person holding a link may see before signing in: which organization it is for and which address it was sent to.
type Preview struct {
	Organization string `json:"organization_name"`
	Email        string `json:"email"`
}

type Service struct {
	pool    *pgxpool.Pool
	plans   *plans.Service
	mail    email.Sender
	log     *slog.Logger
	baseURL string
}

func NewService(pool *pgxpool.Pool, planSvc *plans.Service, mail email.Sender, log *slog.Logger, appBaseURL string) *Service {
	return &Service{pool: pool, plans: planSvc, mail: mail, log: log, baseURL: appBaseURL}
}

// newToken returns a random URL-safe token. Like every emailed token it has 256 bits, and only its hash is stored.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// lockOrganization serialises changes to an organization's seats, so two owners' tabs cannot both take the last one.
func lockOrganization(ctx context.Context, tx pgx.Tx, orgID uuid.UUID) (name string, err error) {
	err = tx.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1 FOR UPDATE`, orgID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", organizations.ErrNotFound
	}
	return name, err
}

// Invite sends an invitation to address, or sends a fresh link if one is already pending (the earlier link stops working). The caller has already
// established that inviter is an owner of the organization.
func (s *Service) Invite(ctx context.Context, orgID uuid.UUID, inviter auth.User, address string) (Invitation, error) {
	addr, err := auth.NormalizeEmail(address)
	if err != nil {
		return Invitation{}, err
	}
	raw, err := newToken()
	if err != nil {
		return Invitation{}, err
	}

	var (
		inv     Invitation
		orgName string
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		orgName, err = lockOrganization(ctx, tx, orgID)
		if err != nil {
			return err
		}
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_members m JOIN users u ON u.id = m.user_id
			WHERE m.organization_id = $1 AND u.email = $2)`, orgID, addr).Scan(&member); err != nil {
			return err
		}
		if member {
			return ErrAlreadyMember
		}
		// Expired invitations hold no seat and are in the way of the unique (organization, address) row.
		if _, err := tx.Exec(ctx, `DELETE FROM organization_invitations WHERE organization_id = $1 AND expires_at <= now()`, orgID); err != nil {
			return err
		}
		// Sending the link again to someone already invited keeps their seat, so only a new address needs a free one.
		var pending bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_invitations WHERE organization_id = $1 AND email = $2)`, orgID, addr).Scan(&pending); err != nil {
			return err
		}
		if !pending {
			if err := s.plans.CanInviteTeamMember(ctx, tx, orgID); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, `INSERT INTO organization_invitations (organization_id, email, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, now() + $5::interval)
			ON CONFLICT (organization_id, email) DO UPDATE
			SET token_hash = EXCLUDED.token_hash, invited_by = EXCLUDED.invited_by, created_at = now(), expires_at = EXCLUDED.expires_at
			RETURNING id, email, created_at, expires_at`, orgID, addr, auth.HashToken(raw), inviter.ID, TTL).
			Scan(&inv.ID, &inv.Email, &inv.CreatedAt, &inv.ExpiresAt)
	})
	if err != nil {
		return Invitation{}, err
	}

	if err := s.sendInvitation(ctx, inviter.Email, orgName, addr, raw); err != nil {
		// Nobody has the link, so keeping the row would only spend a seat on an invitation no one can accept.
		if _, delErr := s.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM organization_invitations WHERE id = $1`, inv.ID); delErr != nil {
			s.log.ErrorContext(ctx, "remove unsent invitation failed", "error", delErr)
		}
		s.log.ErrorContext(ctx, "send invitation email failed", "error", err)
		return Invitation{}, ErrSendFailed
	}
	return inv, nil
}

func (s *Service) sendInvitation(ctx context.Context, inviterEmail, orgName, to, raw string) error {
	link := fmt.Sprintf("%s/accept-invite?token=%s", s.baseURL, raw)
	c := email.Content{
		Preheader: inviterEmail + " invited you to join " + orgName + " on HawkEye.",
		Heading:   "You're invited to join " + orgName,
		Paragraphs: []string{
			inviterEmail + " invited you to join their team on HawkEye, so you can see their websites, incidents and uptime together.",
			"Sign in, or create a free account with this email address (" + to + "), then accept the invitation.",
		},
		ButtonLabel: "Accept invitation", ButtonURL: link,
		Note:       "This link works for 7 days and only for " + to + ". If you weren't expecting it, you can ignore this email and nothing will change.",
		AppBaseURL: s.baseURL,
	}
	return s.mail.Send(ctx, c.Message("", to, inviterEmail+" invited you to "+orgName+" on HawkEye"))
}

// List returns the organization's pending invitations, oldest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Invitation, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, email, created_at, expires_at FROM organization_invitations
		WHERE organization_id = $1 AND expires_at > now() ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.Email, &i.CreatedAt, &i.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Resend sends a fresh link for a pending invitation. The old link stops working.
func (s *Service) Resend(ctx context.Context, orgID, id uuid.UUID, inviter auth.User) (Invitation, error) {
	var addr string
	err := s.pool.QueryRow(ctx, `SELECT email FROM organization_invitations WHERE id = $1 AND organization_id = $2`, id, orgID).Scan(&addr)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invitation{}, ErrNotFound
	}
	if err != nil {
		return Invitation{}, err
	}
	return s.Invite(ctx, orgID, inviter, addr)
}

// Revoke cancels a pending invitation, which frees its seat. Its link stops working at once.
func (s *Service) Revoke(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM organization_invitations WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Preview says which organization a link is for and which address it was sent to, so the person can sign in or register with the right one.
// It changes nothing; holding the token is what authorises it.
func (s *Service) Preview(ctx context.Context, token string) (Preview, error) {
	var p Preview
	err := s.pool.QueryRow(ctx, `SELECT o.name, i.email FROM organization_invitations i JOIN organizations o ON o.id = i.organization_id
		WHERE i.token_hash = $1 AND i.expires_at > now()`, auth.HashToken(token)).Scan(&p.Organization, &p.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrInvalidToken
	}
	return p, err
}

// Accept makes user a member of the invited organization and uses up the invitation. The account must have the invited address, and must have
// verified it: otherwise anyone who registered that address first, without owning the mailbox, could use a link that leaked.
func (s *Service) Accept(ctx context.Context, user auth.User, token string) (organizations.Organization, error) {
	var org organizations.Organization
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			id    uuid.UUID
			orgID uuid.UUID
			to    string
		)
		err := tx.QueryRow(ctx, `SELECT id, organization_id, email FROM organization_invitations
			WHERE token_hash = $1 AND expires_at > now() FOR UPDATE`, auth.HashToken(token)).Scan(&id, &orgID, &to)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		if !strings.EqualFold(to, user.Email) {
			return ErrWrongAccount // the link is not used up: the right person can still follow it
		}
		if !user.EmailVerified() {
			return ErrEmailNotVerified
		}
		if _, err := lockOrganization(ctx, tx, orgID); err != nil {
			return err
		}
		var already bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organization_members WHERE organization_id = $1 AND user_id = $2)`, orgID, user.ID).Scan(&already); err != nil {
			return err
		}
		if !already {
			// The invitation held a seat, but the plan may have shrunk since (a downgrade): then nobody new can join.
			if err := s.plans.CanAddTeamMember(ctx, tx, orgID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, $3)`, orgID, user.ID, organizations.RoleMember); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM organization_invitations WHERE id = $1`, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT o.id, o.name, m.role, o.created_at FROM organizations o
			JOIN organization_members m ON m.organization_id = o.id WHERE o.id = $1 AND m.user_id = $2`, orgID, user.ID).
			Scan(&org.ID, &org.Name, &org.Role, &org.CreatedAt)
	})
	if err != nil {
		return organizations.Organization{}, err
	}
	return org, nil
}
