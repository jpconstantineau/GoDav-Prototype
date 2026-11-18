package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"mime"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

const MaxContentSearchNodes = 2048
const MaxContentSearchBytes int64 = 128 << 20
const ContentSearchTimeout = 5 * time.Second

// SearchContent rebuilds an ephemeral, memory-only FTS5 index from a committed
// scoped snapshot. The complete operation owns the normal store gate. It never
// migrates main, changes publication, or retains a cache that can become stale.
// Rebuilding every call is deliberately conservative: work is bounded, but this
// initial implementation trades repeated indexing cost for simple freshness.
func (s *AccountStore) SearchContent(ctx context.Context, directory []string, opts files.ContentSearchOptions) (files.ContentSearchResult, error) {
	empty := files.ContentSearchResult{Entries: []files.ContentSearchHit{}}
	if strings.TrimSpace(opts.Query) == "" || len(opts.Query) > files.MaxContentSearchQueryBytes || !utf8.ValidString(opts.Query) || strings.ContainsRune(opts.Query, 0) || opts.Limit < 1 || opts.Limit > files.MaxContentSearchResults {
		return empty, files.ErrInvalid
	}
	if (opts.MinSize != nil && *opts.MinSize < 0) || (opts.MaxSize != nil && *opts.MaxSize < 0) || (opts.MinSize != nil && opts.MaxSize != nil && *opts.MinSize > *opts.MaxSize) || (!opts.ModifiedAfter.IsZero() && !opts.ModifiedBefore.IsZero() && !opts.ModifiedAfter.Before(opts.ModifiedBefore)) {
		return empty, files.ErrInvalid
	}
	for _, name := range directory {
		if !files.ValidName(name) {
			return empty, files.ErrInvalid
		}
	}
	if len(directory) > files.MaxDepth {
		return empty, files.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, ContentSearchTimeout)
	defer cancel()
	if e := s.acquire(ctx); e != nil {
		return empty, e
	}
	defer s.release()
	failed := func() (files.ContentSearchResult, error) {
		if ctx.Err() != nil {
			return empty, files.ErrTimeout
		}
		return empty, files.ErrUnavailable
	}
	// Content must not spill into an OS temporary file outside private state ACLs.
	if _, e := s.conn.ExecContext(ctx, "PRAGMA temp_store=MEMORY"); e != nil {
		return failed()
	}
	tx, e := s.conn.BeginTx(ctx, nil)
	if e != nil {
		return failed()
	}
	defer tx.Rollback()
	root, canonical, e := lookupNode(ctx, tx, directory)
	if e != nil {
		return empty, e
	}
	if root.File {
		return empty, files.ErrInvalid
	}
	rows, e := tx.QueryContext(ctx, `WITH RECURSIVE scoped(id,path,depth) AS (
 SELECT ?, ?, 0
 UNION ALL
 SELECT n.id,CASE WHEN t.path='' THEN n.name ELSE t.path||'/'||n.name END,t.depth+1
 FROM main.nodes n JOIN scoped t ON n.parent_id=t.id
 WHERE t.depth<32 LIMIT ?
 ) SELECT n.id,t.path,n.name,n.modified_at,f.size,f.content_type,f.etag
 FROM scoped t JOIN main.nodes n ON n.id=t.id
 LEFT JOIN main.file_contents f ON f.node_id=n.id`, root.ID, canonical, MaxContentSearchNodes+1)
	if e != nil {
		return failed()
	}
	type candidate struct {
		id  string
		hit files.ContentSearchHit
	}
	candidates := []candidate{}
	count := 0
	var total int64
	for rows.Next() {
		var id, relative, name string
		var modified int64
		var size sql.NullInt64
		var kind, etag sql.NullString
		if rows.Scan(&id, &relative, &name, &modified, &size, &kind, &etag) != nil {
			rows.Close()
			return failed()
		}
		count++
		if count > MaxContentSearchNodes {
			rows.Close()
			return empty, files.ErrCapacity
		}
		if !size.Valid {
			continue
		}
		if size.Int64 < 0 || size.Int64 > files.AbsoluteMaxFileBytes || !kind.Valid || !etag.Valid {
			rows.Close()
			return empty, files.ErrUnavailable
		}
		if total > MaxContentSearchBytes-size.Int64 {
			rows.Close()
			return empty, files.ErrCapacity
		}
		total += size.Int64
		hit := files.ContentSearchHit{Path: relative, Name: name, ContentType: kind.String, ETag: etag.String, Size: size.Int64, Modified: time.Unix(modified, 0).UTC()}
		candidates = append(candidates, candidate{id, hit})
	}
	if rows.Err() != nil {
		rows.Close()
		return failed()
	}
	if rows.Close() != nil {
		return failed()
	}
	// Only content is tokenized. Paths and metadata cannot create false body hits.
	if _, e = tx.ExecContext(ctx, `CREATE VIRTUAL TABLE temp.godav_content_fts USING fts5(content, path UNINDEXED, tokenize='unicode61 remove_diacritics 0')`); e != nil {
		return failed()
	}
	for i, c := range candidates {
		if ctx.Err() != nil {
			return failed()
		}
		var data []byte
		if e = tx.QueryRowContext(ctx, `SELECT content FROM main.file_contents WHERE node_id=? AND etag=?`, c.id, c.hit.ETag).Scan(&data); e != nil {
			return failed()
		}
		if int64(len(data)) != c.hit.Size {
			clear(data)
			return empty, files.ErrUnavailable
		}
		eligible := utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
		if eligible {
			_, e = tx.ExecContext(ctx, `INSERT INTO temp.godav_content_fts(rowid,content,path) VALUES(?,?,?)`, i+1, string(data), c.hit.Path)
		}
		clear(data)
		if e != nil {
			return failed()
		}
	}
	// Quote the whole query as a literal FTS phrase. It is never an FTS operator
	// expression, SQL fragment, filename match, prefix match or substring search.
	phrase := `"` + strings.ReplaceAll(strings.TrimSpace(opts.Query), `"`, `""`) + `"`
	rows, e = tx.QueryContext(ctx, `SELECT rowid FROM temp.godav_content_fts WHERE godav_content_fts MATCH ? ORDER BY bm25(godav_content_fts),path COLLATE BINARY`, phrase)
	if e != nil {
		return failed()
	}
	result := files.ContentSearchResult{Entries: []files.ContentSearchHit{}}
	for rows.Next() {
		var id int
		if rows.Scan(&id) != nil || id < 1 || id > len(candidates) {
			rows.Close()
			return failed()
		}
		hit := candidates[id-1].hit
		if !contentSearchFilter(hit, opts) {
			continue
		}
		if len(result.Entries) == opts.Limit {
			result.Partial = true
			break
		}
		result.Entries = append(result.Entries, hit)
	}
	if rows.Err() != nil {
		rows.Close()
		return failed()
	}
	if rows.Close() != nil {
		return failed()
	}
	// Rollback destroys the TEMP index and leaves persisted schema/bytes intact.
	if e = tx.Rollback(); e != nil {
		return failed()
	}
	return result, nil
}

func contentSearchFilter(hit files.ContentSearchHit, o files.ContentSearchOptions) bool {
	if o.Extension != "" && asciiLower(path.Ext(hit.Name)) != asciiLower(o.Extension) {
		return false
	}
	if o.ContentType != "" {
		base, _, e := mime.ParseMediaType(hit.ContentType)
		if e != nil || !strings.EqualFold(base, o.ContentType) {
			return false
		}
	}
	if !o.ModifiedAfter.IsZero() && hit.Modified.Before(o.ModifiedAfter) {
		return false
	}
	if !o.ModifiedBefore.IsZero() && !hit.Modified.Before(o.ModifiedBefore) {
		return false
	}
	if o.MinSize != nil && hit.Size < *o.MinSize {
		return false
	}
	if o.MaxSize != nil && hit.Size > *o.MaxSize {
		return false
	}
	return true
}

func asciiLower(value string) string {
	b := []byte(value)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
