//go:build godav_test_fail_delete_commit

package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Synthetic disposable build only: all rows changed but no commit performed.
func deleteCommitFailure(context.Context) error { return files.ErrUnavailable }
