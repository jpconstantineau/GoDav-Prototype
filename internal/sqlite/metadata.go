package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"maps"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

var metadataKeys = [4]string{"Win32CreationTime", "Win32LastModifiedTime", "Win32LastAccessTime", "Win32FileAttributes"}

const metadataCheck = `(creation IS NULL OR octet_length(creation)<=4096) AND (modified IS NULL OR octet_length(modified)<=4096) AND (accessed IS NULL OR octet_length(accessed)<=4096) AND (attributes IS NULL OR octet_length(attributes)<=4096)`

// Called only under the existing explicit preparation operation gate.
func (s *AccountStore) prepareMetadata(ctx context.Context) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.ErrUnavailable
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE node_metadata(node_id TEXT PRIMARY KEY NOT NULL REFERENCES nodes(id),creation TEXT,modified TEXT,accessed TEXT,attributes TEXT,CHECK(`+metadataCheck+`)) STRICT; PRAGMA user_version=4;`)
	if err != nil || metadataMigrationFailure() != nil || tx.Commit() != nil {
		return files.ErrUnavailable
	}
	return nil
}
func loadMetadata(ctx context.Context, q namespaceQuery, node *files.Node) error {
	var values [4]sql.NullString
	err := q.QueryRowContext(ctx, `SELECT creation,modified,accessed,attributes FROM node_metadata WHERE node_id=?`, node.ID).Scan(&values[0], &values[1], &values[2], &values[3])
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return files.ErrUnavailable
	}
	node.Metadata = map[string]string{}
	for i, v := range values {
		if v.Valid {
			node.Metadata[metadataKeys[i]] = v.String
		}
	}
	return nil
}
func (s *AccountStore) PatchMetadata(ctx context.Context, names []string, update files.MetadataUpdate, expectation files.Expectation) (files.Node, error) {
	if !s.writable {
		return files.Node{}, files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return files.Node{}, err
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.Node{}, files.ErrUnavailable
	}
	defer tx.Rollback()
	node, _, err := lookupNode(ctx, tx, names)
	if err != nil {
		return files.Node{}, err
	}
	if update.CollectionOnly && node.File {
		return files.Node{}, files.ErrInvalid
	}
	if err := expectation.CheckWrite(node, true); err != nil {
		return files.Node{}, err
	}
	before := maps.Clone(node.Metadata)
	if node.Metadata == nil {
		node.Metadata = map[string]string{}
	}
	for _, change := range update.Changes {
		if change.Remove {
			delete(node.Metadata, change.Name)
		} else {
			node.Metadata[change.Name] = change.Value
		}
	}
	if maps.Equal(before, node.Metadata) {
		return node, nil
	}
	var values [4]any
	for i, key := range metadataKeys {
		if value, ok := node.Metadata[key]; ok {
			values[i] = value
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO node_metadata VALUES(?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET creation=excluded.creation,modified=excluded.modified,accessed=excluded.accessed,attributes=excluded.attributes`, node.ID, values[0], values[1], values[2], values[3]); err != nil {
		return files.Node{}, files.ErrUnavailable
	}
	if node.File {
		var tag [16]byte
		if _, err = rand.Read(tag[:]); err != nil {
			return files.Node{}, files.ErrUnavailable
		}
		node.ETag = `"` + hex.EncodeToString(tag[:]) + `"`
		if _, err = tx.ExecContext(ctx, `UPDATE file_contents SET etag=? WHERE node_id=?`, node.ETag, node.ID); err != nil {
			return files.Node{}, files.ErrUnavailable
		}
	}
	if metadataCommitFailure(ctx) != nil {
		return files.Node{}, files.ErrUnavailable
	}
	if tx.Commit() != nil {
		return files.Node{}, files.ErrUnavailable
	}
	return node, nil
}
func validateMetadata(ctx context.Context, conn *sql.Conn) error {
	if namespaceTable(ctx, conn, "node_metadata", []namespaceColumn{{"node_id", "TEXT", 1, 1}, {"creation", "TEXT", 0, 0}, {"modified", "TEXT", 0, 0}, {"accessed", "TEXT", 0, 0}, {"attributes", "TEXT", 0, 0}}) != nil || !namespaceCheck(ctx, conn, "node_metadata", metadataCheck) {
		return files.ErrUnavailable
	}
	var count, matching int
	if conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum("table"='nodes' AND "from"='node_id' AND "to"='id' AND on_update='NO ACTION' AND on_delete='NO ACTION'),0) FROM pragma_foreign_key_list('node_metadata','main')`).Scan(&count, &matching) != nil || count != 1 || matching != 1 {
		return files.ErrUnavailable
	}
	var bad int
	if conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.sqlite_schema WHERE type='trigger' AND tbl_name='node_metadata' COLLATE NOCASE) OR EXISTS(SELECT 1 FROM node_metadata m LEFT JOIN nodes n ON n.id=m.node_id WHERE n.id IS NULL) OR EXISTS(SELECT 1 FROM node_metadata WHERE NOT (`+metadataCheck+`))`).Scan(&bad) != nil || bad != 0 {
		return files.ErrUnavailable
	}
	rows, err := conn.QueryContext(ctx, `SELECT creation,modified,accessed,attributes FROM node_metadata`)
	if err != nil {
		return files.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var v [4]sql.NullString
		if rows.Scan(&v[0], &v[1], &v[2], &v[3]) != nil {
			return files.ErrUnavailable
		}
		for _, value := range v {
			if value.Valid && !files.ValidMetadataValue(value.String) {
				return files.ErrUnavailable
			}
		}
	}
	if rows.Err() != nil {
		return files.ErrUnavailable
	}
	return nil
}
