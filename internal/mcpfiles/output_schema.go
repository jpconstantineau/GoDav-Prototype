package mcpfiles

// outputSchema describes the existing successful GoDav result objects. These
// use the common subset supported by both legacy and modern MCP clients.
func outputSchema(tool string) map[string]any {
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	}
	array := func(items map[string]any) map[string]any {
		return map[string]any{"type": "array", "items": items}
	}
	integer := func(minimum int) map[string]any {
		return map[string]any{"type": "integer", "minimum": minimum}
	}
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	fields := func() map[string]any {
		return map[string]any{"name": text("Canonical name in the containing folder."), "file": map[string]any{"type": "boolean"}, "size": integer(0), "etag": text("Exact strong ETag; pass unchanged as expected_etag.")}
	}
	file := func() map[string]any {
		props := fields()
		props["file"] = map[string]any{"type": "boolean", "enum": []bool{true}}
		return object(props, "name", "file", "size", "etag")
	}
	path := text("Canonical decoded path relative to the configured file library, not to the requested search folder.")
	partial := func(description string) map[string]any {
		return map[string]any{"type": "boolean", "description": description}
	}
	switch tool {
	case "list_files":
		// GoDav may attach validators to collection metadata. Only file
		// entries unconditionally require one.
		entry := object(fields(), "name", "file", "size")
		entry["allOf"] = []any{map[string]any{"if": map[string]any{"properties": map[string]any{"file": map[string]any{"enum": []bool{true}}}}, "then": map[string]any{"required": []string{"etag"}}}}
		return object(map[string]any{"entries": array(entry), "next_offset": map[string]any{"type": "integer", "minimum": 1, "description": "Supply as offset for the next page. Omitted on the last page. Pages are not a snapshot."}}, "entries")
	case "read_file":
		return object(map[string]any{"file": file(), "content": text("Complete UTF-8 text. Oversized or binary files are refused, never truncated.")}, "file", "content")
	case "create_file", "edit_file":
		return object(map[string]any{"file": file(), "created": map[string]any{"type": "boolean", "enum": []bool{tool == "create_file"}}}, "file", "created")
	case "delete_file":
		return object(map[string]any{"deleted": map[string]any{"type": "boolean", "enum": []bool{true}}}, "deleted")
	case "search_files":
		entry := file()
		entry["properties"].(map[string]any)["path"] = path
		entry["required"] = []string{"path", "name", "file", "size", "etag"}
		result := object(map[string]any{
			"entries": array(entry), "partial": partial("The traversal stopped at a limit or changed during the scan. Narrow and run a new search; there is no cursor."),
			"stop_reason":     map[string]any{"type": "string", "enum": []string{"result_limit", "directory_limit", "scan_limit", "tree_changed"}},
			"scanned_entries": integer(0), "scanned_directories": integer(0),
		}, "entries", "partial", "scanned_entries", "scanned_directories")
		result["allOf"] = []any{map[string]any{
			"if":   map[string]any{"properties": map[string]any{"partial": map[string]any{"enum": []bool{true}}}},
			"then": map[string]any{"required": []string{"stop_reason"}},
			"else": map[string]any{"not": map[string]any{"required": []string{"stop_reason"}}},
		}}
		return result
	case "search_file_contents":
		entry := object(map[string]any{"path": path, "name": text("Canonical file name."), "size": integer(0), "etag": text("Exact strong ETag; pass unchanged as expected_etag."), "content_type": text("Stored MIME type, which may include parameters."), "modified": map[string]any{"type": "string", "format": "date-time"}}, "path", "name", "size", "etag", "content_type", "modified")
		return object(map[string]any{"entries": array(entry), "partial": partial("More matches exist than the result limit. Narrow the filters and run a new search; there is no cursor.")}, "entries", "partial")
	default:
		return nil
	}
}
