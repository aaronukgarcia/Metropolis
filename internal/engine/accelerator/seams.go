package accelerator

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/education"
	"github.com/aaronukgarcia/Metropolis/internal/engine/wellbeing"
)

// This file defines accelerator's local view of each registered outbound
// module's inbound contract (code.json engine.accelerator.outbound.calls) —
// the GR#20 "consume via registered interfaces" shape. Built modules are
// consumed through a local seam with a compile-time assertion proving the
// real type satisfies it; unbuilt modules (engine.fdi, feat.facilitypermits,
// feat.decommission) are consumed purely through the seam, wired by the
// composition root and faked in tests.

// ResearchSource is the seam over engine.education's research-output surface
// — the single figure the shared expert gate measures. The real
// *education.EducationAPI satisfies it structurally (asserted below). The
// method is named to match the education module's own surface; this package
// only reads the output, never computes it (no local education accounting).
type ResearchSource interface {
	ResearchPoints() int64
}

// ExpertGate is the local contract shape for FEAT-055's shared §8 expert
// gate: given the current research output (read through [ResearchSource]),
// it returns the verdict. The threshold/check logic is FEAT-055's; this
// package consumes only the verdict. [ThresholdGate] is the stub-forever
// standing-in until feat.megafacilities lands its real gate surface.
type ExpertGate interface {
	Gate(researchOutput int64) (bool, error)
}

// WellbeingSource is the seam over engine.wellbeing's registered surface —
// the health driver the accelerator's research spillover posts into (AC-8).
// The spillover routes to engine.wellbeing, never a phantom engine.health
// module. [WellbeingSpilloverAdapter] bridges the real *wellbeing.WellbeingAPI
// to this seam.
type WellbeingSource interface {
	PostHealthSpillover(magnitude float64) error
}

// FdiSource is the seam over engine.fdi's registered prospect surface — the
// anchor-prospect draw the accelerator posts when built (AC-9). engine.fdi
// (MOD-059) is not yet built, so this seam is consumed contract-first and
// faked in tests.
//
// RemoveAnchorProspect is the compensating inverse of AddAnchorProspect:
// Build calls it to roll the draw back when a later build-time side effect
// (the day-one decommission liability) fails, so a rejected build leaves no
// phantom FDI anchor (AC-13). It is the same compensating-removal shape the
// repo already uses for partial-win rollback (engine.fdi's FirmsEdge
// "RegisterFirm + Fail" pattern).
type FdiSource interface {
	AddAnchorProspect(magnitude int64) error
	RemoveAnchorProspect(magnitude int64) error
}

// PermitSource is the seam over feat.facilitypermits (FEAT-053) — the
// inherited §7 land-allocation permit the build path delegates to (AC-11).
// The accelerator owns no permit state.
type PermitSource interface {
	HasPermit(facilityKey string) (bool, error)
}

// DecommissionSource is the seam over feat.decommission (FEAT-054) — the
// inherited §7 "put back to nature" liability accrued at build time (AC-12).
// The accelerator owns no liability ledger.
type DecommissionSource interface {
	AccrueLiability(facilityKey string) error
}

// Compile-time proof that the real engine.education API satisfies this
// package's ResearchSource seam (GR#20: consume the real registered
// interface, never a reimplementation of the education-output model — AC-2).
var _ ResearchSource = (*education.EducationAPI)(nil)

// ThresholdGate is the accelerator's standing-in for FEAT-055's shared
// expert gate (GR#20 stub-forever): it compares the research output to the
// data-sourced threshold. When feat.megafacilities lands its real gate
// surface, the composition root wires that instead; the verdict-consumption
// contract ([ExpertGate]) does not change. The threshold arrives from
// data/accelerator.json (via [Config.ExpertGateThreshold]) — never a Go
// literal.
type ThresholdGate struct {
	Threshold int64
}

// Gate implements ExpertGate. A research output at or above the threshold is
// accepted; anything below is rejected — the numeric-threshold flip that
// makes "money alone cannot buy it" mechanical (AC-3).
func (g ThresholdGate) Gate(researchOutput int64) (bool, error) {
	return researchOutput >= g.Threshold, nil
}

// WellbeingSpilloverAdapter bridges the real engine.wellbeing WellbeingAPI
// to the [WellbeingSource] seam. engine.wellbeing owns the health model and
// does not yet expose an explicit research-spillover injection point, so the
// adapter records the spillover magnitude for the composition root to apply
// once that point lands. It exists so the accelerator's tick path can call
// through the seam today (GR#20 stub-forever) without depending on a method
// engine.wellbeing does not have yet.
type WellbeingSpilloverAdapter struct {
	API *wellbeing.WellbeingAPI

	// Spillover is the most recent magnitude posted, for the composition root
	// to surface into wellbeing's attribution engine.
	Spillover float64
}

// PostHealthSpillover implements WellbeingSource by recording the magnitude.
func (w *WellbeingSpilloverAdapter) PostHealthSpillover(magnitude float64) error {
	w.Spillover = magnitude
	return nil
}
