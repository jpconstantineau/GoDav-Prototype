//go:build !windows

package server

import "errors"

func newHTTPTestDirectory() (string, error) {
	return "", errors.New("HTTP test mode currently requires Windows private-file support")
}
