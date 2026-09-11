# FEAT-1972079934: Sparse-Action-Log + Tick Synthesis (Replay Cost Reduction)

**Mkey:** FEAT-1972079934  
**Epic:** Structural fix for BUG-460 (genesis-replay OOM). Sparse-action-log + tick-synthesis to bound replay allocation and preserve genesis permanently.

**Relates to:** FEAT-1972079897 (hard-reset replay inc1), BUG-460 (replay allocation churn), BUG-617 (savepoint-anchored chunked replay — the complementary path already shipped)

**GR#25 scope:** webconsole-internal `journal.ts` / `genesisReplay.ts` / `store.tsx` / `types.ts`. No new code.json edges. Extends the journal format to store only sparse player actions (not every tick); replay synthesizes the ticks between them.

---

## Problem Statement

Current journal stores EVERY state-affecting action including `tick`. At turbo speed or in a long game, `tick` actions dominate: a 2,644-tick city with ~100 player placements generates ~2,500 tick entries (97% of the journal). The ring-buffer `JOURNAL_CAP = 50,000` means:
- Long games evict genesis (genesis replay becomes impossible for saves older than 1–2 hours of play)
- Replay cost scales linearly with replay ticks, not player actions: observed ~90s to replay 900 ticks on a moderate fixture (BUG-606-replay test), measured ~2–6ms/action at scale

**Option (2) — savepoint-anchored replay (BUG-617, shipped):** Replays journal *tail* onto a snapshot. Solves the wedge for large tails but does not solve genesis eviction or the need for hard-reset replay across engine rule changes.

**Option (1) — sparse-action-log + tick synthesis (this increment):** Store only player actions (place, bulldoze, policy, tax, loan, consolidator toggle, etc.) stamped with their tick; synthesize the ticks between them on replay. Expected outcome: journal shrinks 10–100x, genesis stays available indefinitely, replay cost dominates by player action count (dozens–hundreds) not total ticks.

---

## Design (from hard-reset-replay brief §4.1–4.2)

### Journal Schema Change

**Current:** `{ entries: JournalEntry[] }` where `JournalEntry = { tick: number, action: Action }`

**New:** Two separate persisted logs:
1. **Sparse action log** (`SPARSEentries`): Stores only player actions, each stamped with its recorded tick.
   - Schema: `{ tick: number, action: Action }` (unchanged field names, clearer semantics: tick is the action's recorded tick, not an implicit "advance to this tick then apply action")
   - No `tick` actions stored (the reducer case `{ type: 'tick' }` is omitted)
   - Ring-buffer unbounded or much larger (no fixed `JOURNAL_CAP`; cap moved to bytes or true max player actions — TBD with Aaron)
   - Persisted to a NEW localStorage key (e.g., `metropolis.sparseActionLog`) alongside the kept snapshot+tail savepoints

2. **Backwards compatibility:** The existing `metropolis.journal` key keeps the full journal for **current-save autosave/resume on the same build** (same-build resume via `restoreFromSavepoint` is NOT affected). The sparse log is additive: used ONLY for genesis replay and cross-build rebuild.

### Replay Path (genesisReplay.ts)

Replayer flow for `replayFromGenesisFromSparseLog(journal: SparseActionLog, opts)`:
```
state = initialState(opts)
lastTick = 0
for each (tick, action) in sparseActionLog.entries:
  // Synthesize tick advances between last recorded tick and this action's tick
  while lastTick < tick:
    state = reducer(state, { type: 'tick' })
    lastTick++
  // Apply the player action
  state = reducer(state, action)
// Final ticks to the end-of-game tick (recorded in sparseActionLog.endTick)
while lastTick < sparseActionLog.endTick:
  state = reducer(state, { type: 'tick' })
  lastTick++
return state
```

The **existing** `replayFromGenesis(journal)` stays for compat; new code uses the sparse variant. Determinism is byte-identical when fed the same initial tick and end tick: the reducer sees the same actions in the same order, merely with interleaved synthetic ticks.

---

## Acceptance Criteria

### AC-1 (sparse action log is stored, not full journal)

**Scenario:** Player places a building at tick 50, ticks 51–150 pass silently (100 tick actions), then places another building at tick 151. Serialize the journal for persistence.

**Check:** The persisted sparse-action log contains EXACTLY 2 entries (one place at tick 50, one at tick 151), not 102 entries (101 ticks + 1 place). Size savings measured on the fixture: ~95% smaller.

**Mutation:** Include every tick action in the sparse log. Serialized size returns to full-journal size. Test measures non-reduced size.

**False-pass:** Journal is externally sparse but internal representation still holds all ticks.

---

### AC-2 (synthesized ticks match recorded ticks in replay)

**Scenario:** Replay the sparse action log from AC-1 via `replayFromGenesisFromSparseLog` with endTick=151. Compare the final state to a reference replay of the FULL journal (same actions + all ticks, from the committed full journal).

**Check:** Final state `stableStringify()` byte-identical. No off-by-one errors in tick synthesis. `replayIsDeterministic()` harness confirms same-log replays are deterministic (run the replay twice on the same sparse log, compare outputs).

**Mutation 1:** Replay loop synthesizes tick 50 twice (mistaken off-by-one). Final population/funds differ. Test goes red.

**Mutation 2:** Skip synthesizing the final ticks (endTick ticks are not applied). Final state missing the last tick's effects. Test goes red.

**False-pass:** Sparse log exists but replay never actually synthesizes; instead, the replayer reads the FULL journal as a fallback.

---

### AC-3 (genesis is never evicted by sparse log)

**Scenario:** Long game: 50,000 player actions over many hours (turbo speed), each action recorded with its tick, total ticks >> 50,000. The sparse log grows to ~50,000 entries (player actions only; no tick actions). Ring-buffer capacity for sparse log is unbounded OR set to (e.g.) 100,000.

**Check:** Boot after the long game. Sparse log still holds the very first player action (tick ~0). `replayFromGenesisFromSparseLog` on the full sparse log reconstructs the final state deterministically.

**Mutation 1:** Sparse log uses `JOURNAL_CAP = 50,000` (the OLD cap). After >50,000 player actions, earliest entries are evicted. Genesis is lost; replay is incomplete.

**Mutation 2:** Replay reads the OLD full journal (ticks included) instead of the sparse log. Falls off the end at the CAP boundary.

**False-pass:** Sparse log exists and is large, but its size is never tested; a small fixture hides the growth.

---

### AC-4 (player-action-only persistence reduces save/load/replay latency)

**Scenario:** Aaron's recorded fixture: 49k-building city, 2,644 ticks, 110 player placements. Full journal: ~2,644 entries. Sparse log: ~110 entries. Measure:
- Serialization time: `JSON.stringify` of full vs sparse
- Deserialization time: `JSON.parse`
- Replay time: `replayFromGenesis` (full) vs `replayFromGenesisFromSparseLog` (sparse)

**Check:**
- Sparse log JSON is ≤1% of full-journal size (saves localStorage quota and transmission time)
- Sparse-replay time ≤ 10% of full-replay time (dominated by player actions, not ticks)

Measured baseline from BUG-606 test: full replay of 900 ticks on 160-building fixture ~6.5s. Sparse replay with ~10 player actions expected <100ms.

**Mutation:** Replay synthesizes ticks on every millisecond boundary instead of tick-by-tick (coarse-grained synthesis). Converges to wrong state; consistency checks fail.

**False-pass:** Benchmark exists but uses a tiny fixture where sparse saves are negligible.

---

### AC-5 (cross-build rebuild uses sparse log, reports exact tick range replayed)

**Scenario:** Player's save is stamped with build v0.3.0-42. New build v0.3.0-50 has engine changes. Boot prompt offers rebuild. Player clicks "Rebuild on v0.3.0-50".

**Check:**
- `replayFromGenesisFromSparseLog` is called (not full-journal replay)
- Rebuild report shows: "Replayed 110 player actions from tick 0 to tick 2,644 under the new engine"
- The tick range is exact (derived from `sparseLog.endTick`)
- Before/after metrics compared (old build snapshot vs new build replay result)

**Mutation:** Report says "replayed from full journal" or tick count is wrong. Test goes red.

**False-pass:** Sparse log path exists but is bypassed; rebuild still uses full journal.

---

### AC-6 (sparse log survives rotation; genesis never drops)

**Scenario:** Player saves/loads the sparse log across a session. Check that:
- Sparse log is included in the `GameSave` serialization (journal field, or a new `sparseActionLog` field)
- `persistGameSave` and `loadGameSave` preserve the sparse log byte-identically
- Genesis replay uses the restored sparse log

**Mutation 1:** Sparse log is not persisted; only the full journal (savepoint tail) is kept. Genesis is missing on load.

**Mutation 2:** Sparse log is persisted but truncated (only the tail is saved, not the full history). Old actions are evicted.

**False-pass:** Serialization format exists but is never actually called on save; an in-memory full journal is used instead.

---

### AC-7 (determinism re-verified: same sparse log, different tick synthesis engines)

**Scenario:** Implement two independent tick-synthesis engines:
- Engine A (reference): a loop that dispenses `{ type: 'tick' }` and passes to reducer
- Engine B (optimized): possibly a pre-computed tick batch or alternative route

**Check:** Both engines, fed the same sparse action log, produce byte-identical final state and consistency reports.

**Mutation:** Engine B skips a tick in its batch (off-by-one in batch size). Final state diverges. Test goes red.

**False-pass:** Engines are identical (no real alternative), so the test is vacuous.

---

### AC-8 (fallback to full journal if sparse log corrupt/missing)

**Scenario:** Sparse log file corrupted (truncated JSON, invalid entry). Fallback: use the existing full `metropolis.journal` for replay via the old `replayFromGenesis` path.

**Check:**
- Boot detects sparse log parse error, logs MET-V9XX (registry code), gracefully falls back to full journal
- Player loses no data; replay succeeds (slower, but correct)
- Next save captures a fresh sparse log

**Mutation:** Sparse parse error crashes boot instead of falling back. Test fails catastrophically.

**False-pass:** No sparse log exists, so fallback is never exercised.

---

## Placeholder Constants / Config

| Constant | Value (TBD) | Notes |
|----------|-------------|-------|
| `SPARSE_ACTION_LOG_KEY` | `'metropolis.sparseActionLog'` | New localStorage key for the sparse log |
| `SPARSE_ACTION_LOG_CAP` | Unbounded or 100,000 | Max entries; if unbounded, thenBytes quota applies (localStorage limit). Aaron's ruling TBD. |

---

## Files Expected to Change

- **`webconsole/src/sim/journal.ts`** — define `SparseActionLog` interface, separate persist/load paths for full vs sparse
- **`webconsole/src/sim/genesisReplay.ts`** — add `replayFromGenesisFromSparseLog()`, keep existing `replayFromGenesis()` for compat
- **`webconsole/src/sim/types.ts`** — extend `GameSave` interface with optional `sparseActionLog`
- **`webconsole/src/sim/store.tsx`** — dispatch sparse-log writes on player actions, pass to load/save flow
- **`webconsole/test/harness*.test.mjs`** — add sparse-replay tests, AC-1 through AC-8

---

## Out of Scope

- **Full-journal autosave path:** Existing snapshot+tail-journal logic for same-build resume remains unchanged. No behavioral change for the common case (player plays, hits reload, same build runs).
- **IndexedDB mirroring:** Sparse log mirrors to IndexedDB alongside full journal (FEAT-2326609780); specific mirror strategy TBD with Aaron (same bytes, or re-encode for compressed storage).
- **Compaction/export:** Very-long-game log compaction (hard-reset-replay brief inc3) deferred.
- **Go engine integration:** Webconsole-only for now. Go engine has its own determinism/WAL story.

---

## Open Questions for Aaron

1. **Sparse log retention:** Unbounded (grow until localStorage quota), or a fixed-size cap (e.g., 100,000 entries)? If capped, what happens when a >100,000-action game is played — do we evict genesis or keep the full journal as a fallback?
2. **Sparse log default:** On new cities, do we start with both full journal AND sparse log, or only sparse log? Guidance on when to prefer one over the other.
3. **Export/recovery:** If a player's sparse log is corrupted and unrecoverable, do we offer an export path to download the full fallback journal for manual recovery, or is fallback-to-full-journal sufficient?
4. **Optimization detail:** Tick-synthesis batch size or pre-computed tick representation? (Simple loop is safest; pre-computation risks divergence. Recommend keeping simple unless profiling shows tick synthesis itself is a bottleneck, which is unlikely at <1ms per synthetic tick.)

---

*Incremental specification for FEAT-1972079934 sparse-action-log + tick synthesis. References hard-reset-replay brief §4.1–4.2. Awaiting Aaron's answers on questions and sizing rules before dev dispatch.*
