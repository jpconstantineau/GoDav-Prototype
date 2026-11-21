package mcpindexadmin

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
)

type fakeController struct {
	calls  [][]string
	result indexing.Status
	err    error
}

func (f *fakeController) capture(op, owner, id string) (indexing.Status, error) {
	f.calls = append(f.calls, []string{op, owner, id})
	return f.result, f.err
}
func (f *fakeController) Start(_ context.Context, owner string) (indexing.Status, error) {
	return f.capture("start", owner, "")
}
func (f *fakeController) Status(_ context.Context, owner, id string) (indexing.Status, error) {
	return f.capture("status", owner, id)
}
func (f *fakeController) Pause(_ context.Context, owner, id string) (indexing.Status, error) {
	return f.capture("pause", owner, id)
}
func (f *fakeController) Resume(_ context.Context, owner, id string) (indexing.Status, error) {
	return f.capture("resume", owner, id)
}
func (f *fakeController) Cancel(_ context.Context, owner, id string) (indexing.Status, error) {
	return f.capture("cancel", owner, id)
}

func TestAdminOperationsUseVerifiedPrincipalAndCurrentJob(t *testing.T) {
	f := &fakeController{result: indexing.Status{ID: "0123456789abcdef0123456789abcdef", State: "paused", Total: 7, Processed: 3, Indexed: 2, Skipped: 1, Backlog: 4, PauseReason: "manual"}}
	tool, err := New(f, nil)
	if err != nil || tool.Name != ToolName || tool.InputSchema["additionalProperties"] != false {
		t.Fatalf("bad admin descriptor: %v", err)
	}
	owner := auth.WithPrincipal(t.Context(), "verified-account-id")
	for _, tc := range []struct{ args, operation, job string }{
		{`{"operation":"status"}`, "status", ""},
		{`{"operation":"status","job_id":"0123456789abcdef0123456789abcdef"}`, "status", "0123456789abcdef0123456789abcdef"},
		{`{"operation":"start"}`, "start", ""},
		{`{"operation":"pause","job_id":"0123456789abcdef0123456789abcdef"}`, "pause", "0123456789abcdef0123456789abcdef"},
		{`{"operation":"resume","job_id":"0123456789abcdef0123456789abcdef"}`, "resume", "0123456789abcdef0123456789abcdef"},
		{`{"operation":"cancel","job_id":"0123456789abcdef0123456789abcdef"}`, "cancel", "0123456789abcdef0123456789abcdef"},
	} {
		before := len(f.calls)
		got, err := tool.Call(owner, json.RawMessage(tc.args))
		if len(f.calls) != before+1 {
			t.Fatalf("%s dispatched %d commands, want one: %v", tc.operation, len(f.calls)-before, err)
		}
		last := f.calls[len(f.calls)-1]
		if err != nil || !reflect.DeepEqual(last, []string{tc.operation, "verified-account-id", tc.job}) {
			t.Fatalf("wrong command %v, error %v", last, err)
		}
		b, err := json.Marshal(got)
		if err != nil || string(b) != `{"job_id":"0123456789abcdef0123456789abcdef","state":"paused","total":7,"processed":3,"indexed":2,"skipped":1,"backlog":4,"pause_reason":"manual"}` {
			t.Fatalf("wrong status projection: %s, %v", b, err)
		}
	}
}

func TestAdminRejectsSelectorsAndUnsafeControlInputBeforeDispatch(t *testing.T) {
	f := &fakeController{}
	up := true
	tool, err := New(f, func() bool { return up })
	if err != nil {
		t.Fatal(err)
	}
	owner := auth.WithPrincipal(t.Context(), "verified-account-id")
	for _, args := range []string{
		`null`, `[]`, `{}`, `{"operation":null}`, `{"operation":"rebuild_all"}`,
		`{"operation":"status","owner_id":"claimed"}`, `{"operation":"status","collection":"Outside"}`,
		`{"operation":"start","job_id":"0123456789abcdef0123456789abcdef"}`, `{"operation":"start","path":"Outside"}`,
		`{"operation":"pause"}`, `{"operation":"resume","job_id":null}`,
		`{"operation":"cancel","job_id":"../outside"}`, `{"operation":"cancel","job_id":""}`,
	} {
		if result, err := tool.Call(owner, json.RawMessage(args)); !errors.Is(err, errInvalid) || result != nil {
			t.Fatalf("invalid command accepted: %s", args)
		}
	}
	ctx, cancel := context.WithCancel(owner)
	cancel()
	for _, ctx := range []context.Context{t.Context(), ctx} {
		if result, err := tool.Call(ctx, json.RawMessage(`{"operation":"start"}`)); !errors.Is(err, errUnavailable) || result != nil {
			t.Fatal("unverified or canceled command reached the controller")
		}
	}
	up = false
	if result, err := tool.Call(owner, json.RawMessage(`{"operation":"start"}`)); !errors.Is(err, errUnavailable) || result != nil {
		t.Fatal("unavailable worker accepted a new job")
	}
	if len(f.calls) != 0 {
		t.Fatalf("%d calls were made despite invalid inputs", len(f.calls))
	}
}

func TestAdminSanitizesRuntimeStateAndFailure(t *testing.T) {
	f := &fakeController{result: indexing.Status{ID: "0123456789abcdef0123456789abcdef", State: "failed", PauseReason: "directory SECRET", ErrorCode: "raw database SECRET"}}
	tool, err := New(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := auth.WithPrincipal(t.Context(), "verified-account-id")
	args := json.RawMessage(`{"operation":"status","job_id":"0123456789abcdef0123456789abcdef"}`)
	got, err := tool.Call(owner, args)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil || strings.Contains(string(b), "SECRET") || !strings.Contains(string(b), `"error_code":"indexing_failed"`) {
		t.Fatalf("unsafe failure projection: %s, %v", b, err)
	}
	f.err = errors.New("credential SECRET")
	if got, err := tool.Call(owner, args); got != nil || !errors.Is(err, errUnavailable) || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("raw indexing controller error surfaced")
	}
	f.err = nil
	f.result.ID = "../SECRET"
	if got, err := tool.Call(owner, args); !errors.Is(err, errUnavailable) || got != (snapshot{}) {
		t.Fatal("invalid controller job ID was not rejected")
	}
}
