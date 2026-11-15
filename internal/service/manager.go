package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Action string

const (
	Install   Action = "install"
	Check     Action = "check"
	Start     Action = "start"
	Stop      Action = "stop"
	Uninstall Action = "uninstall"
)

type Registration struct{ Name, Executable, ConfigPath, OperatorSID string }

func (r Registration) Account() string { return `NT SERVICE\` + r.Name }
func (r Registration) Arguments() []string {
	return []string{r.Executable, "run", "server", "--config", r.ConfigPath, "--service-name", r.Name, "--operator-sid", r.OperatorSID, "--scm"}
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var operatorSID = regexp.MustCompile(`^S-1-5-21-[0-9]+-[0-9]+-[0-9]+-[0-9]+$`)

func (r Registration) Validate() error {
	if !serviceName.MatchString(r.Name) || !operatorSID.MatchString(r.OperatorSID) {
		return errors.New("invalid service name or operator SID")
	}
	for _, p := range []string{r.Executable, r.ConfigPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsAny(p, "\x00\r\n\"") {
			return errors.New("service paths must be clean absolute paths")
		}
	}
	return nil
}

// Configuration is the complete policy checked before any existing-service mutation.
type Configuration struct {
	Registration                                                                                      Registration
	Account                                                                                           string
	DemandStart, OwnProcess, RestrictedSID, MinimalPrivileges, NoRecovery, NoDependencies, NoTriggers bool
}

func ExpectedConfiguration(r Registration) Configuration {
	return Configuration{
		Registration: r, Account: r.Account(), DemandStart: true, OwnProcess: true,
		RestrictedSID: true, MinimalPrivileges: true, NoRecovery: true,
		NoDependencies: true, NoTriggers: true,
	}
}

type State uint32

const (
	Stopped      State = 1
	StartPending State = 2
	StopPending  State = 3
	Running      State = 4
)

type Status struct {
	State                                  State
	Win32ExitCode, ServiceSpecificExitCode uint32
}

var ErrNotFound = errors.New("service registration not found")

// Backend owns SCM handles only, never application files or permissions.
type Backend interface {
	Open(string, Action) (ManagedService, error)
	Create(Registration) (ManagedService, error)
}
type ManagedService interface {
	Configuration() (Configuration, error)
	ConfigurePolicy() error
	Query() (Status, error)
	Start() error
	Stop() error
	Delete() error
	Close() error
}

// Manage validates the caller's complete expected registration before controlling it.
// SCM state, rather than acceptance of a control request, determines completion.
func Manage(ctx context.Context, b Backend, action Action, r Registration) (result Status, err error) {
	if err = r.Validate(); err != nil {
		return
	}
	switch action {
	case Install, Check, Start, Stop, Uninstall:
	default:
		return result, errors.New("unknown service operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return
	}
	var h ManagedService
	if action == Install {
		h, err = b.Create(r)
		if err != nil {
			return
		}
		defer func() { err = errors.Join(err, h.Close()) }()
		if err = h.ConfigurePolicy(); err == nil {
			var c Configuration
			c, err = h.Configuration()
			if err == nil && c != ExpectedConfiguration(r) {
				err = errors.New("created registration policy mismatch")
			}
		}
		if err != nil {
			if rollback := h.Delete(); rollback != nil {
				err = fmt.Errorf("installation incomplete; rollback failed: %w", errors.Join(err, rollback))
			}
			return
		}
		result, err = h.Query()
		if err != nil {
			err = fmt.Errorf("registration created; status could not be confirmed: %w", err)
		} else if result.State != Stopped {
			err = errors.New("registration created but is not stopped")
		}
		return
	}
	h, err = b.Open(r.Name, action)
	if err != nil {
		return
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, h.Close())
		}
	}()
	c, e := h.Configuration()
	if e != nil {
		return result, e
	}
	if c != ExpectedConfiguration(r) {
		return result, errors.New("refusing mismatched service registration or policy")
	}
	result, err = h.Query()
	if err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		return
	}
	switch action {
	case Check:
		return
	case Start:
		switch result.State {
		case Running:
			return
		case Stopped:
			if err = h.Start(); err != nil {
				return
			}
		case StartPending:
		default:
			return result, errors.New("service is not stopped or starting")
		}
		return awaitState(ctx, h, Running)
	case Stop:
		switch result.State {
		case Stopped:
			return result, statusError(result)
		case Running:
			if err = h.Stop(); err != nil {
				return
			}
		case StopPending:
		default:
			return result, errors.New("service is not running or stopping")
		}
		return awaitState(ctx, h, Stopped)
	case Uninstall:
		if result.State != Stopped {
			return result, errors.New("service must be stopped before uninstall")
		}
		if err = h.Delete(); err != nil {
			return
		}
		err = h.Close()
		closed = true
		if err != nil {
			return
		}
		for {
			var probe ManagedService
			probe, err = b.Open(r.Name, Check)
			if errors.Is(err, ErrNotFound) {
				err = nil
				return
			}
			if err != nil {
				return result, fmt.Errorf("deletion requested; absence not confirmed: %w", err)
			}
			if e = probe.Close(); e != nil {
				return result, e
			}
			if err = poll(ctx); err != nil {
				return result, fmt.Errorf("deletion requested; absence not confirmed: %w", err)
			}
		}
	}
	return
}
func statusError(s Status) error {
	if s.Win32ExitCode != 0 || s.ServiceSpecificExitCode != 0 {
		return fmt.Errorf("service stopped with Win32 exit %d, service-specific exit %d", s.Win32ExitCode, s.ServiceSpecificExitCode)
	}
	return nil
}
func awaitState(ctx context.Context, h ManagedService, target State) (Status, error) {
	for {
		if err := ctx.Err(); err != nil {
			return Status{}, err
		}
		s, err := h.Query()
		if err != nil {
			return s, err
		}
		if s.State == target {
			return s, statusError(s)
		}
		if target == Running && s.State == Stopped {
			if err = statusError(s); err == nil {
				err = errors.New("service stopped before reaching running")
			}
			return s, err
		}
		if err = poll(ctx); err != nil {
			return s, err
		}
	}
}
func poll(ctx context.Context) error {
	t := time.NewTimer(100 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
