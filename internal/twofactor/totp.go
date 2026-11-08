package twofactor

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP as authenticator apps implement it (RFC 6238 over RFC 4226): HMAC-SHA1, 30-second steps, 6 digits. Those are the defaults every app
// supports, so the otpauth URI leaves them implicit-compatible but states them anyway.
const (
	stepSeconds = 30
	digits      = 6
	// skew accepts the previous and next step too, for clocks that are a little off and codes typed as they roll over.
	skew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newSecret returns 160 random bits, base32-encoded as authenticator apps expect.
func newSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return b32.EncodeToString(b), nil
}

// codeAt computes the code for one time step.
func codeAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("bad TOTP secret: %w", err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", digits, bin%1_000_000), nil
}

func stepOf(t time.Time) int64 { return t.Unix() / stepSeconds }

// normalizeCode keeps only digits, so "123 456" and "123-456" work.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// verify reports the step a code matches (within skew of now), refusing steps at or before lastStep so a code can't be used twice.
func verify(secret, code string, now time.Time, lastStep *int64) (int64, bool) {
	code = normalizeCode(code)
	if len(code) != digits {
		return 0, false
	}
	cur := stepOf(now)
	for d := -skew; d <= skew; d++ {
		step := cur + int64(d)
		if lastStep != nil && step <= *lastStep {
			continue
		}
		want, err := codeAt(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// otpauthURI is what the QR code holds: authenticator apps read the issuer, the account label and the secret from it.
func otpauthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(digits)}, "period": {fmt.Sprint(stepSeconds)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Code returns the code an authenticator app shows for secret at t. HawkEye only checks codes; this exists for tests and support tooling.
func Code(secret string, t time.Time) (string, error) { return codeAt(secret, stepOf(t)) }
