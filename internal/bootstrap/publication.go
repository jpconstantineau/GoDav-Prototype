//go:build !godav_test_fail_yaml

package bootstrap

func publishCommittedSettings(path string) error { return publishSettings(path) }
