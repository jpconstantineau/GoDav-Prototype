//go:build !windows

package bootstrap

import (
	"errors"
	"io"
)

var errUnsupportedPlatform = errors.New("bootstrap is unsupported on this platform")

func checkPrivateFile(path string) error { return errUnsupportedPlatform }

func pinPrivateDirectory(path string, requireFileInheritance bool) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}

func checkPath(path string, allowMissing bool) error { return errUnsupportedPlatform }
func checkPrivateDirectory(path string) error        { return errUnsupportedPlatform }
func acquireOwnership(path string, requireFileInheritance bool) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}
func publishExclusive(temp, path string) error { return errUnsupportedPlatform }
