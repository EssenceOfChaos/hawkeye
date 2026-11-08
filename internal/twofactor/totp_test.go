package twofactor

import (
	"strings"
	"testing"
	"time"
)

// The SHA-1 test vectors from RFC 6238 appendix B (secret "12345678901234567890"), last 6 digits.
func TestCodesMatchRFC6238(t *testing.T) {
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	for _, c := range []struct {
		unix int64
		want string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		got, err := codeAt(secret, c.unix/stepSeconds)
		if err != nil || got != c.want {
			t.Errorf("t=%d: got %q, %v; want %q", c.unix, got, err, c.want)
		}
	}
}

func TestVerifyAcceptsNeighboringStepsOnceEach(t *testing.T) {
	secret, err := newSecret()
	if err != nil || len(secret) != 32 {
		t.Fatalf("%q %v", secret, err)
	}
	now := time.Unix(1_800_000_000, 0)
	for _, d := range []int64{-1, 0, 1} {
		code, _ := codeAt(secret, stepOf(now)+d)
		if _, ok := verify(secret, code, now, nil); !ok {
			t.Fatalf("step %+d should be accepted", d)
		}
	}
	for _, d := range []int64{-2, 2} {
		code, _ := codeAt(secret, stepOf(now)+d)
		if _, ok := verify(secret, code, now, nil); ok {
			t.Fatalf("step %+d is too far off", d)
		}
	}

	code, _ := codeAt(secret, stepOf(now))
	step, ok := verify(secret, code[:3]+" "+code[3:], now, nil)
	if !ok {
		t.Fatal("spaces are ignored")
	}
	if _, ok := verify(secret, code, now, &step); ok {
		t.Fatal("the same code must not work twice")
	}
	earlier, _ := codeAt(secret, stepOf(now)-1)
	if _, ok := verify(secret, earlier, now, &step); ok {
		t.Fatal("an older code must not work after a newer one was used")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := verify(secret, bad, now, nil); ok {
			t.Fatalf("%q", bad)
		}
	}
}

func TestOtpauthURI(t *testing.T) {
	u := otpauthURI("HawkEye", "me@example.com", "ABCDEF")
	if !strings.HasPrefix(u, "otpauth://totp/HawkEye:me@example.com?") || !strings.Contains(u, "secret=ABCDEF") || !strings.Contains(u, "issuer=HawkEye") {
		t.Fatal(u)
	}
}
