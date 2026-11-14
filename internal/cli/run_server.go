package cli

import (
	"context"
	"errors"
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
	if err := server.Run(ctx, path, server.Identity{Revision: revision, BuildTime: buildTime, Source: sourceState}, stderr); err != nil {
		if errors.Is(err, server.ErrShutdownFailed) {
			fmt.Fprintln(stderr, "godav: server shutdown failed")
		} else if errors.Is(err, server.ErrShutdownDeadline) {
			fmt.Fprintln(stderr, "godav: shutdown deadline expired; unfinished work cancelled")
		} else if errors.Is(err, server.ErrServing) {
			fmt.Fprintln(stderr, "godav: HTTPS serving failed")
		} else {
			fmt.Fprintln(stderr, "godav: server startup failed")
		}
		return 1
	}
	return 0
}
