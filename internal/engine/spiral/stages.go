package spiral

import "math"

// This file implements AC-2's "no scripted loss" contract: every spiral
// stage transition is a threshold or derivative on a real, externally-owned
// value (engine.attract's attractiveness score / net migration, the fiscal
// distress signal, the tax delta), never an internal counter, a wall clock,
// or a hardcoded stage-advance sequence. Each predicate below takes that
// real value as an explicit argument, and the stage is DERIVED from the
// current inputs by EvaluateStage — so reversing the driving value (e.g.
// attractiveness recovering) halts or reverses the progression, which a
// timer-driven implementation cannot do.

// StageInputs carries the real, externally-owned values one stage
// evaluation consumes (AC-2). Every field comes from a sibling module
// (engine.attract for Attractiveness/NetMigration, engine.finance for
// TaxDelta/InsolvencyRisk) or from the live world state (AbandonedCells,
// ShockRecorded) — supplied by the composition root or the scripted
// scenario, never computed inside this package.
type StageInputs struct {
	// Attractiveness is engine.attract's composite A() score this month.
	Attractiveness float64
	// PrevAttractiveness is the previous month's A() score — the input to
	// the attractiveness-decline derivative.
	PrevAttractiveness float64
	// NetMigration is engine.attract's signed net migration (positive =
	// immigration, negative = emigration).
	NetMigration float64
	// TaxDelta is the change in tax receipts month-over-month (the fiscal
	// "tax base" signal from engine.finance).
	TaxDelta int64
	// InsolvencyRisk is the fiscal distress signal (debt/service cuts) from
	// engine.finance's credit/insolvency bookkeeping.
	InsolvencyRisk bool
	// AbandonedCells is the number of currently-abandoned cells in the live
	// world.
	AbandonedCells int
	// ShockRecorded is whether a shock has been recorded this save.
	ShockRecorded bool
}

// EmigrationOnset reports whether the attractiveness score has fallen below
// the data-sourced emigration threshold (spiral.json stage block) — AC-2's
// first transition, a threshold on engine.attract's real score. A
// non-finite attractiveness never triggers (a NaN/Inf would make the
// threshold comparison meaningless, GR#16).
func (d *DecayAPI) EmigrationOnset(attractiveness float64) bool {
	if err := d.checkNotCopied("EmigrationOnset"); err != nil {
		return false
	}
	if math.IsNaN(attractiveness) || math.IsInf(attractiveness, 0) {
		return false
	}
	return attractiveness < d.cfg.Stage.EmigrationAttractivenessThreshold
}

// TaxBaseDecline reports whether tax receipts declined month-over-month
// (a negative tax delta) — the §12 "tax base ↓" link, a sign-derivative on
// engine.finance's real tax-delta signal.
func (d *DecayAPI) TaxBaseDecline(taxDelta int64) bool {
	if err := d.checkNotCopied("TaxBaseDecline"); err != nil {
		return false
	}
	return taxDelta < 0
}

// ServiceCutsDebt reports whether the fiscal distress signal (forced
// service cuts or rising debt) is active — consumed from engine.finance,
// never re-derived here.
func (d *DecayAPI) ServiceCutsDebt(insolvencyRisk bool) bool {
	if err := d.checkNotCopied("ServiceCutsDebt"); err != nil {
		return false
	}
	return insolvencyRisk
}

// AttractivenessDecline reports whether the attractiveness score fell since
// the previous month — a derivative on the real score, the §12
// "attractiveness ↓" link.
func (d *DecayAPI) AttractivenessDecline(attractiveness, prevAttractiveness float64) bool {
	if err := d.checkNotCopied("AttractivenessDecline"); err != nil {
		return false
	}
	if math.IsNaN(attractiveness) || math.IsInf(attractiveness, 0) ||
		math.IsNaN(prevAttractiveness) || math.IsInf(prevAttractiveness, 0) {
		return false
	}
	return attractiveness < prevAttractiveness
}

// AbandonmentOnset reports whether net migration is negative (emigration is
// occurring) — the §12 "more emigration → abandoned buildings" link, a sign
// on engine.attract's real net-migration figure.
func (d *DecayAPI) AbandonmentOnset(netMigration float64) bool {
	if err := d.checkNotCopied("AbandonmentOnset"); err != nil {
		return false
	}
	if math.IsNaN(netMigration) || math.IsInf(netMigration, 0) {
		return false
	}
	return netMigration < 0
}

// BlightSpreadOnset reports whether abandoned cells exist from which blight
// can spread — the §12 "abandoned buildings → district blight spreads
// cell-by-cell" link.
func (d *DecayAPI) BlightSpreadOnset(abandonedCells int) bool {
	if err := d.checkNotCopied("BlightSpreadOnset"); err != nil {
		return false
	}
	return abandonedCells > 0
}

// EvaluateStage derives the current spiral stage from live inputs (AC-2).
// The stage is the DEEPEST predicate that currently holds, checked in
// reverse chain order — so when the driving value recovers and a predicate
// stops holding, the stage retreats to the shallower stage, making the
// progression reversible rather than a ratcheting counter. There is no
// `stage++` and no stored "previous stage" that advances monotonically:
// the stage is a pure function of the inputs.
func (d *DecayAPI) EvaluateStage(in StageInputs) Stage {
	if err := d.checkNotCopied("EvaluateStage"); err != nil {
		return StageStable
	}
	if d.BlightSpreadOnset(in.AbandonedCells) {
		return StageBlightSpread
	}
	if d.AbandonmentOnset(in.NetMigration) {
		return StageAbandonmentOnset
	}
	if d.AttractivenessDecline(in.Attractiveness, in.PrevAttractiveness) {
		return StageAttractivenessDecline
	}
	if d.ServiceCutsDebt(in.InsolvencyRisk) {
		return StageServiceCutsDebt
	}
	if d.TaxBaseDecline(in.TaxDelta) {
		return StageTaxBaseDecline
	}
	if d.EmigrationOnset(in.Attractiveness) {
		return StageEmigrationOnset
	}
	if in.ShockRecorded {
		return StageShock
	}
	return StageStable
}
