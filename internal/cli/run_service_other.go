//go:build !windows

package cli

import (
	"context"
	"errors"
	"io"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
)

func accountPolicy(path, name, operator string) (*localfs.Policy, error) {
	if name == "" && operator == "" {
		return nil, nil
	}
	return nil, errors.New("native Windows service identity is unsupported on this platform")
}
func runServerPlatform(ctx context.Context, options serverOptions, output io.Writer) error {
	if options.scm {
		return errServiceContext
	}
	if options.serviceName != "" {
		return errors.New("native Windows service identity is unsupported on this platform")
	}
	return server.Run(ctx, options.path, buildIdentity(), output)
}
