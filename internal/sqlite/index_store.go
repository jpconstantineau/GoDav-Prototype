package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
)

const (
	maxIndexNodes   = 2048
	maxIndexBytes   = 128 << 20
	maxIndexReplans = 8
)

const indexJobDDL = `CREATE TABLE godav_idx_job(
 id INTEGER PRIMARY KEY NOT NULL CHECK(id=1), job_id TEXT NOT NULL, owner TEXT NOT NULL, scope TEXT NOT NULL,
 root_id TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('queued','running','paused','completed','cancelled','failed')),
 error_code TEXT NOT NULL, file_limit INTEGER NOT NULL, replans INTEGER NOT NULL
) STRICT`

const indexWorkDDL = `CREATE TABLE godav_idx_work(
 node_id TEXT PRIMARY KEY NOT NULL, path TEXT NOT NULL, etag TEXT NOT NULL, mime TEXT NOT NULL, size INTEGER NOT NULL,
 modified INTEGER NOT NULL, outcome INTEGER NOT NULL CHECK(outcome BETWEEN 0 AND 3)
) STRICT`

const indexDocsDDL = `CREATE TABLE godav_idx_docs(
 docid INTEGER PRIMARY KEY NOT NULL, node_id TEXT NOT NULL UNIQUE, path TEXT NOT NULL, etag TEXT NOT NULL,
 mime TEXT NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL
) STRICT`

const indexFTSDDL = `CREATE VIRTUAL TABLE godav_idx_fts USING fts5(body, content='', contentless_delete=1, tokenize='unicode61 remove_diacritics 0')`

var indexSchema = []struct{ name, ddl string }{
	{"godav_idx_job", indexJobDDL}, {"godav_idx_work", indexWorkDDL}, {"godav_idx_docs", indexDocsDDL}, {"godav_idx_fts", indexFTSDDL},
}

type indexStore struct {
	store *AccountStore
	names []string
	scope string
}

var _ indexing.Store = (*indexStore)(nil)

// PrepareIndexing explicitly opts this existing writable store into a private
// shadow index. Normal startup and ordinary search never call it. The additive
// tables do not change the base schema, journal mode or retained connection.
func (s *AccountStore) PrepareIndexing(ctx context.Context, root string) (indexing.Store, error) {
	names, err := files.Components(strings.TrimPrefix(root, "/"))
	if err != nil || len(names) == 0 || !s.writable {
		return nil, indexing.ErrInvalid
	}
	idx := &indexStore{store: s, names: names, scope: files.Key(strings.Join(names, "/"))}
	if err = s.acquire(ctx); err != nil {
		return nil, indexErr(ctx, err)
	}
	defer s.release()
	if _, err = s.conn.ExecContext(ctx, "PRAGMA temp_store=MEMORY"); err != nil {
		return nil, indexErr(ctx, err)
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, indexErr(ctx, err)
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM main.sqlite_schema WHERE name GLOB 'godav_idx_*'`).Scan(&count); err != nil {
		return nil, indexErr(ctx, err)
	}
	if count == 0 {
		for _, table := range indexSchema {
			if _, err = tx.ExecContext(ctx, table.ddl); err != nil {
				return nil, indexErr(ctx, err)
			}
		}
	} else {
		// Do not adopt unrelated objects or unknown pilot schemas by name.
		if count != len(indexSchema)+4 {
			return nil, indexing.ErrUnavailable
		}
		for _, table := range indexSchema {
			var actual string
			if err = tx.QueryRowContext(ctx, `SELECT CASE WHEN octet_length(sql)<=65536 THEN sql END FROM main.sqlite_schema WHERE name=? AND type='table'`, table.name).Scan(&actual); err != nil || strings.Join(strings.Fields(actual), " ") != strings.Join(strings.Fields(table.ddl), " ") {
				return nil, indexing.ErrUnavailable
			}
		}
		var shadows, triggers int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_list WHERE schema='main' AND type='shadow' AND name IN ('godav_idx_fts_data','godav_idx_fts_idx','godav_idx_fts_docsize','godav_idx_fts_config')`).Scan(&shadows) != nil || shadows != 4 ||
			tx.QueryRowContext(ctx, `SELECT count(*) FROM main.sqlite_schema WHERE type='trigger' AND tbl_name GLOB 'godav_idx_*'`).Scan(&triggers) != nil || triggers != 0 {
			return nil, indexing.ErrUnavailable
		}
	}
	var saved string
	err = tx.QueryRowContext(ctx, `SELECT scope FROM godav_idx_job WHERE id=1`).Scan(&saved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, indexErr(ctx, err)
	}
	if err == nil && saved != idx.scope {
		return nil, indexing.ErrInvalid
	}
	if err = tx.Commit(); err != nil {
		return nil, indexErr(ctx, err)
	}
	s.indexPriority.Store(true)
	return idx, nil
}

type indexJob struct {
	id, owner, scope, rootID, code string
	state                          indexing.State
	fileLimit                      int64
	replans                        int
}

func readIndexJob(ctx context.Context, tx *sql.Tx) (indexJob, error) {
	var j indexJob
	err := tx.QueryRowContext(ctx, `SELECT job_id,owner,scope,root_id,state,error_code,file_limit,replans FROM godav_idx_job WHERE id=1`).
		Scan(&j.id, &j.owner, &j.scope, &j.rootID, &j.state, &j.code, &j.fileLimit, &j.replans)
	return j, err
}

func activeIndex(j indexJob) bool {
	return j.state == indexing.Queued || j.state == indexing.Running || j.state == indexing.Paused
}
func runnableIndex(j indexJob) bool { return j.state == indexing.Queued || j.state == indexing.Running }

func indexErr(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, files.ErrBusy) {
		return indexing.ErrBusy
	}
	return indexing.ErrUnavailable
}

func indexOwnerEnabled(ctx context.Context, tx *sql.Tx, owner string) (bool, error) {
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=? AND enabled=1)`, owner).Scan(&enabled)
	return enabled, err
}

func indexStatus(ctx context.Context, tx *sql.Tx, j indexJob) (indexing.Status, error) {
	result := indexing.Status{ID: j.id, State: j.state, ErrorCode: j.code}
	if j.state == indexing.Paused {
		result.PauseReason = string(indexing.Manual)
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE outcome<>3),count(*) FILTER(WHERE outcome IN (1,2)),count(*) FILTER(WHERE outcome=1),count(*) FILTER(WHERE outcome=2),count(*) FILTER(WHERE outcome=0) FROM godav_idx_work`).
		Scan(&result.Total, &result.Processed, &result.Indexed, &result.Skipped, &result.Backlog); err != nil {
		return indexing.Status{}, indexErr(ctx, err)
	}
	return result, nil
}

func (i *indexStore) Control(ctx context.Context, action indexing.Action, owner, id string) (indexing.Status, error) {
	var empty indexing.Status
	if err := i.store.acquire(ctx); err != nil {
		return empty, indexErr(ctx, err)
	}
	defer i.store.release()
	tx, err := i.store.conn.BeginTx(ctx, nil)
	if err != nil {
		return empty, indexErr(ctx, err)
	}
	defer tx.Rollback()
	enabled, err := indexOwnerEnabled(ctx, tx, owner)
	if err != nil {
		return empty, indexErr(ctx, err)
	}
	if !enabled {
		return empty, indexing.ErrNotFound
	}
	j, err := readIndexJob(ctx, tx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, indexErr(ctx, err)
	}
	if action == indexing.StartAction {
		if activeIndex(j) {
			if j.owner != owner || j.scope != i.scope {
				return empty, indexing.ErrBusy
			}
			return indexStatus(ctx, tx, j)
		}
		root, _, err := lookupNode(ctx, tx, i.names)
		if errors.Is(err, files.ErrMissing) || root.File {
			return empty, indexing.ErrNotFound
		}
		if err != nil {
			return empty, indexErr(ctx, err)
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return empty, indexing.ErrUnavailable
		}
		j = indexJob{id: hex.EncodeToString(nonce[:]), owner: owner, scope: i.scope, rootID: root.ID, state: indexing.Queued}
		if _, err = tx.ExecContext(ctx, `DELETE FROM godav_idx_work; DELETE FROM godav_idx_job`); err != nil {
			return empty, indexErr(ctx, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO godav_idx_job VALUES(1,?,?,?,?,?,'',0,0)`, j.id, owner, i.scope, root.ID, j.state); err != nil {
			return empty, indexErr(ctx, err)
		}
	} else {
		if j.id == "" || j.owner != owner || j.scope != i.scope || id != "" && j.id != id {
			return empty, indexing.ErrNotFound
		}
		next := j.state
		switch action {
		case indexing.StatusAction:
			return indexStatus(ctx, tx, j)
		case indexing.PauseAction:
			if !activeIndex(j) {
				return empty, indexing.ErrInvalidState
			}
			next = indexing.Paused
		case indexing.ResumeAction:
			if !activeIndex(j) {
				return empty, indexing.ErrInvalidState
			}
			if j.state == indexing.Paused {
				next = indexing.Queued
				if j.fileLimit > 0 {
					next = indexing.Running
				}
			}
		case indexing.CancelAction:
			if !activeIndex(j) && j.state != indexing.Cancelled {
				return empty, indexing.ErrInvalidState
			}
			next = indexing.Cancelled
		default:
			return empty, indexing.ErrInvalid
		}
		if next != j.state {
			if _, err = tx.ExecContext(ctx, `UPDATE godav_idx_job SET state=? WHERE id=1`, next); err != nil {
				return empty, indexErr(ctx, err)
			}
			j.state = next
		}
	}
	result, err := indexStatus(ctx, tx, j)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, indexErr(ctx, err)
	}
	return result, nil
}
