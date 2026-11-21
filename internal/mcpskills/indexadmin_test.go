package mcpskills

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestIndexAdminDiscoversOnlyOrdinaryToolAndSharesQueue(t *testing.T) {
	base, _ := fixture(t)
	admin, err := NewIndexAdmin(base, Tool{
		Name: "manage_indexing", InputSchema: map[string]any{"type": "object"},
		Call: func(context.Context, json.RawMessage) (any, error) { return map[string]any{"state": "queued"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, v := rpc(t, admin, "server/discover", nil)
	if resp.Code != http.StatusOK || resp.Header().Get("Cache-Control") != "no-store" || v["error"] != nil {
		t.Fatalf("invalid discover reply: %d %v", resp.Code, v)
	}
	discovery := v["result"].(map[string]any)
	capabilities := discovery["capabilities"].(map[string]any)
	if len(capabilities) != 1 || capabilities["tools"] == nil || discovery["cacheScope"] != "private" || discovery["ttlMs"] != float64(0) {
		t.Fatal("admin discovery advertised non-tool capabilities or nonprivate caching")
	}
	for _, tc := range []struct {
		handler http.Handler
		name    string
	}{{admin, "manage_indexing"}, {base, "list_skills"}} {
		w, v := rpc(t, tc.handler, "tools/list", nil)
		if w.Code != http.StatusOK || v["error"] != nil {
			t.Fatalf("tools/list failed: %+v", v)
		}
		b, _ := json.Marshal(v["result"])
		tools := v["result"].(map[string]any)["tools"].([]any)
		if first := tools[0].(map[string]any)["name"]; first != tc.name ||
			(tc.handler == admin && len(tools) != 1) || (tc.handler == base && len(tools) != 2) {
			t.Fatalf("wrong tool surface: %s", b)
		}
	}
	for _, method := range []string{"tasks/get", "skills/list", "resources/list"} {
		w, v := rpc(t, admin, method, nil)
		if w.Code != http.StatusNotFound || v["error"].(map[string]any)["code"] != float64(-32601) {
			t.Fatalf("%s is not unsupported: %d %v", method, w.Code, v)
		}
	}
	w, v := rpc(t, admin, "tools/call", map[string]any{"name": "manage_indexing", "arguments": map[string]any{"operation": "status"}})
	if w.Code != http.StatusOK || v["error"] != nil || v["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("admin tool is not an ordinary successful call: %d %v", w.Code, v)
	}
	StopAdmission(base)
	stopped := queueRequest(t.Context(), admin, "tools/call", "manage_indexing", false)
	queueStatus(t, stopped, http.StatusServiceUnavailable, "mcp-stopping", "1")
}
