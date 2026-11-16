//go:build godav_test_pause_metadata_commit && !godav_test_fail_metadata_commit

package sqlite

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Synthetic CLI synchronization only. Both property and validator updates have
// occurred, but this tagged build always rolls back without committing them.
func metadataCommitFailure(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, "test-only metadata publication pause")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
	return files.ErrUnavailable
}
