package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func validateFiles(ctx context.Context, conn *sql.Conn) error {
	if namespaceTable(ctx, conn, "file_contents", []namespaceColumn{{"node_id", "TEXT", 1, 1}, {"content", "BLOB", 1, 0}, {"size", "INTEGER", 1, 0}, {"content_type", "TEXT", 1, 0}, {"etag", "TEXT", 1, 0}}) != nil {
		return files.ErrUnavailable
	}
	for _, predicate := range []string{`node_id<>'root'`, fileSizeCheck, `octet_length(content_type) BETWEEN 1 AND 255`, `octet_length(etag)=34`} {
		if !namespaceCheck(ctx, conn, "file_contents", predicate) {
			return files.ErrUnavailable
		}
	}
	var count, matching int
	if conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum("table"='nodes' AND "from"='node_id' AND "to"='id' AND on_update='NO ACTION' AND on_delete='NO ACTION'),0) FROM pragma_foreign_key_list('file_contents','main')`).Scan(&count, &matching) != nil || count != 1 || matching != 1 {
		return files.ErrUnavailable
	}
	var bad int
	if conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.sqlite_schema WHERE type='trigger' AND tbl_name='file_contents' COLLATE NOCASE) OR EXISTS(SELECT 1 FROM file_contents f LEFT JOIN nodes n ON n.id=f.node_id WHERE n.id IS NULL OR f.node_id='root') OR EXISTS(SELECT 1 FROM nodes n JOIN file_contents f ON n.parent_id=f.node_id)`).Scan(&bad) != nil || bad != 0 {
		return files.ErrUnavailable
	}
	rows, err := conn.QueryContext(ctx, `SELECT CASE WHEN typeof(content)='blob' AND length(content)=size AND size BETWEEN 0 AND 67108864 THEN size END,CASE WHEN octet_length(content_type) BETWEEN 1 AND 255 THEN content_type END,CASE WHEN octet_length(etag)=34 THEN etag END FROM file_contents`)
	if err != nil {
		return files.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var size int64
		var kind, etag string
		if rows.Scan(&size, &kind, &etag) != nil {
			return files.ErrUnavailable
		}
		if !files.ValidContentType(kind) || etag[0] != '"' || etag[33] != '"' {
			return files.ErrUnavailable
		}
		if _, err := hex.DecodeString(etag[1:33]); err != nil {
			return files.ErrUnavailable
		}
	}
	if rows.Err() != nil {
		return files.ErrUnavailable
	}
	return nil
}
