package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

// RunHTTPTest cannot open an operator-supplied config or database. Every run
// creates a fresh private directory/database, removed after the shared drain.
func RunHTTPTest(ctx context.Context, port int, identity Identity, output io.Writer) (result error) {
	if port < 1 || port > 65535 || ctx.Err() != nil {
		return errors.New("invalid HTTP test port or context")
	}
	if !routingSupported() {
		return errors.New("legacy HTTP routing unsupported")
	}
	dir, err := newHTTPTestDirectory()
	if err != nil {
		return errors.New("HTTP test directory unavailable")
	}
	state := &httpTestState{dir: dir}
	defer func() { result = errors.Join(result, state.Close()) }()
	state.pin, err = localfs.PinPrivateDirectory(dir, true)
	if err != nil {
		return errors.New("HTTP test directory unsafe")
	}
	path := filepath.Join(dir, "disposable.sqlite")
	state.lock, err = localfs.AcquireOwnership(path+".lock", true)
	if err != nil {
		return errors.New("HTTP test ownership failed")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("HTTP test storage creation failed")
	}
	if err = f.Close(); err != nil {
		return errors.New("HTTP test storage close failed")
	}
	account, _, err := auth.Generate("HTTPTest")
	if err != nil {
		return errors.New("HTTP test identity failed")
	}
	if err = sqlite.Initialize(ctx, path, account); err != nil {
		return errors.New("HTTP test storage initialization failed")
	}
	state.store, err = sqlite.OpenExistingForUpdate(ctx, path)
	if err != nil {
		return errors.New("HTTP test storage open failed")
	}
	address := "127.0.0.1:" + strconv.Itoa(port)
	state.settings = config.Settings{ListenAddress: address, PublicOrigin: "http://" + address, DAVPrefix: "/dav/", DatabasePath: path, MaxFileBytes: 32 << 20, ShutdownGrace: 5 * time.Second}
	return serveState(ctx, state, identity, output, nil, nil, true)
}

type httpTestState struct {
	dir       string
	settings  config.Settings
	store     *sqlite.AccountStore
	pin, lock io.Closer
}

func (s *httpTestState) Settings() config.Settings      { return s.settings }
func (s *httpTestState) Accounts() *sqlite.AccountStore { return s.store }
func (s *httpTestState) Close() error {
	var result error
	if s.store != nil {
		result = errors.Join(result, s.store.Close())
		s.store = nil
	}
	if s.lock != nil {
		result = errors.Join(result, s.lock.Close())
		s.lock = nil
	}
	if s.pin != nil {
		result = errors.Join(result, s.pin.Close())
		s.pin = nil
	}
	// dir is always our own freshly created directory, never user input.
	if s.dir != "" {
		result = errors.Join(result, os.RemoveAll(s.dir))
		s.dir = ""
	}
	return result
}
