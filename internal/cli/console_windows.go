package cli

import (
	"errors"
	"os"
	"syscall"
)

// openPrivateConsole deliberately ignores stdout and stderr, which can be
// redirected to ordinary logs. A process without a real console fails closed.
func openPrivateConsole() (*os.File, error) {
	// Windows can provide console device handles to CREATE_NO_WINDOW children
	// even though no interactive console exists. A ConPTY has a message-only
	// console window handle; a headless no-window process has none.
	window, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	if window == 0 {
		return nil, errors.New("no interactive console")
	}
	// GetConsoleMode requires read access even though delivery only writes.
	f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	var mode uint32
	if err := syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
