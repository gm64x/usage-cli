package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gm64x/usage-cli/internal/domain"
	"github.com/gm64x/usage-cli/internal/fsutil"
	"github.com/gm64x/usage-cli/internal/security"
)

const (
	// DefaultTelemetryMaxBytes is the default 2 MiB rotation threshold for telemetry JSONL.
	DefaultTelemetryMaxBytes int64 = 2 * 1024 * 1024
	// DefaultTelemetryMaxBackups is the default number of rotated telemetry backup files.
	DefaultTelemetryMaxBackups = 2
)

// StorageConfig configures local state, fast-cache, and telemetry storage options.
type StorageConfig struct {
	StateDir            string `json:"stateDir,omitempty"`
	CacheDir            string `json:"cacheDir,omitempty"`
	TelemetryMaxBytes   int64  `json:"telemetryMaxBytes"`
	TelemetryMaxBackups int    `json:"telemetryMaxBackups"`
}

// ProviderConfig holds non-secret provider enablement and polling preferences.
type ProviderConfig struct {
	Enabled             bool    `json:"enabled"`
	MonthlyBudgetUSD    float64 `json:"monthlyBudgetUsd,omitempty"`
	PollIntervalSeconds int     `json:"pollIntervalSeconds,omitempty"`
}

// ProvidersConfig groups per-provider configurations.
type ProvidersConfig struct {
	Claude    ProviderConfig `json:"claude"`
	Agy       ProviderConfig `json:"agy"`
	APIBudget ProviderConfig `json:"apiBudget"`
}

// Config is the root persisted configuration for usage-cli.
type Config struct {
	SchemaVersion string            `json:"schemaVersion"`
	Storage       StorageConfig     `json:"storage"`
	Policy        domain.Policy     `json:"policy"`
	Providers     ProvidersConfig   `json:"providers"`
	ToolAliases   map[string]string `json:"toolAliases,omitempty"`
}

// DefaultConfig returns a valid Config populated with Plan v3 defaults.
func DefaultConfig() Config {
	return Config{
		SchemaVersion: domain.SchemaVersion,
		Storage: StorageConfig{
			TelemetryMaxBytes:   DefaultTelemetryMaxBytes,
			TelemetryMaxBackups: DefaultTelemetryMaxBackups,
		},
		Policy: domain.DefaultPolicy(),
		Providers: ProvidersConfig{
			Claude:    ProviderConfig{Enabled: true, PollIntervalSeconds: 30},
			Agy:       ProviderConfig{Enabled: true, PollIntervalSeconds: 30},
			APIBudget: ProviderConfig{Enabled: false, PollIntervalSeconds: 60},
		},
		ToolAliases: map[string]string{},
	}
}

// Load reads and validates a JSON configuration file from path, applying defaults
// for any omitted fields.
func Load(path string) (Config, error) {
	f, err := security.OpenReadOnly(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer func() {
		_ = f.Close()
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.ToolAliases == nil {
		cfg.ToolAliases = map[string]string{}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return cfg, nil
}

// SaveAtomic validates c and writes it atomically to path.
func (c Config) SaveAtomic(path string) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	return fsutil.WriteAtomic(path, "config", data, 0o600)
}

// Validate checks structural and threshold invariants of Config.
func (c Config) Validate() error {
	if c.SchemaVersion == "" {
		return errors.New("schemaVersion is required")
	}
	if c.SchemaVersion != domain.SchemaVersion {
		return fmt.Errorf("unsupported schemaVersion %q", c.SchemaVersion)
	}
	if c.Storage.TelemetryMaxBytes < 0 {
		return errors.New("storage.telemetryMaxBytes must be >= 0")
	}
	if c.Storage.TelemetryMaxBackups < 0 {
		return errors.New("storage.telemetryMaxBackups must be >= 0")
	}

	windows := map[string]domain.WindowThreshold{
		"policy.fiveHour":   c.Policy.FiveHour,
		"policy.sevenDay":   c.Policy.SevenDay,
		"policy.spendLimit": c.Policy.SpendLimit,
		"policy.apiBudget":  c.Policy.APIBudget,
	}
	for name, wt := range windows {
		if err := validateWindowThreshold(name, wt); err != nil {
			return err
		}
	}

	ss := c.Policy.SafeStop
	if ss.GraceToolCallsBeforeRestrict < 0 {
		return errors.New("policy.safeStop.graceToolCallsBeforeRestrict must be >= 0")
	}
	if ss.GraceSecondsBeforeRestrict < 0 {
		return errors.New("policy.safeStop.graceSecondsBeforeRestrict must be >= 0")
	}
	if ss.PredictiveStopMinutes < 0 {
		return errors.New("policy.safeStop.predictiveStopMinutes must be >= 0")
	}
	if ss.WarnCooldownSeconds < 0 {
		return errors.New("policy.safeStop.warnCooldownSeconds must be >= 0")
	}
	if ss.WarnStepPercent < 0 || ss.WarnStepPercent > 100 {
		return errors.New("policy.safeStop.warnStepPercent must be within [0, 100]")
	}
	if ss.WarnAgentBlockPercent5h < 0 || ss.WarnAgentBlockPercent5h > 100 {
		return errors.New("policy.safeStop.warnAgentBlockPercent5h must be within [0, 100]")
	}
	if ss.WarnAgentBlockPercent7d < 0 || ss.WarnAgentBlockPercent7d > 100 {
		return errors.New("policy.safeStop.warnAgentBlockPercent7d must be within [0, 100]")
	}
	if ss.StopMaxUsedPercent < 0 || ss.StopMaxUsedPercent > 100 {
		return errors.New("policy.safeStop.stopMaxUsedPercent must be within [0, 100]")
	}
	if ss.MaxSafeStopToolCalls < 0 {
		return errors.New("policy.safeStop.maxSafeStopToolCalls must be >= 0")
	}
	if ss.MaxSafeStopDurationSeconds < 0 {
		return errors.New("policy.safeStop.maxSafeStopDurationSeconds must be >= 0")
	}
	if ss.IdleTimeoutGateSeconds < 0 {
		return errors.New("policy.safeStop.idleTimeoutGateSeconds must be >= 0")
	}
	if ss.MaxConsecutiveDenies < 0 {
		return errors.New("policy.safeStop.maxConsecutiveDenies must be >= 0")
	}
	switch ss.OnUnavailable {
	case "allow-silent", "allow-warn", "block":
	default:
		return fmt.Errorf("unsupported policy.safeStop.onUnavailable %q", ss.OnUnavailable)
	}

	return nil
}

func validateWindowThreshold(name string, wt domain.WindowThreshold) error {
	if wt.WarnPercent < 0 || wt.WarnPercent > 100 {
		return fmt.Errorf("%s.warnPercent must be within [0, 100]", name)
	}
	if wt.SafeStopPercent < wt.WarnPercent || wt.SafeStopPercent > 100 {
		return fmt.Errorf("%s.safeStopPercent must be >= warnPercent and <= 100", name)
	}
	if wt.HardBlockPercent < wt.SafeStopPercent || wt.HardBlockPercent > 100 {
		return fmt.Errorf("%s.hardBlockPercent must be >= safeStopPercent and <= 100", name)
	}
	if wt.HardCeilingPercent < wt.HardBlockPercent || wt.HardCeilingPercent > 100 {
		return fmt.Errorf("%s.hardCeilingPercent must be >= hardBlockPercent and <= 100", name)
	}
	if wt.HysteresisPercent < 0 || wt.HysteresisPercent > 50 {
		return fmt.Errorf("%s.hysteresisPercent must be within [0, 50]", name)
	}
	return nil
}
