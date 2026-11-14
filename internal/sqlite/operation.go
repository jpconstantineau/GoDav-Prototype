package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Every complete operation on the retained connection shares this gate. It must
// be held across a transaction, not reacquired inside its component statements.
// Account administration can join the same gate without another connection.
func (s *AccountStore) acquire(ctx context.Context) error {
	if ctx.Err() != nil {
		return files.ErrUnavailable
	}
	select {
	case s.operation <- struct{}{}:
		if ctx.Err() != nil {
			s.release()
			return files.ErrUnavailable
		}
		return nil
	case <-ctx.Done():
		return files.ErrUnavailable
	}
}
func (s *AccountStore) release() { <-s.operation }
