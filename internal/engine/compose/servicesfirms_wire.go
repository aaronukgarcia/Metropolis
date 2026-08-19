package compose

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-167 completion (docs/planning/icd/engine.services-coverage.md,
// docs/planning/icd/engine.firms-labourmarket.md): the last two §11 attract
// terms — ServiceCoverage and JobAvailability — replacing the flat
// baselineOneTermValue=50.0 placeholder the ORIGINAL FEAT-167 wave
// deliberately left undone (docs/planning/icd/engine.attract-terms.md §3:
// "no usable real signal today"). Both aggregates (engine.services'
// CoverageSummary, engine.firms' LabourMarket) have since landed
// independently (b47eb3a, fbbe57b) — this file is compose's bridge to them,
// mirroring safetyTerm/leisureFitTerm/environmentTerm's shape in compose.go.

// serviceCoverageTerm queries engine.services' city-wide CoverageSummary
// (ICD engine.services-coverage.md §3/§4) and scales its already-[0,1]-
// clamped CoverageRatio onto attract's [0,100] term scale via
// data/attract_terms.json's serviceCoverage.coverageRatioScalePercent
// (GR#15). Deliberate choice (per the ICD's own round note): city-wide
// TotalDemand is NOT guaranteed to equal the sum of per-district demand (the
// two channels are independent pushes), so this term reads the CITYWIDE
// CoverageSummary consistently — never CoverageByDistrict — rather than
// re-deriving a citywide figure from the district breakdown. Baseline one
// wires no automatic engine.build -> engine.services registration bridge
// (a separate, larger integration outside this ICD's scope — its own open
// decision 1), so in an unmodified run CoverageSummary reports zero
// registered instances, TotalDemand=0, and coverageRatio's own "1.0 when
// demand is zero" rule holds: the term legitimately reads 100 until that
// bridge lands. Proven to move under a direct engine.services mutation
// (RegisterService + UpdateDemand) in servicesfirms_wire_test.go.
func (st *simState) serviceCoverageTerm() (float64, error) {
	summary, err := st.services.CoverageSummary()
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "services"})
	}
	scale := st.attractTerms.ServiceCoverage.CoverageRatioScalePercent / 100.0
	term := 100 * summary.CoverageRatio * scale
	return clampTerm(term), nil
}

// jobAvailabilityTerm queries engine.firms' city-wide LabourMarket
// aggregate (ICD engine.firms-labourmarket.md §3/§4) and maps its
// VacancyRatePerMille onto attract's [0,100] term scale through a
// half-saturation curve — the same shape environmentTerm's
// pollutionHalfSaturationKg already uses (data/attract_terms.json's
// jobAvailability.vacancyRateHalfSaturationPerMille, GR#15). A half-
// saturation curve is required here (rather than a linear scale) because
// VacancyRatePerMille carries NO upper clamp (the ICD's own doc: vacancies
// can exceed workforce), so a linear mapping could run past 100 without a
// saturating shape. Baseline one wires no automatic firm-founding into the
// composition root (a separate, larger integration outside this ICD's
// scope), so in an unmodified run TotalVacancies is legitimately 0 and this
// term reads 0 until that lands. Proven to move under a direct engine.firms
// mutation (RegisterFirm growing headroom) in servicesfirms_wire_test.go.
func (st *simState) jobAvailabilityTerm() (float64, error) {
	lm, err := st.firms.LabourMarket()
	if err != nil {
		return 0, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "firms"})
	}
	rate := float64(lm.VacancyRatePerMille)
	if rate < 0 {
		rate = 0
	}
	half := st.attractTerms.JobAvailability.VacancyRateHalfSaturationPerMille
	term := 100 * rate / (rate + half)
	return clampTerm(term), nil
}

// clampTerm bounds a computed term value to attract's documented [0,100]
// domain (AttractAPI.SetTermInputs' own validateTermInputs enforces this
// too, but every other term function in this package — safetyTerm/
// leisureFitTerm/environmentTerm — already returns a bounded value by
// construction; this defends the two new terms against a non-finite or
// out-of-range half-saturation edge case rather than letting
// SetTermInputs reject the whole monthly migration step for an arithmetic
// slip). NaN is explicitly guarded (round-verdict hardening item, 2026-08-19):
// today's two callers can never produce NaN (jobAvailabilityTerm's rate/half
// are both non-negative finite, so rate+half > 0 always; serviceCoverageTerm's
// CoverageRatio is already clamped finite by the services package, and
// CoverageRatioScalePercent is validated finite/>0 at load), so this branch
// is currently unreachable — but clampTerm is the composition root's last
// line of defence before an attract.SetTermInputs call, and a plain `v < 0`
// comparison against NaN is false (IEEE 754: every comparison with NaN
// except != is false), so an unguarded clampTerm would let a future NaN
// silently fall through the v>100 check too and reach SetTermInputs
// unclamped. Treated the same as the <0 branch (return the lower bound),
// mirroring the "fail toward the safe/neutral edge, never propagate a
// corrupt float" discipline the rest of this package already follows.
func clampTerm(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
