package cli

import (
	"fmt"
	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"io"
	"path/filepath"
)

func serviceUnavailable(stderr io.Writer) int {
	fmt.Fprintln(stderr, "godav: service operation unavailable; identity and provisioning contract pending")
	return 1
}

func serviceConfigPathValid(path string) bool {
	return filepath.IsAbs(path) && config.ValidPathSyntax(path)
}
