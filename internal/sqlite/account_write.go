package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
)

var ErrAccountExists = errors.New("account already exists")

// AddAccount atomically adds one stable identity through the retained validated
// connection. Hashing is the caller's responsibility before entering this method.
func (s *AccountStore) AddAccount(ctx context.Context, account auth.Account) error {
	failed := errors.New("account creation failed")
	if !s.writable || s.conn == nil || account.Validate() != nil {
		return failed
	}
	if s.acquire(ctx) != nil {
		return failed
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return failed
	}
	defer tx.Rollback()
	var exists bool
	if tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE username_key=?)", account.Key).Scan(&exists) != nil {
		return failed
	}
	if exists {
		return ErrAccountExists
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO accounts(id,username,username_key,verifier,enabled,created_at) VALUES(?,?,?,?,1,?)", account.ID, account.Name, account.Key, account.Verifier, account.Created); err != nil {
		return failed
	}
	if tx.Commit() != nil {
		return failed
	}
	return nil
}

var ErrAccountMissing = errors.New("account does not exist")

// ResetPassword changes only the selected account's verifier. Stable identity,
// display spelling, enabled status, creation time and unrelated data are retained.
func (s *AccountStore) ResetPassword(ctx context.Context, username, verifier string) error {
	failed := errors.New("password reset failed")
	if !s.writable || s.conn == nil || !auth.ValidUsername(username) {
		return failed
	}
	if s.acquire(ctx) != nil {
		return failed
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return failed
	}
	defer tx.Rollback()
	var account auth.Account
	err = tx.QueryRowContext(ctx, "SELECT id,username,username_key,created_at FROM accounts WHERE username_key=?", strings.ToLower(username)).Scan(&account.ID, &account.Name, &account.Key, &account.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAccountMissing
	}
	if err != nil {
		return failed
	}
	account.Verifier = verifier
	if account.Validate() != nil {
		return failed
	}
	result, err := tx.ExecContext(ctx, "UPDATE accounts SET verifier=? WHERE id=?", verifier, account.ID)
	if err != nil {
		return failed
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return failed
	}
	if tx.Commit() != nil {
		return failed
	}
	return nil
}
