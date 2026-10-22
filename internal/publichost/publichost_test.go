package publichost

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestParseHost(t *testing.T) {
	ok := map[string]string{
		"example.com":                          "example.com",
		"  Example.COM  ":                      "example.com",
		"example.com.":                         "example.com",
		"https://example.com":                  "example.com",
		"http://www.example.com/path?q=1#frag": "www.example.com",
		"example.com/some/path":                "example.com",
		"example.com:443":                      "example.com",
		"example.com:80":                       "example.com",
		"http://example.com":                   "example.com",
		"https://example.com:443/":             "example.com",
		"sub.domain.example.co.uk":             "sub.domain.example.co.uk",
		"münchen.de":                           "xn--mnchen-3ya.de",
		"xn--mnchen-3ya.de":                    "xn--mnchen-3ya.de",
		"my-site.example":                      "my-site.example",
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got.Host != want {
			t.Errorf("Parse(%q) = %+v, %v; want host %q", in, got, err, want)
		}
	}

	bad := []string{
		"", "   ", "localhost", "intranet", "nodots",
		// IP addresses in every spelling
		"127.0.0.1", "10.0.0.1", "8.8.8.8", "0x7f.0.0.1", "2130706433", "[::1]", "::1", "https://[2001:4860:4860::8888]/", "1.2.3.4:443",
		// private-network names
		"printer.local", "db.internal", "a.localhost", "router.lan", "x.home.arpa", "host.localdomain",
		// ports other than 443, credentials, other schemes, wildcards
		"example.com:8080", "example.com:22", "https://user:pw@example.com", "user@example.com", "ftp://example.com", "gopher://example.com", "*.example.com",
		// malformed
		"-bad.example.com", "bad-.example.com", "exa mple.com", "example..com", ".example.com", "exa_mple.com", "example.com\x00", "a/b", "https://", "//",
		strings.Repeat("a", 64) + ".com", strings.Repeat("a.", 130) + "com",
	}
	for _, in := range bad {
		got, err := Parse(in)
		var ih *InvalidHostError
		if !errors.As(err, &ih) || got != (Target{}) {
			t.Errorf("Parse(%q) = %+v, %v; want an InvalidHostError", in, got, err)
		}
	}
	if _, err := Parse(strings.Repeat("a", maxInputLen+1)); err == nil {
		t.Error("over-long input must be refused")
	}
}

func TestParseKeepsWhatTheVisitorChose(t *testing.T) {
	for in, want := range map[string]Target{
		"example.com":               {Host: "example.com"},
		"https://Example.com/x?y=1": {Host: "example.com", Scheme: "https"},
		"http://example.com":        {Host: "example.com", Scheme: "http"},
		"example.com:80":            {Host: "example.com", Port: "80"},
		"http://example.com:80/":    {Host: "example.com", Scheme: "http", Port: "80"},
		"example.com:443":           {Host: "example.com", Port: "443"},
	} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
}

// FuzzParse checks what Parse promises whatever it is given: it never panics, and a host it accepts is a lower-case ASCII DNS name with
// at least two labels that is not an IP address in any spelling the standard library understands.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"example.com", "https://example.com/a?b#c", "127.0.0.1", "[::1]", "a@b.c", "http://0x7f.1", "münchen.de", "example.com:80", "*.example.com", "ｅｘａｍｐｌｅ。ｃｏｍ", "a/b", "%00", "evil.com\\@example.com", "example.com#@evil.com"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got, err := Parse(in)
		if err != nil {
			return
		}
		h := got.Host
		if h == "" || len(h) > 253 || !strings.Contains(h, ".") || strings.ToLower(h) != h {
			t.Fatalf("Parse(%q) accepted a malformed host %q", in, h)
		}
		for _, r := range h {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
				t.Fatalf("Parse(%q) accepted %q with character %q", in, h, r)
			}
		}
		if _, err := netip.ParseAddr(h); err == nil {
			t.Fatalf("Parse(%q) accepted the IP address %q", in, h)
		}
		if got.Scheme != "" && got.Scheme != "http" && got.Scheme != "https" || got.Port != "" && got.Port != "80" && got.Port != "443" {
			t.Fatalf("Parse(%q) = %+v", in, got)
		}
	})
}
