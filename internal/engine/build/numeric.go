package build

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is engine.build's remaining numeric helper after the FEAT-086
// DRY refactor: the shared saturating arithmetic (SatAdd/SatSub/SafeMul)
// and the float64→int64 choke point (ClampInt64FromFloat) now live in
// foundation/num, which this package imports (see build.go and doc.go).
// Only the build-specific lead-time modulation stays here.

// effectiveLeadTime computes a §13-F3 base lead time (in simulation days)
// modulated by §9's construction-speed multiplier (winter < 1.0 → longer),
// as the integer number of simulation days one build order will spend in
// the queue:
//
//	effective = ceil(base / multiplier)
//
// It is the ONLY place a float64 seasonal multiplier touches this package's
// integer lead-time arithmetic, and it clamps every conversion through
// num.ClampInt64FromFloat. A non-positive, NaN, or infinite multiplier is
// rejected by the caller with ErrInvalidSeasonalMultiplier — this helper
// saturates defensively (to math.MaxInt64, "effectively never completes")
// rather than dividing by zero or producing +Inf. base is assumed
// non-negative (validated at load).
func effectiveLeadTime(base int64, multiplier float64) int64 {
	if !(multiplier > 0) || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return math.MaxInt64
	}
	eff := math.Ceil(float64(base) / multiplier)
	return num.ClampInt64FromFloat(eff)
}
