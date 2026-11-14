// Package sqlite persists versioned application state. It never selects an
// alternate database or creates a missing database on an existing-state path.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	_ "modernc.org/sqlite"
)

func open(path, mode string) (*sql.DB, error) {
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := u.Query()
	q.Set("mode", mode)
	q.Add("_pragma", "trusted_schema(OFF)")
	q.Add("_pragma", "busy_timeout(0)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// Validate opens only the selected existing database, read-only. It does not
// migrate, repair, reseed or recover partial state by performing writes.
func Validate(ctx context.Context, path string) error {
	// Read-only SQLite connections can create WAL/SHM files for a database
	// whose persistent journal mode is WAL, even after a clean close. Check
	// the fixed header before any SQLite open; accept only rollback format.
	// https://www.sqlite.org/fileformat.html#file_format_version_numbers
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var header [100]byte
	_, readErr := io.ReadFull(f, header[:])
	closeErr := f.Close()
	if readErr != nil || string(header[:16]) != "SQLite format 3\x00" || header[18] != 1 || header[19] != 1 {
		return errors.New("unsupported database file format")
	}
	if closeErr != nil {
		return closeErr
	}
	db, err := open(path, "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var version, application int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&application); err != nil {
		return err
	}
	if version != 1 || application != 1195655510 {
		return errors.New("unsupported database schema")
	}
	if err := validateSchema(ctx, db); err != nil {
		return err
	}
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("invalid database integrity")
	}
	// Guard encoded field sizes in SQLite before materializing them in Go.
	// NULL on any unsupported field makes Scan fail closed, not skip the row.
	rows, err := db.QueryContext(ctx, `SELECT
CASE WHEN typeof(id)='text' AND octet_length(id)=32 THEN id END,
CASE WHEN typeof(username)='text' AND octet_length(username)<=64 THEN username END,
CASE WHEN typeof(username_key)='text' AND octet_length(username_key)<=64 THEN username_key END,
CASE WHEN typeof(verifier)='text' AND octet_length(verifier)<=128 THEN verifier END,
CASE WHEN typeof(enabled)='integer' THEN enabled END,
CASE WHEN typeof(created_at)='text' AND octet_length(created_at)<=40 THEN created_at END
FROM accounts`)
	if err != nil {
		return err
	}
	defer rows.Close()
	keys, ids := map[string]bool{}, map[string]bool{}
	enabledCount := 0
	for rows.Next() {
		var a auth.Account
		var enabled int
		if err := rows.Scan(&a.ID, &a.Name, &a.Key, &a.Verifier, &enabled, &a.Created); err != nil {
			return err
		}
		if a.Validate() != nil || ids[a.ID] || keys[a.Key] || (enabled != 0 && enabled != 1) {
			return errors.New("invalid account state")
		}
		ids[a.ID], keys[a.Key] = true, true
		enabledCount += enabled
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if enabledCount == 0 {
		return errors.New("uninitialized accounts")
	}
	return nil
}

// Initialize requires an exclusively claimed, precreated empty database. The
// complete initial schema and identity commit together, never in separate steps.
func Initialize(ctx context.Context, path string, account auth.Account) error {
	db, err := open(path, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;"); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE accounts (
id TEXT PRIMARY KEY NOT NULL,
username TEXT NOT NULL,
username_key TEXT UNIQUE NOT NULL,
verifier TEXT NOT NULL,
enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
created_at TEXT NOT NULL
) STRICT;
PRAGMA application_id=1195655510;
PRAGMA user_version=1;`)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO accounts(id,username,username_key,verifier,enabled,created_at) VALUES(?,?,?,?,1,?)", account.ID, account.Name, account.Key, account.Verifier, account.Created); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return db.Close()
}
