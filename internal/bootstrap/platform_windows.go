package bootstrap

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/jpconstantineau/GoDav-Prototype/internal/localfs"
	"golang.org/x/sys/windows"
)

// The caller flushes file contents before publishExclusive requests WRITE_THROUGH.
// Windows documents no ordinary unprivileged directory-fsync equivalent here;
// this supports normal restart persistence, not proven power-loss durability
// of directory entries.
func publishExclusive(temp, path string) error {
	if err := localfs.CheckPath(temp, false); err != nil {
		return err
	}
	if err := localfs.CheckPath(path, true); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(temp), filepath.Dir(path)) || strings.EqualFold(temp, path) {
		return errors.New("bootstrap publication requires distinct files in one directory")
	}
	parents, err := localfs.PinPrivateDirectory(filepath.Dir(path), false)
	if err != nil {
		return err
	}
	defer parents.Close()
	from, _ := windows.UTF16PtrFromString(temp)
	to, _ := windows.UTF16PtrFromString(path)
	// Neither REPLACE_EXISTING nor COPY_ALLOWED: an existing destination fails,
	// and publication cannot silently become a cross-volume copy/delete.
	if windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH) != nil {
		return errors.New("bootstrap exclusive publication failed")
	}
	return nil
}
