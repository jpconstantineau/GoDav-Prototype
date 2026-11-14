//go:build godav_test_fail_file_stage

package files

// Only the explicitly tagged disposable fixture fails after receiving bytes.
func stageFileFailure() error { return ErrUnavailable }
