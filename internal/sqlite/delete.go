package sqlite

import (
	"context"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

func (s *AccountStore) DeleteResource(ctx context.Context, names []string, request files.DeleteRequest, e files.Expectation) error {
	if !s.writable {
		return files.ErrUnavailable
	}
	if len(names) == 0 {
		return files.ErrExists
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return files.ErrUnavailable
	}
	defer tx.Rollback()
	node, path, err := lookupNode(ctx, tx, names)
	if err != nil {
		return err
	}
	if node.File && request.CollectionOnly || !node.File && request.FiniteDepth {
		return files.ErrInvalid
	}
	if err := s.checkWrite(ctx, tx, names, node, true, true, e); err != nil {
		return err
	}
	if err := s.locks.CheckTreeWrite(path, e.Principal, e.DAV.Submitted()); err != nil {
		return err
	}
	tree, err := readMutationTree(ctx, tx, node)
	if err != nil {
		return err
	}
	parent, _, err := lookupNode(ctx, tx, names[:len(names)-1])
	if err != nil {
		return err
	}
	if err := removeMutationTree(ctx, tx, tree); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET modified_at=? WHERE id=?`, time.Now().UTC().Unix(), parent.ID); err != nil {
		return files.ErrUnavailable
	}
	if deleteCommitFailure(ctx) != nil {
		return files.ErrUnavailable
	}
	if tx.Commit() != nil {
		return files.ErrUnavailable
	}
	// Publication owns the operation gate through lease removal even when the
	// request is cancelled after commit. An ancestor's lease remains applicable.
	s.locks.RemoveTree(path)
	return nil
}
