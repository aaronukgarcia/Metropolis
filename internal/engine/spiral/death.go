package spiral

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file implements the two death conditions §12 names — insolvency
// (AC-6, consumed from engine.finance) and the ghost-city population
// collapse (AC-7) — and the FEAT-068 warning gate on the ghost-city trigger
// (AC-15/AC-16/AC-17). The spiral module does NOT reimplement either
// underlying system: insolvency is engine.finance's own signal, and the
// ghost-city margin/warning is engine.projections' own computation; this
// package only consumes them and applies its own documented thresholds.

// ghostCityPeakFloor is §12's "has ever exceeded 50,000" floor (AC-7) and
// ghostCityPopulationFraction is §12's "below 10% of historic peak" fraction.
// Both are STRUCTURAL spec constants quoted directly from §12, not tunable
// balance numbers — engine.projections' deathwarnings.go quotes the same two
// figures (ghostCityPeakFloor/ghostCityPopulationFraction) for the
// MarginToGhostCity side of the same gate.
const (
	ghostCityPeakFloor          = 50_000
	ghostCityPopulationFraction = 0.10
)

// EvaluateInsolvency consumes engine.finance's real game-over signal (AC-6):
// it calls FinanceAPI.IsInsolvent() directly — this module does not
// reimplement insolvency math. It returns DeathInsolvency when the signal
// has fired, DeathNone otherwise.
func (d *DecayAPI) EvaluateInsolvency(f *finance.FinanceAPI) DeathVerdict {
	if err := d.checkNotCopied("EvaluateInsolvency"); err != nil {
		return DeathNone
	}
	if f == nil {
		return DeathNone
	}
	if f.IsInsolvent() {
		return DeathInsolvency
	}
	return DeathNone
}

// GhostCityConditionMet evaluates §12's dual-threshold ghost-city condition
// (AC-7) in isolation, taking BOTH the current population and the historic
// peak as explicit inputs (AC-7's check): the condition is met only when the
// population is below 10% of the historic peak AND that peak has exceeded
// 50,000 at some point in the save. It does not consult the warning gate —
// that is [GhostCityTrigger]'s job (AC-15) — so a test can assert the
// threshold itself independently of the FEAT-068 gating.
func (d *DecayAPI) GhostCityConditionMet(currentPop, historicPeak int64) bool {
	if err := d.checkNotCopied("GhostCityConditionMet"); err != nil {
		return false
	}
	if historicPeak <= ghostCityPeakFloor {
		return false
	}
	threshold := float64(historicPeak) * ghostCityPopulationFraction
	return float64(currentPop) < threshold
}

// GhostCityTrigger is the gated game-over transition (AC-7 + AC-15): it
// fires only when [GhostCityConditionMet] holds AND engine.projections'
// WarningLedger already carries a qualifying MarginToGhostCity entry
// recorded at least MinWarningLeadMonths before this month — a structural
// gate, not a logged correlation.
//
// Returns:
//   - (false, nil): the dual threshold is not met (no trigger).
//   - (true, nil): the threshold is met AND a qualifying warning is on
//     record with sufficient lead — the death condition fires.
//   - (false, ErrGhostCityNoWarning): the threshold is met but no
//     qualifying warning is on record — the trigger cannot fire (AC-15(b),
//     AC-17). A typed, registry-sourced rejection, never a silent game-over.
//   - (false, ErrDependencyMissing): projections is not wired.
func (d *DecayAPI) GhostCityTrigger(currentPop, historicPeak, month int64) (bool, error) {
	if err := d.checkNotCopied("GhostCityTrigger"); err != nil {
		return false, err
	}
	if !d.GhostCityConditionMet(currentPop, historicPeak) {
		return false, nil
	}

	p := d.projectionsSnapshot()
	if p == nil {
		return false, errs.New(ErrDependencyMissing, d.correlationID, map[string]any{
			"dependency": "projections",
			"operation":  "GhostCityTrigger",
		})
	}
	ledger, err := p.WarningLedger()
	if err != nil {
		return false, err
	}
	lead := d.cfg.GhostCity.MinWarningLeadMonths
	for _, e := range ledger.Query(projections.MetricMarginToGhostCity, 0, month) {
		if float64(e.Month)+lead <= float64(month) {
			return true, nil
		}
	}
	return false, errs.New(ErrGhostCityNoWarning, d.correlationID, map[string]any{
		"month":        month,
		"population":   currentPop,
		"historicPeak": historicPeak,
	})
}

// ActiveGhostCityWarning reports whether the city is CURRENTLY in a
// qualifying ghost-city warning state (AC-16): a real, worsening population
// trend toward the 10%-of-peak threshold, as computed by engine.projections'
// MarginToGhostCity. It is re-derived every call from the live margin — a
// recovered city (flat or rising population) returns false, never a stale
// latched warning. Returns (false, nil) when projections is not wired.
func (d *DecayAPI) ActiveGhostCityWarning(month int64) bool {
	if err := d.checkNotCopied("ActiveGhostCityWarning"); err != nil {
		return false
	}
	p := d.projectionsSnapshot()
	if p == nil {
		return false
	}
	res, err := p.MarginToGhostCity(month)
	if err != nil {
		return false
	}
	// ConfidenceComputed means extrapolateToThreshold found a real worsening
	// trend (slope toward the threshold); a flat or recovering trend returns
	// noImminentRiskMonths/Extrapolated and reads as "no active warning".
	if res.Confidence != projections.ConfidenceComputed {
		return false
	}
	return res.MonthsRemaining <= d.cfg.GhostCity.WarningThresholdMonths
}

// EvaluateDeath returns the active death verdict for the month, consuming
// the wired finance dependency (AC-6) and the ghost-city inputs + gate
// (AC-7/AC-15). It returns the verdict and any gate-rejection error — a
// gate rejection is a soft failure (no game-over, surfaced to the caller),
// distinct from a genuine death verdict.
func (d *DecayAPI) EvaluateDeath(f *finance.FinanceAPI, currentPop, historicPeak, month int64) (DeathVerdict, error) {
	if err := d.checkNotCopied("EvaluateDeath"); err != nil {
		return DeathNone, err
	}
	if v := d.EvaluateInsolvency(f); v != DeathNone {
		return v, nil
	}
	triggered, err := d.GhostCityTrigger(currentPop, historicPeak, month)
	if err != nil {
		return DeathNone, err
	}
	if triggered {
		return DeathGhostCity, nil
	}
	return DeathNone, nil
}
