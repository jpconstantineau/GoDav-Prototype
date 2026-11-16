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

type serverOptions struct {
	path, serviceName, operatorSID string
	scm                            bool
}

func runServer(args []string, stdout, stderr io.Writer) int {
	var options serverOptions
	var valuesArgs []string
	for _, arg := range args {
		if arg == "--scm" {
			if options.scm {
				fmt.Fprintln(stderr, "godav: invalid service runtime arguments")
				return 2
			}
			options.scm = true
		} else {
			valuesArgs = append(valuesArgs, arg)
		}
	}
	values, valid := explicitValues(valuesArgs, "--config", "--service-name", "--operator-sid")
	options.path, options.serviceName, options.operatorSID = values["--config"], values["--service-name"], values["--operator-sid"]
	if !valid || options.path == "" || strings.HasPrefix(options.path, "--") {
		fmt.Fprintln(stderr, "godav: run server requires one explicit --config path")
		return 2
	}
	if (options.serviceName == "") != (options.operatorSID == "") || (options.scm && options.serviceName == "") {
		fmt.Fprintln(stderr, "godav: invalid service runtime arguments")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := runServerPlatform(ctx, options, stderr); err != nil {
		if errors.Is(err, errServiceContext) {
			fmt.Fprintln(stderr, "godav: SCM invocation requires the native Windows service dispatcher")
		} else if errors.Is(err, server.ErrShutdownFailed) {
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

var errServiceContext = errors.New("invalid native service context")

func buildIdentity() server.Identity {
	return server.Identity{Revision: revision, BuildTime: buildTime, Source: sourceState}
}
