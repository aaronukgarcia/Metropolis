// Package integration is INCREMENT 1 of the Integration Engine
// (docs/planning/proposals/integration-engine.md, §8 build increments):
// the Integration contract + a location-transparent, deterministic shard
// executor. Nothing else — the priority-tiered overflow queue, the
// resilience/reconnect state machine, crash recovery, the ICD template +
// code.json registration, and the monitoring dashboard are later
// increments (§8 points 2-6). This package does not touch code.json or
// the master plan; registration happens once the contract stabilises
// (proposal §7, "Home").
//
// # What this package guarantees
//
// An Integration's work is a pure, seeded function over a shard of state,
// merged in a fixed order (proposal §1 point 1: "determinism is sacred and
// location-transparent"). This package does not reimplement that
// guarantee — it is entirely inherited from internal/foundation/det:
// det.MergeInOrder folds the 256 per-shard results in strict ascending
// shard order, and det.ApplyBarrier applies every cross-shard message in
// canonical (shard, sequence) order. Execute (executor.go) is a thin
// driver around those two primitives; it contributes no ordering decision
// of its own. Byte-identical output therefore follows from the same
// argument det.RunPhase's doc comment already makes: shard assignment is
// fixed, and both merge stages re-sort into canonical order before
// combining, discarding whatever order goroutine (or, later, network)
// scheduling happened to produce this run.
//
// # Location transparency: the WorkerPool seam
//
// The one thing this package adds beyond internal/engine/core's existing
// runPhaseForHook is the WorkerPool abstraction (worker_pool.go): WHERE a
// shard's RunShard call happens is factored out from HOW its result gets
// merged. det.RunPhase hardcodes "N goroutines steal shards from a shared
// queue" as its dispatch strategy; that strategy is fine for local
// execution but has no way to become "ship this shard's pure inputs to a
// cloud worker and await its pure output" (proposal §4, the cloud path).
// WorkerPool.Dispatch(numShards, work) is the seam that generalises it:
// work is a closure of exactly the same shape det.RunPhase's own internal
// worker goroutines call, and Dispatch's only contract is "call work(s)
// for every s in [0, numShards) exactly once, then don't return until
// every call has completed" — HOW it does that (goroutine pool, strict
// serial loop, or eventually an RPC fan-out awaiting responses) is
// invisible to Execute, which only ever sees the shard results Dispatch
// leaves behind. Because Execute always merges via det.MergeInOrder and
// applies via det.ApplyBarrier regardless of which WorkerPool ran the
// work, the merge is provably invariant to the dispatch strategy — this
// is what executor_test.go's equivalence tests exist to demonstrate
// mechanically rather than merely assert by argument.
//
// Two WorkerPool implementations ship in this increment, both purely
// local, to prove the seam is real before anything remote exists:
//
//   - LocalPool: concurrent goroutine dispatch, parameterised by worker
//     count — the direct analogue of det.RunPhase's own dispatch loop.
//   - SerialPool: a single goroutine, strictly in shard order 0→255 — the
//     degenerate case a slow, one-shard-at-a-time remote worker would
//     produce. Included specifically because "runs on one everything-in-
//     order worker" is the shape a naive first RemotePool would have, and
//     the equivalence tests prove that shape already produces the exact
//     same byte-identical result as full concurrency does, today, with no
//     remote code written yet.
//
// # The future RemotePool seam (not built here)
//
// A later increment's RemotePool implements the same WorkerPool interface
// by serialising each shard's pure inputs (Integration is already
// required to be a pure, seeded function of the shard index — no captured
// mutable state), shipping them to a cloud worker, and writing the
// returned (T, []det.Message[M]) into the same per-shard result slot
// LocalPool and SerialPool write locally. Nothing else in this package
// changes: Execute's merge/barrier calls, the Integration contract, and
// every existing WorkerPool caller are untouched by that addition — this
// is the concrete form of proposal §7's "a WorkerPool abstraction hides
// local-vs-remote; the deterministic merge is invariant" and §4's "moving
// it to a cloud worker changes nothing observable." RemotePool itself,
// its transport, retry/backoff, and reconnect behaviour are proposal §8
// increments 2-3 — explicitly out of scope here.
//
// # BUG-269 fast path
//
// An Integration that opts into SingleShard() == true (mirroring
// internal/engine/core's SingleShardHook) skips the 256-shard dispatch
// entirely and runs RunShard(0) inline — see executeSingleShard's doc
// comment in executor.go for the same byte-identical argument
// runPhaseForHookFast's doc comment makes, restated for this package's
// Integration/Combine shape instead of engine/core's PhaseHook/Effect
// shape.
package integration
