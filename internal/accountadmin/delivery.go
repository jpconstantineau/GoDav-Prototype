//go:build !godav_test_fail_console

package accountadmin

import (
	"fmt"
	"os"
)

func deliverPassword(console *os.File, password string) error {
	_, err := fmt.Fprintf(console, "Password: %s\n", password)
	return err
}
