package cli_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func registerGitCleanup(t *testing.T, dir string) {
	t.Helper()
	// Run before TempDir cleanup. Git creates read-only loose objects, which
	// Windows RemoveAll can fail to delete. Only this synthetic fixture is owned.
	t.Cleanup(func() {
		for _, path := range []string{dir, filepath.Join(dir, ".git")} {
			info, err := os.Lstat(path)
			if err != nil {
				t.Error(err)
				return
			}
			attrs := info.Sys().(*syscall.Win32FileAttributeData).FileAttributes
			if !info.IsDir() || attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				t.Errorf("refuse Git cleanup through non-directory or reparse point: %s", path)
				return
			}
		}
		err := filepath.WalkDir(filepath.Join(dir, ".git", "objects"), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			attrs := info.Sys().(*syscall.Win32FileAttributeData).FileAttributes
			if attrs&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				return fmt.Errorf("refuse Git cleanup at reparse point: %s", path)
			}
			if info.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refuse Git cleanup of non-regular object: %s", path)
			}
			if attrs&syscall.FILE_ATTRIBUTE_READONLY != 0 {
				return os.Chmod(path, 0600)
			}
			return nil
		})
		if err != nil {
			t.Errorf("prepare Git fixture cleanup: %v", err)
		}
	})
}
