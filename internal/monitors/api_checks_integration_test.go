package monitors_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/checks"
	"hawkeye/internal/monitors"
	"hawkeye/internal/plans"
	"hawkeye/internal/secrets"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

func strp(s string) *string { return &s }

func apiIn() monitors.CreateInput {
	in := newIn("API")
	in.Method = strp("POST")
	in.Headers = []checks.Header{{Name: " X-Env ", Value: "prod"}}
	in.Body = strp(`{"q":1}`)
	in.Auth = &monitors.AuthInput{Type: "bearer", Secret: strp("s3cret")}
	in.Assertions = []checks.Assertion{
		{Type: "json_path", Path: "status", Operator: "equals", Value: "ok"},
		{Type: "body_contains", Value: "ok", Path: "ignored", Operator: "ignored"},
	}
	return in
}

func TestCreateAPIMonitorNeverReturnsTheSecret(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	m, err := svc.Create(t.Context(), org, apiIn())
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != "POST" || len(m.Headers) != 1 || m.Headers[0].Name != "X-Env" || m.Body == nil || *m.Body != `{"q":1}` ||
		m.Auth.Type != "bearer" || !m.Auth.SecretSet || len(m.Assertions) != 2 || m.Assertions[1].Path != "" {
		t.Fatalf("%+v", m)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "s3cret") {
		t.Fatalf("secret leaked in JSON: %s", b)
	}
	// The database holds only the encrypted credential, bound to this monitor.
	var stored string
	if err := pool.QueryRow(t.Context(), `SELECT auth_secret FROM monitors WHERE id = $1`, m.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "s3cret") || !secrets.IsSealed(stored) {
		t.Fatalf("credential stored without encryption: %q", stored)
	}
	if plain, err := testutil.SecretBox().Open(stored, m.ID.String()); err != nil || plain != "s3cret" {
		t.Fatalf("stored credential does not open for its own monitor: %q %v", plain, err)
	}
	if _, err := testutil.SecretBox().Open(stored, uuid.NewString()); err == nil {
		t.Fatal("stored credential opens for a different monitor")
	}

	// A plain monitor gets empty, non-null collections and no auth.
	p, _ := svc.Create(t.Context(), org, newIn("plain"))
	if p.Headers == nil || p.Assertions == nil || p.Body != nil || p.Auth.Type != "none" || p.Auth.SecretSet {
		t.Fatalf("%+v", p)
	}
}

func TestAPIMonitorValidation(t *testing.T) {
	svc, _, org := setup(t, "free", "active")
	cases := []struct {
		name  string
		edit  func(*monitors.CreateInput)
		field string
	}{
		{"bad header name", func(in *monitors.CreateInput) { in.Headers = []checks.Header{{Name: "Bad Name", Value: "x"}} }, "headers[0].name"},
		{"reserved header", func(in *monitors.CreateInput) { in.Headers = []checks.Header{{Name: "host", Value: "x"}} }, "headers[0].name"},
		{"authorization header", func(in *monitors.CreateInput) { in.Headers = []checks.Header{{Name: "Authorization", Value: "x"}} }, "headers[0].name"},
		{"duplicate header", func(in *monitors.CreateInput) {
			in.Headers = []checks.Header{{Name: "X-A", Value: "1"}, {Name: "x-a", Value: "2"}}
		}, "headers[1].name"},
		{"header injection", func(in *monitors.CreateInput) { in.Headers = []checks.Header{{Name: "X-A", Value: "1\r\nX-B: 2"}} }, "headers[0].value"},
		{"too many headers", func(in *monitors.CreateInput) {
			in.Headers = nil
			for i := range 21 {
				in.Headers = append(in.Headers, checks.Header{Name: "X-" + string(rune('A'+i)), Value: "v"})
			}
		}, "headers"},
		{"body with GET", func(in *monitors.CreateInput) { in.Method = strp("GET") }, "body"},
		{"body too large", func(in *monitors.CreateInput) { in.Body = strp(strings.Repeat("x", 64<<10+1)) }, "body"},
		{"unknown auth", func(in *monitors.CreateInput) { in.Auth.Type = "digest" }, "auth.type"},
		{"missing secret", func(in *monitors.CreateInput) { in.Auth.Secret = nil }, "auth.secret"},
		{"basic without username", func(in *monitors.CreateInput) { in.Auth.Type = "basic" }, "auth.username"},
		{"header auth name", func(in *monitors.CreateInput) { in.Auth.Type = "header"; in.Auth.HeaderName = "bad name" }, "auth.header_name"},
		{"header auth clashes", func(in *monitors.CreateInput) { in.Auth.Type = "header"; in.Auth.HeaderName = "x-env" }, "auth.header_name"},
		{"unknown assertion", func(in *monitors.CreateInput) { in.Assertions[0].Type = "regex" }, "assertions[0].type"},
		{"bad path", func(in *monitors.CreateInput) { in.Assertions[0].Path = "a[x]" }, "assertions[0].path"},
		{"bad operator", func(in *monitors.CreateInput) { in.Assertions[0].Operator = "gt" }, "assertions[0].operator"},
		{"empty contains", func(in *monitors.CreateInput) { in.Assertions[1].Value = "" }, "assertions[1].value"},
		{"assertions with HEAD", func(in *monitors.CreateInput) { in.Method = strp("HEAD"); in.Body = nil }, "assertions"},
	}
	for _, c := range cases {
		in := apiIn()
		c.edit(&in)
		_, err := svc.Create(t.Context(), org, in)
		var ve *validation.Error
		if !errors.As(err, &ve) || ve.Fields[c.field] == "" {
			t.Errorf("%s: want error on %s, got %v", c.name, c.field, err)
		}
	}
}

func TestUpdateKeepsSecretUnlessReplacedOrTypeChanges(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	in := apiIn()
	in.Auth = &monitors.AuthInput{Type: "basic", Username: "u", Secret: strp("pw")}
	m, err := svc.Create(t.Context(), org, in)
	if err != nil {
		t.Fatal(err)
	}
	rawStored := func() *string {
		var s *string
		pool.QueryRow(t.Context(), `SELECT auth_secret FROM monitors WHERE id = $1`, m.ID).Scan(&s)
		return s
	}
	// stored returns the decrypted credential (the database only ever holds the encrypted form).
	stored := func() *string {
		raw := rawStored()
		if raw == nil {
			return nil
		}
		plain, err := testutil.SecretBox().Open(*raw, m.ID.String())
		if err != nil {
			t.Fatalf("stored credential unreadable: %v", err)
		}
		return &plain
	}
	// Changing only the username keeps the password.
	u, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Auth: &monitors.AuthInput{Type: "basic", Username: "v"}})
	if err != nil || u.Auth.Username != "v" || !u.Auth.SecretSet || *stored() != "pw" {
		t.Fatalf("%+v %v", u.Auth, err)
	}
	// Edits that do not mention auth keep it.
	if u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Name: strp("x")}); err != nil || *stored() != "pw" {
		t.Fatalf("%v", err)
	}
	// A new type needs a new secret.
	var ve *validation.Error
	if _, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Auth: &monitors.AuthInput{Type: "bearer"}}); !errors.As(err, &ve) || ve.Fields["auth.secret"] == "" {
		t.Fatalf("switching type without a secret: %v", err)
	}
	if u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Auth: &monitors.AuthInput{Type: "bearer", Secret: strp("tok")}}); err != nil ||
		u.Auth.Username != "" || *stored() != "tok" {
		t.Fatalf("%+v %v", u.Auth, err)
	}
	// none clears everything.
	if u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Auth: &monitors.AuthInput{Type: "none"}}); err != nil ||
		u.Auth.SecretSet || stored() != nil {
		t.Fatalf("%+v %v", u.Auth, err)
	}
}

func TestUpdateRequestFields(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	m, err := svc.Create(t.Context(), org, apiIn())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET current_status='up', consecutive_successes=3 WHERE id=$1`, m.ID)

	// Assertions change what success means, but not the request: health state is kept.
	u, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Assertions: &[]checks.Assertion{}})
	if err != nil || len(u.Assertions) != 0 || u.CurrentStatus != "up" || len(u.Headers) != 1 {
		t.Fatalf("%+v %v", u, err)
	}
	// Switching to GET needs the body cleared in the same request.
	if _, err := svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Method: strp("GET")}); err == nil {
		t.Fatal("GET with a stored body must be rejected")
	}
	u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Method: strp("GET"), Body: monitors.Nullable[string]{Set: true}})
	if err != nil || u.Body != nil || u.CurrentStatus != "unknown" {
		t.Fatalf("%+v %v", u, err)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET current_status='up' WHERE id=$1`, m.ID)
	u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Headers: &[]checks.Header{}})
	if err != nil || len(u.Headers) != 0 || u.CurrentStatus != "unknown" {
		t.Fatalf("changing headers must reset health state: %+v %v", u, err)
	}
	_, _ = pool.Exec(t.Context(), `UPDATE monitors SET current_status='up' WHERE id=$1`, m.ID)
	u, err = svc.Update(t.Context(), org, m.ID, monitors.UpdateInput{Auth: &monitors.AuthInput{Type: "bearer", Secret: strp("rotated")}})
	if err != nil || u.CurrentStatus != "unknown" {
		t.Fatalf("new credentials must reset health state: %+v %v", u, err)
	}
}

func TestSavingACredentialFailsClosedWithoutAnEncryptionKey(t *testing.T) {
	_, pool, org := setup(t, "free", "active")
	noKey := monitors.NewService(pool, plans.NewService("http://app.test")) // no WithSecretBox
	if _, err := noKey.Create(t.Context(), org, apiIn()); err == nil {
		t.Fatal("a credential was accepted although it could not be encrypted")
	}
	var n int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM monitors WHERE organization_id = $1`, org).Scan(&n)
	if n != 0 {
		t.Fatalf("%d monitors stored", n)
	}
	// Monitors without credentials do not need a key.
	if _, err := noKey.Create(t.Context(), org, newIn("plain")); err != nil {
		t.Fatalf("plain monitor: %v", err)
	}
}

func TestLegacyPlainTextCredentialsAreEncryptedOnce(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds, auth_type, auth_secret)
		VALUES ($1, 'old', 'https://example.com', 300, 10, 'bearer', 'legacy-token') RETURNING id`, org).Scan(&id); err != nil {
		t.Fatal(err)
	}
	n, err := svc.EncryptLegacySecrets(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("converted %d, err %v", n, err)
	}
	var stored string
	pool.QueryRow(t.Context(), `SELECT auth_secret FROM monitors WHERE id = $1`, id).Scan(&stored)
	if plain, err := testutil.SecretBox().Open(stored, id.String()); err != nil || plain != "legacy-token" {
		t.Fatalf("%q %v", plain, err)
	}
	if n, err := svc.EncryptLegacySecrets(t.Context()); err != nil || n != 0 {
		t.Fatalf("second run converted %d (err %v); it must be idempotent", n, err)
	}
}

func TestDeletingAMonitorWipesCredentialsAndRequestMaterial(t *testing.T) {
	svc, pool, org := setup(t, "free", "active")
	m, err := svc.Create(t.Context(), org, apiIn())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(t.Context(), org, m.ID); err != nil {
		t.Fatal(err)
	}
	var secret, body *string
	var typ string
	var headers string
	if err := pool.QueryRow(t.Context(), `SELECT auth_secret, request_body, auth_type, request_headers::text FROM monitors WHERE id = $1`, m.ID).
		Scan(&secret, &body, &typ, &headers); err != nil {
		t.Fatal(err)
	}
	if secret != nil || body != nil || typ != "none" || headers != "[]" {
		t.Fatalf("deleted monitor still holds request material: secret=%v body=%v type=%s headers=%s", secret, body, typ, headers)
	}
}

func TestWriteMethodsNeedASlowerInterval(t *testing.T) {
	svc, _, org := setup(t, "pro", "active") // pro allows 1-minute checks
	in := newIn("writer")
	in.Method = strp("DELETE")
	in.CheckIntervalSeconds = new(int)
	*in.CheckIntervalSeconds = 60
	_, err := svc.Create(t.Context(), org, in)
	var ve *validation.Error
	if !errors.As(err, &ve) || ve.Fields["check_interval_seconds"] == "" {
		t.Fatalf("a 1-minute DELETE monitor was accepted: %v", err)
	}
	in.CheckIntervalSeconds = nil // default: raised to the write-method minimum, not the plan's 60 s
	m, err := svc.Create(t.Context(), org, in)
	if err != nil || m.CheckIntervalSeconds < monitors.MinWriteIntervalSeconds {
		t.Fatalf("default interval %d err %v", m.CheckIntervalSeconds, err)
	}
	// Read-only methods keep the plan's speed.
	g := newIn("reader")
	if m, err := svc.Create(t.Context(), org, g); err != nil || m.CheckIntervalSeconds != 60 {
		t.Fatalf("GET interval %d err %v", m.CheckIntervalSeconds, err)
	}
}
