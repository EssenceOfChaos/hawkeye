package monitors

import "testing"

func TestNormalizeURL(t *testing.T) {
	good := map[string]string{
		"https://Example.com/Path?q=1#frag": "https://example.com/Path?q=1",
		"  http://example.com  ":            "http://example.com",
		"https://example.com:443/":          "https://example.com:443/",
		"https://sub.example.co.uk/a/b":     "https://sub.example.co.uk/a/b",
		"https://8.8.8.8/":                  "https://8.8.8.8/",
	}
	for in, want := range good {
		got, reason := NormalizeURL(in)
		if reason != "" || got != want {
			t.Errorf("%q: got %q (%s), want %q", in, got, reason, want)
		}
	}
	bad := []string{
		"", "example.com", "ftp://example.com", "javascript:alert(1)", "file:///etc/passwd", "//example.com",
		"https://user:pw@example.com", "https://", "http://localhost", "http://app.localhost", "http://printer.local",
		"http://metadata.internal", "http://intranet", "http://127.0.0.1", "http://10.0.0.1", "http://192.168.1.1",
		"http://169.254.169.254/latest/meta-data", "http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://0.0.0.0",
		"http://2130706433", "https://example.com:8080", "https://example.com:22", "http://[fe80::1]/",
		"https://example.com/" + string(make([]byte, 2100)),
	}
	for _, in := range bad {
		if got, reason := NormalizeURL(in); reason == "" {
			t.Errorf("%q should be rejected, got %q", in, got)
		}
	}
}
