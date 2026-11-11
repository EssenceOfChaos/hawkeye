package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"hawkeye/internal/audit"
)

// HawkEye's own Telegram bot. A customer connects a chat with two taps: the app gives them a t.me link carrying a one-time code, Telegram opens
// the chat with the bot (or a group picker), and the bot's webhook receives "/start <code>" from that chat. The customer never sees a token or a
// chat ID. Customers who prefer their own bot use CreateURL with its token instead.

const (
	telegramAPI         = "https://api.telegram.org"
	telegramCodeTTL     = 15 * time.Minute
	telegramSetupPause  = time.Minute
	TelegramWebhookPath = "/api/v1/webhooks/telegram"
)

// ErrTelegramUnavailable means HawkEye's bot is not configured, or has not yet been reached at startup.
var ErrTelegramUnavailable = errors.New("connecting Telegram is not available right now; try again in a few minutes, or use your own bot")

// TelegramBot is HawkEye's own bot: its token, the secret Telegram sends back on every webhook call, and its username (learned from Telegram).
type TelegramBot struct {
	token  string
	secret string
	api    string
	http   *http.Client

	mu       sync.RWMutex
	username string
}

// NewTelegramBot returns the bot. api and client are for tests; empty and nil mean Telegram itself and a plain client with a timeout.
func NewTelegramBot(token, secret, api string, client *http.Client) *TelegramBot {
	if api == "" {
		api = telegramAPI
	}
	if client == nil {
		client = &http.Client{Timeout: postTimeout}
	}
	return &TelegramBot{token: token, secret: secret, api: api, http: client}
}

// Username is the bot's @name without the @, or "" until Setup has succeeded.
func (b *TelegramBot) Username() string {
	if b == nil {
		return ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.username
}

// CheckSecret reports whether a webhook request carries the secret this bot registered (header X-Telegram-Bot-Api-Secret-Token).
func (b *TelegramBot) CheckSecret(got string) bool {
	return b != nil && subtle.ConstantTimeCompare([]byte(got), []byte(b.secret)) == 1
}

// sendURL is the Bot API URL that sends to a chat, in the same shape as a customer's own-bot destination.
func (b *TelegramBot) sendURL(chatID string) string {
	return b.api + "/bot" + b.token + "/sendMessage?" + url.Values{"chat_id": {chatID}}.Encode()
}

// Setup learns the bot's username and, when webhookURL is public HTTPS, points Telegram's updates at it and sets the bot's command menu.
// Every call is idempotent, so every API process may run it.
func (b *TelegramBot) Setup(ctx context.Context, webhookURL string) error {
	var me struct {
		Username string `json:"username"`
	}
	if err := b.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return err
	}
	if strings.HasPrefix(webhookURL, "https://") {
		if err := b.call(ctx, "setWebhook", map[string]any{
			"url": webhookURL, "secret_token": b.secret, "allowed_updates": []string{"message", "channel_post", "my_chat_member"},
		}, nil); err != nil {
			return err
		}
		if err := b.call(ctx, "setMyCommands", map[string]any{"commands": []map[string]string{
			{"command": "start", "description": "Connect this chat to HawkEye alerts"},
			{"command": "stop", "description": "Stop HawkEye alerts in this chat"},
		}}, nil); err != nil {
			return err
		}
	}
	b.mu.Lock()
	b.username = me.Username
	b.mu.Unlock()
	return nil
}

// Run calls Setup until it succeeds (Telegram may be unreachable when the API starts), then returns.
func (b *TelegramBot) Run(ctx context.Context, webhookURL string, log *slog.Logger) {
	if !strings.HasPrefix(webhookURL, "https://") {
		log.Info("telegram bot: the API is not on public https, so its webhook is not registered and no updates will arrive", "webhook_url", webhookURL)
	}
	for {
		err := b.Setup(ctx, webhookURL)
		if err == nil {
			log.Info("telegram bot ready", "username", b.Username())
			return
		}
		log.Warn("telegram bot setup failed; retrying", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(telegramSetupPause):
		}
	}
}

// call invokes one Bot API method. Errors never contain the URL (it holds the token).
func (b *TelegramBot) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.api+"/bot"+b.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s: invalid request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	var out struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.OK {
		return fmt.Errorf("telegram %s: HTTP %d %s", method, resp.StatusCode, clean(out.Description))
	}
	if result != nil {
		return json.Unmarshal(out.Result, result)
	}
	return nil
}

// TelegramLink is what the app shows to connect a chat.
type TelegramLink struct {
	Bot        string    `json:"bot"`         // the bot's @name, without the @
	Code       string    `json:"code"`        // for a channel: post "/connect <code>" there
	ChatURL    string    `json:"chat_url"`    // opens a private chat with the bot
	GroupURL   string    `json:"group_url"`   // asks which group to add the bot to
	ChannelURL string    `json:"channel_url"` // asks which channel to add the bot to, as an admin who can post
	ExpiresAt  time.Time `json:"expires_at"`
}

// TelegramLink issues a one-time code that connects a Telegram chat to the organization. It replaces any earlier code, so at most one link per
// organization works at a time.
func (s *Service) TelegramLink(ctx context.Context, orgID, userID uuid.UUID) (TelegramLink, error) {
	bot := s.bot.Username()
	if bot == "" {
		return TelegramLink{}, ErrTelegramUnavailable
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, orgID).Scan(&n); err != nil {
		return TelegramLink{}, err
	}
	if n >= MaxSettingsPerOrganization {
		return TelegramLink{}, ErrTooMany
	}
	code, hash := newToken()
	var expires time.Time
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM telegram_connect_codes WHERE organization_id = $1`, orgID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO telegram_connect_codes (organization_id, created_by, code_hash, expires_at)
			VALUES ($1, $2, $3, now() + $4::interval) RETURNING expires_at`, orgID, userID, hash, telegramCodeTTL).Scan(&expires)
	})
	if err != nil {
		return TelegramLink{}, err
	}
	base := "https://t.me/" + bot
	return TelegramLink{
		Bot: bot, Code: code, ExpiresAt: expires,
		ChatURL: base + "?start=" + code, GroupURL: base + "?startgroup=" + code, ChannelURL: base + "?startchannel&admin=post_messages",
	}, nil
}

// TelegramUpdate is the part of a Telegram update the bot acts on.
type TelegramUpdate struct {
	Message      *telegramUpdateMessage `json:"message"`
	ChannelPost  *telegramUpdateMessage `json:"channel_post"`
	MyChatMember *struct {
		Chat          telegramChat `json:"chat"`
		NewChatMember struct {
			Status string `json:"status"`
		} `json:"new_chat_member"`
	} `json:"my_chat_member"`
}

type telegramUpdateMessage struct {
	Chat            telegramChat `json:"chat"`
	Text            string       `json:"text"`
	MigrateToChatID int64        `json:"migrate_to_chat_id"`
}

type telegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // private, group, supergroup or channel
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

func (c telegramChat) id() string { return strconv.FormatInt(c.ID, 10) }

// label names the chat in the list of alert recipients.
func (c telegramChat) label() string {
	switch {
	case c.Title != "":
		return clean(c.Title)
	case c.Username != "":
		return "@" + clean(c.Username)
	case c.FirstName != "":
		return clean(c.FirstName)
	}
	return "chat " + c.id()
}

// TelegramReply is a message to send back to the chat an update came from. The webhook returns it as its response (Telegram runs a method given
// in the response body), so a reply costs no extra request.
type TelegramReply struct {
	Method string `json:"method"`
	ChatID int64  `json:"chat_id"`
	Text   string `json:"text"`
}

func reply(chat telegramChat, text string) *TelegramReply {
	return &TelegramReply{Method: "sendMessage", ChatID: chat.ID, Text: text}
}

const telegramHelp = "Hi! I send HawkEye website and API monitoring alerts.\n\nTo get alerts here, open HawkEye → Alert recipients → Telegram → Connect, and follow the link. To stop them, send /stop."

// HandleTelegramUpdate acts on one update from HawkEye's bot and returns the reply to send, if any.
func (s *Service) HandleTelegramUpdate(ctx context.Context, u TelegramUpdate) (*TelegramReply, error) {
	if m := u.MyChatMember; m != nil {
		// The bot was removed from a group or channel, or blocked in a private chat: nothing more can be delivered there.
		if m.NewChatMember.Status == "left" || m.NewChatMember.Status == "kicked" {
			return nil, s.forgetTelegramChat(ctx, m.Chat.id())
		}
		return nil, nil
	}
	msg := u.Message
	if msg == nil {
		msg = u.ChannelPost
	}
	if msg == nil {
		return nil, nil
	}
	if msg.MigrateToChatID != 0 {
		// A group became a supergroup, which has a new ID; follow it so alerts keep arriving.
		return nil, s.moveTelegramChat(ctx, msg.Chat.id(), strconv.FormatInt(msg.MigrateToChatID, 10))
	}
	cmd, arg, ok := s.telegramCommand(msg.Text)
	switch {
	case !ok:
		if msg.Chat.Type == "private" {
			return reply(msg.Chat, telegramHelp), nil
		}
		return nil, nil // other talk in a group is not for us
	case (cmd == "start" || cmd == "connect") && arg != "":
		return s.connectTelegramChat(ctx, msg.Chat, arg)
	case cmd == "stop":
		n, err := s.forgetTelegramChatCount(ctx, msg.Chat.id())
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return reply(msg.Chat, "This chat is not receiving HawkEye alerts."), nil
		}
		return reply(msg.Chat, "Done: this chat will no longer receive HawkEye alerts. To connect it again, use Alert recipients in HawkEye."), nil
	default:
		return reply(msg.Chat, telegramHelp), nil
	}
}

// telegramCommand splits "/start@ThisBot abc" into ("start", "abc"). A command addressed to another bot is not ours.
func (s *Service) telegramCommand(text string) (cmd, arg string, ok bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", "", false
	}
	cmd, to, _ := strings.Cut(strings.TrimPrefix(fields[0], "/"), "@")
	if to != "" && !strings.EqualFold(to, s.bot.Username()) {
		return "", "", false
	}
	if len(fields) > 1 {
		arg = fields[1]
	}
	return strings.ToLower(cmd), arg, true
}

// telegramFingerprint identifies a chat reached through HawkEye's bot, for duplicate detection (distinct from any own-bot URL fingerprint).
func telegramFingerprint(chatID string) string {
	sum := sha256.Sum256([]byte("hawkeye-bot:" + chatID))
	return hex.EncodeToString(sum[:])
}

func (s *Service) connectTelegramChat(ctx context.Context, chat telegramChat, code string) (*TelegramReply, error) {
	var org uuid.UUID
	var orgName string
	var created bool
	var settingID uuid.UUID
	who := audit.Actor{Type: audit.ActorSystem}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var by *uuid.UUID
		err := tx.QueryRow(ctx, `UPDATE telegram_connect_codes SET used_at = now()
			WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING organization_id, created_by`, hashToken(code)).Scan(&org, &by)
		if err != nil {
			return err
		}
		// The chat is connected by whoever made the link (someone in the chat tapped Start with it).
		if by != nil {
			var addr string
			if tx.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, *by).Scan(&addr) == nil {
				who = audit.User(*by, addr)
			}
		}
		if err := tx.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1 FOR UPDATE`, org).Scan(&orgName); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, org).Scan(&n); err != nil {
			return err
		}
		if n >= MaxSettingsPerOrganization {
			return ErrTooMany
		}
		err = tx.QueryRow(ctx, `INSERT INTO notification_settings (organization_id, channel, hawkeye_bot, destination, label, fingerprint, verified_at)
			VALUES ($1, 'telegram', true, $2, $3, $4, now())
			ON CONFLICT (organization_id, channel, fingerprint) WHERE fingerprint IS NOT NULL DO NOTHING RETURNING id`,
			org, chat.id(), chat.label(), telegramFingerprint(chat.id())).Scan(&settingID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // this chat was already connected
		}
		created = err == nil
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return reply(chat, "That link has expired or was already used. In HawkEye, open Alert recipients → Telegram → Connect to get a new one."), nil
	case errors.Is(err, ErrTooMany):
		return reply(chat, fmt.Sprintf("This HawkEye account already has the maximum of %d alert recipients. Remove one in HawkEye, then connect again.", MaxSettingsPerOrganization)), nil
	case err != nil:
		return nil, err
	case !created:
		return reply(chat, "This chat already receives HawkEye alerts for "+clean(orgName)+"."), nil
	}
	s.log.InfoContext(ctx, "telegram chat connected", "organization_id", org, "chat_type", chat.Type)
	s.audit.Record(ctx, audit.Event{Action: audit.AlertDestinationAdded, OrganizationID: org, Actor: who, TargetType: "alert_destination",
		TargetID: settingID.String(), TargetName: chat.label(), Details: map[string]any{"channel": ChannelTelegram, "via": "hawkeye_bot"}})
	return reply(chat, "✅ Connected to HawkEye ("+clean(orgName)+").\n\nThis chat will get an alert when a monitor goes offline and when it recovers, and before SSL certificates and domains expire.\n\nTo stop, send /stop or remove me from the chat."), nil
}

func (s *Service) forgetTelegramChat(ctx context.Context, chatID string) error {
	_, err := s.forgetTelegramChatCount(ctx, chatID)
	return err
}

func (s *Service) forgetTelegramChatCount(ctx context.Context, chatID string) (int64, error) {
	rows, err := s.pool.Query(ctx, `DELETE FROM notification_settings WHERE hawkeye_bot AND destination = $1
		RETURNING id, organization_id, COALESCE(label, '')`, chatID)
	if err != nil {
		return 0, err
	}
	type gone struct {
		id, org uuid.UUID
		label   string
	}
	var removed []gone
	for rows.Next() {
		var g gone
		if err := rows.Scan(&g.id, &g.org, &g.label); err != nil {
			rows.Close()
			return 0, err
		}
		removed = append(removed, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, g := range removed {
		// Someone in the chat sent /stop or removed the bot: nobody signed in to HawkEye did it.
		s.audit.Record(ctx, audit.Event{Action: audit.AlertDestinationRemoved, OrganizationID: g.org, Actor: audit.Actor{Type: audit.ActorSystem},
			TargetType: "alert_destination", TargetID: g.id.String(), TargetName: g.label, Details: map[string]any{"channel": ChannelTelegram, "via": "hawkeye_bot"}})
	}
	if n := len(removed); n > 0 {
		s.log.InfoContext(ctx, "telegram chat disconnected", "destinations", n)
	}
	return int64(len(removed)), nil
}

func (s *Service) moveTelegramChat(ctx context.Context, from, to string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// An organization that somehow has both chats keeps the new one.
		if _, err := tx.Exec(ctx, `DELETE FROM notification_settings o WHERE o.hawkeye_bot AND o.destination = $1
			AND EXISTS (SELECT 1 FROM notification_settings n WHERE n.organization_id = o.organization_id AND n.hawkeye_bot AND n.destination = $2)`, from, to); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE notification_settings SET destination = $2, fingerprint = $3 WHERE hawkeye_bot AND destination = $1`, from, to, telegramFingerprint(to))
		return err
	})
}
