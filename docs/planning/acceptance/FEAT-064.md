# FEAT-064: Whole-State Checkpoints with Revert-and-Fork Lineage

**Acceptance Criteria for Player-Initiated Checkpoints, Revert Flow, and Bounded Fork Tree**

---

## Spec Authority: Aaron's 2026-08-12 Ruling (Verbatim)

> "BOUNDED FORK TREE: reverting to checkpoint X branches the timeline; the N most recent abandoned branches remain loadable, older branches auto-prune (N is a balance-regime number -- placeholder + proposal + Aaron approval). Save UI shows a small lineage tree. Determinism note stands: replay harness verifies fork integrity; checkpoints double as dev-mode debugging artifacts (FEAT-065 feedback can reference them)."

---

## Terminology

- **Checkpoint:** A whole-state savepoint created by the player, labelled, with an immutable parent pointer tracking the fork tree's shape. A checkpoint is a **savepoint with metadata**, not a parallel save system.
- **Current Lineage:** The active lineageId at play time; each checkpoint fork inherits a new unique lineageId.
- **Save Sequence (saveSeq):** The monotonic per-lineage counter already committed in the lineageId P0 (BUG-687 / FEAT-2326609780 landed 2026-09-05). Used to order savepoints within a lineage.
- **Revert:** Player-initiated action to load a checkpoint, making it the current lineage and the abandoned timeline a new fork branch.
- **Fork Tree:** The data structure tracking all checkpoints and their lineage relationships; bounded to N most-recent abandoned branches; older branches auto-prune.
- **Genesis Replay:** The deterministic rebuild flow (FEAT-1972079897, hard-reset-replay) that replays the journal from a checkpoint to reproduce it byte-identically.

---

## (A) Creating a Checkpoint

**AC-1: Checkpoint Creation via Player UI**
- When the player invokes "Save Checkpoint" from the UI, the game captures a whole-state savepoint at that tick.
- The checkpoint is **created as a savepoint** (reusing the existing savepoint schema: `Savepoint` interface from replay.ts, including `lineageId`, `saveSeq`, `snapshot`, `journalTail`, `buildVersion`, `camera`).
- A player-facing **label** (string, user-typed, max 255 chars, trimmed, unique within the same lineage) is persisted alongside the savepoint metadata in IDB (not in localStorage).
- The label is stored in a new IDB collection `checkpoints` with schema: `{ checkpointId, lineageId, label, parentLineageId, parentCheckpointId, savedAt, snapshotTick, saveSeq, isPruned }`.

**AC-2: Deterministic Checkpoint ID**
- The checkpoint ID is a **deterministic hash** derived from `(lineageId, saveSeq)`, never `Date.now()` or a random UUID.
- Formula: `checkpointId = SHA-256(lineageId + ':' + saveSeq.toString())` truncated to 16 hex characters.
- This ensures the same checkpoint is always retrievable by the same ID across boots and mirrors (IDB + exports), determinism-verifiable.

**AC-3: Parent Pointer and Fork Tree Edges**
- Every checkpoint carries a `parentLineageId` and `parentCheckpointId`, recording the lineage and checkpoint it was forked from (or `null` if created on the current lineage without a prior revert).
- The tuple `(lineageId, parentLineageId, parentCheckpointId)` encodes the fork tree's shape; querying checkpoints grouped by lineageId yields all branches.

**AC-4: Automatic Checkpoint on Autosave**
- The existing autosave (every 100 ticks, configurable) **does NOT automatically create a checkpoint**; it remains a transient savepoint.
- Player-initiated saves only. Autosave continues as a lifecycle-less savepoint (no label, no lineage edge tracking).

**AC-5: Journal Tail at Checkpoint Time**
- The checkpoint's `journalTail` is captured at the moment of checkpoint creation, including all actions up to that tick.
- On recovery, the journal is recoverable to the exact state at checkpoint time (no actions added, none lost).

---

## (B) Reverting to a Checkpoint

**AC-6: Player Selects a Checkpoint to Restore**
- A UI control (name: AARON DECISION: existing tab extension or new "Checkpoints" sidebar? "Lineage" tab?) displays the fork tree: checkpoint labels grouped by lineageId, with branch depth and auto-prune markers.
- Clicking a checkpoint entry queues it for load.

**AC-7: GR#27 Fail-Closed Capture Before Wipe**
- **Before** reverting (loading a checkpoint), the **current city state must be captured** to the pre-wipe archive (existing mechanism: `captureBeforeWipe.ts` / `metropolis.preWipeArchive`), fail-closed: **if the capture fails, the revert is blocked and surfaced as an error (MET-???)**. The player is told why their city wasn't saved (quota, corruption, etc.), not silently abandoned.
- The capture includes: full sim state, journal, buildVersion, lineageId, saveSeq, current checkpoint label (if on one), and a timestamp.
- Test: a revert with a failed pre-wipe capture must NOT proceed; the current city remains loaded.

**AC-8: Lineage Fork on Revert**
- When a checkpoint is loaded, a new unique `lineageId` is minted (UUID or the same deterministic scheme as checkpoints).
- The OLD lineage remains in IDB, tagged with the checkpoint it was abandoned from: the old savepoint slots are NOT deleted, they are frozen as a historical fork.
- The NEW lineage's saveSeq counter starts at the checkpoint's saveSeq value (so the first autosave after revert increments to saveSeq + 1), preserving monotonicity within the new branch.
- `writeCurrentLineageId()` is called to set the new lineage as current (no other lineage is touched).

**AC-9: Abandoned Lineage Becomes a Fork Branch**
- The abandoned lineage is recorded as a fork in the checkpoints IDB table: a new row with `{ lineageId: oldLineageId, checkpointId: checkpoint_that_was_loaded, isPruned: false }`.
- Subsequent autosaves on the new lineage do NOT touch the old lineage's slots (localStorage rotation continues on `metropolis.savepoint.0..3` key prefixes scoped by lineageId).

**AC-10: Savepoint Slot Namespacing**
- localStorage savepoint keys are namespaced by lineageId: `metropolis.savepoint.<lineageId>.<slot>` (or a derived key that incorporates lineageId without collisions).
- Existing: savepoint keys are `metropolis.savepoint.0..3`. This AC requires slots to be lineage-aware so concurrent lineages never overwrite each other's slots.
- **Note:** the P0 fix (BUG-687) already carries `lineageId` in the Savepoint struct; this AC requires the **key derivation** to reflect it (AARON DECISION: namespace scheme — `<prefix>.<lineageId>.<slot>` vs. a hash key per lineageId vs. IDB-only scoping?).

**AC-11: Fork Journal Starts at Checkpoint**
- When a fork lineage is first played (the player makes an action after revert), the journal starts empty or contains only actions from the checkpoint onward (no actions from the old lineage).
- The first persist on the new lineage records the checkpoint's journal tail as the foundation, and new actions append to it.
- **BLOCKED (ASM-442/ASM-470):** Continuous fork log (every action on a fork is recorded for instant replay without storing the full journal at the checkpoint) is deferred until `harness.replay.Recorder` is refactored to support incremental flush (currently buffers in-memory with no checkpoint mode). AC-11 does NOT implement continuous fork logging; it implements the simpler start-at-checkpoint case. Continuous logging is filed as a separate P2 feature.

**AC-12: Genesis Replay Verifies Fork Byte-Identity**
- When a fork lineage is loaded after a reload, the genesis-replay flow (hard-reset-replay, FEAT-1972079897) replays the journal from the checkpoint snapshot.
- The replayed state MUST match the pre-save state byte-identically (determinism gate).
- The BUG-436 lineage gate (store.tsx's `persistSavepointForced`) is updated to accept a fork's first persist (the new lineage's first autosave after revert) as the new authority, overriding any staleness check. **Note:** AC-11/12 coordination: the first persist after revert MUST succeed; if it fails, the fork is lost (a P1 bug if a quota condition eats a fork). Test both the success and failure (quota) paths.

---

## (C) Fork Tree Bounds and Eviction

**AC-13: Bounded Fork Tree (N Most Recent)**
- The fork tree is queried by lineageId; checkpoints are ordered by their savedAt (wall-clock, stored in IDB) or by creationIndex (a monotonic counter per fork event).
- **Eviction policy:** the N most recent abandoned lineages remain queryable and loadable. Older lineages' savepoint slots and checkpoint records are marked `isPruned: true` in the IDB checkpoints table.
- **Placeholder:** N = 10 (balance-regime number, subject to Aaron's row-by-row approval in the design review; not a junior-invented constant).
- **AARON DECISION:** Eviction trigger — on every checkpoint creation, after every revert, or on-demand (e.g. File → Cleanup Old Forks)? Storage quota as a hard limit (N is a soft cap, evict to stay under quota)?

**AC-14: Pruned Lineage Handling**
- A savepoint slot belonging to a pruned lineage is NOT deleted from localStorage or IDB immediately; it is marked `isPruned: true` in the checkpoints metadata table.
- Load/restore flows check the `isPruned` flag: attempting to load a pruned checkpoint returns an error (MET-???: "This fork was automatically pruned to keep the tree manageable. Check your export backups.").
- **Note:** pruning is metadata-only, not storage-destructive, so a player can recover a pruned fork from an exported save/backup (AC-16 export includes isPruned lineages).

**AC-15: Fork Tree UI Display**
- The save/load UI displays a **small lineage tree** (visual or text-based): checkpoints grouped by lineageId, with branch depth, labels, and timestamps.
- Current lineage is visually distinguished (bold, icon, etc.).
- Pruned branches are marked as "archived" or "pruned," non-interactive.
- **Example layout (text):**
  ```
  Current: MyCity (lineage: abc123)
    └─ Checkpoint A (100 ticks) [CURRENT]
    └─ Checkpoint B (200 ticks)
    
  Recent Forks:
    1. Fork at Checkpoint B (lineage: def456)
       └─ Checkpoint C (320 ticks)
    
    2. Fork at Checkpoint A (lineage: ghi789) [ARCHIVED]
       └─ Checkpoint D (150 ticks)
  ```
- **Tab/component:** AARON DECISION: extend existing "Save & Load" tab (storage.tsx or similar) with a lineage picker tree, or new "Checkpoints" sidebar in the HUD?

**AC-16: Export/Import Preserves Fork Tree**
- File → Export City exports the current lineage's latest savepoint + full checkpoint tree metadata (all lineageIds, checkpointIds, labels, parentage, isPruned status).
- Export format: a new optional section in the GameSave JSON schema: `"forkTree": [ { lineageId, checkpoints: [...] } ]`.
- On import (File → Open), the fork tree is restored: IDB is populated with all checkpoints and their lineageId mappings.
- Pruned checkpoints are imported as-is (marked pruned); reverting to them surfaces the AC-14 error.

---

## (D) Replay and Journal Semantics

**AC-17: Journal Recovery from Checkpoint**
- After reverting to a checkpoint, the journal shown in the Debug panel is the checkpoint's journal tail (actions from snapshot tick onward).
- New actions on the fork append to the journal; the pre-fork journal is not accessible from the new lineage's UI.
- (Dev mode artifact: the pre-wipe archive retains the old lineage's full journal for debugging / FEAT-065 feedback.)

**AC-18: Byte-Identical Genesis Replay on Fork Load**
- Determinism test: load a checkpoint on fork A, make 10 actions, capture the state.
- Reload the tab; boot restores fork A's lineage, plays the journal, and reaches byte-identical state (same buildings, money, citizens, tick, RNG state).
- Compare output using the existing `harness.replay.CompareResult` diffing (MOD-013 harness.replay).

**AC-19: Savepoint Gate Accepts Fork First Persist**
- The existing lineage gate (store.tsx, BUG-436 / `persistSavepointForced`) refuses overwrites of "fresher" saves by comparing `(lineageId, saveSeq)`.
- On a fork, the first persist after revert has `lineageId: newForkId, saveSeq: startValue` (inherited from the checkpoint).
- The gate is updated: **if the new save is the first persist on this lineageId (no existing slots for this lineage), accept it unconditionally**, treating it as the new authority (not a stale overwrite of an older lineage's data).
- Test: fork at tick-500, first autosave lands at tick-510 — must succeed even if an older lineage has tick-1000 savepoints. The new lineage's slots accept the write; old lineage slots are untouched.

---

## (E) Storage: IndexedDB, localStorage, and Old Saves

**AC-20: Checkpoint Metadata in IndexedDB**
- A new IDB object store `checkpoints` holds all checkpoint metadata (labels, parent pointers, pruning status, fork tree edges).
- Schema: `{ checkpointId (primary key), lineageId, label, parentLineageId, parentCheckpointId, savedAt, snapshotTick, saveSeq, isPruned, createdAt }`.
- Mirrored to localStorage (same bootstrap-fast-path pattern as savepoints): `metropolis.checkpoints` holds a JSON array of current-lineage's checkpoints (for instant load UI on boot).
- On boot, the IDB checkpoints table is read async (after mount, behind the existing RebuildPrompt overlay if needed), populating the fork-tree picker.

**AC-21: Savepoint Slot Scoping by Lineage**
- localStorage `metropolis.savepoint.<lineageId>.<slot>` namespaces slots by lineageId.
- Existing saves without a lineageId are treated as belonging to `LEGACY_LINEAGE_ID` (existing behavior, BUG-687 fix).
- When listing available savepoints for boot, only the current lineageId's slots are restored (no cross-lineage confusion).

**AC-22: Backward Tolerance — Old Saves Without Checkpoints**
- A save file (File → Open) that has no `forkTree` metadata loads unchanged: the save is restored as a new single-lineage session (no parent lineage, no fork edges).
- The checkpoint tree is initialized empty for that lineage; the first player-initiated checkpoint on that loaded save becomes a new root checkpoint.
- No migration is forced; old saves remain old saves (GR#3 SSOT: savepoints are the source of truth, checkpoints are a feature layer on top).

---

## (F) Testing Plan (Directional, Determinism-Focused)

**AC-23: Unit Tests — Checkpoint Metadata**
- Test checkpoint creation: label, ID derivation, parent pointer, saveSeq inheritance.
- Test fork tree eviction: create N+5 forks, verify oldest N are pruned, N newest are loadable.
- Test checkpoint export/import: export a 3-fork tree, import it, verify all checkpoints + lineageIds + labels recovered.

**AC-24: Integration Test — Revert and Recover**
- Create a checkpoint at tick-100.
- Play to tick-300 (player builds, money changes, citizens migrate).
- Revert to the checkpoint.
- Verify: pre-revert state is captured in pre-wipe archive; new lineageId is assigned; first autosave lands on new lineage.
- Reload tab; verify fork A's lineage is restored, journal replays, final state byte-identical.

**AC-25: Determinism Test — Replay Bytes Match**
- Genesis-replay the fork from the checkpoint snapshot. Compare output to the expected state using MOD-013's `CompareResult` diffing (zero divergences).
- Run across city sizes 10k / 100k / 1M citizens to verify determinism holds at scale.

**AC-26: GR#27 Test — Failed Capture Blocks Revert**
- Mock a failed pre-wipe archive write (quota error, storage unavailable).
- Player attempts revert to a checkpoint.
- Verify: the revert is blocked; the error is surfaced (MET-??? code + user message); current city remains loaded; journal not cleared.

**AC-27: Savepoint Gate Test — Fork First Persist**
- Create a checkpoint on lineageA at tick-500.
- Revert to create lineageB.
- Simulate an autosave attempt on lineageB at tick-510 into `metropolis.savepoint.<lineageB>.0`.
- Verify: write succeeds (gate does not refuse it as stale, even if lineageA has tick-1000 savepoints in slot.1).
- Verify: lineageA slots remain untouched (no overwrite).

**AC-28: Wall-Clock Time Test — Checkpoints Across Boots**
- Create checkpoint A at 10:00 AM.
- Play to 10:30 AM, create checkpoint B.
- Revert to checkpoint A.
- Reload tab, verify checkpoint A is restored (not B), without relying on wall-clock ordering (saveSeq is the authority).

---

## Summary of Implementation Sites

- **webconsole/src/sim/replay.ts:** Extend `Savepoint` interface (checkpoint metadata if needed; most metadata lives in IDB, not in the savepoint struct). `savepointKey` derivation if lineageId namespacing is implemented here.
- **webconsole/src/sim/saveStore.ts:** New IDB object store `checkpoints`; checkpoint metadata lifecycle.
- **webconsole/src/sim/store.tsx:** Extend `persistSavepointForced` gate to accept fork-first-persist; new `revertToCheckpoint()` handler; pre-wipe capture gate (fail-closed).
- **webconsole/src/components/SaveLoadUI.tsx** (or similar): Fork-tree picker UI (existing tab extension or new sidebar).
- **webconsole/src/sim/gamesave.ts:** Extend `GameSave` schema with `forkTree` section (export/import path).
- **Go engine side:** No new Go code required (not wired; checkpoints are TypeScript-only in the webconsole). If the feature later requires Go-side checkpoint snapshots (not planned for Baseline One), a new int.serializer participant would register (separate feature).

---

## GR#25 Edge Conformance (Verbatim)

FEAT-064 depends on and consumes edges already registered in code.json (no new edges added; GR#3 SSOT: checkpoints are a feature layer on the existing savepoint machinery):

**Outbound edges (feat.checkpoint → ...):**
1. `feat.saveux` (GUID inbound: `20c48d83-347b-434c-902f-b61aeef16711`) — checkpoint creation and fork semantics build on the existing `Savepoint` interface, `Manager` (save/load), and `Participant` model (GR#20 contract-first).
2. `harness.replay` (GUID inbound: `37a8ee7f-8d84-44f3-b52b-4aac310c688c`) — fork recovery via genesis replay (determinism verification, `CompareResult` diffing, journal replay).
3. `int.serializer` (GUID inbound: `2ed08d03-985d-4f84-941b-12aa7d3285f2`) — savepoint bundle format (snapshot + journal + metadata) for export/import.
4. `foundation.errors` (GUID inbound: `d230f06c-f605-4e79-a80f-61638344e6a8`) — MET-E/MET-V registry-sourced errors (AC-7, AC-14 error surfaces).
5. `foundation.num` (GUID inbound: `74ff5b3b-bfc6-4376-b461-267f4467a39f`) — numeric types (saveSeq counter, lineageId hashing).

**Inbound edge (foundation.integration → feat.checkpoint):** The composition root wires checkpoints into the save/load/revert flow on demand.

**TypeScript surface has no code.json edges** (AC implementers live in webconsole TypeScript, not Go modules). The Go engine's checkpoint layer (if future post-Baseline-One work exists) would register a new edge then.

---

## Aaron Decisions Required

1. **AC-10/AC-21 Savepoint Key Namespacing:** How should slot keys incorporate lineageId to avoid collisions? Options:
   - `metropolis.savepoint.<lineageId>.<slot>` (concat)
   - `metropolis.savepoint.<hash(lineageId)>` (hash-based, shorter keys)
   - Scoped entirely to IDB (localStorage holds only current lineage; fork slots live in IDB only)

2. **AC-13 Eviction Policy:** Trigger and hard limit:
   - On every checkpoint creation, prune if N+1 forks exist?
   - On-demand (File → Cleanup)?
   - Storage quota as a hard limit (auto-prune when quota would exceed X MB)?
   - N value: placeholder 10, or measured from Aaron's row-by-row balance review?

3. **AC-15 UI Placement:** Where does the fork-tree picker live?
   - Extend existing "Save & Load" tab with a lineage selector tree?
   - New "Checkpoints" or "Lineage" sidebar in the HUD?
   - Nested menu under File → Load?

4. **AC-11/AC-12 Continuous Fork Log:** Post-Baseline-One feature (P2, blocked on harness.replay.Recorder refactor). Clarify: should AC-11/12 include a continuous log, or defer that to the next increment after ASM-442/470 close?

---

## Traceability to Northstar

- **Waypoint 1 (Watchable Baseline One):** Checkpoints enable developer / player save/restore workflows and dev-mode debugging (FEAT-065) without blocking the core loop. Support gameplay resilience.
- **Waypoint 2 (Dogfood lane):** Checkpoints allow rapid iteration: build, checkpoint, revert, try another path, determinism-verified.
- **Waypoint 3 (Engine convergence):** Fork trees survive upgrades; hard-reset replay on each fork validates determinism.

---

## Acceptance Criteria Summary

**Grouped:** (a) Creating checkpoints [AC-1..5], (b) Reverting [AC-6..12], (c) Fork bounds [AC-13..16], (d) Replay [AC-17..19], (e) Storage [AC-20..22], (f) Tests [AC-23..28].

**Total ACs:** 28 numbered acceptance criteria.

**Existing Machinery Relied On:**
- `Savepoint` interface (replay.ts): lineageId, saveSeq, snapshot, journalTail, buildVersion, camera.
- `persistSavepoint` / `persistSavepointForced` (store.tsx, BUG-436 verdict): gate logic and re-stamping.
- `genesis-replay` (hard-reset-replay, FEAT-1972079897): byte-identical recovery from snapshot + journal.
- `saveStore.ts` / IndexedDB: async durable storage layer (IDB with localStorage bootstrap).
- `CompareResult` (MOD-013, harness.replay): determinism diffing.

**Key Missing Decisions (Awaiting Aaron):**
1. Savepoint key namespacing scheme (AC-10/21).
2. Fork eviction policy and N value (AC-13).
3. UI placement for fork-tree picker (AC-15).
4. Scope of continuous fork logging (AC-11/12 vs. post-Baseline-One feature).

