package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func runServer(args []string, stdout, stderr io.Writer) int {
	path := ""
	if len(args) == 2 && args[0] == "--config" {
		path = args[1]
	}
	if len(args) == 1 && strings.HasPrefix(args[0], "--config=") {
		path = strings.TrimPrefix(args[0], "--config=")
	}
	if path == "" || strings.HasPrefix(path, "--") {
		fmt.Fprintln(stderr, "godav: run server requires one explicit --config path")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if server.Run(ctx, path, server.Identity{Revision: revision, BuildTime: buildTime, Source: sourceState}, stderr) != nil {
		fmt.Fprintln(stderr, "godav: server startup failed")
		return 1
	}
	return 0
}
