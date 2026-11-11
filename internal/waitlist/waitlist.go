// Package waitlist records who wants to hear when a coming-soon feature launches ("Get notified" on the landing page), so the sign-up counts show
// which feature to build first.
//
// Anyone can type any address into a public form, so an address entered while signed out is only counted once its owner confirms it from an email
// (a link valid for 48 hours). A signed-in person with a verified address is added at once. Each address is used only to announce the feature it
// signed up for; every email carries a link that removes it (remove_token), and deleting the account removes its entries. Counts are read with SQL
// (docs/operations.md#feature-waitlists).
package waitlist

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/validation"
)

// Features are the coming-soon features people can sign up for, by key, with the name emails use. Keep in step with the migration's CHECK and the
// landing page (web/src/components/ComingSoon.tsx).
var Features = map[string]string{
	"status_page_domains":     "status pages on your own domain",
	"status_page_subscribers": "email subscribers for status pages",
}

const confirmTTL = 48 * time.Hour

var ErrInvalidToken = errors.New("invalid or expired waitlist link")

// Status says what happened to a sign-up.
type Status string

const (
	Added      Status = "added"       // on the list now (a verified, signed-in address)
	CheckInbox Status = "check_email" // a confirmation email was sent (or would have been: the answer is the same either way)
	AlreadyIn  Status = "already"     // a signed-in person who was already on the list
)

type Service struct {
	pool       *pgxpool.Pool
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
}

func NewService(pool *pgxpool.Pool, mail email.Sender, log *slog.Logger, appBaseURL string) *Service {
	return &Service{pool: pool, mail: mail, log: log, appBaseURL: appBaseURL}
}

func validFeature(f string) error {
	if _, ok := Features[f]; !ok {
		return validation.New("feature", "is not a feature you can sign up for")
	}
	return nil
}

func hash(raw string) []byte {
	s := sha256.Sum256([]byte(raw))
	return s[:]
}

// JoinVerified adds a signed-in person with a verified address straight to the list.
func (s *Service) JoinVerified(ctx context.Context, feature string, u auth.User) (Status, error) {
	if err := validFeature(feature); err != nil {
		return "", err
	}
	if !u.EmailVerified() {
		return "", errors.New("address not verified")
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO waitlist_entries (feature, email, user_id, confirmed_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (feature, email) DO UPDATE SET confirmed_at = COALESCE(waitlist_entries.confirmed_at, now()), user_id = EXCLUDED.user_id,
			confirm_token_hash = NULL, confirm_expires_at = NULL
		WHERE waitlist_entries.confirmed_at IS NULL`, feature, u.Email, u.ID)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return AlreadyIn, nil
	}
	return Added, nil
}

// Join records an address typed in by someone who isn't signed in (or typed another address) and returns whether a confirmation email should be
// sent, with the link to put in it. An address that is already confirmed needs nothing; the caller answers "check your inbox" either way, so the
// form never reveals who is on a list.
func (s *Service) Join(ctx context.Context, feature, address string) (send bool, err error) {
	if err := validFeature(feature); err != nil {
		return false, err
	}
	addr, err := auth.NormalizeEmail(address)
	if err != nil {
		return false, err
	}
	// Unconfirmed sign-ups don't linger: anything not confirmed within the link's life is dropped.
	if _, err := s.pool.Exec(ctx, `DELETE FROM waitlist_entries WHERE confirmed_at IS NULL AND confirm_expires_at < now()`); err != nil {
		return false, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return false, err
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	var removeToken uuid.UUID
	err = s.pool.QueryRow(ctx, `INSERT INTO waitlist_entries (feature, email, confirm_token_hash, confirm_expires_at) VALUES ($1, $2, $3, now() + $4::interval)
		ON CONFLICT (feature, email) DO UPDATE SET confirm_token_hash = EXCLUDED.confirm_token_hash, confirm_expires_at = EXCLUDED.confirm_expires_at
		WHERE waitlist_entries.confirmed_at IS NULL
		RETURNING remove_token`, feature, addr, hash(raw), confirmTTL).Scan(&removeToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // already confirmed: nothing to send
	}
	if err != nil {
		return false, err
	}
	s.sendConfirmation(ctx, addr, feature, raw, removeToken)
	return true, nil
}

// Confirm marks an address confirmed from its email link and returns the feature's name.
func (s *Service) Confirm(ctx context.Context, token string) (string, error) {
	var feature string
	err := s.pool.QueryRow(ctx, `UPDATE waitlist_entries SET confirmed_at = now(), confirm_token_hash = NULL, confirm_expires_at = NULL
		WHERE confirm_token_hash = $1 AND confirm_expires_at > now() AND confirmed_at IS NULL RETURNING feature`, hash(token)).Scan(&feature)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	return Features[feature], nil
}

// Leave removes an address from a list from the link in any waitlist email. Unknown links are not an error: the address is off the list either way.
func (s *Service) Leave(ctx context.Context, token string) error {
	id, err := uuid.Parse(token)
	if err != nil {
		return nil
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM waitlist_entries WHERE remove_token = $1`, id)
	return err
}

// Entry is one list an address is on, for the account export.
type Entry struct {
	Feature   string     `json:"feature"`
	Confirmed *time.Time `json:"confirmed_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// ForAddress lists the waitlists an address is on.
func ForAddress(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, address string) ([]Entry, error) {
	rows, err := q.Query(ctx, `SELECT feature, confirmed_at, created_at FROM waitlist_entries WHERE email = lower($1) ORDER BY created_at`, address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Feature, &e.Confirmed, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) sendConfirmation(ctx context.Context, to, feature, raw string, removeToken uuid.UUID) {
	name := Features[feature]
	c := email.Content{
		Preheader: "Confirm you'd like to hear when HawkEye launches " + name + ".", Heading: "Confirm your spot on the list",
		Paragraphs: []string{fmt.Sprintf("Someone, probably you, asked to be told when HawkEye launches %s. Confirm below and we'll email you once, "+
			"when it's ready. We won't use this address for anything else.", name)},
		ButtonLabel: "Confirm", ButtonURL: fmt.Sprintf("%s/waitlist/confirm?token=%s", s.appBaseURL, raw),
		Note: fmt.Sprintf("If you didn't ask, ignore this email and you won't hear from us. To take this address off the list now: %s/waitlist/leave?token=%s",
			s.appBaseURL, removeToken),
		AppBaseURL: s.appBaseURL,
	}
	if err := s.mail.Send(ctx, c.Message("", to, "Confirm: tell me when HawkEye launches "+name)); err != nil {
		s.log.ErrorContext(ctx, "send waitlist confirmation failed", "feature", feature, "error", err)
	}
}
