package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
	"github.com/gm64x/usage-cli/internal/security"
)

// HealthStore manages per-origin hook error diagnostics at health/last-error.<origin>.json.
type HealthStore struct {
	stateDir string
	origin   string
	locker   *Locker
}

// NewHealthStore creates a HealthStore rooted at stateDir for origin.
func NewHealthStore(stateDir, origin string, locker *Locker) *HealthStore {
	return &HealthStore{
		stateDir: stateDir,
		origin:   origin,
		locker:   locker,
	}
}

// HealthDir returns the health directory path.
func (s *HealthStore) HealthDir() string {
	return filepath.Join(s.stateDir, "health")
}

// ErrorPath returns this origin's last-error file path.
func (s *HealthStore) ErrorPath() string {
	return filepath.Join(s.HealthDir(), fmt.Sprintf("last-error.%s.json", s.origin))
}

// RecordHookError redacts errMsg and persists the latest hook error shard under lock.
func (s *HealthStore) RecordHookError(hookName string, provider domain.Provider, errMsg string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	redactedMsg := security.Redact(errMsg)

	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	count := 1
	path := s.ErrorPath()
	if data, err := ReadFileShared(path); err == nil {
		var prev domain.HookErrorHealth
		if json.Unmarshal(data, &prev) == nil && prev.HookName == hookName && prev.Message == redactedMsg {
			count = prev.Count + 1
		}
	}

	rec := domain.HookErrorHealth{
		SchemaVersion: domain.SchemaVersion,
		Origin:        s.origin,
		HookName:      hookName,
		Provider:      provider,
		Message:       redactedMsg,
		OccurredAt:    now.UTC(),
		Count:         count,
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hook error health: %w", err)
	}
	data = append(data, '\n')
	return WriteAtomic(path, s.origin, data, 0o600)
}

// ReadRecentErrors loads all last-error.<origin>.json shards within maxAge (if > 0).
func (s *HealthStore) ReadRecentErrors(maxAge time.Duration, now time.Time) ([]domain.HookErrorHealth, error) {
	dir := s.HealthDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read health dir %q: %w", dir, err)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	records := make([]domain.HookErrorHealth, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !IsValidShardFile(name) || !strings.HasPrefix(name, "last-error.") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		data, err := ReadFileShared(fullPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read health shard %q: %w", fullPath, err)
		}
		var rec domain.HookErrorHealth
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("unmarshal health shard %q: %w", fullPath, err)
		}
		if maxAge > 0 && now.Sub(rec.OccurredAt) > maxAge {
			continue
		}
		records = append(records, rec)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Origin < records[j].Origin
	})
	return records, nil
}

// ClearError removes this origin's last-error shard if present.
func (s *HealthStore) ClearError() error {
	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	err := os.Remove(s.ErrorPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
