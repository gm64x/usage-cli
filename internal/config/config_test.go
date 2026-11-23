package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLoadSaveValidate(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	cfg.Storage.StateDir = filepath.Join(dir, "state")
	cfg.ToolAliases["bash"] = "Bash"

	if err := cfg.SaveAtomic(cfgPath); err != nil {
		t.Fatalf("SaveAtomic failed: %v", err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.Storage.StateDir != cfg.Storage.StateDir {
		t.Fatalf("StateDir = %q, want %q", loaded.Storage.StateDir, cfg.Storage.StateDir)
	}
	if loaded.ToolAliases["bash"] != "Bash" {
		t.Fatalf("ToolAliases = %v, want bash->Bash", loaded.ToolAliases)
	}

	// Invalid threshold ordering should fail validation.
	bad := DefaultConfig()
	bad.Policy.FiveHour.SafeStopPercent = 50.0 // < WarnPercent (72)
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected Validate to fail when SafeStopPercent < WarnPercent")
	}
}

func TestResolvePathsWSLDiscoveryAndLockDirOffMntC(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()

	usersMount := filepath.Join(root, "mnt", "c", "Users")
	homeDir := filepath.Join(root, "home", "alice")
	shmRoot := filepath.Join(root, "dev", "shm")
	procVersion := filepath.Join(root, "proc", "version")

	if err := os.MkdirAll(shmRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll shm: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(procVersion), 0o755); err != nil {
		t.Fatalf("MkdirAll proc: %v", err)
	}
	if err := os.WriteFile(procVersion, []byte("Linux version 5.15.0-microsoft-standard-WSL2"), 0o600); err != nil {
		t.Fatalf("WriteFile proc/version: %v", err)
	}

	// System directories with markers MUST be ignored.
	for _, sysDir := range []string{"Public", "Default", "Default User", "All Users"} {
		cand := filepath.Join(usersMount, sysDir, "AppData", "Local", "usage-cli")
		if err := os.MkdirAll(cand, 0o755); err != nil {
			t.Fatalf("MkdirAll sysDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cand, RootMarkerFile), []byte("1"), 0o600); err != nil {
			t.Fatalf("WriteFile marker: %v", err)
		}
	}

	// Real Windows user directory WITHOUT marker or config.json must also be ignored.
	noMarkerDir := filepath.Join(usersMount, "Bob", "AppData", "Local", "usage-cli")
	if err := os.MkdirAll(noMarkerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll Bob: %v", err)
	}

	// Real Windows user directory WITH .usage-cli-root marker should be discovered.
	aliceWinDir := filepath.Join(usersMount, "Alice", "AppData", "Local", "usage-cli")
	if err := os.MkdirAll(aliceWinDir, 0o755); err != nil {
		t.Fatalf("MkdirAll Alice: %v", err)
	}
	if err := os.WriteFile(filepath.Join(aliceWinDir, RootMarkerFile), []byte("1"), 0o600); err != nil {
		t.Fatalf("WriteFile Alice marker: %v", err)
	}

	envMap := map[string]string{
		"WSL_DISTRO_NAME": "Ubuntu",
	}
	env := PathEnv{
		GOOS:              "linux",
		Getenv:            func(k string) string { return envMap[k] },
		UserHomeDir:       func() (string, error) { return homeDir, nil },
		UserCacheDir:      func() (string, error) { return filepath.Join(homeDir, ".cache"), nil },
		ProcVersionPath:   procVersion,
		WSLUsersMountRoot: usersMount,
		ShmRoot:           shmRoot,
	}

	paths, err := ResolvePathsWithEnv("", "", env)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv failed: %v", err)
	}

	if !paths.IsWSL || paths.WSLDistro != "Ubuntu" {
		t.Fatalf("unexpected WSL detection: isWSL=%v distro=%q", paths.IsWSL, paths.WSLDistro)
	}
	if paths.Origin != "wsl-Ubuntu" {
		t.Fatalf("Origin = %q, want %q", paths.Origin, "wsl-Ubuntu")
	}
	if paths.StateDir != filepath.Clean(aliceWinDir) {
		t.Fatalf("StateDir = %q, want %q", paths.StateDir, filepath.Clean(aliceWinDir))
	}

	// LockDir MUST be on native ext4 (~/.local/state/usage-cli/locks), NEVER under /mnt/c!
	wantLockDir := filepath.Join(homeDir, ".local", "state", "usage-cli", "locks")
	if paths.LockDir != wantLockDir {
		t.Fatalf("LockDir = %q, want %q", paths.LockDir, wantLockDir)
	}
	if strings.Contains(paths.LockDir, usersMount) {
		t.Fatalf("LockDir %q must NEVER be inside Windows mount %q", paths.LockDir, usersMount)
	}

	wantCacheDir := filepath.Join(shmRoot, "usage-cli")
	if paths.CacheDir != wantCacheDir {
		t.Fatalf("CacheDir = %q, want %q", paths.CacheDir, wantCacheDir)
	}
}

func TestResolvePathsPrecedenceAndOrigins(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()

	homeDir := filepath.Join(root, "home")
	cfgStateDir := filepath.Join(root, "from-config")
	envStateDir := filepath.Join(root, "from-env")
	cliStateDir := filepath.Join(root, "from-cli")
	cfgPath := filepath.Join(root, "custom-config.json")

	cfg := DefaultConfig()
	cfg.Storage.StateDir = cfgStateDir
	if err := cfg.SaveAtomic(cfgPath); err != nil {
		t.Fatalf("SaveAtomic failed: %v", err)
	}

	// 1. config.Storage.StateDir used when explicit and env are empty.
	envMap := map[string]string{}
	env := PathEnv{
		GOOS:            "linux",
		Getenv:          func(k string) string { return envMap[k] },
		UserHomeDir:     func() (string, error) { return homeDir, nil },
		UserCacheDir:    func() (string, error) { return filepath.Join(root, "cache"), nil },
		ProcVersionPath: filepath.Join(root, "nonexistent-proc-version"),
		ShmRoot:         filepath.Join(root, "nonexistent-shm"),
	}

	p1, err := ResolvePathsWithEnv("", cfgPath, env)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv failed: %v", err)
	}
	if p1.Origin != "linux" {
		t.Fatalf("native linux Origin = %q, want %q", p1.Origin, "linux")
	}
	if p1.StateDir != cfgStateDir {
		t.Fatalf("StateDir = %q, want %q", p1.StateDir, cfgStateDir)
	}
	if p1.CacheDir != filepath.Join(root, "cache", "usage-cli") {
		t.Fatalf("fallback CacheDir = %q", p1.CacheDir)
	}

	// 2. USAGE_CLI_STATE_DIR overrides config.
	envMap[StateDirEnvVar] = envStateDir
	p2, err := ResolvePathsWithEnv("", cfgPath, env)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv failed: %v", err)
	}
	if p2.StateDir != envStateDir {
		t.Fatalf("StateDir = %q, want %q", p2.StateDir, envStateDir)
	}

	// 3. --state-dir overrides USAGE_CLI_STATE_DIR and config.
	p3, err := ResolvePathsWithEnv(cliStateDir, cfgPath, env)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv failed: %v", err)
	}
	if p3.StateDir != cliStateDir {
		t.Fatalf("StateDir = %q, want %q", p3.StateDir, cliStateDir)
	}

	// 4. Windows origin and lock dir inside stateDir\locks.
	winEnv := PathEnv{
		GOOS:         "windows",
		Getenv:       func(k string) string { return "" },
		UserHomeDir:  func() (string, error) { return homeDir, nil },
		UserCacheDir: func() (string, error) { return filepath.Join(root, "wincache"), nil },
	}
	pWin, err := ResolvePathsWithEnv(cliStateDir, "", winEnv)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv win failed: %v", err)
	}
	if pWin.Origin != "win" {
		t.Fatalf("win Origin = %q, want %q", pWin.Origin, "win")
	}
	if pWin.LockDir != filepath.Join(cliStateDir, "locks") {
		t.Fatalf("win LockDir = %q, want %q", pWin.LockDir, filepath.Join(cliStateDir, "locks"))
	}
}

func TestResolvePathsHonorsConfigCacheDir(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	root := t.TempDir()

	cfgPath := filepath.Join(root, "config.json")
	customCacheDir := filepath.Join(root, "custom-cache")

	cfg := DefaultConfig()
	cfg.Storage.CacheDir = customCacheDir
	if err := cfg.SaveAtomic(cfgPath); err != nil {
		t.Fatalf("SaveAtomic failed: %v", err)
	}

	env := PathEnv{
		GOOS:            "linux",
		Getenv:          func(string) string { return "" },
		UserHomeDir:     func() (string, error) { return filepath.Join(root, "home"), nil },
		UserCacheDir:    func() (string, error) { return filepath.Join(root, "os-cache"), nil },
		ProcVersionPath: filepath.Join(root, "nonexistent-proc-version"),
		ShmRoot:         filepath.Join(root, "shm"),
	}

	paths, err := ResolvePathsWithEnv(filepath.Join(root, "state"), cfgPath, env)
	if err != nil {
		t.Fatalf("ResolvePathsWithEnv failed: %v", err)
	}
	if paths.CacheDir != filepath.Clean(customCacheDir) {
		t.Fatalf("CacheDir = %q, want %q", paths.CacheDir, filepath.Clean(customCacheDir))
	}
}

func TestConfigValidateSafeStopBounds(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"negative grace calls", func(c *Config) { c.Policy.SafeStop.GraceToolCallsBeforeRestrict = -1 }},
		{"negative grace seconds", func(c *Config) { c.Policy.SafeStop.GraceSecondsBeforeRestrict = -1 }},
		{"negative predictive stop minutes", func(c *Config) { c.Policy.SafeStop.PredictiveStopMinutes = -1 }},
		{"negative warn cooldown", func(c *Config) { c.Policy.SafeStop.WarnCooldownSeconds = -1 }},
		{"warn step < 0", func(c *Config) { c.Policy.SafeStop.WarnStepPercent = -0.1 }},
		{"warn step > 100", func(c *Config) { c.Policy.SafeStop.WarnStepPercent = 100.1 }},
		{"warn agent block 5h < 0", func(c *Config) { c.Policy.SafeStop.WarnAgentBlockPercent5h = -1 }},
		{"warn agent block 5h > 100", func(c *Config) { c.Policy.SafeStop.WarnAgentBlockPercent5h = 101 }},
		{"warn agent block 7d < 0", func(c *Config) { c.Policy.SafeStop.WarnAgentBlockPercent7d = -1 }},
		{"warn agent block 7d > 100", func(c *Config) { c.Policy.SafeStop.WarnAgentBlockPercent7d = 101 }},
		{"stop max used < 0", func(c *Config) { c.Policy.SafeStop.StopMaxUsedPercent = -1 }},
		{"stop max used > 100", func(c *Config) { c.Policy.SafeStop.StopMaxUsedPercent = 101 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("expected Validate() to fail for %s", tc.name)
			}
		})
	}
}
