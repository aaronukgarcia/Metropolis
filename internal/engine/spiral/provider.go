package spiral

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
)

// This file implements the population curve provider engine.spiral registers
// with engine.projections under the reserved key
// projections.CurveKeyGhostCityPopulation ("engine.spiral.population"), so
// MarginToGhostCity can evaluate AC-7's dual threshold and record warnings
// in the WarningLedger (AC-15). The provider implements both CurveProvider
// (Value) and GhostCityPeakProvider (HistoricPeak) — the extra shape
// MarginToGhostCity requires to read the historic-peak half of the
// threshold.

// populationCurveProvider adapts a *DecayAPI's population history to the
// projections.CurveProvider + projections.GhostCityPeakProvider contract.
// It reads the API's append-only population history under the API's lock,
// so it is safe for projections to call from any goroutine.
type populationCurveProvider struct {
	d *DecayAPI
}

// Value returns the recorded population at monthIndex. A month before the
// first recorded month reads as the first recorded population (the world
// genesis population); a month after the last recorded month reads as the
// last recorded population (the sim has not advanced past it).
func (p *populationCurveProvider) Value(monthIndex int64) (float64, error) {
	if err := p.d.checkNotCopied("populationCurveProvider.Value"); err != nil {
		return 0, err
	}
	p.d.mu.RLock()
	defer p.d.mu.RUnlock()
	if len(p.d.popHistory) == 0 {
		return 0, nil
	}
	if monthIndex < 0 {
		return float64(p.d.popHistory[0]), nil
	}
	if monthIndex >= int64(len(p.d.popHistory)) {
		return float64(p.d.popHistory[len(p.d.popHistory)-1]), nil
	}
	return float64(p.d.popHistory[monthIndex]), nil
}

// HistoricPeak returns the highest population ever recorded — the AC-7
// "historic peak" input to MarginToGhostCity's dual-threshold check.
func (p *populationCurveProvider) HistoricPeak() float64 {
	if err := p.d.checkNotCopied("populationCurveProvider.HistoricPeak"); err != nil {
		return 0
	}
	p.d.mu.RLock()
	defer p.d.mu.RUnlock()
	return float64(p.d.historicPeak)
}

// projectionsSnapshot returns the wired projections API under a brief read
// lock, or nil if it is not wired. It exists so every caller reads the
// dependency through the same lock without holding d.mu across a call into
// projections (which would risk a lock-order inversion against the provider
// reading back into d).
func (d *DecayAPI) projectionsSnapshot() *projections.ProjectionsAPI {
	if err := d.checkNotCopied("projectionsSnapshot"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.projections
}
