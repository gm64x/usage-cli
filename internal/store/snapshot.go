package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gm64x/usage-cli/internal/domain"
)

// SnapshotStore persists and loads per-origin QuotaSnapshot shards.
type SnapshotStore struct {
	stateDir  string
	origin    string
	locker    *Locker
	fastCache *FastCache
}

// NewSnapshotStore creates a SnapshotStore bound to stateDir, origin, locker, and optional fastCache.
func NewSnapshotStore(stateDir, origin string, locker *Locker, fastCache *FastCache) *SnapshotStore {
	return &SnapshotStore{
		stateDir:  stateDir,
		origin:    origin,
		locker:    locker,
		fastCache: fastCache,
	}
}

// ProviderDir returns the directory containing origin shards for provider.
func (s *SnapshotStore) ProviderDir(provider domain.Provider) string {
	return filepath.Join(s.stateDir, "snapshots", string(provider))
}

// ShardPath returns the path to this origin's snapshot shard for provider.
func (s *SnapshotStore) ShardPath(provider domain.Provider) string {
	return filepath.Join(s.ProviderDir(provider), s.origin+".json")
}

// SaveShard atomically persists snap to this origin's shard and invalidates the
// provider fast-cache under the intra-kernel lock.
func (s *SnapshotStore) SaveShard(snap domain.QuotaSnapshot) error {
	if snap.Provider == "" {
		return errors.New("snapshot provider is required")
	}
	if snap.SchemaVersion == "" {
		snap.SchemaVersion = domain.SchemaVersion
	}
	if snap.ContractVersion == "" {
		snap.ContractVersion = domain.ContractVersion
	}
	if snap.Origin == "" {
		snap.Origin = s.origin
	}
	if len(snap.Samples) > domain.MaxUsageSamples {
		snap.Samples = snap.Samples[len(snap.Samples)-domain.MaxUsageSamples:]
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	data = append(data, '\n')

	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	if err := WriteAtomic(s.ShardPath(snap.Provider), s.origin, data, 0o600); err != nil {
		return err
	}

	if s.fastCache != nil {
		if err := s.fastCache.Invalidate(snap.Provider); err != nil {
			return fmt.Errorf("invalidate fast cache: %w", err)
		}
	}
	return nil
}

// LoadAllShards reads all finalized snapshot shards for provider using shared-delete I/O.
func (s *SnapshotStore) LoadAllShards(provider domain.Provider) ([]domain.QuotaSnapshot, error) {
	dir := s.ProviderDir(provider)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot dir %q: %w", dir, err)
	}

	shards := make([]domain.QuotaSnapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !IsValidShardFile(entry.Name()) {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		data, err := ReadFileShared(fullPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read snapshot shard %q: %w", fullPath, err)
		}
		var snap domain.QuotaSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot shard %q: %w", fullPath, err)
		}
		shards = append(shards, snap)
	}

	sort.Slice(shards, func(i, j int) bool {
		return shards[i].Origin < shards[j].Origin
	})
	return shards, nil
}
