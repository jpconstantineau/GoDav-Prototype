package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/server"
	"github.com/jpconstantineau/GoDav-Prototype/internal/service"
	"golang.org/x/sys/windows/svc"
)

// Handler.Execute is the public SCM protocol seam. Buffered channels stand in
// for SCM; this test does not register, start, stop or alter a native service.
func TestHandlerReportsDistinctLifecycleFailureCodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		result   error
		specific bool
		code     uint32
	}{
		{"success", nil, false, 0},
		{"startup", errors.New("fixture startup failure"), true, 1},
		{"serving", server.ErrServing, true, 2},
		{"wrapped-serving", fmt.Errorf("fixture: %w", server.ErrServing), true, 2},
		{"shutdown-deadline", server.ErrShutdownDeadline, true, 3},
		{"shutdown-failed", server.ErrShutdownFailed, true, 4},
		{"deadline-before-serving", errors.Join(server.ErrServing, server.ErrShutdownDeadline), true, 3},
		{"shutdown-failure-before-deadline", errors.Join(server.ErrShutdownDeadline, server.ErrShutdownFailed), true, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := service.Handler{Run: func(context.Context, server.Observer) error { calls++; return tc.result }}
			statuses := make(chan svc.Status, 8)
			specific, code := handler.Execute([]string{"FixtureService"}, make(chan svc.ChangeRequest), statuses)
			if calls != 1 || specific != tc.specific || code != tc.code {
				t.Fatalf("SCM result specific=%v code=%d calls=%d; want specific=%v code=%d calls=1", specific, code, calls, tc.specific, tc.code)
			}
			if len(statuses) != 1 || (<-statuses).State != svc.StartPending {
				t.Fatal("terminal result invented a running or stopped status")
			}
		})
	}
}
