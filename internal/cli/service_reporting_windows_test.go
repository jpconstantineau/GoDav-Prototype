package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
)

// This seam uses the real manager and CLI outcome reporting, but never opens SCM,
// verifies real accounts, or changes filesystem permissions.
type reportingBackend struct {
	h      *reportingService
	exists bool
}

func (b *reportingBackend) Create(r service.Registration) (service.ManagedService, error) {
	if b.exists {
		return nil, errors.New("already exists")
	}
	b.exists = true
	b.h.config = service.ExpectedConfiguration(r)
	return b.h, nil
}
func (b *reportingBackend) Open(string, service.Action) (service.ManagedService, error) {
	if !b.exists {
		return nil, service.ErrNotFound
	}
	return b.h, nil
}

type reportingService struct {
	config                 service.Configuration
	statuses               []service.Status
	starts, stops, deletes int
	closeErr               error
	deleted                func()
}

func (h *reportingService) Configuration() (service.Configuration, error) { return h.config, nil }
func (h *reportingService) ConfigurePolicy() error                        { return nil }
func (h *reportingService) Query() (service.Status, error) {
	s := h.statuses[0]
	if len(h.statuses) > 1 {
		h.statuses = h.statuses[1:]
	}
	return s, nil
}
func (h *reportingService) Start() error  { h.starts++; return nil }
func (h *reportingService) Stop() error   { h.stops++; return nil }
func (h *reportingService) Delete() error { h.deletes++; h.deleted(); return nil }
func (h *reportingService) Close() error  { return h.closeErr }

func reportingFixture(t *testing.T) (service.Registration, *reportingBackend, *reportingService) {
	t.Helper()
	dir := t.TempDir()
	r := service.Registration{Name: "SyntheticReportingService", Executable: filepath.Join(dir, "godav.exe"), ConfigPath: filepath.Join(dir, "config.yaml"), OperatorSID: "S-1-5-21-1-2-3-1001"}
	h := &reportingService{config: service.ExpectedConfiguration(r), statuses: []service.Status{{State: service.Stopped, Win32ExitCode: 1077}}}
	b := &reportingBackend{h: h, exists: true}
	h.deleted = func() { b.exists = false }
	return r, b, h
}

func TestServiceReportingInstallNeverStartedSucceeds(t *testing.T) {
	r, b, h := reportingFixture(t)
	b.exists = false
	status, err := service.Manage(context.Background(), b, service.Install, r)
	var stdout, stderr bytes.Buffer
	code := reportServiceResult(service.Install, status, err, &stdout, &stderr)
	if code != 0 || stdout.String() != "service: stopped\nwin32-exit: 1077\nservice-exit: 0\n" || stderr.Len() != 0 {
		t.Fatalf("install exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !b.exists || h.starts+h.stops+h.deletes != 0 {
		t.Fatal("install did more than create a stopped registration")
	}
}

func TestServiceReportingCheckNeverStartedSucceeds(t *testing.T) {
	r, b, h := reportingFixture(t)
	status, err := service.Manage(context.Background(), b, service.Check, r)
	var stdout, stderr bytes.Buffer
	code := reportServiceResult(service.Check, status, err, &stdout, &stderr)
	if code != 0 || stdout.String() != "service: stopped\nwin32-exit: 1077\nservice-exit: 0\n" || stderr.Len() != 0 {
		t.Fatalf("check exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !b.exists || h.starts+h.stops+h.deletes != 0 {
		t.Fatal("check changed the installed registration")
	}
}

func TestServiceReportingNeverStartedExceptionIsNarrow(t *testing.T) {
	for _, action := range []service.Action{service.Install, service.Check} {
		for _, tc := range []struct {
			name   string
			status service.Status
		}{
			{"access-denied", service.Status{State: service.Stopped, Win32ExitCode: 5}},
			{"service-failure", service.Status{State: service.Stopped, Win32ExitCode: 1066, ServiceSpecificExitCode: 1}},
			{"service-code-alone", service.Status{State: service.Stopped, ServiceSpecificExitCode: 1}},
			{"never-started-with-service-error", service.Status{State: service.Stopped, Win32ExitCode: 1077, ServiceSpecificExitCode: 1}},
			{"never-started-running", service.Status{State: service.Running, Win32ExitCode: 1077}},
			{"never-started-start-pending", service.Status{State: service.StartPending, Win32ExitCode: 1077}},
			{"never-started-stop-pending", service.Status{State: service.StopPending, Win32ExitCode: 1077}},
			{"never-started-unknown-state", service.Status{State: 99, Win32ExitCode: 1077}},
		} {
			t.Run(string(action)+"/"+tc.name, func(t *testing.T) {
				r, b, h := reportingFixture(t)
				b.exists = action != service.Install
				h.statuses = []service.Status{tc.status}
				status, err := service.Manage(context.Background(), b, action, r)
				var stdout, stderr bytes.Buffer
				if code := reportServiceResult(action, status, err, &stdout, &stderr); code != 1 {
					t.Fatalf("failure accepted: exit=%d status=%+v err=%v", code, status, err)
				}
				if !b.exists || h.starts+h.stops+h.deletes != 0 {
					t.Fatal("status reporting mutated the registration")
				}
			})
		}
	}
}

func TestServiceReportingNeverStartedDoesNotHideManagerError(t *testing.T) {
	for _, action := range []service.Action{service.Install, service.Check} {
		t.Run(string(action), func(t *testing.T) {
			r, b, h := reportingFixture(t)
			b.exists = action != service.Install
			h.closeErr = errors.New("synthetic handle close failed")
			status, err := service.Manage(context.Background(), b, action, r)
			if !errors.Is(err, h.closeErr) {
				t.Fatalf("lost manager error: %v", err)
			}
			var stdout, stderr bytes.Buffer
			code := reportServiceResult(action, status, err, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || stderr.String() != "godav: service operation failed: synthetic handle close failed\n" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestServiceReportingStartRequiresObservedSuccessfulRunning(t *testing.T) {
	for _, tc := range []struct {
		name         string
		initial      service.State
		final        service.Status
		code, starts int
	}{
		{"first-start-succeeds", service.Stopped, service.Status{State: service.Running}, 0, 1},
		{"start-returns-never-started", service.Stopped, service.Status{State: service.Stopped, Win32ExitCode: 1077}, 1, 1},
		{"pending-start-returns-never-started", service.StartPending, service.Status{State: service.Stopped, Win32ExitCode: 1077}, 1, 0},
		{"running-retains-never-started", service.Stopped, service.Status{State: service.Running, Win32ExitCode: 1077}, 1, 1},
		{"start-fails", service.Stopped, service.Status{State: service.Stopped, Win32ExitCode: 1066, ServiceSpecificExitCode: 1}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, h := reportingFixture(t)
			h.statuses = []service.Status{{State: tc.initial, Win32ExitCode: 1077}, tc.final}
			status, err := service.Manage(context.Background(), b, service.Start, r)
			var stdout, stderr bytes.Buffer
			code := reportServiceResult(service.Start, status, err, &stdout, &stderr)
			if code != tc.code || h.starts != tc.starts || h.stops+h.deletes != 0 {
				t.Fatalf("exit=%d starts=%d status=%+v err=%v", code, h.starts, status, err)
			}
			if tc.code == 0 && (stdout.String() != "service: running\nwin32-exit: 0\nservice-exit: 0\n" || stderr.Len() != 0) {
				t.Fatalf("success status lost: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if tc.code != 0 && (err == nil || stderr.Len() == 0) {
				t.Fatal("start failure did not retain the manager diagnostic")
			}
		})
	}
}

func TestServiceReportingStopKeepsCleanlyStoppedContract(t *testing.T) {
	for _, tc := range []struct {
		name        string
		statuses    []service.Status
		code, stops int
	}{
		{"cleanly-stopped", []service.Status{{State: service.Stopped}}, 0, 0},
		{"never-started-is-not-clean-stop", []service.Status{{State: service.Stopped, Win32ExitCode: 1077}}, 1, 0},
		{"stop-returns-never-started", []service.Status{{State: service.Running}, {State: service.Stopped, Win32ExitCode: 1077}}, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, b, h := reportingFixture(t)
			h.statuses = tc.statuses
			status, err := service.Manage(context.Background(), b, service.Stop, r)
			var stdout, stderr bytes.Buffer
			if code := reportServiceResult(service.Stop, status, err, &stdout, &stderr); code != tc.code || h.stops != tc.stops || h.starts+h.deletes != 0 {
				t.Fatalf("exit=%d stops=%d status=%+v err=%v", code, h.stops, status, err)
			}
		})
	}
}

func TestServiceReportingUninstallNeverStartedRequiresConfirmedAbsence(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "unconfirmed"}[confirmed], func(t *testing.T) {
			r, b, h := reportingFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !confirmed {
				h.deleted = cancel
			}
			status, err := service.Manage(ctx, b, service.Uninstall, r)
			var stdout, stderr bytes.Buffer
			code := reportServiceResult(service.Uninstall, status, err, &stdout, &stderr)
			if confirmed {
				if code != 0 || b.exists || stdout.String() != "service: not-installed\n" || stderr.Len() != 0 {
					t.Fatalf("confirmed removal: exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
				}
			} else if code != 1 || !b.exists || stdout.Len() != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("unconfirmed removal: exit=%d stdout=%q err=%v", code, stdout.String(), err)
			}
			if h.deletes != 1 || h.starts+h.stops != 0 {
				t.Fatal("uninstall changed control behavior")
			}
		})
	}
}
