package statuspage_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"hawkeye/internal/plans"
	"hawkeye/internal/statuspage"
	"hawkeye/internal/validation"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.NRGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (e *env) page(t *testing.T, cfg statuspage.Config) *statuspage.Config {
	t.Helper()
	m := e.monitor(t, "Website", "up")
	cfg.Monitors = []statuspage.MonitorConfig{{MonitorID: m}}
	c, err := e.svc.Create(t.Context(), e.org, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func limitKind(err error) string {
	var le *plans.LimitError
	if errors.As(err, &le) {
		return le.Kind
	}
	return ""
}

func TestWhatEachPlanMayBrand(t *testing.T) {
	branded := statuspage.Config{Slug: "acme", Title: "Acme", Published: true, AccentColor: "#1A73E8", WebsiteURL: "https://acme.example.com"}

	free := newEnv(t, "free")
	m := free.monitor(t, "Website", "up")
	branded.Monitors = []statuspage.MonitorConfig{{MonitorID: m}}
	if _, err := free.svc.Create(t.Context(), free.org, branded); limitKind(err) != plans.LimitPageBranding {
		t.Fatalf("Free cannot brand a page: %v", err)
	}
	if _, err := free.svc.Create(t.Context(), free.org, statuspage.Config{Slug: "acme", Title: "Acme", HidePoweredBy: true}); limitKind(err) != plans.LimitWhiteLabel {
		t.Fatalf("Free cannot hide Powered by: %v", err)
	}

	pro := newEnv(t, "pro")
	c := pro.page(t, branded)
	if c.AccentColor != "#1a73e8" || c.WebsiteURL != "https://acme.example.com" {
		t.Fatalf("Pro brands a page (colour stored lower-case): %+v", c)
	}
	c.HidePoweredBy = true
	if _, err := pro.svc.Update(t.Context(), pro.org, c.ID, *c); limitKind(err) != plans.LimitWhiteLabel {
		t.Fatalf("hiding Powered by is Agency only: %v", err)
	}
	p, _ := pro.svc.Public(t.Context(), "acme")
	if p.Branding.AccentColor != "#1a73e8" || p.Branding.WebsiteURL != "https://acme.example.com" || !p.Branding.PoweredBy {
		t.Fatalf("%+v", p.Branding)
	}

	agency := newEnv(t, "agency")
	branded.HidePoweredBy = true
	agency.page(t, branded)
	if p, _ := agency.svc.Public(t.Context(), "acme"); p.Branding.PoweredBy {
		t.Fatal("Agency hides Powered by")
	}
}

func TestBrandingIsKeptButHiddenAfterADowngrade(t *testing.T) {
	e := newEnv(t, "agency")
	c := e.page(t, statuspage.Config{Slug: "acme", Title: "Acme", Published: true, AccentColor: "#112233", WebsiteURL: "https://acme.example.com", HidePoweredBy: true})
	if err := e.svc.SaveLogo(t.Context(), e.org, c.ID, tinyPNG(t)); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.Public(t.Context(), "acme"); p.Branding.LogoURL == "" || !strings.HasPrefix(p.Branding.LogoURL, "/api/v1/public/status/acme/logo?v=") {
		t.Fatalf("%+v", p.Branding)
	}
	if _, _, err := e.svc.PublicLogo(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}

	e.pool.Exec(t.Context(), `UPDATE subscriptions SET status = 'canceled' WHERE organization_id = $1`, e.org)
	cur, _ := e.svc.Get(t.Context(), e.org, c.ID)
	cur.Title = "Acme status"
	saved, err := e.svc.Update(t.Context(), e.org, c.ID, *cur)
	if err != nil || saved.AccentColor != "#112233" || !saved.HidePoweredBy || !saved.HasLogo {
		t.Fatalf("the stored branding may be saved again unchanged: %+v %v", saved, err)
	}
	p, _ := e.svc.Public(t.Context(), "acme")
	if p.Branding != (statuspage.PublicBranding{PoweredBy: true}) {
		t.Fatalf("on Free the page has the standard look: %+v", p.Branding)
	}
	if _, _, err := e.svc.PublicLogo(t.Context(), "acme"); !errors.Is(err, statuspage.ErrNoLogo) {
		t.Fatalf("a kept logo is not served on Free: %v", err)
	}
	cur.AccentColor = "#445566"
	if _, err := e.svc.Update(t.Context(), e.org, c.ID, *cur); limitKind(err) != plans.LimitPageBranding {
		t.Fatalf("a new colour needs the plan again: %v", err)
	}
	cur.AccentColor, cur.WebsiteURL, cur.HidePoweredBy = "", "", false
	if _, err := e.svc.Update(t.Context(), e.org, c.ID, *cur); err != nil {
		t.Fatalf("clearing is always allowed: %v", err)
	}
}

func TestBrandingValidation(t *testing.T) {
	e := newEnv(t, "pro")
	m := e.monitor(t, "Website", "up")
	for field, cfg := range map[string]statuspage.Config{
		"accent_color": {AccentColor: "blue"},
		"website_url":  {WebsiteURL: "http://acme.example.com"},
	} {
		cfg.Slug, cfg.Title, cfg.Monitors = "acme", "Acme", []statuspage.MonitorConfig{{MonitorID: m}}
		var ve *validation.Error
		if _, err := e.svc.Create(t.Context(), e.org, cfg); !errors.As(err, &ve) || ve.Fields[field] == "" {
			t.Errorf("%s: %v", field, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "https://user:pw@acme.example.com", "https://"} {
		cfg := statuspage.Config{Slug: "acme", Title: "Acme", WebsiteURL: bad}
		var ve *validation.Error
		if _, err := e.svc.Create(t.Context(), e.org, cfg); !errors.As(err, &ve) || ve.Fields["website_url"] == "" {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestPageLogos(t *testing.T) {
	free := newEnv(t, "free")
	fp := free.page(t, statuspage.Config{Slug: "free-page", Title: "Free"})
	if err := free.svc.SaveLogo(t.Context(), free.org, fp.ID, tinyPNG(t)); limitKind(err) != plans.LimitPageBranding {
		t.Fatalf("Free cannot upload a logo: %v", err)
	}

	e := newEnv(t, "pro")
	c := e.page(t, statuspage.Config{Slug: "draft", Title: "Draft"})
	var ve *validation.Error
	if err := e.svc.SaveLogo(t.Context(), e.org, c.ID, []byte("<svg onload=alert(1)>")); !errors.As(err, &ve) {
		t.Fatalf("only PNG and JPEG: %v", err)
	}
	if err := e.svc.SaveLogo(t.Context(), e.org, c.ID, tinyPNG(t)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.svc.Get(t.Context(), e.org, c.ID)
	if !got.HasLogo || got.LogoVersion == 0 {
		t.Fatalf("%+v", got)
	}
	if data, ctype, err := e.svc.Logo(t.Context(), e.org, c.ID); err != nil || ctype != "image/png" || len(data) == 0 {
		t.Fatalf("the owner previews it: %s %v", ctype, err)
	}
	if _, _, err := e.svc.PublicLogo(t.Context(), "draft"); !errors.Is(err, statuspage.ErrNoLogo) {
		t.Fatalf("a draft's logo is not public: %v", err)
	}
	// Deleting a logo needs no plan, so this checks ownership alone: another organization's request finds nothing.
	other := e.otherOrg(t)
	if err := other.svc.DeleteLogo(t.Context(), other.org, c.ID); !errors.Is(err, statuspage.ErrNotFound) {
		t.Fatalf("another organization cannot touch it: %v", err)
	}
	if _, _, err := other.svc.Logo(t.Context(), other.org, c.ID); !errors.Is(err, statuspage.ErrNoLogo) {
		t.Fatalf("nor see it: %v", err)
	}
	if got, _ := e.svc.Get(t.Context(), e.org, c.ID); !got.HasLogo {
		t.Fatal("the owner's logo is untouched")
	}
	if err := e.svc.DeleteLogo(t.Context(), e.org, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Logo(t.Context(), e.org, c.ID); !errors.Is(err, statuspage.ErrNoLogo) {
		t.Fatalf("%v", err)
	}
}
