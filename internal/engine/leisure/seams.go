package leisure

import (
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// This file defines leisure's local view of code.json's registered
// engine.leisure → engine.wellbeing outbound edge (the GR#20 "consume via
// registered interfaces" shape). Leisure needs exactly ONE value from
// wellbeing — the LeisureFit driver push (AC-10) — so it consumes it through
// a narrow seam rather than reaching for wellbeing's full attribution surface.
// The real *wellbeing.WellbeingAPI is adapted by [WellbeingLeisureFitAdapter];
// tests inject a fake.

// WellbeingLeisureFitAdapter adapts *wellbeing.WellbeingAPI to leisure's
// [WellbeingAPI] seam (AC-10). engine.wellbeing's pure Attribute engine
// computes the LeisureFit driver delta from DriverInputs.LeisureFit, while
// its gather path (AttributeCitizen) receives LeisureFit through
// ContextInputs.LeisureFit pushed by the caller. This adapter records each
// pushed per-citizen fit so the composition root can fold it into
// ContextInputs.LeisureFit, and it uses wellbeing's own Attribute to compute
// the real LeisureFit driver delta — the single computation, never a locally
// duplicated leisure-fit driver model (GR#3/GR#20).
type WellbeingLeisureFitAdapter struct {
	// Wellbeing is the real engine.wellbeing API. A nil value degrades the
	// push to a no-op (AC-14-style), matching [WellbeingFamilyStress].
	Wellbeing *wellbeing.WellbeingAPI

	// Month is the sim month the composition root wires into the adapter.
	// Attribute is a function of (worldSeed, citizenID, month) for its
	// baseline jitter; the LeisureFit driver delta itself is month-independent.
	Month int64

	mu     sync.RWMutex
	fits   map[uint64]float64 // citizenID → pushed LeisureFit (ContextInputs.LeisureFit)
	deltas map[uint64]float64 // citizenID → real LeisureFit driver delta (wellbeing.Attribute)
}

// SetLeisureFit implements [WellbeingAPI]. It pushes one citizen's leisure-fit
// through the real engine.wellbeing: wellbeing's own Attribute computes the
// LeisureFit driver delta from DriverInputs.LeisureFit (the single
// computation — an out-of-domain fit is rejected, never silently clamped),
// and the fit is recorded for the composition root to fold into
// ContextInputs.LeisureFit.
func (a *WellbeingLeisureFitAdapter) SetLeisureFit(citizenID uint64, fit float64) error {
	if a.Wellbeing == nil {
		return nil // no source wired: degrade to a no-op push (AC-14-style)
	}
	attr, err := a.Wellbeing.Attribute(citizenID, a.Month, wellbeing.DriverInputs{LeisureFit: fit})
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fits == nil {
		a.fits = make(map[uint64]float64)
		a.deltas = make(map[uint64]float64)
	}
	a.fits[citizenID] = fit
	a.deltas[citizenID] = attr.Mental.LeisureFit.Delta
	return nil
}

// LeisureFitContext returns the last pushed per-citizen leisure-fit as a
// ContextInputs whose LeisureFit field is set (everything else zero — the
// composition root fills in the remaining context for its AttributeCitizen
// gather call), plus the real LeisureFit driver delta wellbeing's own
// Attribute computed from the fit. ok is false when no fit has been pushed
// for the citizen.
func (a *WellbeingLeisureFitAdapter) LeisureFitContext(citizenID uint64) (wellbeing.ContextInputs, float64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	fit, ok := a.fits[citizenID]
	if !ok {
		return wellbeing.ContextInputs{}, 0, false
	}
	return wellbeing.ContextInputs{LeisureFit: fit}, a.deltas[citizenID], true
}

// Compile-time proof that the real engine.wellbeing API is bridged to
// leisure's [WellbeingAPI] seam (GR#20: consume the real registered
// interface, never a reimplementation of the leisure-fit driver model — AC-10).
var _ WellbeingAPI = (*WellbeingLeisureFitAdapter)(nil)
