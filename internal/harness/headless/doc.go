// Package headless is harness.headless (MOD-015, "H-HEADLESS: engine
// without UI" — M0-ENG §2.3, docs/METROPOLIS-MASTER-v2.1.md line 848):
// the balance/Batch/CI workhorse, driving engine.core's real orchestrator
// — the SAME core.NewEngine + RunCommandLoop/HandleCommand path a live,
// UI-attached boot would use (engine.headless.md's AC-1/AC-2/AC-9,
// answering ASM-001) — with no UI, no TUI, and no wall-clock pacing
// attached.
//
// Module key: harness.headless (see code.json)
// Spec ref:   M0-ENG §2.3 (line 848); §16 Roadmap point 3 (M2 balance
//
//	harness, line 272)
//
// Acceptance: docs/planning/acceptance/harness.headless.md (MOD-015, the
//
//	CLI/library contract this package implements); docs/planning/
//	acceptance/engine.headless.md (MOD-015, engine.core's headless-run
//	contract this package is built against — read that file first, it
//	governs what this package is and is not allowed to do).
//
// # What this package proves
//
// Run (run.go) constructs a real *core.Engine and drives it over a real
// *protocol.InProcTransport, through RunCommandLoop/HandleCommand for
// every tick and every scenario command — never a headless-only bypass
// of AdvanceTicks (engine.headless.md AC-1/AC-2: "never a headless-only
// path that calls Engine.AdvanceTicks directly"). That is a deliberate,
// load-bearing design choice, not an implementation detail: it is what
// lets this package's headless contract and engine.core's future
// live-boot contract be the same code path by construction, rather than
// two implementations that happen to agree today (engine.headless.md
// AC-9, directly answering ASM-001 — the Sprint 1 exit gate did not
// previously prove engine.core participates in the live rendered path).
//
// cmd/metropolis's `-headless` flag (cmd/metropolis/headless.go) is the
// CLI entry point wrapping this package; this package itself owns no
// flag.FlagSet, so it is equally usable as a library by a future test
// harness (harness.synth, feat.detgate) without going through a
// subprocess.
//
// # Determinism (GR#21)
//
// Run never calls the wall clock except for -report's per-tick
// ElapsedMs field (report.go), which is operator progress reporting
// only — it is never fed back into simulation state or the -out
// snapshot bundle's content (AC-10). Running the same (seed, months,
// scenario) twice produces byte-identical -out snapshots (AC-7): every
// per-command CorrelationID this package mints is random, but
// CorrelationID never reaches engine.core's committed state (only
// tick/month/seed do — see engine.core's persist.go), so that
// randomness cannot leak into the hash the determinism gate cares about.
package headless
