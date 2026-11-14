// Package bootstrap owns the explicit, offline two-store initialization flow.
package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpconstantineau/GoDav-Prototype/internal/auth"
	settings "github.com/jpconstantineau/GoDav-Prototype/internal/config"
	"github.com/jpconstantineau/GoDav-Prototype/internal/sqlite"
)

const Defaults = "config_version: 1\nlisten_address: '127.0.0.1:8443'\npublic_origin: 'https://localhost:8443'\ndav_prefix: '/dav/'\ndatabase_path: 'godav.sqlite'\ntls:\n  cert_file: 'tls/server.crt'\n  key_file: 'tls/server.key'\nauthentication:\n  method: basic\nlimits:\n  max_file_bytes: 33554432\n"

var ErrConsole = errors.New("private interactive console required")

// Init returns true only for newly committed settings/account state. No secret
// escapes through its return values or routine CLI output. openConsole is the
// platform's real private terminal adapter, not a password-export interface.
func Init(config, username string, openConsole func() (*os.File, error)) (bool, error) {
	if !settings.ValidPathSyntax(config) {
		return false, errors.New("invalid settings path")
	}
	config, err := filepath.Abs(config)
	if err != nil {
		return false, errors.New("invalid settings path")
	}
	dbPath := filepath.Join(filepath.Dir(config), "godav.sqlite")
	if err := checkPath(config, true); err != nil {
		return false, errors.New("unsafe settings path")
	}
	_, stateErr := os.Lstat(config)
	existing := stateErr == nil
	if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		return false, errors.New("settings are inaccessible")
	}
	var current settings.Settings
	if existing {
		current, err = settings.Load(config)
		if err != nil {
			return false, errors.New("invalid settings")
		}
		dbPath = current.DatabasePath
	}
	var console *os.File
	if !existing {
		for _, suffix := range []string{"", ".lock", "-wal", "-journal", "-shm"} {
			if strings.EqualFold(config, dbPath+suffix) {
				return false, errors.New("settings and storage paths conflict")
			}
		}
		if err := requireAbsent(dbPath); err != nil {
			return false, err
		}
		console, err = openConsole()
		if err != nil {
			return false, ErrConsole
		}
		defer console.Close()
	}
	settingsPin, err := pinPrivateDirectory(filepath.Dir(config))
	if err != nil {
		return false, errors.New("settings directory must be private")
	}
	defer settingsPin.Close()
	if err := checkPrivateDirectory(filepath.Dir(dbPath)); err != nil {
		return false, errors.New("database directory must be private")
	}
	if err := checkPath(dbPath, true); err != nil {
		return false, errors.New("unsafe database path")
	}
	certPath, keyPath := filepath.Join(filepath.Dir(config), "tls", "server.crt"), filepath.Join(filepath.Dir(config), "tls", "server.key")
	if existing {
		certPath, keyPath = current.CertFile, current.KeyFile
	}
	if checkPath(certPath, true) != nil || checkPath(keyPath, true) != nil {
		return false, errors.New("unsafe TLS path")
	}
	if existing {
		if err := checkPrivateFile(dbPath); err != nil {
			return false, errors.New("database is missing, unsafe or inaccessible")
		}
		if err := requireNoSidecars(dbPath); err != nil {
			return false, err
		}
	} else {
		if err := requireAbsent(dbPath); err != nil {
			return false, err
		}
	}
	lock, err := acquireOwnership(dbPath + ".lock")
	if err != nil {
		return false, errors.New("database is in use or ownership is unsafe")
	}
	defer lock.Close()
	if existing {
		if checkPath(config, false) != nil {
			return false, errors.New("unsafe settings path")
		}
		rechecked, err := settings.Load(config)
		if err != nil || rechecked != current {
			return false, errors.New("settings changed during initialization")
		}
		if err := checkPrivateFile(dbPath); err != nil {
			return false, errors.New("database is missing, unsafe or inaccessible")
		}
		if err := requireNoSidecars(dbPath); err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := sqlite.Validate(ctx, dbPath); err != nil {
			return false, errors.New("invalid or unsupported database/account state")
		}
		return false, nil
	}
	if _, err := os.Lstat(config); !errors.Is(err, os.ErrNotExist) {
		return false, errors.New("settings changed during initialization")
	}
	if err := requireAbsent(dbPath); err != nil {
		return false, err
	}
	account, password, err := auth.Generate(username)
	if err != nil {
		return false, errors.New("credential generation failed")
	}
	f, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, errors.New("database creation failed")
	}
	if err := f.Close(); err != nil {
		return false, errors.New("database close failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sqlite.Initialize(ctx, dbPath, account); err != nil {
		return false, errors.New("database initialization failed; retain state for recovery")
	}
	f, err = os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		return false, errors.New("database durability check failed")
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return false, errors.New("database flush failed; retain state for recovery")
	}
	if err := publishCommittedSettings(config); err != nil {
		return false, errors.New("settings publication failed; retain database and reconstruct settings")
	}
	if err := deliverPassword(console, password); err != nil {
		return false, errors.New("private delivery failed; retain state and explicitly reset password")
	}
	return true, nil
}

func requireAbsent(path string) error {
	for _, suffix := range []string{"", "-wal", "-journal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return errors.New("database or partial state exists; retain database and reconstruct settings")
		}
	}
	return nil
}

func requireNoSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-journal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			return errors.New("partial database state requires explicit recovery")
		}
	}
	return nil
}

func publishSettings(path string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".godav-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := io.WriteString(f, Defaults); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return publishExclusive(f.Name(), path)
}
