package localfs

import (
	"errors"
	"io"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errServicePolicy = errors.New("invalid or inaccessible service file policy")

// ServicePolicy accepts an already-bound per-instance virtual account and an
// explicit ordinary Windows account. Name-to-SID binding belongs to the native
// service adapter; accepting these strings is not evidence of that binding.
func ServicePolicy(serviceSID, operatorSID string) (*Policy, error) {
	service, e1 := windows.StringToSid(serviceSID)
	operator, e2 := windows.StringToSid(operatorSID)
	if e1 != nil || e2 != nil || !service.IsValid() || !operator.IsValid() || service.String() != serviceSID || operator.String() != operatorSID ||
		!strings.HasPrefix(serviceSID, "S-1-5-80-") || service.SubAuthorityCount() != 6 ||
		!ordinaryOperator(operator, operatorSID) || service.Equals(operator) {
		return nil, errServicePolicy
	}
	p := &Policy{serviceSID: serviceSID, operatorSID: operatorSID}
	if p.checkCaller() != nil {
		return nil, errServicePolicy
	}
	return p, nil
}
func (p *Policy) checkCaller() error {
	user, err := windows.GetCurrentThreadEffectiveToken().GetTokenUser()
	if err != nil || (user.User.Sid.String() != p.serviceSID && user.User.Sid.String() != p.operatorSID) {
		return errServicePolicy
	}
	return nil
}

func expandedFileMask(mask uint32) uint32 {
	if mask&windows.GENERIC_ALL != 0 {
		mask = mask&^windows.GENERIC_ALL | 0x001f01ff
	}
	if mask&windows.GENERIC_READ != 0 {
		mask = mask&^windows.GENERIC_READ | windows.FILE_GENERIC_READ
	}
	if mask&windows.GENERIC_WRITE != 0 {
		mask = mask&^windows.GENERIC_WRITE | windows.FILE_GENERIC_WRITE
	}
	if mask&windows.GENERIC_EXECUTE != 0 {
		mask = mask&^windows.GENERIC_EXECUTE | windows.FILE_GENERIC_EXECUTE
	}
	return mask
}

// policyACL rejects any unmodelled ACE rather than approximating effective
// access. Direct grants to BOTH identities are required; administrator group
// membership is never a substitute for the named operator's explicit grant.
// A service-owned mutable object still confers implicit owner DACL authority;
// restricting explicit ACE masks is not containment of a malicious data owner.
func (p *Policy) policyACL(handle windows.Handle, directory, mutable, inherit bool) error {
	if !p.IsService() {
		return restrictedACL(handle, inherit)
	}
	if p.checkCaller() != nil {
		return errServicePolicy
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return errServicePolicy
	}
	defer runtime.KeepAlive(sd)
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errServicePolicy
	}
	ownerID := owner.String()
	if ownerID != p.operatorSID && ownerID != "S-1-5-18" && ownerID != "S-1-5-32-544" && !(mutable && ownerID == p.serviceSID) {
		return errServicePolicy
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errServicePolicy
	}
	const read = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE
	const modify = read | windows.FILE_GENERIC_WRITE | windows.DELETE
	required := uint32(read)
	allowedService := uint32(read)
	if mutable {
		required = modify
		allowedService = modify
		if directory {
			allowedService |= 0x00000040
		}
	}
	var serviceDirect, operatorDirect, serviceChildren, operatorChildren uint32
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		grant, err := readGrant(acl, i)
		if err != nil {
			return err
		}
		id := grant.sid
		if id != p.serviceSID && id != p.operatorSID && id != "S-1-5-18" && id != "S-1-5-32-544" {
			return errServicePolicy
		}
		mask := grant.mask
		if id == p.serviceSID && mask & ^allowedService != 0 {
			return errServicePolicy
		}
		direct := grant.flags&windows.INHERIT_ONLY_ACE == 0
		child := grant.flags&windows.OBJECT_INHERIT_ACE != 0
		if id == p.serviceSID {
			if direct {
				serviceDirect |= mask
			}
			if child {
				serviceChildren |= mask
			}
		}
		if id == p.operatorSID {
			if direct {
				operatorDirect |= mask
			}
			if child {
				operatorChildren |= mask
			}
		}
	}
	if serviceDirect&required != required || operatorDirect&required != required {
		return errServicePolicy
	}
	if inherit && (serviceChildren&modify != modify || operatorChildren&modify != modify) {
		return errServicePolicy
	}
	return nil
}
func (p *Policy) pinObject(path string, directory, mutable, inherit bool) (io.Closer, error) {
	handles, err := openCheckedPathPinned(path, false, directory, windows.READ_CONTROL, p.IsService())
	if err != nil {
		return nil, err
	}
	if err = p.policyACL(handles[len(handles)-1], directory, mutable, inherit); err != nil {
		closePathHandles(handles)
		return nil, err
	}
	return &privateDirectoryPin{handles: handles}, nil
}

// SID shape alone does not distinguish an account from a domain group.
func ordinaryOperator(sid *windows.SID, spelling string) bool {
	if sid == nil || !sid.IsValid() || sid.String() != spelling || !strings.HasPrefix(spelling, "S-1-5-21-") || sid.SubAuthorityCount() != 5 {
		return false
	}
	_, _, kind, err := sid.LookupAccount("")
	return err == nil && kind == windows.SidTypeUser
}

type accessGrant struct {
	sid   string
	mask  uint32
	flags uint8
}

func readGrant(acl *windows.ACL, index uint32) (accessGrant, error) {
	var ace *windows.ACCESS_ALLOWED_ACE
	if windows.GetAce(acl, index, &ace) != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceSize < 16 || ace.Header.AceFlags & ^uint8(0x1f) != 0 {
		return accessGrant{}, errServicePolicy
	}
	ptr := unsafe.Pointer(&ace.SidStart)
	head := unsafe.Slice((*byte)(ptr), 8)
	if 8+4*int(head[1]) > int(ace.Header.AceSize)-8 {
		return accessGrant{}, errServicePolicy
	}
	sid := (*windows.SID)(ptr)
	if !sid.IsValid() {
		return accessGrant{}, errServicePolicy
	}
	return accessGrant{sid.String(), expandedFileMask(uint32(ace.Mask)), ace.Header.AceFlags}, nil
}
