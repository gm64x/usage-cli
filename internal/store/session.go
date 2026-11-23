package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
)

// SessionStore manages per-session state shards named <origin>-<sha256(sessionID)>.json.
type SessionStore struct {
	stateDir string
	origin   string
	locker   *Locker
}

// NewSessionStore creates a SessionStore rooted at stateDir for origin.
func NewSessionStore(stateDir, origin string, locker *Locker) *SessionStore {
	return &SessionStore{
		stateDir: stateDir,
		origin:   origin,
		locker:   locker,
	}
}

// ShardPath returns the deterministic shard file path for sessionID.
func (s *SessionStore) ShardPath(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	hash := hex.EncodeToString(sum[:])
	return filepath.Join(s.stateDir, "sessions", fmt.Sprintf("%s-%s.json", s.origin, hash))
}

// Load reads and parses the SessionState shard for sessionID using shared-delete I/O.
func (s *SessionStore) Load(sessionID string) (domain.SessionState, error) {
	if sessionID == "" {
		return domain.SessionState{}, errors.New("sessionID is required")
	}
	path := s.ShardPath(sessionID)
	data, err := ReadFileShared(path)
	if err != nil {
		return domain.SessionState{}, err
	}
	var st domain.SessionState
	if err := json.Unmarshal(data, &st); err != nil {
		return domain.SessionState{}, fmt.Errorf("unmarshal session %q: %w", path, err)
	}
	return st, nil
}

// Save atomically persists st under the intra-kernel lock.
func (s *SessionStore) Save(st domain.SessionState) error {
	if st.SessionID == "" {
		return errors.New("sessionID is required")
	}
	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return err
		}
		defer func() {
			_ = unlock()
		}()
	}
	return s.saveLocked(st)
}

// Update atomically loads (or initializes) the session state, applies fn, and persists it.
func (s *SessionStore) Update(sessionID string, fn func(*domain.SessionState) error) (domain.SessionState, error) {
	if sessionID == "" {
		return domain.SessionState{}, errors.New("sessionID is required")
	}
	if fn == nil {
		return domain.SessionState{}, errors.New("update callback is required")
	}

	if s.locker != nil {
		unlock, err := s.locker.Acquire()
		if err != nil {
			return domain.SessionState{}, err
		}
		defer func() {
			_ = unlock()
		}()
	}

	st, err := s.Load(sessionID)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return domain.SessionState{}, err
		}
		st = domain.SessionState{
			SchemaVersion: domain.SchemaVersion,
			SessionID:     sessionID,
			Origin:        s.origin,
			Phase:         domain.PhaseOK,
		}
	}

	if err := fn(&st); err != nil {
		return domain.SessionState{}, err
	}
	st.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(st); err != nil {
		return domain.SessionState{}, err
	}
	return st, nil
}

func (s *SessionStore) saveLocked(st domain.SessionState) error {
	if st.SchemaVersion == "" {
		st.SchemaVersion = domain.SchemaVersion
	}
	if st.Origin == "" {
		st.Origin = s.origin
	}
	if st.Phase == "" {
		st.Phase = domain.PhaseOK
	}
	if st.UpdatedAt.IsZero() {
		st.UpdatedAt = time.Now().UTC()
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session state: %w", err)
	}
	data = append(data, '\n')
	return WriteAtomic(s.ShardPath(st.SessionID), s.origin, data, 0o600)
}
