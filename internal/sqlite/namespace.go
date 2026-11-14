package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const rootID = "root"

// PrepareNamespace is an explicit write operation, never part of opening or
// read-only validation. Accounts and ownership remain on the same connection.
func (s *AccountStore) PrepareNamespace(ctx context.Context) error {
	if !s.writable || unicode.Version != files.UnicodeVersion {
		return files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	var version int
	if s.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		return files.ErrUnavailable
	}
	if version == 2 {
		return nil
	}
	if version != 1 {
		return files.ErrUnavailable
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.ErrUnavailable
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE namespace_policy (
 id INTEGER PRIMARY KEY NOT NULL CHECK(id=1),
 algorithm TEXT NOT NULL, unicode_version TEXT NOT NULL
 ) STRICT;
 CREATE TABLE nodes (
 id TEXT PRIMARY KEY NOT NULL,
 parent_id TEXT REFERENCES nodes(id),
 name TEXT NOT NULL,
 name_key TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 modified_at INTEGER NOT NULL,
 CHECK((id='root' AND parent_id IS NULL AND name='' AND name_key='') OR
 (id<>'root' AND parent_id IS NOT NULL AND octet_length(name) BETWEEN 1 AND 255 AND octet_length(name_key) BETWEEN 1 AND 255)),
 UNIQUE(parent_id,name_key)
 ) STRICT;`)
	if err != nil {
		return files.ErrUnavailable
	}
	now := time.Now().UTC().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO namespace_policy VALUES(1,?,?);`, files.Policy, files.UnicodeVersion); err != nil {
		return files.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO nodes VALUES('root',NULL,'','',?,?);`, now, now); err != nil {
		return files.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `PRAGMA user_version=2`); err != nil {
		return files.ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return files.ErrUnavailable
	}
	return nil
}

type namespaceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func lookupCollection(ctx context.Context, q namespaceQuery, names []string) (files.Node, string, error) {
	var node files.Node
	var created, modified int64
	if err := q.QueryRowContext(ctx, `SELECT id,name,created_at,modified_at FROM nodes WHERE id='root'`).Scan(&node.ID, &node.Name, &created, &modified); err != nil {
		return node, "", files.ErrUnavailable
	}
	canonical := make([]string, 0, len(names))
	for _, name := range names {
		err := q.QueryRowContext(ctx, `SELECT id,name,created_at,modified_at FROM nodes WHERE parent_id=? AND name_key=?`, node.ID, files.Key(name)).Scan(&node.ID, &node.Name, &created, &modified)
		if errors.Is(err, sql.ErrNoRows) {
			return files.Node{}, "", files.ErrMissing
		}
		if err != nil {
			return files.Node{}, "", files.ErrUnavailable
		}
		canonical = append(canonical, node.Name)
	}
	node.Created, node.Modified = time.Unix(created, 0).UTC(), time.Unix(modified, 0).UTC()
	return node, strings.Join(canonical, "/"), nil
}

func (s *AccountStore) CreateCollection(ctx context.Context, names []string) error {
	if !s.writable {
		return files.ErrUnavailable
	}
	if len(names) == 0 {
		return files.ErrExists
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.ErrUnavailable
	}
	defer tx.Rollback()
	parent, parentPath, err := lookupCollection(ctx, tx, names[:len(names)-1])
	if errors.Is(err, files.ErrMissing) {
		return files.ErrParentMissing
	}
	if err != nil {
		return err
	}
	name := names[len(names)-1]
	canonical := name
	if parentPath != "" {
		canonical = parentPath + "/" + name
	}
	if _, err := files.Components(canonical); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM nodes WHERE parent_id=? AND name_key=?`, parent.ID, files.Key(name)).Scan(&existing)
	if err == nil {
		return files.ErrExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return files.ErrUnavailable
	}
	var count int
	if tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE parent_id=?`, parent.ID).Scan(&count) != nil {
		return files.ErrUnavailable
	}
	if count >= files.MaxChildren {
		return files.ErrCapacity
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return files.ErrUnavailable
	}
	now := time.Now().UTC().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO nodes VALUES(?,?,?,?,?,?)`, hex.EncodeToString(id[:]), parent.ID, name, files.Key(name), now, now); err != nil {
		return files.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=?`, now, parent.ID); err != nil {
		return files.ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return files.ErrUnavailable
	}
	return nil
}

func (s *AccountStore) CollectionSnapshot(ctx context.Context, names []string, children bool) (files.Snapshot, error) {
	var snapshot files.Snapshot
	if err := s.acquire(ctx); err != nil {
		return snapshot, err
	}
	defer s.release()
	node, path, err := lookupCollection(ctx, s.conn, names)
	if err != nil {
		return snapshot, err
	}
	snapshot.Node, snapshot.Path = node, path
	if !children {
		return snapshot, nil
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT id,name,created_at,modified_at FROM nodes WHERE parent_id=? ORDER BY name_key LIMIT ?`, node.ID, files.MaxChildren+1)
	if err != nil {
		return files.Snapshot{}, files.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var n files.Node
		var created, modified int64
		if rows.Scan(&n.ID, &n.Name, &created, &modified) != nil || len(snapshot.Children) >= files.MaxChildren {
			return files.Snapshot{}, files.ErrUnavailable
		}
		n.Created, n.Modified = time.Unix(created, 0).UTC(), time.Unix(modified, 0).UTC()
		snapshot.Children = append(snapshot.Children, n)
	}
	if rows.Err() != nil {
		return files.Snapshot{}, files.ErrUnavailable
	}
	return snapshot, nil
}

func validateNamespace(ctx context.Context, conn *sql.Conn) error {
	if namespaceShape(ctx, conn) != nil {
		return files.ErrUnavailable
	}
	var triggers int
	if conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.sqlite_schema WHERE type='trigger' AND tbl_name COLLATE NOCASE IN ('nodes','namespace_policy'))`).Scan(&triggers) != nil || triggers != 0 {
		return files.ErrUnavailable
	}
	var algorithm, version string
	if unicode.Version != files.UnicodeVersion || conn.QueryRowContext(ctx, `SELECT CASE WHEN octet_length(algorithm)<=64 THEN algorithm END,CASE WHEN octet_length(unicode_version)<=16 THEN unicode_version END FROM namespace_policy WHERE id=1`).Scan(&algorithm, &version) != nil || algorithm != files.Policy || version != files.UnicodeVersion {
		return files.ErrUnavailable
	}
	return namespaceRows(ctx, conn)
}
