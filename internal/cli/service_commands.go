package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
)

func serviceCommand(action string, args []string, stdout, stderr io.Writer) int {
	values := map[string]string{}
	valid := true
	for len(args) > 0 {
		flag, value, joined := strings.Cut(args[0], "=")
		args = args[1:]
		if !joined {
			if len(args) == 0 {
				valid = false
				break
			}
			value, args = args[0], args[1:]
		}
		if (flag != "--name" && (action != "install" || (flag != "--config" && flag != "--account"))) ||
			value == "" || strings.HasPrefix(value, "--") || strings.ContainsAny(value, "\x00\r\n") || values[flag] != "" {
			valid = false
			break
		}
		values[flag] = value
	}
	name := values["--name"]
	if len(name) < 1 || len(name) > 64 {
		valid = false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' && c != '.' {
			valid = false
		}
	}
	if action == "install" && (!filepath.IsAbs(values["--config"]) || !config.ValidPathSyntax(values["--config"]) || values["--account"] == "") {
		valid = false
	}
	if !valid {
		fmt.Fprintln(stderr, "godav: invalid service arguments; explicit --name is required, and install also requires absolute --config and explicit --account")
		return 2
	}
	return serviceUnavailable(stderr)
}
