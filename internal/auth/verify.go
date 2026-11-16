package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/argon2"
)

// VerifyPassword uses only the bounded, supported verifier profile. No account
// mutation/rehash occurs; callers must bound simultaneous expensive work.
func (a Account) VerifyPassword(password string) bool {
	if len(password) == 0 || len(password) > 1024 || a.Validate() != nil {
		return false
	}
	parts := strings.Split(a.Verifier, "$")
	salt, _ := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	expected, _ := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	actual := argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
	defer clear(actual)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// DummyAccount is a public noncredential used only to equalize bounded work for
// absent/disabled users. A caller must never authenticate it, even on a match.
func DummyAccount() Account {
	return Account{ID: strings.Repeat("0", 32), Name: "Unknown", Key: "unknown", Created: "2000-01-01T00:00:00Z", Verifier: verifierPrefix + base64.RawStdEncoding.EncodeToString(make([]byte, 16)) + "$" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
}
