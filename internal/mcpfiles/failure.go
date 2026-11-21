package mcpfiles

import (
	"context"
	"errors"

	"github.com/jpconstantineau/GoDav-Prototype/internal/files"
)

// Failure returns a stable code and redacted, operation-specific guidance.
// Never return the underlying error, which may contain implementation detail.
func Failure(tool string, err error) (string, string) {
	switch {
	case errors.Is(err, files.ErrExists), tool == "create_file" && errors.Is(err, files.ErrPrecondition):
		return "already_exists", "A file or folder already exists at that path. Choose a different name, or use read_file and its current ETag before editing an existing file."
	case errors.Is(err, files.ErrPrecondition):
		return "precondition_failed", "The file version no longer matches. Use read_file to inspect the current contents and ETag before deciding whether to try again."
	case errors.Is(err, files.ErrLocked), errors.Is(err, files.ErrLockConflict), errors.Is(err, files.ErrLockOwner), errors.Is(err, files.ErrLockScope):
		return "locked", "A WebDAV lock blocks this change. Wait for the lock to be released; do not try to bypass it."
	case errors.Is(err, files.ErrParentMissing):
		return "parent_not_found", "The parent folder does not exist. Use list_files to check the path or create the folder through WebDAV first."
	case errors.Is(err, files.ErrMissing):
		return "not_found", "The requested file or folder was not found in the configured file library. Use list_files to check its path."
	case errors.Is(err, files.ErrTooLarge):
		return "too_large", "The file exceeds this tool's byte limit. Use a smaller text file, or use WebDAV if the file fits the server's file limit."
	case errors.Is(err, errNotText):
		return "unsupported_content", "This tool handles only UTF-8 text without NUL bytes. Use WebDAV for binary files."
	case errors.Is(err, files.ErrInvalid), errors.Is(err, files.ErrInput):
		return "invalid_arguments", "Check the arguments against tools/list, including the relative path, required ETag and file or folder type."
	case errors.Is(err, files.ErrCapacity):
		if tool == "search_file_contents" {
			return "capacity_exceeded", "The folder exceeds the content search budget. Search a smaller subfolder."
		}
		return "capacity_exceeded", "The folder reached a storage limit. Check the folder or choose another location."
	case errors.Is(err, files.ErrBusy), errors.Is(err, files.ErrTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		if !IsWrite(tool) {
			return "unavailable", "The operation did not finish. Try the read again later."
		}
		fallthrough
	default:
		if IsWrite(tool) {
			return "unavailable", "The operation did not return a confirmed result. List the parent or read the file to check its current state before trying another write."
		}
		return "unavailable", "The operation could not be completed. Check the file library and try again later."
	}
}

// Preserve the existing invalid-input error classification for callers of
// the file adapter while giving the wire layer a specific recovery action.
var errNotText = errors.Join(files.ErrInvalid, errors.New("not UTF-8 text"))
