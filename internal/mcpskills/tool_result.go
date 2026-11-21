package mcpskills

func failedToolResult(code, message string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []any{map[string]any{"type": "text", "text": message}},
		"_meta":   map[string]any{"io.godav/errorCode": code},
	}
}
