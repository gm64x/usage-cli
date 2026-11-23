package store

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
	"github.com/gm64x/usage-cli/internal/security"
)

const stressWorkerEnv = "USAGE_CLI_TEST_STRESS_WORKER"

func TestMain(m *testing.M) {
	if os.Getenv(stressWorkerEnv) == "1" {
		if err := runStressWorker(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "stress worker failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runStressWorker() error {
	stateDir := os.Getenv("USAGE_CLI_STRESS_STATE_DIR")
	lockDir := os.Getenv("USAGE_CLI_STRESS_LOCK_DIR")
	origin := os.Getenv("USAGE_CLI_STRESS_ORIGIN")
	workerID := os.Getenv("USAGE_CLI_STRESS_WORKER_ID")
	iters, err := strconv.Atoi(os.Getenv("USAGE_CLI_STRESS_ITERS"))
	if err != nil || iters <= 0 {
		iters = 20
	}

	locker := NewLocker(lockDir, 2*time.Second)
	snapStore := NewSnapshotStore(stateDir, origin, locker, nil)
	resetAt := time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC)

	for i := 0; i < iters; i++ {
		snap := domain.QuotaSnapshot{
			Provider:   domain.ProviderClaude,
			Origin:     origin,
			CapturedAt: time.Now().UTC(),
			Source:     "stress-worker-" + workerID,
			Confidence: domain.ConfidenceExact,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{
					UsedPercent:  float64((i % 80) + 1),
					RemainingPct: float64(100 - ((i % 80) + 1)),
					ResetsAt:     resetAt,
				},
			},
		}
		if err := snapStore.SaveShard(snap); err != nil {
			return fmt.Errorf("iter %d SaveShard: %w", i, err)
		}
		shards, err := snapStore.LoadAllShards(domain.ProviderClaude)
		if err != nil {
			return fmt.Errorf("iter %d LoadAllShards: %w", i, err)
		}
		if len(shards) == 0 {
			return fmt.Errorf("iter %d expected >= 1 shard", i)
		}
	}
	return nil
}

func TestAccountGateZeroProbeWhenNoGateAndLifecycle(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")

	locker := NewLocker(lockDir, DefaultLockTimeout)
	gateStore := NewAccountGateStore(stateDir, "win", locker, 45*time.Second)
	resetAt := time.Now().UTC().Add(3 * time.Hour)

	// 1. Zero gate shards -> zero disk writes (NO .probe.win created!).
	insp, err := gateStore.Inspect(domain.ProviderClaude, resetAt)
	if err != nil {
		t.Fatalf("Inspect empty failed: %v", err)
	}
	if insp.Active || insp.ProbeWritten {
		t.Fatalf("expected Active=false, ProbeWritten=false, got %+v", insp)
	}
	if _, statErr := os.Stat(gateStore.ProbePath()); !os.IsNotExist(statErr) {
		t.Fatalf("probe file %q MUST NOT exist when no gate exists", gateStore.ProbePath())
	}

	// 2. Acquire gate by session A, denied for session B.
	resA, err := gateStore.Acquire(domain.AccountGate{
		Provider:              domain.ProviderClaude,
		HolderSessionID:       "sess-alpha",
		State:                 domain.PhaseSafeStopInProgress,
		WindowResetAt:         resetAt,
		SafeStopToolCallsUsed: 1,
	}, resetAt)
	if err != nil {
		t.Fatalf("Acquire A failed: %v", err)
	}
	if !resA.Acquired {
		t.Fatalf("expected A to acquire gate")
	}

	resB, err := gateStore.Acquire(domain.AccountGate{
		Provider:        domain.ProviderClaude,
		HolderSessionID: "sess-beta",
		State:           domain.PhaseSafeStopInProgress,
		WindowResetAt:   resetAt,
	}, resetAt)
	if err != nil {
		t.Fatalf("Acquire B failed: %v", err)
	}
	if resB.Acquired || resB.Holder == nil || resB.Holder.HolderSessionID != "sess-alpha" {
		t.Fatalf("expected B to be blocked by sess-alpha, got %+v", resB)
	}

	// Probe file WAS created during Inspect/Acquire when gate existed.
	if _, statErr := os.Stat(gateStore.ProbePath()); statErr != nil {
		t.Fatalf("expected probe file to exist when gate is active: %v", statErr)
	}

	// 3. Idle timeout 45s measured via NTFS mtime against probe.
	past := time.Now().Add(-46 * time.Second)
	if err := os.Chtimes(gateStore.GatePath(domain.ProviderClaude), past, past); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}
	inspIdle, err := gateStore.Inspect(domain.ProviderClaude, resetAt)
	if err != nil {
		t.Fatalf("Inspect idle failed: %v", err)
	}
	if inspIdle.Active || inspIdle.ExpiredRemoved != 1 {
		t.Fatalf("expected idle gate to expire and be removed, got %+v", inspIdle)
	}

	// 4. WindowResetAt invalidation when snapshot ResetsAt > gate.WindowResetAt + 120s.
	resA2, err := gateStore.Acquire(domain.AccountGate{
		Provider:        domain.ProviderClaude,
		HolderSessionID: "sess-alpha",
		State:           domain.PhaseSafeStopInProgress,
		WindowResetAt:   resetAt,
	}, resetAt)
	if err != nil || !resA2.Acquired {
		t.Fatalf("re-acquire A failed: %v, %+v", err, resA2)
	}
	newerReset := resetAt.Add(125 * time.Second)
	inspWin, err := gateStore.Inspect(domain.ProviderClaude, newerReset)
	if err != nil {
		t.Fatalf("Inspect newerReset failed: %v", err)
	}
	if inspWin.Active || inspWin.ExpiredRemoved != 1 {
		t.Fatalf("expected window rollover gate invalidation, got %+v", inspWin)
	}
}

func TestAccountGateTouchLifecycle(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")

	locker := NewLocker(lockDir, DefaultLockTimeout)
	gateStore := NewAccountGateStore(stateDir, "win", locker, 45*time.Second)
	resetAt := time.Now().UTC().Add(2 * time.Hour)

	res, err := gateStore.Acquire(domain.AccountGate{
		Provider:              domain.ProviderClaude,
		HolderSessionID:       "sess-touch",
		State:                 domain.PhaseSafeStopInProgress,
		WindowResetAt:         resetAt,
		SafeStopToolCallsUsed: 3,
	}, resetAt)
	if err != nil || !res.Acquired {
		t.Fatalf("Acquire failed: %v, %+v", err, res)
	}

	// Age gate mtime to -46s (past 45s idleTimeout) and verify Touch refreshes mtime
	// via atomic rewrite without altering SafeStopToolCallsUsed.
	gatePath := gateStore.GatePath(domain.ProviderClaude)
	staleTime := time.Now().Add(-46 * time.Second)
	if err := os.Chtimes(gatePath, staleTime, staleTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	// Wrong session must fail Touch.
	if err := gateStore.Touch(domain.ProviderClaude, "sess-wrong"); err == nil {
		t.Fatalf("expected Touch with wrong sessionID to fail")
	}

	// Touch by holder refreshes host filesystem mtime and keeps lease valid.
	if err := gateStore.Touch(domain.ProviderClaude, "sess-touch"); err != nil {
		t.Fatalf("Touch failed: %v", err)
	}

	info, err := os.Stat(gatePath)
	if err != nil {
		t.Fatalf("Stat gate failed: %v", err)
	}
	if !info.ModTime().After(staleTime) {
		t.Fatalf("expected Touch to advance mtime beyond %v, got %v", staleTime, info.ModTime())
	}

	insp, err := gateStore.Inspect(domain.ProviderClaude, resetAt)
	if err != nil {
		t.Fatalf("Inspect after Touch failed: %v", err)
	}
	if !insp.Active || insp.Winner == nil {
		t.Fatalf("expected gate to remain active after Touch, got %+v", insp)
	}
	if insp.Winner.SafeStopToolCallsUsed != 3 {
		t.Fatalf("SafeStopToolCallsUsed = %d, want 3", insp.Winner.SafeStopToolCallsUsed)
	}
}

func TestAccountGateDeterministicTieBreaker(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")

	locker := NewLocker(lockDir, DefaultLockTimeout)
	winGateStore := NewAccountGateStore(stateDir, "win", locker, 45*time.Second)
	wslGateStore := NewAccountGateStore(stateDir, "wsl-Ubuntu", locker, 45*time.Second)
	resetAt := time.Now().UTC().Add(2 * time.Hour)

	if err := os.MkdirAll(winGateStore.GateDir(), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	writeRawGate := func(path string, gate domain.AccountGate, mtime time.Time) {
		b, err := json.Marshal(gate)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}

	// Equal NTFS mtime -> lexicographically smaller HolderSessionID ("sess-a" < "sess-b") wins.
	sameTime := time.Now().Truncate(time.Second)
	writeRawGate(winGateStore.GatePath(domain.ProviderClaude), domain.AccountGate{
		SchemaVersion:   domain.SchemaVersion,
		Provider:        domain.ProviderClaude,
		Origin:          "win",
		HolderSessionID: "sess-b",
		State:           domain.PhaseSafeStopInProgress,
		WindowResetAt:   resetAt,
	}, sameTime)
	writeRawGate(wslGateStore.GatePath(domain.ProviderClaude), domain.AccountGate{
		SchemaVersion:   domain.SchemaVersion,
		Provider:        domain.ProviderClaude,
		Origin:          "wsl-Ubuntu",
		HolderSessionID: "sess-a",
		State:           domain.PhaseSafeStopInProgress,
		WindowResetAt:   resetAt,
	}, sameTime)

	insp, err := winGateStore.Inspect(domain.ProviderClaude, resetAt)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if !insp.Active || insp.Winner == nil || insp.Winner.HolderSessionID != "sess-a" {
		t.Fatalf("expected sess-a to win equal-mtime tie-breaker, got %+v", insp.Winner)
	}

	// Earlier mtime wins even if HolderSessionID is larger.
	earlierTime := sameTime.Add(-5 * time.Second)
	if err := os.Chtimes(winGateStore.GatePath(domain.ProviderClaude), earlierTime, earlierTime); err != nil {
		t.Fatalf("Chtimes earlier: %v", err)
	}
	insp2, err := wslGateStore.Inspect(domain.ProviderClaude, resetAt)
	if err != nil {
		t.Fatalf("Inspect2 failed: %v", err)
	}
	if !insp2.Active || insp2.Winner == nil || insp2.Winner.HolderSessionID != "sess-b" {
		t.Fatalf("expected earlier mtime (sess-b) to win, got %+v", insp2.Winner)
	}
}

func TestFastCacheTTLAndGuardsAndInvalidation(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")
	cacheDir := filepath.Join(root, "shm")

	fc := NewFastCache(cacheDir, "wsl-Ubuntu")
	locker := NewLocker(lockDir, DefaultLockTimeout)
	snapStore := NewSnapshotStore(stateDir, "wsl-Ubuntu", locker, fc)

	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

	// Valid entry: <55%, burnRate <1.0, gateOK=true, confidence=exact, TTL<=4s.
	entry := FastCacheEntry{
		SchemaVersion: domain.SchemaVersion,
		Provider:      domain.ProviderClaude,
		CachedAt:      now,
		UsedPercent:   40.0,
		BurnRate:      0.5,
		GateOK:        true,
		Confidence:    domain.ConfidenceExact,
	}
	if err := fc.Set(entry); err != nil {
		t.Fatalf("FastCache.Set failed: %v", err)
	}

	if _, ok := fc.Get(domain.ProviderClaude, now.Add(3*time.Second)); !ok {
		t.Fatalf("expected cache hit within 4s TTL")
	}
	if _, ok := fc.Get(domain.ProviderClaude, now.Add(5*time.Second)); ok {
		t.Fatalf("expected cache miss after 4s TTL")
	}

	// SaveShard must invalidate fast cache atomically under lock.
	err := snapStore.SaveShard(domain.QuotaSnapshot{
		Provider:   domain.ProviderClaude,
		Origin:     "wsl-Ubuntu",
		CapturedAt: now,
		Source:     "test",
		Confidence: domain.ConfidenceExact,
	})
	if err != nil {
		t.Fatalf("SaveShard failed: %v", err)
	}
	if _, ok := fc.Get(domain.ProviderClaude, now.Add(1*time.Second)); ok {
		t.Fatalf("expected SaveShard to invalidate fast cache")
	}

	// Guard checks: >=55%, burnRate>=1.0, !gateOK, non-exact confidence must NOT cache.
	ineligible := []FastCacheEntry{
		{SchemaVersion: domain.SchemaVersion, Provider: domain.ProviderClaude, CachedAt: now, UsedPercent: 55.0, BurnRate: 0.2, GateOK: true, Confidence: domain.ConfidenceExact},
		{SchemaVersion: domain.SchemaVersion, Provider: domain.ProviderClaude, CachedAt: now, UsedPercent: 30.0, BurnRate: 1.0, GateOK: true, Confidence: domain.ConfidenceExact},
		{SchemaVersion: domain.SchemaVersion, Provider: domain.ProviderClaude, CachedAt: now, UsedPercent: 30.0, BurnRate: 0.2, GateOK: false, Confidence: domain.ConfidenceExact},
		{SchemaVersion: domain.SchemaVersion, Provider: domain.ProviderClaude, CachedAt: now, UsedPercent: 30.0, BurnRate: 0.2, GateOK: true, Confidence: domain.ConfidenceStale},
	}
	for i, bad := range ineligible {
		if err := fc.Set(bad); err != nil {
			t.Fatalf("case %d Set failed: %v", i, err)
		}
		if _, ok := fc.Get(domain.ProviderClaude, now); ok {
			t.Fatalf("case %d expected ineligible entry not to be cached", i)
		}
	}
}

func TestTelemetryRotationAndSanitization(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")

	locker := NewLocker(lockDir, DefaultLockTimeout)
	ts := NewTelemetryStore(stateDir, locker, 400, 2)

	for i := 0; i < 10; i++ {
		err := ts.Append(security.TelemetryEvent{
			Timestamp:    time.Date(2026, 11, 23, 15, 0, i, 0, time.UTC),
			Origin:       "win",
			Provider:     "claude",
			ToolName:     "Edit",
			Decision:     "allow",
			UsedPct5h:    float64(i * 5),
			UsedPct7d:    12.0,
			State:        "OK",
			PathBasename: fmt.Sprintf(`C:\Users\secret\repo\file_%d.go`, i),
		})
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}

	// Verify active log + 2 backup generations exist, and no .3 backup exists.
	for _, path := range []string{ts.FilePath(), ts.FilePath() + ".1", ts.FilePath() + ".2"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected rotated file %q to exist: %v", path, err)
		}
	}
	if _, err := os.Stat(ts.FilePath() + ".3"); !os.IsNotExist(err) {
		t.Fatalf("expected maxBackups=2 (no .3 file)")
	}

	raw, err := os.ReadFile(ts.FilePath())
	if err != nil {
		t.Fatalf("ReadFile active log: %v", err)
	}
	if strings.Contains(string(raw), `C:\Users\secret`) {
		t.Fatalf("telemetry log leaked full path: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"pathBasename":"file_9.go"`) {
		t.Fatalf("expected sanitized pathBasename in telemetry log: %s", string(raw))
	}
}

func TestSessionAndHealthStores(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")
	locker := NewLocker(lockDir, DefaultLockTimeout)

	// SessionStore
	ss := NewSessionStore(stateDir, "win", locker)
	updated, err := ss.Update("session-xyz", func(st *domain.SessionState) error {
		st.Provider = domain.ProviderClaude
		st.Phase = domain.PhaseWARN
		st.LastWarnPercent = 74.5
		return nil
	})
	if err != nil {
		t.Fatalf("SessionStore.Update failed: %v", err)
	}
	if updated.Phase != domain.PhaseWARN {
		t.Fatalf("Phase = %q, want WARN", updated.Phase)
	}
	loaded, err := ss.Load("session-xyz")
	if err != nil {
		t.Fatalf("SessionStore.Load failed: %v", err)
	}
	if loaded.LastWarnPercent != 74.5 {
		t.Fatalf("LastWarnPercent = %v, want 74.5", loaded.LastWarnPercent)
	}

	// HealthStore
	hs := NewHealthStore(stateDir, "win", locker)
	now := time.Now().UTC()
	if err := hs.RecordHookError("PreToolUse", domain.ProviderClaude, "failed with sk-ant-api03-secret999", now); err != nil {
		t.Fatalf("RecordHookError 1 failed: %v", err)
	}
	if err := hs.RecordHookError("PreToolUse", domain.ProviderClaude, "failed with sk-ant-api03-secret999", now.Add(time.Second)); err != nil {
		t.Fatalf("RecordHookError 2 failed: %v", err)
	}

	errs, err := hs.ReadRecentErrors(time.Minute, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ReadRecentErrors failed: %v", err)
	}
	if len(errs) != 1 || errs[0].Count != 2 {
		t.Fatalf("expected 1 error with Count=2, got %+v", errs)
	}
	if strings.Contains(errs[0].Message, "secret999") {
		t.Fatalf("HealthStore leaked raw secret in error message: %s", errs[0].Message)
	}
	if err := hs.ClearError(); err != nil {
		t.Fatalf("ClearError failed: %v", err)
	}
}

func TestMultiProcessStressSnapshotConcurrency(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	lockDir := filepath.Join(root, "locks")

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	const numWorkers = 6
	const itersPerWorker = 20

	// Concurrent in-process reader hammering LoadAllShards while subprocesses write/rename.
	stopReaders := make(chan struct{})
	readerErrCh := make(chan error, 1)
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		readerStore := NewSnapshotStore(stateDir, "reader", nil, nil)
		for {
			select {
			case <-stopReaders:
				return
			default:
				if _, loadErr := readerStore.LoadAllShards(domain.ProviderClaude); loadErr != nil {
					select {
					case readerErrCh <- loadErr:
					default:
					}
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	workerErrs := make(chan error, numWorkers)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			origin := "win"
			if id%2 == 1 {
				origin = "wsl-Ubuntu"
			}
			cmd := exec.Command(exe, "-test.run=^$")
			cmd.Env = append(os.Environ(),
				"USAGE_CLI_STRICT=1",
				stressWorkerEnv+"=1",
				"USAGE_CLI_STRESS_STATE_DIR="+stateDir,
				"USAGE_CLI_STRESS_LOCK_DIR="+lockDir,
				"USAGE_CLI_STRESS_ORIGIN="+origin,
				fmt.Sprintf("USAGE_CLI_STRESS_WORKER_ID=%d", id),
				fmt.Sprintf("USAGE_CLI_STRESS_ITERS=%d", itersPerWorker),
			)
			out, runErr := cmd.CombinedOutput()
			if runErr != nil {
				workerErrs <- fmt.Errorf("worker %d (%s): %v: %s", id, origin, runErr, string(out))
			}
		}(w)
	}

	wg.Wait()
	close(stopReaders)
	readerWG.Wait()
	close(workerErrs)
	close(readerErrCh)

	for wErr := range workerErrs {
		t.Fatalf("multiprocess stress worker error: %v", wErr)
	}
	if rErr := <-readerErrCh; rErr != nil {
		t.Fatalf("concurrent reader saw error/corruption: %v", rErr)
	}

	// Verify final shards parse cleanly and ZERO .tmp-* orphans remain.
	snapStore := NewSnapshotStore(stateDir, "win", nil, nil)
	shards, err := snapStore.LoadAllShards(domain.ProviderClaude)
	if err != nil {
		t.Fatalf("final LoadAllShards failed: %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("expected 2 origin shards (win, wsl-Ubuntu), got %d", len(shards))
	}

	entries, err := os.ReadDir(snapStore.ProviderDir(domain.ProviderClaude))
	if err != nil {
		t.Fatalf("ReadDir snapshot dir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("found leftover temp orphan file %q", entry.Name())
		}
	}
}
