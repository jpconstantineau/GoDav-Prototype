package server

import (
	"strings"
	"testing"

	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

type fixtureBackendCapacity int

func (f fixtureBackendCapacity) MaxMCPDataConcurrency() int { return int(f) }

func TestMCPDataConcurrencyRequiresBackendCapability(t *testing.T) {
	if err := validateMCPDataConcurrency(1, new(sqlite.AccountStore)); err != nil {
		t.Fatal(err)
	}
	err := validateMCPDataConcurrency(2, new(sqlite.AccountStore))
	if err == nil || !strings.Contains(err.Error(), "exceeds backend capability") {
		t.Fatalf("SQLite must reject MCP width two, got %v", err)
	}
	if err := validateMCPDataConcurrency(2, fixtureBackendCapacity(2)); err != nil {
		t.Fatalf("capable future backend was rejected: %v", err)
	}
}
