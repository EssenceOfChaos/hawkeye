package statuspagedomains_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"hawkeye/internal/plans"
	"hawkeye/internal/statuspagedomains"
	"hawkeye/internal/testutil"
	"hawkeye/internal/validation"
)

const target = "domains.hawkeye.observer"

// zone is a DNS fake the test edits: TXT values by name, and the names whose CNAME points at the target.
type zone struct {
	txt  map[string][]string
	ours map[string]bool
}

func (z *zone) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := z.txt[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func (z *zone) LookupCNAME(_ context.Context, host string) (string, error) {
	if z.ours[host] {
		return target + ".", nil
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (z *zone) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if host == target || z.ours[host] {
		return []netip.Addr{netip.MustParseAddr("203.0.113.10")}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// publish adds the records a claim asks for, as its owner would.
func (z *zone) publish(d *statuspagedomains.Domain) {
	for _, r := range d.Records {
		if r.Type == "TXT" {
			z.txt[r.Name] = append(z.txt[r.Name], r.Value)
		} else {
			z.ours[r.Name] = true
		}
	}
}

type env struct {
	pool *pgxpool.Pool
	svc  *statuspagedomains.Service
	dns  *zone
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewPoolSize(t, 5)
	z := &zone{txt: map[string][]string{}, ours: map[string]bool{}}
	v := &statuspagedomains.Verifier{Resolver: z, Target: target}
	return &env{pool: pool, svc: statuspagedomains.NewService(pool, plans.NewService("http://app.test"), v, "hawkeye.observer"), dns: z}
}

// org creates an organization on a plan, with one status page per slug given.
func (e *env) org(t *testing.T, plan string, slugs ...string) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	var org uuid.UUID
	if err := e.pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('o') RETURNING id`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO subscriptions (organization_id, plan_id, status) SELECT $1, id, 'active' FROM plans WHERE code = $2`, org, plan); err != nil {
		t.Fatal(err)
	}
	pages := make([]uuid.UUID, len(slugs))
	for i, s := range slugs {
		if err := e.pool.QueryRow(t.Context(), `INSERT INTO status_pages (organization_id, slug, title) VALUES ($1, $2, 'Page') RETURNING id`, org, s).Scan(&pages[i]); err != nil {
			t.Fatal(err)
		}
	}
	return org, pages
}

func limitKind(err error) string {
	var le *plans.LimitError
	if errors.As(err, &le) {
		return le.Kind
	}
	return ""
}

func TestOnlyAgencyClaimsDomains(t *testing.T) {
	e := newEnv(t)
	for _, plan := range []string{"free", "pro"} {
		org, pages := e.org(t, plan, "page-"+plan)
		if _, err := e.svc.Claim(t.Context(), org, pages[0], "status.client.com"); limitKind(err) != plans.LimitPageDomains {
			t.Fatalf("%s may not claim a domain: %v", plan, err)
		}
	}
	org, pages := e.org(t, "agency", "agency-page")
	d, err := e.svc.Claim(t.Context(), org, pages[0], "https://Status.Client.com/")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hostname != "status.client.com" || d.State != statuspagedomains.StatePending || d.NextCheckAt == nil || len(d.Records) != 2 {
		t.Fatalf("a new claim is pending, due now, with two records: %+v", d)
	}
	if d.Records[0] != (statuspagedomains.Record{Type: "CNAME", Name: "status.client.com", Value: target}) ||
		d.Records[1].Name != "_hawkeye.status.client.com" || !strings.HasPrefix(d.Records[1].Value, "hawkeye-verify=") {
		t.Fatalf("records: %+v", d.Records)
	}
	if _, err := e.svc.Claim(t.Context(), org, pages[0], "client.com"); !errors.As(err, new(*validation.Error)) {
		t.Fatalf("an apex is refused: %v", err)
	}
	if _, err := e.svc.Claim(t.Context(), org, uuid.New(), "status.client.com"); !errors.Is(err, statuspagedomains.ErrNoPage) {
		t.Fatalf("another page id: %v", err)
	}
}

func TestPlanLimitCountsOtherPagesOnly(t *testing.T) {
	e := newEnv(t)
	org, pages := e.org(t, "agency", "page-a", "page-b")
	if _, err := e.pool.Exec(t.Context(), `UPDATE plans SET max_status_page_domains = 1 WHERE code = 'agency'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Claim(t.Context(), org, pages[0], "status.a.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Claim(t.Context(), org, pages[0], "uptime.a.com"); err != nil {
		t.Fatalf("replacing the page's own domain is within the limit: %v", err)
	}
	if _, err := e.svc.Claim(t.Context(), org, pages[1], "status.b.com"); limitKind(err) != plans.LimitPageDomains {
		t.Fatalf("a second page's domain is over the limit: %v", err)
	}
}

func TestClaimVerifyServeAndRemove(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	org, pages := e.org(t, "agency", "acme")
	d, err := e.svc.Claim(ctx, org, pages[0], "status.acme.com")
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.Claim(ctx, org, pages[0], "status.acme.com")
	if err != nil || again.Records[1].Value != d.Records[1].Value || again.ID != d.ID {
		t.Fatalf("claiming the same name again keeps the claim and its records: %v %+v", err, again)
	}

	got, ch, err := e.svc.CheckNow(ctx, org, pages[0])
	if err != nil || got.State != statuspagedomains.StatePending || ch != nil || !strings.Contains(got.LastError, "TXT record") {
		t.Fatalf("no records yet: %v %+v %+v", err, got, ch)
	}
	if _, ok, _ := e.svc.ServedPage(ctx, "status.acme.com"); ok {
		t.Fatal("a pending claim is not served")
	}

	e.dns.publish(d)
	got, ch, err = e.svc.CheckNow(ctx, org, pages[0])
	if err != nil || got.State != statuspagedomains.StateActive || got.VerifiedAt == nil || got.LastError != "" {
		t.Fatalf("records in place: %v %+v", err, got)
	}
	if ch == nil || ch.From != statuspagedomains.StatePending || ch.Domain.State != statuspagedomains.StateActive {
		t.Fatalf("the change is reported: %+v", ch)
	}
	if page, ok, err := e.svc.ServedPage(ctx, "Status.Acme.com."); err != nil || !ok || page != pages[0] {
		t.Fatalf("served: %v %v %v", page, ok, err)
	}

	// Replacing the name stops the old one at once and needs new records.
	nd, err := e.svc.Claim(ctx, org, pages[0], "uptime.acme.com")
	if err != nil || nd.State != statuspagedomains.StatePending || nd.Records[1].Value == d.Records[1].Value {
		t.Fatalf("a new name is a new claim with a new token: %v %+v", err, nd)
	}
	if _, ok, _ := e.svc.ServedPage(ctx, "status.acme.com"); ok {
		t.Fatal("the replaced name is no longer served")
	}

	if _, err := e.svc.Remove(ctx, org, pages[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, org, pages[0]); !errors.Is(err, statuspagedomains.ErrNotFound) {
		t.Fatalf("removed: %v", err)
	}
	if _, err := e.svc.Remove(ctx, org, pages[0]); !errors.Is(err, statuspagedomains.ErrNotFound) {
		t.Fatalf("removing twice: %v", err)
	}
}

func TestNamesAcrossOrganizations(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	owner, ownerPages := e.org(t, "agency", "real", "real-2")
	squatter, squatterPages := e.org(t, "agency", "squat")

	// A pending claim blocks nobody: the squatter claims first, but cannot write the TXT record.
	if _, err := e.svc.Claim(ctx, squatter, squatterPages[0], "status.brand.com"); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Claim(ctx, owner, ownerPages[0], "status.brand.com")
	if err != nil {
		t.Fatalf("the real owner can still claim it: %v", err)
	}
	e.dns.publish(d)
	if got, _, err := e.svc.CheckNow(ctx, squatter, squatterPages[0]); err != nil || got.State != statuspagedomains.StatePending {
		t.Fatalf("the owner's token does not verify the squatter: %v %+v", err, got)
	}
	if got, _, err := e.svc.CheckNow(ctx, owner, ownerPages[0]); err != nil || got.State != statuspagedomains.StateActive {
		t.Fatalf("the owner verifies: %v %+v", err, got)
	}

	// Once it is served, nobody else can claim it, and the same organization cannot put it on a second page.
	other, otherPages := e.org(t, "agency", "late")
	if _, err := e.svc.Claim(ctx, other, otherPages[0], "status.brand.com"); !errors.Is(err, statuspagedomains.ErrHostnameInUse) {
		t.Fatalf("a served name is taken: %v", err)
	}
	if _, err := e.svc.Claim(ctx, owner, ownerPages[1], "status.brand.com"); !errors.Is(err, statuspagedomains.ErrHostnameOnOtherPage) {
		t.Fatalf("one name, one page: %v", err)
	}

	// If the zone holds both tokens, the first verified claim keeps the name; the other stays pending and says why.
	sq, _ := e.svc.Get(ctx, squatter, squatterPages[0])
	e.dns.publish(sq)
	got, _, err := e.svc.CheckNow(ctx, squatter, squatterPages[0])
	if err != nil || got.State != statuspagedomains.StatePending || !strings.Contains(got.LastError, "already in use") {
		t.Fatalf("second verified claim of a served name: %v %+v", err, got)
	}

	// Organizations never see each other's claims.
	if _, err := e.svc.Get(ctx, squatter, ownerPages[0]); !errors.Is(err, statuspagedomains.ErrNotFound) {
		t.Fatalf("another organization's page: %v", err)
	}
	if _, err := e.svc.Remove(ctx, squatter, ownerPages[0]); !errors.Is(err, statuspagedomains.ErrNotFound) {
		t.Fatalf("removing another organization's domain: %v", err)
	}
	if _, ok, _ := e.svc.ServedPage(ctx, "status.brand.com"); !ok {
		t.Fatal("still served after the attempts above")
	}
}

func TestDeletingThePageDeletesItsDomain(t *testing.T) {
	e := newEnv(t)
	org, pages := e.org(t, "agency", "gone")
	if _, err := e.svc.Claim(t.Context(), org, pages[0], "status.gone.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(t.Context(), `DELETE FROM status_pages WHERE id = $1`, pages[0]); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM status_page_domains`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("domains left: %d %v", n, err)
	}
}
