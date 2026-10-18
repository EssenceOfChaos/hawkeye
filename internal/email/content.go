package email

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

// Tone picks the accent colour of a branded email.
type Tone string

const (
	ToneDefault Tone = ""        // brand blue
	ToneAlert   Tone = "alert"   // red: an outage
	ToneSuccess Tone = "success" // green: a recovery
)

// Fact is one labelled line, such as "Website: https://example.com".
type Fact struct{ Label, Value string }

// Content describes a transactional email once; it renders to both a plain-text and a branded HTML body so the two can never drift.
type Content struct {
	Preheader   string // the grey preview text shown by mail clients next to the subject
	Heading     string
	Paragraphs  []string
	Facts       []Fact
	Quote       string // untrusted multi-line text shown in a bordered box with its line breaks preserved
	ButtonLabel string
	ButtonURL   string
	Note        string // small print under the button
	Tone        Tone
	AppBaseURL  string // used for the logo and footer links
}

// Message renders the content into a ready-to-send Message.
func (c Content) Message(from, to, subject string) Message {
	return Message{From: from, To: to, Subject: subject, Text: c.RenderText(), HTML: c.RenderHTML()}
}

// oneLine turns any value into a single printable line: control characters (including line breaks and tabs) are dropped or become spaces, and
// runs of whitespace collapse. A monitor name, cause or sender name can therefore never add lines, fake headings or fake "labels" to a message.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)), " ")
}

// multiLine keeps line breaks (normalised to \n) but drops every other control character.
func multiLine(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// noSpace is for URLs: no whitespace or control characters at all.
func noSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if r <= 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// clean returns a copy that is safe to compose into a message, whatever the caller put in it. Everything is a single line except Quote, the one
// field meant to hold the visitor's multi-line text. The renderers call it themselves, so no caller can forget.
func (c Content) clean() Content {
	c.Preheader, c.Heading, c.ButtonLabel, c.Note = oneLine(c.Preheader), oneLine(c.Heading), oneLine(c.ButtonLabel), oneLine(c.Note)
	c.ButtonURL, c.AppBaseURL = noSpace(c.ButtonURL), noSpace(c.AppBaseURL)
	c.Quote = multiLine(c.Quote)
	ps := make([]string, len(c.Paragraphs))
	for i, p := range c.Paragraphs {
		ps[i] = oneLine(p)
	}
	c.Paragraphs = ps
	fs := make([]Fact, len(c.Facts))
	for i, f := range c.Facts {
		fs[i] = Fact{Label: oneLine(f.Label), Value: oneLine(f.Value)}
	}
	c.Facts = fs
	return c
}

// RenderText is the plain-text alternative.
func (c Content) RenderText() string {
	c = c.clean()
	var b strings.Builder
	if c.Heading != "" {
		b.WriteString(c.Heading + "\n\n")
	}
	for _, p := range c.Paragraphs {
		b.WriteString(p + "\n\n")
	}
	for _, f := range c.Facts {
		fmt.Fprintf(&b, "%s: %s\n", f.Label, f.Value)
	}
	if len(c.Facts) > 0 {
		b.WriteString("\n")
	}
	if c.Quote != "" {
		for _, line := range strings.Split(strings.ReplaceAll(c.Quote, "\r\n", "\n"), "\n") {
			b.WriteString("> " + line + "\n")
		}
		b.WriteString("\n")
	}
	if c.ButtonURL != "" {
		fmt.Fprintf(&b, "%s:\n%s\n\n", c.ButtonLabel, c.ButtonURL)
	}
	if c.Note != "" {
		b.WriteString(c.Note + "\n\n")
	}
	b.WriteString("-- \nHawkEye. Your websites. Always in sight.\n")
	if c.AppBaseURL != "" {
		b.WriteString("Privacy: " + c.AppBaseURL + "/privacy  Terms: " + c.AppBaseURL + "/terms  Contact: " + c.AppBaseURL + "/contact\n")
	}
	return b.String()
}

// Brand colours (kept in step with web/src/index.css).
const (
	colInk    = "#0b1d4a"
	colAccent = "#1f4fd8"
	colBg     = "#f6f8fc"
	colBorder = "#d6dcec"
	colDown   = "#c62828"
	colUp     = "#1a7f37"
	colMuted  = "#5b6785"
)

func (c Content) accent() string {
	switch c.Tone {
	case ToneAlert:
		return colDown
	case ToneSuccess:
		return colUp
	}
	return colAccent
}

type htmlData struct {
	Content
	Accent, Ink, Bg, Border, Muted string
	LogoURL                        string
}

// The layout is a single-column, table-based, inline-styled design: the only approach that renders consistently in Gmail, Outlook and Apple Mail.
// Every value is escaped by html/template; URLs are filtered to safe schemes.
var layout = template.Must(template.New("email").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light only">
<meta name="supported-color-schemes" content="light only">
<title>{{.Heading}}</title>
</head>
<body style="margin:0;padding:0;background:{{.Bg}};">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:{{.Bg}};">
<tr><td align="center" style="padding:24px 12px;">
  <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="width:100%;max-width:560px;">
    <tr><td style="padding:0 4px 16px 4px;">
      {{if .LogoURL}}<img src="{{.LogoURL}}" width="190" alt="HawkEye" style="display:block;border:0;height:auto;width:190px;">{{else}}<span style="font:700 22px Arial,Helvetica,sans-serif;color:{{.Ink}};">HawkEye</span>{{end}}
    </td></tr>
    <tr><td style="background:#ffffff;border:1px solid {{.Border}};border-top:4px solid {{.Accent}};border-radius:8px;padding:28px 28px 24px 28px;font:16px/1.55 Arial,Helvetica,sans-serif;color:{{.Ink}};">
      {{if .Heading}}<h1 style="margin:0 0 16px 0;font:700 22px/1.3 Arial,Helvetica,sans-serif;color:{{.Ink}};">{{.Heading}}</h1>{{end}}
      {{range .Paragraphs}}<p style="margin:0 0 16px 0;">{{.}}</p>{{end}}
      {{if .Facts}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:0 0 20px 0;border-top:1px solid {{$.Border}};">
        {{range .Facts}}<tr>
          <td style="padding:8px 12px 8px 0;border-bottom:1px solid {{$.Border}};font:13px Arial,Helvetica,sans-serif;color:{{$.Muted}};width:38%;vertical-align:top;">{{.Label}}</td>
          <td style="padding:8px 0;border-bottom:1px solid {{$.Border}};font:14px Arial,Helvetica,sans-serif;color:{{$.Ink}};word-break:break-word;">{{.Value}}</td>
        </tr>{{end}}
      </table>{{end}}
      {{if .Quote}}<div style="margin:0 0 20px 0;padding:14px 16px;background:{{.Bg}};border-left:4px solid {{.Accent}};border-radius:4px;font:15px/1.55 Arial,Helvetica,sans-serif;white-space:pre-wrap;word-break:break-word;">{{.Quote}}</div>{{end}}
      {{if .ButtonURL}}<table role="presentation" cellpadding="0" cellspacing="0" style="margin:8px 0 20px 0;"><tr><td style="background:{{.Accent}};border-radius:6px;">
        <a href="{{.ButtonURL}}" style="display:inline-block;padding:12px 22px;font:700 16px Arial,Helvetica,sans-serif;color:#ffffff;text-decoration:none;">{{.ButtonLabel}}</a>
      </td></tr></table>
      <p style="margin:0 0 16px 0;font-size:12px;color:{{.Muted}};">Button not working? Copy this link into your browser:<br><a href="{{.ButtonURL}}" style="color:{{.Accent}};word-break:break-all;">{{.ButtonURL}}</a></p>{{end}}
      {{if .Note}}<p style="margin:0;font-size:13px;color:{{.Muted}};">{{.Note}}</p>{{end}}
    </td></tr>
    <tr><td style="padding:16px 4px 0 4px;font:12px/1.6 Arial,Helvetica,sans-serif;color:{{.Muted}};">
      <strong style="color:{{.Ink}};">HawkEye</strong> &middot; Your websites. Always in sight.<br>
      {{if .AppBaseURL}}<a href="{{.AppBaseURL}}/privacy" style="color:{{.Muted}};">Privacy</a> &middot; <a href="{{.AppBaseURL}}/terms" style="color:{{.Muted}};">Terms</a> &middot; <a href="{{.AppBaseURL}}/contact" style="color:{{.Muted}};">Contact us</a>{{end}}
    </td></tr>
  </table>
</td></tr>
</table>
</body>
</html>
`))

// RenderHTML is the branded HTML body.
func (c Content) RenderHTML() string {
	c = c.clean()
	d := htmlData{Content: c, Accent: c.accent(), Ink: colInk, Bg: colBg, Border: colBorder, Muted: colMuted}
	if c.AppBaseURL != "" {
		d.LogoURL = c.AppBaseURL + "/email/hawkeye-logo.png"
	}
	var b bytes.Buffer
	if err := layout.Execute(&b, d); err != nil {
		return "" // the plain-text part still goes out
	}
	return b.String()
}
