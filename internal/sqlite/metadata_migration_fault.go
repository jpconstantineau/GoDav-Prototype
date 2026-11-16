//go:build godav_test_fail_metadata_migration

package sqlite

import "github.com/jpconstantineau/GoDav-Prototype/internal/files"

// Synthetic build only: fail after schema changes, before their transaction commits.
func metadataMigrationFailure() error { return files.ErrUnavailable }
