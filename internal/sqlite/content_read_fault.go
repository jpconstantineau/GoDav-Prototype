//go:build godav_test_fail_file_read

package sqlite

import "github.com/jpconstantineau/GoDav-Prototype/internal/files"

// This fixture makes a content read fail while metadata remains available.
func fileReadFailure() error { return files.ErrUnavailable }
