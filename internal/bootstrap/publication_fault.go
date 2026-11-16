//go:build godav_test_fail_yaml

package bootstrap

import "errors"

// Compiled only into the explicitly tagged disposable CLI fault fixture.
// No runtime flag or environment variable enables this in a normal binary.
func publishCommittedSettings(string) error {
	return errors.New("test-only settings I/O failure")
}
