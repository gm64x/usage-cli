package security

import (
	"path/filepath"
	"strings"
	"time"
)

// TelemetryEvent represents the strictly minimal, sanitized telemetry record allowed on disk.
// Full commands, prompts, file contents, and full paths must never be stored here.
type TelemetryEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	Origin       string    `json:"origin"`
	Provider     string    `json:"provider"`
	ToolName     string    `json:"toolName,omitempty"`
	Decision     string    `json:"decision"`
	UsedPct5h    float64   `json:"usedPct5h"`
	UsedPct7d    float64   `json:"usedPct7d"`
	State        string    `json:"state"`
	PathBasename string    `json:"pathBasename,omitempty"`
}

// SanitizeTelemetry returns a sanitized copy of ev with PathBasename stripped to its
// base filename and all string fields passed through Redact.
func SanitizeTelemetry(ev TelemetryEvent) TelemetryEvent {
	return TelemetryEvent{
		Timestamp:    ev.Timestamp.UTC(),
		Origin:       Redact(strings.TrimSpace(ev.Origin)),
		Provider:     Redact(strings.TrimSpace(ev.Provider)),
		ToolName:     Redact(strings.TrimSpace(ev.ToolName)),
		Decision:     Redact(strings.TrimSpace(ev.Decision)),
		UsedPct5h:    ev.UsedPct5h,
		UsedPct7d:    ev.UsedPct7d,
		State:        Redact(strings.TrimSpace(ev.State)),
		PathBasename: sanitizeBasename(ev.PathBasename),
	}
}

func sanitizeBasename(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// Normalize Windows separators so cross-OS paths still strip to basename on Linux/WSL.
	normalized := strings.ReplaceAll(trimmed, `\`, "/")
	base := filepath.Base(filepath.FromSlash(normalized))
	if base == "." || base == string(filepath.Separator) || base == "/" {
		return ""
	}
	return Redact(base)
}
