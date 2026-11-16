package files

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"strings"
)

const AbsoluteMaxFileBytes int64 = 64 << 20
const PayloadBudget int64 = 128 << 20

var ErrTooLarge = errors.New("file exceeds limit")
var ErrInput = errors.New("incomplete input")
var ErrBusy = errors.New("operation unavailable")
var ErrTimeout = errors.New("input timeout")

// The stored representation type is bounded ASCII MIME syntax, equally safe
// in HTTP headers and XML properties, independent of host MIME databases.
func ValidContentType(value string) bool {
	if len(value) == 0 || len(value) > 255 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	kind, _, err := mime.ParseMediaType(value)
	// ParseMediaType also accepts bare Content-Disposition tokens.
	return err == nil && strings.Contains(kind, "/")
}

func (s *Service) reserve(size int64) bool {
	s.payloadMu.Lock()
	defer s.payloadMu.Unlock()
	if size < 0 || size > PayloadBudget-s.payloadBytes {
		return false
	}
	s.payloadBytes += size
	return true
}
func (s *Service) release(size int64) {
	s.payloadMu.Lock()
	defer s.payloadMu.Unlock()
	s.payloadBytes -= size
}

func (s *Service) Put(ctx context.Context, path string, body io.Reader, length int64, contentType string, maxBytes int64, expectation Expectation) (Node, bool, error) {
	names, err := Components(path)
	if err != nil {
		return Node{}, false, err
	}
	if len(names) == 0 {
		return Node{}, false, ErrExists
	}
	if strings.HasSuffix(path, "/") {
		if snapshot, err := s.store.CollectionSnapshot(ctx, names, false); err == nil && !snapshot.Node.File {
			return Node{}, false, ErrExists
		}
		return Node{}, false, ErrInvalid
	}
	if !ValidContentType(contentType) {
		return Node{}, false, ErrInvalid
	}
	if maxBytes < 1 || maxBytes > AbsoluteMaxFileBytes || length < -1 {
		return Node{}, false, ErrInvalid
	}
	if length > maxBytes {
		return Node{}, false, ErrTooLarge
	}
	capacity := maxBytes
	if length >= 0 {
		capacity = length
	}
	if !s.reserve(capacity) {
		return Node{}, false, ErrBusy
	}
	defer s.release(capacity)
	data := make([]byte, int(capacity))
	defer clear(data)
	n := 0
	for {
		if ctx.Err() != nil {
			return Node{}, false, ErrBusy
		}
		var probe [1]byte
		destination := data[n:]
		if len(destination) == 0 {
			destination = probe[:]
		}
		read, readErr := body.Read(destination)
		if n == len(data) && read > 0 {
			if capacity == maxBytes {
				return Node{}, false, ErrTooLarge
			}
			return Node{}, false, ErrInput
		}
		n += read
		if read > 0 && stageFileFailure() != nil {
			return Node{}, false, ErrUnavailable
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			var timeout net.Error
			if errors.As(readErr, &timeout) && timeout.Timeout() {
				return Node{}, false, ErrTimeout
			}
			return Node{}, false, ErrInput
		}
	}
	if length >= 0 && int64(n) != length {
		return Node{}, false, ErrInput
	}
	if ctx.Err() != nil {
		return Node{}, false, ErrBusy
	}
	data = data[:n]
	return s.store.PutFile(ctx, names, data, contentType, expectation)
}

func (s *Service) Read(ctx context.Context, path string, expectations ...Expectation) (Snapshot, []byte, func(), error) {
	noop := func() {}
	names, err := Components(path)
	if err != nil {
		return Snapshot{}, nil, noop, err
	}
	if strings.HasSuffix(path, "/") {
		return Snapshot{}, nil, noop, ErrInvalid
	}
	if !s.reserve(AbsoluteMaxFileBytes) {
		return Snapshot{}, nil, noop, ErrBusy
	}
	snapshot, data, err := s.store.ReadFile(ctx, names, expectations...)
	if err != nil {
		s.release(AbsoluteMaxFileBytes)
		return Snapshot{}, nil, noop, err
	}
	return snapshot, data, func() { clear(data); s.release(AbsoluteMaxFileBytes) }, nil
}
