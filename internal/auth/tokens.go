package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// newToken returns a random URL-safe token and the SHA-256 hash that is stored in the database.
// Tokens carry 256 bits of entropy, so an unsalted fast hash is appropriate.
func newToken() (raw string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// HashToken returns the stored form of a raw token.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
