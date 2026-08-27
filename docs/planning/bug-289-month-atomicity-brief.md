# BUG-289: Monthly Settlement Atomicity — Architectural Options

**Date**: 2026-08-27  
**Status**: Architecture Brief (UNRESOLVED)  
**Requires**: Aaron's ruling on retry/rollback semantics  

## The Problem

When a monthly-phase hook fails partway through the monthly pipeline (e.g., PhaseProduction succeeds, PhaseLogisticsSettlement fails), the world is left in an **inconsistent partial state**:
- Clock has already advanced past the month boundary (tick 30 → 31, month 0 → 1)
- Effects from earlier phases (PhaseProduction) have been applied via `hook.ApplyEffect(eff)` and mutated the world state
- Later phases never run (Consumption, Population, LandValueDecay, Finance)
- On the next AdvanceTicks call, phases run AGAIN on already-mutated state → **double-application** of earlier phases' effects

## Phase Dependency Analysis

### Monthly Phase Order (core/phase.go:79-86)
```
PhaseProduction
PhaseLogisticsSettlement  
PhaseConsumptionShortfall
PhasePopulation (attract)
PhaseLandValueDecay
PhaseFinance
```

### Cross-Phase Dependencies

**Evidence: Phase Execution Model (core/phase.go:221-269)**
- Each phase's `hook.RunShard(shard)` computes Effects in parallel across 256 shards
- Effects are collected per-shard, then applied serially via `hook.ApplyEffect(eff)` in canonical (shard, sequence) order
- Each phase's ApplyEffect **mutates the shared world state** before the next phase begins

**Evidence: Attract Reading Finance State (attract/api.go)**
- `HousingAffordability()` calls `finance.WagesPosted()` to read this month's wage bill (line: `wages := int64(finance.WagesPosted())`)
- Per BUG-355 comment: "PhaseFinance is the LAST monthly phase, so the tick opened here holds exactly this month's posts when NEXT month's population phase reads WagesPosted"
- **This is a 1-month lag**, so attract doesn't read Production within the same month
- However, within a month: Production → Logistics → Consumption form a read-write chain (each phase reads state mutated by earlier phases)

**Conclusion: Phases Are DEPENDENT (read-write chain)**  
Each phase applies Effects that mutate state, and subsequent phases in the same month read that mutated state. This is by design (production flows through logistics to consumption). **A rolled-back clock alone does NOT restore the world state.**

## Atomicity Options

### Option A: Deferred-Apply (Stage All Effects, Apply Only If All Succeed)

**Mechanism**: 
1. Run ALL monthly phases' `RunShard` methods sequentially
2. Collect every Effect in a buffer without applying them
3. Only after ALL phases' RunShards complete successfully: apply Effects serially via ApplyEffect in canonical order
4. If any RunShard fails: discard the buffer, return error without mutating state

**Pros**:
- Guarantees atomicity: either zero phases or all phases mutate state
- No world-state snapshot cost (Effects are staging primitives already)
- Preserves phase read-write dependencies (each phase still computes based on "current" state within the buffer pipeline)

**Cons**:
- Requires redesign: each phase must accumulate Effects into a shared buffer instead of immediately calling ApplyEffect
- Phase contract change: ApplyEffect is called from the phase pipeline today — this would shift to a late barrier-application
- Effect types must support serialization into buffer (minor, Effects are already serialized at the det.RunPhase barrier)

**Feasibility**: Medium — requires refactoring the phase barrier but is conceptually simple and has no perf overhead beyond staging.

### Option B: World-State Snapshot & Rollback

**Mechanism**:
1. At month start, snapshot the entire mutable world state (citizens, cells, markets, finance state, etc.)
2. Run all monthly phases normally (they mutate state in-flight)
3. If any phase fails: restore the world state from the snapshot
4. Clock is already rolled back (requires fix to engine.go's advanceOneDailyTick)

**Pros**:
- No phase contract change — phases work exactly as today
- Familiar pattern (existing WAL/checkpoint model in persist.go already snapshots for saves)

**Cons**:
- Snapshot cost per month: the world has ~1000s of fields per shard (citizens, parcels, firms, etc.) — a full snapshot may be expensive
- Snapshot storage: must hold the snapshot in memory for the duration of the monthly phase run
- Rollback complexity: must restore not just clock but every mutable field in the engine's registered modules
- **Blocker question**: Can the snapshot be a shallow "pointer to HEAD" copy using the existing WAL/checkpoint infrastructure, or does it require a deep field-by-field copy?

**Feasibility**: Medium-high — doable, but perf impact unknown without measuring snapshot size and restore time.

### Option C: Clean Halt (No Atomicity, Accept Partial State)

**Mechanism**:
1. Run monthly phases normally
2. If any phase fails: do NOT advance clock, do NOT increment tickCounter, return error
3. World state remains partial but consistent-as-of-the-failing-phase
4. Caller must handle retry/recovery (operator intervention, saved-game reload, etc.)

**Pros**:
- Minimal code change: only clock/tickCounter need fixing
- No snapshot/buffer overhead
- Fail-fast: error surface is clear

**Cons**:
- Partial state is visible on failure (inconsistent from player perspective)
- Requires external retry logic (the ledger has a new tick opened, citizens are partially migrated, etc.)
- May violate invariants if the game assumes months are always "complete or never started"

**Feasibility**: High — straightforward but may break game logic that assumes month atomicity.

---

## Recommendation

**Option A (Deferred-Apply)** is the most aligned with the architecture:

1. **Phase independence at the Effect level**: Phases produce Effects (read-only compute), Effects are applied (write-only mutation) — this is the contract buried in core/phase.go's doc comment.
2. **Existing barrier pattern**: det.RunPhase already stages Effects and applies them at a barrier. This proposal extends that pattern to the monthly-phase level.
3. **No snapshot cost**: No full world-state copy per month.
4. **Correctness**: True atomicity — either all phases apply, or none do.

**Implementation sketch** (for Aaron):
- Refactor `advanceOneDailyTick` to run monthly phases into a staged-effect buffer
- Each phase's ApplyEffect is wrapped to append to buffer instead of applying immediately
- At barrier: call ApplyEffect serially in canonical order, or on failure discard buffer and rollback clock

**Complexity**: Medium. The phase hook interface itself doesn't change (ApplyEffect signature is the same), only the orchestration in engine.go.

**Fallback if perf testing reveals a problem**: Option B (snapshot-rollback) using the existing persist.go snapshot patterns.

---

## Open Questions for Aaron

1. **Retry semantics**: On partial-month failure, should the caller retry AdvanceTicks from the same tick (option C halts here), or is retry out of scope (assuming the game operator handles it)?
2. **Accepted partial state**: Is a partial-but-halted month acceptable (option C), or is month atomicity a hard invariant?
3. **Snapshot infrastructure**: Can engine.core reuse the persist.go snapshot for world-state rollback (option B), or does it need its own mechanism?

