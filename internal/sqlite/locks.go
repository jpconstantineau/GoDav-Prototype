package sqlite

import (
	"context"
	"errors"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"strings"
	"time"
)

// Call only while owning the existing operation gate. All tagged resources and
// current validators are read from this same transaction/snapshot; lock state
// cannot change between checking conditions and publishing the mutation.
func (s *AccountStore) checkDAV(ctx context.Context, q namespaceQuery, names []string, node files.Node, exists bool, e files.Expectation) error {
	if e.Invalid != nil {
		return e.Invalid
	}
	s.locks.Expire(time.Now())
	return s.locks.CheckDAV(e.DAV, strings.Join(names, "/"), node, exists, func(path string) (files.Node, bool, error) {
		parts, err := files.Components(path)
		if err != nil {
			return files.Node{}, false, err
		}
		n, _, err := lookupNode(ctx, q, parts)
		if errors.Is(err, files.ErrMissing) {
			return files.Node{}, false, nil
		}
		return n, err == nil, err
	})
}
func (s *AccountStore) checkWrite(ctx context.Context, q namespaceQuery, names []string, node files.Node, exists, membership bool, e files.Expectation) error {
	if err := e.CheckWrite(node, exists); err != nil {
		return err
	}
	if err := s.checkDAV(ctx, q, names, node, exists, e); err != nil {
		return err
	}
	return s.locks.CheckWrite(strings.Join(names, "/"), e.Principal, e.DAV.Submitted(), membership)
}
func (s *AccountStore) CreateLock(ctx context.Context, names []string, request files.LockRequest, e files.Expectation) (files.Lease, bool, error) {
	if !s.writable {
		return files.Lease{}, false, files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return files.Lease{}, false, err
	}
	defer s.release()
	node, path, err := lookupNode(ctx, s.conn, names)
	created := errors.Is(err, files.ErrMissing)
	if err != nil && !created {
		return files.Lease{}, false, err
	}
	if created {
		if len(names) == 0 {
			return files.Lease{}, false, files.ErrInvalid
		}
		parent, parentPath, err := lookupNode(ctx, s.conn, names[:len(names)-1])
		if errors.Is(err, files.ErrMissing) || parent.File {
			return files.Lease{}, false, files.ErrParentMissing
		}
		if err != nil {
			return files.Lease{}, false, err
		}
		path = names[len(names)-1]
		if parentPath != "" {
			path = parentPath + "/" + path
		}
	}
	if err := e.CheckWrite(node, !created); err != nil {
		return files.Lease{}, false, err
	}
	if err := s.checkDAV(ctx, s.conn, names, node, !created, e); err != nil {
		return files.Lease{}, false, err
	}
	now := time.Now()
	lease, err := s.locks.New(path, e.Principal, request, now)
	if err != nil {
		return files.Lease{}, false, err
	}
	if created {
		if _, _, err := s.putFile(ctx, names, []byte{}, "application/octet-stream", e); err != nil {
			return files.Lease{}, false, err
		}
	}
	// Install while publication ownership is still held, even if cancellation
	// arrives after the empty resource commit. No half-created lease is visible.
	s.locks.Install(lease)
	return lease, created, nil
}
func (s *AccountStore) RefreshLock(ctx context.Context, names []string, token string, duration time.Duration, e files.Expectation) (files.Lease, error) {
	if !s.writable {
		return files.Lease{}, files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return files.Lease{}, err
	}
	defer s.release()
	node, _, err := lookupNode(ctx, s.conn, names)
	if errors.Is(err, files.ErrMissing) {
		return files.Lease{}, files.ErrLockScope
	}
	if err != nil {
		return files.Lease{}, err
	}
	if err := e.CheckWrite(node, true); err != nil {
		return files.Lease{}, err
	}
	s.locks.Expire(time.Now())
	if !s.locks.Covers(token, strings.Join(names, "/")) {
		return files.Lease{}, files.ErrLockScope
	}
	if err := s.checkDAV(ctx, s.conn, names, node, true, e); err != nil {
		return files.Lease{}, err
	}
	return s.locks.Refresh(strings.Join(names, "/"), token, e.Principal, duration, time.Now())
}
func (s *AccountStore) Unlock(ctx context.Context, names []string, token string, e files.Expectation) error {
	if !s.writable {
		return files.ErrUnavailable
	}
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	node, _, err := lookupNode(ctx, s.conn, names)
	if errors.Is(err, files.ErrMissing) {
		return files.ErrUnlockScope
	}
	if err != nil {
		return err
	}
	if err := e.CheckWrite(node, true); err != nil {
		return err
	}
	if err := s.checkDAV(ctx, s.conn, names, node, true, e); err != nil {
		return err
	}
	return s.locks.Unlock(strings.Join(names, "/"), token, e.Principal)
}
