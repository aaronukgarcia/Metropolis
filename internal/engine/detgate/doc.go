// Package detgate is FEAT-004, the CI determinism gate: same seed, same
// command log, run twice, sha256(worldSnapshot) must match; then again
// across POOL-SIM worker counts. It is A8's mechanical enforcement arm
// (docs/golden-rules-detail.md Rule #21 — "A Red Determinism Gate Stops
// the Line") and the master doc's crown-rule check (§1.2 point 5:
// "CI runs the determinism gate on every merge: same seed, 120 months,
// twice, sha256(worldSnapshot) must match; then again with POOL-SIM=1 vs
// =14. A mismatch fails the build. This test is written FIRST, in M1 week
// one, against the walking-skeleton world.").
//
// Module key: feat.detgate (see code.json; GUID fac130bf-4875-417b-88b0-67b83208aaae)
// Spec ref:   §1.2.5; A8; M0-ENG §6.4
//
// # This gate must exist and pass BEFORE any real simulation logic
//
// Per M0-ENG §6.4 (docs/METROPOLIS-MASTER-v2.1.md "6. Working agreement
// for Claude Code", point 4 — "walking skeleton first ... determinism
// gate ... before any real model") and Rule #21's own text, this package
// was built and green first against the stub engine (zero registered
// PhaseHooks — the walking-skeleton property) and was later repointed at
// the composed engine once the composition root landed (BUG-375): a gate
// that hashes zero hooks proves nothing about the hooks production runs.
// A future contributor must not
// treat this package as optional post-hoc tooling: it is the reference
// implementation every later determinism-relevant module's own
// shard-count-invariance test (M0-ENG §6's Definition of Done) is
// expected to copy — fixed seed, N identically-labelled runs, hash
// compare, worker-count variant (see RunSpec/RunGate below).
//
// # What RunGate exercises
//
// RunGate boots a real engine.core.Engine (MOD-012) wired through
// compose.Wire — the composition root's full baseline-one hook set, the
// same registration order production runs (BUG-375: hashing a bare-core,
// zero-hook engine proved nothing about the hooks that actually run) —
// and drives it exclusively through the real
// protocol.Command surface (protocol.InProcTransport +
// Engine.RunCommandLoop), never by calling Engine.AdvanceTicks directly.
// This matters: the gate must prove the whole seam (envelope validation,
// command dispatch, HandleCommand) is deterministic, not just the
// internal tick loop underneath it. Once every run's commands have all
// been accepted, RunGate calls Engine.Snapshot directly (T-PERSIST,
// persist.go) — Snapshot is engine.core's own hook, not a protocol
// command; the v1 command vocabulary (internal/protocol/commands.go) has
// no SnapshotPayload/Kind, by design (persist is a save-system concern,
// not something the UI drives mid-tick).
//
// # Hashing (AC-5, BUG-375 r3)
//
// serialize.NDJSONSerializer.WriteShard already guarantees byte-identical
// output for the same sequence of records (see its doc comment) — gzip
// header fields that would otherwise vary (ModTime, OS, Name, Comment)
// are pinned, and nothing in that path touches the wall clock. RunGate
// hashes three components in order as a single sha256.Sum:
//
//  1. The canonical JSON encoding of the returned serialize.Header (fixed
//     struct field order, no maps — see serialize/header.go). Header
//     carries no wall-clock field (verified: FormatVersion, WorldSeed,
//     CreatedAtTick, GameMonth, AppVersion, DebugTouched, ShardIndex —
//     CreatedAtTick is a simulation tick, not time.Now(), per
//     serialize.Header's own doc comment) so including it cannot introduce
//     nondeterminism.
//  2. The shard bytes Snapshot wrote (engine.core's "meta" shard containing
//     tick/month/seed).
//  3. The Composition.StateDigest — the BROAD composed-state fingerprint
//     (BUG-375 r3). It observes EVERY composed module observable a hook can
//     mutate: the citizen-store PopulationHash (the original r2 probe), the
//     finance ledger's per-account balances plus its money-stock/tax/wages/
//     debt aggregates, the crime module's threat/safety/per-type figures,
//     the refuse module's per-stream tonnage, and compose's own
//     people/money conservation ledgers. See
//     compose.Composition.StateDigest for the full field list and its
//     honest known limits.
//
// # Why the digest is BROAD, not PopulationHash alone (BUG-375 r2 -> r3)
//
// The r2 gate hashed only header + shard + PopulationHash. That catches
// population-class nondeterminism (births/deaths/migration) but nothing
// else: an independent destructive round injected conserving map-order
// nondeterminism into financeHook (a treasury<->households transfer of a
// map-iteration-order-dependent amount, total money conserved) and two
// same-seed 120-month runs diverged ~54,000 micropounds in treasury while
// PopulationHash stayed byte-identical — and the gate PASSED. Finance,
// crime, refuse and ledger nondeterminism shipped green. StateDigest closes
// that hole so that "one nondeterministic ordering bug in ANY hook ships
// green" is false: the r3 change was proved by scratch-injecting conserving
// map-order nondeterminism into financeHook (treasury diverged) AND into
// the refuse path (contamination diverged) and confirming the gate goes RED
// on each while a PopulationHash-only hash stays GREEN.
//
// This ensures the gate catches when hooks are missing entirely AND when
// any hook contains nondeterminism (e.g. a map-range iteration whose order
// varies between runs), in any observed module — not the population alone.
//
// # GR#21 — a red gate is auto-P0
//
// Any RunGate mismatch (docs/golden-rules-detail.md Rule #21) is, by
// project rule, automatically P0 and blocks every other merge until
// green — reverting the offending commit is always an acceptable first
// response. gate_test.go's TestDeterminismGate states this in its
// failure message so a human triaging red CI does not need to know the
// rule from memory (AC-8).
//
// # Determinism of the gate itself (AC-9, GR#21)
//
// This package never calls time.Now (nothing here seeds or hashes
// anything off the wall clock — protocol.NewCorrelationID uses
// crypto/rand, not a time-seeded source) and never ranges over a Go map
// on a path that feeds the hash: GateReport's comparison walks a plain
// []RunResult slice in the caller-supplied RunSpec order, never a map.
package detgate
