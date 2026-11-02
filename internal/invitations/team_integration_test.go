package invitations_test

import (
	"errors"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/invitations"
	"hawkeye/internal/organizations"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

type team struct {
	pool  *pgxpool.Pool
	mail  *email.Memory
	svc   *invitations.Service
	orgs  *organizations.Service
	org   uuid.UUID
	owner auth.User
}

func newTeam(t *testing.T) *team {
	t.Helper()
	pool := testutil.NewPool(t)
	tm := &team{pool: pool, mail: &email.Memory{}, orgs: organizations.NewService(pool)}
	tm.svc = invitations.NewService(pool, plans.NewService("http://app.test"), tm.mail, slog.New(slog.NewTextHandler(io.Discard, nil)), "http://app.test")
	tm.owner = tm.user(t, "owner@example.com", true)
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('Acme Agency') RETURNING id`).Scan(&tm.org)
	pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, tm.org, tm.owner.ID)
	pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = 'agency'`, tm.org)
	// The owner's own address receives alerts, as it does from sign-up.
	pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, user_id, channel, destination, verified_at) VALUES ($1, $2, 'email', $3, now())`,
		tm.org, tm.owner.ID, tm.owner.Email)
	return tm
}

func (tm *team) user(t *testing.T, addr string, verified bool) auth.User {
	t.Helper()
	u := auth.User{Email: addr}
	if verified {
		now := time.Now()
		u.EmailVerifiedAt = &now
	}
	if err := tm.pool.QueryRow(t.Context(), `INSERT INTO users (email, password_hash, email_verified_at) VALUES ($1, 'x', $2) RETURNING id`, addr, u.EmailVerifiedAt).Scan(&u.ID); err != nil {
		t.Fatal(err)
	}
	return u
}

// join invites addr, then accepts as that (verified) user, with or without "send me alerts".
func (tm *team) join(t *testing.T, addr string, alerts bool) (auth.User, invitations.Accepted) {
	t.Helper()
	u := tm.user(t, addr, true)
	if _, err := tm.svc.Invite(t.Context(), tm.org, tm.owner, addr); err != nil {
		t.Fatal(err)
	}
	msgs := tm.mail.Messages()
	link := msgs[len(msgs)-1].Text
	i := strings.Index(link, "token=")
	token := strings.Fields(link[i+len("token="):])[0]
	if tok, err := url.QueryUnescape(token); err == nil {
		token = tok
	}
	res, err := tm.svc.Accept(t.Context(), u, token, alerts)
	if err != nil {
		t.Fatal(err)
	}
	return u, res
}

func (tm *team) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := tm.pool.QueryRow(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (tm *team) lastTo(addr string) *email.Message {
	msgs := tm.mail.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].To == addr {
			return &msgs[i]
		}
	}
	return nil
}

func TestJoiningTellsTheOwnerAndCanSignUpForAlerts(t *testing.T) {
	tm := newTeam(t)
	ann, res := tm.join(t, "ann@example.com", true)
	if res.Alerts != invitations.AlertsAdded || res.Organization.Role != "member" {
		t.Fatalf("%+v", res)
	}
	if tm.count(t, `SELECT count(*) FROM notification_settings WHERE organization_id = $1 AND user_id = $2 AND destination = $3 AND verified_at IS NOT NULL`,
		tm.org, ann.ID, ann.Email) != 1 {
		t.Fatal("Ann's own address receives alerts at once: it is her verified account address")
	}
	notice := tm.lastTo(tm.owner.Email)
	if notice == nil || notice.Subject != "ann@example.com joined Acme Agency on HawkEye" || !strings.Contains(notice.Text, "alert recipients list") {
		t.Fatalf("the owner is told: %+v", notice)
	}

	_, res = tm.join(t, "bob@example.com", false)
	if res.Alerts != "" || tm.count(t, `SELECT count(*) FROM notification_settings WHERE destination = 'bob@example.com'`) != 0 {
		t.Fatalf("Bob did not ask for alerts: %+v", res)
	}
	if n := tm.lastTo(tm.owner.Email); n == nil || !strings.Contains(n.Subject, "bob@example.com joined") || strings.Contains(n.Text, "alert recipients list") {
		t.Fatalf("%+v", n)
	}
}

func TestAlertSignUpLinksAnExistingAddressAndRespectsTheLimit(t *testing.T) {
	tm := newTeam(t)
	// The owner had already added Cara's address, still waiting for her to confirm.
	tm.pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination) VALUES ($1, 'email', 'cara@example.com')`, tm.org)
	cara, res := tm.join(t, "cara@example.com", true)
	if res.Alerts != invitations.AlertsAlready || tm.count(t, `SELECT count(*) FROM notification_settings WHERE destination = 'cara@example.com' AND user_id = $1 AND verified_at IS NOT NULL`, cara.ID) != 1 {
		t.Fatalf("the existing entry is confirmed and linked, not duplicated: %+v", res)
	}

	for i := tm.count(t, `SELECT count(*) FROM notification_settings WHERE organization_id = $1`, tm.org); i < 10; i++ {
		tm.pool.Exec(t.Context(), `INSERT INTO notification_settings (organization_id, channel, destination, verified_at) VALUES ($1, 'email', $2, now())`, tm.org, uuid.NewString()+"@example.com")
	}
	_, res = tm.join(t, "dan@example.com", true)
	if res.Alerts != invitations.AlertsFull || res.Organization.ID != tm.org {
		t.Fatalf("Dan still joins when the list is full: %+v", res)
	}
	if n := tm.lastTo(tm.owner.Email); n == nil || !strings.Contains(n.Text, "list is full") {
		t.Fatalf("%+v", n)
	}
}

func TestLeavingTheTeamStopsTheirAlerts(t *testing.T) {
	tm := newTeam(t)
	ann, _ := tm.join(t, "ann@example.com", true)
	bob, _ := tm.join(t, "bob@example.com", true)
	if err := tm.orgs.RemoveMember(t.Context(), tm.owner.ID, tm.org, ann.ID); err != nil {
		t.Fatal(err)
	}
	if err := tm.orgs.Leave(t.Context(), bob.ID, tm.org); err != nil {
		t.Fatal(err)
	}
	if tm.count(t, `SELECT count(*) FROM notification_settings WHERE organization_id = $1 AND user_id IN ($2, $3)`, tm.org, ann.ID, bob.ID) != 0 {
		t.Fatal("removed and departed members receive no more alerts")
	}
	if tm.count(t, `SELECT count(*) FROM notification_settings WHERE user_id = $1`, tm.owner.ID) != 1 {
		t.Fatal("the owner's own alerts are untouched")
	}
}

func TestTransferringOwnership(t *testing.T) {
	tm := newTeam(t)
	ann, _ := tm.join(t, "ann@example.com", false)
	unverified := tm.user(t, "new@example.com", false)
	tm.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'member')`, tm.org, unverified.ID)
	stranger := tm.user(t, "stranger@example.com", true)

	for name, c := range map[string]struct {
		to      uuid.UUID
		confirm string
		want    error
	}{
		"wrong name":          {ann.ID, "Acme", invitations.ErrConfirmMismatch},
		"not a member":        {stranger.ID, "Acme Agency", organizations.ErrMemberNotFound},
		"to the owner":        {tm.owner.ID, "Acme Agency", organizations.ErrMemberNotFound},
		"unconfirmed address": {unverified.ID, "Acme Agency", invitations.ErrNewOwnerNotVerified},
	} {
		if _, err := tm.svc.Transfer(t.Context(), tm.org, tm.owner, c.to, c.confirm); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	addr, err := tm.svc.Transfer(t.Context(), tm.org, tm.owner, ann.ID, "  acme agency ")
	if err != nil || addr != ann.Email {
		t.Fatalf("%q %v", addr, err)
	}
	if o, _ := tm.orgs.Get(t.Context(), ann.ID, tm.org); o.Role != "owner" {
		t.Fatalf("Ann owns it: %+v", o)
	}
	if o, _ := tm.orgs.Get(t.Context(), tm.owner.ID, tm.org); o.Role != "member" {
		t.Fatalf("the old owner stays as a member: %+v", o)
	}
	if tm.count(t, `SELECT count(*) FROM organization_members WHERE organization_id = $1 AND role = 'owner'`, tm.org) != 1 {
		t.Fatal("exactly one owner")
	}
	if m := tm.lastTo(ann.Email); m == nil || !strings.Contains(m.Subject, "You are now the owner of Acme Agency") {
		t.Fatalf("%+v", m)
	}
	if m := tm.lastTo(tm.owner.Email); m == nil || !strings.Contains(m.Text, "If you did not do this") {
		t.Fatalf("the old owner gets a security notice: %+v", m)
	}
	// The old owner's stale page cannot transfer it again, and can now be removed like any member.
	if _, err := tm.svc.Transfer(t.Context(), tm.org, tm.owner, unverified.ID, "Acme Agency"); !errors.Is(err, organizations.ErrForbidden) {
		t.Fatalf("%v", err)
	}
	if err := tm.orgs.RemoveMember(t.Context(), ann.ID, tm.org, tm.owner.ID); err != nil {
		t.Fatalf("%v", err)
	}
}

func TestTheDatabaseAllowsOneOwner(t *testing.T) {
	tm := newTeam(t)
	ann, _ := tm.join(t, "ann@example.com", false)
	if _, err := tm.pool.Exec(t.Context(), `UPDATE organization_members SET role = 'owner' WHERE organization_id = $1 AND user_id = $2`, tm.org, ann.ID); err == nil {
		t.Fatal("a second owner must be refused by the database")
	}
}
