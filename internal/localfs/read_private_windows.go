package localfs

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// ReadPrivateFile reads bounded TLS material from the very handle whose identity
// and owner/DACL are validated, retaining every path component while reading.
func ReadPrivateFile(path string, limit int64) ([]byte, error) {
	return (*Policy)(nil).ReadPrivateFile(path, limit)
}

func (policy *Policy) ReadPrivateFile(path string, limit int64) ([]byte, error) {
	if limit < 1 || limit > 1<<20 {
		return nil, errors.New("invalid private file bound")
	}
	handles, err := openCheckedPathPinned(path, false, false, windows.READ_CONTROL|windows.GENERIC_READ, policy.IsService())
	if err != nil {
		return nil, err
	}
	defer closePathHandles(handles[:len(handles)-1])
	h := handles[len(handles)-1]
	f := os.NewFile(uintptr(h), "private TLS material")
	if f == nil {
		windows.CloseHandle(h)
		return nil, errors.New("private file open failed")
	}
	defer f.Close()
	if err := policy.policyACL(h, false, false, false); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(body)) > limit {
		clear(body)
		return nil, errors.New("private file read failed")
	}
	return body, nil
}
