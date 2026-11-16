package cli_test

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// privateConsoleCommand runs only disposable fixture executables. All arguments
// must be nonsecret. Returned strings can contain secrets, including stdout or
// stderr if production regresses: callers must never print any capture.
// dir must be a test-owned temporary directory. No capture is written to disk.
//
// Native setup follows Microsoft's Creating a Pseudoconsole Session and
// UpdateProcThreadAttribute documentation. STARTF_USESTDHANDLES plus an explicit
// HANDLE_LIST separates routine channels from writes opening CONOUT$ directly.
func privateConsoleCommand(t *testing.T, dir, executable string, args ...string) (console, stdout, stderr string, exitCode int) {
	return privateConsoleInteraction(t, dir, executable, nil, args...)
}

// privateConsoleInteraction sends input only to this disposable child pseudoconsole.
// The callback reads sanitized assertions from memory; never print its captures.
func privateConsoleInteraction(t *testing.T, dir, executable string, interact func(*os.File, func() string), args ...string) (console, stdout, stderr string, exitCode int) {
	t.Helper()
	kernel := syscall.NewLazyDLL("kernel32.dll")
	createConsole := kernel.NewProc("CreatePseudoConsole")
	closeConsole := kernel.NewProc("ClosePseudoConsole")
	initialize := kernel.NewProc("InitializeProcThreadAttributeList")
	update := kernel.NewProc("UpdateProcThreadAttribute")
	deleteList := kernel.NewProc("DeleteProcThreadAttributeList")
	peek := kernel.NewProc("PeekNamedPipe")
	for _, proc := range []*syscall.LazyProc{createConsole, closeConsole, initialize, update, deleteList, peek} {
		if proc.Find() != nil {
			t.Fatal("private console support unavailable")
		}
	}
	pipe := func() (*os.File, *os.File) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal("private console pipe creation failed")
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		return r, w
	}
	inR, inW := pipe()
	defer inR.Close()
	defer inW.Close()
	consoleR, consoleW := pipe()
	defer consoleR.Close()
	defer consoleW.Close()
	outR, outW := pipe()
	defer outR.Close()
	defer outW.Close()
	errR, errW := pipe()
	defer errR.Close()
	defer errW.Close()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal("private console input setup failed")
	}
	defer stdin.Close()

	// One sole reader per pipe only reads bytes reported available by Peek.
	// This avoids uncancellable synchronous ReadFile calls and allows cleanup to
	// join every reader even if a misbehaving child retains an inherited handle.
	type capture struct {
		data string
		bad  bool
	}
	stop := make(chan struct{})
	startReader := func(file *os.File, live *safeCapture) <-chan capture {
		done := make(chan capture, 1)
		go func() {
			var buffer bytes.Buffer
			bad := false
			defer func() { done <- capture{buffer.String(), bad} }()
			var scratch [4096]byte
			for {
				select {
				case <-stop:
					return
				default:
				}
				var available uint32
				ok, _, e := peek.Call(file.Fd(), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
				if ok == 0 {
					bad = bad || e != syscall.ERROR_BROKEN_PIPE
					return
				}
				if available == 0 {
					time.Sleep(5 * time.Millisecond)
					continue
				}
				if available > uint32(len(scratch)) {
					available = uint32(len(scratch))
				}
				var n uint32
				if syscall.ReadFile(syscall.Handle(file.Fd()), scratch[:available], &n, nil) != nil {
					bad = true
					return
				}
				// Continue draining after the cap to avoid blocking console shutdown.
				if buffer.Len()+int(n) <= 1<<20 {
					buffer.Write(scratch[:n])
					if live != nil {
						_, _ = live.Write(scratch[:n])
					}
				} else {
					bad = true
				}
			}
		}()
		return done
	}
	liveErrors := &safeCapture{}
	readers := []<-chan capture{startReader(consoleR, nil), startReader(outR, nil), startReader(errR, liveErrors)}
	joined := make([]bool, len(readers))
	defer func() {
		close(stop)
		for i, reader := range readers {
			if !joined[i] {
				<-reader
			}
		}
	}()

	var pc syscall.Handle
	// COORD is passed by value: width 120, height 40; no cursor inheritance.
	hr, _, _ := createConsole.Call(120|(40<<16), inR.Fd(), consoleW.Fd(), 0, uintptr(unsafe.Pointer(&pc)))
	if int32(hr) < 0 {
		t.Fatal("private console creation failed")
	}
	defer func() {
		if pc != 0 {
			closeConsole.Call(uintptr(pc))
		}
	}()

	var size uintptr
	initialize.Call(0, 2, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 || size > 1<<20 {
		t.Fatal("private console attribute sizing failed")
	}
	// uintptr storage provides native pointer alignment for the opaque list.
	storage := make([]uintptr, (size+unsafe.Sizeof(uintptr(0))-1)/unsafe.Sizeof(uintptr(0)))
	list := unsafe.Pointer(&storage[0])
	if ok, _, _ := initialize.Call(uintptr(list), 2, 0, uintptr(unsafe.Pointer(&size))); ok == 0 {
		t.Fatal("private console attribute initialization failed")
	}
	defer deleteList.Call(uintptr(list))
	if ok, _, _ := update.Call(uintptr(list), 0, 0x00020016, uintptr(pc), unsafe.Sizeof(pc), 0, 0); ok == 0 {
		t.Fatal("private console attachment setup failed")
	}
	handles := []syscall.Handle{syscall.Handle(stdin.Fd()), syscall.Handle(outW.Fd()), syscall.Handle(errW.Fd())}
	// Serialize the brief inheritable-handle window with Go's process launcher.
	// Otherwise another concurrently launched process could inherit a writer.
	syscall.ForkLock.Lock()
	inheritanceLocked := true
	defer func() {
		if inheritanceLocked {
			for _, handle := range handles {
				syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, 0)
			}
			syscall.ForkLock.Unlock()
		}
	}()
	for _, handle := range handles {
		if syscall.SetHandleInformation(handle, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT) != nil {
			t.Fatal("private console standard handle setup failed")
		}
	}
	if ok, _, _ := update.Call(uintptr(list), 0, 0x00020002, uintptr(unsafe.Pointer(&handles[0])), uintptr(len(handles))*unsafe.Sizeof(handles[0]), 0, 0); ok == 0 {
		t.Fatal("private console handle inheritance setup failed")
	}
	type startupInfoEx struct {
		syscall.StartupInfo
		attributes unsafe.Pointer
	}
	si := startupInfoEx{attributes: list}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]
	toUTF16 := func(value string) *uint16 {
		p, e := syscall.UTF16PtrFromString(value)
		if e != nil {
			t.Fatal("private console process arguments invalid")
		}
		return p
	}
	quoted := []string{syscall.EscapeArg(executable)}
	for _, arg := range args {
		quoted = append(quoted, syscall.EscapeArg(arg))
	}
	var process syscall.ProcessInformation
	err = syscall.CreateProcess(toUTF16(executable), toUTF16(strings.Join(quoted, " ")), nil, nil, true, 0x00080000, nil, toUTF16(dir), &si.StartupInfo, &process)
	runtime.KeepAlive(storage)
	runtime.KeepAlive(handles)
	if err != nil {
		t.Fatal("private console process creation failed")
	}
	syscall.CloseHandle(process.Thread)
	defer syscall.CloseHandle(process.Process)
	defer func() {
		if state, _ := syscall.WaitForSingleObject(process.Process, 0); state != syscall.WAIT_OBJECT_0 {
			syscall.TerminateProcess(process.Process, 1)
			syscall.WaitForSingleObject(process.Process, 5000)
		}
	}()
	inR.Close()
	consoleW.Close()
	outW.Close()
	errW.Close()
	stdin.Close()
	syscall.ForkLock.Unlock()
	inheritanceLocked = false
	if interact != nil {
		interact(inW, liveErrors.String)
	}
	state, waitErr := syscall.WaitForSingleObject(process.Process, 30000)
	if waitErr != nil || state != syscall.WAIT_OBJECT_0 {
		t.Fatal("private console process exceeded deadline or could not be waited")
	}
	var code uint32
	if syscall.GetExitCodeProcess(process.Process, &code) != nil {
		t.Fatal("private console exit status unavailable")
	}
	// Keep readers alive through ClosePseudoConsole and until EOF, including on
	// Windows 11 24H2 where closing the console returns before final output.
	closeConsole.Call(uintptr(pc))
	pc = 0
	inW.Close()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	results := make([]capture, len(readers))
	for i, reader := range readers {
		select {
		case results[i] = <-reader:
			joined[i] = true
			if results[i].bad {
				t.Fatal("private console capture failed or exceeded size limit")
			}
		case <-deadline.C:
			t.Fatal("private console capture exceeded cleanup deadline")
		}
	}
	return results[0].data, results[1].data, results[2].data, int(code)
}
