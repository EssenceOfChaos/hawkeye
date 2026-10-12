package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Hasher hashes passwords with Argon2id and encodes them in the standard PHC string format,
// so parameters can be raised later while old hashes keep verifying.
type Hasher struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

const (
	saltLen = 16
	keyLen  = 32
)

// DefaultHasher uses 64 MiB, 3 iterations, 2 lanes (above the OWASP minimum).
func DefaultHasher() Hasher { return Hasher{Memory: 64 * 1024, Time: 3, Threads: 2} }

func (h Hasher) Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.Time, h.Memory, h.Threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.Memory, h.Time, h.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

var errBadHash = errors.New("malformed password hash")

// Verify checks password against an encoded hash in constant time.
func Verify(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var h Hasher
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.Memory, &h.Time, &h.Threads); err != nil {
		return false, errBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, h.Time, h.Memory, h.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
