package email

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() Content {
	return Content{
		Preheader: "Preview text", Heading: "Verify your email", Paragraphs: []string{"Confirm your address to continue."},
		Facts:       []Fact{{"Website", "https://example.com/"}},
		ButtonLabel: "Verify email", ButtonURL: "https://app.example.com/verify-email?token=abc123",
		Note: "If you did not sign up, ignore this email.", AppBaseURL: "https://app.example.com",
	}
}

func TestBrandedHTMLUsesBrandColoursAndLogo(t *testing.T) {
	h := sample().RenderHTML()
	for _, want := range []string{"#e8b95e", "#091c33", "#0f2744", "https://app.example.com/email/hawkeye-logo.png", `href="https://app.example.com/verify-email?token=abc123"`, "Verify email", "Preview text", "/privacy", "/contact"} {
		if !strings.Contains(h, want) {
			t.Errorf("html missing %q", want)
		}
	}
	if strings.Contains(h, "ZgotmplZ") {
		t.Fatal("html/template rejected a value (ZgotmplZ), so a style would be lost")
	}
}

func TestToneChangesAccent(t *testing.T) {
	c := sample()
	c.Tone = ToneAlert
	if !strings.Contains(c.RenderHTML(), "#ff8a80") {
		t.Fatal("alert tone should use the red accent")
	}
	c.Tone = ToneSuccess
	if !strings.Contains(c.RenderHTML(), "#56d364") {
		t.Fatal("success tone should use the green accent")
	}
}

func TestHTMLEscapesUntrustedValues(t *testing.T) {
	c := sample()
	c.Heading = `<script>alert(1)</script>`
	c.Facts = []Fact{{"Website", `"><img src=x onerror=alert(1)>`}}
	c.ButtonURL = "javascript:alert(1)"
	h := c.RenderHTML()
	for _, bad := range []string{"<script>", "<img src=x", `href="javascript:`} {
		if strings.Contains(h, bad) {
			t.Fatalf("unsafe content rendered: %q in\n%s", bad, h)
		}
	}
}

func TestTextVersionCarriesTheSameInformation(t *testing.T) {
	txt := sample().RenderText()
	for _, want := range []string{"Verify your email", "Confirm your address", "Website: https://example.com/", "https://app.example.com/verify-email?token=abc123", "If you did not sign up"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q:\n%s", want, txt)
		}
	}
}

func TestBuildMessageMultipartAndReplyTo(t *testing.T) {
	from, _ := mail.ParseAddress("HawkEye <alerts@mail.example.com>")
	to, _ := mail.ParseAddress("user@example.org")
	rt, _ := mail.ParseAddress("Visitor <visitor@example.net>")
	c := sample()
	raw, err := buildMessage(from, to, rt, Message{Subject: "Hello ünïcode", Text: c.RenderText(), HTML: c.RenderHTML()})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Header.Get("Reply-To"); !strings.Contains(got, "visitor@example.net") {
		t.Fatalf("reply-to = %q", got)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/alternative" {
		t.Fatalf("content type %q %v", mt, err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var types []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(p) // multipart already decodes quoted-printable
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		types = append(types, ct)
		if ct == "text/html" && !strings.Contains(string(body), "hawkeye-logo.png") {
			t.Error("html part is missing the logo")
		}
		if ct == "text/plain" && !strings.Contains(string(body), "https://app.example.com/verify-email?token=abc123") {
			t.Error("text part is missing the link")
		}
	}
	if strings.Join(types, ",") != "text/plain,text/html" {
		t.Fatalf("parts = %v (plain text must come first so HTML is preferred)", types)
	}
}

func TestBuildMessagePlainTextWhenNoHTML(t *testing.T) {
	from, _ := mail.ParseAddress("a@example.com")
	to, _ := mail.ParseAddress("b@example.org")
	raw, err := buildMessage(from, to, nil, Message{Subject: "Hi", Text: "body"})
	if err != nil || !strings.Contains(string(raw), "text/plain") || strings.Contains(string(raw), "multipart") || strings.Contains(string(raw), "Reply-To") {
		t.Fatalf("%v\n%s", err, raw)
	}
}

func TestUntrustedValuesCannotAddLinesOrControlCharactersToTheMessage(t *testing.T) {
	c := Content{
		Heading:     "Down: evil\r\n\r\nYour password was reset, call 555-0100",
		Paragraphs:  []string{"Monitor \"shop\"\nTo stay safe visit https://evil.example\x00"},
		Facts:       []Fact{{Label: "Website\nFake", Value: "https://x.example\r\nPassword: hunter2"}},
		Quote:       "line one\r\nline two\x07\x1b[31m",
		Note:        "note\nsecond line",
		ButtonLabel: "Open\nnow", ButtonURL: "https://app.example.com/a b\r\nBcc: x@y.z", AppBaseURL: "https://app.example.com",
	}
	text := c.RenderText()
	for _, bad := range []string{"\r", "\x00", "\x07", "\x1b"} {
		if strings.Contains(text, bad) || strings.Contains(c.RenderHTML(), bad) {
			t.Fatalf("control character %q reached the message", bad)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		for _, fake := range []string{"Your password was reset", "To stay safe", "Password:", "Fake:", "second line", "Bcc:"} {
			if strings.HasPrefix(line, fake) {
				t.Fatalf("an untrusted value started its own line: %q", line)
			}
		}
	}
	if !strings.Contains(text, "> line one\n> line two") {
		t.Fatalf("the quote must keep its line breaks:\n%s", text)
	}
}

func TestEmailIsDarkNavyAndGoldNotTheOldBlueAndWhite(t *testing.T) {
	h := sample().RenderHTML()
	for _, want := range []string{`content="dark"`, `bgcolor="#091c33"`, `bgcolor="#0f2744"`, `bgcolor="#e8b95e"`, "color:#132139"} { // dark-only page, navy backdrop, navy card, gold button with navy text
		if !strings.Contains(h, want) {
			t.Errorf("html missing %q", want)
		}
	}
	for _, old := range []string{"#1f4fd8", "#f6f8fc", "#ffffff;border:1px", `content="light only"`} {
		if strings.Contains(h, old) {
			t.Errorf("the old light/blue styling %q is still in the email", old)
		}
	}
}

// EMAIL_PREVIEW_DIR=/some/dir go test ./internal/email -run Preview writes sample emails as .html files for a look in a browser.
func TestWritePreviewFiles(t *testing.T) {
	dir := os.Getenv("EMAIL_PREVIEW_DIR")
	if dir == "" {
		t.Skip("set EMAIL_PREVIEW_DIR to write preview files")
	}
	base := os.Getenv("EMAIL_PREVIEW_BASE") // where /email/hawkeye-logo.png is served from, so the logo shows
	if base == "" {
		base = "https://app.example.com"
	}
	verify := sample()
	verify.AppBaseURL = base
	outage := Content{Preheader: "Offline", Heading: "Marketing site may be offline", Tone: ToneAlert, AppBaseURL: base,
		Paragraphs:  []string{"HawkEye confirmed an outage after repeated failed checks."},
		Facts:       []Fact{{"Website", "https://example.com/"}, {"Detected", "2026-10-18 21:04 UTC"}, {"Cause", "Connection timed out"}},
		ButtonLabel: "See what's down", ButtonURL: base + "/incidents", Note: "You get one alert when an outage is confirmed and one when it ends."}
	recovered := Content{Preheader: "Online", Heading: "Marketing site is back online", Tone: ToneSuccess, AppBaseURL: base,
		Paragraphs: []string{"It was offline for 12 minutes."}, Facts: []Fact{{"Website", "https://example.com/"}, {"Offline for", "12 minutes"}},
		ButtonLabel: "Open HawkEye", ButtonURL: base + "/dashboard"}
	contact := Content{Preheader: "New message", Heading: "Charged twice", Tone: ToneAlert, AppBaseURL: base,
		Paragraphs: []string{"New message from the HawkEye contact form."}, Facts: []Fact{{"Reference", "HK-7F3A2C"}, {"Priority", "High"}},
		Quote: "I was charged twice this month.\nPlease check my invoice."}
	for name, c := range map[string]Content{"verify": verify, "outage": outage, "recovered": recovered, "contact": contact} {
		if err := os.WriteFile(filepath.Join(dir, name+".html"), []byte(c.RenderHTML()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
