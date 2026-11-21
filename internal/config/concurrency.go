package config

import (
	"time"

	"go.yaml.in/yaml/v3"
)

// Concurrency controls independent admission layers. A lower upstream limit
// can make part of a downstream budget unreachable; that is not invalid.
type Concurrency struct {
	MaxConnections, MaxRequests, MaxRequestsPerAccount int
	AuthSlots, ReceivingUploads, MCPActive, MCPWaiting int
	MCPWait, RequestTimeout                            time.Duration
}

func DefaultConcurrency() Concurrency {
	return Concurrency{
		MaxConnections: 128, MaxRequests: 100, MaxRequestsPerAccount: 10,
		AuthSlots: 10, ReceivingUploads: 2, MCPActive: 1, MCPWaiting: 5,
		MCPWait: 500 * time.Millisecond, RequestTimeout: 10 * time.Second,
	}
}

func parseConcurrency(node *yaml.Node) (Concurrency, bool) {
	c := DefaultConcurrency()
	if node == nil {
		return c, true
	}
	layers := []struct {
		key    string
		cap    int64
		target *int
	}{
		{"max_connections", 1024, &c.MaxConnections},
		{"max_requests", 512, &c.MaxRequests},
		{"max_requests_per_account", 128, &c.MaxRequestsPerAccount},
		{"auth_slots", 16, &c.AuthSlots},
		{"receiving_uploads", 32, &c.ReceivingUploads},
		{"mcp_active", 16, &c.MCPActive},
		{"mcp_waiting", 32, &c.MCPWaiting},
	}
	timers := []struct {
		key    string
		cap    int64
		target *time.Duration
	}{
		{"mcp_wait_ms", 5000, &c.MCPWait},
		{"request_timeout_ms", 10000, &c.RequestTimeout},
	}
	keys := make([]string, 0, len(layers)+len(timers))
	for _, entry := range layers {
		keys = append(keys, entry.key)
	}
	for _, entry := range timers {
		keys = append(keys, entry.key)
	}
	nodes, ok := mappingOptional(node, nil, keys...)
	if !ok {
		return Concurrency{}, false
	}
	for _, entry := range layers {
		if option := nodes[entry.key]; option != nil {
			value, good := positiveInt(option)
			if !good || value > entry.cap {
				return Concurrency{}, false
			}
			*entry.target = int(value)
		}
	}
	for _, entry := range timers {
		if option := nodes[entry.key]; option != nil {
			value, good := positiveInt(option)
			if !good || value > entry.cap {
				return Concurrency{}, false
			}
			*entry.target = time.Duration(value) * time.Millisecond
		}
	}
	return c, true
}
