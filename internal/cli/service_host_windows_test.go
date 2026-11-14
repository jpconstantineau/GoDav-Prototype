package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
	"golang.org/x/sys/windows/svc"
)

type serviceOutcome struct {
	specific bool
	code     uint32
}
type fakeSCM struct {
	controls chan svc.ChangeRequest
	statuses chan svc.Status
	done     chan serviceOutcome
	released chan struct{}
}

// This executes the real HTTPS/SQLite lifecycle behind the SCM protocol seam.
// Channels are a fake SCM, not proof of registration, logon, or native dispatch.
func startServiceHarness(t *testing.T, config string) *fakeSCM {
	t.Helper()
	scm := &fakeSCM{make(chan svc.ChangeRequest), make(chan svc.Status), make(chan serviceOutcome, 1), make(chan struct{})}
	abort, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	h := service.Handler{Run: func(ctx context.Context, observer server.Observer) error {
		ctx, stop := context.WithCancel(ctx)
		cleanup := context.AfterFunc(abort, stop)
		defer cleanup()
		defer stop()
		err := server.RunObserved(ctx, config, server.Identity{}, io.Discard, observer)
		close(scm.released)
		return err
	}}
	go func() {
		defer close(finished)
		specific, code := h.Execute([]string{"FixtureService"}, scm.controls, scm.statuses)
		scm.done <- serviceOutcome{specific, code}
	}()
	t.Cleanup(func() {
		cancel()
		close(scm.controls)
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-scm.statuses:
			case <-finished:
				return
			case <-timer.C:
				t.Error("service fixture did not join")
				return
			}
		}
	})
	return scm
}
func (s *fakeSCM) next(t *testing.T, want svc.State) svc.Status {
	t.Helper()
	select {
	case got := <-s.statuses:
		if got.State != want {
			t.Fatalf("SCM state=%v want=%v", got.State, want)
		}
		return got
	case <-s.done:
		t.Fatal("service exited before expected status")
	case <-time.After(8 * time.Second):
		t.Fatal("SCM status timeout")
	}
	return svc.Status{}
}
func (s *fakeSCM) send(t *testing.T, command svc.Cmd) {
	t.Helper()
	select {
	case s.controls <- svc.ChangeRequest{Cmd: command}:
	case <-s.done:
		t.Fatal("service exited before control request")
	case <-time.After(3 * time.Second):
		t.Fatal("SCM control timeout")
	}
}
func (s *fakeSCM) result(t *testing.T, specific bool, code uint32) {
	t.Helper()
	select {
	case got := <-s.done:
		if got.specific != specific || got.code != code {
			t.Fatalf("SCM exit=%v/%d want=%v/%d", got.specific, got.code, specific, code)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("SCM completion timeout")
	}
}

func TestServiceHostStopDrainsBeforeCompletion(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	scm := startServiceHarness(t, f.config)
	scm.next(t, svc.StartPending)
	running := scm.next(t, svc.Running)
	if running.Accepts != svc.AcceptStop|svc.AcceptShutdown {
		t.Fatal("missing lifecycle controls")
	}
	client := fixtureClient(f)
	client.Timeout = 15 * time.Second
	defer client.CloseIdleConnections()
	body, upload := finishingUpload(t, client, f, "/dav/service-drained")
	scm.send(t, svc.Stop)
	scm.next(t, svc.StopPending)
	// The second checkpoint corresponds to real admission closure.
	scm.next(t, svc.StopPending)
	select {
	case <-scm.done:
		t.Fatal("reported stopped with an active upload")
	default:
	}
	if err := body.Close(); err != nil {
		t.Fatal("upload completion failed")
	}
	select {
	case code := <-upload:
		if code != 201 {
			t.Fatal("service stop did not drain upload")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drained upload did not finish")
	}
	scm.result(t, false, 0)
	restarted := startServer(t, bin, f)
	defer restarted.stop()
	code, _, data := fileRequest(t, client, f, "GET", "/dav/service-drained", nil, nil)
	if code != 200 || string(data) != "complete" {
		t.Fatal("service stop lost committed data")
	}
}

func TestServiceHostStartupFailureNeverReportsRunning(t *testing.T) {
	scm := startServiceHarness(t, filepath.Join(t.TempDir(), "missing.yaml"))
	scm.next(t, svc.StartPending)
	select {
	case got := <-scm.statuses:
		t.Fatalf("failed startup published state %v", got.State)
	case result := <-scm.done:
		if !result.specific || result.code != 1 {
			t.Fatal("startup failure exit mapping incorrect")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("startup failure did not finish")
	}
}

func TestServiceHostShutdownDeadlinePreservesCommittedState(t *testing.T) {
	bin := development(t)
	f := newServerFixture(t, bin)
	settings, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal("fixture settings read failed")
	}
	settings = append(settings, []byte("  shutdown_grace_ms: 200\n")...)
	if err := os.WriteFile(f.config, settings, 0600); err != nil {
		t.Fatal("fixture settings write failed")
	}
	scm := startServiceHarness(t, f.config)
	scm.next(t, svc.StartPending)
	scm.next(t, svc.Running)
	client := fixtureClient(f)
	defer client.CloseIdleConnections()
	code, _, _ := fileRequest(t, client, f, "PUT", "/dav/existing", bytes.NewBufferString("prior"), nil)
	if code != 201 {
		t.Fatal("seed upload failed")
	}
	out, stderr, exit := command(t, t.TempDir(), bin, "run", "server", "--config", f.config)
	if exit != 1 || out != "" || stderr != "godav: server startup failed\n" {
		t.Fatal("foreground overlapped service ownership")
	}
	scm.send(t, svc.Pause)
	scm.send(t, svc.Interrogate)
	if state := scm.next(t, svc.Running); state.CheckPoint != 0 {
		t.Fatal("interrogate invented progress")
	}
	body, upload := finishingUpload(t, client, f, "/dav/existing")
	scm.send(t, svc.Shutdown)
	scm.next(t, svc.StopPending)
	scm.next(t, svc.StopPending)
	scm.send(t, svc.Stop)
	scm.send(t, svc.Interrogate)
	if state := scm.next(t, svc.StopPending); state.CheckPoint != 2 {
		t.Fatal("repeated stop reset progress")
	}
	scm.result(t, true, 3)
	body.CloseWithError(io.ErrClosedPipe)
	select {
	case status := <-upload:
		if status >= 200 && status < 300 {
			t.Fatal("unfinished replacement succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled upload did not join")
	}
	restarted := startServer(t, bin, f)
	defer restarted.stop()
	code, _, data := fileRequest(t, client, f, "GET", "/dav/existing", nil, nil)
	if code != 200 || string(data) != "prior" {
		t.Fatal("cancelled service overwrite damaged committed data")
	}
}

func TestServiceCommandsRemainBlockedWithoutProvisioningContract(t *testing.T) {
	bin := development(t)
	for _, action := range []string{"install", "check", "start", "stop", "uninstall"} {
		dir := t.TempDir()
		args := []string{action, "service", "--name", "FixtureService"}
		if action == "install" {
			args = append(args, "--config", filepath.Join(dir, "missing.yaml"), "--account", `EXAMPLE\FixtureAccount`)
		}
		out, stderr, code := command(t, dir, bin, args...)
		if code != 1 || out != "" || stderr != "godav: service operation unavailable; identity and provisioning contract pending\n" {
			t.Fatal("unapproved service operation was not clearly blocked")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatal("blocked service command touched data")
		}
		if strings.Contains(stderr, "FixtureAccount") {
			t.Fatal("service error echoed account")
		}
	}
}

func TestServiceHostIdleStopOrdersPendingBeforeCompletion(t *testing.T) {
	f := newServerFixture(t, development(t))
	for i := 0; i < 16; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			scm := startServiceHarness(t, f.config)
			scm.next(t, svc.StartPending)
			scm.next(t, svc.Running)
			scm.send(t, svc.Stop)
			// Hold the handler on its first status send while the real runner
			// completely releases its DB and queues all lifecycle transitions.
			select {
			case <-scm.released:
			case <-time.After(8 * time.Second):
				t.Fatal("idle server did not release state")
			}
			scm.next(t, svc.StopPending)
			if got := scm.next(t, svc.StopPending); got.CheckPoint != 2 {
				t.Fatal("admission checkpoint was lost")
			}
			scm.result(t, false, 0)
		})
	}
}
