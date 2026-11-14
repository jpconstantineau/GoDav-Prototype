package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"time"
)

// Every complete operation on the retained connection shares this gate. It must
// be held across a transaction, not reacquired inside its component statements.
// Account administration can join the same gate without another connection.
func (s *AccountStore) acquire(ctx context.Context) error {
	if ctx.Err() != nil {
		return files.ErrBusy
	}
	wait, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	select {
	case s.operation <- struct{}{}:
		if wait.Err() != nil {
			s.release()
			return files.ErrBusy
		}
		return nil
	case <-wait.Done():
		return files.ErrBusy
	}
}
func (s *AccountStore) release() { <-s.operation }
