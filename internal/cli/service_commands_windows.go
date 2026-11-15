package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
	"golang.org/x/sys/windows"
)

func verifyOperator(id string) error {
	sid, err := windows.StringToSid(id)
	if err != nil || !sid.IsValid() || sid.String() != id || !strings.HasPrefix(id, "S-1-5-21-") || strings.Count(id, "-") != 7 {
		return errors.New("invalid operator")
	}
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil || !sid.Equals(user.User.Sid) {
		return errors.New("operator mismatch")
	}
	_, _, kind, err := sid.LookupAccount("")
	if err != nil || kind != windows.SidTypeUser {
		return errors.New("unknown operator")
	}
	return nil
}

func manageService(action service.Action, r service.Registration, stdout, stderr io.Writer) int {
	if verifyOperator(r.OperatorSID) != nil {
		fmt.Fprintln(stderr, "godav: service operation requires the explicit operator account")
		return 1
	}
	if action == service.Install {
		parent, err := localfs.PinProgramDirectory(r.OperatorSID, filepath.Dir(r.Executable))
		if err != nil {
			fmt.Fprintln(stderr, "godav: service executable integrity validation failed")
			return 1
		}
		defer parent.Close()
		program, err := localfs.PinProgramFile(r.OperatorSID, r.Executable)
		if err != nil {
			fmt.Fprintln(stderr, "godav: service executable integrity validation failed")
			return 1
		}
		defer program.Close()
		settings, err := localfs.PinPathFile(r.ConfigPath)
		if err != nil {
			fmt.Fprintln(stderr, "godav: service settings path validation failed")
			return 1
		}
		defer settings.Close()
	}
	status, err := service.ManageNative(context.Background(), action, r)
	return reportServiceResult(action, status, err, stdout, stderr)
}

// reportServiceResult is the CLI outcome boundary after native management.
func reportServiceResult(action service.Action, status service.Status, err error, stdout, stderr io.Writer) int {
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			fmt.Fprintln(stderr, "godav: service is not installed")
		} else {
			fmt.Fprintf(stderr, "godav: service operation failed: %s\n", err)
		}
		return 1
	}
	if action == service.Uninstall {
		fmt.Fprintln(stdout, "service: not-installed")
		return 0
	}
	state := "unknown"
	switch status.State {
	case service.Stopped:
		state = "stopped"
	case service.StartPending:
		state = "start-pending"
	case service.StopPending:
		state = "stop-pending"
	case service.Running:
		state = "running"
	}
	fmt.Fprintf(stdout, "service: %s\nwin32-exit: %d\nservice-exit: %d\n", state, status.Win32ExitCode, status.ServiceSpecificExitCode)
	// SCM reports ERROR_SERVICE_NEVER_STARTED until a start attempt since boot.
	// Registration and inspection must not require a start or hide the raw status.
	neverStarted := (action == service.Install || action == service.Check) && status.State == service.Stopped &&
		status.Win32ExitCode == uint32(windows.ERROR_SERVICE_NEVER_STARTED) && status.ServiceSpecificExitCode == 0
	if !neverStarted && (status.Win32ExitCode != 0 || status.ServiceSpecificExitCode != 0) {
		return 1
	}
	return 0
}

func serviceConfigPathValid(path string) bool {
	return filepath.IsAbs(path) && config.ValidPathSyntax(path)
}
