package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hawkeye/internal/notifications"
)

const telegramSecret = "0123456789abcdef0123456789abcdef"

func telegramEnv(t *testing.T) *env {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":{"username":"HawkEyeObserverBot"}}`))
	}))
	t.Cleanup(api.Close)
	bot := notifications.NewTelegramBot("123456789:AAEhBP0av28MapQ3a_Bb-cDeFgHiJkLmNoP", telegramSecret, api.URL, api.Client())
	if err := bot.Setup(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	return newEnvOpts(t, 1000, envOpts{telegram: bot})
}

// telegramPost calls the webhook as Telegram would: no session, no CSRF token, the secret in a header.
func (e *env) telegramPost(t *testing.T, secret string, update string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/webhooks/telegram", bytes.NewBufferString(update))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body
}

func TestConnectingTelegramThroughTheWebhook(t *testing.T) {
	e := telegramEnv(t)
	c, _ := e.register(t, "owner@example.com")

	if r := c.call("GET", "/api/v1/notification-settings", nil); r.Body["telegram_bot"] != "HawkEyeObserverBot" {
		t.Fatalf("the list says which bot to use: %v", r.Body)
	}
	if r := c.call("POST", "/api/v1/notification-settings/telegram-link", nil); r.Status != 403 {
		t.Fatalf("an unverified owner cannot connect chats: %d %v", r.Status, r.Body)
	}
	e.verify(t, c, "owner@example.com")
	r := c.call("POST", "/api/v1/notification-settings/telegram-link", nil)
	link, _ := r.Body["telegram_link"].(map[string]any)
	code, _ := link["code"].(string)
	if r.Status != 201 || code == "" || link["chat_url"] != "https://t.me/HawkEyeObserverBot?start="+code {
		t.Fatalf("%d %v", r.Status, r.Body)
	}

	start := `{"message":{"chat":{"id":-555,"type":"group","title":"Ops team"},"text":"/start@HawkEyeObserverBot ` + code + `"}}`
	for name, secret := range map[string]string{"no secret": "", "wrong secret": strings.Repeat("x", 32)} {
		if status, _ := e.telegramPost(t, secret, start); status != 401 {
			t.Fatalf("%s: %d", name, status)
		}
	}
	status, body := e.telegramPost(t, telegramSecret, start)
	if status != 200 || body["method"] != "sendMessage" || body["chat_id"] != float64(-555) || !strings.Contains(body["text"].(string), "Connected") {
		t.Fatalf("%d %v", status, body)
	}
	list := c.call("GET", "/api/v1/notification-settings", nil).Body["notification_settings"].([]any)
	last := list[len(list)-1].(map[string]any)
	if last["channel"] != "telegram" || last["destination"] != "Ops team" || last["verified"] != true {
		t.Fatalf("%v", list)
	}
	if status, _ := e.telegramPost(t, telegramSecret, `not json`); status != 200 {
		t.Fatalf("an update we cannot read is acknowledged, so Telegram does not resend it: %d", status)
	}
}

func TestTelegramEndpointsWithoutABot(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	e.verify(t, c, "owner@example.com")
	if r := c.call("GET", "/api/v1/notification-settings", nil); r.Body["telegram_bot"] != "" {
		t.Fatalf("%v", r.Body)
	}
	if r := c.call("POST", "/api/v1/notification-settings/telegram-link", nil); r.Status != 503 || errCode(r) != "TELEGRAM_UNAVAILABLE" {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	if status, _ := e.telegramPost(t, telegramSecret, `{}`); status != 404 && status != 405 {
		t.Fatalf("no webhook without a bot: %d", status)
	}
}
