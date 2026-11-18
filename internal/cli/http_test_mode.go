package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

// This separate command cannot load a config or select existing data/service state.
func runHTTPTest(args []string, _ io.Writer, stderr io.Writer) int {
	values, valid := explicitValues(args, "--port")
	port := 8080
	if raw, ok := values["--port"]; ok {
		var err error
		port, err = strconv.Atoi(raw)
		valid = valid && err == nil && strconv.Itoa(port) == raw
	}
	if !valid || port < 1 || port > 65535 {
		fmt.Fprintln(stderr, "godav: run http-test accepts only --port <1-65535>")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := server.RunHTTPTest(ctx, port, buildIdentity(), stderr); err != nil {
		fmt.Fprintln(stderr, "godav: HTTP test server failed")
		return 1
	}
	return 0
}
