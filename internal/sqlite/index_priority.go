package sqlite

import (
	"context"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
)

// tryIndex never joins the foreground permit wait queue. A request already
// waiting for any DAV, MCP or auth store operation wins. The same mutex
// serializes the start of a foreground wait against this background probe.
func (s *AccountStore) tryIndex(ctx context.Context, b indexing.Budget, stage string) (func(), indexing.Pressure, error) {
	if ctx.Err() != nil {
		return nil, "", indexing.ErrBusy
	}
	s.foregroundMu.Lock()
	defer s.foregroundMu.Unlock()
	if b.MCPWaiting != nil && b.MCPWaiting() > 0 {
		return nil, indexing.MCPQueue, nil
	}
	if s.foregroundWaiting > 0 || len(s.operation) > 0 {
		return nil, indexing.Foreground, nil
	}
	if time.Since(s.lastForegroundAt) < b.QuietPeriod {
		return nil, indexing.QuietPeriod, nil
	}
	if ctx.Err() != nil {
		return nil, "", indexing.ErrBusy
	}
	select {
	case s.operation <- struct{}{}:
		now := time.Now()
		return func() {
			<-s.operation
			if b.ObserveStoreHold != nil {
				b.ObserveStoreHold(stage, time.Since(now))
			}
		}, "", nil
	default:
		return nil, indexing.Foreground, nil
	}
}
