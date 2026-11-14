//go:build godav_test_fail_metadata_commit

package sqlite

import "github.com/jpconstantineau/GoDav-Prototype/internal/files"

// Synthetic build only: fail after metadata and tag updates, before commit.
func metadataCommitFailure() error { return files.ErrUnavailable }
