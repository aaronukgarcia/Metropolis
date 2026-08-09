BOW code: MOD-004

# Acceptance criteria — foundation.det (MOD-004)

**BOW code:** MOD-004
**Spec refs:** §1.2 (Deterministic parallelism — the crown rule, in full, `docs/METROPOLIS-MASTER-v2.1.md` lines 819-827); A8 (Mechanical enforcement, line 1368, and R9's adjudication, line 1358); GR#21 (Red Determinism Gate Stops the Line, `CLAUDE.md` line 52).
**Date:** 2026-08-09
**Status:** active
**Package under test:** `internal/foundation/det/` (confirm via `node claude-bow.js show MOD-004` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/foundation/det/...`.

## User stories

- As **`engine.core`**, I need a fixed 256-shard partition with shard-order (0→255) merges and phase barriers, so "same seed + same commands ⇒ bit-identical world at any worker count" is a property this package guarantees, not something every engine module has to reinvent.
- As **every stochastic draw in the simulation** (citizen mortality, personality noise, route-choice perturbation…), I need a counter-based Philox-style RNG keyed `(worldSeed, entityId, month, purposeTag)`, so draws are position-independent and order-free — no shared RNG object anywhere.
- As **the finance/economy modules**, I need int64 micro-pounds helpers, so money arithmetic never touches float64 and summation order never changes a balance.
- As **`feat.detgate`**, I need this package's shard-merge and RNG-stream code to itself be provably deterministic across worker counts, so the CI gate has something real to hash-compare rather than hoping the underlying primitives behave.
- As **CI** (A8, GR#21), I need a lint rule banning `range` over a Go map on any code path this package's callers use for ordering-sensitive output, so the map-iteration-nondeterminism class of bug is caught mechanically, not by review.

## Scope

The determinism core: the 256-fixed-shard model (partition + shard-order merge), phase-barrier message routing `(shard, sequence)`-ordered, the counter-based RNG stream keyed `(worldSeed, entityId, month, purposeTag)`, int64 micro-pounds money helpers, and fixed-order float summation for the cases where float64 is unavoidable.

## Acceptance criteria

### Functional

- **AC-1.** The package exposes a shard-count constant fixed at **256**, never derived from `runtime.NumCPU()` or any runtime value — `go doc ./internal/foundation/det` shows a named constant (e.g. `NumShards = 256`), and a passing test asserts its value.
- **AC-2.** A shard-assignment function exists for both partitioning schemes named in spec: spatial (cells/network, by position) and id-hash (citizens/firms, by entity ID). Check: `go doc ./internal/foundation/det` lists two distinct assignment functions/methods (e.g. `ShardForCell(x, y int) int`, `ShardForEntity(id uint64) int`), each returning a value in `[0, 256)`; a passing test asserts the range invariant across a spread of inputs, and that `ShardForEntity` is a pure hash (same ID always maps to the same shard).
- **AC-3.** A shard-order merge function combines per-shard results in **strict ascending shard order (0→255)**, regardless of the order workers finished their shards in — a passing test feeds shard results to the merge function in a randomized completion order and asserts the merged output is identical to feeding them in already-sorted order, across multiple randomized-order trials.
- **AC-4.** A phase-barrier message-routing mechanism exists: cross-shard effects produced during a phase are captured as messages and applied at the barrier in `(shard, sequence)` order — a passing test submits messages out of both shard order and per-shard sequence order and asserts the applied order is the canonical `(shard, sequence)` sort, not submission order.
- **AC-5.** A counter-based (Philox-style) RNG stream constructor exists, keyed by `(worldSeed, entityId, month, purposeTag)` (four-integer/string key), producing position-independent, order-free draws — `go doc ./internal/foundation/det` shows a constructor (e.g. `NewStream(worldSeed uint64, entityID uint64, month int64, purpose string) Stream` or equivalent) and a draw method. A passing test asserts: (a) the same key always produces the same sequence of draws, (b) two different keys (differing in any one of the four components) produce statistically distinct sequences (a simple inequality-on-first-N-draws check is sufficient, not a full statistical test suite), (c) drawing from one stream never mutates or depends on any other stream's state (no shared global RNG object — verified by running two streams interleaved vs sequentially and asserting each stream's own output sequence is unaffected by interleaving).
- **AC-6.** int64 micro-pounds money helpers exist: at minimum, safe add/subtract/multiply-by-rational helpers operating on a named `Micropounds` (or equivalent) `int64`-based type, so calling code never has to hand-roll fixed-point arithmetic or accidentally mix float64 into a money computation. Check: `go doc ./internal/foundation/det` (or a co-located `money.go`) shows the type and helpers; a passing test asserts round-trip correctness (e.g. `FromPounds(x).ToPounds() == x` for representable values) and overflow behaviour (AC-11).
- **AC-7.** A fixed-order float64 summation helper exists for the "float64 unavoidable" case (e.g. physics-ish diffusion per §1.2 point 4) — it sums a slice/shard-ordered sequence of float64 values in a documented, fixed order (e.g. always ascending shard index, never map iteration) so cross-shard aggregation is reproducible. A passing test asserts summing the same multiset of values via two different *input orderings* that nonetheless map to the same canonical shard order produces bit-identical results, while summing via an explicitly non-canonical order (to demonstrate the helper enforces canonical order rather than trusting the caller) is exercised by a test that feeds shard-tagged values out of order and asserts the helper re-sorts by shard before summing.

### Error handling

- **AC-8.** `ShardForCell`/`ShardForEntity` never panic for any valid input range; out-of-range/invalid input (e.g. a negative entity ID where the type allows it) is documented as either impossible by type (preferred — use `uint64`) or handled via a registry-sourced `errs.E` rather than an unchecked panic.
- **AC-9.** The RNG stream constructor rejects a zero/degenerate `worldSeed` only if that is genuinely unsafe for the underlying Philox construction (document the decision either way) — if zero is a valid seed, a test confirms it produces a well-defined, non-degenerate stream; if it is rejected, a test confirms a clear registry-sourced error rather than a silently weak stream.
- **AC-10.** The shard-order merge function detects and errors (rather than silently merging incompletely) if it is handed fewer than 256 shard results when 256 are expected, or a duplicate shard index — a passing test asserts both cases produce a registry-sourced error.
- **AC-11.** Money-helper overflow (e.g. multiply pushing past `int64` range) is detected and returns a registry-sourced error rather than silently wrapping/truncating — a passing test asserts an overflow-inducing input errors cleanly.

### Determinism & safety

- **AC-12 (GR#21 — shard-merge determinism at multiple worker counts).** A passing test simulates the shard-order merge and phase-barrier message application under at least two different **simulated worker counts** (e.g. shards processed by 1 goroutine vs by N goroutines pulling from a work queue) with the same input shard results/messages, and asserts the merged/applied output is byte-identical across worker counts. This is the primitive-level counterpart to `feat.detgate`'s full-engine 1-vs-14-worker hash comparison — this package must prove its own building blocks are worker-count-invariant before `engine.core` builds on them.
- **AC-13 (GR#21).** `go test ./internal/foundation/det/... -race -count=1` passes with no data race when multiple goroutines draw from *different* RNG streams concurrently and when multiple goroutines merge different shard ranges concurrently.
- **AC-14 (A8 — map-range lint).** No `range` over a Go map appears anywhere in this package's own code on any path feeding shard assignment, merge order, RNG key construction, or money arithmetic — manual scan of every `.go` file in the package (excluding `_test.go`) for `for _, v := range someMap` or `for k := range someMap` where `someMap` is a `map[...]...` and the loop's output affects a returned/committed value; none found (Tester records file:line on any hit). If A8's "custom golangci-lint rule" for this ban has landed elsewhere in the repo by the time this item is tested, `golangci-lint run ./internal/foundation/det/...` passing with that rule enabled is acceptable in place of the manual scan.
- **AC-15 (GR#21).** `grep -rn "time.Now" internal/foundation/det/*.go` (excluding `_test.go`) returns no matches — this package must never call wall-clock time; all "time" inputs are simulation values (`month int64`, tick counters), never `time.Time`/`time.Now()`.
- **AC-16.** `go test ./internal/foundation/det/... -race -count=1` includes a shard-count invariance test in the specific sense M0-ENG §6's Definition of Done requires ("determinism-relevant modules also add a shard-count invariance test") — distinct from AC-12's worker-count test: this asserts that varying the *number of shards a given entity set is spread across* in a test harness (while keeping the canonical 256-shard production constant unchanged) does not change per-entity RNG draw outputs, since RNG streams are keyed by `entityId`, not by shard index, and must not accidentally leak shard-assignment into draw content.

### Documentation

- **AC-17.** The package doc states module key `foundation.det`, cites §1.2 and A8 in full, and documents the "no shared RNG object anywhere" rule and the 256-shard-is-a-constant-forever rule prominently, since both are easy to violate by convenience refactor later.

## Out of scope

- `engine.core`'s actual phase pipeline and orchestration loop (`MOD-012`) — this package provides the primitives (shards, barriers, RNG, money, summation); the orchestrator consumes them.
- `feat.detgate`'s full end-to-end 120-month CI gate — this item's AC-12/AC-16 are primitive-level determinism proofs the gate will build on, not the gate itself.
- Any actual golangci-lint custom-rule implementation for the map-range ban — AC-14 accepts either a manual scan or an already-landed lint rule; authoring that lint rule is not this item's deliverable unless the lead's brief says otherwise.

## Escalations

- None at draft time. No spec/brief conflict found — §1.2 and A8 are unusually precise and left little room for interpretation. One clarification for Bill: the brief's phrase "shard-merge determinism tests at multiple worker counts (GR#21)" is implemented here as AC-12 using *simulated* worker counts within this package's own test suite (since this package doesn't itself own a goroutine pool — `engine.core`'s `POOL-SIM` does); if a literal multi-goroutine-pool test is wanted at this layer specifically, please confirm and this AC will be tightened.
