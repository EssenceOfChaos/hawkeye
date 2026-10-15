// Package secrets encrypts small values (monitor credentials) before they are stored, so a database dump or backup does not contain them in clear.
//
// Values are sealed with AES-256-GCM using a 32-byte key from the environment. Each value gets a random nonce and is bound to a context string
// (the monitor's ID), so a sealed value copied onto another row fails to open. Sealed values look like "enc:v1:<base64>".
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const prefix = "enc:v1:"

// ErrNotSealed is returned by Open for a value that was not produced by Seal (for example a legacy plain-text value).
var ErrNotSealed = errors.New("value is not encrypted")

// Box seals and opens values with one key. It is safe for concurrent use. The zero value is not usable; a nil *Box refuses to seal or open.
type Box struct{ aead cipher.AEAD }

// ErrNoKey is returned when encryption is needed but no key is configured. Callers must fail rather than store plain text.
var ErrNoKey = errors.New("secrets encryption key is not configured")

// New builds a Box from a standard-base64 encoded 32-byte key (for example `openssl rand -base64 32`).
func New(encodedKey string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		return nil, fmt.Errorf("key must be base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("key must decode to 32 bytes, got %d", len(key))
	}
	return fromKey(key)
}

// NewFromPassphrase derives a key from an arbitrary string. It exists for development and tests, where a random key per run would make
// stored values unreadable after a restart; production uses New with a dedicated random key.
func NewFromPassphrase(passphrase string) (*Box, error) {
	sum := sha256.Sum256([]byte("hawkeye/secrets/v1:" + passphrase))
	return fromKey(sum[:])
}

func fromKey(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext, binding it to context.
func (b *Box) Seal(plaintext, context string) (string, error) {
	if b == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context))
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal with the same context.
func (b *Box) Open(sealed, context string) (string, error) {
	if !IsSealed(sealed) {
		return "", ErrNotSealed
	}
	if b == nil {
		return "", ErrNoKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, prefix))
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("sealed value is malformed")
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], []byte(context))
	if err != nil {
		return "", errors.New("could not decrypt value (wrong key or value moved)")
	}
	return string(plain), nil
}

// IsSealed reports whether v looks like a value produced by Seal.
func IsSealed(v string) bool { return strings.HasPrefix(v, prefix) }
