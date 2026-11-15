//go:build !godav_test_fail_move_commit && !godav_test_pause_move_commit

package sqlite

import "context"

func moveCommitFailure(context.Context) error { return nil }
