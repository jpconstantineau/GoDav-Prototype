// Package auth owns account identity and bounded password-verifier semantics.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

type Account struct {
	ID, Name, Key, Verifier string
	Created                 string
}

const verifierPrefix = "$argon2id$v=19$m=19456,t=2,p=1$"

// ValidUsername defines the account-domain policy shared by CLI input and
// persisted-state validation: 1-64 ASCII bytes, starting with an alphanumeric.
func ValidUsername(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := range len(name) {
		c := name[i]
		alphanumeric := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alphanumeric && (i == 0 || c != '.' && c != '_' && c != '-') {
			return false
		}
	}
	return true
}

// Validate rejects unsupported or excessive verifier costs before any future
// authentication code can allocate hash work from stored parameters.
func (a Account) Validate() error {
	bad := errors.New("invalid account state")
	if len(a.ID) != 32 || len(a.Verifier) != len(verifierPrefix)+22+1+43 || len(a.Created) > 40 {
		return bad
	}
	id, err := hex.DecodeString(a.ID)
	if err != nil || len(id) != 16 || hex.EncodeToString(id) != a.ID {
		return bad
	}
	if !ValidUsername(a.Name) || a.Key != strings.ToLower(a.Name) {
		return bad
	}
	if _, err := time.Parse(time.RFC3339Nano, a.Created); err != nil {
		return bad
	}
	parts := strings.Split(a.Verifier, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return bad
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return bad
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) != 32 {
		return bad
	}
	return nil
}

// Generate creates a synthetic or operator-requested initial account. The
// caller must persist only Account and privately disclose password after commit.
// Hashing happens outside any SQLite transaction.
func Generate(name string) (account Account, password string, err error) {
	var random [24]byte
	var salt [16]byte
	var id [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return
	}
	if _, err = rand.Read(salt[:]); err != nil {
		return
	}
	if _, err = rand.Read(id[:]); err != nil {
		return
	}
	password = base64.RawURLEncoding.EncodeToString(random[:])
	clear(random[:])
	hash := argon2.IDKey([]byte(password), salt[:], 2, 19*1024, 1, 32)
	defer clear(hash)
	account = Account{
		ID: hex.EncodeToString(id[:]), Name: name, Key: strings.ToLower(name),
		Verifier: verifierPrefix + base64.RawStdEncoding.EncodeToString(salt[:]) + "$" + base64.RawStdEncoding.EncodeToString(hash),
		Created:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	return
}
