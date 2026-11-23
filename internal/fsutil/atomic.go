package fsutil

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultRenameMaxWait is the bounded retry duration for atomic renames on NTFS.
	DefaultRenameMaxWait = 450 * time.Millisecond
	initialRenameBackoff = 5 * time.Millisecond
	maxRenameBackoff     = 160 * time.Millisecond
)

// WriteAtomic writes data to a unique temp file in the target directory, syncs and closes it,
// and atomically renames it onto path with bounded exponential-backoff retry.
func WriteAtomic(path string, origin string, data []byte, perm os.FileMode) error {
	if origin == "" {
		origin = "unknown"
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %q: %w", dir, err)
	}

	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("generate temp filename: %w", err)
	}

	tmpName := fmt.Sprintf(".tmp-%s-%d-%s", origin, os.Getpid(), hex.EncodeToString(rnd[:]))
	tmpPath := filepath.Join(dir, tmpName)

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("create temp file %q: %w", tmpPath, err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write temp file %q: %w", tmpPath, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync temp file %q: %w", tmpPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp file %q: %w", tmpPath, err)
	}

	if err := RetryRename(tmpPath, path, DefaultRenameMaxWait); err != nil {
		return fmt.Errorf("rename %q to %q: %w", tmpPath, path, err)
	}

	committed = true
	return nil
}

// RetryRename renames src to dst with exponential backoff and jitter up to maxWait
// to tolerate transient Windows sharing/AV locks on NTFS.
func RetryRename(src, dst string, maxWait time.Duration) error {
	if maxWait <= 0 {
		maxWait = DefaultRenameMaxWait
	}

	start := time.Now()
	backoff := initialRenameBackoff

	for {
		err := os.Rename(src, dst)
		if err == nil {
			return nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return err
		}

		elapsed := time.Since(start)
		if elapsed >= maxWait {
			return err
		}

		sleepDur := backoff + jitterDuration(5*time.Millisecond)
		if remaining := maxWait - elapsed; sleepDur > remaining {
			sleepDur = remaining
		}
		time.Sleep(sleepDur)

		backoff *= 2
		if backoff > maxRenameBackoff {
			backoff = maxRenameBackoff
		}
	}
}

func jitterDuration(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxJitter)))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}
