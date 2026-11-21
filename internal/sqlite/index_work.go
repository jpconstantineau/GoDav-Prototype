package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
)

// The plan stores only metadata. Both discovery and the final equality check
// are bounded by stable root identity, known path depth, count, bytes and the
// worker's short context. The current namespace is never scanned in Go.
const indexScopeQuery = `WITH RECURSIVE scoped(id,path,depth) AS (
 SELECT ?, '', 0
 UNION ALL
 SELECT n.id,CASE WHEN t.path='' THEN n.name ELSE t.path||'/'||n.name END,t.depth+1
 FROM nodes n JOIN scoped t ON n.parent_id=t.id WHERE t.depth<? LIMIT ?
), current(node_id,path,etag,mime,size,modified) AS (
 SELECT t.id,t.path,f.etag,f.content_type,f.size,n.modified_at FROM scoped t
 JOIN file_contents f ON f.node_id=t.id JOIN nodes n ON n.id=t.id
)`

func indexScopeArgs(j indexJob) []any { return []any{j.rootID, files.MaxDepth, maxIndexNodes + 1} }

// The optional grant is resolved by server startup and must be a pure,
// bounded check; the database account is independently checked in the same
// transaction. Neither succeeds merely because an account owned the old job.
func indexOwnerAllowed(ctx context.Context, tx *sql.Tx, j indexJob, b indexing.Budget) (bool, error) {
	if b.OwnerAllowed != nil && !b.OwnerAllowed(j.owner) {
		return false, nil
	}
	return indexOwnerEnabled(ctx, tx, j.owner)
}

func (i *indexStore) scopePresent(ctx context.Context, tx *sql.Tx, j indexJob) (bool, error) {
	if j.scope != i.scope {
		return false, nil
	}
	root, _, err := lookupNode(ctx, tx, i.names)
	if errors.Is(err, files.ErrMissing) {
		return false, nil
	}
	return root.ID == j.rootID && !root.File, err
}

func indexBudgetExceeded(ctx context.Context, tx *sql.Tx, j indexJob, limit int64) (bool, error) {
	var count int
	var bytes int64
	err := tx.QueryRowContext(ctx, indexScopeQuery+`SELECT (SELECT count(*) FROM scoped),coalesce(sum(CASE WHEN size<=? THEN size ELSE 0 END),0) FROM current`, append(indexScopeArgs(j), limit)...).Scan(&count, &bytes)
	return count > maxIndexNodes || bytes > maxIndexBytes, err
}

func indexTerminal(ctx context.Context, tx *sql.Tx, state indexing.State, code string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE godav_idx_job SET state=?,error_code=? WHERE id=1`, state, code); err != nil {
		return indexErr(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return indexErr(ctx, err)
	}
	return nil
}

func indexPlan(ctx context.Context, tx *sql.Tx, j indexJob, limit int64) error {
	exceeded, err := indexBudgetExceeded(ctx, tx, j, limit)
	if err != nil {
		return indexErr(ctx, err)
	}
	if exceeded {
		return indexTerminal(ctx, tx, indexing.Failed, "capacity_exceeded")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM godav_idx_work`); err != nil {
		return indexErr(ctx, err)
	}
	if _, err = tx.ExecContext(ctx, indexScopeQuery+`
 INSERT INTO godav_idx_work (node_id,path,etag,mime,size,modified,outcome)
 SELECT node_id,path,etag,mime,size,modified,0 FROM current`, indexScopeArgs(j)...); err != nil {
		return indexErr(ctx, err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE godav_idx_job SET state='running',file_limit=? WHERE id=1`, limit); err != nil {
		return indexErr(ctx, err)
	}
	if err = tx.Commit(); err != nil {
		return indexErr(ctx, err)
	}
	return nil
}

// Resolve current metadata by stable file ID, walking actual parent IDs rather
// than relying on an old filename, a prefix test or revision alone. A case-only
// ancestor rename changes Path while the child's ETag need not change.
func indexFileMeta(ctx context.Context, tx *sql.Tx, j indexJob, id string) (indexing.FileMeta, bool, error) {
	m := indexing.FileMeta{ID: id}
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE ancestry(id,parent_id,path,depth) AS (
 SELECT id,parent_id,name,0 FROM nodes WHERE id=? AND id<>?
 UNION ALL
 SELECT n.id,n.parent_id,CASE WHEN n.id=? THEN a.path ELSE n.name||'/'||a.path END,a.depth+1
 FROM nodes n JOIN ancestry a ON n.id=a.parent_id WHERE a.id<>? AND a.depth<?
 ) SELECT a.path,f.etag,f.content_type,f.size,n.modified_at FROM ancestry a
 JOIN file_contents f ON f.node_id=? JOIN nodes n ON n.id=f.node_id WHERE a.id=?`,
		id, j.rootID, j.rootID, j.rootID, files.MaxDepth, id, j.rootID).Scan(&m.Path, &m.ETag, &m.ContentType, &m.Size, &m.Modified)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	return m, err == nil, err
}

func (i *indexStore) Step(ctx context.Context, b indexing.Budget) (*indexing.Work, indexing.Pressure, error) {
	release, reason, err := i.store.tryIndex(ctx, b, "snapshot")
	if release == nil {
		return nil, reason, err
	}
	defer release()
	tx, err := i.store.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	defer tx.Rollback()
	j, err := readIndexJob(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !activeIndex(j) {
		return nil, indexing.Idle, nil
	}
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	ownerAllowed, err := indexOwnerAllowed(ctx, tx, j, b)
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	if !ownerAllowed {
		return nil, "", indexTerminal(ctx, tx, indexing.Failed, "owner_unavailable")
	}
	if j.state == indexing.Paused {
		return nil, indexing.Idle, nil
	}
	ok, err := i.scopePresent(ctx, tx, j)
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	if !ok {
		return nil, "", indexTerminal(ctx, tx, indexing.Failed, "scope_unavailable")
	}
	if j.fileLimit != b.MaxFileBytes {
		return nil, "", indexPlan(ctx, tx, j, b.MaxFileBytes)
	}

	var id string
	err = tx.QueryRowContext(ctx, `SELECT node_id FROM godav_idx_work WHERE outcome=0 ORDER BY node_id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", indexFinishStep(ctx, tx, j)
	}
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	meta, exists, err := indexFileMeta(ctx, tx, j, id)
	if err != nil {
		return nil, "", indexErr(ctx, err)
	}
	w := &indexing.Work{JobID: j.id, Meta: meta, Missing: !exists, Oversized: meta.Size > b.MaxFileBytes}
	if exists && !w.Oversized {
		if err = tx.QueryRowContext(ctx, `SELECT content FROM file_contents WHERE node_id=? AND etag=? AND size BETWEEN 0 AND ?`, id, meta.ETag, b.MaxFileBytes).Scan(&w.Body); err != nil {
			clear(w.Body)
			return nil, "", indexErr(ctx, err)
		}
	}
	return w, "", nil
}

func indexFinishStep(ctx context.Context, tx *sql.Tx, j indexJob) error {
	// Reap one stale shadow document per transaction. Only an affirmative
	// outcome in this durable plan may retain an indexed row.
	var docID int64
	err := tx.QueryRowContext(ctx, `SELECT d.docid FROM godav_idx_docs d WHERE NOT EXISTS (
 SELECT 1 FROM godav_idx_work w WHERE w.node_id=d.node_id AND w.outcome=1) ORDER BY d.docid LIMIT 1`).Scan(&docID)
	if err == nil {
		if err = deleteIndexDocument(ctx, tx, docID); err != nil {
			return indexErr(ctx, err)
		}
		if err = tx.Commit(); err != nil {
			return indexErr(ctx, err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return indexErr(ctx, err)
	}
	// A later DAV change can affect an already-published file. Before success,
	// compare the full capped metadata manifest in the same commit transaction.
	exceeded, err := indexBudgetExceeded(ctx, tx, j, j.fileLimit)
	if err != nil {
		return indexErr(ctx, err)
	}
	if exceeded {
		return indexTerminal(ctx, tx, indexing.Failed, "capacity_exceeded")
	}
	var changed bool
	if err = tx.QueryRowContext(ctx, indexScopeQuery+`
 SELECT EXISTS(SELECT node_id,path,etag,mime,size,modified FROM current EXCEPT
 SELECT node_id,path,etag,mime,size,modified FROM godav_idx_work WHERE outcome<>3)
 OR EXISTS(SELECT node_id,path,etag,mime,size,modified FROM godav_idx_work WHERE outcome<>3 EXCEPT
 SELECT node_id,path,etag,mime,size,modified FROM current)`, indexScopeArgs(j)...).Scan(&changed); err != nil {
		return indexErr(ctx, err)
	}
	if !changed {
		return indexTerminal(ctx, tx, indexing.Completed, "")
	}
	if j.replans >= maxIndexReplans {
		return indexTerminal(ctx, tx, indexing.Failed, "scope_changing")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE godav_idx_job SET file_limit=0,replans=replans+1 WHERE id=1`); err != nil {
		return indexErr(ctx, err)
	}
	if err = tx.Commit(); err != nil {
		return indexErr(ctx, err)
	}
	return nil
}

func deleteIndexDocument(ctx context.Context, tx *sql.Tx, docID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM godav_idx_fts WHERE rowid=?`, docID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM godav_idx_docs WHERE docid=?`, docID)
	return err
}

func (i *indexStore) Publish(ctx context.Context, b indexing.Budget, w indexing.Work, eligible bool) (indexing.Pressure, error) {
	release, reason, err := i.store.tryIndex(ctx, b, "publish")
	if release == nil {
		return reason, err
	}
	defer release()
	tx, err := i.store.conn.BeginTx(ctx, nil)
	if err != nil {
		return "", indexErr(ctx, err)
	}
	defer tx.Rollback()
	j, err := readIndexJob(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!runnableIndex(j) || w.JobID != j.id) {
		return "", nil
	}
	if err != nil {
		return "", indexErr(ctx, err)
	}
	ownerAllowed, err := indexOwnerAllowed(ctx, tx, j, b)
	if err != nil {
		return "", indexErr(ctx, err)
	}
	if !ownerAllowed {
		return "", indexTerminal(ctx, tx, indexing.Failed, "owner_unavailable")
	}
	ok, err := i.scopePresent(ctx, tx, j)
	if err != nil {
		return "", indexErr(ctx, err)
	}
	if !ok {
		return "", indexTerminal(ctx, tx, indexing.Failed, "scope_unavailable")
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM godav_idx_work WHERE node_id=? AND outcome=0)`, w.Meta.ID).Scan(&pending); err != nil {
		return "", indexErr(ctx, err)
	}
	if pending == 0 {
		return "", nil
	}
	meta, exists, err := indexFileMeta(ctx, tx, j, w.Meta.ID)
	if err != nil {
		return "", indexErr(ctx, err)
	}
	if exists == w.Missing || (exists && meta != w.Meta) {
		// Leaves the same work pending. The next snapshot sees current
		// content/metadata, or a tombstone for removal or move outside scope.
		return "", nil
	}

	var docID int64
	err = tx.QueryRowContext(ctx, `SELECT docid FROM godav_idx_docs WHERE node_id=?`, meta.ID).Scan(&docID)
	if err == nil {
		if err = deleteIndexDocument(ctx, tx, docID); err != nil {
			return "", indexErr(ctx, err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", indexErr(ctx, err)
	}
	outcome := 2
	if !exists {
		outcome = 3
	}
	if exists && eligible && !w.Oversized && int64(len(w.Body)) == meta.Size && meta.Size <= b.MaxFileBytes {
		// Old rows can coexist briefly with a new plan. Bound this transitional
		// footprint even when concurrent revisions grow after planning; the
		// final committed manifest retains the stricter corpus budget.
		var total int64
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(size),0),count(*) FROM godav_idx_docs`).Scan(&total, &count); err != nil {
			return "", indexErr(ctx, err)
		}
		if total > 2*maxIndexBytes-meta.Size || count >= 2*maxIndexNodes {
			return "", indexTerminal(ctx, tx, indexing.Failed, "capacity_exceeded")
		}
		row, err := tx.ExecContext(ctx, `INSERT INTO godav_idx_docs(node_id,path,etag,mime,size,modified) VALUES(?,?,?,?,?,?)`,
			meta.ID, meta.Path, meta.ETag, meta.ContentType, meta.Size, meta.Modified)
		if err != nil {
			return "", indexErr(ctx, err)
		}
		docID, err = row.LastInsertId()
		if err != nil {
			return "", indexErr(ctx, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO godav_idx_fts(rowid,body) VALUES(?,?)`, docID, string(w.Body)); err != nil {
			return "", indexErr(ctx, err)
		}
		outcome = 1
	}
	if _, err = tx.ExecContext(ctx, `UPDATE godav_idx_work SET path=?,etag=?,mime=?,size=?,modified=?,outcome=? WHERE node_id=?`,
		meta.Path, meta.ETag, meta.ContentType, meta.Size, meta.Modified, outcome, meta.ID); err != nil {
		return "", indexErr(ctx, err)
	}
	if err = tx.Commit(); err != nil {
		return "", indexErr(ctx, err)
	}
	return "", nil
}
