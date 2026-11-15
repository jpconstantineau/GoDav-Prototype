//go:build !godav_test_fail_delete_commit && !godav_test_pause_delete_commit

package sqlite

import "context"

func deleteCommitFailure(context.Context) error { return nil }
