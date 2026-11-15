package sqlite

import (
	"context"
	"database/sql"
	"sort"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

type mutationNode struct {
	ID, RelativePath string
	Depth            int
	File             bool
	Size             int64
}

// readMutationTree runs only inside the caller's transaction and operation gate.
// Depth-first expansion bounds the pending frontier by depth * MaxChildren;
// the recursive LIMIT bounds yielded nodes before Go materialization. It loads
// metadata only, never payloads. The root has empty RelativePath and depth zero.
func readMutationTree(ctx context.Context, tx *sql.Tx, root files.Node) ([]mutationNode, error) {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE tree(id,path,depth) AS (
 SELECT id,'',0 FROM nodes WHERE id=?
 UNION ALL SELECT n.id,CASE WHEN t.path='' THEN n.name ELSE t.path||'/'||n.name END,t.depth+1
 FROM nodes n JOIN tree t ON n.parent_id=t.id WHERE t.depth<?
 ORDER BY 3 DESC LIMIT ?
 ) SELECT t.id,t.path,t.depth,f.node_id IS NOT NULL,coalesce(f.size,0)
 FROM tree t LEFT JOIN file_contents f ON f.node_id=t.id`, root.ID, files.MaxDepth, files.MaxTreeResources+1)
	if err != nil {
		return nil, files.ErrUnavailable
	}
	defer rows.Close()
	var tree []mutationNode
	var total int64
	for rows.Next() {
		var n mutationNode
		if rows.Scan(&n.ID, &n.RelativePath, &n.Depth, &n.File, &n.Size) != nil {
			return nil, files.ErrUnavailable
		}
		if len(tree) >= files.MaxTreeResources || n.Size < 0 || n.Size > files.MaxTreeBytes-total {
			return nil, files.ErrCapacity
		}
		total += n.Size
		tree = append(tree, n)
	}
	if rows.Err() != nil || len(tree) == 0 {
		return nil, files.ErrUnavailable
	}
	return tree, nil
}

// removeMutationTree never commits and never touches process locks. The caller
// preflights the complete tree, commits atomically, then updates its lock state.
func removeMutationTree(ctx context.Context, tx *sql.Tx, tree []mutationNode) error {
	// Child-first deletion satisfies existing NO ACTION foreign keys without a
	// schema change, cascading rule, temporary table or unbounded SQL expression.
	ordered := append([]mutationNode(nil), tree...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Depth > ordered[j].Depth })
	for _, n := range ordered {
		if n.ID == rootID {
			return files.ErrInvalid
		}
		for _, statement := range []string{`DELETE FROM node_metadata WHERE node_id=?`, `DELETE FROM file_contents WHERE node_id=?`, `DELETE FROM nodes WHERE id=?`} {
			if _, err := tx.ExecContext(ctx, statement, n.ID); err != nil {
				return files.ErrUnavailable
			}
		}
	}
	return nil
}
