//go:build godav_test_fail_file_commit

package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Synthetic precommit failure. It does not simulate hardware or COMMIT I/O.
func fileCommitFailure(context.Context) error { return files.ErrUnavailable }
