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

const help = "Usage: godav <command>\n\nCommands:\n  init config [--config <path>] --username <name>  Initialize privately (default: godav.yaml)\n  version  Show build identity\n  help     Show this help\n"

func Run(args []string, stdout, stderr io.Writer) int {
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
