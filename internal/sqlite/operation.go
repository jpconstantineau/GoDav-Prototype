package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/requestlog"
	"time"
)

// Every complete operation on the retained connection shares this gate. It must
// be held across a transaction, not reacquired inside its component statements.
// Account administration can join the same gate without another connection.
func (s *AccountStore) acquire(ctx context.Context) (result error) {
	started := time.Now()
	defer func() { requestlog.StoreWait(ctx, time.Since(started), result) }()
	if ctx.Err() != nil {
		return files.ErrBusy
	}
	if s.indexPriority.Load() {
		s.foregroundMu.Lock()
		s.foregroundWaiting++
		s.foregroundMu.Unlock()
		defer func() {
			s.foregroundMu.Lock()
			s.foregroundWaiting--
			s.foregroundMu.Unlock()
		}()
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
func (s *AccountStore) release() {
	if !s.indexPriority.Load() {
		<-s.operation
		return
	}
	s.foregroundMu.Lock()
	s.lastForegroundAt = time.Now()
	<-s.operation
	s.foregroundMu.Unlock()
}
