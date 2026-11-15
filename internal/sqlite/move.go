package sqlite

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Move retains stable node identity and publishes the namespace change under
// the same gate and transaction used for conditions and process lease state.
func (s *AccountStore) Move(ctx context.Context, from, to []string, options files.MoveOptions, e files.Expectation) (bool, error) {
	if !s.writable {
		return false, files.ErrUnavailable
	}
	if len(from) == 0 || len(to) == 0 {
		return false, files.ErrMoveForbidden
	}
	if err := s.acquire(ctx); err != nil {
		return false, err
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, files.ErrUnavailable
	}
	defer tx.Rollback()
	source, sourcePath, err := lookupNode(ctx, tx, from)
	if err != nil {
		return false, err
	}
	if source.File && (options.SourceCollection || options.DestinationCollection) || !source.File && options.ZeroDepth {
		return false, files.ErrInvalid
	}
	parent, parentPath, err := lookupNode(ctx, tx, to[:len(to)-1])
	if errors.Is(err, files.ErrMissing) || parent.File {
		return false, files.ErrParentMissing
	}
	if err != nil {
		return false, err
	}
	destinationPath := to[len(to)-1]
	if parentPath != "" {
		destinationPath = parentPath + "/" + destinationPath
	}
	if _, err := files.Components(destinationPath); err != nil {
		return false, err
	}
	if files.Key(destinationPath) != files.Key(sourcePath) &&
		(strings.HasPrefix(files.Key(destinationPath), files.Key(sourcePath)+"/") || strings.HasPrefix(files.Key(sourcePath), files.Key(destinationPath)+"/")) {
		return false, files.ErrMoveForbidden
	}
	if sourcePath == destinationPath {
		return false, files.ErrMoveForbidden
	}
	destination, _, err := lookupNode(ctx, tx, to)
	created := errors.Is(err, files.ErrMissing)
	if err != nil && !created {
		return false, err
	}
	if !created && destination.File && options.DestinationCollection {
		return false, files.ErrInvalid
	}
	if !created && !options.Overwrite {
		return false, files.ErrPrecondition
	}
	if err := e.CheckWrite(source, true); err != nil {
		return false, err
	}
	if err := s.checkDAV(ctx, tx, from, source, true, e); err != nil {
		return false, err
	}
	submitted := e.DAV.Submitted()
	tree, err := readMutationTree(ctx, tx, source)
	if err != nil {
		return false, err
	}
	paths := make(map[string]string, len(tree))
	for _, node := range tree {
		path := destinationPath
		if node.RelativePath != "" {
			path += "/" + node.RelativePath
		}
		if _, err := files.Components(path); err != nil {
			if node.RelativePath != "" {
				return false, &files.MoveFailure{Path: path, Collection: !node.File, Cause: err}
			}
			return false, err
		}
		paths[files.Key(path)] = path
	}
	var replaced []mutationNode
	if !created && destination.ID != source.ID {
		replaced, err = readMutationTree(ctx, tx, destination)
		if err != nil {
			return false, err
		}
		if len(tree)+len(replaced) > files.MaxTreeResources {
			return false, files.ErrCapacity
		}
		var size int64
		for _, nodes := range [][]mutationNode{tree, replaced} {
			for _, n := range nodes {
				if n.Size > files.MaxTreeBytes-size {
					return false, files.ErrCapacity
				}
				size += n.Size
			}
		}
	}
	// Bound metadata preflight also supplies collection kinds for canonical
	// failing-child hrefs. There is no extra lookup/query during error formatting.
	if err := s.locks.CheckTreeWrite(sourcePath, e.Principal, submitted); err != nil {
		return false, moveTreeLockFailure(err, sourcePath, tree)
	}
	if err := s.locks.CheckTreeWrite(destinationPath, e.Principal, submitted); err != nil {
		return false, moveTreeLockFailure(err, destinationPath, replaced)
	}
	var oldParent string
	if tx.QueryRowContext(ctx, `SELECT parent_id FROM nodes WHERE id=?`, source.ID).Scan(&oldParent) != nil {
		return false, files.ErrUnavailable
	}
	if created && oldParent != parent.ID {
		var count int
		if tx.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE parent_id=?`, parent.ID).Scan(&count) != nil {
			return false, files.ErrUnavailable
		}
		if count >= files.MaxChildren {
			return false, files.ErrCapacity
		}
	}
	if len(replaced) > 0 {
		if err := removeMutationTree(ctx, tx, replaced); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET parent_id=?,name=?,name_key=? WHERE id=?`, parent.ID, to[len(to)-1], files.Key(to[len(to)-1]), source.ID); err != nil {
		return false, files.ErrUnavailable
	}
	now := time.Now().UTC().Unix()
	if destination.ID != source.ID {
		for _, node := range tree {
			if node.File {
				if _, err := tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=?`, now, node.ID); err != nil {
					return false, files.ErrUnavailable
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=? OR id=?`, now, oldParent, parent.ID); err != nil {
		return false, files.ErrUnavailable
	}
	if moveCommitFailure(ctx) != nil {
		return false, files.ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return false, files.ErrUnavailable
	}
	if destination.ID == source.ID {
		s.locks.RenameTreeSpelling(sourcePath, destinationPath)
	} else {
		s.locks.RemoveTree(sourcePath)
		s.locks.KeepTreeMappings(destinationPath, paths)
	}
	return created, nil
}

func moveTreeLockFailure(err error, root string, tree []mutationNode) error {
	var failure *files.TreeLockFailure
	if !errors.As(err, &failure) {
		return err
	}
	for _, node := range tree {
		path := root
		if node.RelativePath != "" {
			path += "/" + node.RelativePath
		}
		if files.Key(path) == files.Key(failure.Path) {
			return &files.MoveFailure{Path: failure.Path, Collection: !node.File, Cause: err}
		}
	}
	return err
}
