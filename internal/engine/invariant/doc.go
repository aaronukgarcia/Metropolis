// Package invariant is the conservation invariant checker (MOD-019): a
// framework that asserts, every tick, that the simulation's conserved
// stocks (people, money, goods, vehicles) balance against their tracked
// flows — a hard assert in dev builds, a registry-sourced logged error
// in release builds. Its whole job is to turn §14's prose invariant
// ("conservation: people, money, goods must balance — hard assert in
// dev") and §19.3's ("nothing ever despawns... an invariant-checker
// assert") into code that makes an imbalance impossible to miss, rather
// than a comment a later change can silently violate — see the dev-team
// process's "weakness pattern #1" note, which names this item's own
// premise directly.
//
// Module key: engine.invariant (see code.json)
// Spec ref:   §14 (conservation, hard assert in dev); §19.3/§19 intro
// (vehicle conservation, despawn-masking gridlock)
//
// # What this package does NOT do
//
// It does not compute production, consumption, transactions, or vehicle
// spawning — those are the owning modules' (engine.citizens,
// engine.finance, engine.logistics, engine.traffic; Sprint 3+) jobs.
// This package only checks that whatever numbers those modules report
// (via a caller-supplied Snapshot) balance. At Sprint 2 build time,
// before those modules are real, the four seeded invariants (people.go,
// money.go, goods.go, vehicle.go) are proven against synthetic
// fixtures — the framework's correctness, not full-city conservation
// (see the acceptance doc's "For Bill" escalation).
//
// # The invariant list is not hardcoded (GR#15)
//
// The four v1 invariants are seeded explicitly (AC-2) because the spec
// names exactly these four conserved stocks (§14). What this package
// does NOT do is duplicate engine.core's phase set: it imports
// core.PhaseKind directly (wire.go) rather than redeclaring a parallel
// list of phase names, so there is nothing here that can drift out of
// sync with engine.core's real, documented phase pipeline
// (internal/engine/core/phase.go).
//
// # Extensibility (US-5)
//
// A future module (engine.market, engine.finance, engine.traffic, ...)
// extends conservation coverage in two independent ways, neither of
// which requires touching this package's existing invariants:
//
//  1. Register a new Invariant (implementing Name/Check) against a
//     shared *Registry via Registry.Register — a config/registration
//     change, per US-5, not a rewrite.
//  2. Populate that invariant's StockReading in the Snapshot the
//     wiring's SnapshotProvider builds each tick. Until a module does
//     this, its stock's StockReading.Registered stays false and
//     RunSuite reports it as skipped (AC-12), never as a false-flagged
//     zero.
//
// # Which phase this checker runs against (ASM-080)
//
// §14 says "hard assert in dev... every tick" — read literally, that
// means every DAILY tick, not just the monthly barrier. WireDaily
// (wire.go) therefore registers against core.PhaseDailyTick, the one
// phase engine.core runs on every AdvanceTicks-driven day (phase.go).
// Wire (the more general form) accepts any core.PhaseKind, so a future
// caller MAY additionally register invariant-checking hooks against a
// monthly phase (e.g. finance, consumption-shortfall) for a
// stock-specific check that only makes sense at the monthly barrier —
// but the default, and what boot wiring should use unless it has a
// specific reason not to, is the daily phase. This was an open question
// (ASM-080, logged against this item before this package existed) that
// §14/§19.3 leave spec-silent; resolved here and recorded against that
// same BOW item rather than left implicit.
//
// # Performance (hot path)
//
// The wired PhaseHook's RunShard is called once per shard per tick
// (256 shards, engine.core's fixed shard count) but does real work only
// for shard 0 — every other shard returns (nil, nil) immediately. The
// suite itself (RunSuite) is O(number of registered invariants), each a
// single map lookup plus a handful of int64 subtractions — no
// allocation on the balanced path beyond the SuiteResult/Outcome slices
// RunSuite builds (bounded by len(registered invariants), never by
// world size). See hook.go's doc comment for the measured shape.
//
// # Determinism (AC-13, AC-14, AC-15)
//
// This package never reads the wall clock (grep -rn "time\.Now\|time\.
// Since" internal/engine/invariant/*.go, excluding _test.go, returns no
// matches) — every check is a pure function of the tick-indexed
// Snapshot the caller supplies. RunSuite iterates the Registry's
// invariants in registration order (a slice, never a map), so its
// output order is fixed regardless of goroutine scheduling or
// POOL-SIM's worker count.
package invariant
