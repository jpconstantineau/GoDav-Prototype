//go:build godav_test_pause_move_commit && !godav_test_fail_move_commit

package sqlite

import (
	"context"
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"os"
	"time"
)

// Disposable tagged executable only; no request header, flag or environment
// controls this pause, and this publication always rolls back.
func moveCommitFailure(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "test-only move publication pause")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return files.ErrUnavailable
}
