package notifications

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/audit"
	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/secrets"
	"hawkeye/internal/validation"
)

const MaxSettingsPerOrganization = 10

var (
	ErrNotFound     = errors.New("notification setting not found")
	ErrDuplicate    = errors.New("destination already configured")
	ErrTooMany      = errors.New("too many notification destinations")
	ErrInvalidToken = errors.New("invalid or expired token")
	// ErrNotVerified means an email recipient has not confirmed its address yet, so it may not be sent a test: a test must never become a way to
	// email an address that did not agree to receive anything from this account.
	ErrNotVerified = errors.New("confirm this address first; it has not been verified yet")
	// ErrTestRateLimited means a test was sent to this address too recently.
	ErrTestRateLimited = errors.New("a test alert was sent to this address recently")
)

type Setting struct {
	ID          uuid.UUID  `json:"id"`
	UserID      *uuid.UUID `json:"user_id"`
	Channel     string     `json:"channel"`
	Destination string     `json:"destination"`
	Enabled     bool       `json:"enabled"`
	Verified    bool       `json:"verified"`
	// Which monitors it gets alerts for: "all" (every monitor, and account-wide alerts) or "tags" (only monitors carrying one of TagIDs).
	MonitorScope string      `json:"monitor_scope"`
	TagIDs       []uuid.UUID `json:"tag_ids"`
	CreatedAt    time.Time   `json:"created_at"`
}

// Actor is the authenticated user performing an action.
type Actor struct {
	ID    uuid.UUID
	Email string
}

type Service struct {
	pool       *pgxpool.Pool
	mail       email.Sender
	log        *slog.Logger
	appBaseURL string
	from       string
	tokenTTL   time.Duration
	box        *secrets.Box // seals webhook and chat URLs; without it, adding one fails rather than storing a URL in clear
	http       *http.Client
	bot        *TelegramBot // HawkEye's own Telegram bot; nil when not configured
	audit      *audit.Recorder
}

type Option func(*Service)

// WithAudit records chats connected and disconnected through HawkEye's Telegram bot in the security log (everything else about alert destinations
// is recorded by the API's handlers).
func WithAudit(r *audit.Recorder) Option { return func(s *Service) { s.audit = r } }

// WithTelegramBot enables connecting Telegram chats through HawkEye's own bot.
func WithTelegramBot(b *TelegramBot) Option { return func(s *Service) { s.bot = b } }

// TelegramBotName is the @name of HawkEye's bot (without the @), or "" when chats cannot be connected through it right now.
func (s *Service) TelegramBotName() string { return s.bot.Username() }

// WithSecretBox sets the key that seals webhook and chat URLs and signing secrets.
func WithSecretBox(b *secrets.Box) Option { return func(s *Service) { s.box = b } }

// WithHTTPClient replaces the client used to send test messages (tests use a local server).
func WithHTTPClient(c *http.Client) Option { return func(s *Service) { s.http = c } }

func NewService(pool *pgxpool.Pool, mail email.Sender, log *slog.Logger, appBaseURL, from string, opts ...Option) *Service {
	s := &Service{pool: pool, mail: mail, log: log, appBaseURL: appBaseURL, from: from, tokenTTL: 48 * time.Hour, http: NewHTTPClient()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// For webhook and chat destinations the stored destination is sealed, so what is shown is the masked label.
const cols = `id, user_id, channel, COALESCE(label, destination), enabled, verified_at IS NOT NULL, created_at, monitor_scope,
	COALESCE((SELECT array_agg(tag_id ORDER BY tag_id) FROM notification_setting_tags WHERE setting_id = notification_settings.id), '{}')`

func scan(row pgx.Row) (Setting, error) {
	var s Setting
	err := row.Scan(&s.ID, &s.UserID, &s.Channel, &s.Destination, &s.Enabled, &s.Verified, &s.CreatedAt, &s.MonitorScope, &s.TagIDs)
	return s, err
}

// Monitor scopes.
const (
	ScopeAll  = "all"
	ScopeTags = "tags"
)

// SetRouting chooses which monitors a destination gets alerts for: every monitor (ScopeAll), or only those carrying one of tagIDs (ScopeTags; at least
// one tag, all of them the organization's own).
func (s *Service) SetRouting(ctx context.Context, orgID, id uuid.UUID, scope string, tagIDs []uuid.UUID) (Setting, error) {
	ids := []uuid.UUID{}
	for _, t := range tagIDs {
		if !slices.Contains(ids, t) {
			ids = append(ids, t)
		}
	}
	switch scope {
	case ScopeAll:
		ids = []uuid.UUID{}
	case ScopeTags:
		if len(ids) == 0 {
			return Setting{}, validation.New("tag_ids", "choose at least one tag, or send alerts for all monitors")
		}
	default:
		return Setting{}, validation.New("monitor_scope", "must be all or tags")
	}
	var st Setting
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM tags WHERE organization_id = $1 AND id = ANY($2)`, orgID, ids).Scan(&n); err != nil {
			return err
		}
		if n != len(ids) {
			return validation.New("tag_ids", "contains a tag that does not exist")
		}
		tag, err := tx.Exec(ctx, `UPDATE notification_settings SET monitor_scope = $3 WHERE id = $1 AND organization_id = $2`, id, orgID, scope)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM notification_setting_tags WHERE setting_id = $1 AND NOT (tag_id = ANY($2))`, id, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO notification_setting_tags (setting_id, tag_id) SELECT $1, unnest($2::uuid[]) ON CONFLICT DO NOTHING`, id, ids); err != nil {
			return err
		}
		st, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM notification_settings WHERE id = $1`, id))
		return err
	})
	return st, err
}

// UncoveredMonitor is a monitor whose alerts no destination is routed to.
type UncoveredMonitor struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Coverage lists the organization's monitors that routing sends to no destination (so their alerts would use the fallback, see routing.go), and
// whether a fallback address exists at all. With fallback false, alerts for those monitors would reach nobody.
func (s *Service) Coverage(ctx context.Context, orgID uuid.UUID) (uncovered []UncoveredMonitor, fallback bool, err error) {
	rows, err := s.pool.Query(ctx, `SELECT m.id, m.name FROM monitors m
		WHERE m.organization_id = $1 AND m.deleted_at IS NULL AND NOT EXISTS (
			SELECT 1 FROM notification_settings s WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL
				AND (s.monitor_scope = 'all' OR EXISTS (SELECT 1 FROM notification_setting_tags nt JOIN monitor_tags mt ON mt.tag_id = nt.tag_id
					WHERE nt.setting_id = s.id AND mt.monitor_id = m.id)))
		ORDER BY lower(m.name), m.id`, orgID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	uncovered = []UncoveredMonitor{}
	for rows.Next() {
		var u UncoveredMonitor
		if err := rows.Scan(&u.ID, &u.Name); err != nil {
			return nil, false, err
		}
		uncovered = append(uncovered, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	err = s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM notification_settings s JOIN organization_members om ON om.organization_id = s.organization_id AND om.user_id = s.user_id
		WHERE s.organization_id = $1 AND s.enabled AND s.verified_at IS NOT NULL AND s.channel = 'email')`, orgID).Scan(&fallback)
	return uncovered, fallback, err
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Setting, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM notification_settings WHERE organization_id = $1 ORDER BY created_at, id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		st, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Create adds an email destination. The caller's own address is trusted (their email is already verified);
// any other address receives a confirmation link and gets no alerts until it is confirmed, so the product
// cannot be used to send alert mail to people who never agreed to it.
func (s *Service) Create(ctx context.Context, orgID uuid.UUID, actor Actor, destination string) (Setting, error) {
	dest, err := auth.NormalizeEmail(destination)
	if err != nil {
		return Setting{}, err
	}
	own := dest == actor.Email

	var st Setting
	var rawToken string
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Serialize per organization so the count check cannot be raced.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxSettingsPerOrganization {
			return ErrTooMany
		}
		var userID *uuid.UUID
		if own {
			userID = &actor.ID
		}
		st, err = scan(tx.QueryRow(ctx, `INSERT INTO notification_settings (organization_id, user_id, channel, destination, verified_at)
			VALUES ($1, $2, 'email', $3, CASE WHEN $4 THEN now() END) RETURNING `+cols, orgID, userID, dest, own))
		if err != nil {
			return err
		}
		if own {
			return nil
		}
		var hash []byte
		rawToken, hash = newToken()
		_, err = tx.Exec(ctx, `INSERT INTO notification_setting_tokens (setting_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, st.ID, hash, s.tokenTTL)
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Setting{}, ErrDuplicate
		}
		return Setting{}, err
	}
	if rawToken != "" {
		s.sendConfirmation(ctx, dest, actor.Email, rawToken)
	}
	return st, nil
}

// SetEnabled turns alerts to a destination on or off. With onlyUser set (a member rather than the owner), only that person's own address may be changed;
// any other destination is ErrNotFound.
func (s *Service) SetEnabled(ctx context.Context, orgID, id uuid.UUID, enabled bool, onlyUser *uuid.UUID) (Setting, error) {
	st, err := scan(s.pool.QueryRow(ctx, `UPDATE notification_settings SET enabled = $3
		WHERE id = $1 AND organization_id = $2 AND ($4::uuid IS NULL OR user_id = $4) RETURNING `+cols, id, orgID, enabled, onlyUser))
	if errors.Is(err, pgx.ErrNoRows) {
		return Setting{}, ErrNotFound
	}
	return st, err
}

// CreateURL adds a webhook or chat (Slack, Discord, Teams, Telegram) destination. A test message is delivered first and the
// destination is only saved (already verified) if the receiver accepts it, so a typo or a revoked URL is reported immediately.
// The URL is sealed before storage. For webhooks the returned signing secret is shown to the caller exactly once; it cannot be
// read back. destination is a URL, except for Telegram, where it is the chat ID and botToken is the bot's token.
func (s *Service) CreateURL(ctx context.Context, orgID uuid.UUID, channel, destination, botToken string) (st Setting, signingSecret string, err error) {
	u, label, err := ParseDestination(channel, destination, botToken)
	if err != nil {
		return Setting{}, "", err
	}
	id := uuid.New()
	sealedURL, err := s.box.Seal(u.String(), id.String())
	if err != nil {
		return Setting{}, "", err
	}
	var sealedSecret *string
	if channel == ChannelWebhook {
		signingSecret = NewSigningSecret()
		v, err := s.box.Seal(signingSecret, id.String())
		if err != nil {
			return Setting{}, "", err
		}
		sealedSecret = &v
	}
	if err := s.sendTest(ctx, channel, u.String(), signingSecret); err != nil {
		return Setting{}, "", validation.New("destination", "the test message was not delivered ("+testReason(channel, err)+")")
	}

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxSettingsPerOrganization {
			return ErrTooMany
		}
		st, err = scan(tx.QueryRow(ctx, `INSERT INTO notification_settings (id, organization_id, channel, destination, label, fingerprint, signing_secret, verified_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now()) RETURNING `+cols, id, orgID, channel, sealedURL, label, Fingerprint(u), sealedSecret))
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Setting{}, "", ErrDuplicate
		}
		return Setting{}, "", err
	}
	return st, signingSecret, nil
}

// Test sends a test message to an existing destination: a test message to a webhook or chat URL, or the test alert email to an email
// recipient. requestedBy (the signed-in user's address) is named in the email. allowEmail, when set, is asked before an email is sent (the
// per-address rate limit lives in the HTTP layer); returning false gives ErrTestRateLimited.
func (s *Service) Test(ctx context.Context, orgID, id uuid.UUID, requestedBy string, allowEmail func(address string) bool) error {
	var channel, dest string
	var verified, hawkeyeBot bool
	var sealedSecret *string
	err := s.pool.QueryRow(ctx, `SELECT channel, destination, signing_secret, verified_at IS NOT NULL, hawkeye_bot FROM notification_settings
		WHERE id = $1 AND organization_id = $2`, id, orgID).Scan(&channel, &dest, &sealedSecret, &verified, &hawkeyeBot)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !verified {
		return ErrNotVerified
	}
	if channel == ChannelEmail {
		if allowEmail != nil && !allowEmail(dest) {
			return ErrTestRateLimited
		}
		msg := Render(Alert{Event: EventTest, To: dest, From: s.from, AppBaseURL: s.appBaseURL, RequestedBy: requestedBy})
		if err := s.mail.Send(ctx, msg); err != nil {
			s.log.WarnContext(ctx, "test alert email was not sent", "error", err)
			return validation.New("destination", "the test email could not be sent right now; try again in a few minutes")
		}
		return nil
	}
	if hawkeyeBot {
		if s.bot == nil {
			return ErrTelegramUnavailable
		}
		if err := s.sendTest(ctx, channel, s.bot.sendURL(dest), ""); err != nil {
			return validation.New("destination", "the test message was not delivered ("+testReason(channel, err)+")")
		}
		return nil
	}
	target, err := s.box.Open(dest, id.String())
	if err != nil {
		return err
	}
	secret := ""
	if sealedSecret != nil {
		if secret, err = s.box.Open(*sealedSecret, id.String()); err != nil {
			return err
		}
	}
	if err := s.sendTest(ctx, channel, target, secret); err != nil {
		return validation.New("destination", "the test message was not delivered ("+testReason(channel, err)+")")
	}
	return nil
}

func (s *Service) sendTest(ctx context.Context, channel, target, secret string) error {
	a := Alert{Event: EventTest, AppBaseURL: s.appBaseURL}
	to, body, header, err := buildRequest(channel, target, a, secret, "", time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, postTimeout)
	defer cancel()
	return post(ctx, s.http, to, body, header)
}

// testReason is the part of a delivery error that is safe and useful to show the user. Telegram's statuses map to the usual
// set-up mistakes, so those get a hint.
func testReason(channel string, err error) string {
	reason := clean(err.Error())
	var se statusError
	if channel != ChannelTelegram || !errors.As(err, &se) {
		return reason
	}
	switch se.code {
	case http.StatusBadRequest:
		return reason + "; check the chat ID, and that the bot has been added to that chat"
	case http.StatusUnauthorized, http.StatusNotFound:
		return reason + "; check the bot token"
	case http.StatusForbidden:
		return reason + "; the bot is not allowed to post in that chat (add it to the group, or as an admin of the channel)"
	}
	return reason
}

// Delete removes a destination. With onlyUser set (a member rather than the owner), only that person's own address may be removed.
func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID, onlyUser *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM notification_settings WHERE id = $1 AND organization_id = $2 AND ($3::uuid IS NULL OR user_id = $3)`, id, orgID, onlyUser)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResendConfirmation issues a fresh confirmation link for an unverified destination (older links stop working).
func (s *Service) ResendConfirmation(ctx context.Context, orgID, id uuid.UUID, actor Actor) error {
	var dest string
	var verified bool
	err := s.pool.QueryRow(ctx, `SELECT destination, verified_at IS NOT NULL FROM notification_settings
		WHERE id = $1 AND organization_id = $2`, id, orgID).Scan(&dest, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil || verified {
		return err
	}
	raw, hash := newToken()
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE notification_setting_tokens SET used_at = now() WHERE setting_id = $1 AND used_at IS NULL`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO notification_setting_tokens (setting_id, token_hash, expires_at)
			VALUES ($1, $2, now() + $3::interval)`, id, hash, s.tokenTTL)
		return err
	})
	if err != nil {
		return err
	}
	s.sendConfirmation(ctx, dest, actor.Email, raw)
	return nil
}

// Confirm consumes a confirmation token (clicked from the destination's mailbox; no login required).
func (s *Service) Confirm(ctx context.Context, token string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var settingID uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE notification_setting_tokens SET used_at = now()
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING setting_id`, hashToken(token)).Scan(&settingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE notification_settings SET verified_at = COALESCE(verified_at, now()) WHERE id = $1`, settingID)
		return err
	})
}

func (s *Service) sendConfirmation(ctx context.Context, to, requestedBy, raw string) {
	link := fmt.Sprintf("%s/confirm-alert-address?token=%s", s.appBaseURL, raw)
	c := email.Content{
		Preheader: "Confirm this address to receive HawkEye alerts.", Heading: "Confirm your alert address",
		Paragraphs:  []string{fmt.Sprintf("%s asked to send HawkEye website-outage alerts to this address. To start receiving them, confirm below.", clean(requestedBy))},
		ButtonLabel: "Confirm alert address", ButtonURL: link,
		Note: "If you did not expect this, ignore this email: no alerts will be sent.", AppBaseURL: s.appBaseURL,
	}
	err := s.mail.Send(ctx, c.Message(s.from, to, "Confirm your HawkEye alert address"))
	if err != nil {
		s.log.ErrorContext(ctx, "send alert-address confirmation failed", "error", err)
	}
}

func newToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system CSPRNG failing is unrecoverable
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw)
}

func hashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
