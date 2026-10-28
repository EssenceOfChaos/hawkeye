package invitations_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/auth"
	"hawkeye/internal/email"
	"hawkeye/internal/invitations"
	"hawkeye/internal/plans"
	"hawkeye/internal/testutil"
)

// failingSender stands in for a mail server that is down.
type failingSender struct{}

func (failingSender) Send(context.Context, email.Message) error {
	return errors.New("smtp: connection refused")
}

func TestFailedEmailLeavesNoInvitationAndNoHeldSeat(t *testing.T) {
	pool := testutil.NewPool(t)
	ctx := t.Context()

	var owner auth.User
	var org uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('owner@example.com', 'x') RETURNING id, email`).Scan(&owner.ID, &owner.Email); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name) VALUES ('Team') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, org, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = 'agency'`, org); err != nil {
		t.Fatal(err)
	}

	svc := invitations.NewService(pool, plans.NewService("http://app.test"), failingSender{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "http://app.test")
	for range 10 { // far more attempts than the plan has seats: a failed send must never spend one
		if _, err := svc.Invite(ctx, org, owner, "friend@example.com"); !errors.Is(err, invitations.ErrSendFailed) {
			t.Fatalf("want ErrSendFailed, got %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_invitations`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an invitation nobody received must not be kept: n=%d err=%v", n, err)
	}
	if err := plans.NewService("").CanInviteTeamMember(ctx, pool, org); err != nil {
		t.Fatalf("seats must still be free: %v", err)
	}
}
