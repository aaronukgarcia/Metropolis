// Package balance is balance.harness (MOD-036, FEAT-192 Tier D — "Balance
// harness: parameter sweeps, pacing tuning to achievable-but-hard", M2): the
// mechanical home of the balance-number regime. It sweeps player-felt
// parameters — the pacing knob secondsPerMonthAt1x, growth-curve coefficient
// sets, milestone-spacing variants — across a seeded JSON scenario
// definition, fans each sweep point out through harness.synth (a synthetic
// world) and harness.headless (a full deterministic engine run over a fixed
// tick window), and emits comparable per-seed results a BA/Aaron can approve
// row-by-row — landing pacing at "achievable-but-hard" instead of by
// hand-guess (§16-M2, §15, §3).
//
// Module key: balance.harness (code.json; GUID 6dcbc6b4-82fd-41a8-8f3c-817b5fb6a2b0)
// Spec ref:   §16-M2 (line 272); §15 (Azure Batch headless balance-tuning);
//
//	§3 (Time / secondsPerMonthAt1x pacing knob)
//
// Acceptance: docs/planning/acceptance/balance.harness.md (MOD-036)
// ICD:        docs/planning/icd/balance.harness.md
// Outbound:   harness.headless (94fcac3b-a76c-40a8-8bb6-6add9ebc9496),
//
//	harness.synth   (2cabd726-8b86-4254-a07d-ab202f6a6a75) — both
//	already registered in code.json; no new edges.
//
// # The 80–150 real-hour target band
//
// §3's own sanity check ("1 game year = 24 min at 4×; 250 game years ≈ 100
// real hours") and MOD-036's BOW description ("0→100M achievable-but-hard in
// 80-150 hours") give a real-hours *pacing* band. This package computes a
// real-hours-to-milestone figure per completed cell (simulated months elapsed
// to cross the milestone × the cell's secondsPerMonthAt1x ÷ 3600) and, via
// [Proposal], selects the configs whose figure lands in the scenario's target
// band. "Achievable-but-hard" beyond that band — the difference between
// "slow" and "difficult" (no Detroit-spiral risk, no resource-margin tension,
// no failure mode a mediocre player hits) — is a difficulty judgement that is
// squarely Aaron's call and is NOT decided here; the metric is a pacing
// figure only (see the acceptance file's "Escalations" section).
//
// # Determinism boundary (GR#21; ASM-216 / FEAT-041)
//
// Results merge in ascending (sweep-point, seed, attempt) order, never
// completion order, and this package's only numeric metric is pure per-cell
// arithmetic over an integer simulated-months count and a scenario float — so
// the same (scenario, seed set) produces byte-identical result tables at a
// fixed worker count, and in fact across worker counts too for THIS package's
// own figures. That is NOT a claim about the engine's cross-shard float
// summation (traffic/flow-style accounting): whether that is bit-identical
// across worker counts is an open P0 (ASM-216), deliberately deferred to
// FEAT-041 (which blocks MOD-023), and is neither assumed nor decided here.
// WorkerCount is recorded as a first-class field on every result so a reader
// can always tell which claim a given table supports.
//
// # BUG-034 honesty (no perf baseline exists yet)
//
// harness.synth's real-1M-scale CI baseline has not yet been recorded on a CI
// runner (BUG-034, open P0). Nothing in this package's tests, docs, or CLI
// output states or implies "no regression against the CI baseline" as an
// achieved, verified condition. Where a synth-derived comparison is surfaced,
// the missing-baseline state is reported explicitly — synth.CompareToBaseline's
// "no prior baseline to compare" outcome — never treated as a pass (see
// TestNoBaselineComparisonReportsMissingBaseline).
//
// This is a tools/ layer package: it does not follow the internal/ doc.go
// GUID-header convention (GR#6's GUID headers apply to internal/ modules);
// the module key + spec refs above are this file's equivalent.
package balance
