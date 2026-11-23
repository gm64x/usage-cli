package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefaultPolicyValues(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	p := DefaultPolicy()

	// FiveHour: 72 / 85 / 95 / 98 / hyst 3
	if p.FiveHour.WarnPercent != 72.0 ||
		p.FiveHour.SafeStopPercent != 85.0 ||
		p.FiveHour.HardBlockPercent != 95.0 ||
		p.FiveHour.HardCeilingPercent != 98.0 ||
		p.FiveHour.HysteresisPercent != 3.0 {
		t.Fatalf("unexpected FiveHour defaults: %+v", p.FiveHour)
	}

	// SevenDay: 78 / 90 / 96 / 98 / hyst 2
	if p.SevenDay.WarnPercent != 78.0 ||
		p.SevenDay.SafeStopPercent != 90.0 ||
		p.SevenDay.HardBlockPercent != 96.0 ||
		p.SevenDay.HardCeilingPercent != 98.0 ||
		p.SevenDay.HysteresisPercent != 2.0 {
		t.Fatalf("unexpected SevenDay defaults: %+v", p.SevenDay)
	}

	// SpendLimit & APIBudget: 75 / 88 / 95 / 98 / hyst 2
	for name, w := range map[string]WindowThreshold{
		"SpendLimit": p.SpendLimit,
		"APIBudget":  p.APIBudget,
	} {
		if w.WarnPercent != 75.0 ||
			w.SafeStopPercent != 88.0 ||
			w.HardBlockPercent != 95.0 ||
			w.HardCeilingPercent != 98.0 ||
			w.HysteresisPercent != 2.0 {
			t.Fatalf("unexpected %s defaults: %+v", name, w)
		}
	}

	// SafeStop defaults
	ss := p.SafeStop
	if ss.GraceToolCallsBeforeRestrict != 0 || ss.GraceSecondsBeforeRestrict != 0 {
		t.Fatalf("expected grace=0, got calls=%d seconds=%d", ss.GraceToolCallsBeforeRestrict, ss.GraceSecondsBeforeRestrict)
	}
	if ss.PredictiveStopMinutes != 10 ||
		ss.WarnAgentBlockPercent5h != 82.0 ||
		ss.WarnAgentBlockPercent7d != 86.0 ||
		ss.MaxSafeStopToolCalls != 6 ||
		ss.MaxSafeStopDurationSeconds != 120 ||
		ss.MaxConsecutiveDenies != 2 ||
		ss.StopMaxUsedPercent != 97.0 ||
		ss.IdleTimeoutGateSeconds != 45 ||
		ss.WarnCooldownSeconds != 300 ||
		ss.WarnStepPercent != 5.0 ||
		ss.AllowReadOwnHandoffAfterComplete ||
		ss.OnUnavailable != "allow-silent" ||
		!ss.FailOpenOnInternalError {
		t.Fatalf("unexpected SafeStop defaults: %+v", ss)
	}
	if len(ss.RequiredSkills) != 3 || len(ss.HandoffMarkerGlobs) != 5 {
		t.Fatalf("unexpected RequiredSkills=%v HandoffMarkerGlobs=%v", ss.RequiredSkills, ss.HandoffMarkerGlobs)
	}
}

func TestCalculateBurnRate(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)
	resetAt := now.Add(60 * time.Minute)

	t.Run("normal exhaustion before reset", func(t *testing.T) {
		samples := []UsageSample{
			{Timestamp: now.Add(-10 * time.Minute), UsedPercent: 60.0, ResetsAt: resetAt},
			{Timestamp: now, UsedPercent: 70.0, ResetsAt: resetAt.Add(30 * time.Second)}, // within 120s tolerance
		}
		res := CalculateBurnRate(samples, resetAt, 30.0, now)
		if !res.Valid {
			t.Fatalf("expected valid burn rate")
		}
		if res.RatePerMinute < 0.99 || res.RatePerMinute > 1.01 {
			t.Fatalf("RatePerMinute = %v, want 1.0", res.RatePerMinute)
		}
		if res.ProjectedExhaustionAt == nil {
			t.Fatalf("expected ProjectedExhaustionAt to be populated")
		}
		if !res.WillExhaustBeforeReset {
			t.Fatalf("expected WillExhaustBeforeReset=true (30m < 60m)")
		}
	})

	t.Run("normal no exhaustion before reset", func(t *testing.T) {
		samples := []UsageSample{
			{Timestamp: now.Add(-10 * time.Minute), UsedPercent: 20.0, ResetsAt: resetAt},
			{Timestamp: now, UsedPercent: 22.0, ResetsAt: resetAt}, // 0.2%/min, remaining 78% -> 390m > 60m
		}
		res := CalculateBurnRate(samples, resetAt, 78.0, now)
		if !res.Valid || res.ProjectedExhaustionAt == nil {
			t.Fatalf("expected valid projection, got %+v", res)
		}
		if res.WillExhaustBeforeReset {
			t.Fatalf("expected WillExhaustBeforeReset=false")
		}
	})

	t.Run("flat usage below 0.05 threshold", func(t *testing.T) {
		samples := []UsageSample{
			{Timestamp: now.Add(-20 * time.Minute), UsedPercent: 40.0, ResetsAt: resetAt},
			{Timestamp: now, UsedPercent: 40.5, ResetsAt: resetAt}, // 0.025%/min <= 0.05
		}
		res := CalculateBurnRate(samples, resetAt, 59.5, now)
		if !res.Valid {
			t.Fatalf("expected Valid=true for flat rate")
		}
		if res.ProjectedExhaustionAt != nil || res.WillExhaustBeforeReset {
			t.Fatalf("expected no exhaustion projection for flat rate <= 0.05, got %+v", res)
		}
	})

	t.Run("less than 2 minutes span guard", func(t *testing.T) {
		samples := []UsageSample{
			{Timestamp: now.Add(-90 * time.Second), UsedPercent: 40.0, ResetsAt: resetAt},
			{Timestamp: now, UsedPercent: 50.0, ResetsAt: resetAt},
		}
		res := CalculateBurnRate(samples, resetAt, 50.0, now)
		if res.Valid {
			t.Fatalf("expected Valid=false when delta < 2m, got %+v", res)
		}
	})

	t.Run("window boundary ignores previous window samples", func(t *testing.T) {
		oldReset := resetAt.Add(-5 * time.Hour)
		samples := []UsageSample{
			{Timestamp: now.Add(-20 * time.Minute), UsedPercent: 90.0, ResetsAt: oldReset},
			{Timestamp: now.Add(-5 * time.Minute), UsedPercent: 10.0, ResetsAt: resetAt},
		}
		res := CalculateBurnRate(samples, resetAt, 90.0, now)
		if res.Valid || res.SampleCount != 1 {
			t.Fatalf("expected only 1 sample in current window and Valid=false, got %+v", res)
		}
	})
}

func TestAppendSampleRingBuffer(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	base := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	reset1 := base.Add(5 * time.Hour)

	var samples []UsageSample
	for i := 0; i < 15; i++ {
		samples = AppendSample(samples, UsageSample{
			Timestamp:   base.Add(time.Duration(i) * time.Minute),
			UsedPercent: float64(i * 3),
			ResetsAt:    reset1,
			Window:      "fiveHour",
		}, 0)
	}
	if len(samples) != MaxUsageSamples {
		t.Fatalf("len(samples) = %d, want %d", len(samples), MaxUsageSamples)
	}
	if samples[0].UsedPercent != 9.0 || samples[len(samples)-1].UsedPercent != 42.0 {
		t.Fatalf("unexpected ring buffer bounds: first=%v last=%v", samples[0], samples[len(samples)-1])
	}

	// Window rollover drops prior window samples.
	reset2 := reset1.Add(5 * time.Hour)
	samples = AppendSample(samples, UsageSample{
		Timestamp:   base.Add(20 * time.Minute),
		UsedPercent: 1.0,
		ResetsAt:    reset2,
		Window:      "fiveHour",
	}, 12)
	if len(samples) != 1 || samples[0].UsedPercent != 1.0 {
		t.Fatalf("expected old window samples dropped on rollover, got %+v", samples)
	}
}

func TestAccountGateJSONHasNoLocalClockFields(t *testing.T) {
	t.Setenv("USAGE_CLI_STRICT", "1")
	_ = t.TempDir()

	gate := AccountGate{
		SchemaVersion:         SchemaVersion,
		Provider:              ProviderClaude,
		Origin:                "win",
		HolderSessionID:       "sess-1",
		State:                 PhaseSafeStopInProgress,
		WindowResetAt:         time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC),
		SafeStopToolCallsUsed: 2,
	}
	b, err := json.Marshal(gate)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	s := string(b)
	for _, forbidden := range []string{"updatedAt", "acquiredAt", "createdAt", "timestamp"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("AccountGate JSON must not contain local clock field %q: %s", forbidden, s)
		}
	}
}
