// Package spiral is the failure-states module (MOD-030): the Detroit spiral,
// abandonment decay, cell-by-cell blight spread, and the ghost-city ending.
//
// Module key: engine.spiral (see code.json)
// Spec refs:  §12 "Failure — the Detroit Spiral" ("No scripted loss. The
// spiral: shock → emigration → tax base ↓ → forced service cuts or debt →
// attractiveness ↓ → more emigration → abandoned buildings (decay state:
// drag neighbouring land value, small ongoing hazard/fire/crime pressure,
// cost money to demolish) → district blight spreads cell-by-cell. Recovery
// is possible but expensive. Death conditions: insolvency (§7), or
// population falls below 10% of historic peak after the city has ever
// exceeded 50,000 — the ghost-city ending, with an epilogue screen generated
// from the city's history log."); §II.7 Government (budgets/loans/credit
// rating as the fiscal side of the same mechanics); §11 Attractiveness &
// Migration (the reputation-momentum "Detroit trap" asymmetry); §13 F9
// Ticker & History (the searchable history log that is the epilogue's data
// source).
//
// # No scripted loss (AC-2, AC-9)
//
// The spiral's causal chain is NOT a scripted cutscene sequence. Every stage
// transition in [DecayAPI.EvaluateStage] is a threshold or derivative on a
// real, externally-owned value — engine.attract's attractiveness score /
// net migration, engine.finance's tax delta / fiscal-distress signal —
// supplied as explicit arguments, never an internal counter, a wall clock,
// or a hardcoded stage-advance switch. Reversing the driving value (e.g.
// attractiveness recovering) halts or reverses the progression. AC-9 makes
// this reproducible: the canonical scripted shock scenario (ASM-240) produces
// a byte-identical ordered event sequence plus final state hash across
// repeated runs and across worker/shard counts (GR#21).
//
// # Death conditions
//
// Two death conditions fire (AC-6/AC-7), and this package consumes them, it
// does not reimplement them:
//
//   - Insolvency (§7): [DecayAPI.EvaluateInsolvency] consumes
//     engine.finance's real game-over signal (FinanceAPI.IsInsolvent) — the
//     engine.spiral→engine.finance edge landed in c36778b and is live, not
//     pending.
//   - Ghost city (§12): [DecayAPI.GhostCityTrigger] fires only when BOTH
//     named conditions hold — the current population is below 10% of the
//     all-time historic peak, AND that peak exceeded 50,000 at some point in
//     the save's history.
//
// # Ghost-city warning gate (FEAT-068, AC-15/AC-16/AC-17)
//
// The ghost-city trigger is gated on engine.projections' WarningLedger per
// AC-15: before the game-over signal can fire, the ledger must already carry
// a qualifying MarginToGhostCity entry recorded at least MinWarningLeadMonths
// (ghost-city's own data-sourced placeholder, spiral.json's ghostCity block —
// mirrored from engine.projections.md AC-20's deathwarnings.json) before the
// trigger month. The engine.spiral↔engine.projections edge is live (BUG-118),
// not pending. A city that recovers before the threshold is reached is
// reflected in [DecayAPI.ActiveGhostCityWarning] returning false — the
// warning tracks the real trajectory, including recovery (AC-16). The gate's
// rejection (threshold reached, no qualifying warning) is a typed,
// registry-sourced error (ErrGhostCityNoWarning, AC-17), never a silent
// game-over.
//
// # Assumptions
//
// This package builds on three logged assumptions: ASM-240 (the canonical
// reproducibility fixture is a fixed major-employer-closure shock against a
// fixed synthetic starting city — confirmed by Aaron), ASM-241 (blight-spread
// rate and decay-severity thresholds are data-file-sourced in spiral.json,
// left untuned pending the M2 balance pass), and ASM-242 (historic-peak
// population is read from the population signal supplied via engine.attract's
// migration outputs, not a direct engine.citizens dependency).
package spiral
