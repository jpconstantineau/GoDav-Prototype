//go:build !windows

package localfs

import (
	"errors"
	"io"
)

var errUnsupportedPlatform = errors.New("bootstrap is unsupported on this platform")

func CheckPrivateFile(path string) error { return errUnsupportedPlatform }

func PinPrivateDirectory(path string, requireFileInheritance bool) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}

func CheckPath(path string, allowMissing bool) error { return errUnsupportedPlatform }
func CheckPrivateDirectory(path string) error        { return errUnsupportedPlatform }
func AcquireOwnership(path string, requireFileInheritance bool) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}
