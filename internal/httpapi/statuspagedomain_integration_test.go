package httpapi_test

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"hawkeye/internal/statuspagedomains"
)

// fakeZone is the DNS a test edits: TXT values by name, and the names whose CNAME points at the target (domains.app.test).
type fakeZone struct {
	mu   sync.Mutex
	txt  map[string][]string
	ours map[string]bool
}

func newZone() *fakeZone { return &fakeZone{txt: map[string][]string{}, ours: map[string]bool{}} }

func nxdomain(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (z *fakeZone) LookupTXT(_ context.Context, name string) ([]string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if v, ok := z.txt[name]; ok {
		return v, nil
	}
	return nil, nxdomain(name)
}

func (z *fakeZone) LookupCNAME(_ context.Context, host string) (string, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.ours[host] {
		return "domains.app.test.", nil
	}
	return "", nxdomain(host)
}

func (z *fakeZone) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	if host == "domains.app.test" || z.ours[host] {
		return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
	}
	return nil, nxdomain(host)
}

// publish adds the records an API response asked for.
func (z *fakeZone) publish(records []any) {
	z.mu.Lock()
	defer z.mu.Unlock()
	for _, r := range records {
		rec := r.(map[string]any)
		name, value := rec["name"].(string), rec["value"].(string)
		if rec["type"] == "TXT" {
			z.txt[name] = append(z.txt[name], value)
		} else {
			z.ours[name] = true
		}
	}
}

// dnsOrEmpty is the resolver the test API uses: the test's own zone, or one where no name exists.
func dnsOrEmpty(r statuspagedomains.Resolver) statuspagedomains.Resolver {
	if r != nil {
		return r
	}
	return newZone()
}

func TestStatusPageDomainEndpoints(t *testing.T) {
	zone := newZone()
	e := newEnvOpts(t, 100000, envOpts{dns: zone})
	owner, org := e.register(t, "owner@example.com")
	e.markVerified(t, "owner@example.com")
	r := owner.call("POST", "/api/v1/status-pages", map[string]any{"slug": "acme", "title": "Acme status"})
	if r.Status != 201 {
		t.Fatalf("create page: %d %v", r.Status, r.Body)
	}
	pageID := r.Body["status_page"].(map[string]any)["id"].(string)
	base := "/api/v1/status-pages/" + pageID + "/domain"

	// Free and Pro see that the feature is not in their plan.
	if r := owner.call("GET", base, nil); r.Status != 200 || r.Body["domain"] != nil || r.Body["max"] != float64(0) || r.Body["target"] != "domains.app.test" {
		t.Fatalf("Free: %d %v", r.Status, r.Body)
	}
	e.setOrgPlan(t, org, "pro")
	if r := owner.call("PUT", base, map[string]any{"hostname": "status.acme.com"}); r.Status != 403 || errCode(r) != "PLAN_LIMIT_REACHED" {
		t.Fatalf("Pro may not set a domain: %d %v", r.Status, r.Body)
	}

	e.setOrgPlan(t, org, "agency")
	if r := owner.call("PUT", base, map[string]any{"hostname": "acme.com"}); r.Status != 400 || !strings.Contains(r.Body["error"].(map[string]any)["message"].(string)+
		strings.Join(fieldValues(r), " "), "subdomain") {
		t.Fatalf("an apex is refused with a suggestion: %d %v", r.Status, r.Body)
	}
	r = owner.call("PUT", base, map[string]any{"hostname": "Status.Acme.com"})
	if r.Status != 200 || r.Body["max"] != float64(50) {
		t.Fatalf("Agency sets a domain: %d %v", r.Status, r.Body)
	}
	d := r.Body["domain"].(map[string]any)
	records := d["records"].([]any)
	if d["hostname"] != "status.acme.com" || d["state"] != "pending" || len(records) != 2 {
		t.Fatalf("domain: %v", d)
	}
	if _, leaked := d["token"]; leaked {
		t.Fatal("the token is only in the TXT record's value")
	}

	if r := owner.call("POST", base+"/check", nil); r.Status != 200 || r.Body["domain"].(map[string]any)["state"] != "pending" ||
		!strings.Contains(r.Body["domain"].(map[string]any)["last_error"].(string), "TXT record") {
		t.Fatalf("checked before the records exist: %d %v", r.Status, r.Body)
	}
	zone.publish(records)
	if r := owner.call("POST", base+"/check", nil); r.Status != 200 || r.Body["domain"].(map[string]any)["state"] != "active" {
		t.Fatalf("checked with the records in place: %d %v", r.Status, r.Body)
	}

	// The page itself shows the domain, and saving it without the redirect setting keeps that setting.
	page := owner.call("GET", "/api/v1/status-pages/"+pageID, nil).Body["status_page"].(map[string]any)
	if page["custom_domain"] != "status.acme.com" || page["custom_domain_state"] != "active" || page["redirect_to_domain"] != false {
		t.Fatalf("page: %v", page)
	}
	save := map[string]any{"slug": "acme", "title": "Acme status", "redirect_to_domain": true}
	if r := owner.call("PUT", "/api/v1/status-pages/"+pageID, save); r.Status != 200 || r.Body["status_page"].(map[string]any)["redirect_to_domain"] != true {
		t.Fatalf("turn the redirect on: %d %v", r.Status, r.Body)
	}
	delete(save, "redirect_to_domain")
	save["title"] = "Acme"
	if r := owner.call("PUT", "/api/v1/status-pages/"+pageID, save); r.Status != 200 || r.Body["status_page"].(map[string]any)["redirect_to_domain"] != true {
		t.Fatalf("a save that leaves it out keeps it: %d %v", r.Status, r.Body)
	}

	// Another organization can neither see this page's domain nor take the name.
	other, otherOrg := e.register(t, "other@example.com")
	e.markVerified(t, "other@example.com")
	e.setOrgPlan(t, otherOrg, "agency")
	if r := other.call("GET", base, nil); r.Status != 404 {
		t.Fatalf("another organization's page: %d", r.Status)
	}
	r = other.call("POST", "/api/v1/status-pages", map[string]any{"slug": "other", "title": "Other"})
	otherBase := "/api/v1/status-pages/" + r.Body["status_page"].(map[string]any)["id"].(string) + "/domain"
	if r := other.call("PUT", otherBase, map[string]any{"hostname": "status.acme.com"}); r.Status != 409 || errCode(r) != "DOMAIN_IN_USE" {
		t.Fatalf("a served name is taken: %d %v", r.Status, r.Body)
	}

	for _, action := range []string{"status_page.domain_set", "status_page.domain_verified"} {
		var n int
		if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = $1 AND details->>'hostname' = 'status.acme.com'`, action).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s logged %d times: %v", action, n, err)
		}
	}

	if r := owner.call("DELETE", base, nil); r.Status != 204 {
		t.Fatalf("remove: %d %v", r.Status, r.Body)
	}
	if r := owner.call("GET", base, nil); r.Body["domain"] != nil {
		t.Fatalf("removed: %v", r.Body)
	}
	if r := owner.call("DELETE", base, nil); r.Status != 404 {
		t.Fatalf("remove twice: %d", r.Status)
	}
	if r := other.call("PUT", otherBase, map[string]any{"hostname": "status.acme.com"}); r.Status != 200 {
		t.Fatalf("once removed, the name is free: %d %v", r.Status, r.Body)
	}
}

// fieldValues lists a validation error's field messages.
func fieldValues(r resp) []string {
	var out []string
	if e, ok := r.Body["error"].(map[string]any); ok {
		if d, ok := e["details"].(map[string]any); ok {
			if f, ok := d["fields"].(map[string]any); ok {
				for _, v := range f {
					out = append(out, v.(string))
				}
			}
		}
	}
	return out
}
