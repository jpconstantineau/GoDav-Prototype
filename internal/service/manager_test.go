package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeSCM struct {
	h              *fakeService
	exists         bool
	creates, opens int
	openErr        error
}

func (b *fakeSCM) Create(r Registration) (ManagedService, error) {
	b.creates++
	if b.exists {
		return nil, errors.New("exists")
	}
	b.exists = true
	b.h.config = ExpectedConfiguration(r)
	return b.h, nil
}
func (b *fakeSCM) Open(_ string, _ Action) (ManagedService, error) {
	b.opens++
	if b.openErr != nil {
		return nil, b.openErr
	}
	if !b.exists {
		return nil, ErrNotFound
	}
	return b.h, nil
}

type fakeService struct {
	config                                  Configuration
	states                                  []Status
	starts, stops, deletes, closes, queries int
	policyErr, deleteErr, queryErr          error
	deleted                                 func()
}

func (h *fakeService) Configuration() (Configuration, error) { return h.config, nil }
func (h *fakeService) ConfigurePolicy() error                { return h.policyErr }
func (h *fakeService) Query() (Status, error) {
	h.queries++
	if h.queryErr != nil {
		return Status{}, h.queryErr
	}
	s := h.states[0]
	if len(h.states) > 1 {
		h.states = h.states[1:]
	}
	return s, nil
}
func (h *fakeService) Start() error { h.starts++; return nil }
func (h *fakeService) Stop() error  { h.stops++; return nil }
func (h *fakeService) Delete() error {
	h.deletes++
	if h.deleteErr == nil && h.deleted != nil {
		h.deleted()
	}
	return h.deleteErr
}
func (h *fakeService) Close() error { h.closes++; return nil }
func fixture(t *testing.T) (Registration, *fakeSCM, *fakeService) {
	t.Helper()
	dir := t.TempDir()
	r := Registration{"GoDav-test", filepath.Join(dir, "godav.exe"), filepath.Join(dir, "config.yaml"), "S-1-5-21-1-2-3-1001"}
	h := &fakeService{config: ExpectedConfiguration(r), states: []Status{{State: Stopped}}}
	b := &fakeSCM{h: h, exists: true}
	h.deleted = func() { b.exists = false }
	return r, b, h
}
func TestManagerWaitsForObservedRunningAndStopped(t *testing.T) {
	for _, action := range []Action{Start, Stop} {
		t.Run(string(action), func(t *testing.T) {
			r, b, h := fixture(t)
			initial, pending, target := Stopped, StartPending, Running
			if action == Stop {
				initial, pending, target = Running, StopPending, Stopped
			}
			h.states = []Status{{State: initial}, {State: pending}, {State: target}}
			s, err := Manage(context.Background(), b, action, r)
			if err != nil || s.State != target || h.queries != 3 {
				t.Fatalf("status=%+v err=%v queries=%d", s, err, h.queries)
			}
			if h.starts+h.stops != 1 {
				t.Fatal("control not issued exactly once")
			}
		})
	}
}
func TestManagerRefusesModifiedRegistrationsBeforeControls(t *testing.T) {
	mutations := []func(*Configuration){func(c *Configuration) { c.Registration.Executable += ".other" },
		func(c *Configuration) { c.Registration.ConfigPath += ".other" },
		func(c *Configuration) { c.Registration.OperatorSID = "S-1-5-21-4-5-6-1001" },
		func(c *Configuration) { c.Account = "LocalSystem" },
		func(c *Configuration) { c.DemandStart = false },
		func(c *Configuration) { c.OwnProcess = false },
		func(c *Configuration) { c.RestrictedSID = false },
		func(c *Configuration) { c.MinimalPrivileges = false },
		func(c *Configuration) { c.NoRecovery = false },
		func(c *Configuration) { c.NoDependencies = false },
		func(c *Configuration) { c.NoTriggers = false }}
	for _, action := range []Action{Check, Start, Stop, Uninstall} {
		for i, mutate := range mutations {
			r, b, h := fixture(t)
			mutate(&h.config)
			if _, err := Manage(context.Background(), b, action, r); err == nil {
				t.Fatalf("%s mutation %d accepted", action, i)
			}
			if h.starts+h.stops+h.deletes+h.queries != 0 {
				t.Fatal("mismatch crossed mutation/status boundary")
			}
		}
	}
}
func TestManagerRepeatedControlsAreStateAware(t *testing.T) {
	for _, action := range []Action{Start, Stop} {
		r, b, h := fixture(t)
		target := Running
		if action == Stop {
			target = Stopped
		}
		h.states = []Status{{State: target}}
		for range 2 {
			if _, err := Manage(context.Background(), b, action, r); err != nil {
				t.Fatal(err)
			}
		}
		if h.starts+h.stops != 0 {
			t.Fatal("repeated control")
		}
	}
}
func TestManagerAlreadyPendingDoesNotRepeatControl(t *testing.T) {
	for _, action := range []Action{Start, Stop} {
		r, b, h := fixture(t)
		pending, target := StartPending, Running
		if action == Stop {
			pending, target = StopPending, Stopped
		}
		h.states = []Status{{State: pending}, {State: target}}
		if _, err := Manage(context.Background(), b, action, r); err != nil {
			t.Fatal(err)
		}
		if h.starts+h.stops != 0 {
			t.Fatal("pending control repeated")
		}
	}
}
func TestManagerReportsStartupFailure(t *testing.T) {
	r, b, h := fixture(t)
	h.states = []Status{{State: Stopped}, {State: Stopped, Win32ExitCode: 1066, ServiceSpecificExitCode: 1}}
	s, err := Manage(context.Background(), b, Start, r)
	if err == nil || s.Win32ExitCode != 1066 || s.ServiceSpecificExitCode != 1 {
		t.Fatalf("lost exit status: %+v %v", s, err)
	}
}
func TestManagerDeadlineIsNotSuccess(t *testing.T) {
	r, b, h := fixture(t)
	h.states = []Status{{State: StartPending}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := Manage(ctx, b, Start, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if h.starts+h.stops+h.deletes != 0 {
		t.Fatal("deadline mutated service")
	}
}
func TestManagerUninstallRequiresStoppedAndConfirmsAbsence(t *testing.T) {
	r, b, h := fixture(t)
	h.states = []Status{{State: Running}}
	if _, err := Manage(context.Background(), b, Uninstall, r); err == nil || h.deletes != 0 {
		t.Fatal("deleted running service")
	}
	h.states = []Status{{State: Stopped}}
	if _, err := Manage(context.Background(), b, Uninstall, r); err != nil {
		t.Fatal(err)
	}
	if b.exists || h.deletes != 1 || h.stops != 0 {
		t.Fatal("bad unregister lifecycle")
	}
}
func TestManagerUninstallReportsUnconfirmedDeletion(t *testing.T) {
	r, b, h := fixture(t)
	h.deleted = func() {}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := Manage(ctx, b, Uninstall, r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}
func TestManagerInstallOnlyRegistersAndRollsBackPolicyFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		r, b, h := fixture(t)
		b.exists = false
		if failure {
			h.policyErr = errors.New("policy failure")
		}
		_, err := Manage(context.Background(), b, Install, r)
		if (err != nil) != failure {
			t.Fatal(err)
		}
		if h.starts+h.stops != 0 {
			t.Fatal("install controls service")
		}
		if failure && (h.deletes != 1 || b.exists) {
			t.Fatal("rollback missing")
		}
	}
}
func TestManagerReportsFailedInstallRollback(t *testing.T) {
	r, b, h := fixture(t)
	b.exists = false
	h.policyErr = errors.New("policy failure")
	h.deleteErr = errors.New("rollback failed")
	_, err := Manage(context.Background(), b, Install, r)
	if !errors.Is(err, h.policyErr) || !errors.Is(err, h.deleteErr) {
		t.Fatalf("lost partial failure %v", err)
	}
	if !b.exists {
		t.Fatal("claimed rollback")
	}
}
func TestManagerDoesNotReplaceExistingInstallation(t *testing.T) {
	r, b, h := fixture(t)
	if _, err := Manage(context.Background(), b, Install, r); err == nil {
		t.Fatal("replaced existing")
	}
	if h.deletes+h.starts+h.stops != 0 {
		t.Fatal("existing mutated")
	}
}
func TestManagerRejectsInvalidRequestWithoutSCM(t *testing.T) {
	r, b, _ := fixture(t)
	r.OperatorSID = "S-1-5-18"
	if _, err := Manage(context.Background(), b, Start, r); err == nil || b.opens+b.creates != 0 {
		t.Fatal("unsafe request crossed SCM boundary")
	}
}

func TestRegistrationAccountAndArguments(t *testing.T) {
	r := Registration{Name: "GoDav-private", Executable: `C:\Program Files\GoDav\godav.exe`, ConfigPath: `C:\GoDav data\config.yaml`, OperatorSID: "S-1-5-21-1-2-3-1001"}
	if r.Account() != `NT SERVICE\GoDav-private` {
		t.Fatal(r.Account())
	}
	want := []string{`C:\Program Files\GoDav\godav.exe`, "run", "server", "--config", `C:\GoDav data\config.yaml`, "--service-name", "GoDav-private", "--operator-sid", "S-1-5-21-1-2-3-1001", "--scm"}
	got := r.Arguments()
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argument %d: %q", i, got[i])
		}
	}
}
func TestManagerCancelledBeforeOpening(t *testing.T) {
	r, b, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Manage(ctx, b, Start, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if b.opens+b.creates != 0 {
		t.Fatal("cancelled request opened SCM")
	}
}
func TestManagerInstallStatusFailureReportsExistingRegistration(t *testing.T) {
	r, b, h := fixture(t)
	b.exists = false
	h.queryErr = errors.New("query failed")
	_, err := Manage(context.Background(), b, Install, r)
	if err == nil || !b.exists || h.deletes != 0 {
		t.Fatalf("status failure mishandled: %v", err)
	}
}
