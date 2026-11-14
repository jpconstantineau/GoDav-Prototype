package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// validateSchema checks structural v1 invariants without executing stored SQL.
// Limits bound metadata materialized in Go, including the CREATE statement.
func validateSchema(ctx context.Context, db *sql.DB) error {
	invalid := errors.New("invalid account schema")
	// Use the actual PRAGMA: a bare pragma_table_list name can resolve to an
	// ordinary stored table and let untrusted state spoof its own metadata.
	var schema, name, kind string
	var columns, withoutRowID, strict int
	if err := db.QueryRowContext(ctx, `PRAGMA main.table_list('accounts')`).Scan(&schema, &name, &kind, &columns, &withoutRowID, &strict); err != nil ||
		schema != "main" || !strings.EqualFold(name, "accounts") || kind != "table" || strict != 1 {
		return invalid
	}
	// The remaining PRAGMA table functions require arguments. Ordinary tables
	// or views with those names fail as non-functions rather than supply rows.
	rows, err := db.QueryContext(ctx, `SELECT CASE WHEN octet_length(name)<=128 THEN name END,
CASE WHEN octet_length(type)<=16 THEN type END, "notnull", pk, hidden
FROM pragma_table_xinfo('accounts','main') LIMIT 65`)
	if err != nil {
		return invalid
	}
	want := map[string]string{"id": "TEXT", "username": "TEXT", "username_key": "TEXT", "verifier": "TEXT", "enabled": "INTEGER", "created_at": "TEXT"}
	seen := map[string]bool{}
	count := 0
	for rows.Next() {
		var name, typ string
		var notnull, pk, hidden int
		if err := rows.Scan(&name, &typ, &notnull, &pk, &hidden); err != nil {
			rows.Close()
			return invalid
		}
		count++
		name = strings.ToLower(name)
		expected, required := want[name]
		if count > 64 || (pk != 0 && (name != "id" || pk != 1)) ||
			(required && (seen[name] || !strings.EqualFold(typ, expected) || notnull != 1 || hidden != 0 || (name == "id" && pk != 1))) {
			rows.Close()
			return invalid
		}
		seen[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return invalid
	}
	for name := range want {
		if !seen[name] {
			return invalid
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT CASE WHEN octet_length(name)<=1024 THEN name END,
"unique", partial FROM pragma_index_list('accounts','main') LIMIT 129`)
	if err != nil {
		return invalid
	}
	var candidates []string
	count = 0
	for rows.Next() {
		var name string
		var unique, partial int
		if err := rows.Scan(&name, &unique, &partial); err != nil {
			rows.Close()
			return invalid
		}
		count++
		if count > 128 {
			rows.Close()
			return invalid
		}
		if unique == 1 && partial == 0 {
			candidates = append(candidates, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return invalid
	}
	found := false
	for _, name := range candidates {
		// Aggregate only a bounded prefix. More than one key column cannot
		// provide the required single-column uniqueness guarantee.
		var keys, matches int
		err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(cid>=0 AND
name='username_key' COLLATE NOCASE AND coll='BINARY' COLLATE NOCASE),0)
FROM (SELECT cid,name,coll FROM pragma_index_xinfo(?,'main') WHERE key=1 LIMIT 2)`, name).Scan(&keys, &matches)
		if err != nil {
			return invalid
		}
		if keys == 1 && matches == 1 {
			found = true
			break
		}
	}
	if !found {
		return invalid
	}
	var triggers int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM main.sqlite_schema
WHERE type='trigger' AND tbl_name='accounts' COLLATE NOCASE)`).Scan(&triggers); err != nil || triggers != 0 {
		return invalid
	}
	var create string
	if err := db.QueryRowContext(ctx, `SELECT CASE WHEN octet_length(sql)<=65536 THEN sql END
FROM main.sqlite_schema WHERE type='table' AND name='accounts' COLLATE NOCASE`).Scan(&create); err != nil || !hasEnabledCheck(create) {
		return invalid
	}
	return nil
}

type schemaToken struct {
	text       string
	identifier bool
}

// schemaTokens preserves the distinction between quoted identifiers, SQL
// keywords, and string literals. Comments and whitespace are insignificant.
func schemaTokens(s string) ([]schemaToken, bool) {
	var out []schemaToken
	for i := 0; i < len(s); {
		if len(out) >= 4096 {
			return nil, false
		}
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' {
			i++
			continue
		}
		if i+1 < len(s) && s[i:i+2] == "--" {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(s) && s[i:i+2] == "/*" {
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			end := c
			if c == '[' {
				end = ']'
			}
			i++
			var value strings.Builder
			closed := false
			for i < len(s) {
				if s[i] == end {
					i++
					if c != '[' && i < len(s) && s[i] == end {
						value.WriteByte(end)
						i++
						continue
					}
					closed = true
					break
				}
				value.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, false
			}
			if c == '\'' {
				out = append(out, schemaToken{text: "<string>"})
			} else {
				out = append(out, schemaToken{text: strings.ToLower(value.String()), identifier: true})
			}
			continue
		}
		start := i
		for i < len(s) && ((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= '0' && s[i] <= '9') || s[i] == '_' || s[i] == '$' || s[i] >= 128) {
			i++
		}
		if i == start {
			i++
		}
		out = append(out, schemaToken{text: strings.ToLower(s[start:i])})
	}
	return out, true
}

func tokenIs(t schemaToken, text string) bool { return !t.identifier && t.text == text }

func closingParen(t []schemaToken, start int) int {
	depth := 0
	for i := start; i < len(t); i++ {
		if tokenIs(t[i], "(") {
			depth++
		}
		if tokenIs(t[i], ")") {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func stripSchemaParens(t []schemaToken) []schemaToken {
	for len(t) >= 2 && tokenIs(t[0], "(") && closingParen(t, 0) == len(t)-1 {
		t = t[1 : len(t)-1]
	}
	return t
}

func enabledInCheck(t []schemaToken) bool {
	t = stripSchemaParens(t)
	// The complete CHECK expression must be this predicate. In particular,
	// finding it inside a larger OR expression would not prove enforcement.
	in := -1
	depth := 0
	for i, tok := range t {
		if tokenIs(tok, "(") {
			depth++
		}
		if tokenIs(tok, ")") {
			depth--
		}
		if depth == 0 && tokenIs(tok, "in") {
			if in != -1 {
				return false
			}
			in = i
		}
	}
	if in < 1 {
		return false
	}
	lhs := stripSchemaParens(t[:in])
	if len(lhs) != 1 || lhs[0].text != "enabled" {
		return false
	}
	rhs := t[in+1:]
	if len(rhs) < 2 || !tokenIs(rhs[0], "(") || closingParen(rhs, 0) != len(rhs)-1 {
		return false
	}
	rhs = rhs[1 : len(rhs)-1]
	comma := -1
	depth = 0
	for i, tok := range rhs {
		if tokenIs(tok, "(") {
			depth++
		}
		if tokenIs(tok, ")") {
			depth--
		}
		if depth == 0 && tokenIs(tok, ",") {
			if comma != -1 {
				return false
			}
			comma = i
		}
	}
	if comma < 0 {
		return false
	}
	a, b := stripSchemaParens(rhs[:comma]), stripSchemaParens(rhs[comma+1:])
	return len(a) == 1 && len(b) == 1 && ((tokenIs(a[0], "0") && tokenIs(b[0], "1")) || (tokenIs(a[0], "1") && tokenIs(b[0], "0")))
}

func hasEnabledCheck(s string) bool {
	t, ok := schemaTokens(s)
	if !ok {
		return false
	}
	depth := 0
	for i, tok := range t {
		// Only constraints directly in the table body count; never a token
		// embedded in another expression, quoted name, or literal.
		if depth == 1 && tokenIs(tok, "check") && i+1 < len(t) && tokenIs(t[i+1], "(") {
			end := closingParen(t, i+1)
			if end < 0 {
				return false
			}
			if enabledInCheck(t[i+2 : end]) {
				return true
			}
		}
		if tokenIs(tok, "(") {
			depth++
		}
		if tokenIs(tok, ")") {
			depth--
			if depth == 0 {
				// Do not recognize constraints from any trailing statement.
				return false
			}
		}
	}
	return false
}
