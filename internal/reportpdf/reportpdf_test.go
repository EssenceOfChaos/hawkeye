package reportpdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/reports"
)

func sample() reports.Monthly {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p := 99.912
	avg := 182
	var days []reports.DayStat
	for d := 1; d <= 30; d++ {
		ds := reports.DayStat{Day: month.AddDate(0, 0, d-1), Checks: 288}
		switch d {
		case 7:
			ds.Failed = 6 // 97.9%
		case 8:
			ds.Failed = 1 // 99.65%
		case 15:
			ds = reports.DayStat{Day: ds.Day} // no checks
		}
		days = append(days, ds)
	}
	end := time.Date(2026, 9, 7, 14, 40, 0, 0, time.UTC)
	return reports.Monthly{
		Name: "Acme Corp — Marketing site", URL: "https://www.acme.example/", Month: month, From: month, To: month.AddDate(0, 1, 0),
		Uptime:          reports.Uptime{TotalChecks: 8500, SuccessfulChecks: 8492, UptimePercent: &p, AvgResponseMs: &avg},
		DowntimeSeconds: 2520, IncidentCount: 2, Days: days, GeneratedAt: time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC),
		Incidents: []reports.IncidentRow{
			{StartedAt: end.Add(-42 * time.Minute), ResolvedAt: &end, Seconds: 2520, Cause: "http_status", Summary: "Unexpected status 503"},
			{StartedAt: time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC), Seconds: 0, Cause: "timeout", Summary: "The site did not answer in time"},
		},
	}
}

func logoPNG(t *testing.T, w, h int, transparent bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if transparent && x < w/4 {
				a = 0
			}
			img.Set(x, y, color.NRGBA{R: 200, G: uint8(x * 255 / w), B: 90, A: a})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var written int

func render(t *testing.T, m reports.Monthly, b Brand) []byte {
	t.Helper()
	out, err := Render(m, b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) || !bytes.Contains(out[len(out)-32:], []byte("%%EOF")) {
		t.Fatalf("not a complete PDF (%d bytes)", len(out))
	}
	if dir := os.Getenv("REPORTPDF_OUT"); dir != "" {
		written++
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s_%d.pdf", strings.ReplaceAll(t.Name(), "/", "_"), written)), out, 0o644)
	}
	return out
}

var pageCount = regexp.MustCompile(`/Type /Page\b`)

func pages(pdf []byte) int { return len(pageCount.FindAll(pdf, -1)) }

func TestStandardReportIsASmallSinglePagePDF(t *testing.T) {
	out := render(t, sample(), Brand{})
	if pages(out) != 1 {
		t.Fatalf("pages = %d, want 1", pages(out))
	}
	if len(out) > 60<<10 {
		t.Fatalf("a plain report is %d bytes; it should stay small enough to attach dozens to one email", len(out))
	}
}

func TestBrandedReportsEmbedPNGWithTransparencyAndJPEG(t *testing.T) {
	pngLogo := logoPNG(t, 400, 120, true)
	b := Brand{Name: "Northwind Digital", Logo: pngLogo, LogoType: "png", LogoWidth: 400, LogoHeight: 120}
	withPNG := render(t, sample(), b)
	if !bytes.Contains(withPNG, []byte("/Subtype /Image")) {
		t.Fatal("the logo is not in the PDF")
	}

	var jb bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for i := range img.Pix {
		img.Pix[i] = byte(i % 251)
	}
	if err := jpeg.Encode(&jb, img, nil); err != nil {
		t.Fatal(err)
	}
	withJPEG := render(t, sample(), Brand{Name: "Square Logo Ltd", Logo: jb.Bytes(), LogoType: "jpeg", LogoWidth: 300, LogoHeight: 300})
	if !bytes.Contains(withJPEG, []byte("/DCTDecode")) {
		t.Fatal("the JPEG logo is not embedded as JPEG")
	}
	// A name alone (no logo) is also a valid brand.
	render(t, sample(), Brand{Name: "Name Only"})
}

func TestAnUnreadableLogoNeverFailsTheReport(t *testing.T) {
	// The branding package only stores validated images, but a report is the thing a client receives: if a logo were ever bad, the report still goes out.
	out, err := Render(sample(), Brand{Name: "Broken Logo Co", Logo: []byte("not an image"), LogoType: "png", LogoWidth: 10, LogoHeight: 10})
	if err != nil {
		t.Fatalf("a bad logo failed the whole report: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
}

func TestManyIncidentsContinueOnLaterPages(t *testing.T) {
	m := sample()
	end := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	m.Incidents = nil
	for i := 0; i < 25; i++ {
		s := end.Add(time.Duration(i) * 24 * time.Hour / 2)
		e := s.Add(9 * time.Minute)
		m.Incidents = append(m.Incidents, reports.IncidentRow{StartedAt: s, ResolvedAt: &e, Seconds: 540, Cause: "connect", Summary: "Connection refused"})
	}
	m.IncidentCount = 61
	out := render(t, m, Brand{Name: "Busy Agency"})
	if pages(out) < 2 {
		t.Fatalf("pages = %d, want the table to continue on a second page", pages(out))
	}
}

func TestOddTextCannotBreakTheLayoutOrFailTheReport(t *testing.T) {
	m := sample()
	m.Name = "Line\nbreak\tand " + strings.Repeat("very long name ", 20) + "Привет 你好 😀"
	m.URL = "https://example.com/" + strings.Repeat("a", 400)
	m.Incidents[0].Summary = strings.Repeat("x", 500) + "\x00\x1b"
	render(t, m, Brand{Name: strings.Repeat("N", 100)})
}

func TestEmptyAndPartialMonths(t *testing.T) {
	m := sample()
	m.Incidents, m.IncidentCount, m.DowntimeSeconds = nil, 0, 0
	out := render(t, m, Brand{})
	if !bytes.Contains(out, []byte("%PDF")) {
		t.Fatal("no pdf")
	}

	m = sample()
	m.Uptime.UptimePercent, m.Uptime.InsufficientData, m.Uptime.AvgResponseMs = nil, true, nil
	m.Partial, m.To = true, time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	m.Days = m.Days[:11]
	render(t, m, Brand{})
}

func TestFormatting(t *testing.T) {
	for in, want := range map[float64]string{100: "100%", 99.912: "99.912%", 99.9: "99.9%", 0: "0%", 97.5: "97.5%"} {
		if got := formatPercent(in); got != want {
			t.Errorf("formatPercent(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[int64]string{0: "0 min", 30: "30 sec", 60: "1 min", 2520: "42 min", 11100: "3 h 05 min", 93600: "1 d 2 h"} {
		if got := formatDowntime(in); got != want {
			t.Errorf("formatDowntime(%d) = %q, want %q", in, got, want)
		}
	}
	if thousands(8500) != "8,500" || thousands(12) != "12" || thousands(1234567) != "1,234,567" {
		t.Error("thousands")
	}
}
