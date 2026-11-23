package domain

import (
	"sort"
	"time"
)

const (
	// MinBurnRateDeltaMinutes is the minimum time span required to compute a burn rate.
	MinBurnRateDeltaMinutes = 2.0
	// MinSignificantBurnRatePerMin is the minimum positive burn rate considered for projection.
	MinSignificantBurnRatePerMin = 0.05
)

// SameWindow reports whether two reset timestamps belong to the same quota window
// within WindowResetSkewTolerance (120 seconds).
func SameWindow(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	diff := a.Sub(b)
	if diff < 0 {
		diff = -diff
	}
	return diff <= WindowResetSkewTolerance
}

// AppendSample appends newSample to the ring buffer, dropping samples from previous windows
// and capping the slice at maxSamples (defaulting to MaxUsageSamples when <= 0).
func AppendSample(samples []UsageSample, newSample UsageSample, maxSamples int) []UsageSample {
	if maxSamples <= 0 {
		maxSamples = MaxUsageSamples
	}

	filtered := make([]UsageSample, 0, len(samples)+1)
	for _, s := range samples {
		if !newSample.ResetsAt.IsZero() && !s.ResetsAt.IsZero() && !SameWindow(s.ResetsAt, newSample.ResetsAt) {
			continue
		}
		if newSample.Window != "" && s.Window != "" && s.Window != newSample.Window {
			continue
		}
		filtered = append(filtered, s)
	}

	if n := len(filtered); n > 0 && filtered[n-1].Timestamp.Equal(newSample.Timestamp) {
		filtered[n-1] = newSample
	} else {
		filtered = append(filtered, newSample)
	}

	if len(filtered) > maxSamples {
		filtered = filtered[len(filtered)-maxSamples:]
	}

	out := make([]UsageSample, len(filtered))
	copy(out, filtered)
	return out
}

// CalculateBurnRate computes quota burn rate purely in memory from samples belonging to
// windowResetsAt. It enforces >= 2 samples, >= 2.0 minutes span, same-window filtering
// (|ΔresetsAt| <= 120s), and projects exhaustion when rate > 0.05%/min.
func CalculateBurnRate(samples []UsageSample, windowResetsAt time.Time, remainingPct float64, now time.Time) BurnRateResult {
	matching := make([]UsageSample, 0, len(samples))
	for _, s := range samples {
		if !windowResetsAt.IsZero() && !SameWindow(s.ResetsAt, windowResetsAt) {
			continue
		}
		matching = append(matching, s)
	}

	if len(matching) < 2 {
		return BurnRateResult{SampleCount: len(matching)}
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Timestamp.Before(matching[j].Timestamp)
	})

	oldest := matching[0]
	newest := matching[len(matching)-1]
	deltaMinutes := newest.Timestamp.Sub(oldest.Timestamp).Minutes()
	if deltaMinutes < MinBurnRateDeltaMinutes {
		return BurnRateResult{SampleCount: len(matching)}
	}

	rate := (newest.UsedPercent - oldest.UsedPercent) / deltaMinutes
	if rate <= 0 {
		return BurnRateResult{
			RatePerMinute: 0,
			Valid:         true,
			SampleCount:   len(matching),
		}
	}

	res := BurnRateResult{
		RatePerMinute: rate,
		Valid:         true,
		SampleCount:   len(matching),
	}

	if rate <= MinSignificantBurnRatePerMin {
		return res
	}

	if remainingPct <= 0 {
		projected := now
		res.ProjectedExhaustionAt = &projected
		res.MinutesToExhaustion = 0
		res.WillExhaustBeforeReset = !windowResetsAt.IsZero() && !now.After(windowResetsAt)
		return res
	}

	minutesToExhaustion := remainingPct / rate
	projected := now.Add(time.Duration(minutesToExhaustion * float64(time.Minute)))
	res.MinutesToExhaustion = minutesToExhaustion
	res.ProjectedExhaustionAt = &projected
	if !windowResetsAt.IsZero() && projected.Before(windowResetsAt) {
		res.WillExhaustBeforeReset = true
	}

	return res
}
