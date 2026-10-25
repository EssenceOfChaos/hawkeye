// Package reportpdf draws a monthly uptime report as a PDF. It is plain Go (no browser or other program is needed), uses only the standard PDF fonts, and
// draws every chart itself, so a report is a small, self-contained file that looks the same everywhere.
//
// Text is written in the Windows-1252 character set that the standard fonts cover (Western European languages). A character outside it, such as
// Cyrillic or Chinese in a website's name, prints as a replacement mark rather than failing the report.
package reportpdf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-pdf/fpdf"

	"hawkeye/internal/reports"
)

// Brand is the look of a report. The zero value is the standard HawkEye look; an Agency organization supplies its own name and logo.
type Brand struct {
	Name       string // printed at the top and in the footer ("Prepared by ...")
	Logo       []byte // PNG or JPEG, already validated by internal/branding
	LogoType   string // "png" or "jpeg"
	LogoWidth  int
	LogoHeight int
}

// Branded says whether the report carries the organization's own identity instead of HawkEye's.
func (b Brand) Branded() bool { return b.Name != "" || len(b.Logo) > 0 }

// colours as RGB triples, matching the HawkEye palette on a white page.
var (
	navy   = [3]int{9, 28, 51}
	gold   = [3]int{232, 185, 94}
	muted  = [3]int{98, 112, 130}
	panel  = [3]int{243, 246, 250}
	line   = [3]int{214, 221, 230}
	green  = [3]int{47, 158, 91}
	amber  = [3]int{224, 160, 48}
	red    = [3]int{214, 69, 69}
	noData = [3]int{225, 230, 237}
)

const (
	marginX  = 18.0
	marginY  = 16.0
	pageW    = 210.0
	pageH    = 297.0
	contentW = pageW - 2*marginX
	footerH  = 16.0
)

type renderer struct {
	pdf *fpdf.Fpdf
	tr  func(string) string
	m   reports.Monthly
	b   Brand
}

// Render draws the report. If the logo cannot be drawn (the branding package only stores images it has checked, but a report is what a client
// receives), the report is drawn again without it rather than failing: the name still identifies who it is from.
func Render(m reports.Monthly, b Brand) ([]byte, error) {
	out, err := draw(m, b)
	if err != nil && len(b.Logo) > 0 {
		b.Logo = nil
		return draw(m, b)
	}
	return out, err
}

func draw(m reports.Monthly, b Brand) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(marginX, marginY, marginX)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetCompression(true)
	pdf.SetCreationDate(m.GeneratedAt)
	r := &renderer{pdf: pdf, tr: pdf.UnicodeTranslatorFromDescriptor(""), m: m, b: b}

	author := "HawkEye"
	if b.Name != "" {
		author = b.Name
	}
	pdf.SetTitle(r.tr(fmt.Sprintf("Uptime report: %s, %s", oneLine(m.Name), m.Month.Format("January 2006"))), false)
	pdf.SetAuthor(r.tr(author), false)
	pdf.SetCreator("HawkEye", false)
	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(r.footer)

	pdf.AddPage()
	r.header()
	y := r.title()
	y = r.tiles(y)
	y = r.days(y)
	r.incidents(y)
	r.notes()

	if pdf.Err() {
		return nil, pdf.Error()
	}
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (r *renderer) color(c [3]int)  { r.pdf.SetTextColor(c[0], c[1], c[2]) }
func (r *renderer) fill(c [3]int)   { r.pdf.SetFillColor(c[0], c[1], c[2]) }
func (r *renderer) stroke(c [3]int) { r.pdf.SetDrawColor(c[0], c[1], c[2]) }

// text prints s at x, y (top of the line box) in the current font, after making it safe for the standard fonts.
func (r *renderer) text(x, y, w, h float64, s, align string) {
	r.pdf.SetXY(x, y)
	r.pdf.CellFormat(w, h, r.tr(oneLine(s)), "", 0, align, false, 0, "")
}

// fit shortens s with "..." until it is no wider than w in the current font.
func (r *renderer) fit(s string, w float64) string {
	s = oneLine(s)
	if r.pdf.GetStringWidth(r.tr(s)) <= w {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if c := strings.TrimRightFunc(string(rs), unicode.IsSpace) + "..."; r.pdf.GetStringWidth(r.tr(c)) <= w {
			return c
		}
	}
	return "..."
}

func (r *renderer) header() {
	pdf := r.pdf
	left := marginX
	top := marginY
	drewLogo := false
	if len(r.b.Logo) > 0 && r.b.LogoWidth > 0 && r.b.LogoHeight > 0 {
		const boxW, boxH = 62.0, 20.0
		w, h := boxH*float64(r.b.LogoWidth)/float64(r.b.LogoHeight), boxH
		if w > boxW {
			w, h = boxW, boxW*float64(r.b.LogoHeight)/float64(r.b.LogoWidth)
		}
		info := pdf.RegisterImageOptionsReader("brand-logo", fpdf.ImageOptions{ImageType: strings.ToUpper(r.b.LogoType)}, bytes.NewReader(r.b.Logo))
		if info != nil && !pdf.Err() {
			pdf.ImageOptions("brand-logo", left, top+(boxH-h)/2, w, h, false, fpdf.ImageOptions{ImageType: strings.ToUpper(r.b.LogoType)}, 0, "")
			drewLogo = true
		}
	}
	if !drewLogo {
		if r.b.Name != "" {
			pdf.SetFont("Helvetica", "B", 18)
			r.color(navy)
			r.text(left, top+3, 110, 10, r.fit(r.b.Name, 110), "L")
		} else {
			pdf.SetFont("Helvetica", "B", 20)
			r.color(navy)
			r.text(left, top+3, 18, 10, "Hawk", "L")
			r.color(gold)
			r.text(left+pdf.GetStringWidth("Hawk")+0.3, top+3, 14, 10, "Eye", "L")
		}
	}
	pdf.SetFont("Helvetica", "B", 8)
	r.color(muted)
	r.text(pageW-marginX-70, top+4, 70, 4, "MONTHLY UPTIME REPORT", "R")
	if drewLogo && r.b.Name != "" {
		pdf.SetFont("Helvetica", "", 9)
		r.color(navy)
		r.text(pageW-marginX-70, top+9, 70, 5, r.fit(r.b.Name, 70), "R")
	}
	r.stroke(gold)
	pdf.SetLineWidth(0.7)
	pdf.Line(marginX, top+24, pageW-marginX, top+24)
}

func (r *renderer) title() float64 {
	pdf := r.pdf
	y := marginY + 32
	r.color(navy)
	size := 20.0
	for ; size > 12; size -= 2 {
		pdf.SetFont("Helvetica", "B", size)
		if pdf.GetStringWidth(r.tr(oneLine(r.m.Name))) <= contentW {
			break
		}
	}
	pdf.SetFont("Helvetica", "B", size)
	r.text(marginX, y, contentW, 9, r.fit(r.m.Name, contentW), "L")
	y += 9
	if r.m.URL != "" {
		pdf.SetFont("Helvetica", "", 10)
		r.color(muted)
		r.text(marginX, y, contentW, 5, r.fit(r.m.URL, contentW), "L")
		y += 6
	}
	pdf.SetFont("Helvetica", "B", 12)
	r.color(navy)
	r.text(marginX, y+2, contentW, 6, r.m.Month.Format("January 2006"), "L")
	pdf.SetFont("Helvetica", "", 9)
	r.color(muted)
	period := r.m.From.Format("2 Jan") + " – " + r.m.To.Add(-time.Second).Format("2 Jan 2006") + " (UTC)"
	if r.m.Partial {
		period = "Month to date: " + r.m.From.Format("2 Jan") + " – " + r.m.To.Format("2 Jan 2006") + " (UTC)"
	}
	r.text(marginX, y+8, contentW, 5, period, "L")
	return y + 20
}

func (r *renderer) tiles(y float64) float64 {
	const gap, h = 4.0, 26.0
	w := (contentW - 3*gap) / 4
	up, upNote := "n/a", "Not enough checks yet"
	if r.m.Uptime.UptimePercent != nil {
		up, upNote = formatPercent(*r.m.Uptime.UptimePercent), fmt.Sprintf("%s checks", thousands(r.m.Uptime.TotalChecks))
	}
	avg := "n/a"
	if r.m.Uptime.AvgResponseMs != nil {
		avg = fmt.Sprintf("%d ms", *r.m.Uptime.AvgResponseMs)
	}
	cards := []struct{ label, value, note string }{
		{"UPTIME", up, upNote},
		{"CONFIRMED DOWNTIME", formatDowntime(r.m.DowntimeSeconds), plural(r.m.IncidentCount, "incident")},
		{"INCIDENTS", strconv.Itoa(r.m.IncidentCount), "in this period"},
		{"AVG. RESPONSE", avg, "successful checks"},
	}
	for i, c := range cards {
		x := marginX + float64(i)*(w+gap)
		r.fill(panel)
		r.pdf.RoundedRect(x, y, w, h, 2, "1234", "F")
		r.pdf.SetFont("Helvetica", "B", 7)
		r.color(muted)
		r.text(x+4, y+4, w-8, 4, c.label, "L")
		size := 16.0
		r.pdf.SetFont("Helvetica", "B", size)
		for r.pdf.GetStringWidth(r.tr(c.value)) > w-8 && size > 9 {
			size--
			r.pdf.SetFont("Helvetica", "B", size)
		}
		r.color(navy)
		r.text(x+4, y+10, w-8, 8, c.value, "L")
		r.pdf.SetFont("Helvetica", "", 7.5)
		r.color(muted)
		r.text(x+4, y+19, w-8, 4, r.fit(c.note, w-8), "L")
	}
	return y + h + 10
}

// dayColour is the bar colour for a day's availability: green from 99.9%, amber from 99%, red below, grey with no checks.
func dayColour(d reports.DayStat) [3]int {
	a, ok := d.Available()
	switch {
	case !ok:
		return noData
	case a >= 0.999:
		return green
	case a >= 0.99:
		return amber
	}
	return red
}

func (r *renderer) days(y float64) float64 {
	pdf := r.pdf
	pdf.SetFont("Helvetica", "B", 11)
	r.color(navy)
	r.text(marginX, y, contentW, 6, "Daily availability", "L")
	y += 9
	n := r.m.Month.AddDate(0, 1, 0).Sub(r.m.Month).Hours() / 24
	days := int(n + 0.5)
	byDay := map[int]reports.DayStat{}
	for _, d := range r.m.Days {
		byDay[d.Day.Day()] = d
	}
	const barH = 22.0
	slot := contentW / float64(days)
	for i := 1; i <= days; i++ {
		x := marginX + float64(i-1)*slot
		r.fill(dayColour(byDay[i]))
		pdf.Rect(x+0.25, y, slot-0.5, barH, "F")
		if i == 1 || i%5 == 0 || i == days {
			pdf.SetFont("Helvetica", "", 6.5)
			r.color(muted)
			r.text(x-1, y+barH+1, slot+2, 3, strconv.Itoa(i), "C")
		}
	}
	y += barH + 8
	pdf.SetFont("Helvetica", "", 7.5)
	x := marginX
	for _, k := range []struct {
		c [3]int
		s string
	}{{green, "99.9% or better"}, {amber, "99% to 99.9%"}, {red, "below 99%"}, {noData, "no checks"}} {
		r.fill(k.c)
		pdf.Rect(x, y+0.6, 3.2, 3.2, "F")
		r.color(muted)
		r.text(x+4.6, y, 32, 4.4, k.s, "L")
		x += 6 + pdf.GetStringWidth(k.s) + 5
	}
	return y + 12
}

var causeLabels = map[string]string{
	"dns": "DNS", "connect": "Connection", "tls": "Certificate / TLS", "timeout": "Timeout", "redirect": "Redirects",
	"blocked": "Blocked", "http_status": "HTTP status", "assertion": "Content check", "other": "Other",
}

func (r *renderer) incidents(y float64) {
	pdf := r.pdf
	pdf.SetFont("Helvetica", "B", 11)
	r.color(navy)
	r.text(marginX, y, contentW, 6, "Incidents", "L")
	y += 9
	if r.m.IncidentCount == 0 {
		pdf.SetFont("Helvetica", "", 10)
		r.color(muted)
		r.text(marginX, y, contentW, 6, "No incidents were recorded in this period.", "L")
		return
	}
	cols := []struct {
		title string
		w     float64
	}{{"STARTED (UTC)", 34}, {"RECOVERED (UTC)", 34}, {"DURATION", 24}, {"CAUSE", contentW - 92}}
	head := func(y float64) float64 {
		pdf.SetFont("Helvetica", "B", 7)
		r.color(muted)
		x := marginX
		for _, c := range cols {
			r.text(x, y, c.w, 4, c.title, "L")
			x += c.w
		}
		r.stroke(line)
		pdf.SetLineWidth(0.3)
		pdf.Line(marginX, y+5, pageW-marginX, y+5)
		return y + 6.5
	}
	y = head(y)
	for _, in := range r.m.Incidents {
		if y > pageH-footerH-14 {
			pdf.AddPage()
			y = head(marginY)
		}
		recovered := "Still open"
		if in.ResolvedAt != nil && !in.ResolvedAt.After(r.m.To) {
			recovered = in.ResolvedAt.UTC().Format("2 Jan 15:04")
		} else if in.ResolvedAt != nil {
			recovered = "After this period"
		}
		cause := causeLabels[in.Cause]
		if in.Summary != "" {
			if cause != "" {
				cause += ": "
			}
			cause += in.Summary
		}
		if cause == "" {
			cause = "Not available"
		}
		pdf.SetFont("Helvetica", "", 9)
		r.color(navy)
		vals := []string{in.StartedAt.UTC().Format("2 Jan 15:04"), recovered, formatDowntime(in.Seconds), r.fit(cause, cols[3].w-2)}
		x := marginX
		for i, c := range cols {
			r.text(x, y, c.w, 5, vals[i], "L")
			x += c.w
		}
		r.stroke(line)
		pdf.SetLineWidth(0.15)
		pdf.Line(marginX, y+6.2, pageW-marginX, y+6.2)
		y += 7
	}
	if r.m.IncidentCount > len(r.m.Incidents) {
		pdf.SetFont("Helvetica", "", 8.5)
		r.color(muted)
		r.text(marginX, y+1, contentW, 5, fmt.Sprintf("Showing the first %d of %d incidents. Totals above include all of them.", len(r.m.Incidents), r.m.IncidentCount), "L")
	}
}

func (r *renderer) notes() {
	pdf := r.pdf
	y := pdf.GetY()
	if y > pageH-footerH-34 {
		pdf.AddPage()
		y = marginY
	} else {
		y += 12
	}
	pdf.SetFont("Helvetica", "B", 8)
	r.color(muted)
	r.text(marginX, y, contentW, 4, "HOW THESE NUMBERS ARE MEASURED", "L")
	pdf.SetFont("Helvetica", "", 8)
	pdf.SetXY(marginX, y+5)
	pdf.MultiCell(contentW, 4, r.tr("Uptime is the share of completed checks that succeeded. Checks that ran during scheduled maintenance, and any time monitoring was paused, "+
		"are not counted. Confirmed downtime is the time between a confirmed outage and its recovery. An outage is confirmed only after consecutive failed checks, "+
		"so an interruption shorter than the checking interval may not appear. All times are UTC."), "", "L", false)
}

func (r *renderer) footer() {
	pdf := r.pdf
	r.stroke(line)
	pdf.SetLineWidth(0.3)
	pdf.Line(marginX, pageH-footerH, pageW-marginX, pageH-footerH)
	pdf.SetFont("Helvetica", "", 8)
	r.color(muted)
	left := "Generated by HawkEye on " + r.m.GeneratedAt.UTC().Format("2 Jan 2006") + " (UTC)"
	if r.b.Name != "" {
		left = "Prepared by " + r.b.Name + "  ·  Monitoring by HawkEye"
	}
	r.text(marginX, pageH-footerH+3, contentW-30, 4, r.fit(left, contentW-30), "L")
	r.text(pageW-marginX-30, pageH-footerH+3, 30, 4, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "R")
}

// oneLine turns any text into a single printable line, so a name with a line break or control character cannot break the layout.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)), " ")
}

// formatPercent prints 99.912, 99.9 or 100 without padding zeros.
func formatPercent(p float64) string {
	s := strconv.FormatFloat(p, 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s + "%"
}

// formatDowntime prints seconds as the two largest sensible units: "0 min", "42 min", "3 h 05 min", "2 d 4 h".
func formatDowntime(sec int64) string {
	switch {
	case sec <= 0:
		return "0 min"
	case sec < 60:
		return fmt.Sprintf("%d sec", sec)
	case sec < 3600:
		return fmt.Sprintf("%d min", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d h %02d min", sec/3600, (sec%3600)/60)
	}
	return fmt.Sprintf("%d d %d h", sec/86400, (sec%86400)/3600)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Filename is a safe file name for a website's report ("acme-corp-2026-09.pdf"): lower-case letters, digits and dashes only, so it is valid in an email
// attachment and in a download header whatever the website is called.
func Filename(siteName string, month time.Time) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(siteName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "website"
	}
	return name + "-" + month.UTC().Format("2006-01") + ".pdf"
}
