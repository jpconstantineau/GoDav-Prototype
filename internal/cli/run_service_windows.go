package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
	"golang.org/x/sys/windows"
)

func runtimePolicy(path, name, operator string, scm bool) (*localfs.Policy, error) {
	executable, err := os.Executable()
	r := service.Registration{Name: name, Executable: executable, ConfigPath: path, OperatorSID: operator}
	if err != nil || r.Validate() != nil || !serviceConfigPathValid(path) {
		return nil, errors.New("invalid service runtime arguments")
	}
	sid, _, _, err := windows.LookupSID("", r.Account())
	if err != nil {
		return nil, errors.New("service identity unavailable")
	}
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	expected := operator
	if scm {
		expected = sid.String()
	}
	if err != nil || user.User.Sid.String() != expected {
		return nil, errors.New("service identity mismatch")
	}
	return localfs.ServicePolicy(sid.String(), operator)
}

func accountPolicy(path, name, operator string) (*localfs.Policy, error) {
	if name == "" && operator == "" {
		return nil, nil
	}
	return runtimePolicy(path, name, operator, false)
}

func runServerPlatform(ctx context.Context, options serverOptions, output io.Writer) error {
	dispatcher := service.NativeDispatcher{}
	native, err := dispatcher.IsService()
	if err != nil || native != options.scm {
		return errServiceContext
	}
	if options.scm {
		// Connect to SCM before policy, filesystem, database or TLS validation. The
		// shared handler reports StartPending until the real runner reports Ready.
		return service.Dispatch(dispatcher, options.serviceName, func(ctx context.Context, observer server.Observer) error {
			return runSharedServer(ctx, options, output, observer)
		})
	}
	if options.serviceName != "" {
		return runSharedServer(ctx, options, output, nil)
	}
	return server.Run(ctx, options.path, buildIdentity(), output)
}

func runSharedServer(ctx context.Context, options serverOptions, output io.Writer, observer server.Observer) error {
	policy, err := runtimePolicy(options.path, options.serviceName, options.operatorSID, options.scm)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	parent, err := policy.PinReadOnlyDirectory(filepath.Dir(executable))
	if err != nil {
		return err
	}
	defer parent.Close()
	program, err := policy.PinReadOnlyFile(executable)
	if err != nil {
		return err
	}
	defer program.Close()
	return server.RunWithPolicy(ctx, options.path, buildIdentity(), output, observer, policy)
}
