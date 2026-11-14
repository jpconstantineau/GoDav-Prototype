//go:build !windows

package cli

import (
	"fmt"
	"io"
	"path/filepath"
)

func serviceUnavailable(stderr io.Writer) int {
	fmt.Fprintln(stderr, "godav: native Windows service commands are unsupported on this platform")
	return 1
}

// A non-Windows command never opens this path. Native absolute syntax is enough
// to distinguish usage errors before reporting the platform as unsupported.
func serviceConfigPathValid(path string) bool { return filepath.IsAbs(path) }
