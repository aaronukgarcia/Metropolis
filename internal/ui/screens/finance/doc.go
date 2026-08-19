// Package finance is F2, the Finance screen (FEAT-014).
//
// Spec refs:
//   - §13-F2: Finance screen (GDD line 249)
//   - §54: The Fiscal Circuit (Sankey, public payroll clawback, GDD lines 680-688)
//   - §39: Taxation (tax-instrument sliders, GDD lines 558-565)
//
// View subscription:
//   - "f2.finance": the view delivering P&L aggregates, balance-sheet lists, active loans,
//     tax-instrument settings/elasticity curves, gross-vs-net public payroll, and Fiscal Circuit
//     Sankey flow bands.
//
// Gating Notes & Architecture Seams (BUG-058 / ASM-1482):
//   - FIN-8 command rejection surfacing (ApplyResult) is fully designed, implemented, and verified
//     in unit tests. However, actual routing of command results to screen sub-receivers is currently
//     unwired in the wider frame pending the core routing-seam implementation (ASM-1482) — do not
//     invent custom/ad-hoc wiring.
package finance
