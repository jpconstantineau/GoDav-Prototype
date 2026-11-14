//go:build !windows

package cli

import (
	"fmt"
	"io"
)

func serviceUnavailable(stderr io.Writer) int {
	fmt.Fprintln(stderr, "godav: native Windows service commands are unsupported on this platform")
	return 1
}
