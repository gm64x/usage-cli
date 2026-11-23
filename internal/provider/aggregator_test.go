package provider

import (
	"testing"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
)

func TestAggregateSnapshotsClockSkewMonotonicMerge(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
	reset5h := now.Add(3 * time.Hour)
	reset7d := now.Add(5 * 24 * time.Hour)

	// win.json has +5m future skew and LOWER usage (62%),
	// wsl-Ubuntu.json has -5m past skew and HIGHER usage (86%).
	// Within the same active window (|ΔresetsAt| <= 120s), monotonic merge MUST pick 86%
	// ignoring local CapturedAt timestamps!
	shards := []domain.QuotaSnapshot{
		{
			SchemaVersion:   domain.SchemaVersion,
			ContractVersion: domain.ContractVersion,
			Provider:        domain.ProviderClaude,
			Origin:          "win",
			CapturedAt:      now.Add(5 * time.Minute), // +5m skew
			Source:          "oauth",
			Confidence:      domain.ConfidenceExact,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{UsedPercent: 62.0, ResetsAt: reset5h},
				SevenDay: &domain.QuotaWindow{UsedPercent: 40.0, ResetsAt: reset7d},
			},
			Samples: []domain.UsageSample{
				{Timestamp: now.Add(-15 * time.Minute), UsedPercent: 55.0, ResetsAt: reset5h, Window: "fiveHour"},
			},
		},
		{
			SchemaVersion:   domain.SchemaVersion,
			ContractVersion: domain.ContractVersion,
			Provider:        domain.ProviderClaude,
			Origin:          "wsl-Ubuntu",
			CapturedAt:      now.Add(-5 * time.Minute), // -5m skew
			Source:          "oauth",
			Confidence:      domain.ConfidenceStale,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{UsedPercent: 86.0, ResetsAt: reset5h.Add(45 * time.Second)},
				SevenDay: &domain.QuotaWindow{UsedPercent: 38.0, ResetsAt: reset7d},
			},
			Samples: []domain.UsageSample{
				{Timestamp: now.Add(-5 * time.Minute), UsedPercent: 86.0, ResetsAt: reset5h, Window: "fiveHour"},
			},
		},
	}

	agg := AggregateSnapshots(shards, now, 10*time.Minute)
	if agg.Windows.FiveHour == nil || agg.Windows.FiveHour.UsedPercent != 86.0 {
		t.Fatalf("expected monotonic max FiveHour=86.0, got %+v", agg.Windows.FiveHour)
	}
	if agg.WinningOrigin5h != "wsl-Ubuntu" {
		t.Fatalf("WinningOrigin5h = %q, want %q", agg.WinningOrigin5h, "wsl-Ubuntu")
	}
	if agg.Windows.SevenDay == nil || agg.Windows.SevenDay.UsedPercent != 40.0 || agg.WinningOrigin7d != "win" {
		t.Fatalf("expected SevenDay=40.0 from win, got %+v (origin=%q)", agg.Windows.SevenDay, agg.WinningOrigin7d)
	}
	// Confidence preserves highest across active shards (exact > stale).
	if agg.Confidence != domain.ConfidenceExact {
		t.Fatalf("Confidence = %q, want %q", agg.Confidence, domain.ConfidenceExact)
	}
	if agg.ActiveShards != 2 || agg.DiscardedShards != 0 {
		t.Fatalf("ActiveShards=%d DiscardedShards=%d, want 2/0", agg.ActiveShards, agg.DiscardedShards)
	}
	// Combined samples across shards: 55% at -15m -> 86% at -5m (3.1%/min over 10m).
	if !agg.BurnRate5h.Valid || agg.BurnRate5h.RatePerMinute < 3.0 {
		t.Fatalf("expected valid combined BurnRate5h >= 3.0, got %+v", agg.BurnRate5h)
	}
}

func TestAggregateSnapshotsWindowRolloverDiscardsStaleHighShard(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 18, 5, 0, 0, time.UTC)
	oldReset := now.Add(-1 * time.Minute) // still within 120s grace of wall clock, BUT...
	newReset := now.Add(5 * time.Hour)    // newer window exists with resetsAt > oldReset + 120s!

	shards := []domain.QuotaSnapshot{
		{
			Provider:   domain.ProviderClaude,
			Origin:     "win",
			CapturedAt: now.Add(-10 * time.Minute),
			Confidence: domain.ConfidenceExact,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{UsedPercent: 96.0, ResetsAt: oldReset},
			},
		},
		{
			Provider:   domain.ProviderClaude,
			Origin:     "wsl-Ubuntu",
			CapturedAt: now,
			Confidence: domain.ConfidenceExact,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{UsedPercent: 3.0, ResetsAt: newReset},
			},
		},
	}

	agg := AggregateSnapshots(shards, now, 10*time.Minute)
	if agg.Windows.FiveHour == nil || agg.Windows.FiveHour.UsedPercent != 3.0 {
		t.Fatalf("expected rolled-over shard (96%%) discarded in favor of 3%%, got %+v", agg.Windows.FiveHour)
	}
	if agg.WinningOrigin5h != "wsl-Ubuntu" {
		t.Fatalf("WinningOrigin5h = %q, want wsl-Ubuntu", agg.WinningOrigin5h)
	}
	if agg.ActiveShards != 1 || agg.DiscardedShards != 1 {
		t.Fatalf("ActiveShards=%d DiscardedShards=%d, want 1/1", agg.ActiveShards, agg.DiscardedShards)
	}
}

func TestAggregateSnapshotsAllExpiredReturnsUnavailable(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 18, 10, 0, 0, time.UTC)
	expiredReset := now.Add(-5 * time.Minute) // > 120s past reset

	shards := []domain.QuotaSnapshot{
		{
			Provider:   domain.ProviderClaude,
			Origin:     "win",
			CapturedAt: now.Add(-15 * time.Minute),
			Confidence: domain.ConfidenceExact,
			Windows: domain.QuotaWindows{
				FiveHour: &domain.QuotaWindow{UsedPercent: 88.0, ResetsAt: expiredReset},
			},
		},
	}

	agg := AggregateSnapshots(shards, now, 10*time.Minute)
	if agg.Confidence != domain.ConfidenceUnavailable {
		t.Fatalf("Confidence = %q, want unavailable", agg.Confidence)
	}
	if agg.Windows.FiveHour != nil {
		t.Fatalf("expected expired FiveHour window discarded, got %+v", agg.Windows.FiveHour)
	}
	if agg.ActiveShards != 0 || agg.DiscardedShards != 1 {
		t.Fatalf("ActiveShards=%d DiscardedShards=%d, want 0/1", agg.ActiveShards, agg.DiscardedShards)
	}
}

func TestAggregateSnapshotsWindowlessEmptyConfidenceDiscarded(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 18, 15, 0, 0, time.UTC)
	shards := []domain.QuotaSnapshot{
		{
			Provider:   domain.ProviderClaude,
			Origin:     "win",
			CapturedAt: now,
			Confidence: "", // normalized to ConfidenceUnavailable
		},
	}

	agg := AggregateSnapshots(shards, now, 10*time.Minute)
	if agg.Confidence != domain.ConfidenceUnavailable {
		t.Fatalf("Confidence = %q, want unavailable", agg.Confidence)
	}
	if agg.ActiveShards != 0 || agg.DiscardedShards != 1 {
		t.Fatalf("ActiveShards=%d DiscardedShards=%d, want 0/1", agg.ActiveShards, agg.DiscardedShards)
	}
}
