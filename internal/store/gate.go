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
)

const (
	// DefaultGateIdleTimeout is the default 45s NTFS mtime idle expiry for AccountGate shards.
	DefaultGateIdleTimeout = 45 * time.Second
)

// GateRecord pairs a parsed AccountGate shard with its NTFS file modification timestamp.
type GateRecord struct {
	Gate  domain.AccountGate
	Mtime time.Time
	Path  string
}

// GateInspection summarizes active AccountGate evaluation using an on-demand NTFS probe file.
type GateInspection struct {
	Active         bool
	Winner         *domain.AccountGate
	WinnerMtime    time.Time
	ProbeTime      time.Time
	ProbeWritten   bool
	ActiveGates    []GateRecord
	ExpiredRemoved int
}

// GateAcquireResult reports whether gate acquisition succeeded or was blocked by an active holder.
type GateAcquireResult struct {
	Acquired bool
	Holder   *domain.AccountGate
}

// AccountGateStore manages cross-origin AccountGate shards (gate/<provider>.<origin>.json)
// using NTFS mtime against an on-demand probe file (gate/.probe.<origin>).
type AccountGateStore struct {
	stateDir    string
	origin      string
	locker      *Locker
	idleTimeout time.Duration
}

// NewAccountGateStore creates an AccountGateStore. When idleTimeout <= 0, it defaults to 45s.
func NewAccountGateStore(stateDir, origin string, locker *Locker, idleTimeout time.Duration) *AccountGateStore {
	if idleTimeout <= 0 {
		idleTimeout = DefaultGateIdleTimeout
	}
	return &AccountGateStore{
		stateDir:    stateDir,
		origin:      origin,
		locker:      locker,
		idleTimeout: idleTimeout,
	}
}

// GateDir returns the gate storage directory.
func (s *AccountGateStore) GateDir() string {
	return filepath.Join(s.stateDir, "gate")
}

// GatePath returns the gate shard path for provider and this origin.
func (s *AccountGateStore) GatePath(provider domain.Provider) string {
	return filepath.Join(s.GateDir(), fmt.Sprintf("%s.%s.json", provider, s.origin))
}

// ProbePath returns the on-demand NTFS probe path for this origin.
func (s *AccountGateStore) ProbePath() string {
	return filepath.Join(s.GateDir(), ".probe."+s.origin)
}

// Inspect checks active AccountGate shards for provider.
// When zero gate shards exist, it performs ZERO disk writes (never creating .probe.<origin>).
// When at least one gate shard exists, it writes .probe.<origin> to measure NTFS mtime delta.
func (s *AccountGateStore) Inspect(provider domain.Provider, currentWindowResetAt time.Time) (GateInspection, error) {
	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return GateInspection{}, err
		}
		defer func() {
			_ = unlock()
		}()
	}
	return s.inspectLocked(provider, currentWindowResetAt)
}

// Acquire attempts to acquire or renew the AccountGate for gate.Provider.
func (s *AccountGateStore) Acquire(gate domain.AccountGate, currentWindowResetAt time.Time) (GateAcquireResult, error) {
	if gate.Provider == "" {
		return GateAcquireResult{}, errors.New("gate provider is required")
	}
	if gate.HolderSessionID == "" {
		return GateAcquireResult{}, errors.New("gate holderSessionId is required")
	}
	if gate.SchemaVersion == "" {
		gate.SchemaVersion = domain.SchemaVersion
	}
	if gate.Origin == "" {
		gate.Origin = s.origin
	}

	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return GateAcquireResult{}, err
		}
		defer func() {
			_ = unlock()
		}()
	}

	inspection, err := s.inspectLocked(gate.Provider, currentWindowResetAt)
	if err != nil {
		return GateAcquireResult{}, err
	}
	if inspection.Active && inspection.Winner != nil && inspection.Winner.HolderSessionID != gate.HolderSessionID {
		return GateAcquireResult{
			Acquired: false,
			Holder:   inspection.Winner,
		}, nil
	}

	data, err := json.MarshalIndent(gate, "", "  ")
	if err != nil {
		return GateAcquireResult{}, fmt.Errorf("marshal account gate: %w", err)
	}
	data = append(data, '\n')

	if err := WriteAtomic(s.GatePath(gate.Provider), s.origin, data, 0o600); err != nil {
		return GateAcquireResult{}, err
	}

	return GateAcquireResult{
		Acquired: true,
		Holder:   &gate,
	}, nil
}

// Touch refreshes the gate shard's mtime while held by sessionID.
func (s *AccountGateStore) Touch(provider domain.Provider, sessionID string) error {
	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	path := s.GatePath(provider)
	data, err := ReadFileShared(path)
	if err != nil {
		return err
	}
	var gate domain.AccountGate
	if err := json.Unmarshal(data, &gate); err != nil {
		return fmt.Errorf("unmarshal gate %q: %w", path, err)
	}
	if sessionID != "" && gate.HolderSessionID != sessionID {
		return fmt.Errorf("gate held by %q, not %q", gate.HolderSessionID, sessionID)
	}

	return WriteAtomic(path, s.origin, data, 0o600)
}

// Release removes this origin's gate shard if held by sessionID (or unconditionally if sessionID == "").
func (s *AccountGateStore) Release(provider domain.Provider, sessionID string) error {
	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}

	path := s.GatePath(provider)
	data, err := ReadFileShared(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var gate domain.AccountGate
	if err := json.Unmarshal(data, &gate); err != nil {
		return fmt.Errorf("unmarshal gate %q: %w", path, err)
	}
	if sessionID != "" && gate.HolderSessionID != sessionID {
		return nil
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *AccountGateStore) inspectLocked(provider domain.Provider, currentWindowResetAt time.Time) (GateInspection, error) {
	gateDir := s.GateDir()
	entries, err := os.ReadDir(gateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return GateInspection{Active: false}, nil
		}
		return GateInspection{}, fmt.Errorf("read gate dir %q: %w", gateDir, err)
	}

	prefix := string(provider) + "."
	candidatePaths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".probe.") || !IsValidShardFile(name) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		candidatePaths = append(candidatePaths, filepath.Join(gateDir, name))
	}

	// Zero active gate shards -> zero disk writes (never touch .probe.<origin>).
	if len(candidatePaths) == 0 {
		return GateInspection{Active: false}, nil
	}

	probeTime, err := s.writeAndStatProbe()
	if err != nil {
		return GateInspection{}, err
	}

	active := make([]GateRecord, 0, len(candidatePaths))
	expiredRemoved := 0

	for _, path := range candidatePaths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return GateInspection{}, fmt.Errorf("stat gate %q: %w", path, err)
		}
		gateMtime := info.ModTime()

		data, err := ReadFileShared(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return GateInspection{}, fmt.Errorf("read gate %q: %w", path, err)
		}
		var gate domain.AccountGate
		if err := json.Unmarshal(data, &gate); err != nil {
			return GateInspection{}, fmt.Errorf("unmarshal gate %q: %w", path, err)
		}

		// Invalidate if snapshot rolled into a newer window (ResetsAt > gate.WindowResetAt + 120s).
		if !currentWindowResetAt.IsZero() && !gate.WindowResetAt.IsZero() &&
			currentWindowResetAt.Sub(gate.WindowResetAt) > domain.WindowResetSkewTolerance {
			if rmErr := os.Remove(path); rmErr == nil {
				expiredRemoved++
			}
			continue
		}

		// Invalidate if idle timeout exceeded via NTFS mtime delta (probeTime - gateMtime > idleTimeout).
		if probeTime.Sub(gateMtime) > s.idleTimeout {
			if rmErr := os.Remove(path); rmErr == nil {
				expiredRemoved++
			}
			continue
		}

		active = append(active, GateRecord{
			Gate:  gate,
			Mtime: gateMtime,
			Path:  path,
		})
	}

	if len(active) == 0 {
		return GateInspection{
			Active:         false,
			ProbeTime:      probeTime,
			ProbeWritten:   true,
			ExpiredRemoved: expiredRemoved,
		}, nil
	}

	// Deterministic cross-OS tie-breaker: earliest NTFS mtime, then lexicographical HolderSessionID.
	sort.Slice(active, func(i, j int) bool {
		if !active[i].Mtime.Equal(active[j].Mtime) {
			return active[i].Mtime.Before(active[j].Mtime)
		}
		return active[i].Gate.HolderSessionID < active[j].Gate.HolderSessionID
	})

	winner := active[0].Gate
	return GateInspection{
		Active:         true,
		Winner:         &winner,
		WinnerMtime:    active[0].Mtime,
		ProbeTime:      probeTime,
		ProbeWritten:   true,
		ActiveGates:    active,
		ExpiredRemoved: expiredRemoved,
	}, nil
}

func (s *AccountGateStore) writeAndStatProbe() (time.Time, error) {
	probePath := s.ProbePath()
	if err := WriteAtomic(probePath, s.origin, []byte("probe\n"), 0o600); err != nil {
		return time.Time{}, fmt.Errorf("write probe %q: %w", probePath, err)
	}
	info, err := os.Stat(probePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("stat probe %q: %w", probePath, err)
	}
	return info.ModTime(), nil
}
