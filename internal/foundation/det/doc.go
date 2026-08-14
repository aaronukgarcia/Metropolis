// Package det is the determinism core: the 256-fixed-shard model, the
// phase-barrier scheduler, counter-based (Philox-style) RNG streams, an
// int64 micro-pounds money type, and fixed-order float64 summation. Same
// seed + same command log must produce a bit-identical world regardless of
// worker count, on any machine (M0-ENG §1.2, "the crown rule"). This
// package provides the primitives; `engine.core` (POOL-SIM, MOD-012)
// consumes them to build the actual tick pipeline.
//
// Module key: foundation.det (see code.json; GUID 4e1a1a9c-0757-4964-9d4b-74d37584e739)
// Spec ref:   §1.2 (in full, quoted below); A8 (mechanical enforcement)
//
// # §1.2 Deterministic parallelism (the crown rule, spelled out)
//
// Same seed + same command log ⇒ bit-identical world, regardless of worker
// count, on any machine. Parallelism must therefore be structured, never
// opportunistic:
//
//  1. World is partitioned into 256 fixed shards (spatial for cells/
//     network, id-hash for citizens/firms). 256 is constant forever —
//     never derived from core count. Workers steal shards; results are
//     merged in shard order 0→255 at each phase barrier.
//  2. Every phase is a barrier: phase k+1 reads only phase-k-committed
//     state. Within a phase, shards write only shard-local scratch;
//     cross-shard effects are emitted as messages routed and applied in
//     (shard, sequence) order at the barrier.
//  3. RNG: counter-based (Philox-style) streams keyed (worldSeed,
//     entityId, month, purposeTag) — draws are position-independent and
//     order-free. No shared RNG object anywhere.
//  4. Money is int64 micro-pounds. Simulation aggregates that must sum
//     across shards use int64 or fixed-point; where float64 is unavoidable
//     (physics-ish diffusion), summation is performed in fixed shard
//     order. Never range over a Go map on the tick path — iteration order
//     is nondeterministic; use sorted keys or slices.
//  5. CI runs the determinism gate on every merge: same seed, 120 months,
//     twice, sha256(worldSnapshot) must match; then again with
//     POOL-SIM=1 vs =14. A mismatch fails the build. This test is written
//     FIRST, in M1 week one, against the walking-skeleton world.
//
// # A8 Mechanical enforcement
//
// Escape-analysis gate, gctrace perf gate, determinism-gate-first TDD,
// lint-enforced determinism rules (custom golangci-lint rule bans `range`
// over a Go map on any ordering-sensitive path — this package's own code
// contains none; see the package tests' manual-scan note for AC-14).
//
// # Two rules that are easy to violate by convenience refactor later
//
//   - NumShards (256) is a constant forever. It must NEVER be derived from
//     runtime.NumCPU(), a config value, or anything else that could vary
//     between machines or builds. Every merge, message-routing, and
//     shard-count-invariance guarantee in this package (and every package
//     built on it) depends on that number never moving.
//   - No shared RNG object anywhere. Every Stream is an independent value
//     keyed by (worldSeed, entityId, month, purposeTag); it holds no
//     reference to any other stream, no package-level mutable RNG state
//     exists, and two streams may be drawn from concurrently and
//     interleaved from any number of goroutines without affecting each
//     other's output. If a future refactor introduces a single shared
//     *rand.Rand or similar "for convenience," that refactor is a
//     determinism regression, not a simplification.
package det
