package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// raw sends a request with an arbitrary body and headers (the logo is sent as the image itself, and reports come back as a file). It sends the CSRF
// header like the web app does.
func (c *client) raw(method, path string, body []byte, headers map[string]string) (int, http.Header, []byte) {
	c.t.Helper()
	if c.csrf() == "" {
		c.do(http.MethodGet, "/api/v1/auth/me", nil, false)
	}
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", c.csrf())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func jsonOf(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %q", b)
	}
	return m
}

func setPlan(t *testing.T, e *env, code string) {
	t.Helper()
	if _, err := e.pool.Exec(t.Context(), `UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = $1)`, code); err != nil {
		t.Fatal(err)
	}
}

func logoPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 20, 120, 200, 255
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// seedLastMonth gives a monitor hourly checks through the whole of last month (UTC), so a report has something to show.
func seedLastMonth(t *testing.T, e *env, monitorID string) (month string) {
	t.Helper()
	now := time.Now().UTC()
	thisMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	prev := thisMonth.AddDate(0, -1, 0)
	if _, err := e.pool.Exec(t.Context(), `INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms)
		SELECT $1, ts, ts, true, 200, 120 FROM generate_series($2::timestamptz, $3::timestamptz - interval '1 hour', interval '1 hour') ts`, monitorID, prev, thisMonth); err != nil {
		t.Fatal(err)
	}
	return prev.Format("2006-01")
}

func TestReportsAreAPaidFeature(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	m := newMonitorID(t, c, "Client Site")
	month := seedLastMonth(t, e, m)

	status, _, body := c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil)
	if status != 403 || jsonOf(t, body)["error"].(map[string]any)["code"] != "PLAN_LIMIT_REACHED" {
		t.Fatalf("free plan: %d %s", status, body)
	}
	view := jsonOf(t, mustGet(t, c, "/api/v1/branding"))
	if view["can_report"] != false || view["can_brand"] != false {
		t.Fatalf("free plan flags: %v", view)
	}
	for name, in := range map[string]map[string]any{"name": {"display_name": "Mine"}, "monthly email": {"monthly_report_email": true}} {
		if r := c.call("PUT", "/api/v1/branding", in); r.Status != 403 {
			t.Errorf("free plan changed %s: %d", name, r.Status)
		}
	}
	if status, _, _ := c.raw("PUT", "/api/v1/branding/logo", logoPNG(t, 50, 20), nil); status != 403 {
		t.Fatalf("free plan uploaded a logo: %d", status)
	}
}

func mustGet(t *testing.T, c *client, path string) []byte {
	t.Helper()
	status, _, b := c.raw("GET", path, nil, nil)
	if status != 200 {
		t.Fatalf("GET %s = %d %s", path, status, b)
	}
	return b
}

func TestProDownloadsAReportButCannotBrandIt(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	setPlan(t, e, "pro")
	m := newMonitorID(t, c, "Client Site")
	month := seedLastMonth(t, e, m)

	status, h, body := c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil)
	if status != 200 || h.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF-")) {
		t.Fatalf("%d %q %q", status, h.Get("Content-Type"), body[:min(len(body), 40)])
	}
	if want := `attachment; filename="client-site-` + month + `.pdf"`; h.Get("Content-Disposition") != want {
		t.Fatalf("disposition = %q, want %q", h.Get("Content-Disposition"), want)
	}
	// No month: the last full month, the same report.
	if status, _, _ := c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf", nil, nil); status != 200 {
		t.Fatalf("default month: %d", status)
	}

	view := jsonOf(t, mustGet(t, c, "/api/v1/branding"))
	if view["can_report"] != true || view["can_brand"] != false {
		t.Fatalf("pro flags: %v", view)
	}
	if r := c.call("PUT", "/api/v1/branding", map[string]any{"monthly_report_email": true}); r.Status != 200 {
		t.Fatalf("pro turns on the monthly email: %d %v", r.Status, r.Body)
	}
	if r := c.call("PUT", "/api/v1/branding", map[string]any{"display_name": "My Agency"}); r.Status != 403 {
		t.Fatalf("pro set a brand name: %d", r.Status)
	}
	if status, _, _ := c.raw("PUT", "/api/v1/branding/logo", logoPNG(t, 50, 20), nil); status != 403 {
		t.Fatalf("pro uploaded a logo: %d", status)
	}
}

func TestReportRequestsAreValidatedAndScopedToTheOrganization(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	other, _ := e.register(t, "other@example.com")
	setPlan(t, e, "pro")
	m := newMonitorID(t, c, "Mine")
	month := seedLastMonth(t, e, m)

	next := time.Now().UTC().AddDate(0, 2, 0).Format("2006-01")
	for name, q := range map[string]string{"garbage": "month=banana", "future": "month=" + next, "ancient": "month=2001-01", "not a month": "month=2026-09-01"} {
		if status, _, body := c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?"+q, nil, nil); status != 400 {
			t.Errorf("%s: %d %s", name, status, body)
		}
	}
	if status, _, _ := c.raw("GET", "/api/v1/monitors/00000000-0000-0000-0000-000000000000/report.pdf?month="+month, nil, nil); status != 404 {
		t.Fatalf("unknown monitor: %d", status)
	}
	if status, _, _ := other.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil); status != 404 {
		t.Fatalf("another organization's monitor must look like it does not exist: %d", status)
	}
	if status, _, _ := e.newClient(t).raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil); status != 401 {
		t.Fatalf("signed out: %d", status)
	}
}

func TestAgencyBrandsItsReports(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	setPlan(t, e, "agency")
	m := newMonitorID(t, c, "Client Site")
	month := seedLastMonth(t, e, m)

	if r := c.call("PUT", "/api/v1/branding", map[string]any{"display_name": "  Northwind Digital  ", "monthly_report_email": true}); r.Status != 200 {
		t.Fatalf("%d %v", r.Status, r.Body)
	}
	status, _, body := c.raw("PUT", "/api/v1/branding/logo", logoPNG(t, 300, 90), map[string]string{"Content-Type": "image/png"})
	if status != 200 {
		t.Fatalf("upload: %d %s", status, body)
	}
	b := jsonOf(t, body)["branding"].(map[string]any)
	if b["display_name"] != "Northwind Digital" || b["has_logo"] != true || b["monthly_report_email"] != true || b["logo_width"] != float64(300) {
		t.Fatalf("branding = %v", b)
	}
	status, h, img := c.raw("GET", "/api/v1/branding/logo", nil, nil)
	if status != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("logo: %d %q", status, h.Get("Content-Type"))
	}
	if _, err := png.Decode(bytes.NewReader(img)); err != nil {
		t.Fatalf("the served logo is not a PNG: %v", err)
	}

	status, _, pdf := c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil)
	if status != 200 || !bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Fatalf("the branded report has no logo (%d)", status)
	}

	// After a downgrade the same link gives the standard report, and the stored logo is kept.
	setPlan(t, e, "pro")
	_, _, pdf = c.raw("GET", "/api/v1/monitors/"+m+"/report.pdf?month="+month, nil, nil)
	if bytes.Contains(pdf, []byte("/Subtype /Image")) {
		t.Fatal("a Pro report must not show the logo")
	}
	setPlan(t, e, "agency")
	if status, _, _ := c.raw("GET", "/api/v1/branding/logo", nil, nil); status != 200 {
		t.Fatal("the logo must survive a downgrade")
	}

	if status, _, body := c.raw("DELETE", "/api/v1/branding/logo", nil, nil); status != 200 || jsonOf(t, body)["branding"].(map[string]any)["has_logo"] != false {
		t.Fatalf("delete: %d %s", status, body)
	}
	if status, _, _ := c.raw("GET", "/api/v1/branding/logo", nil, nil); status != 404 {
		t.Fatalf("after delete: %d", status)
	}
	if r := c.call("PUT", "/api/v1/branding", map[string]any{"display_name": ""}); r.Status != 200 {
		t.Fatalf("clearing the name: %d", r.Status)
	}
}

func TestLogoUploadsAreValidated(t *testing.T) {
	e := newEnv(t, 1000)
	c, _ := e.register(t, "owner@example.com")
	setPlan(t, e, "agency")
	for name, body := range map[string][]byte{
		"svg":         []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"text":        []byte("hello"),
		"empty":       nil,
		"too large":   append(logoPNG(t, 20, 20), make([]byte, 600<<10)...),
		"too wide":    logoPNG(t, 2100, 4),
		"truncated":   logoPNG(t, 100, 100)[:50],
		"html as png": []byte("<html></html>"),
	} {
		status, _, resp := c.raw("PUT", "/api/v1/branding/logo", body, map[string]string{"Content-Type": "image/png"})
		if status != 400 || jsonOf(t, resp)["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", name, status, resp)
		}
	}
	if status, _, _ := c.raw("GET", "/api/v1/branding/logo", nil, nil); status != 404 {
		t.Fatal("a refused upload left something behind")
	}
	if r := c.call("PUT", "/api/v1/branding", map[string]any{"display_name": strings.Repeat("a", 101)}); r.Status != 400 {
		t.Fatalf("overlong name: %d", r.Status)
	}
	if r := c.call("PUT", "/api/v1/branding", map[string]any{"display_name": "two\nlines"}); r.Status != 400 {
		t.Fatalf("multi-line name: %d", r.Status)
	}
}

func TestBrandingBelongsToOneOrganizationAndOnlyOwnersChangeIt(t *testing.T) {
	e := newEnv(t, 1000)
	owner, orgA := e.register(t, "owner@example.com")
	other, _ := e.register(t, "other@example.com")
	member, _ := e.register(t, "member@example.com")
	setPlan(t, e, "agency")
	if status, _, _ := owner.raw("PUT", "/api/v1/branding/logo", logoPNG(t, 80, 30), nil); status != 200 {
		t.Fatalf("upload: %d", status)
	}
	// Another organization sees no logo of ours.
	if status, _, _ := other.raw("GET", "/api/v1/branding/logo", nil, nil); status != 404 {
		t.Fatalf("a logo leaked to another organization: %d", status)
	}
	// A plain member of our organization can look but not change.
	var memberID string
	e.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = 'member@example.com'`).Scan(&memberID)
	e.pool.Exec(context.Background(), `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'member')`, orgA, memberID)
	as := map[string]string{"X-Organization-ID": orgA}
	if status, _, _ := member.raw("GET", "/api/v1/branding", nil, as); status != 200 {
		t.Fatalf("a member may view: %d", status)
	}
	if status, _, _ := member.raw("DELETE", "/api/v1/branding/logo", nil, as); status != 403 {
		t.Fatalf("a member deleted the logo: %d", status)
	}
	if status, _, _ := member.raw("PUT", "/api/v1/branding/logo", logoPNG(t, 10, 10), as); status != 403 {
		t.Fatalf("a member uploaded a logo: %d", status)
	}
	body, _ := json.Marshal(map[string]any{"display_name": "Hijacked"})
	if status, _, _ := member.raw("PUT", "/api/v1/branding", body, map[string]string{"X-Organization-ID": orgA, "Content-Type": "application/json"}); status != 403 {
		t.Fatalf("a member renamed the brand: %d", status)
	}
}
