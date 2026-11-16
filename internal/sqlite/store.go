package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// AccountStore holds the exact connection on which existing state was validated.
// It exposes no raw SQL, pool or transaction. Callers must drain users before
// Close; feature-specific operations belong in separate files in this package.
type AccountStore struct {
	db        *sql.DB
	conn      *sql.Conn
	writable  bool
	operation chan struct{}
	locks     files.LockTable
}

// OpenExistingReadOnly requires private path checks and exclusive ownership
// already held by localstate. It never creates, migrates, repairs or reseeds.
func OpenExistingReadOnly(ctx context.Context, path string) (*AccountStore, error) {
	return openExisting(ctx, path, false)
}

// OpenExistingForUpdate also requires a pinned private DB parent whose restricted
// ACL has file-inheritable grants, protecting rollback journals at creation.
func OpenExistingForUpdate(ctx context.Context, path string) (*AccountStore, error) {
	return openExisting(ctx, path, true)
}

func openExisting(ctx context.Context, path string, writable bool) (*AccountStore, error) {
	invalid := errors.New("invalid or unsupported database/account state")
	if ctx.Err() != nil || validateHeader(path) != nil {
		return nil, invalid
	}
	mode := "ro"
	if writable {
		mode = "rw"
	}
	db, err := open(path, mode)
	if err != nil {
		return nil, invalid
	}
	s := &AccountStore{db: db, writable: writable, operation: make(chan struct{}, 1)}
	opened := false
	defer func() {
		if !opened {
			s.Close()
		}
	}()
	s.conn, err = db.Conn(ctx)
	if err != nil {
		return nil, invalid
	}
	// Never convert journal mode. The header guard precedes SQLite open and
	// the live connection must confirm rollback mode. busy_timeout(0) and
	// trusted_schema(OFF) are installed by open on this same connection.
	var journal string
	if s.conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal) != nil || journal != "delete" {
		return nil, invalid
	}
	if writable {
		if _, err := s.conn.ExecContext(ctx, "PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON;"); err != nil {
			return nil, invalid
		}
	}
	if validateAccounts(ctx, s.conn) != nil || ctx.Err() != nil {
		return nil, invalid
	}
	opened = true
	return s, nil
}

// Close is idempotent, attempts both closes and returns only a fixed error.
func (s *AccountStore) Close() error {
	failed := false
	if s.conn != nil {
		failed = s.conn.Close() != nil
		s.conn = nil
	}
	if s.db != nil {
		if s.db.Close() != nil {
			failed = true
		}
		s.db = nil
	}
	if failed {
		return errors.New("account store close failed")
	}
	return nil
}
