//go:build windows

package store

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// ReadFileShared opens path with FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE
// so concurrent atomic renames or deletes on NTFS do not fail with sharing violations.
func ReadFileShared(path string) ([]byte, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode path %q: %w", path, err)
	}

	shareMode := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		shareMode,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	f := os.NewFile(uintptr(handle), path)
	if f == nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	defer func() {
		_ = f.Close()
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return data, nil
}
