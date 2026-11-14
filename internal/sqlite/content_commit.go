//go:build !godav_test_fail_file_commit && !godav_test_pause_file_commit && !godav_test_stuck_file_commit

package sqlite

import "context"

func fileCommitFailure(context.Context) error { return nil }
