package secrets

import (
	"encoding/base64"
	"strings"
	"testing"
)

func key(b byte) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune(b)), 32)))
}

func TestRoundTripAndNoPlaintextInOutput(t *testing.T) {
	box, err := New(key('a'))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal("s3cr3t-token", "monitor-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "s3cr3t") || !IsSealed(sealed) {
		t.Fatalf("not sealed: %q", sealed)
	}
	got, err := box.Open(sealed, "monitor-1")
	if err != nil || got != "s3cr3t-token" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestEverySealIsDifferent(t *testing.T) {
	box, _ := New(key('a'))
	a, _ := box.Seal("same", "ctx")
	b, _ := box.Seal("same", "ctx")
	if a == b {
		t.Fatal("nonce reuse: identical ciphertexts for the same plaintext")
	}
}

func TestValueCannotBeMovedToAnotherRowOrOpenedWithAnotherKey(t *testing.T) {
	box, _ := New(key('a'))
	sealed, _ := box.Seal("x", "monitor-1")
	if _, err := box.Open(sealed, "monitor-2"); err == nil {
		t.Fatal("opened under a different context")
	}
	other, _ := New(key('b'))
	if _, err := other.Open(sealed, "monitor-1"); err == nil {
		t.Fatal("opened with a different key")
	}
}

func TestTamperingIsDetected(t *testing.T) {
	box, _ := New(key('a'))
	sealed, _ := box.Seal("hello", "c")
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	raw[len(raw)-1] ^= 1
	if _, err := box.Open(prefix+base64.StdEncoding.EncodeToString(raw), "c"); err == nil {
		t.Fatal("modified ciphertext was accepted")
	}
}

func TestFailsClosedWithoutAKeyAndRejectsPlaintext(t *testing.T) {
	var none *Box
	if _, err := none.Seal("x", "c"); err != ErrNoKey {
		t.Fatalf("seal without key: %v", err)
	}
	box, _ := New(key('a'))
	if _, err := box.Open("plain text value", "c"); err != ErrNotSealed {
		t.Fatalf("plain text must not be treated as sealed: %v", err)
	}
	if _, err := none.Open("enc:v1:AAAA", "c"); err != ErrNoKey {
		t.Fatalf("open without key: %v", err)
	}
}

func TestKeyValidation(t *testing.T) {
	for _, bad := range []string{"", "not base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := New(bad); err == nil {
			t.Errorf("accepted key %q", bad)
		}
	}
}

func TestPassphraseKeysAreStableAndDistinct(t *testing.T) {
	a1, _ := NewFromPassphrase("secret-a")
	a2, _ := NewFromPassphrase("secret-a")
	b, _ := NewFromPassphrase("secret-b")
	sealed, err := a1.Seal("token", "monitor-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := a2.Open(sealed, "monitor-1"); err != nil || got != "token" {
		t.Fatalf("same secret must derive the same key: %q %v", got, err)
	}
	if _, err := b.Open(sealed, "monitor-1"); err == nil {
		t.Fatal("a different secret must not open the value")
	}
}
