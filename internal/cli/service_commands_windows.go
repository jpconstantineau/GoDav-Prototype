package cli

import (
	"fmt"
	"io"
)

func serviceUnavailable(stderr io.Writer) int {
	fmt.Fprintln(stderr, "godav: service operation unavailable; identity and provisioning contract pending")
	return 1
}
