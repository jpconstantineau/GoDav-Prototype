package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
)

const serviceUsage = "godav: invalid service arguments; explicit --name, absolute --config and --operator-sid are required; install also requires matching --account"

func serviceCommand(action string, args []string, stdout, stderr io.Writer) int {
	values, ok := explicitValues(args, "--name", "--config", "--operator-sid", "--account")
	executable, err := os.Executable()
	r := service.Registration{Name: values["--name"], Executable: executable, ConfigPath: values["--config"], OperatorSID: values["--operator-sid"]}
	if !ok || err != nil || r.Validate() != nil || !serviceConfigPathValid(r.ConfigPath) ||
		(action == "install" && values["--account"] != r.Account()) || (action != "install" && values["--account"] != "") {
		fmt.Fprintln(stderr, serviceUsage)
		return 2
	}
	return manageService(service.Action(action), r, stdout, stderr)
}

// Explicit values disallow repetitions, controls, positional arguments and
// flags used as values. No service command accepts credentials.
func explicitValues(args []string, allowed ...string) (map[string]string, bool) {
	values := map[string]string{}
	for len(args) > 0 {
		flag, value, joined := strings.Cut(args[0], "=")
		args = args[1:]
		if !joined {
			if len(args) == 0 {
				return values, false
			}
			value, args = args[0], args[1:]
		}
		known := false
		for _, name := range allowed {
			known = known || flag == name
		}
		if !known || value == "" || strings.HasPrefix(value, "--") || strings.IndexFunc(value, unicode.IsControl) >= 0 || values[flag] != "" {
			return values, false
		}
		values[flag] = value
	}
	return values, true
}
