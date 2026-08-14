package attract

import "math"

// This file is engine.attract's remaining numeric helpers after the
// FEAT-086 DRY refactor. The shared saturating arithmetic (SatAdd/SatSub/
// SafeMul), the float64→int64 choke point (ClampInt64FromFloat), and the
// float64 finiteness guard (IsFinite) now live in foundation/num, which
// this package imports (see migration.go, api.go, config.go, weights.go).
// Only the attract-specific bounded-float/div helpers stay here.

// clampFloat clamps v into [lo, hi]; NaN clamps to lo (documented — the
// only sane NaN answer for a bounded quality/hazard value).
func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// minI64 returns the smaller of a and b.
func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// positiveDiv divides a non-negative numerator by a positive denominator,
// truncating toward zero; a negative numerator or a non-positive
// denominator yields 0 (defensive — a per-household average must never go
// negative or divide by zero).
func positiveDiv(numerator, denominator int64) int64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	return numerator / denominator
}
