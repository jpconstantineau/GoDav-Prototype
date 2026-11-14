//go:build godav_test_fail_metadata_commit

package sqlite

import (
	"context"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Synthetic build only: fail after metadata and tag updates, before commit.
func metadataCommitFailure(context.Context) error { return files.ErrUnavailable }
