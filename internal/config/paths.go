package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	// StateDirEnvVar overrides the auto-detected state directory.
	StateDirEnvVar = "USAGE_CLI_STATE_DIR"
	// ConfigPathEnvVar overrides the auto-detected configuration file path.
	ConfigPathEnvVar = "USAGE_CLI_CONFIG"
	// RootMarkerFile marks a valid Windows usage-cli state directory for WSL discovery.
	RootMarkerFile = ".usage-cli-root"
)

var wslSkippedSystemUserDirs = map[string]struct{}{
	"public":       {},
	"default":      {},
	"default user": {},
	"all users":    {},
}

// Paths holds resolved runtime directories and origin metadata.
type Paths struct {
	StateDir   string
	ConfigPath string
	LockDir    string
	CacheDir   string
	Origin     string
	IsWSL      bool
	WSLDistro  string
}

// PathEnv injects OS, environment, and filesystem roots for deterministic path resolution.
type PathEnv struct {
	GOOS              string
	Getenv            func(string) string
	UserHomeDir       func() (string, error)
	UserCacheDir      func() (string, error)
	ProcVersionPath   string
	WSLUsersMountRoot string
	ShmRoot           string
}

// DefaultPathEnv returns a PathEnv wired to the live OS environment.
func DefaultPathEnv() PathEnv {
	return PathEnv{
		GOOS:              runtime.GOOS,
		Getenv:            os.Getenv,
		UserHomeDir:       os.UserHomeDir,
		UserCacheDir:      os.UserCacheDir,
		ProcVersionPath:   "/proc/version",
		WSLUsersMountRoot: "/mnt/c/Users",
		ShmRoot:           "/dev/shm",
	}
}

func (e PathEnv) withDefaults() PathEnv {
	def := DefaultPathEnv()
	if e.GOOS == "" {
		e.GOOS = def.GOOS
	}
	if e.Getenv == nil {
		e.Getenv = def.Getenv
	}
	if e.UserHomeDir == nil {
		e.UserHomeDir = def.UserHomeDir
	}
	if e.UserCacheDir == nil {
		e.UserCacheDir = def.UserCacheDir
	}
	if e.ProcVersionPath == "" {
		e.ProcVersionPath = def.ProcVersionPath
	}
	if e.WSLUsersMountRoot == "" {
		e.WSLUsersMountRoot = def.WSLUsersMountRoot
	}
	if e.ShmRoot == "" {
		e.ShmRoot = def.ShmRoot
	}
	return e
}

// ResolvePaths resolves storage, lock, cache, config, and origin metadata using live OS defaults.
func ResolvePaths(explicitStateDir, explicitConfigPath string) (Paths, error) {
	return ResolvePathsWithEnv(explicitStateDir, explicitConfigPath, DefaultPathEnv())
}

// ResolvePathsWithEnv resolves paths using an injectable PathEnv.
// Precedence for StateDir: explicitStateDir > USAGE_CLI_STATE_DIR > config.Storage.StateDir > auto-detect.
func ResolvePathsWithEnv(explicitStateDir, explicitConfigPath string, env PathEnv) (Paths, error) {
	env = env.withDefaults()

	homeDir, err := env.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}

	isWSL, distro := detectWSL(env)
	origin := resolveOrigin(env.GOOS, isWSL, distro)

	autoStateDir, autoConfigPath := detectDefaultDirs(env, homeDir, isWSL)

	// Determine config path first.
	configPath := strings.TrimSpace(explicitConfigPath)
	if configPath == "" {
		configPath = strings.TrimSpace(env.Getenv(ConfigPathEnvVar))
	}

	explicitOrEnvStateDir := strings.TrimSpace(explicitStateDir)
	if explicitOrEnvStateDir == "" {
		explicitOrEnvStateDir = strings.TrimSpace(env.Getenv(StateDirEnvVar))
	}

	if configPath == "" {
		if explicitOrEnvStateDir != "" {
			configPath = filepath.Join(explicitOrEnvStateDir, "config.json")
		} else {
			configPath = autoConfigPath
		}
	}

	var cfgCacheDir string
	stateDir := explicitOrEnvStateDir
	if configPath != "" {
		if cfg, loadErr := Load(configPath); loadErr == nil {
			if stateDir == "" && strings.TrimSpace(cfg.Storage.StateDir) != "" {
				stateDir = strings.TrimSpace(cfg.Storage.StateDir)
			}
			cfgCacheDir = strings.TrimSpace(cfg.Storage.CacheDir)
		}
	}
	if stateDir == "" {
		stateDir = autoStateDir
	}

	lockDir := resolveLockDir(env.GOOS, homeDir, stateDir)
	cacheDir := cfgCacheDir
	if cacheDir == "" {
		cacheDir = resolveCacheDir(env, stateDir)
	}

	return Paths{
		StateDir:   filepath.Clean(stateDir),
		ConfigPath: filepath.Clean(configPath),
		LockDir:    filepath.Clean(lockDir),
		CacheDir:   filepath.Clean(cacheDir),
		Origin:     origin,
		IsWSL:      isWSL,
		WSLDistro:  distro,
	}, nil
}

func detectWSL(env PathEnv) (bool, string) {
	if env.GOOS != "linux" {
		return false, ""
	}
	distro := sanitizeOriginSegment(env.Getenv("WSL_DISTRO_NAME"))
	if distro != "" {
		return true, distro
	}
	if strings.TrimSpace(env.Getenv("WSL_INTEROP")) != "" {
		return true, "unknown"
	}
	if data, err := os.ReadFile(env.ProcVersionPath); err == nil {
		if strings.Contains(strings.ToLower(string(data)), "microsoft") {
			return true, "unknown"
		}
	}
	return false, ""
}

func resolveOrigin(goos string, isWSL bool, distro string) string {
	switch goos {
	case "windows":
		return "win"
	case "linux":
		if isWSL {
			if distro == "" {
				return "wsl-unknown"
			}
			return "wsl-" + distro
		}
		return "linux"
	default:
		return sanitizeOriginSegment(goos)
	}
}

func detectDefaultDirs(env PathEnv, homeDir string, isWSL bool) (stateDir string, configPath string) {
	if env.GOOS == "windows" {
		localAppData := strings.TrimSpace(env.Getenv("LOCALAPPDATA"))
		if localAppData == "" {
			localAppData = filepath.Join(homeDir, "AppData", "Local")
		}
		stateDir = filepath.Join(localAppData, "usage-cli")
		return stateDir, filepath.Join(stateDir, "config.json")
	}

	if isWSL {
		if discovered := discoverWSLWindowsStateDir(env.WSLUsersMountRoot); discovered != "" {
			return discovered, filepath.Join(discovered, "config.json")
		}
	}

	xdgState := strings.TrimSpace(env.Getenv("XDG_STATE_HOME"))
	if xdgState == "" {
		xdgState = filepath.Join(homeDir, ".local", "state")
	}
	stateDir = filepath.Join(xdgState, "usage-cli")

	xdgConfig := strings.TrimSpace(env.Getenv("XDG_CONFIG_HOME"))
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}
	configPath = filepath.Join(xdgConfig, "usage-cli", "config.json")
	return stateDir, configPath
}

// discoverWSLWindowsStateDir scans /mnt/c/Users/*/AppData/Local/usage-cli without invoking
// cmd.exe, skipping Windows system profile directories and requiring .usage-cli-root or config.json.
func discoverWSLWindowsStateDir(usersRoot string) string {
	entries, err := os.ReadDir(usersRoot)
	if err != nil {
		return ""
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, skip := wslSkippedSystemUserDirs[strings.ToLower(entry.Name())]; skip {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var fallback string
	for _, user := range names {
		candidate := filepath.Join(usersRoot, user, "AppData", "Local", "usage-cli")
		if fileExists(filepath.Join(candidate, RootMarkerFile)) {
			return candidate
		}
		if fallback == "" && fileExists(filepath.Join(candidate, "config.json")) {
			fallback = candidate
		}
	}
	return fallback
}

func resolveLockDir(goos, homeDir, stateDir string) string {
	if goos == "windows" {
		return filepath.Join(stateDir, "locks")
	}
	// On WSL and Linux, intra-kernel flock files MUST stay on native ext4/tmpfs, never on /mnt/c.
	return filepath.Join(homeDir, ".local", "state", "usage-cli", "locks")
}

func resolveCacheDir(env PathEnv, stateDir string) string {
	if env.GOOS == "linux" {
		if info, err := os.Stat(env.ShmRoot); err == nil && info.IsDir() {
			return filepath.Join(env.ShmRoot, "usage-cli")
		}
	}
	if cacheBase, err := env.UserCacheDir(); err == nil && strings.TrimSpace(cacheBase) != "" {
		return filepath.Join(cacheBase, "usage-cli")
	}
	return filepath.Join(stateDir, "cache")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sanitizeOriginSegment(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
