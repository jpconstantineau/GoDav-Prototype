//go:build !godav_test_fail_console

package bootstrap

import (
	"fmt"
	"os"
)

func deliverPassword(console *os.File, password string) error {
	_, err := fmt.Fprintf(console, "Initial password: %s\n", password)
	return err
}
