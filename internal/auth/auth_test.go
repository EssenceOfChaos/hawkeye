package auth

import (
	"strings"
	"testing"
)

var fastHasher = Hasher{Memory: 8, Time: 1, Threads: 1}

func TestHashAndVerify(t *testing.T) {
	h, err := fastHasher.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("unexpected format: %s", h)
	}
	if ok, err := Verify("correct horse battery", h); err != nil || !ok {
		t.Fatalf("expected match: %v %v", ok, err)
	}
	if ok, _ := Verify("wrong", h); ok {
		t.Fatal("wrong password verified")
	}
	h2, _ := fastHasher.Hash("correct horse battery")
	if h == h2 {
		t.Fatal("salts must differ")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=8,t=1,p=1$!!$!!", "$bcrypt$v=19$a$b$c"} {
		if ok, err := Verify("x", bad); ok || err == nil {
			t.Errorf("%q: ok=%v err=%v", bad, ok, err)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  Alice@Example.COM ")
	if err != nil || got != "alice@example.com" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "nope", "a@b", "Alice <a@example.com>", "a@@example.com", strings.Repeat("a", 250) + "@example.com"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil || ValidatePassword(strings.Repeat("a", 129)) == nil {
		t.Fatal("length limits not enforced")
	}
	if err := ValidatePassword("long enough pw"); err != nil {
		t.Fatal(err)
	}
}

func TestTokensAreRandomAndHashed(t *testing.T) {
	a, ha, _ := newToken()
	b, _, _ := newToken()
	if a == b || len(a) < 40 || string(HashToken(a)) != string(ha) {
		t.Fatal("token generation broken")
	}
}
