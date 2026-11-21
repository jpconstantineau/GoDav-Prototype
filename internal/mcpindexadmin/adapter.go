// Package mcpindexadmin projects the indexing controller into a narrow MCP tool.
// Install the tool only on the authenticated, separately authorized admin endpoint.
package mcpindexadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/indexing"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"
)

const ToolName = "manage_indexing"

type Controller interface {
	Start(context.Context, string) (indexing.Status, error)
	Status(context.Context, string, string) (indexing.Status, error)
	Pause(context.Context, string, string) (indexing.Status, error)
	Resume(context.Context, string, string) (indexing.Status, error)
	Cancel(context.Context, string, string) (indexing.Status, error)
}

var (
	errInvalid     = errors.New("invalid indexing operation")
	errUnavailable = errors.New("indexing operation unavailable")
	runID          = regexp.MustCompile("^[a-f0-9]{32}$")
)

type snapshot struct {
	JobID       string `json:"job_id"`
	State       string `json:"state"`
	Total       int    `json:"total"`
	Processed   int    `json:"processed"`
	Indexed     int    `json:"indexed"`
	Skipped     int    `json:"skipped"`
	Backlog     int    `json:"backlog"`
	PauseReason string `json:"pause_reason,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
}

// New constructs only the descriptor and adapter; endpoint registration owns
// account authorization. The controller itself additionally enforces job ownership.
func New(controller Controller, available func() bool) (mcpskills.Tool, error) {
	if controller == nil {
		return mcpskills.Tool{}, errUnavailable
	}
	return mcpskills.Tool{
		Name:        ToolName,
		Description: "Monitor or manage the permitted collection's indexing job. Status optionally takes job_id to read a specific job. Start never takes job_id; pause, resume and cancel must use the current job_id. Starting reports accepted state, not a completed index.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{"type": "string", "enum": []string{"status", "start", "pause", "resume", "cancel"}},
				"job_id":    map[string]any{"type": "string", "pattern": runID.String()},
			},
			"required":             []string{"operation"},
			"additionalProperties": false,
		},
		Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			principal := auth.Principal(ctx)
			if principal == "" || ctx.Err() != nil || (available != nil && !available()) {
				return nil, errUnavailable
			}
			return invoke(ctx, controller, principal, raw)
		},
	}, nil
}

func invoke(ctx context.Context, c Controller, principal string, raw json.RawMessage) (any, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil || len(args) == 0 {
		return nil, errInvalid
	}
	readString := func(raw json.RawMessage) (string, bool) {
		raw = bytes.TrimSpace(raw)
		var value string
		if len(raw) < 2 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return "", false
		}
		return value, true
	}
	for key := range args {
		if key != "operation" && key != "job_id" {
			return nil, errInvalid
		}
	}
	op, ok := readString(args["operation"])
	if !ok {
		return nil, errInvalid
	}
	var id string
	if v, provided := args["job_id"]; provided {
		if id, ok = readString(v); !ok || !runID.MatchString(id) {
			return nil, errInvalid
		}
	}
	var out indexing.Status
	var err error
	switch op {
	case "status":
		out, err = c.Status(ctx, principal, id)
	case "start":
		if id != "" {
			return nil, errInvalid
		}
		out, err = c.Start(ctx, principal)
	case "pause", "resume", "cancel":
		if id == "" {
			return nil, errInvalid
		}
		switch op {
		case "pause":
			out, err = c.Pause(ctx, principal, id)
		case "resume":
			out, err = c.Resume(ctx, principal, id)
		case "cancel":
			out, err = c.Cancel(ctx, principal, id)
		}
	default:
		return nil, errInvalid
	}
	if err != nil {
		return nil, errUnavailable
	}
	return project(out)
}

func project(out indexing.Status) (snapshot, error) {
	state := string(out.State)
	switch state {
	case "queued", "running", "paused", "completed", "cancelled", "failed":
	default:
		return snapshot{}, errUnavailable
	}
	if !runID.MatchString(out.ID) || out.Total < 0 || out.Processed < 0 ||
		out.Indexed < 0 || out.Skipped < 0 || out.Backlog < 0 {
		return snapshot{}, errUnavailable
	}
	s := snapshot{JobID: out.ID, State: state, Total: out.Total,
		Processed: out.Processed, Indexed: out.Indexed, Skipped: out.Skipped, Backlog: out.Backlog}
	switch out.PauseReason {
	case "manual", "mcp_queue", "foreground", "quiet_period":
		s.PauseReason = out.PauseReason
	}
	switch out.ErrorCode {
	case "scope_unavailable", "capacity_exceeded", "scope_changing", "storage_unavailable", "owner_unavailable":
		s.ErrorCode = out.ErrorCode
	case "":
		if state == "failed" {
			s.ErrorCode = "indexing_failed"
		}
	default:
		s.ErrorCode = "indexing_failed"
	}
	return s, nil
}
