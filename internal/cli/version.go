// Package cli implements the public GoDav command-line interface.
package cli

import (
	"fmt"
	"io"
)

// These strings are stamped by the release builder. Development builds remain
// explicitly unknown rather than confusing commit time with build time.
var revision = "unknown"
var buildTime = "unknown"
var sourceState = "unknown"

const help = "Usage: godav <command>\n\nCommands:\n  init config [--config <path>] --username <name>  Initialize privately (default: godav.yaml)\n  run server --config <path>  Serve an initialized HTTPS share\n  add user [--config <path>] --username <name>  Add an account offline\n  reset password [--config <path>] --username <name>  Reset an account offline\n  version  Show build identity\n  help     Show this help\n"

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) >= 2 && args[1] == "service" {
		switch args[0] {
		case "install", "check", "start", "stop", "uninstall":
			return serviceCommand(args[0], args[2:], stdout, stderr)
		}
	}
	if len(args) >= 2 && args[0] == "run" && args[1] == "server" {
		return runServer(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && ((args[0] == "add" && args[1] == "user") || (args[0] == "reset" && args[1] == "password")) {
		return accountCommand(args[0]+" "+args[1], args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "init" && args[1] == "config" {
		return initConfig(args[2:], stdout, stderr)
	}
	if len(args) == 0 || (len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h")) {
		fmt.Fprint(stdout, help)
		return 0
	}
	if len(args) != 1 || args[0] != "version" {
		fmt.Fprint(stderr, "godav: invalid command\n", help)
		return 2
	}
	fmt.Fprintf(stdout, "revision: %s\nbuild-time: %s\nsource: %s\n", revision, buildTime, sourceState)
	return 0
}
