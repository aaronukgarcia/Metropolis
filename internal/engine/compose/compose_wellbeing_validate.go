package compose

import (
	"fmt"
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-1231 r2: the saved wellbeing record is installed as the live month-start
// status, and three consumers multiply by it (mortality, migration
// satisfaction and emigration, firm productivity). A damaged or hand-edited
// record must therefore be refused before anything is installed.
//
// The bounds are derived from the engine's own arithmetic, none is invented:
//
//   - sampleSize: a count of attributed citizens, 0..wellbeingSampleCap
//     (reconstructWellbeing, compose_wellbeing.go: ids capped at
//     wellbeingSampleCap, count only increments per attributed citizen).
//   - noData: true exactly when sampleSize is 0 (computeWellbeingStatus,
//     compose_wellbeing.go:count == 0 branch), and then both means are 0 and
//     every modifier is wellbeingNeutralModifier.
//   - meanPhysical/meanMental: the mean of per-citizen track totals, so they lie
//     inside the per-citizen total range, which wellbeing.TrackEnvelope derives
//     from the live configuration (baseline +-3 jitter, driver weights, age
//     curve, seasonal wave: wellbeing/attribute.go:31-58, drivers.go). The tracks
//     are NOT clamped to [0,100] by the module, so [0,100] would wrongly refuse
//     a legitimate city whose healthcare/diet/travel/sport terms push the
//     physical total above 100.
//   - the four modifiers: each is linear in the track mean
//     (wellbeing/drivers.go:265-289), so for means inside the envelope each lies
//     between its values at the two envelope corners. The module's own
//     functions are evaluated at those corners; nothing is hard coded.
//
// Unknown extra keys in the record are tolerated (the ledger wire is read with
// encoding/json, which ignores them) so a later version can add fields and an
// older build still loads the save; the fields that matter are all checked.

// wellbeingBoundsEps is the relative slack on the derived bounds, covering the
// float rounding of evaluating the same linear function at the corners.
const wellbeingBoundsEps = 1e-9

func wellbeingRefuse(cid, field string, value any, rule string) error {
	return errs.New(ErrSavedWellbeingInvalid, cid, map[string]any{
		"field": field,
		"value": fmt.Sprint(value),
		"rule":  rule,
	})
}

func finiteIn(v, lo, hi float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	slack := wellbeingBoundsEps * (1 + math.Abs(lo) + math.Abs(hi))
	return v >= lo-slack && v <= hi+slack
}

// validateSavedWellbeing refuses a saved wellbeing record the engine could not
// have produced. w is the live wellbeing module (bounds source); a nil w means
// the composition has no wellbeing module, in which case the record cannot be
// checked and is refused rather than trusted.
func validateSavedWellbeing(w *wellbeing.WellbeingAPI, cid string, ww wellbeingStatusWire) error {
	if w == nil {
		return wellbeingRefuse(cid, "wellbeing", "present", "no wellbeing module to check it against")
	}
	if ww.SampleSize < 0 || ww.SampleSize > wellbeingSampleCap {
		return wellbeingRefuse(cid, "sampleSize", ww.SampleSize, fmt.Sprintf("must be in [0,%d]", wellbeingSampleCap))
	}
	if ww.NoData != (ww.SampleSize == 0) {
		return wellbeingRefuse(cid, "noData", ww.NoData, fmt.Sprintf("must be true exactly when sampleSize is 0 (sampleSize %d)", ww.SampleSize))
	}
	mods := []struct {
		name string
		v    float64
		fn   func(p, m float64) float64
	}{
		{"mortalityModifier", ww.MortalityModifier, w.MortalityModifier},
		{"productivityModifier", ww.ProductivityModifier, w.ProductivityModifier},
		{"satisfactionModifier", ww.SatisfactionModifier, w.SatisfactionModifier},
		{"emigrationModifier", ww.EmigrationModifier, w.EmigrationModifier},
	}
	if ww.NoData {
		if ww.MeanPhysical != 0 {
			return wellbeingRefuse(cid, "meanPhysical", ww.MeanPhysical, "must be 0 when noData")
		}
		if ww.MeanMental != 0 {
			return wellbeingRefuse(cid, "meanMental", ww.MeanMental, "must be 0 when noData")
		}
		for _, m := range mods {
			if m.v != wellbeingNeutralModifier {
				return wellbeingRefuse(cid, m.name, m.v, fmt.Sprintf("must be the neutral %v when noData", wellbeingNeutralModifier))
			}
		}
		return nil
	}
	pLo, pHi, mLo, mHi := w.TrackEnvelope()
	if !finiteIn(ww.MeanPhysical, pLo, pHi) {
		return wellbeingRefuse(cid, "meanPhysical", ww.MeanPhysical, fmt.Sprintf("must be finite and in [%v,%v]", pLo, pHi))
	}
	if !finiteIn(ww.MeanMental, mLo, mHi) {
		return wellbeingRefuse(cid, "meanMental", ww.MeanMental, fmt.Sprintf("must be finite and in [%v,%v]", mLo, mHi))
	}
	for _, m := range mods {
		a, b := m.fn(pLo, mLo), m.fn(pHi, mHi)
		lo, hi := math.Min(a, b), math.Max(a, b)
		if !finiteIn(m.v, lo, hi) {
			return wellbeingRefuse(cid, m.name, m.v, fmt.Sprintf("must be finite and in [%v,%v]", lo, hi))
		}
	}
	return nil
}
