package service

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ManageNative is the production SCM boundary. Unit tests never invoke it.
func ManageNative(ctx context.Context, action Action, r Registration) (Status, error) {
	if err := r.Validate(); err != nil {
		return Status{}, err
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	switch action {
	case Install, Check, Start, Stop, Uninstall:
	default:
		return Status{}, errors.New("unknown service operation")
	}
	rights := uint32(windows.SC_MANAGER_CONNECT)
	if action == Install {
		rights |= windows.SC_MANAGER_CREATE_SERVICE
	}
	handle, err := windows.OpenSCManager(nil, nil, rights)
	if err != nil {
		return Status{}, err
	}
	b := &nativeBackend{handle: handle, expected: r}
	s, err := Manage(ctx, b, action, r)
	return s, errors.Join(err, windows.CloseServiceHandle(handle))
}

type nativeBackend struct {
	handle   windows.Handle
	expected Registration
}

func (b *nativeBackend) Open(name string, action Action) (ManagedService, error) {
	rights := uint32(windows.SERVICE_QUERY_CONFIG | windows.SERVICE_QUERY_STATUS)
	switch action {
	case Start:
		rights |= windows.SERVICE_START
	case Stop:
		rights |= windows.SERVICE_STOP
	case Uninstall:
		rights |= windows.DELETE
	}
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(b.handle, n, rights)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &nativeService{service: mgr.Service{Name: name, Handle: h}, expected: b.expected}, nil
}
func (b *nativeBackend) Create(r Registration) (ManagedService, error) {
	name, err := windows.UTF16PtrFromString(r.Name)
	if err != nil {
		return nil, err
	}
	command, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(r.Arguments()))
	if err != nil {
		return nil, err
	}
	account, err := windows.UTF16PtrFromString(r.Account())
	if err != nil {
		return nil, err
	}
	rights := uint32(windows.SERVICE_QUERY_CONFIG | windows.SERVICE_QUERY_STATUS | windows.SERVICE_CHANGE_CONFIG | windows.DELETE)
	h, err := windows.CreateService(b.handle, name, name, rights, windows.SERVICE_WIN32_OWN_PROCESS, windows.SERVICE_DEMAND_START, windows.SERVICE_ERROR_NORMAL, command, nil, nil, nil, account, nil)
	if err != nil {
		return nil, err
	}
	return &nativeService{service: mgr.Service{Name: r.Name, Handle: h}, expected: r}, nil
}

type nativeService struct {
	service  mgr.Service
	expected Registration
}

func (s *nativeService) Close() error  { return s.service.Close() }
func (s *nativeService) Start() error  { return s.service.Start() }
func (s *nativeService) Stop() error   { _, err := s.service.Control(svc.Stop); return err }
func (s *nativeService) Delete() error { return s.service.Delete() }
func (s *nativeService) Query() (Status, error) {
	v, err := s.service.Query()
	return Status{State(v.State), v.Win32ExitCode, v.ServiceSpecificExitCode}, err
}
func (s *nativeService) ConfigurePolicy() error {
	sid := uint32(windows.SERVICE_SID_TYPE_RESTRICTED)
	if err := windows.ChangeServiceConfig2(s.service.Handle, windows.SERVICE_CONFIG_SERVICE_SID_INFO, (*byte)(unsafe.Pointer(&sid))); err != nil {
		return err
	}
	// SERVICE_REQUIRED_PRIVILEGES_INFO, MULTI_SZ with one privilege and a final NUL.
	privileges := append(windows.StringToUTF16("SeChangeNotifyPrivilege"), 0)
	info := struct{ Privileges *uint16 }{&privileges[0]}
	return windows.ChangeServiceConfig2(s.service.Handle, 6, (*byte)(unsafe.Pointer(&info)))
}
func (s *nativeService) queryPolicy(level uint32) ([]byte, error) {
	size := uint32(1024)
	for {
		b := make([]byte, size)
		err := windows.QueryServiceConfig2(s.service.Handle, level, &b[0], size, &size)
		if err == nil {
			return b, nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || size <= uint32(len(b)) || size > 1024*1024 {
			return nil, err
		}
	}
}
func (s *nativeService) Configuration() (Configuration, error) {
	c, err := s.service.Config()
	if err != nil {
		return Configuration{}, err
	}
	out := Configuration{Account: c.ServiceStartName, DemandStart: c.StartType == windows.SERVICE_DEMAND_START && !c.DelayedAutoStart, OwnProcess: c.ServiceType == windows.SERVICE_WIN32_OWN_PROCESS, RestrictedSID: c.SidType == windows.SERVICE_SID_TYPE_RESTRICTED, NoDependencies: len(c.Dependencies) == 0 && c.LoadOrderGroup == "" && c.TagId == 0}
	if c.BinaryPathName == windows.ComposeCommandLine(s.expected.Arguments()) && s.service.Name == s.expected.Name {
		out.Registration = s.expected
	}
	if c.ErrorControl != windows.SERVICE_ERROR_NORMAL {
		return out, errors.New("unexpected service error policy")
	}
	privileges, err := s.queryPolicy(6)
	if err != nil {
		return out, err
	}
	ptr := *(**uint16)(unsafe.Pointer(&privileges[0]))
	// Validate the whole MULTI_SZ, not merely the first privilege.
	if ptr != nil {
		base := uintptr(unsafe.Pointer(&privileges[0]))
		start := uintptr(unsafe.Pointer(ptr))
		end := base + uintptr(len(privileges))
		if start < base || start >= end || start%2 != 0 {
			return out, fmt.Errorf("invalid privilege buffer")
		}
		words := unsafe.Slice(ptr, int((end-start)/2))
		want := append(windows.StringToUTF16("SeChangeNotifyPrivilege"), 0)
		if len(words) >= len(want) {
			out.MinimalPrivileges = true
			for i, v := range want {
				if words[i] != v {
					out.MinimalPrivileges = false
					break
				}
			}
		}
	}
	recovery, err := s.queryPolicy(windows.SERVICE_CONFIG_FAILURE_ACTIONS)
	if err != nil {
		return out, err
	}
	f := (*windows.SERVICE_FAILURE_ACTIONS)(unsafe.Pointer(&recovery[0]))
	out.NoRecovery = f.ActionsCount == 0 && windows.UTF16PtrToString(f.Command) == "" && windows.UTF16PtrToString(f.RebootMsg) == ""
	triggers, err := s.queryPolicy(8) // SERVICE_CONFIG_TRIGGER_INFO
	if err != nil {
		return out, err
	}
	out.NoTriggers = *(*uint32)(unsafe.Pointer(&triggers[0])) == 0
	return out, nil
}
