package auth

import (
	"net/mail"
	"strings"
	"unicode/utf8"

	"hawkeye/internal/validation"
)

const (
	MinPasswordLen = 10
	MaxPasswordLen = 128 // bounds Argon2 work per request
	maxEmailLen    = 254
)

// NormalizeEmail trims and lower-cases an address and checks it is a plain mailbox.
func NormalizeEmail(s string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(s))
	if e == "" || len(e) > maxEmailLen {
		return "", validation.New("email", "must be a valid email address")
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return "", validation.New("email", "must be a valid email address")
	}
	return e, nil
}

func ValidatePassword(p string) error {
	n := utf8.RuneCountInString(p)
	if n < MinPasswordLen || len(p) > MaxPasswordLen {
		return validation.New("password", "must be between 10 and 128 characters")
	}
	return nil
}
