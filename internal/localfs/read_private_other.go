//go:build !windows

package localfs

func ReadPrivateFile(path string, limit int64) ([]byte, error) { return nil, errUnsupportedPlatform }
