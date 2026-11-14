//go:build godav_test_pause_file_commit && !godav_test_fail_file_commit

package sqlite

import (
	"context"
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"os"
	"time"
)

// A fixed test-only CLI synchronization record, never request data or a runtime
// control. The disposable tagged binary always rolls this publication back.
func fileCommitFailure(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "test-only file publication pause")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return files.ErrUnavailable
}
