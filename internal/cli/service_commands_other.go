//go:build !windows

package cli

import (
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
	"io"
	"path/filepath"
)

func manageService(_ service.Action, _ service.Registration, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "godav: native Windows service commands are unsupported on this platform")
	return 1
}

// Non-Windows commands never open this path.
func serviceConfigPathValid(path string) bool { return filepath.IsAbs(path) }
