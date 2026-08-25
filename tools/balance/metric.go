package balance

// metricRealHours is the only metric this package computes today (AC-5).
// A scenario's target.metric must equal this string; it is the one figure
// that directly answers "which configurations land in the 80-150 real-hour
// band" (§3, MOD-036).
const metricRealHours = "realHoursToMilestone"

// realHours computes the real-world hours to reach the population milestone
// at a secondsPerMonthAt1x pacing, from a simulated-months figure read out of
// the headless run's end-state (AC-5's exact formula: simulated months
// elapsed to cross the milestone × secondsPerMonthAt1x ÷ 3600).
//
// Pure arithmetic over an integer simulated-months count and a scenario
// float — no wall clock, no cross-shard float summation — so the result is
// bit-identical for the same inputs on every platform Go supports (GR#21).
func realHours(simulatedMonths int64, secondsPerMonthAt1x float64) float64 {
	return float64(simulatedMonths) * secondsPerMonthAt1x / 3600.0
}

// Proposal returns the completed records whose real-hours figure falls within
// the target band, in deterministic ascending (sweep-point, seed) order — the
// achievable-but-hard candidate set a BA/Aaron approves row-by-row (the
// balance-number regime: a delegate proposal, never applied directly).
// Records with a nil RealHours (non-completed) are excluded.
func Proposal(target Target, records []CellResult) []CellResult {
	var out []CellResult
	for _, r := range records {
		if r.Status != StatusCompleted || r.RealHours == nil {
			continue
		}
		if *r.RealHours >= target.Band[0] && *r.RealHours <= target.Band[1] {
			out = append(out, r)
		}
	}
	return out
}
