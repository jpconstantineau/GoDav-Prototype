//go:build godav_test_stuck_file_commit && !godav_test_fail_file_commit && !godav_test_pause_file_commit

package sqlite

import (
	"context"
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"os"
	"time"
)

// A disposable-build-only unresponsive owner. There is no runtime control and
// publication always fails. This tests waiting for owners, not disk fault repair.
func fileCommitFailure(context.Context) error {
	fmt.Fprintln(os.Stderr, "test-only unresponsive file publication pause")
	time.Sleep(3 * time.Second)
	return files.ErrUnavailable
}
