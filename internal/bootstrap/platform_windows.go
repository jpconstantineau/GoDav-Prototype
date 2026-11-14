package bootstrap

import (
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errUnsafePath = errors.New("bootstrap requires an unaliased local regular-file path")
var errPrivateDirectory = errors.New("bootstrap requires a privately owned directory with a restricted ACL")

// localPath rejects alternate Win32 namespaces and spellings before any OS
// access. In particular, filepath.Clean must not erase evidence of an alias.
func localPath(path string) ([]string, error) {
	if len(path) < 3 || path[1] != ':' || path[2] != '\\' ||
		!((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) ||
		strings.ContainsAny(path[2:], ":/\x00") {
		return nil, errUnsafePath
	}
	parts := strings.Split(path[3:], `\`)
	if path == path[:3] {
		parts = nil
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>"|?*`) {
			return nil, errUnsafePath
		}
		for _, r := range part {
			if r < 32 {
				return nil, errUnsafePath
			}
		}
		stem, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" ||
			(len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9') ||
			strings.HasPrefix(stem, "COM¹") || strings.HasPrefix(stem, "COM²") || strings.HasPrefix(stem, "COM³") ||
			strings.HasPrefix(stem, "LPT¹") || strings.HasPrefix(stem, "LPT²") || strings.HasPrefix(stem, "LPT³") {
			return nil, errUnsafePath
		}
	}
	root, err := windows.UTF16PtrFromString(path[:3])
	if err != nil || windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return nil, errUnsafePath
	}
	return parts, nil
}

func closePathHandles(handles []windows.Handle) {
	for i := len(handles) - 1; i >= 0; i-- {
		windows.CloseHandle(handles[i])
	}
}

func validatePathHandle(handle windows.Handle, path string, directory bool) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(handle, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errUnsafePath
	}
	if (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
		return errUnsafePath
	}
	if !directory && info.NumberOfLinks != 1 {
		return errUnsafePath
	}
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		return errUnsafePath
	}
	name := make([]uint16, 32768)
	n, err := windows.GetFinalPathNameByHandle(handle, &name[0], uint32(len(name)), 0)
	if err != nil || n == 0 || n >= uint32(len(name)) {
		return errUnsafePath
	}
	actual := strings.TrimPrefix(windows.UTF16ToString(name[:n]), `\\?\`)
	if !strings.EqualFold(actual, path) {
		return errUnsafePath
	}
	return nil
}

// openCheckedPath pins every existing component against rename/deletion while
// the handles remain open. A missing suffix is allowed only after all lexical
// validation and validation of the existing ancestors have succeeded.
func openCheckedPath(path string, allowMissing, directory bool) ([]windows.Handle, error) {
	return openCheckedPathAccess(path, allowMissing, directory, 0)
}

func openCheckedPathAccess(path string, allowMissing, directory bool, finalAccess uint32) ([]windows.Handle, error) {
	parts, err := localPath(path)
	if err != nil || (len(parts) == 0 && !directory) {
		return nil, errUnsafePath
	}
	var handles []windows.Handle
	current := path[:3]
	for i := 0; i <= len(parts); i++ {
		if i > 0 {
			current = filepath.Join(current, parts[i-1])
		}
		p, _ := windows.UTF16PtrFromString(current)
		access := uint32(windows.FILE_READ_ATTRIBUTES)
		if i == len(parts) {
			access |= finalAccess
		}
		h, e := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if e != nil {
			if allowMissing && (errors.Is(e, windows.ERROR_FILE_NOT_FOUND) || errors.Is(e, windows.ERROR_PATH_NOT_FOUND)) {
				return handles, nil
			}
			closePathHandles(handles)
			return nil, errUnsafePath
		}
		handles = append(handles, h)
		if validatePathHandle(h, current, i < len(parts) || directory) != nil {
			closePathHandles(handles)
			return nil, errUnsafePath
		}
	}
	return handles, nil
}

func checkPath(path string, allowMissing bool) error {
	handles, err := openCheckedPath(path, allowMissing, false)
	closePathHandles(handles)
	return err
}

// restrictedACL is a deliberately conservative allowlist, not a complete
// effective-access analysis. It rejects deny/object/callback/unknown ACE forms
// rather than trying to evaluate them, and inspects inherit-only ACEs too.
func restrictedACL(handle windows.Handle) error {
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil {
		return errPrivateDirectory
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return errPrivateDirectory
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return errPrivateDirectory
	}
	allowed := func(sid *windows.SID) bool {
		return sid != nil && sid.IsValid() && (sid.Equals(user.User.Sid) || sid.Equals(system) || sid.Equals(admins))
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return errPrivateDirectory
	}
	owner, _, err := sd.Owner()
	if err != nil || !allowed(owner) {
		return errPrivateDirectory
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errPrivateDirectory
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, i, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < 16 || ace.Header.AceFlags & ^uint8(0x1f) != 0 {
			return errPrivateDirectory
		}
		// SID length is 8 + four bytes per subauthority. Check the ACE's extent
		// before calling SID methods, even though Windows validates file ACLs.
		sidPointer := unsafe.Pointer(&ace.SidStart)
		header := unsafe.Slice((*byte)(sidPointer), 8)
		length := 8 + 4*int(header[1])
		if length > int(ace.Header.AceSize)-8 || !allowed((*windows.SID)(sidPointer)) {
			return errPrivateDirectory
		}
	}
	runtime.KeepAlive(sd)
	return nil
}

func checkPrivateDirectory(path string) error {
	pin, err := pinPrivateDirectory(path)
	if err != nil {
		return err
	}
	return pin.Close()
}

type privateDirectoryPin struct {
	handles []windows.Handle
}

func (pin *privateDirectoryPin) Close() error {
	var first error
	for i := len(pin.handles) - 1; i >= 0; i-- {
		if err := windows.CloseHandle(pin.handles[i]); err != nil && first == nil {
			first = err
		}
	}
	pin.handles = nil
	return first
}

// pinPrivateDirectory validates owner/DACL on the very handle retained until
// Close, not a prior pathname lookup. Every ancestor is also held without
// FILE_SHARE_DELETE, preventing directory substitution during the operation.
// This does not freeze ACLs against the owner, SYSTEM or administrators, and is
// not intended to protect against a malicious process running as those actors.
func pinPrivateDirectory(path string) (io.Closer, error) {
	handles, err := openCheckedPathAccess(path, false, true, windows.READ_CONTROL)
	if err != nil {
		return nil, err
	}
	if err := restrictedACL(handles[len(handles)-1]); err != nil {
		closePathHandles(handles)
		return nil, err
	}
	return &privateDirectoryPin{handles: handles}, nil
}

func checkPrivateFile(path string) error {
	return checkPrivateObject(path, false)
}

func checkPrivateObject(path string, directory bool) error {
	handles, err := openCheckedPathAccess(path, false, directory, windows.READ_CONTROL)
	if err != nil {
		return err
	}
	defer closePathHandles(handles)
	return restrictedACL(handles[len(handles)-1])
}

type ownership struct {
	file    windows.Handle
	parents io.Closer
}

func (lock *ownership) Close() error {
	if lock.file == windows.InvalidHandle {
		return nil
	}
	err := windows.CloseHandle(lock.file)
	lock.file = windows.InvalidHandle
	parentErr := lock.parents.Close()
	lock.parents = nil
	if err == nil {
		return parentErr
	}
	return err
}

func acquireOwnership(path string) (io.Closer, error) {
	if err := checkPath(path, true); err != nil {
		return nil, err
	}
	parents, err := pinPrivateDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			parents.Close()
		}
	}()
	p, _ := windows.UTF16PtrFromString(path)
	// OPEN_ALWAYS never truncates. Zero sharing is an OS-held exclusive owner
	// and prevents removal/replacement until this handle closes. The stable
	// empty artifact remains after close, including when validation fails.
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("bootstrap ownership is unavailable")
	}
	var info windows.ByHandleFileInformation
	if validatePathHandle(h, path, false) != nil || windows.GetFileInformationByHandle(h, &info) != nil ||
		info.FileSizeHigh != 0 || info.FileSizeLow != 0 || restrictedACL(h) != nil {
		windows.CloseHandle(h)
		return nil, errors.New("bootstrap ownership artifact is unsafe")
	}
	failed = false
	return &ownership{file: h, parents: parents}, nil
}

func publishExclusive(temp, path string) error {
	if err := checkPath(temp, false); err != nil {
		return err
	}
	if err := checkPath(path, true); err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(temp), filepath.Dir(path)) || strings.EqualFold(temp, path) {
		return errors.New("bootstrap publication requires distinct files in one directory")
	}
	parents, err := openCheckedPath(filepath.Dir(path), false, true)
	if err != nil {
		return err
	}
	defer closePathHandles(parents)
	from, _ := windows.UTF16PtrFromString(temp)
	to, _ := windows.UTF16PtrFromString(path)
	// Neither REPLACE_EXISTING nor COPY_ALLOWED: an existing destination fails,
	// and publication cannot silently become a cross-volume copy/delete.
	if windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH) != nil {
		return errors.New("bootstrap exclusive publication failed")
	}
	return nil
}

// syncParent intentionally performs no directory flush on Windows. The caller
// flushes file contents before publishExclusive, which requests WRITE_THROUGH.
// Windows documents no ordinary unprivileged directory-fsync equivalent here;
// this supports normal restart persistence, NOT proven power-loss durability
// of directory entries. Do not turn this no-op into a stronger durability claim.
func syncParent(path string) error { return nil }
