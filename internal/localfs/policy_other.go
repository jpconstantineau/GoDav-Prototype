//go:build !windows

package localfs

import "io"

func ServicePolicy(serviceSID, operatorSID string) (*Policy, error) {
	return nil, errUnsupportedPlatform
}
func (p *Policy) pinObject(path string, directory, mutable, inherit bool) (io.Closer, error) {
	return nil, errUnsupportedPlatform
}
func (p *Policy) AcquireOwnership(path string, inherit bool) (io.Closer, error) {
	return AcquireOwnership(path, inherit)
}
func (p *Policy) ReadPrivateFile(path string, limit int64) ([]byte, error) {
	return ReadPrivateFile(path, limit)
}
