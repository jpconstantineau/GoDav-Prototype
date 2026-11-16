//go:build !godav_test_fail_metadata_commit && !godav_test_pause_metadata_commit

package sqlite

import "context"

func metadataCommitFailure(context.Context) error { return nil }
