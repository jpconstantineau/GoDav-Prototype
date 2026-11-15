package files

import "strings"

// CheckTreeWrite checks a removed/rebound subtree and its parent membership.
// Like the other LockTable methods, callers must own the publication gate.
// Enumerating leases is bounded by MaxLocks, independently of namespace size.
func (l *LockTable) CheckTreeWrite(path, principal string, submitted map[string]bool) error {
	if err := l.CheckWrite(path, principal, submitted, true); err != nil {
		return err
	}
	for _, lease := range l.entries {
		if withinTree(path, lease.Path) {
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

// RemoveTree forgets leases rooted in removed mappings, never ancestor leases.
// Invoke only after the namespace transaction commits, before releasing its gate.
func (l *LockTable) RemoveTree(path string) {
	for token, lease := range l.entries {
		if withinTree(path, lease.Path) {
			delete(l.entries, token)
		}
	}
}

func withinTree(root, path string) bool {
	root, path = Key(root), Key(path)
	return root == "" || path == root || strings.HasPrefix(path, root+"/")
}
