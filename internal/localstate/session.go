// Package localstate owns access to existing private settings and accounts.
package localstate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

// Intent is fixed for the lifetime of a session. Zero is deliberately invalid.
type Intent uint8

const (
	ReadOnly      Intent = 1
	WriteExisting Intent = 2
)

// Session retains exclusive ownership and parent pins until Close. Callers must
// drain all users before closing it; cancellation never silently closes it.
type Session struct {
	settings    config.Settings
	accounts    *sqlite.AccountStore
	ownership   io.Closer
	settingsPin io.Closer
}

func (s *Session) Settings() config.Settings      { return s.settings }
func (s *Session) Accounts() *sqlite.AccountStore { return s.accounts }

// Close attempts all cleanup, closing SQLite before ownership and parent pins.
// It is idempotent and never unlinks the stable ownership artifact.
func (s *Session) Close() error {
	failed := false
	if s.accounts != nil {
		failed = s.accounts.Close() != nil
		s.accounts = nil
	}
	for _, closer := range []io.Closer{s.ownership, s.settingsPin} {
		if closer != nil && closer.Close() != nil {
			failed = true
		}
	}
	s.ownership, s.settingsPin = nil, nil
	if failed {
		return errors.New("local state close failed")
	}
	return nil
}

// OpenExisting never initializes, migrates, repairs or reseeds state. Both
// intents retain exclusive process ownership. The intent cannot be upgraded.
func OpenExisting(ctx context.Context, configPath string, intent Intent) (*Session, error) {
	if intent != ReadOnly && intent != WriteExisting {
		return nil, errors.New("invalid local state intent")
	}
	if ctx.Err() != nil {
		return nil, errors.New("local state opening canceled")
	}
	if !config.ValidPathSyntax(configPath) {
		return nil, errors.New("invalid settings path")
	}
	path, err := filepath.Abs(configPath)
	if err != nil {
		return nil, errors.New("invalid settings path")
	}
	if localfs.CheckPath(path, true) != nil {
		return nil, errors.New("unsafe settings path")
	}
	if localfs.CheckPrivateFile(path) != nil {
		return nil, errors.New("settings are missing, unsafe or inaccessible")
	}
	current, err := config.Load(path)
	if err != nil {
		return nil, errors.New("invalid settings")
	}
	s := &Session{settings: current}
	opened := false
	defer func() {
		if !opened {
			s.Close()
		}
	}()
	s.settingsPin, err = localfs.PinPrivateDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, errors.New("settings directory must be private")
	}
	if localfs.CheckPrivateDirectory(filepath.Dir(current.DatabasePath)) != nil {
		return nil, errors.New("database directory must be private")
	}
	if localfs.CheckPath(current.DatabasePath, true) != nil {
		return nil, errors.New("unsafe database path")
	}
	if localfs.CheckPath(current.CertFile, true) != nil || localfs.CheckPath(current.KeyFile, true) != nil {
		return nil, errors.New("unsafe TLS path")
	}
	if localfs.CheckPrivateFile(current.DatabasePath) != nil {
		return nil, errors.New("database is missing, unsafe or inaccessible")
	}
	if err := requireNoSidecars(current.DatabasePath); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, errors.New("local state opening canceled")
	}
	// The ownership object retains the checked DB parent. Writers require
	// restricted file inheritance before SQLite can create rollback journals.
	s.ownership, err = localfs.AcquireOwnership(current.DatabasePath+".lock", intent == WriteExisting)
	if err != nil {
		return nil, errors.New("database is in use or ownership is unsafe")
	}
	if localfs.CheckPrivateFile(path) != nil {
		return nil, errors.New("settings are missing, unsafe or inaccessible")
	}
	rechecked, err := config.Load(path)
	if err != nil || rechecked != current {
		return nil, errors.New("settings changed during initialization")
	}
	if localfs.CheckPrivateFile(current.DatabasePath) != nil {
		return nil, errors.New("database is missing, unsafe or inaccessible")
	}
	if err := requireNoSidecars(current.DatabasePath); err != nil {
		return nil, err
	}
	if intent == ReadOnly {
		s.accounts, err = sqlite.OpenExistingReadOnly(ctx, current.DatabasePath)
	} else {
		s.accounts, err = sqlite.OpenExistingForUpdate(ctx, current.DatabasePath)
	}
	if err != nil {
		return nil, errors.New("invalid or unsupported database/account state")
	}
	if ctx.Err() != nil {
		return nil, errors.New("local state opening canceled")
	}
	opened = true
	return s, nil
}

func requireNoSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return errors.New("partial database state requires explicit recovery")
		}
	}
	return nil
}
