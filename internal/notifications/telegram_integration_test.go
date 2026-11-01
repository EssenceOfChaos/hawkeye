package notifications_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/notifications"
	"hawkeye/internal/testutil"
)

const botToken = "123456789:AAEhBP0av28MapQ3a_Bb-cDeFgHiJkLmNoP"

// readyBot is HawkEye's bot after a successful Setup against a fake Bot API that knows it as @HawkEyeObserverBot.
func readyBot(t *testing.T) *notifications.TelegramBot {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"HawkEyeObserverBot"}}`))
	}))
	t.Cleanup(api.Close)
	bot := notifications.NewTelegramBot(botToken, "0123456789abcdef", api.URL, api.Client())
	if err := bot.Setup(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	return bot
}

func botService(e *env, bot *notifications.TelegramBot) *notifications.Service {
	return notifications.NewService(e.pool, &scriptedSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "https://app.example.com", "alerts@example.com",
		notifications.WithSecretBox(testutil.SecretBox()), notifications.WithTelegramBot(bot))
}

func (e *env) user(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`, uuid.NewString()+"@example.com").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// update builds a Telegram update from JSON, as the webhook would decode it.
func update(t *testing.T, raw string) notifications.TelegramUpdate {
	t.Helper()
	var u notifications.TelegramUpdate
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

func message(chat, text string) string {
	return `{"message":{"chat":` + chat + `,"text":` + strconvQuote(text) + `}}`
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

const (
	privateChat = `{"id":4242,"type":"private","username":"alice","first_name":"Alice"}`
	groupChat   = `{"id":-555,"type":"group","title":"Ops team"}`
)

func TestConnectingAChatThroughHawkEyesBot(t *testing.T) {
	e := newEnv(t, 5)
	svc := botService(e, readyBot(t))
	ctx := t.Context()

	link, err := svc.TelegramLink(ctx, e.org, e.user(t))
	if err != nil || link.Bot != "HawkEyeObserverBot" || link.ChatURL != "https://t.me/HawkEyeObserverBot?start="+link.Code ||
		link.GroupURL != "https://t.me/HawkEyeObserverBot?startgroup="+link.Code || len(link.Code) > 64 {
		t.Fatalf("%+v %v", link, err)
	}

	r, err := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "/start "+link.Code)))
	if err != nil || r == nil || r.ChatID != 4242 || r.Method != "sendMessage" || !strings.Contains(r.Text, "Connected to HawkEye (o)") {
		t.Fatalf("%+v %v", r, err)
	}
	list, _ := svc.List(ctx, e.org)
	if len(list) != 1 || list[0].Channel != "telegram" || list[0].Destination != "@alice" || !list[0].Verified || !list[0].Enabled {
		t.Fatalf("%+v", list)
	}

	// A code works once.
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(groupChat, "/start "+link.Code))); r == nil || !strings.Contains(r.Text, "expired") {
		t.Fatalf("a used code must not connect another chat: %+v", r)
	}

	// A group, added through the startgroup link, says "/start@Bot <code>".
	link2, _ := svc.TelegramLink(ctx, e.org, e.user(t))
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(groupChat, "/start@hawkeyeobserverbot "+link2.Code))); r == nil || !strings.Contains(r.Text, "Connected") {
		t.Fatalf("%+v", r)
	}
	// The same chat again is reported, not duplicated.
	link3, _ := svc.TelegramLink(ctx, e.org, e.user(t))
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(groupChat, "/connect "+link3.Code))); r == nil || !strings.Contains(r.Text, "already receives") {
		t.Fatalf("%+v", r)
	}
	if list, _ := svc.List(ctx, e.org); len(list) != 2 || list[1].Destination != "Ops team" {
		t.Fatalf("%+v", list)
	}
}

func TestANewLinkCancelsTheLastAndCommandsForOtherBotsAreIgnored(t *testing.T) {
	e := newEnv(t, 5)
	svc := botService(e, readyBot(t))
	ctx := t.Context()
	old, _ := svc.TelegramLink(ctx, e.org, e.user(t))
	cur, _ := svc.TelegramLink(ctx, e.org, e.user(t))
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "/start "+old.Code))); r == nil || !strings.Contains(r.Text, "expired") {
		t.Fatalf("an earlier link must stop working: %+v", r)
	}
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(groupChat, "/start@SomeOtherBot "+cur.Code))); r != nil {
		t.Fatalf("a command for another bot is not ours: %+v", r)
	}
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(groupChat, "hello everyone"))); r != nil {
		t.Fatalf("group chatter gets no reply: %+v", r)
	}
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "hello"))); r == nil || !strings.Contains(r.Text, "Alert recipients") {
		t.Fatalf("a private message gets help: %+v", r)
	}
	if list, _ := svc.List(ctx, e.org); len(list) != 0 {
		t.Fatalf("%+v", list)
	}
}

func TestStopRemovalAndUpgradeKeepTheListTrue(t *testing.T) {
	e := newEnv(t, 5)
	svc := botService(e, readyBot(t))
	ctx := t.Context()
	connect := func(chat string) {
		link, _ := svc.TelegramLink(ctx, e.org, e.user(t))
		if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(chat, "/start "+link.Code))); r == nil || !strings.Contains(r.Text, "Connected") {
			t.Fatalf("%+v", r)
		}
	}
	dest := func() []string {
		var out []string
		rows, _ := e.pool.Query(ctx, `SELECT destination FROM notification_settings WHERE organization_id = $1 AND hawkeye_bot ORDER BY destination`, e.org)
		defer rows.Close()
		for rows.Next() {
			var d string
			rows.Scan(&d)
			out = append(out, d)
		}
		return out
	}

	connect(privateChat)
	connect(groupChat)
	// The group becomes a supergroup with a new ID.
	if _, err := svc.HandleTelegramUpdate(ctx, update(t, `{"message":{"chat":`+groupChat+`,"migrate_to_chat_id":-1009999}}`)); err != nil {
		t.Fatal(err)
	}
	if d := dest(); strings.Join(d, ",") != "-1009999,4242" {
		t.Fatalf("%v", d)
	}
	// Someone removes the bot from the supergroup.
	if _, err := svc.HandleTelegramUpdate(ctx, update(t, `{"my_chat_member":{"chat":{"id":-1009999,"type":"supergroup"},"new_chat_member":{"status":"kicked"}}}`)); err != nil {
		t.Fatal(err)
	}
	if d := dest(); strings.Join(d, ",") != "4242" {
		t.Fatalf("%v", d)
	}
	// And /stop in the private chat.
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "/stop"))); r == nil || !strings.Contains(r.Text, "no longer") || len(dest()) != 0 {
		t.Fatalf("%+v %v", r, dest())
	}
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "/stop"))); r == nil || !strings.Contains(r.Text, "not receiving") {
		t.Fatalf("%+v", r)
	}
}

func TestConnectingRespectsTheDestinationLimit(t *testing.T) {
	e := newEnv(t, 5)
	svc := botService(e, readyBot(t))
	ctx := t.Context()
	link, _ := svc.TelegramLink(ctx, e.org, e.user(t))
	for i := range notifications.MaxSettingsPerOrganization {
		e.pool.Exec(ctx, `INSERT INTO notification_settings (organization_id, channel, destination) VALUES ($1, 'email', $2)`, e.org, uuid.NewString()+"@example.com")
		_ = i
	}
	if _, err := svc.TelegramLink(ctx, e.org, e.user(t)); !errors.Is(err, notifications.ErrTooMany) {
		t.Fatalf("no link once the list is full: %v", err)
	}
	if r, _ := svc.HandleTelegramUpdate(ctx, update(t, message(privateChat, "/start "+link.Code))); r == nil || !strings.Contains(r.Text, "maximum") {
		t.Fatalf("%+v", r)
	}
}

func TestNoLinkWithoutAReadyBot(t *testing.T) {
	e := newEnv(t, 5)
	for name, bot := range map[string]*notifications.TelegramBot{
		"not configured": nil,
		"not yet set up": notifications.NewTelegramBot(botToken, "0123456789abcdef", "", nil),
	} {
		if _, err := botService(e, bot).TelegramLink(t.Context(), e.org, e.user(t)); !errors.Is(err, notifications.ErrTelegramUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAlertsToABotChatUseTheConfiguredToken(t *testing.T) {
	e := newEnv(t, 5)
	rcv := newReceiver(t, 200)
	bot := notifications.NewTelegramBot(botToken, "0123456789abcdef", "", nil) // sending needs only the token; the test client reroutes api.telegram.org
	var setting, delivery uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, hawkeye_bot, destination, label, fingerprint, verified_at)
		VALUES ($1, 'telegram', true, '-100777', 'Ops team', 'fp', now()) RETURNING id`, e.org).Scan(&setting)
	e.pool.QueryRow(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type) VALUES ($1, $2, 'outage') RETURNING id`,
		e.inc, setting).Scan(&delivery)

	d := dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{}).WithSecrets(testutil.SecretBox()).WithHTTPClient(rcv.client()).WithTelegramBot(bot)
	d.ProcessOne(t.Context())
	got := rcv.requests()
	if e.row(t, delivery).Status != "sent" || len(got) != 1 || got[0].Path != "/bot"+botToken+"/sendMessage" ||
		!strings.Contains(string(got[0].Body), `"chat_id":"-100777"`) || !strings.Contains(string(got[0].Body), "may be offline") {
		t.Fatalf("%+v %+v", e.row(t, delivery), got)
	}

	// The test button goes the same way.
	svc := notifications.NewService(e.pool, &scriptedSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "https://app.example.com", "alerts@example.com",
		notifications.WithTelegramBot(bot), notifications.WithHTTPClient(rcv.client()))
	if err := svc.Test(t.Context(), e.org, setting, "owner@example.com", nil); err != nil || len(rcv.requests()) != 2 {
		t.Fatalf("%v %d", err, len(rcv.requests()))
	}
}

func TestAlertsToABotChatFailPermanentlyWithoutTheBot(t *testing.T) {
	e := newEnv(t, 5)
	var setting, delivery uuid.UUID
	e.pool.QueryRow(t.Context(), `INSERT INTO notification_settings (organization_id, channel, hawkeye_bot, destination, label, fingerprint, verified_at)
		VALUES ($1, 'telegram', true, '-100777', 'Ops team', 'fp', now()) RETURNING id`, e.org).Scan(&setting)
	e.pool.QueryRow(t.Context(), `INSERT INTO notification_deliveries (incident_id, notification_setting_id, event_type) VALUES ($1, $2, 'outage') RETURNING id`,
		e.inc, setting).Scan(&delivery)
	dispatcher(e, &scriptedSender{}, notifications.DispatcherConfig{}).WithSecrets(testutil.SecretBox()).ProcessOne(t.Context())
	if r := e.row(t, delivery); r.Status != "failed" {
		t.Fatalf("%+v", r)
	}
}
