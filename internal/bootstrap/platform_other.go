//go:build !windows

package bootstrap

import "errors"

func publishExclusive(temp, path string) error {
	return errors.New("bootstrap is unsupported on this platform")
}
