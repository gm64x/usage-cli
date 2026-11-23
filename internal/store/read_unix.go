//go:build !windows

package store

import (
	"fmt"
	"io"

	"github.com/gm64x/usage-cli/internal/security"
)

// ReadFileShared opens path strictly read-only on Unix systems, where unlink/rename
// over an open file descriptor is natively supported.
func ReadFileShared(path string) ([]byte, error) {
	f, err := security.OpenReadOnly(path)
	if err != nil {
		return nil, err
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
