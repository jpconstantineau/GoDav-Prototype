package files

import (
	"context"
	"errors"
	"strings"
)

var ErrMoveForbidden = errors.New("move mapping forbidden")

type MoveFailure struct {
	Path  string
	Cause error
}

func (e *MoveFailure) Error() string { return e.Cause.Error() }
func (e *MoveFailure) Unwrap() error { return e.Cause }

type MoveOptions struct {
	Overwrite                               bool
	ZeroDepth                               bool
	SourceCollection, DestinationCollection bool
}

func (s *Service) Move(ctx context.Context, source, destination string, options MoveOptions, e Expectation) (bool, error) {
	from, err := Components(source)
	if err != nil {
		return false, err
	}
	to, err := Components(destination)
	if err != nil {
		return false, err
	}
	if len(from) == 0 || len(to) == 0 {
		return false, ErrMoveForbidden
	}
	options.SourceCollection = strings.HasSuffix(source, "/")
	options.DestinationCollection = strings.HasSuffix(destination, "/")
	return s.store.Move(ctx, from, to, options, e)
}

// A case-only spelling update is not a removal of namespace mappings. Preserve
// its leases, updating only display paths after the namespace commit succeeds.
func (l *LockTable) RenameTreeSpelling(source, destination string) {
	if Key(source) != Key(destination) {
		return
	}
	count := len(strings.Split(source, "/"))
	for token, lease := range l.entries {
		if withinTree(source, lease.Path) {
			parts := strings.Split(lease.Path, "/")
			lease.Path = destination
			if len(parts) > count {
				lease.Path += "/" + strings.Join(parts[count:], "/")
			}
			l.entries[token] = lease
		}
	}
}

// KeepTreeMappings removes overwritten descendant lock roots that no longer
// map to a resource. Leases whose destination URL survives still protect that
// URL after MOVE (RFC 4918 section 7.6), independently of the new node identity.
func (l *LockTable) KeepTreeMappings(root string, paths map[string]string) {
	for token, lease := range l.entries {
		if withinTree(root, lease.Path) {
			if canonical, exists := paths[Key(lease.Path)]; exists {
				lease.Path = canonical
				l.entries[token] = lease
			} else {
				delete(l.entries, token)
			}
		}
	}
}
