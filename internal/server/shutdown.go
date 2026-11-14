package server

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Cancellation is issued by the owner before this call. Keep both connection
// closure and handler joining inside the cleanup target; TLS Close itself can
// wait for transport I/O. A delay is a failure, never permission to close SQLite
// underneath an owner. There is only one cleanup goroutine per server.
func finishRequests(srv *http.Server, users *requestUsers, logger *slog.Logger) error {
	started := time.Now()
	joined := make(chan error, 1)
	go func() {
		err := srv.Close()
		users.wait()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			joined <- ErrShutdownFailed
			return
		}
		joined <- nil
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-joined:
		logger.Info("shutdown", "phase", "joined", "duration", time.Since(started))
		return err
	case <-timer.C:
		logger.Error("shutdown", "phase", "cleanup-delayed", "duration", time.Since(started))
		err := <-joined
		return errors.Join(ErrShutdownFailed, err)
	}
}
