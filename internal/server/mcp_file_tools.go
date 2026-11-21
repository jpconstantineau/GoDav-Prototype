package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpfiles"
	"github.com/jpconstantineau/GoDav-Prototype/internal/mcpskills"
)

func buildMCP(settings config.Settings, service *files.Service) (http.Handler, error) {
	if settings.MCPFilesRoot == "" {
		return mcpskills.NewWithToolsAndAdmission(service, settings.MCPSkillsRoot, nil, settings.Concurrency)
	}
	backend, err := mcpfiles.New(service, settings.MCPFilesRoot, settings.MaxFileBytes)
	if err != nil {
		return nil, err
	}
	descriptors := backend.Tools()
	selected := make([]mcpskills.Tool, 0, len(descriptors))
	for _, d := range descriptors {
		if mcpfiles.IsWrite(d.Name) && !settings.MCPFileWrites {
			continue
		}
		selected = append(selected, mcpskills.Tool{
			Name: d.Name, Description: d.Description, InputSchema: d.InputSchema,
			Call: func(ctx context.Context, args json.RawMessage) (any, error) {
				if mcpfiles.IsWrite(d.Name) && !settings.MCPFileWrites {
					return nil, files.ErrInvalid
				}
				return backend.Call(ctx, auth.Principal(ctx), d.Name, args)
			},
		})
	}
	return mcpskills.NewWithToolsAndAdmission(service, settings.MCPSkillsRoot, selected, settings.Concurrency)
}
