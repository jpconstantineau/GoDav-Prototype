//go:build godav_test_fail_console

package bootstrap

import (
	"errors"
	"os"
)

// No runtime control enables this in a normal executable.
func deliverPassword(*os.File, string) error {
	return errors.New("test-only private console I/O failure")
}
