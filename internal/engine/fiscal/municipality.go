package fiscal

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// SetPlanningFunding sets the planning & administration funding level — the
// §54 "municipality as a modelled department" input (AC-5). level is a
// fraction of the data-authored target (0..1; 1.0 = the target funding in
// data/fiscal.json). A non-finite level or one outside [0,1] is rejected
// with ErrInvalidFundingLevel — never silently clamped (SEC-093/AC-12
// shape).
func (f *FiscalAPI) SetPlanningFunding(level float64) error {
	if err := f.checkNotCopied("SetPlanningFunding"); err != nil {
		return err
	}
	if !num.IsFinite(level) || level < 0 || level > 1 {
		return errs.New(ErrInvalidFundingLevel, f.correlationID, map[string]any{"level": level})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planningFunding = level
	return nil
}

// PlanningFunding returns the current planning & administration funding
// level (0..1).
func (f *FiscalAPI) PlanningFunding() float64 {
	if err := f.checkNotCopied("PlanningFunding"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.planningFunding
}

// PlanningFundingTarget returns the data-authored monthly funding target (a
// 100% funding level corresponds to this absolute amount).
func (f *FiscalAPI) PlanningFundingTarget() finance.Money {
	if err := f.checkNotCopied("PlanningFundingTarget"); err != nil {
		return 0
	}
	return finance.Money(f.cfg.Municipality.FundingTargetPerMonthMicroPounds)
}

// PermitSpeedMultiplier returns the permit-processing speed multiplier at the
// current funding level (AC-5): higher funding ⇒ faster permits. Linearly
// interpolated between the data's zero-funding and full-funding anchors.
func (f *FiscalAPI) PermitSpeedMultiplier() float64 {
	if err := f.checkNotCopied("PermitSpeedMultiplier"); err != nil {
		return 0
	}
	f.mu.RLock()
	level := f.planningFunding
	f.mu.RUnlock()
	m := f.cfg.Municipality
	return linearInterp(level, m.PermitSpeedAtZeroFunding, m.PermitSpeedAtFullFunding)
}

// BuildCostErrorRate returns the build-cost error rate (fraction of project
// cost) at the current funding level (AC-5): lower funding ⇒ higher error,
// matching §54's "10–20% over" underfunding outcome. Linearly interpolated
// between the data's zero-funding and full-funding anchors.
func (f *FiscalAPI) BuildCostErrorRate() float64 {
	if err := f.checkNotCopied("BuildCostErrorRate"); err != nil {
		return 0
	}
	f.mu.RLock()
	level := f.planningFunding
	f.mu.RUnlock()
	m := f.cfg.Municipality
	return linearInterp(level, m.BuildCostErrorAtZeroFunding, m.BuildCostErrorAtFullFunding)
}

// LayoutQualityBonus returns the layout-quality bonus coefficient at the
// current funding level (AC-5): higher funding ⇒ more likely the §52
// design-code compounding applies by default. Linearly interpolated between
// the data's zero-funding and full-funding anchors.
func (f *FiscalAPI) LayoutQualityBonus() float64 {
	if err := f.checkNotCopied("LayoutQualityBonus"); err != nil {
		return 0
	}
	f.mu.RLock()
	level := f.planningFunding
	f.mu.RUnlock()
	m := f.cfg.Municipality
	return linearInterp(level, m.LayoutBonusAtZeroFunding, m.LayoutBonusAtFullFunding)
}

// CorruptionRisk returns the corruption risk at the current funding level
// (AC-5): a threshold shape — zero at or above the data's corruption
// threshold, rising linearly to the data's corruption maximum at zero
// funding ("only rises meaningfully at the low end", §54).
func (f *FiscalAPI) CorruptionRisk() float64 {
	if err := f.checkNotCopied("CorruptionRisk"); err != nil {
		return 0
	}
	f.mu.RLock()
	level := f.planningFunding
	f.mu.RUnlock()
	m := f.cfg.Municipality
	if level >= m.CorruptionThreshold {
		return 0
	}
	return (m.CorruptionThreshold - level) / m.CorruptionThreshold * m.CorruptionMax
}

// linearInterp returns the linear interpolation between zero (at level 0)
// and full (at level 1): zero + (full-zero) × level. The anchors are data
// values already validated finite/non-negative/ordered by Config.Validate.
func linearInterp(level, zero, full float64) float64 {
	return zero + (full-zero)*level
}
