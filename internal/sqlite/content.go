package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const fileSizeCheck = `size BETWEEN 0 AND 67108864 AND length(content)=size`

func (s *AccountStore) PrepareFiles(ctx context.Context) error {
	if !s.writable {
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
	if version == 3 {
		return nil
	}
	if version != 2 {
		return files.ErrUnavailable
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.ErrUnavailable
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE file_contents (
 node_id TEXT PRIMARY KEY NOT NULL REFERENCES nodes(id),
 content BLOB NOT NULL, size INTEGER NOT NULL,
 content_type TEXT NOT NULL, etag TEXT NOT NULL,
 CHECK(node_id<>'root'), CHECK(`+fileSizeCheck+`),
 CHECK(octet_length(content_type) BETWEEN 1 AND 255),
 CHECK(octet_length(etag)=34)
 ) STRICT; PRAGMA user_version=3;`)
	if err != nil || tx.Commit() != nil {
		return files.ErrUnavailable
	}
	return nil
}

func fileMetadata(ctx context.Context, q namespaceQuery, node *files.Node) error {
	err := q.QueryRowContext(ctx, `SELECT size,content_type,etag FROM file_contents WHERE node_id=?`, node.ID).Scan(&node.Size, &node.ContentType, &node.ETag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return files.ErrUnavailable
	}
	node.File = true
	return nil
}

func (s *AccountStore) PutFile(ctx context.Context, names []string, data []byte, contentType string) (files.Node, bool, error) {
	if !s.writable || len(names) == 0 || int64(len(data)) > files.AbsoluteMaxFileBytes {
		return files.Node{}, false, files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return files.Node{}, false, err
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	defer tx.Rollback()
	parent, parentPath, err := lookupNode(ctx, tx, names[:len(names)-1])
	if errors.Is(err, files.ErrMissing) || parent.File {
		return files.Node{}, false, files.ErrParentMissing
	}
	if err != nil {
		return files.Node{}, false, err
	}
	name := names[len(names)-1]
	path := name
	if parentPath != "" {
		path = parentPath + "/" + name
	}
	if _, err = files.Components(path); err != nil {
		return files.Node{}, false, err
	}
	node, _, err := lookupNode(ctx, tx, names)
	created := errors.Is(err, files.ErrMissing)
	if err != nil && !created {
		return files.Node{}, false, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if created {
		var count int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE parent_id=?`, parent.ID).Scan(&count) != nil {
			return files.Node{}, false, files.ErrUnavailable
		}
		if count >= files.MaxChildren {
			return files.Node{}, false, files.ErrCapacity
		}
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return files.Node{}, false, files.ErrUnavailable
		}
		node = files.Node{ID: hex.EncodeToString(id[:]), Name: name, Created: now, Modified: now, File: true}
		if _, err = tx.ExecContext(ctx, `INSERT INTO nodes VALUES(?,?,?,?,?,?)`, node.ID, parent.ID, name, files.Key(name), now.Unix(), now.Unix()); err != nil {
			return files.Node{}, false, files.ErrUnavailable
		}
		if _, err = tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=?`, now.Unix(), parent.ID); err != nil {
			return files.Node{}, false, files.ErrUnavailable
		}
	} else if !node.File {
		return files.Node{}, false, files.ErrExists
	}
	var version [16]byte
	if _, err = rand.Read(version[:]); err != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	node.ETag = `"` + hex.EncodeToString(version[:]) + `"`
	node.ContentType = contentType
	node.Size = int64(len(data))
	node.Modified = now
	if _, err = tx.ExecContext(ctx, `INSERT INTO file_contents VALUES(?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET content=excluded.content,size=excluded.size,content_type=excluded.content_type,etag=excluded.etag`, node.ID, data, node.Size, node.ContentType, node.ETag); err != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=?`, now.Unix(), node.ID); err != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	if fileCommitFailure(ctx) != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	if err = tx.Commit(); err != nil {
		return files.Node{}, false, files.ErrUnavailable
	}
	return node, created, nil
}

func (s *AccountStore) ReadFile(ctx context.Context, names []string) (files.Snapshot, []byte, error) {
	if err := s.acquire(ctx); err != nil {
		return files.Snapshot{}, nil, err
	}
	defer s.release()
	node, path, err := lookupNode(ctx, s.conn, names)
	if err != nil {
		return files.Snapshot{}, nil, err
	}
	if !node.File {
		return files.Snapshot{}, nil, files.ErrExists
	}
	if fileReadFailure() != nil {
		return files.Snapshot{}, nil, files.ErrUnavailable
	}
	var data []byte
	if s.conn.QueryRowContext(ctx, `SELECT CASE WHEN typeof(content)='blob' AND length(content)<=67108864 THEN content END FROM file_contents WHERE node_id=?`, node.ID).Scan(&data) != nil || int64(len(data)) != node.Size {
		return files.Snapshot{}, nil, files.ErrUnavailable
	}
	return files.Snapshot{Node: node, Path: path}, data, nil
}
