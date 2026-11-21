package mcpskills

import "net/http"

// PendingDataRequests is the current number of reserved MCP data waiters.
// Background work may use it as a pressure hint, not as proof SQLite is idle.
func PendingDataRequests(h http.Handler) int {
	if scoped, ok := h.(*handler); ok && scoped != nil {
		_, waiting := scoped.admission.occupancy()
		return waiting
	}
	return 0
}
