package provider

import (
	"math"
	"sort"
	"time"

	"github.com/gm64x/usage-cli/internal/domain"
)

type windowCandidate struct {
	origin     string
	window     *domain.QuotaWindow
	confidence domain.Confidence
	shardIndex int
}

// AggregateSnapshots performs a clock-skew-proof monotonic merge across per-origin
// QuotaSnapshot shards. It discards expired or rolled-over window shards, picks max(UsedPercent)
// within the same active window (|ΔresetsAt| <= 120s) regardless of local CapturedAt skew,
// preserves the highest confidence, and computes combined burn rates.
func AggregateSnapshots(shards []domain.QuotaSnapshot, now time.Time, staleAfter time.Duration) domain.AggregatedQuota {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if len(shards) == 0 {
		return domain.AggregatedQuota{
			CapturedAt: now.UTC(),
			Confidence: domain.ConfidenceUnavailable,
		}
	}

	provider := shards[0].Provider
	fiveHourCandidates := make([]windowCandidate, 0, len(shards))
	sevenDayCandidates := make([]windowCandidate, 0, len(shards))
	spendLimitCandidates := make([]windowCandidate, 0, len(shards))
	shardEffConf := make([]domain.Confidence, len(shards))
	shardHasWindow := make([]bool, len(shards))
	shardHasActiveWindow := make([]bool, len(shards))

	for i, s := range shards {
		if provider == "" && s.Provider != "" {
			provider = s.Provider
		}
		conf := effectiveConfidence(s, now, staleAfter)
		shardEffConf[i] = conf

		if s.Windows.FiveHour != nil {
			shardHasWindow[i] = true
			fiveHourCandidates = append(fiveHourCandidates, windowCandidate{
				origin:     s.Origin,
				window:     s.Windows.FiveHour,
				confidence: conf,
				shardIndex: i,
			})
		}
		if s.Windows.SevenDay != nil {
			shardHasWindow[i] = true
			sevenDayCandidates = append(sevenDayCandidates, windowCandidate{
				origin:     s.Origin,
				window:     s.Windows.SevenDay,
				confidence: conf,
				shardIndex: i,
			})
		}
		if s.Windows.SpendLimit != nil {
			shardHasWindow[i] = true
			spendLimitCandidates = append(spendLimitCandidates, windowCandidate{
				origin:     s.Origin,
				window:     s.Windows.SpendLimit,
				confidence: conf,
				shardIndex: i,
			})
		}
	}

	win5h, origin5h, active5h := mergeWindow(fiveHourCandidates, now)
	win7d, origin7d, active7d := mergeWindow(sevenDayCandidates, now)
	winSpend, _, activeSpend := mergeWindow(spendLimitCandidates, now)

	for _, idx := range active5h {
		shardHasActiveWindow[idx] = true
	}
	for _, idx := range active7d {
		shardHasActiveWindow[idx] = true
	}
	for _, idx := range activeSpend {
		shardHasActiveWindow[idx] = true
	}

	// Merge optional APIBudget and determine shard activity.
	var bestBudget *domain.APIBudgetStatus
	for i, s := range shards {
		if s.APIBudget != nil {
			expired := !s.APIBudget.ResetsAt.IsZero() && now.After(s.APIBudget.ResetsAt.Add(domain.WindowResetSkewTolerance))
			if !expired {
				shardHasActiveWindow[i] = true
				if bestBudget == nil || s.APIBudget.UsedPercent > bestBudget.UsedPercent {
					cp := *s.APIBudget
					bestBudget = &cp
				}
			}
		} else if !shardHasWindow[i] && shardEffConf[i] != domain.ConfidenceUnavailable {
			shardHasActiveWindow[i] = true
		}
	}

	activeShards := 0
	discardedShards := 0
	bestConfidence := domain.ConfidenceUnavailable
	combinedSamples := make([]domain.UsageSample, 0, domain.MaxUsageSamples*len(shards))

	for i, s := range shards {
		if !shardHasActiveWindow[i] {
			discardedShards++
			continue
		}
		activeShards++
		if domain.ConfidenceRank(shardEffConf[i]) > domain.ConfidenceRank(bestConfidence) {
			bestConfidence = shardEffConf[i]
		}
		combinedSamples = append(combinedSamples, s.Samples...)
	}

	if activeShards == 0 {
		bestConfidence = domain.ConfidenceUnavailable
	}

	out := domain.AggregatedQuota{
		Provider:   provider,
		CapturedAt: now.UTC(),
		Confidence: bestConfidence,
		Windows: domain.QuotaWindows{
			FiveHour:   win5h,
			SevenDay:   win7d,
			SpendLimit: winSpend,
		},
		APIBudget:       bestBudget,
		WinningOrigin5h: origin5h,
		WinningOrigin7d: origin7d,
		ActiveShards:    activeShards,
		DiscardedShards: discardedShards,
	}

	if win5h != nil {
		samples5h := filterWindowSamples(combinedSamples, win5h.ResetsAt, "fiveHour")
		rem5h := math.Max(0, 100.0-win5h.UsedPercent)
		out.BurnRate5h = domain.CalculateBurnRate(samples5h, win5h.ResetsAt, rem5h, now)
		if len(samples5h) > domain.MaxUsageSamples {
			samples5h = samples5h[len(samples5h)-domain.MaxUsageSamples:]
		}
		out.Samples = samples5h
	}

	if win7d != nil {
		samples7d := filterWindowSamples(combinedSamples, win7d.ResetsAt, "sevenDay")
		rem7d := math.Max(0, 100.0-win7d.UsedPercent)
		out.BurnRate7d = domain.CalculateBurnRate(samples7d, win7d.ResetsAt, rem7d, now)
	}

	return out
}

func effectiveConfidence(s domain.QuotaSnapshot, now time.Time, staleAfter time.Duration) domain.Confidence {
	conf := s.Confidence
	if conf == "" {
		conf = domain.ConfidenceUnavailable
	}
	if conf == domain.ConfidenceExact && staleAfter > 0 && !s.CapturedAt.IsZero() {
		if now.Sub(s.CapturedAt) > staleAfter {
			return domain.ConfidenceStale
		}
	}
	return conf
}

func mergeWindow(candidates []windowCandidate, now time.Time) (*domain.QuotaWindow, string, []int) {
	if len(candidates) == 0 {
		return nil, "", nil
	}

	// Step 1: Discard windows expired past now > resetsAt + 120s.
	nonExpired := make([]windowCandidate, 0, len(candidates))
	var maxResetsAt time.Time
	for _, c := range candidates {
		if c.window == nil {
			continue
		}
		if !c.window.ResetsAt.IsZero() {
			if now.After(c.window.ResetsAt.Add(domain.WindowResetSkewTolerance)) {
				continue
			}
			if maxResetsAt.IsZero() || c.window.ResetsAt.After(maxResetsAt) {
				maxResetsAt = c.window.ResetsAt
			}
		}
		nonExpired = append(nonExpired, c)
	}
	if len(nonExpired) == 0 {
		return nil, "", nil
	}

	// Step 2: Discard shards from older windows when another valid shard has resetsAt > shard.ResetsAt + 120s.
	active := make([]windowCandidate, 0, len(nonExpired))
	activeIndices := make([]int, 0, len(nonExpired))
	for _, c := range nonExpired {
		if !maxResetsAt.IsZero() && !c.window.ResetsAt.IsZero() {
			if maxResetsAt.Sub(c.window.ResetsAt) > domain.WindowResetSkewTolerance {
				continue
			}
		}
		active = append(active, c)
		activeIndices = append(activeIndices, c.shardIndex)
	}
	if len(active) == 0 {
		return nil, "", nil
	}

	// Step 3: Monotonically pick max(UsedPercent) within the active window, ignoring CapturedAt.
	best := active[0]
	for _, c := range active[1:] {
		if c.window.UsedPercent > best.window.UsedPercent {
			best = c
			continue
		}
		if c.window.UsedPercent == best.window.UsedPercent {
			if domain.ConfidenceRank(c.confidence) > domain.ConfidenceRank(best.confidence) {
				best = c
			} else if domain.ConfidenceRank(c.confidence) == domain.ConfidenceRank(best.confidence) && c.origin < best.origin {
				best = c
			}
		}
	}

	cp := *best.window
	if cp.RemainingPct == 0 && cp.UsedPercent < 100.0 {
		cp.RemainingPct = math.Max(0, 100.0-cp.UsedPercent)
	}
	return &cp, best.origin, activeIndices
}

func filterWindowSamples(samples []domain.UsageSample, resetsAt time.Time, windowName string) []domain.UsageSample {
	out := make([]domain.UsageSample, 0, len(samples))
	for _, s := range samples {
		if s.Window != "" && s.Window != windowName {
			continue
		}
		if !resetsAt.IsZero() && !domain.SameWindow(s.ResetsAt, resetsAt) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}
