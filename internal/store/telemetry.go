package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gm64x/usage-cli/internal/security"
)

const (
	// DefaultTelemetryMaxBytes is the default 2 MiB size threshold for JSONL rotation.
	DefaultTelemetryMaxBytes int64 = 2 * 1024 * 1024
	// DefaultTelemetryMaxBackups is the default 2 rotated backup generations.
	DefaultTelemetryMaxBackups = 2
)

// TelemetryStore appends sanitized TelemetryEvent JSONL records under lock with size rotation.
type TelemetryStore struct {
	stateDir   string
	locker     *Locker
	maxBytes   int64
	maxBackups int
}

// NewTelemetryStore creates a TelemetryStore rooted at stateDir.
func NewTelemetryStore(stateDir string, locker *Locker, maxBytes int64, maxBackups int) *TelemetryStore {
	if maxBytes <= 0 {
		maxBytes = DefaultTelemetryMaxBytes
	}
	if maxBackups <= 0 {
		maxBackups = DefaultTelemetryMaxBackups
	}
	return &TelemetryStore{
		stateDir:   stateDir,
		locker:     locker,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}
}

// FilePath returns the active telemetry JSONL file path.
func (s *TelemetryStore) FilePath() string {
	return filepath.Join(s.stateDir, "telemetry", "events.jsonl")
}

// Append sanitizes ev, rotates events.jsonl if appending would exceed maxBytes,
// and appends the JSON line under the intra-kernel lock.
func (s *TelemetryStore) Append(ev security.TelemetryEvent) error {
	sanitized := security.SanitizeTelemetry(ev)
	line, err := json.Marshal(sanitized)
	if err != nil {
		return fmt.Errorf("marshal telemetry event: %w", err)
	}
	line = append(line, '\n')

	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	path := s.FilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create telemetry directory %q: %w", dir, err)
	}

	if info, err := os.Stat(path); err == nil {
		if info.Size()+int64(len(line)) > s.maxBytes {
			if rotErr := s.rotateLocked(path); rotErr != nil {
				return fmt.Errorf("rotate telemetry log: %w", rotErr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat telemetry log %q: %w", path, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open telemetry log %q: %w", path, err)
	}

	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("write telemetry log: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync telemetry log: %w", err)
	}
	return f.Close()
}

func (s *TelemetryStore) rotateLocked(path string) error {
	oldest := fmt.Sprintf("%s.%d", path, s.maxBackups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	for i := s.maxBackups - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", path, i)
		dst := fmt.Sprintf("%s.%d", path, i+1)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := RetryRename(src, dst, defaultRenameMaxWait); err != nil {
			return err
		}
	}

	return RetryRename(path, path+".1", defaultRenameMaxWait)
}
