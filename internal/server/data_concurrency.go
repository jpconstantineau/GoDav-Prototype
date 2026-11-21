package server

import "errors"

// dataConcurrencyPolicy separates dispatcher sizing from database choice. A
// backend may advertise greater capacity only once it owns the consistency,
// transaction and write guarantees for those simultaneous operations.
type dataConcurrencyPolicy interface {
	MaxMCPDataConcurrency() int
}

func validateMCPDataConcurrency(requested int, backend dataConcurrencyPolicy) error {
	if requested < 1 || requested > backend.MaxMCPDataConcurrency() {
		return errors.New("MCP data concurrency exceeds backend capability")
	}
	return nil
}
