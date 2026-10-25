package branding_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"hawkeye/internal/branding"
	"hawkeye/internal/testutil"
)

func TestBrandingIsStoredPerOrganizationAndOnlyUsedWhenThePlanAllowsIt(t *testing.T) {
	pool := testutil.NewPool(t)
	svc := branding.NewService(pool)
	var a, b uuid.UUID
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('a') RETURNING id`).Scan(&a)
	pool.QueryRow(t.Context(), `INSERT INTO organizations (name) VALUES ('b') RETURNING id`).Scan(&b)

	if got, err := svc.Get(t.Context(), a); err != nil || got != (branding.Branding{}) {
		t.Fatalf("nothing saved yet: %v %+v", err, got)
	}
	if _, _, err := svc.Logo(t.Context(), a); !errors.Is(err, branding.ErrNoLogo) {
		t.Fatalf("no logo yet: %v", err)
	}

	if err := svc.SaveName(t.Context(), a, " Northwind "); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveLogo(t.Context(), a, pngFile(t, 200, 60)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMonthlyEmail(t.Context(), a, true); err != nil {
		t.Fatal(err)
	}
	// Each save leaves the others alone.
	got, _ := svc.Get(t.Context(), a)
	if got.DisplayName != "Northwind" || !got.HasLogo || got.LogoWidth != 200 || got.LogoHeight != 60 || !got.MonthlyReportEmail {
		t.Fatalf("%+v", got)
	}
	if err := svc.SaveName(t.Context(), a, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ = svc.Get(t.Context(), a); got.DisplayName != "" || !got.HasLogo || !got.MonthlyReportEmail {
		t.Fatalf("clearing the name touched something else: %+v", got)
	}
	data, ctype, err := svc.Logo(t.Context(), a)
	if err != nil || ctype != "image/png" || len(data) == 0 {
		t.Fatalf("logo: %v %q %d", err, ctype, len(data))
	}

	// The other organization sees none of it.
	if got, _ := svc.Get(t.Context(), b); got != (branding.Branding{}) {
		t.Fatalf("leaked across organizations: %+v", got)
	}
	if _, _, err := svc.Logo(t.Context(), b); !errors.Is(err, branding.ErrNoLogo) {
		t.Fatalf("logo leaked: %v", err)
	}

	// Reports use the brand only on a plan that includes it; the stored branding is kept either way.
	svc.SaveName(t.Context(), a, "Northwind")
	if br, _ := svc.Brand(t.Context(), a, false); br.Branded() {
		t.Fatalf("a plan without branding must get the standard look: %+v", br)
	}
	br, err := svc.Brand(t.Context(), a, true)
	if err != nil || br.Name != "Northwind" || len(br.Logo) == 0 || br.LogoType != "png" || br.LogoWidth != 200 {
		t.Fatalf("%v %+v", err, br)
	}
	if got, _ := svc.Get(t.Context(), a); got.DisplayName != "Northwind" || !got.HasLogo {
		t.Fatal("branding must survive being unused")
	}

	if err := svc.DeleteLogo(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteLogo(t.Context(), b); err != nil {
		t.Fatalf("deleting when there is none must succeed: %v", err)
	}
	if got, _ = svc.Get(t.Context(), a); got.HasLogo || got.DisplayName != "Northwind" {
		t.Fatalf("%+v", got)
	}
	if err := svc.SaveLogo(t.Context(), a, []byte("<svg/>")); err == nil {
		t.Fatal("a non-image was stored")
	}
}
