// Package localstate owns access to existing private settings and accounts.
package localstate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	policyPins  []io.Closer
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
	for _, closer := range append([]io.Closer{s.ownership, s.settingsPin}, s.policyPins...) {
		if closer != nil && closer.Close() != nil {
			failed = true
		}
	}
	s.ownership, s.settingsPin = nil, nil
	s.policyPins = nil
	if failed {
		return errors.New("local state close failed")
	}
	return nil
}

// OpenExisting never initializes, migrates, repairs or reseeds state. Both
// intents retain exclusive process ownership. The intent cannot be upgraded.
func OpenExisting(ctx context.Context, configPath string, intent Intent) (*Session, error) {
	return OpenExistingWithPolicy(ctx, configPath, intent, nil)
}

// OpenExistingWithPolicy uses a session-scoped file policy without granting or repairing access.
func OpenExistingWithPolicy(ctx context.Context, configPath string, intent Intent, policy *localfs.Policy) (*Session, error) {
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
	s := &Session{}
	opened := false
	defer func() {
		if !opened {
			s.Close()
		}
	}()
	if policy.IsService() {
		// Protect settings identity and its read-only parent BEFORE the first
		// pathname-based config read. The same pins survive through session close.
		parent, pinErr := policy.PinReadOnlyDirectory(filepath.Dir(path))
		if pinErr != nil {
			return nil, errors.New("settings directory must be private")
		}
		s.policyPins = append(s.policyPins, parent)
		file, pinErr := policy.PinReadOnlyFile(path)
		if pinErr != nil {
			return nil, errors.New("settings are missing, unsafe or inaccessible")
		}
		s.policyPins = append(s.policyPins, file)
	}
	if policy.CheckReadOnlyFile(path) != nil {
		return nil, errors.New("settings are missing, unsafe or inaccessible")
	}
	current, err := config.Load(path)
	if err != nil {
		return nil, errors.New("invalid settings")
	}
	s.settings = current
	s.settingsPin, err = policy.PinReadOnlyDirectory(filepath.Dir(path))
	if err != nil {
		return nil, errors.New("settings directory must be private")
	}
	if policy.CheckMutableDirectory(filepath.Dir(current.DatabasePath)) != nil {
		return nil, errors.New("database directory must be private")
	}
	if policy.IsService() {
		// Read-only material must never live in the service-writable database tree.
		// Retain each checked file and parent, including TLS, for the whole session.
		for _, file := range []string{path, current.CertFile, current.KeyFile} {
			rel, relErr := filepath.Rel(filepath.Dir(current.DatabasePath), filepath.Dir(file))
			if relErr != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return nil, errors.New("service read-only material must be outside database directory")
			}
			for _, dir := range []bool{true, false} {
				var pin io.Closer
				var pinErr error
				if dir {
					pin, pinErr = policy.PinReadOnlyDirectory(filepath.Dir(file))
				} else {
					pin, pinErr = policy.PinReadOnlyFile(file)
				}
				if pinErr != nil {
					return nil, errors.New("service read-only material is unsafe or inaccessible")
				}
				s.policyPins = append(s.policyPins, pin)
			}
		}
	}
	if localfs.CheckPath(current.DatabasePath, true) != nil {
		return nil, errors.New("unsafe database path")
	}
	if localfs.CheckPath(current.CertFile, true) != nil || localfs.CheckPath(current.KeyFile, true) != nil {
		return nil, errors.New("unsafe TLS path")
	}
	if policy.CheckMutableFile(current.DatabasePath) != nil {
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
	s.ownership, err = policy.AcquireOwnership(current.DatabasePath+".lock", intent == WriteExisting)
	if err != nil {
		return nil, errors.New("database is in use or ownership is unsafe")
	}
	if policy.CheckReadOnlyFile(path) != nil {
		return nil, errors.New("settings are missing, unsafe or inaccessible")
	}
	rechecked, err := config.Load(path)
	if err != nil || rechecked != current {
		return nil, errors.New("settings changed during initialization")
	}
	if policy.CheckMutableFile(current.DatabasePath) != nil {
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
