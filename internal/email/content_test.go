package email

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
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
	for _, want := range []string{"#1f4fd8", "#0b1d4a", "https://app.example.com/email/hawkeye-logo.png", `href="https://app.example.com/verify-email?token=abc123"`, "Verify email", "Preview text", "/privacy", "/contact"} {
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
	if !strings.Contains(c.RenderHTML(), "#c62828") {
		t.Fatal("alert tone should use the red accent")
	}
	c.Tone = ToneSuccess
	if !strings.Contains(c.RenderHTML(), "#1a7f37") {
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
	raw, err := buildMessage(from, to, rt, "Hello ünïcode", c.RenderText(), c.RenderHTML())
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
	raw, err := buildMessage(from, to, nil, "Hi", "body", "")
	if err != nil || !strings.Contains(string(raw), "text/plain") || strings.Contains(string(raw), "multipart") || strings.Contains(string(raw), "Reply-To") {
		t.Fatalf("%v\n%s", err, raw)
	}
}
