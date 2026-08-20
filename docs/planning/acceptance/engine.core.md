BOW code: MOD-012

> **See also:** `BUG-019.md` (StartSubscriptionPump copy-guard bug) carries
> its own acceptance criteria for this package — see README.md's
> cross-reference convention.

# Acceptance criteria — engine.core (MOD-012)

**BOW code:** MOD-012
**Spec refs:** §3 (Time, `docs/METROPOLIS-MASTER-v2.1.md` lines 117-135); M0-ENG §1.1-1.3 (process/thread topology, deterministic parallelism, memory budget, lines 794-840); §9 (Seasonality/month index, line 226); `int.protocol` (INT-001, the command surface this orchestrator drives).
**Date:** 2026-08-08
**Status:** done — MOD-012 closed 2026-08-09 (Tester-1 PASS 32/32, `-race`; full pipeline; determinism discipline accepted by lead review). **2026-08-16 refresh (BA):** status updated from `draft-ahead`; spec §/line refs re-pointed to current `docs/METROPOLIS-MASTER-v2.1.md`; MOD-004/MOD-005 dependency statuses refreshed (both `done`).
**Package under test:** `internal/engine/core/` (confirm via `node claude-bow.js show MOD-012` at dispatch)
**Standard gates:** see `README.md` — package for SG-4/SG-7 is `./internal/engine/core/...`.

## User stories

- As **the player** (via the UI, through `int.protocol`), I need `AdvanceTicks`/`SetSpeed`/`Pause`/`Resume` to drive a real two-layer clock (30 daily logistics ticks per calendar-month cycle), so that game pacing matches §3's designed cadence.
- As **`feat.detgate`**, I need the phase pipeline to run in a fixed, deterministic order over 256 fixed shards with ordered merges, so that same-seed-same-commands produces a bit-identical world at any worker count (M0-ENG §1.2).
- As **`T-SUBSCR`** (the view-subscription server), I need the orchestrator to expose a hook that computes and pushes per-subscription deltas off the tick path, so the UI never blocks on simulation work.
- As **`T-PERSIST`**, I need copy-on-write snapshot marshalling that runs concurrently with the tick loop, so saves never stall gameplay.
- As **every later engine module** (`MOD-017` world, `MOD-018` citizens, `MOD-022` finance, …), I need a stable phase-barrier pipeline and pre-sized arena allocation contract to register against, so modules can go from stub to real one at a time without re-architecting the orchestrator.

## Scope

The tick orchestrator: two-layer clock (calendar month ⇄ 30 daily ticks), fixed-order phase pipeline (production → logistics settlement → consumption & shortfall → population → land value & decay → finance), `POOL-SIM` shard-worker pool, `T-SUBSCR` and `T-PERSIST` side loops, speed control, zero-heap-allocation steady state.

## Acceptance criteria

### Functional

- **AC-1.** A clock type models the two-layer cadence: one calendar month = one day-night cycle = 30 daily logistics ticks (§3); `secondsPerMonthAt1x` is a named, non-hardcoded pacing constant (not a magic number sprinkled through the codebase) sourced from a config/data value per GR#15.
- **AC-2.** Speed control supports pause / 1x / 2x / 4x, with 8x reserved for debug mode (`feat.debugmode`, gated — this package need only expose the hook, not enforce the debug-only gate itself). A passing test asserts tick-advance rate scales with the configured speed multiplier.
- **AC-3.** The monthly tick executes phases in the fixed, documented order: production → logistics settlement → consumption & shortfall → population → land value & decay → finance. A passing test asserts phase execution order via an instrumented/mock phase sequence (e.g. a slice recording phase-entry order).
- **AC-4.** `POOL-SIM` is sized `runtime.NumCPU()-2` (M0-ENG §1.1: "leave 2 for UI+OS"), with a floor (documented) preventing zero or negative pool size on low-core machines.
- **AC-5.** The world is partitioned into exactly **256 fixed shards** (spatial for cells/network, id-hash for citizens/firms per §1.2) — the shard count is a named constant, never derived from core count, and a test asserts `NumShards() == 256` (or equivalent).
- **AC-6.** Every phase is a barrier: phase *k+1* only reads phase-*k*-committed state; cross-shard effects during a phase are captured as messages applied in `(shard, sequence)` order at the barrier. A passing test with an intentionally out-of-order-submitted message set asserts deterministic application order regardless of submission order.
- **AC-7.** `T-SUBSCR`-facing hook: the orchestrator exposes a way to compute/push per-subscription deltas that runs off the main tick path (a separate goroutine/queue, not inline in phase execution) — code inspection confirms delta computation is not synchronously blocking phase advancement.
- **AC-8.** `T-PERSIST`-facing hook: a copy-on-write snapshot request can be issued while ticks continue; a passing test issues a snapshot mid-run and asserts the tick loop's throughput is not blocked for the duration of marshalling (e.g. ticks continue to advance concurrently, verified via a channel/counter, not a wall-clock timing assertion which would be flaky).
- **AC-9.** Steady-state monthly tick allocates zero bytes on the heap on the hot path once arenas are warmed — a benchmark test using `testing.B` with `-benchmem` (or equivalent `go test -bench . -benchmem`) reports `0 allocs/op` for the steady-state tick path after warm-up, OR (if zero is not yet achievable at draft time) the AC is satisfied by an escape-analysis CI gate (`go build -gcflags="-m"`) showing no unexpected heap escapes on the named hot-path functions — Tester picks whichever the junior's PR actually implements and records which.

### Error handling

- **AC-10 (GR#7).** A phase hook that returns an error surfaces as `ErrPhaseHookFailed` (`MET-E001`) and aborts that tick's remaining phases (and therefore the whole `AdvanceTicks` call, since later ticks would run against an uncommitted/partial phase-k state) rather than panicking or silently continuing to the next phase. Check: `grep -n "ErrPhaseHookFailed\s*=" internal/engine/core/errors.go` shows `MET-E001`; a passing test (`grep -rn "func Test.*[Pp]haseHook.*[Ee]rr\|func Test.*[Pp]hase.*[Ff]ail" internal/engine/core/*_test.go`) makes one phase hook return an error and asserts both that `AdvanceTicks`'s returned error carries `MET-E001` AND that no later phase in that tick ran (e.g. a subsequent phase's instrumented call count is zero) — not merely that a matching-named test function exists and passes.
- **AC-11 (GR#7).** `AdvanceTicks` with `n <= 0` or `n > MaxAdvanceTicksPerCall` is rejected with `ErrInvalidAdvanceTicks` (`MET-E000`), and the tick counter is left completely unchanged — never silently clamped to a valid range or partially advanced. Check: `grep -n "ErrInvalidAdvanceTicks\s*=" internal/engine/core/errors.go` shows `MET-E000`; `grep -n "MaxAdvanceTicksPerCall" internal/engine/core/engine.go` shows the bound; a passing test (`grep -rn "func Test.*[Ii]nvalidAdvanceTicks\|func Test.*AdvanceTicks.*[Nn]egative" internal/engine/core/*_test.go`) calls `AdvanceTicks` with a negative value and with a value exceeding the bound, and asserts for each that the returned error carries `MET-E000` AND that the engine's tick counter after the call equals its value before the call — not merely that a matching-named test function exists and passes.

### Determinism & safety

- **AC-12 (GR#21).** `grep -rn "time.Now" internal/engine/core/*.go` (excluding `_test.go`) returns no matches — "nothing in the engine ever calls wall-clock time for logic" (M0-ENG §1.1) is absolute for this package; simulation time is `Tick`/calendar-month only.
- **AC-13 (GR#21).** No `range` over a Go map on the tick path — manual scan of phase-execution and shard-merge code for map-range loops whose iteration order could affect committed state; none found (file:line recorded on any hit).
- **AC-14 (GR#21).** `GOGC=200` is set (or documented as the deployment default) and `GODEBUG=gctrace=1` is usable to verify GC pause behaviour in the perf harness — check: a doc comment or build/run script sets/documents this.
- **AC-15 (GR#21).** A same-seed, same-command-log run produces a bit-identical world snapshot at `POOL-SIM=1` vs a higher worker count — a passing test runs the orchestrator twice with different pool sizes and asserts `sha256` of the resulting world state matches (this is `feat.detgate`'s job as a dedicated CI gate, but `engine.core` must expose what that gate needs — a hashable/serializable world-state snapshot function — and this AC checks that hook exists and produces matching hashes across pool sizes in a minimal smoke test here).

### Documentation

- **AC-16.** The package doc states module key `engine.core`, cites §3 and M0-ENG §1.1-1.3, and documents the fixed phase order and the 256-shard invariant in prose (not only in code).

## Out of scope

- The determinism CI gate itself as a standalone 120-month×2-runs×worker-count test (that is `feat.detgate`, `FEAT-004`) — `engine.core` only needs to expose the hashable-snapshot hook `feat.detgate` will call.
- Any real simulation module content (citizens, finance, traffic, …) — `engine.core` orchestrates phases; the phases themselves are stub implementations at this stage (M0-ENG §2's "module stubbing" discipline), wired via the module registry (`MOD-005`, a dependency, not part of this item).
- `T-SUBSCR`'s actual per-view projection logic and `T-PERSIST`'s actual serialization format — those are `int.protocol` (subscriptions) and `int.serializer` (snapshots) respectively, already frozen in Sprint 0; `engine.core` only needs the orchestration hooks that call into them.

## Escalations

- **ASM-005** (P2, `internal/engine/core/clock.go`) — `secondsPerMonthAt1x` pacing constant is a named Go var (`DefaultSecondsPerMonthAt1x=480` + `WithSecondsPerMonthAt1x` override), satisfying "not a magic number sprinkled about" but NOT literal GR#15 data sourcing; deferral to the MOD-036 balance harness is a balance-regime placeholder (pacing tuning is owned by the M2 balance pass; debt FEAT-030).
- **Resolved.** The draft-time escalation is moot: `MOD-004` (foundation.det) and `MOD-005` (module registry) are both `done`, and `MOD-012` itself closed 2026-08-09. No open dependencies remain.

## Spec-fold amendments (FEAT-084 SF wave, 2026-08-18)

> Substantive AC amendments folded from the FEAT-084 ASM disposition (class SF).

### ASM-053 — advanceOneDailyTick deliberately omits its own checkNotCopied (amends the copy-guard AC)
`advanceOneDailyTick`'s `e.mu.Lock()` (the 8th site) is deliberately left WITHOUT its own `checkNotCopied` call: it is unexported with exactly one call site (AdvanceTicks' loop, reached only after `seal()` has already rejected a copy), and `self` never changes for an Engine's lifetime, so a redundant check is provably unreachable-for-a-copy dead code on the hottest path (once per TICK, not once per AdvanceTicks call). If it ever gains a second call site that does not go through `seal()` first, the safety argument breaks silently — the function's doc comment states the contract so a future second caller is a deliberate, visible decision. Check: the doc comment on `advanceOneDailyTick` states the single-call-site safety argument.

- **ASM-826 (confirm-and-close).** Spec-ref line fix: §9 Seasonality header is at line 226 (month-index content 228), not 224 (which is §8 traffic); the §3 range 115-136 is approximately correct.
