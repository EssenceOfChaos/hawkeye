package apitokens_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/apitokens"
	"hawkeye/internal/email"
	"hawkeye/internal/testutil"
)

// seedKey adds a person, their organization and one key that was made `age` ago and lasts `life` in total.
func seedKey(t *testing.T, pool *pgxpool.Pool, addr, name string, age, life time.Duration) string {
	t.Helper()
	ctx := t.Context()
	var id string
	err := pool.QueryRow(ctx, `WITH u AS (INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, 'x', now()) RETURNING id),
		o AS (INSERT INTO organizations (name) VALUES ('Acme') RETURNING id),
		m AS (INSERT INTO organization_members (organization_id, user_id, role) SELECT o.id, u.id, 'owner' FROM o, u RETURNING organization_id, user_id)
		INSERT INTO api_tokens (organization_id, user_id, name, scope, prefix, token_hash, created_at, expires_at)
		SELECT organization_id, user_id, $2, 'read', 'hk_abcdefg', sha256(gen_random_uuid()::text::bytea), now() - $3::interval, now() - $3::interval + $4::interval FROM m
		RETURNING id::text`, addr, name, age, life).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func countTo(mail *email.Memory, to, subject string) int {
	n := 0
	for _, m := range mail.Messages() {
		if m.To == to && strings.Contains(m.Subject, subject) {
			n++
		}
	}
	return n
}

func TestExpiryEmailsGoOutOnceEach(t *testing.T) {
	pool := testutil.NewPool(t)
	mail := &email.Memory{}
	job := apitokens.NewExpiryJob(pool, mail, slog.New(slog.NewTextHandler(io.Discard, nil)), "https://app.example.com", time.Minute)
	day := 24 * time.Hour

	seedKey(t, pool, "soon@example.com", "Soon", 85*day, 90*day)        // 5 days left: warn
	seedKey(t, pool, "later@example.com", "Later", 10*day, 90*day)      // 80 days left: nothing yet
	seedKey(t, pool, "short@example.com", "Short", 1*day, 7*day)        // made to last a week: no warning
	seedKey(t, pool, "expired@example.com", "Gone", 31*day, 30*day)     // expired a day ago: notice
	seedKey(t, pool, "ancient@example.com", "Ancient", 100*day, 90*day) // expired 10 days ago: too late to bother
	revoked := seedKey(t, pool, "revoked@example.com", "Revoked", 85*day, 90*day)
	if _, err := pool.Exec(t.Context(), `UPDATE api_tokens SET revoked_at = now() WHERE id = $1`, revoked); err != nil {
		t.Fatal(err)
	}

	sent, err := job.RunOnce(t.Context())
	if err != nil || sent != 2 {
		t.Fatalf("sent %d, err %v", sent, err)
	}
	if countTo(mail, "soon@example.com", "expires soon") != 1 || countTo(mail, "expired@example.com", "has expired") != 1 {
		t.Fatalf("%v", mail.Messages())
	}
	for _, addr := range []string{"later@example.com", "short@example.com", "ancient@example.com", "revoked@example.com"} {
		if countTo(mail, addr, "") != 0 {
			t.Fatalf("%s should not be emailed", addr)
		}
	}
	for _, m := range mail.Messages() {
		if !strings.Contains(m.Text, "Acme") || !strings.Contains(m.Text, "hk_abcdefg") || !strings.Contains(m.Text, "/api-keys") {
			t.Fatalf("the email should name the key, the organization and link to the keys page: %s", m.Text)
		}
	}

	// A second pass (or a second worker) sends nothing new.
	if sent, err := job.RunOnce(t.Context()); err != nil || sent != 0 {
		t.Fatalf("second pass sent %d, err %v", sent, err)
	}

	// Old revoked and expired keys are deleted; recent ones stay.
	oldRevoked := seedKey(t, pool, "oldrevoked@example.com", "Old revoked", 40*day, 90*day)
	if _, err := pool.Exec(t.Context(), `UPDATE api_tokens SET revoked_at = now() - interval '31 days' WHERE id = $1`, oldRevoked); err != nil {
		t.Fatal(err)
	}
	oldExpired := seedKey(t, pool, "oldexpired@example.com", "Old expired", 200*day, 100*day) // expired 100 days ago
	if _, err := job.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	var left int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM api_tokens WHERE id = ANY($1::uuid[])`, []string{oldRevoked, oldExpired}).Scan(&left)
	var kept int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM api_tokens WHERE id = $1`, revoked).Scan(&kept)
	if left != 0 || kept != 1 {
		t.Fatalf("old keys left %d, recently revoked kept %d", left, kept)
	}

	// Six days on, the warned key and the week-long one have both expired, and their owners hear so (the week-long one with no warning first).
	job.WithClock(func() time.Time { return time.Now().Add(6 * day) })
	if sent, err := job.RunOnce(t.Context()); err != nil || sent != 2 ||
		countTo(mail, "soon@example.com", "has expired") != 1 || countTo(mail, "short@example.com", "has expired") != 1 || countTo(mail, "short@example.com", "soon") != 0 {
		t.Fatalf("sent %d, err %v", sent, err)
	}
}
