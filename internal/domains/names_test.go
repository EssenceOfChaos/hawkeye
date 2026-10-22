package domains

import (
	"testing"
	"time"
)

func TestRegistrable(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"example.com", "example.com", true},
		{"www.Example.COM", "example.com", true},
		{"https://api.example.co.uk/health?x=1", "example.co.uk", true},
		{"https://user.example.com:443", "example.com", true},
		{"shop.example.com.", "example.com", true},
		{"bücher.de", "xn--bcher-kva.de", true},
		{"blog.münchen.de", "xn--mnchen-3ya.de", true},
		{"", "", false},
		{"localhost", "", false},
		{"com", "", false},
		{"co.uk", "", false},
		{"127.0.0.1", "", false},
		{"[::1]", "", false},
		{"https://203.0.113.9/", "", false},
		{"something.invalidtld", "", false},
		{"mysite.vercel.app", "", false}, // under a private suffix: not the customer's registration
		{"me.github.io", "", false},
		{"a b.com", "", false},
		{"https://%zz", "", false},
	}
	for _, c := range cases {
		got, ok := Registrable(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("Registrable(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestTLD(t *testing.T) {
	if got := TLD("example.co.uk"); got != "uk" {
		t.Fatalf("TLD = %q", got)
	}
	if got := TLD("com"); got != "com" {
		t.Fatalf("TLD = %q", got)
	}
}

func TestStatusAndBucket(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, c := range []struct {
		left   time.Duration
		status string
		bucket int
		warn   bool
		days   int
	}{
		{90 * day, StatusValid, 0, false, 90},
		{31 * day, StatusValid, 0, false, 31},
		{30 * day, StatusExpiring, 30, true, 30},
		{10 * day, StatusExpiring, 14, true, 10},
		{3 * day, StatusExpiring, 7, true, 3},
		{12 * time.Hour, StatusExpiring, 1, true, 0},
		{-time.Hour, StatusExpired, 0, true, -1},
	} {
		exp := now.Add(c.left)
		if got := Status(exp, now); got != c.status {
			t.Errorf("Status(%v) = %q, want %q", c.left, got, c.status)
		}
		b, ok := Bucket(exp, now)
		if b != c.bucket || ok != c.warn {
			t.Errorf("Bucket(%v) = %d, %v; want %d, %v", c.left, b, ok, c.bucket, c.warn)
		}
		if got := DaysRemaining(exp, now); got != c.days {
			t.Errorf("DaysRemaining(%v) = %d, want %d", c.left, got, c.days)
		}
	}
}
