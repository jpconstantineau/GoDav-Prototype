//go:build !windows

package cli

import (
	"errors"
	"os"
)

func openPrivateConsole() (*os.File, error) {
	return nil, errors.New("private initialization console is not supported on this platform")
}
