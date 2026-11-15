package localfs

import (
	"io"
	"runtime"

	"golang.org/x/sys/windows"
)

// PinProgramFile validates executable integrity, not private-data access. Other
// principals may read/execute, but only the explicit operator, SYSTEM and
// administrators may own or change it. Keep the immediate directory pin too.
func PinProgramFile(operatorSID, path string) (io.Closer, error) {
	return pinProgram(operatorSID, path, false)
}
func PinProgramDirectory(operatorSID, path string) (io.Closer, error) {
	return pinProgram(operatorSID, path, true)
}

// PinPathFile checks only canonical regular-file identity and pins ancestors.
// It grants no assurance about privacy or integrity and must not authorize a
// settings read. Registration may retain this before service identity exists.
func PinPathFile(path string) (io.Closer, error) {
	handles, err := openCheckedPathPinned(path, false, false, 0, true)
	if err != nil {
		return nil, err
	}
	return &privateDirectoryPin{handles: handles}, nil
}

func pinProgram(operatorSID, path string, directory bool) (io.Closer, error) {
	operator, err := windows.StringToSid(operatorSID)
	if err != nil || !ordinaryOperator(operator, operatorSID) {
		return nil, errServicePolicy
	}
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil || !operator.Equals(user.User.Sid) {
		return nil, errServicePolicy
	}
	handles, err := openCheckedPathPinned(path, false, directory, windows.READ_CONTROL, true)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			closePathHandles(handles)
		}
	}()
	sd, err := windows.GetSecurityInfo(handles[len(handles)-1], windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return nil, errServicePolicy
	}
	defer runtime.KeepAlive(sd)
	trusted := func(id string) bool { return id == operatorSID || id == "S-1-5-18" || id == "S-1-5-32-544" }
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !trusted(owner.String()) {
		return nil, errServicePolicy
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return nil, errServicePolicy
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		grant, err := readGrant(acl, i)
		if err != nil {
			return nil, err
		}
		if !trusted(grant.sid) && grant.mask & ^uint32(windows.FILE_GENERIC_READ|windows.FILE_GENERIC_EXECUTE) != 0 {
			return nil, errServicePolicy
		}
	}
	ok = true
	return &privateDirectoryPin{handles: handles}, nil
}
