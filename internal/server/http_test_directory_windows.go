//go:build windows

package server

import (
	"crypto/rand"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

// Create atomically with restrictive, inheritable ACLs. Never change an existing
// directory's permissions; an existing random name simply fails creation.
func newHTTPTestDirectory() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(os.TempDir(), "godav-http-test-"+rand.Text())
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(path, &sa); err != nil {
		return "", err
	}
	return dir, nil
}
