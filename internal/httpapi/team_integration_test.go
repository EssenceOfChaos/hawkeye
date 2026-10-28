package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// callIn sends a protected request on behalf of one organization, as the SPA does once someone has more than one.
func (c *client) callIn(orgID, method, path string, body any) resp {
	c.t.Helper()
	if c.csrf() == "" {
		c.do(http.MethodGet, "/api/v1/auth/me", nil, false)
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	req.Header.Set("X-Organization-ID", orgID)
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return resp{res.StatusCode, m}
}

// teamEnv is an Agency owner (verified) who has not invited anyone yet.
func teamEnv(t *testing.T) (*env, *client, string) {
	t.Helper()
	e := newEnv(t, 1000)
	owner, org := e.register(t, "owner@example.com")
	e.verify(t, owner, "owner@example.com")
	if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'agency') WHERE organization_id = $1`, org); err != nil {
		t.Fatal(err)
	}
	return e, owner, org
}

// invite sends an invitation and returns the token from the email.
func (e *env) invite(t *testing.T, owner *client, org, to string) string {
	t.Helper()
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": to}); r.Status != 201 {
		t.Fatalf("invite %s: %d %v", to, r.Status, r.Body)
	}
	return e.lastToken(t, strings.ToLower(to), "invited you") // the email goes to the normalised address
}

// join registers (or signs in) the invitee, verifies the address and accepts the invitation.
func (e *env) join(t *testing.T, to, token string) (*client, string) {
	t.Helper()
	c, personal := e.register(t, to)
	e.verify(t, c, to)
	r := c.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token})
	if r.Status != 200 {
		t.Fatalf("accept: %d %v", r.Status, r.Body)
	}
	return c, personal
}

func TestOwnerInvitesAndInviteeJoinsAsMember(t *testing.T) {
	e, owner, org := teamEnv(t)
	token := e.invite(t, owner, org, "Colleague@Example.com") // the address is normalised

	// The email goes to that address and says who invited them and to what.
	var found bool
	for _, m := range e.mail.Messages() {
		if m.To == "colleague@example.com" && strings.Contains(m.Subject, "owner@example.com invited you") && strings.Contains(m.Text, "/accept-invite?token="+token) {
			found = true
		}
	}
	if !found {
		t.Fatalf("invitation email not found: %+v", e.mail.Messages())
	}

	list := owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil)
	invs := list.Body["invitations"].([]any)
	if list.Status != 200 || len(invs) != 1 || invs[0].(map[string]any)["email"] != "colleague@example.com" {
		t.Fatalf("list: %d %v", list.Status, list.Body)
	}

	// Anyone holding the link can see which organization it is for and which address it was sent to, without signing in.
	pv := e.newClient(t).call("POST", "/api/v1/invitations/preview", map[string]string{"token": token})
	inv := pv.Body["invitation"].(map[string]any)
	if pv.Status != 200 || inv["email"] != "colleague@example.com" || inv["organization_name"] != "owner's organization" {
		t.Fatalf("preview: %d %v", pv.Status, pv.Body)
	}

	colleague, personal := e.join(t, "colleague@example.com", token)

	// The colleague now belongs to two organizations: their own, and the team's, as a member.
	orgs := colleague.call("GET", "/api/v1/organizations", nil).Body["organizations"].([]any)
	roles := map[string]string{}
	for _, o := range orgs {
		m := o.(map[string]any)
		roles[m["id"].(string)] = m["role"].(string)
	}
	if len(roles) != 2 || roles[org] != "member" || roles[personal] != "owner" {
		t.Fatalf("roles: %v", roles)
	}

	// They see the team's monitors when they act for it, and a member may manage monitors like any member.
	m := newMonitorID(t, owner, "Client site")
	got := colleague.callIn(org, "GET", "/api/v1/monitors", nil)
	if got.Status != 200 || got.Body["total"] != float64(1) {
		t.Fatalf("member sees team monitors: %d %v", got.Status, got.Body)
	}
	if r := colleague.callIn(org, "POST", "/api/v1/monitors/"+m+"/pause", nil); r.Status != 200 {
		t.Fatalf("member pauses a monitor: %d %v", r.Status, r.Body)
	}
	// Acting for their own organization still shows only their own (empty) list.
	if own := colleague.callIn(personal, "GET", "/api/v1/monitors", nil); own.Body["total"] != float64(0) {
		t.Fatalf("own org: %v", own.Body)
	}
	// With no header the default is their own organization, even though the team's is older.
	if def := colleague.call("GET", "/api/v1/monitors", nil); def.Status != 200 || def.Body["total"] != float64(0) {
		t.Fatalf("default organization must be their own, not the older team: %d %v", def.Status, def.Body)
	}

	// The invitation is used up, the member list shows both people, and the link no longer works.
	if left := owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil).Body["invitations"].([]any); len(left) != 0 {
		t.Fatalf("invitation should be gone: %v", left)
	}
	members := owner.call("GET", "/api/v1/organizations/"+org+"/members", nil).Body["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members: %v", members)
	}
	if r := colleague.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 400 || errCode(r) != "INVALID_TOKEN" {
		t.Fatalf("a used link must not work again: %d %v", r.Status, r.Body)
	}
}

func TestMembersCannotDoOwnerThings(t *testing.T) {
	e, owner, org := teamEnv(t)
	member, _ := e.join(t, "m@example.com", e.invite(t, owner, org, "m@example.com"))

	for name, r := range map[string]resp{
		"invite":         member.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "x@example.com"}),
		"list invites":   member.call("GET", "/api/v1/organizations/"+org+"/invitations", nil),
		"rename org":     member.call("PATCH", "/api/v1/organizations/"+org, map[string]string{"name": "Mine now"}),
		"billing":        member.callIn(org, "POST", "/api/v1/billing/portal-session", nil),
		"add recipient":  member.callIn(org, "POST", "/api/v1/notification-settings", map[string]string{"destination": "z@example.com"}),
		"remove someone": member.call("DELETE", "/api/v1/organizations/"+org+"/members/"+userID(t, owner), nil),
	} {
		if r.Status != 403 {
			t.Errorf("%s: want 403 got %d %v", name, r.Status, r.Body)
		}
	}
}

func userID(t *testing.T, c *client) string {
	t.Helper()
	return c.call("GET", "/api/v1/auth/me", nil).Body["user"].(map[string]any)["id"].(string)
}

func TestInvitationLinkIsBoundToItsAddress(t *testing.T) {
	e, owner, org := teamEnv(t)
	token := e.invite(t, owner, org, "right@example.com")

	// Someone else, even with the link and a verified account, cannot use it, and trying does not use it up.
	other, _ := e.register(t, "other@example.com")
	e.verify(t, other, "other@example.com")
	if r := other.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 403 || errCode(r) != "INVITATION_EMAIL_MISMATCH" {
		t.Fatalf("wrong account: %d %v", r.Status, r.Body)
	}
	if r := other.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 404 {
		t.Fatalf("the wrong account must not have joined: %d", r.Status)
	}

	// The right address, but not confirmed yet, is not enough either: the link must not be usable by someone who merely registered the address.
	right, _ := e.register(t, "right@example.com")
	if r := right.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("unverified: %d %v", r.Status, r.Body)
	}
	e.verify(t, right, "right@example.com")
	if r := right.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 200 {
		t.Fatalf("the right, verified account can still accept: %d %v", r.Status, r.Body)
	}

	// Not signed in at all.
	if r := e.newClient(t).call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 401 {
		t.Fatalf("anonymous accept: %d", r.Status)
	}
	// A guessed token.
	if r := other.call("POST", "/api/v1/invitations/accept", map[string]string{"token": "not-a-real-token"}); r.Status != 400 {
		t.Fatalf("bad token: %d", r.Status)
	}
	if r := e.newClient(t).call("POST", "/api/v1/invitations/preview", map[string]string{"token": "not-a-real-token"}); r.Status != 400 || errCode(r) != "INVALID_TOKEN" {
		t.Fatalf("bad preview: %d", r.Status)
	}
}

func TestSendingAgainReplacesTheLinkAndExpiredLinksFail(t *testing.T) {
	e, owner, org := teamEnv(t)
	first := e.invite(t, owner, org, "late@example.com")
	second := e.invite(t, owner, org, "late@example.com")
	if first == second {
		t.Fatal("a new link must be issued")
	}
	if n := len(owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil).Body["invitations"].([]any)); n != 1 {
		t.Fatalf("one pending invitation per address, got %d", n)
	}
	if r := e.newClient(t).call("POST", "/api/v1/invitations/preview", map[string]string{"token": first}); r.Status != 400 {
		t.Fatalf("the replaced link must stop working: %d", r.Status)
	}

	// Resend from the list does the same.
	id := owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil).Body["invitations"].([]any)[0].(map[string]any)["id"].(string)
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations/"+id+"/resend", nil); r.Status != 200 {
		t.Fatalf("resend: %d %v", r.Status, r.Body)
	}
	third := e.lastToken(t, "late@example.com", "invited you")
	if third == second {
		t.Fatal("resend must issue a new link")
	}

	// Past its expiry the link is dead and the invitation holds no seat.
	if _, err := e.pool.Exec(t.Context(), `UPDATE organization_invitations SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if r := e.newClient(t).call("POST", "/api/v1/invitations/preview", map[string]string{"token": third}); r.Status != 400 {
		t.Fatalf("expired preview: %d", r.Status)
	}
	late, _ := e.register(t, "late@example.com")
	e.verify(t, late, "late@example.com")
	if r := late.call("POST", "/api/v1/invitations/accept", map[string]string{"token": third}); r.Status != 400 {
		t.Fatalf("expired accept: %d", r.Status)
	}
	if n := len(owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil).Body["invitations"].([]any)); n != 0 {
		t.Fatalf("expired invitations are not listed: %d", n)
	}
	// And inviting the same address again works.
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "late@example.com"}); r.Status != 201 {
		t.Fatalf("re-invite after expiry: %d %v", r.Status, r.Body)
	}
}

func TestSeatsAreLimitedByPlanAndPendingInvitationsHoldOne(t *testing.T) {
	e, owner, org := teamEnv(t)
	// Agency has 5 seats and the owner holds one.
	for i := 1; i <= 4; i++ {
		e.invite(t, owner, org, fmt.Sprintf("p%d@example.com", i))
	}
	r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "p5@example.com"})
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("fifth invitation: %d %v", r.Status, r.Body)
	}
	d := r.Body["error"].(map[string]any)["details"].(map[string]any)
	if d["limit_type"] != "team_members" || d["limit"] != float64(5) || d["used"] != float64(5) {
		t.Fatalf("details: %v", d)
	}
	// Sending a link again to someone already invited needs no extra seat.
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "p1@example.com"}); r.Status != 201 {
		t.Fatalf("re-send within limit: %d %v", r.Status, r.Body)
	}
	// Revoking one frees its seat, and its link stops working.
	list := owner.call("GET", "/api/v1/organizations/"+org+"/invitations", nil).Body["invitations"].([]any)
	revokedToken := e.lastToken(t, "p2@example.com", "invited you")
	for _, i := range list {
		if i.(map[string]any)["email"] == "p2@example.com" {
			if r := owner.call("DELETE", "/api/v1/organizations/"+org+"/invitations/"+i.(map[string]any)["id"].(string), nil); r.Status != 204 {
				t.Fatalf("revoke: %d", r.Status)
			}
		}
	}
	if r := e.newClient(t).call("POST", "/api/v1/invitations/preview", map[string]string{"token": revokedToken}); r.Status != 400 {
		t.Fatalf("a revoked link must stop working: %d", r.Status)
	}
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "p5@example.com"}); r.Status != 201 {
		t.Fatalf("seat freed by revoking: %d %v", r.Status, r.Body)
	}
}

func TestFreeAndProOwnersCannotInvite(t *testing.T) {
	for _, plan := range []string{"free", "pro"} {
		e := newEnv(t, 1000)
		owner, org := e.register(t, "solo@example.com")
		e.verify(t, owner, "solo@example.com")
		if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = $1)`, plan); err != nil {
			t.Fatal(err)
		}
		r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "friend@example.com"})
		if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" || !strings.Contains(r.Body["error"].(map[string]any)["message"].(string), "one person") {
			t.Fatalf("%s: %d %v", plan, r.Status, r.Body)
		}
		for _, m := range e.mail.Messages() {
			if m.To == "friend@example.com" {
				t.Fatalf("%s: nothing may be sent when the plan has no seat", plan)
			}
		}
	}
}

func TestInvitationValidationAndAbuseGuards(t *testing.T) {
	e, owner, org := teamEnv(t)
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "not-an-email"}); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("invalid address: %d %v", r.Status, r.Body)
	}
	// Inviting yourself, or someone already on the team.
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "OWNER@example.com"}); r.Status != 409 || errCode(r) != "ALREADY_MEMBER" {
		t.Fatalf("self: %d %v", r.Status, r.Body)
	}
	e.join(t, "in@example.com", e.invite(t, owner, org, "in@example.com"))
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "in@example.com"}); r.Status != 409 || errCode(r) != "ALREADY_MEMBER" {
		t.Fatalf("existing member: %d %v", r.Status, r.Body)
	}

	// An unverified owner cannot send mail to other people.
	un := newEnv(t, 1000)
	rookie, rookieOrg := un.register(t, "rookie@example.com")
	un.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'agency')`)
	if r := rookie.call("POST", "/api/v1/organizations/"+rookieOrg+"/invitations", map[string]string{"email": "x@example.com"}); r.Status != 403 || errCode(r) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("unverified inviter: %d %v", r.Status, r.Body)
	}

	// A stranger gets "not found", not "forbidden", so organizations cannot be probed.
	stranger, _ := e.register(t, "stranger@example.com")
	e.verify(t, stranger, "stranger@example.com")
	for name, r := range map[string]resp{
		"list":   stranger.call("GET", "/api/v1/organizations/"+org+"/invitations", nil),
		"invite": stranger.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "x@example.com"}),
	} {
		if r.Status != 404 {
			t.Errorf("stranger %s: want 404 got %d", name, r.Status)
		}
	}
}

func TestOwnerRemovesMemberAndMemberLeaves(t *testing.T) {
	e, owner, org := teamEnv(t)
	a, _ := e.join(t, "a@example.com", e.invite(t, owner, org, "a@example.com"))
	b, _ := e.join(t, "b@example.com", e.invite(t, owner, org, "b@example.com"))
	ownerID, aID := userID(t, owner), userID(t, a)

	// The owner cannot be removed (by themselves or anyone), and cannot leave.
	if r := owner.call("DELETE", "/api/v1/organizations/"+org+"/members/"+ownerID, nil); r.Status != 409 || errCode(r) != "OWNER_PROTECTED" {
		t.Fatalf("remove owner: %d %v", r.Status, r.Body)
	}
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/leave", nil); r.Status != 409 || errCode(r) != "OWNER_PROTECTED" {
		t.Fatalf("owner leaves: %d %v", r.Status, r.Body)
	}
	// A member cannot remove another member.
	if r := b.call("DELETE", "/api/v1/organizations/"+org+"/members/"+aID, nil); r.Status != 403 {
		t.Fatalf("member removes member: %d", r.Status)
	}

	// The owner removes a: their access ends on the very next request, and they keep their own organization.
	if r := owner.call("DELETE", "/api/v1/organizations/"+org+"/members/"+aID, nil); r.Status != 204 {
		t.Fatalf("remove: %d %v", r.Status, r.Body)
	}
	if r := a.callIn(org, "GET", "/api/v1/monitors", nil); r.Status != 404 {
		t.Fatalf("removed member must lose access at once: %d", r.Status)
	}
	if r := a.call("GET", "/api/v1/monitors", nil); r.Status != 200 {
		t.Fatalf("their own organization is untouched: %d", r.Status)
	}
	if r := owner.call("DELETE", "/api/v1/organizations/"+org+"/members/"+aID, nil); r.Status != 404 {
		t.Fatalf("removing twice: %d", r.Status)
	}

	// b leaves on their own, which frees the seat for someone new.
	if r := b.call("POST", "/api/v1/organizations/"+org+"/leave", nil); r.Status != 204 {
		t.Fatalf("leave: %d %v", r.Status, r.Body)
	}
	if members := owner.call("GET", "/api/v1/organizations/"+org+"/members", nil).Body["members"].([]any); len(members) != 1 {
		t.Fatalf("only the owner remains: %v", members)
	}
	if r := owner.call("POST", "/api/v1/organizations/"+org+"/invitations", map[string]string{"email": "a@example.com"}); r.Status != 201 {
		t.Fatalf("a removed person can be invited again: %d %v", r.Status, r.Body)
	}
	// A stranger cannot leave an organization they are not in.
	stranger, _ := e.register(t, "s@example.com")
	if r := stranger.call("POST", "/api/v1/organizations/"+org+"/leave", nil); r.Status != 404 {
		t.Fatalf("stranger leaves: %d", r.Status)
	}
}

func TestDowngradeKeepsMembersButStopsNewOnesJoining(t *testing.T) {
	e, owner, org := teamEnv(t)
	token := e.invite(t, owner, org, "late@example.com")
	// The owner downgrades before the invitee accepts: the seat the invitation held is gone.
	if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'free') WHERE organization_id = $1`, org); err != nil {
		t.Fatal(err)
	}
	late, _ := e.register(t, "late@example.com")
	e.verify(t, late, "late@example.com")
	if r := late.call("POST", "/api/v1/invitations/accept", map[string]string{"token": token}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("accept after downgrade: %d %v", r.Status, r.Body)
	}
}

// A member's personal data export names the teams they belong to, but the team's websites and recipients are the owner's data.
func TestMembersExportDoesNotIncludeTheTeamsData(t *testing.T) {
	e, owner, org := teamEnv(t)
	newMonitorID(t, owner, "Client site")
	member, _ := e.join(t, "m@example.com", e.invite(t, owner, org, "m@example.com"))

	find := func(c *client) map[string]any {
		t.Helper()
		res, err := http.NewRequest("GET", c.base+"/api/v1/account/export", nil)
		if err != nil {
			t.Fatal(err)
		}
		r, err := c.http.Do(res)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var body struct {
			Organizations []map[string]any `json:"organizations"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, o := range body.Organizations {
			if o["id"] == org {
				return o
			}
		}
		t.Fatalf("organization %s not listed", org)
		return nil
	}
	if got := find(owner); len(got["monitors"].([]any)) != 1 {
		t.Fatalf("the owner's export has the team's monitors: %v", got["monitors"])
	}
	got := find(member)
	if got["role"] != "member" || len(got["monitors"].([]any)) != 0 || len(got["alert_recipients"].([]any)) != 0 || got["status_page"] != nil {
		t.Fatalf("a member's export must name the team and nothing more: %v", got)
	}
}
