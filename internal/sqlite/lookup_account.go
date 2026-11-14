package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
)

// LookupEnabled reads through the retained connection on which the local session
// validated schema/accounts. It never reopens the pool or performs writes.
func (s *AccountStore) LookupEnabled(ctx context.Context, name string) (auth.Account, bool, error) {
	var a auth.Account
	if !auth.ValidUsername(name) {
		return a, false, nil
	}
	err := s.conn.QueryRowContext(ctx, `SELECT
CASE WHEN typeof(id)='text' AND octet_length(id)=32 THEN id END,
CASE WHEN typeof(username)='text' AND octet_length(username)<=64 THEN username END,
CASE WHEN typeof(username_key)='text' AND octet_length(username_key)<=64 THEN username_key END,
CASE WHEN typeof(verifier)='text' AND octet_length(verifier)<=128 THEN verifier END,
CASE WHEN typeof(created_at)='text' AND octet_length(created_at)<=40 THEN created_at END
FROM accounts WHERE username_key=? AND enabled=1`, strings.ToLower(name)).Scan(&a.ID, &a.Name, &a.Key, &a.Verifier, &a.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Account{}, false, nil
	}
	if err != nil || a.Validate() != nil {
		return auth.Account{}, false, errors.New("account lookup failed")
	}
	return a, true, nil
}
