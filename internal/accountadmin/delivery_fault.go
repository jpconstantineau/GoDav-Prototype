//go:build godav_test_fail_console

package accountadmin

import (
	"errors"
	"os"
)

// Reuse the existing test-build-only console failure seam. No normal-release
// flag or environment setting can select this implementation.
func deliverPassword(*os.File, string) error {
	return errors.New("test-only private console I/O failure")
}
