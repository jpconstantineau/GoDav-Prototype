package files

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const MaxLocks = 1024

var ErrLocked = errors.New("required lock not submitted")
var ErrLockConflict = errors.New("conflicting lock")
var ErrLockScope = errors.New("lock does not cover request")
var ErrUnlockScope = errors.New("unlock does not cover request")
var ErrLockOwner = errors.New("lock creator differs")

type LockConflict struct {
	Path       string
	Descendant bool
}

func (e *LockConflict) Error() string { return ErrLockConflict.Error() }
func (e *LockConflict) Unwrap() error { return ErrLockConflict }

type LockRequired struct{ Path string }

func (e *LockRequired) Error() string { return ErrLocked.Error() }
func (e *LockRequired) Unwrap() error { return ErrLocked }

type DAVCondition struct {
	Not   bool
	Token string
	ETag  EntityTag
}
type DAVList struct {
	Path       string
	Tagged     bool
	Conditions []DAVCondition
}
type DAVExpectation struct {
	Present bool
	Lists   []DAVList
}
type LockRequest struct {
	OwnerXML  string
	ZeroDepth bool
	Duration  time.Duration
}
type Lease struct {
	Token, Principal, Path, OwnerXML string
	ZeroDepth                        bool
	Expires                          time.Time
	Duration                         time.Duration
}

// LockTable is process state, never persisted. Its owner must serialize every
// method with publication and namespace lookup using the same operation gate.
type LockTable struct{ entries map[string]Lease }

func (l *LockTable) Expire(now time.Time) {
	for token, lease := range l.entries {
		if !now.Before(lease.Expires) {
			delete(l.entries, token)
		}
	}
}
func covers(lease Lease, path string) bool {
	root, path := Key(lease.Path), Key(path)
	return path == root || !lease.ZeroDepth && (root == "" || strings.HasPrefix(path, root+"/"))
}
func (l *LockTable) At(path string) []Lease {
	var leases []Lease
	for _, lease := range l.entries {
		if covers(lease, path) {
			leases = append(leases, lease)
		}
	}
	sort.Slice(leases, func(i, j int) bool { return leases[i].Token < leases[j].Token })
	return leases
}

func (l *LockTable) Covers(token, path string) bool {
	lease, ok := l.entries[token]
	return ok && covers(lease, path)
}
func (l *LockTable) New(path, principal string, request LockRequest, now time.Time) (Lease, error) {
	if principal == "" || len(request.OwnerXML) > 16384 || request.Duration < time.Second || request.Duration > time.Hour {
		return Lease{}, ErrInvalid
	}
	lease := Lease{Path: path, Principal: principal, OwnerXML: request.OwnerXML, ZeroDepth: request.ZeroDepth, Duration: request.Duration, Expires: now.Add(request.Duration)}
	for _, existing := range l.entries {
		if covers(existing, path) || covers(lease, existing.Path) {
			return Lease{}, &LockConflict{Path: existing.Path, Descendant: Key(existing.Path) != Key(path) && covers(lease, existing.Path)}
		}
	}
	if len(l.entries) >= MaxLocks {
		return Lease{}, ErrCapacity
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Lease{}, ErrUnavailable
	}
	random[6] = (random[6] & 15) | 64
	random[8] = (random[8] & 63) | 128
	lease.Token = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16])
	if _, exists := l.entries[lease.Token]; exists {
		return Lease{}, ErrUnavailable
	}
	return lease, nil
}
func (l *LockTable) Install(lease Lease) {
	if l.entries == nil {
		l.entries = make(map[string]Lease)
	}
	l.entries[lease.Token] = lease
}
func (l *LockTable) Refresh(path, token, principal string, duration time.Duration, now time.Time) (Lease, error) {
	lease, ok := l.entries[token]
	if !ok || !covers(lease, path) {
		return Lease{}, ErrLockScope
	}
	if lease.Principal != principal {
		return Lease{}, ErrLockOwner
	}
	if duration < time.Second || duration > time.Hour {
		return Lease{}, ErrInvalid
	}
	lease.Duration, lease.Expires = duration, now.Add(duration)
	l.entries[token] = lease
	return lease, nil
}
func (l *LockTable) Unlock(path, token, principal string) error {
	lease, ok := l.entries[token]
	if !ok || !covers(lease, path) {
		return ErrUnlockScope
	}
	if lease.Principal != principal {
		return ErrLockOwner
	}
	delete(l.entries, token)
	return nil
}

// DAV If has two distinct purposes: boolean preconditions, and submission of
// tokens. A token in a failed or negated alternative still counts as submitted.
func (e DAVExpectation) Submitted() map[string]bool {
	out := make(map[string]bool)
	for _, list := range e.Lists {
		for _, c := range list.Conditions {
			if c.Token != "" {
				out[c.Token] = true
			}
		}
	}
	return out
}
func (l *LockTable) CheckDAV(e DAVExpectation, path string, node Node, exists bool, lookup func(string) (Node, bool, error)) error {
	if !e.Present {
		return nil
	}
	matched := false
	for _, list := range e.Lists {
		name, n, found := path, node, exists
		if list.Tagged {
			name = list.Path
			var err error
			n, found, err = lookup(name)
			if err != nil {
				return err
			}
		}
		all := true
		for _, c := range list.Conditions {
			value := false
			if c.Token != "" {
				if lease, ok := l.entries[c.Token]; ok && found {
					value = covers(lease, name)
				}
			} else {
				value = found && n.ETag != "" && c.ETag.Value == n.ETag
			}
			if c.Not {
				value = !value
			}
			all = all && value
		}
		matched = matched || all
	}
	if !matched {
		return ErrPrecondition
	}
	return nil
}
func (l *LockTable) CheckWrite(path, principal string, submitted map[string]bool, membership bool) error {
	paths := []string{path}
	if membership && path != "" {
		parent := ""
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			parent = path[:i]
		}
		paths = append(paths, parent)
	}
	for _, name := range paths {
		for _, lease := range l.At(name) {
			if !submitted[lease.Token] {
				return &LockRequired{Path: lease.Path}
			}
			if lease.Principal != principal {
				return ErrLockOwner
			}
		}
	}
	return nil
}

func (s *Service) Lock(ctx context.Context, path string, request LockRequest, e Expectation) (Lease, bool, error) {
	names, err := Components(path)
	if err != nil {
		return Lease{}, false, err
	}
	if strings.HasSuffix(path, "/") {
		snapshot, err := s.store.CollectionSnapshot(ctx, names, false)
		if err != nil {
			return Lease{}, false, err
		}
		if snapshot.Node.File {
			return Lease{}, false, ErrInvalid
		}
	}
	return s.store.CreateLock(ctx, names, request, e)
}
func (s *Service) RefreshLock(ctx context.Context, path, token string, duration time.Duration, e Expectation) (Lease, error) {
	names, err := Components(path)
	if err != nil {
		return Lease{}, err
	}
	if strings.HasSuffix(path, "/") {
		snapshot, err := s.store.CollectionSnapshot(ctx, names, false)
		if err != nil && !errors.Is(err, ErrMissing) {
			return Lease{}, err
		}
		if err == nil && snapshot.Node.File {
			return Lease{}, ErrInvalid
		}
	}
	return s.store.RefreshLock(ctx, names, token, duration, e)
}
func (s *Service) Unlock(ctx context.Context, path, token string, e Expectation) error {
	names, err := Components(path)
	if err != nil {
		return err
	}
	if strings.HasSuffix(path, "/") {
		snapshot, err := s.store.CollectionSnapshot(ctx, names, false)
		if err != nil && !errors.Is(err, ErrMissing) {
			return err
		}
		if err == nil && snapshot.Node.File {
			return ErrInvalid
		}
	}
	return s.store.Unlock(ctx, names, token, e)
}
