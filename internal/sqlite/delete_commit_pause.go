//go:build godav_test_pause_delete_commit && !godav_test_fail_delete_commit

package sqlite

import (
	"context"
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"os"
	"time"
)

// Synthetic CLI lifecycle synchronization; always roll back after the pause.
func deleteCommitFailure(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "test-only delete publication pause")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return files.ErrUnavailable
}
