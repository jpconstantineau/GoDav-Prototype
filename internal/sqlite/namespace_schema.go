package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type namespaceColumn struct {
	name, kind       string
	notnull, primary int
}

func namespaceTable(ctx context.Context, conn *sql.Conn, table string, columns []namespaceColumn) error {
	var schema, name, kind string
	var count, withoutRowID, strict int
	// table is an internal constant, never a request or stored identifier.
	if conn.QueryRowContext(ctx, "PRAGMA main.table_list('"+table+"')").Scan(&schema, &name, &kind, &count, &withoutRowID, &strict) != nil || schema != "main" || name != table || kind != "table" || strict != 1 || count != len(columns) {
		return files.ErrUnavailable
	}
	rows, err := conn.QueryContext(ctx, `SELECT CASE WHEN octet_length(name)<=64 THEN name END,CASE WHEN octet_length(type)<=16 THEN type END,"notnull",pk,hidden FROM pragma_table_xinfo(?,'main') ORDER BY cid LIMIT 16`, table)
	if err != nil {
		return files.ErrUnavailable
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var c namespaceColumn
		var hidden int
		if rows.Scan(&c.name, &c.kind, &c.notnull, &c.primary, &hidden) != nil || i >= len(columns) || c != columns[i] || hidden != 0 {
			return files.ErrUnavailable
		}
		i++
	}
	if rows.Err() != nil || i != len(columns) {
		return files.ErrUnavailable
	}
	return nil
}

func namespaceShape(ctx context.Context, conn *sql.Conn) error {
	if namespaceTable(ctx, conn, "nodes", []namespaceColumn{{"id", "TEXT", 1, 1}, {"parent_id", "TEXT", 0, 0}, {"name", "TEXT", 1, 0}, {"name_key", "TEXT", 1, 0}, {"created_at", "INTEGER", 1, 0}, {"modified_at", "INTEGER", 1, 0}}) != nil || namespaceTable(ctx, conn, "namespace_policy", []namespaceColumn{{"id", "INTEGER", 1, 1}, {"algorithm", "TEXT", 1, 0}, {"unicode_version", "TEXT", 1, 0}}) != nil {
		return files.ErrUnavailable
	}
	rows, err := conn.QueryContext(ctx, `SELECT CASE WHEN octet_length(name)<=1024 THEN name END FROM pragma_index_list('nodes','main') WHERE "unique"=1 AND partial=0 LIMIT 129`)
	if err != nil {
		return files.ErrUnavailable
	}
	var indexes []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || len(indexes) >= 128 {
			rows.Close()
			return files.ErrUnavailable
		}
		indexes = append(indexes, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return files.ErrUnavailable
	}
	unique := false
	for _, index := range indexes {
		var count, matching int
		if conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(cid>=0 AND coll='BINARY' COLLATE NOCASE AND ((seqno=0 AND name='parent_id') OR (seqno=1 AND name='name_key'))),0) FROM (SELECT seqno,cid,name,coll FROM pragma_index_xinfo(?,'main') WHERE key=1 LIMIT 3)`, index).Scan(&count, &matching) != nil {
			return files.ErrUnavailable
		}
		unique = unique || count == 2 && matching == 2
	}
	if !unique {
		return files.ErrUnavailable
	}
	var count, matching int
	if conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum("table"='nodes' AND "from"='parent_id' AND "to"='id' AND on_update='NO ACTION' AND on_delete='NO ACTION'),0) FROM pragma_foreign_key_list('nodes','main')`).Scan(&count, &matching) != nil || count != 1 || matching != 1 {
		return files.ErrUnavailable
	}
	if !namespaceCheck(ctx, conn, "namespace_policy", `id=1`) || !namespaceCheck(ctx, conn, "nodes", namespaceRootCheck) {
		return files.ErrUnavailable
	}
	return nil
}

// Recognize the chosen complete CHECK predicate, allowing whitespace/comments
// and quoted identifiers but never a matching fragment inside a weaker OR.
// This is deliberately not an evaluator for arbitrary equivalent stored SQL.
func namespaceCheck(ctx context.Context, conn *sql.Conn, table, predicate string) bool {
	var create string
	if conn.QueryRowContext(ctx, `SELECT CASE WHEN octet_length(sql)<=65536 THEN sql END FROM main.sqlite_schema WHERE type='table' AND name=? COLLATE NOCASE`, table).Scan(&create) != nil {
		return false
	}
	tokens, ok := schemaTokens(create)
	if !ok {
		return false
	}
	want, _ := schemaTokens(predicate)
	depth := 0
	for i, token := range tokens {
		if depth == 1 && tokenIs(token, "check") && i+1 < len(tokens) && tokenIs(tokens[i+1], "(") {
			end := closingParen(tokens, i+1)
			if end < 0 {
				return false
			}
			actual := stripSchemaParens(tokens[i+2 : end])
			matched := len(actual) == len(want)
			if matched {
				for j := range want {
					if actual[j].literal != want[j].literal || actual[j].text != want[j].text {
						matched = false
						break
					}
				}
			}
			if matched {
				return true
			}
		}
		if tokenIs(token, "(") {
			depth++
		}
		if tokenIs(token, ")") {
			depth--
			if depth == 0 {
				return false
			}
		}
	}
	return false
}

func namespaceRows(ctx context.Context, conn *sql.Conn) error {
	var count, rootCount, policyCount int
	if conn.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(id='root' AND parent_id IS NULL AND name='' AND name_key=''),0) FROM nodes`).Scan(&count, &rootCount) != nil || rootCount != 1 {
		return files.ErrUnavailable
	}
	if conn.QueryRowContext(ctx, `SELECT count(*) FROM namespace_policy`).Scan(&policyCount) != nil || policyCount != 1 {
		return files.ErrUnavailable
	}
	var bad int
	if conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nodes GROUP BY parent_id HAVING count(*)>?)`, files.MaxChildren).Scan(&bad) != nil || bad != 0 {
		return files.ErrUnavailable
	}
	// A reachable tree has one parent per non-root node. A bounded recursive
	// walk counts it without materializing the entire namespace in Go. Orphans,
	// detached cycles and excess depth/path length all fail closed.
	var reachable int
	if conn.QueryRowContext(ctx, `WITH RECURSIVE tree(id,depth,bytes) AS (
 SELECT id,0,0 FROM nodes WHERE id='root'
 UNION ALL SELECT n.id,t.depth+1,t.bytes+octet_length(n.name)+(t.depth>0) FROM nodes n JOIN tree t ON n.parent_id=t.id WHERE t.depth<? AND t.bytes+octet_length(n.name)+(t.depth>0)<=?
 ) SELECT count(*) FROM tree`, files.MaxDepth, files.MaxPathBytes).Scan(&reachable) != nil || reachable != count {
		return files.ErrUnavailable
	}
	rows, err := conn.QueryContext(ctx, `SELECT CASE WHEN octet_length(id)<=32 THEN id END,CASE WHEN octet_length(name)<=255 THEN name END,CASE WHEN octet_length(name_key)<=255 THEN name_key END,created_at,modified_at FROM nodes`)
	if err != nil {
		return files.ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, key string
		var created, modified int64
		if rows.Scan(&id, &name, &key, &created, &modified) != nil || created < 0 || created > 253402300799 || modified < 0 || modified > 253402300799 {
			return files.ErrUnavailable
		}
		if id == rootID {
			if name != "" || key != "" {
				return files.ErrUnavailable
			}
			continue
		}
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 16 || id != strings.ToLower(id) || !files.ValidName(name) || files.Key(name) != key {
			return files.ErrUnavailable
		}
	}
	if rows.Err() != nil {
		return files.ErrUnavailable
	}
	return nil
}
