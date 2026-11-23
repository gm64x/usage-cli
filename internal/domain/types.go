package domain

import "time"

const (
	// SchemaVersion is the current JSON schema version for persisted state files.
	SchemaVersion = "1.0"
	// ContractVersion is the current wire/hook contract version.
	ContractVersion = "1"
	// MaxUsageSamples is the maximum number of samples retained in the snapshot ring buffer.
	MaxUsageSamples = 12
	// WindowResetSkewTolerance is the maximum allowed drift between reset timestamps
	// when treating two snapshots or samples as belonging to the same quota window.
	WindowResetSkewTolerance = 120 * time.Second
)

// Confidence indicates how trustworthy a quota reading is.
type Confidence string

const (
	ConfidenceExact       Confidence = "exact"
	ConfidenceStale       Confidence = "stale"
	ConfidenceEstimated   Confidence = "estimated"
	ConfidenceUnavailable Confidence = "unavailable"
)

// ConfidenceRank returns a deterministic ordering rank where exact > stale > estimated > unavailable.
func ConfidenceRank(c Confidence) int {
	switch c {
	case ConfidenceExact:
		return 4
	case ConfidenceStale:
		return 3
	case ConfidenceEstimated:
		return 2
	case ConfidenceUnavailable:
		return 1
	default:
		return 0
	}
}

// Provider identifies a monitored subscription or budget source.
type Provider string

const (
	ProviderClaude    Provider = "claude"
	ProviderAgy       Provider = "agy"
	ProviderAPIBudget Provider = "api-budget"
)

// SessionPhase represents the guardrail state machine phase for a session.
type SessionPhase string

const (
	PhaseOK                 SessionPhase = "OK"
	PhaseWARN               SessionPhase = "WARN"
	PhaseSafeStopRequired   SessionPhase = "SAFE_STOP_REQUIRED"
	PhaseSafeStopInProgress SessionPhase = "SAFE_STOP_IN_PROGRESS"
	PhaseHandoffCompleted   SessionPhase = "HANDOFF_COMPLETED"
	PhaseHardBlock          SessionPhase = "HARD_BLOCK"
)

// QuotaWindow captures usage percentage and reset timing for a single quota window.
type QuotaWindow struct {
	UsedPercent  float64   `json:"usedPercent"`
	RemainingPct float64   `json:"remainingPct,omitempty"`
	ResetsAt     time.Time `json:"resetsAt"`
	Used         float64   `json:"used,omitempty"`
	Limit        float64   `json:"limit,omitempty"`
	Currency     string    `json:"currency,omitempty"`
}

// QuotaWindows groups the standard subscription quota windows.
type QuotaWindows struct {
	FiveHour   *QuotaWindow `json:"fiveHour,omitempty"`
	SevenDay   *QuotaWindow `json:"sevenDay,omitempty"`
	SpendLimit *QuotaWindow `json:"spendLimit,omitempty"`
}

// APIBudgetStatus tracks optional API/credit budget consumption.
type APIBudgetStatus struct {
	UsedUSD     float64   `json:"usedUsd"`
	LimitUSD    float64   `json:"limitUsd"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt,omitempty"`
}

// UsageSample represents a single historical data point inside the snapshot ring buffer.
type UsageSample struct {
	Timestamp   time.Time `json:"timestamp"`
	UsedPercent float64   `json:"usedPercent"`
	ResetsAt    time.Time `json:"resetsAt"`
	Window      string    `json:"window,omitempty"`
}

// TelemetryMeta records non-sensitive capture diagnostics for a snapshot.
type TelemetryMeta struct {
	StaleReason    string `json:"staleReason,omitempty"`
	CacheHit       bool   `json:"cacheHit,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	PollDurationMs int64  `json:"pollDurationMs,omitempty"`
}

// QuotaSnapshot is a single origin's persisted quota reading with an embedded ring buffer.
type QuotaSnapshot struct {
	SchemaVersion   string           `json:"schemaVersion"`
	ContractVersion string           `json:"contractVersion"`
	BinaryVersion   string           `json:"binaryVersion,omitempty"`
	Provider        Provider         `json:"provider"`
	Origin          string           `json:"origin"`
	CapturedAt      time.Time        `json:"capturedAt"`
	Source          string           `json:"source"`
	Confidence      Confidence       `json:"confidence"`
	Windows         QuotaWindows     `json:"windows"`
	APIBudget       *APIBudgetStatus `json:"apiBudget,omitempty"`
	Samples         []UsageSample    `json:"samples,omitempty"`
	TelemetryMeta   *TelemetryMeta   `json:"telemetryMeta,omitempty"`
}

// WindowThreshold defines warning, safe-stop, hard-block, ceiling, and hysteresis thresholds.
type WindowThreshold struct {
	WarnPercent        float64 `json:"warnPercent"`
	SafeStopPercent    float64 `json:"safeStopPercent"`
	HardBlockPercent   float64 `json:"hardBlockPercent"`
	HardCeilingPercent float64 `json:"hardCeilingPercent"`
	HysteresisPercent  float64 `json:"hysteresisPercent"`
}

// SafeStopPolicy configures safe-stop, handoff, predictive stop, and fail-open behavior.
type SafeStopPolicy struct {
	GraceToolCallsBeforeRestrict     int      `json:"graceToolCallsBeforeRestrict"`
	GraceSecondsBeforeRestrict       int      `json:"graceSecondsBeforeRestrict"`
	PredictiveStopMinutes            int      `json:"predictiveStopMinutes"`
	WarnAgentBlockPercent5h          float64  `json:"warnAgentBlockPercent5h"`
	WarnAgentBlockPercent7d          float64  `json:"warnAgentBlockPercent7d"`
	MaxSafeStopToolCalls             int      `json:"maxSafeStopToolCalls"`
	MaxSafeStopDurationSeconds       int      `json:"maxSafeStopDurationSeconds"`
	MaxConsecutiveDenies             int      `json:"maxConsecutiveDenies"`
	StopMaxUsedPercent               float64  `json:"stopMaxUsedPercent"`
	IdleTimeoutGateSeconds           int      `json:"idleTimeoutGateSeconds"`
	WarnCooldownSeconds              int      `json:"warnCooldownSeconds"`
	WarnStepPercent                  float64  `json:"warnStepPercent"`
	RequiredSkills                   []string `json:"requiredSkills"`
	HandoffMarkerGlobs               []string `json:"handoffMarkerGlobs"`
	AllowReadOwnHandoffAfterComplete bool     `json:"allowReadOwnHandoffAfterComplete"`
	OnUnavailable                    string   `json:"onUnavailable"`
	FailOpenOnInternalError          bool     `json:"failOpenOnInternalError"`
}

// Policy defines all quota thresholds and safe-stop orchestration rules.
type Policy struct {
	FiveHour   WindowThreshold `json:"fiveHour"`
	SevenDay   WindowThreshold `json:"sevenDay"`
	SpendLimit WindowThreshold `json:"spendLimit"`
	APIBudget  WindowThreshold `json:"apiBudget"`
	SafeStop   SafeStopPolicy  `json:"safeStop"`
}

// DefaultPolicy returns the Plan v3 default guardrail policy.
func DefaultPolicy() Policy {
	return Policy{
		FiveHour: WindowThreshold{
			WarnPercent:        72.0,
			SafeStopPercent:    85.0,
			HardBlockPercent:   95.0,
			HardCeilingPercent: 98.0,
			HysteresisPercent:  3.0,
		},
		SevenDay: WindowThreshold{
			WarnPercent:        78.0,
			SafeStopPercent:    90.0,
			HardBlockPercent:   96.0,
			HardCeilingPercent: 98.0,
			HysteresisPercent:  2.0,
		},
		SpendLimit: WindowThreshold{
			WarnPercent:        75.0,
			SafeStopPercent:    88.0,
			HardBlockPercent:   95.0,
			HardCeilingPercent: 98.0,
			HysteresisPercent:  2.0,
		},
		APIBudget: WindowThreshold{
			WarnPercent:        75.0,
			SafeStopPercent:    88.0,
			HardBlockPercent:   95.0,
			HardCeilingPercent: 98.0,
			HysteresisPercent:  2.0,
		},
		SafeStop: SafeStopPolicy{
			GraceToolCallsBeforeRestrict:     0,
			GraceSecondsBeforeRestrict:       0,
			PredictiveStopMinutes:            10,
			WarnAgentBlockPercent5h:          82.0,
			WarnAgentBlockPercent7d:          86.0,
			MaxSafeStopToolCalls:             6,
			MaxSafeStopDurationSeconds:       120,
			MaxConsecutiveDenies:             2,
			StopMaxUsedPercent:               97.0,
			IdleTimeoutGateSeconds:           45,
			WarnCooldownSeconds:              300,
			WarnStepPercent:                  5.0,
			RequiredSkills:                   []string{"safe-stop", "parada-segura", "handoff"},
			HandoffMarkerGlobs:               []string{".handoff/**", "HANDOFF.md", "*handoff*.md", "NEXT_STEPS.md", "PROXIMOS_PASSOS.md"},
			AllowReadOwnHandoffAfterComplete: false,
			OnUnavailable:                    "allow-silent",
			FailOpenOnInternalError:          true,
		},
	}
}

// SessionState tracks the guardrail lifecycle for a single agent session shard.
type SessionState struct {
	SchemaVersion      string       `json:"schemaVersion"`
	SessionID          string       `json:"sessionId"`
	Origin             string       `json:"origin"`
	Provider           Provider     `json:"provider"`
	Phase              SessionPhase `json:"phase"`
	SafeStopEnteredAt  *time.Time   `json:"safeStopEnteredAt,omitempty"`
	SafeStopToolCalls  int          `json:"safeStopToolCalls"`
	ConsecutiveDenies  int          `json:"consecutiveDenies"`
	LastWarnAt         *time.Time   `json:"lastWarnAt,omitempty"`
	LastWarnPercent    float64      `json:"lastWarnPercent,omitempty"`
	HandoffMarkerPath  string       `json:"handoffMarkerPath,omitempty"`
	HandoffCompletedAt *time.Time   `json:"handoffCompletedAt,omitempty"`
	WindowResetAt      time.Time    `json:"windowResetAt,omitempty"`
	UpdatedAt          time.Time    `json:"updatedAt"`
}

// AccountGate represents an active account-level safe-stop lock shard.
// Local clock tie-breaker fields are intentionally omitted from the JSON payload;
// cross-OS ordering and idle timeouts rely strictly on NTFS file mtime.
type AccountGate struct {
	SchemaVersion         string       `json:"schemaVersion"`
	Provider              Provider     `json:"provider"`
	Origin                string       `json:"origin"`
	HolderSessionID       string       `json:"holderSessionId"`
	State                 SessionPhase `json:"state"`
	WindowResetAt         time.Time    `json:"windowResetAt"`
	SafeStopToolCallsUsed int          `json:"safeStopToolCallsUsed"`
}

// HookErrorHealth records the most recent sanitized hook error for an origin.
type HookErrorHealth struct {
	SchemaVersion string    `json:"schemaVersion"`
	Origin        string    `json:"origin"`
	HookName      string    `json:"hookName"`
	Provider      Provider  `json:"provider,omitempty"`
	Message       string    `json:"message"`
	OccurredAt    time.Time `json:"occurredAt"`
	Count         int       `json:"count"`
}

// BurnRateResult summarizes linear quota burn rate and projected exhaustion.
type BurnRateResult struct {
	RatePerMinute          float64    `json:"ratePerMinute"`
	Valid                  bool       `json:"valid"`
	SampleCount            int        `json:"sampleCount"`
	ProjectedExhaustionAt  *time.Time `json:"projectedExhaustionAt,omitempty"`
	MinutesToExhaustion    float64    `json:"minutesToExhaustion,omitempty"`
	WillExhaustBeforeReset bool       `json:"willExhaustBeforeReset"`
}

// AggregatedQuota holds the monotonic cross-origin merged quota view for a provider.
type AggregatedQuota struct {
	Provider        Provider         `json:"provider"`
	CapturedAt      time.Time        `json:"capturedAt"`
	Confidence      Confidence       `json:"confidence"`
	Windows         QuotaWindows     `json:"windows"`
	APIBudget       *APIBudgetStatus `json:"apiBudget,omitempty"`
	BurnRate5h      BurnRateResult   `json:"burnRate5h"`
	BurnRate7d      BurnRateResult   `json:"burnRate7d"`
	WinningOrigin5h string           `json:"winningOrigin5h,omitempty"`
	WinningOrigin7d string           `json:"winningOrigin7d,omitempty"`
	ActiveShards    int              `json:"activeShards"`
	DiscardedShards int              `json:"discardedShards"`
	Samples         []UsageSample    `json:"samples,omitempty"`
}
