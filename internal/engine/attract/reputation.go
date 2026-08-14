package attract

// reputationState is the §11 reputation-momentum state — the asymmetric,
// slow-moving term that makes the Detroit trap a mechanic rather than a
// marketing description (US-2/AC-5).
//
// Model (v1): reputation is a signed momentum value that lags the
// deviation of the six-term fundamentals (the mean of the six non-
// reputation terms) from a baseline anchored at the first observed value.
// The convergence rate is asymmetric: a positive deviation (fundamentals
// above baseline — a rising city) is chased at RiseRate, while an equal
// negative deviation (a falling city) is chased at the strictly-larger
// FallRate. So equal-magnitude, equal-duration positive and negative
// shocks move reputation farther in the negative direction — "cities
// rising attract beyond fundamentals; cities falling repel beyond
// fundamentals", with the repel stronger. A symmetric EMA would produce
// equal departures for equal stimuli and fail AC-5; the FallRate > RiseRate
// rule is enforced at Config validation (config.go) and exercised by the
// reputation asymmetry test.
//
// reputation contributes w₇·reputation to A, signed: positive reputation
// adds beyond the six fundamentals, negative reputation subtracts. At
// steady state reputation converges to zero and contributes nothing.
type reputationState struct {
	hasBaseline bool
	baseline    float64
	value       float64
}

// advance updates the momentum from the current six-term fundamentals mean
// f, using the config's asymmetric rates and clamp. The first advance
// anchors the baseline and leaves value at zero (a neutral start); each
// later advance moves value toward (f − baseline) at the direction-
// dependent rate.
func (r *reputationState) advance(f, riseRate, fallRate, max float64) {
	if !r.hasBaseline {
		r.baseline = f
		r.value = 0
		r.hasBaseline = true
		return
	}
	dev := f - r.baseline
	if dev >= r.value {
		r.value += riseRate * (dev - r.value)
	} else {
		r.value += fallRate * (dev - r.value)
	}
	r.value = clampFloat(r.value, -max, max)
}
