//go:build !windows

package localfs

import "io"

func PinProgramFile(operatorSID, path string) (io.Closer, error) { return nil, errUnsupportedPlatform }
func PinProgramDirectory(operatorSID, path string) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}
func PinPathFile(path string) (io.Closer, error) { return nil, errUnsupportedPlatform }
