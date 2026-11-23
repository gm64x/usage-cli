package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
)

const (
	// FastCacheMaxTTL is the maximum age of a valid fast-path cache entry.
	FastCacheMaxTTL = 4 * time.Second
	// FastCacheMaxUsedPercent restricts fast-path caching strictly below 55% usage.
	FastCacheMaxUsedPercent = 55.0
	// FastCacheMaxBurnRate restricts fast-path caching to low burn rates (< 1.0%/min).
	FastCacheMaxBurnRate = 1.0
)

// FastCacheEntry holds an ultra-fast hook evaluation cache entry in /dev/shm (or user cache).
type FastCacheEntry struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Provider      domain.Provider        `json:"provider"`
	CachedAt      time.Time              `json:"cachedAt"`
	UsedPercent   float64                `json:"usedPercent"`
	BurnRate      float64                `json:"burnRate"`
	GateOK        bool                   `json:"gateOk"`
	Confidence    domain.Confidence      `json:"confidence"`
	Quota         domain.AggregatedQuota `json:"quota"`
}

// IsEligibleForWrite reports whether the entry satisfies the non-time fast-cache criteria.
func (e FastCacheEntry) IsEligibleForWrite() bool {
	return e.UsedPercent < FastCacheMaxUsedPercent &&
		e.BurnRate < FastCacheMaxBurnRate &&
		e.GateOK &&
		e.Confidence == domain.ConfidenceExact
}

// IsValid reports whether the entry is eligible and within the <= 4s TTL at now.
func (e FastCacheEntry) IsValid(now time.Time) bool {
	if e.SchemaVersion != domain.SchemaVersion {
		return false
	}
	if !e.IsEligibleForWrite() {
		return false
	}
	age := now.Sub(e.CachedAt)
	return age >= 0 && age <= FastCacheMaxTTL
}

// FastCache manages per-provider fast-path cache files.
type FastCache struct {
	cacheDir string
	origin   string
}

// NewFastCache creates a FastCache rooted at cacheDir.
func NewFastCache(cacheDir, origin string) *FastCache {
	return &FastCache{
		cacheDir: cacheDir,
		origin:   origin,
	}
}

// Path returns the fast-cache file path for provider.
func (c *FastCache) Path(provider domain.Provider) string {
	return filepath.Join(c.cacheDir, string(provider)+".fast.json")
}

// Get returns a valid FastCacheEntry if present and all TTL/threshold guards pass.
func (c *FastCache) Get(provider domain.Provider, now time.Time) (FastCacheEntry, bool) {
	if c == nil || c.cacheDir == "" {
		return FastCacheEntry{}, false
	}
	data, err := ReadFileShared(c.Path(provider))
	if err != nil {
		return FastCacheEntry{}, false
	}
	var entry FastCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return FastCacheEntry{}, false
	}
	if !entry.IsValid(now) {
		return FastCacheEntry{}, false
	}
	return entry, true
}

// Set atomically writes entry if eligible, or invalidates the cache if ineligible.
func (c *FastCache) Set(entry FastCacheEntry) error {
	if c == nil || c.cacheDir == "" {
		return nil
	}
	if !entry.IsEligibleForWrite() {
		return c.Invalidate(entry.Provider)
	}
	if entry.SchemaVersion == "" {
		entry.SchemaVersion = domain.SchemaVersion
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return WriteAtomic(c.Path(entry.Provider), c.origin, data, 0o600)
}

// Invalidate removes the fast-cache entry for provider if it exists.
func (c *FastCache) Invalidate(provider domain.Provider) error {
	if c == nil || c.cacheDir == "" {
		return nil
	}
	err := os.Remove(c.Path(provider))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
