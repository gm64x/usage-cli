package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gofrs/flock"
)

const (
	// DefaultLockTimeout is the bounded acquisition timeout for intra-kernel locks.
	DefaultLockTimeout = 300 * time.Millisecond
	lockPollInterval   = 5 * time.Millisecond
)

// Locker serializes concurrent writers within the same OS kernel using an flock file.
type Locker struct {
	lockPath string
	timeout  time.Duration
}

// NewLocker constructs a Locker targeting lockDir. On Windows it uses win.lock;
// on Linux/WSL it uses wsl.lock (which callers must place on native ext4/tmpfs).
func NewLocker(lockDir string, timeout time.Duration) *Locker {
	if timeout <= 0 {
		timeout = DefaultLockTimeout
	}
	lockName := "wsl.lock"
	if runtime.GOOS == "windows" {
		lockName = "win.lock"
	}
	return &Locker{
		lockPath: filepath.Join(lockDir, lockName),
		timeout:  timeout,
	}
}

// LockPath returns the underlying lock file path.
func (l *Locker) LockPath() string {
	return l.lockPath
}

// Acquire acquires the intra-kernel lock within the bounded timeout and returns
// an unlock function that must be called when the critical section completes.
func (l *Locker) Acquire() (func() error, error) {
	dir := filepath.Dir(l.lockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory %q: %w", dir, err)
	}

	fl := flock.New(l.lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	locked, err := fl.TryLockContext(ctx, lockPollInterval)
	if err != nil {
		return nil, fmt.Errorf("acquire lock %q: %w", l.lockPath, err)
	}
	if !locked {
		return nil, fmt.Errorf("acquire lock %q: timed out after %s", l.lockPath, l.timeout)
	}

	return fl.Unlock, nil
}
