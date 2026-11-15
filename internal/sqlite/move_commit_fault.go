//go:build godav_test_fail_move_commit

package sqlite

import (
	"context"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Synthetic precommit failure, not a device fault or crash simulation.
func moveCommitFailure(context.Context) error { return files.ErrUnavailable }
