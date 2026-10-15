package testutil

import "hawkeye/internal/secrets"

// SecretBox returns a fixed, test-only cipher for services that store monitor credentials.
func SecretBox() *secrets.Box {
	b, err := secrets.NewFromPassphrase("test-only")
	if err != nil {
		panic(err)
	}
	return b
}
