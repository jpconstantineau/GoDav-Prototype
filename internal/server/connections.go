package server

import (
	"log/slog"
	"net"
	"sync"
)

const maxConnections = 16

// Count transport owners before TLS or HTTP can start a goroutine. Excess
// sockets are closed here, where no parsed HTTP response can be promised.
type boundedListener struct {
	net.Listener
	permits chan struct{}
	logger  *slog.Logger
}

func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.permits <- struct{}{}:
			return &boundedConn{Conn: conn, release: func() { <-l.permits }}, nil
		default:
			_ = conn.Close()
			l.logger.Info("admission", "result", "rejected", "reason", "connections")
		}
	}
}

type boundedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
