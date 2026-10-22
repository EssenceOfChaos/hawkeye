package httpapi_test

import (
	"testing"
	"time"
)

func domainNames(t *testing.T, c *client) (names []string, limit float64) {
	t.Helper()
	r := c.call("GET", "/api/v1/domains", nil)
	if r.Status != 200 {
		t.Fatalf("list: %d %v", r.Status, r.Body)
	}
	for _, d := range r.Body["domains"].([]any) {
		names = append(names, d.(map[string]any)["name"].(string))
	}
	return names, r.Body["limit"].(float64)
}

func TestDomainsFollowMonitorsAndCanBeAddedByOwners(t *testing.T) {
	e := newEnv(t, 1000)
	owner, orgID := e.register(t, "owner@example.com")

	if names, limit := domainNames(t, owner); len(names) != 0 || limit != 3 {
		t.Fatalf("new organization: %v, limit %v", names, limit)
	}

	// A monitor brings its domain.
	mon := owner.call("POST", "/api/v1/monitors", map[string]any{"name": "Shop", "url": "https://shop.example.co.uk/health"})
	if mon.Status != 201 {
		t.Fatalf("%d %v", mon.Status, mon.Body)
	}
	r := owner.call("GET", "/api/v1/domains", nil)
	d := r.Body["domains"].([]any)[0].(map[string]any)
	if d["name"] != "example.co.uk" || d["manual"] != false || d["monitor_count"].(float64) != 1 || d["status"] != "pending" || d["tracked"] != true || d["expires_at"] != nil {
		t.Fatalf("%v", d)
	}

	// Adding by hand: a URL is reduced to its registrable domain.
	r = owner.call("POST", "/api/v1/domains", map[string]any{"name": "https://www.acme.com/pricing"})
	if r.Status != 201 || r.Body["domain"].(map[string]any)["name"] != "acme.com" || r.Body["domain"].(map[string]any)["manual"] != true {
		t.Fatalf("add: %d %v", r.Status, r.Body)
	}
	acme := r.Body["domain"].(map[string]any)["id"].(string)
	if r := owner.call("POST", "/api/v1/domains", map[string]any{"name": "localhost"}); r.Status != 400 || errCode(r) != "VALIDATION_ERROR" {
		t.Fatalf("invalid: %d %v", r.Status, r.Body)
	}
	owner.call("POST", "/api/v1/domains", map[string]any{"name": "third.org"})
	r = owner.call("POST", "/api/v1/domains", map[string]any{"name": "fourth.io"})
	if r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("over the free limit: %d %v", r.Status, r.Body)
	}
	details := r.Body["error"].(map[string]any)["details"].(map[string]any)
	if details["limit_type"] != "domains" || details["limit"].(float64) != 3 || details["next_plan"].(map[string]any)["code"] != "pro" || details["next_plan"].(map[string]any)["max_domains"].(float64) != 25 {
		t.Fatalf("%v", details)
	}

	// A domain a monitor uses cannot be removed; one added by hand can.
	monitorDomain := owner.call("GET", "/api/v1/domains", nil).Body["domains"].([]any)
	var derived string
	for _, x := range monitorDomain {
		if x.(map[string]any)["name"] == "example.co.uk" {
			derived = x.(map[string]any)["id"].(string)
		}
	}
	if r := owner.call("DELETE", "/api/v1/domains/"+derived, nil); r.Status != 409 || errCode(r) != "DOMAIN_IN_USE" {
		t.Fatalf("derived: %d %v", r.Status, r.Body)
	}
	if r := owner.call("DELETE", "/api/v1/domains/"+acme, nil); r.Status != 204 {
		t.Fatalf("manual: %d", r.Status)
	}
	if r := owner.call("DELETE", "/api/v1/domains/"+acme, nil); r.Status != 404 {
		t.Fatalf("gone: %d", r.Status)
	}
	if r := owner.call("DELETE", "/api/v1/domains/not-a-uuid", nil); r.Status != 404 {
		t.Fatalf("bad id: %d", r.Status)
	}

	// Upgrading raises the limit.
	if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'pro')`); err != nil {
		t.Fatal(err)
	}
	if _, limit := domainNames(t, owner); limit != 25 {
		t.Fatalf("pro limit %v", limit)
	}

	// Members can look but not change; another organization sees none of it.
	bob, bobOrg := e.register(t, "bob@example.com")
	e.pool.Exec(t.Context(), `INSERT INTO organization_members (organization_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email='bob@example.com'`, orgID)
	if st := bob.doWithHeaderBody("GET", "/api/v1/domains", orgID, ""); st != 200 {
		t.Fatalf("member list: %d", st)
	}
	if st := bob.doWithHeaderBody("POST", "/api/v1/domains", orgID, `{"name":"evil.com"}`); st != 403 {
		t.Fatalf("member add: %d", st)
	}
	if st := bob.doWithHeaderBody("DELETE", "/api/v1/domains/"+derived, orgID, ""); st != 403 {
		t.Fatalf("member delete: %d", st)
	}
	if r := bob.doWithHeaderBody("GET", "/api/v1/domains", bobOrg, ""); r != 200 {
		t.Fatalf("own organization: %d", r)
	}
	if st := bob.doWithHeaderBody("DELETE", "/api/v1/domains/"+derived, bobOrg, ""); st != 404 {
		t.Fatalf("another organization's domain: %d", st)
	}
}

func TestDashboardSummaryListsDomainsThatExpireSoon(t *testing.T) {
	e := newEnv(t, 1000)
	c, org := e.register(t, "d@example.com")
	for _, u := range []string{"https://soon.example.com", "https://later.example.org", "https://unknown.example.net"} {
		if r := c.call("POST", "/api/v1/monitors", map[string]any{"name": "m", "url": u}); r.Status != 201 {
			t.Fatalf("%d %v", r.Status, r.Body)
		}
	}
	set := func(name string, in time.Duration) {
		if _, err := e.pool.Exec(t.Context(), `UPDATE domains SET status = 'known', expires_at = now() + $3::interval, checked_at = now() WHERE organization_id = $1 AND name = $2`,
			org, name, in); err != nil {
			t.Fatal(err)
		}
	}
	set("example.com", 9*24*time.Hour)
	set("example.org", 200*24*time.Hour)

	r := c.call("GET", "/api/v1/dashboard/summary", nil)
	if r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	s := r.Body["summary"].(map[string]any)
	list := s["domains"].([]any)
	if s["domains_tracked"].(float64) != 2 || len(list) != 1 {
		t.Fatalf("tracked %v, expiring %v", s["domains_tracked"], list)
	}
	d := list[0].(map[string]any)
	if d["name"] != "example.com" || d["status"] != "expiring" || d["days_remaining"].(float64) < 8 || d["days_remaining"].(float64) > 9 {
		t.Fatalf("%v", d)
	}
}
