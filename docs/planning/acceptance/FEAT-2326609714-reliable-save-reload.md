# FEAT-2326609714: Reliable Save & Reload

**Baseline One Goal #6** — "I can save and reload my city" with confidence. This feature consolidates 5 open bugs (BUG-439, BUG-445, BUG-446, BUG-469, FEAT-2326609714 itself) and implements save-verify round-trips so every save is proven to restore correctly before reporting "saved".

## What Reliable Means

A reliable save/reload system must ensure:

1. **Round-trip determinism** — a saved city reloads to the SAME city. Replaying the saved journal against the snapshot reproduces the pre-save state exactly (byte-identical tick, population, funds, buildings, etc.). BUG-439 must be impossible: rebuilt cities (post-version-change) must replay their full action history, never an empty journal.

2. **No silent destruction** — a save operation never silently overwrites a different city's save. Named-save collisions (Save As / rename) require explicit user confirmation before overwriting (BUG-445).

3. **Autosave protection** — autosaves are protected against concurrent-load races, tab conflicts, and browser-reload wipes. No save loses more than the autosave interval (10 minutes in prod, 30 seconds per spec interval). A reload does not overwrite the autosave; a second browser tab loading a named save does not corrupt it (BUG-469).

4. **Verify before report** — every save (autosave, manual save, or named save) writes the savepoint to storage, then loads it back and runs consistency checks BEFORE reporting success. A save that cannot be verified is reported as failed, not silent (FEAT-2326609714).

5. **Malformed input rejection** — `parseGameSave()` rejects corrupt/garbage input with a registry-sourced error (GR#1/GR#7), never accepts incomplete or malformed buildings/entries. Every validation failure is logged with a trapped, typed error code (BUG-446).

## Save-Slot Model

The webconsole implements TWO tiers of save slots:

**Session Autosaves (rolling, in-memory recovery):**
- Automatic savepoints every 10 minutes (AUTOSAVE_INTERVAL_MS, tunable).
- Persisted to localStorage as a ring buffer of the latest **N autosave slots** (SAVEPOINT_CAP, currently 1; Aaron ruling: increase to 3+ to protect against reload wipes and tab races).
- Fail-safe: autosave errors do NOT block the game loop; they set a quiet error flag for the UI.

**Named Saves (explicit, cross-session storage):**
- Player-named save slots (Save, Save As, Rename).
- Persisted as indexed named entries in localStorage (metadata index + compressed blob per slug).
- Max 2 named saves kept at a time (NAMED_SAVE_BLOB_CAP); older saves auto-purge.
- Browser "From file" saves download a complete GameSave JSON (format "metropolis-save/1") for offline archival.

## Acceptance Criteria

### AC-1: Save-verify round trip (FEAT-2326609714)
Every autosave (via `persistSavepoint()` called from the autosave interval), manual save (via `saveGame()`), and named save (via `saveGameAs()`) MUST:
1. Serialize the current SimState to a Savepoint via `createSavepoint(state, tail, now, buildVersion, camera)`.
2. Persist to localStorage (or named-save slot).
3. Read the persisted savepoint back from storage.
4. Run `runConsistencyChecks()` on the snapshot to verify it is structurally valid (building array, counts, etc.).
5. Restore the snapshot via `restoreLatestSnapshotOrGenesis()` or equivalent load logic and run consistency checks again.
6. Only if both pre-save and post-restore consistency checks pass: report the save as successful.
7. If verification fails at ANY step, log a registry-sourced error (MET-Sxxx), update the UI error flag, and the operation report SHALL be "failed", not "succeeded silently".

**Grounded in:** `persistSavepoint(storage, savepoint)` → `readNamedSave(storage, slug)` → `runConsistencyChecks(state)` (from consistency.ts).

**Catches:** FEAT-2326609714 (the round-trip verify) + FEAT-1972079936 savepoint-aware restore flow + BUG-439 (if the journal is empty after restore, consistency will fail or the rebuild will not replay correctly).

---

### AC-2: Autosave slot history (BUG-469)
At least **3 autosave slots** MUST be maintained by the autosave timer:
1. Each autosave interval (10 min prod), call `createSavepoint(state, journalTail(...), now, buildVersion, camera)`.
2. Persist to the next slot (round-robin across SAVEPOINT_CAP slots) via `persistSavepoint(storage, savepoint)`.
3. Every autosave attempt increments the active slot; slots rotate so the 3 most recent are always available.
4. Autosave errors set the error flag but do NOT wipe prior savepoints.
5. On page reload, the bootstrap restores the most-recent autosave via `readAllSavepoints()` → `mostRecentSavepoint()` → `restoreLatestSnapshotOrGenesis()`.
6. A second browser tab loading via `loadNamed(slug)` does NOT modify the autosave slot(s); it loads the named save independently.

**Grounded in:** AUTOSAVE_INTERVAL_MS interval → `createSavepoint()` → `persistSavepoint()` → SAVEPOINT_CAP (currently 1, MUST increase).

**Catches:** BUG-469 (single autosave slot = data loss on reload; history of 3 prevents 1-tab races and reload wipes).

---

### AC-3: No JSON injection into named slots (BUG-446)
`parseGameSave(text: string)` MUST:
1. Validate that the parsed JSON root is an object (not array, null, or scalar).
2. Check format string === "metropolis-save/1".
3. Verify name, savedAt, buildVersion are non-empty strings.
4. Verify savepoint object exists and contains snapshot.
5. Verify snapshot contains tick (number) and buildings (array).
6. Verify journal contains entries (array).
7. For each entry in snapshot.buildings, validate that it is a valid Building object:
   - MUST have an id (uuid-ish string), spec (string referencing a known Spec), and position (x, y coords).
   - MUST NOT have unrecognized/garbage fields that will break the reducer.
   - BA ASSUMPTION: the exact Building validation schema is TBD; until then, a JSON object with id/spec/position counts as structurally valid.
8. If ANY validation fails, return { ok: false, reason: "<human-readable error message>" }.
9. The reason string MUST be drawn from a registry-sourced error code (GR#7) — not a bare message.

**Grounded in:** `parseGameSave(text)` → `ParseGameSaveResult` return type.

**Catches:** BUG-446 (zero test coverage for malformed input; accept garbage elements without validation).

---

### AC-4: Autosave survives reload wipes (BUG-469)
When the user refreshes the page or closes/reopens the tab:
1. At unload time, `captureOnUnload(getState, appVersion, storage)` runs best-effort to archive the pre-wipe state (GR#27).
2. At boot, `readAllSavepoints(storage)` reads all SAVEPOINT_CAP savepoints.
3. `mostRecentSavepoint(savepoints)` selects the newest by savedAt timestamp.
4. Restore the selected savepoint via the normal consistency-verified restore flow (AC-1).
5. The restored state IS the city pre-reload; no data loss from the autosave interval.

**Grounded in:** `captureOnUnload()` (from captureBeforeWipe.ts) → `readAllSavepoints()` → `mostRecentSavepoint()` → restore with AC-1 verify.

**Catches:** BUG-469 (reload overwrites autosave; with multi-slot history, reload simply boots the most-recent slot, no overwrite).

---

### AC-5: Named-save collision confirmation (BUG-445)
When the user calls `saveGameAs(name)` or `renameCity(newName)`:
1. Compute the slug via `cityNameToSlug(displayCityName(name))`.
2. Call `readNamedSave(storage, slug)` to check if a save already exists at that slug.
3. If a save exists (not null):
   - Display a confirmation dialog: "A city named '[displayCityName(name)]' already exists. Overwrite it?" (yes / cancel).
   - Only if the user confirms yes: call `writeNamedSave(storage, newSave)`.
   - If the user cancels: abort and return control to the save-as / rename form without changing storage.
4. If no existing save at that slug: proceed directly to `writeNamedSave()`.
5. After successful write, the metadata index is updated and the old save (if overwritten) is removed via `storage.removeItem(slotKey(oldSlug))`.

**Grounded in:** `cityNameToSlug()` → `readNamedSave()` (check existence) → `writeNamedSave()` (line 95 already filters old slug, but has no UI confirmation loop).

**Catches:** BUG-445 (silent collision destroy; confirmation prevents accidental overwrites).

---

### AC-6: Rebuild with full journal replay (BUG-439)
When a saved city is loaded on a different build version:
1. User loads a save via `loadGame()` or `loadNamed()`.
2. `parseGameSave()` extracts the savepoint, including snapshot and journal.
3. `applyLoadedSave(save)` detects a version mismatch: `save.buildVersion !== currentBuildVersion()`.
4. A rebuild prompt offers: "This city was saved on version X. Rebuild to the current version Y?" (Rebuild / Cancel).
5. On Rebuild:
   - The snapshot is restored as the starting state.
   - The full journal (save.journal.entries, not the truncated journalTail) is replayed via `reducer(..., { type: 'journal-replay', entries })` or similar.
   - Every entry is deterministically re-executed under the new build rules, re-deriving the end state.
   - Consistency checks run before AND after replay (via AC-1 verify flow).
   - The rebuilt city is the result; no loss of actions.
6. On Cancel: the load is aborted, current city left intact.

**Grounded in:** `applyLoadedSave()` → rebuild decision logic (lines 761-771) → journal replay (entry point TBD; currently only journalTail is available, not the full journal).

**Catches:** BUG-439 (rebuild replays empty journal, pre-save state not recreated; AC-6 ensures the FULL journal is captured and replayed, not truncated).

**BA ASSUMPTION:** the current code saves an EMPTY journal (buildCurrentSave line 735 has journalTail: []) and loads an EMPTY journalTail (applyLoadedSave line 787). This means the full action history is lost at save time. AC-6 requires the full journal to be preserved. If the current save format cannot carry the full journal (storage quota), an alternative is to ensure the snapshot alone is byte-identical to the pre-save state (no journal needed), but then rebuilds cannot re-derive changes from new rules.

---

### AC-7: Autosave error reporting (BUG-469 + GR#1)
Every autosave failure (localStorage error, quota exceeded, corruption) MUST:
1. Catch the error in the autosave setInterval (line 368, store.tsx).
2. Set the autoSaveError flag so the UI can display a quiet warning (e.g., a small icon in the corner).
3. Log the error via `recordError(msg, { type: 'app', action: 'autosave' })` with a registry-sourced error code.
4. NOT crash the game loop, NOT block the next tick.
5. The error message MUST identify the cause (quota, corruption, etc.) so the user can troubleshoot.

**Grounded in:** autosave setInterval (line 368) → setAutoSaveError(flag) + recordError() + try/catch.

**Catches:** BUG-469 (silent autosave failures leave no trace; explicit error flag + logging makes failures visible).

---

### AC-8: Savepoint structure validation (BUG-446)
Every savepoint persisted by `persistSavepoint()` or `createSavepoint()` MUST have:
1. A valid Savepoint schema matching the TypeScript interface (savedAt, snapshotTick, snapshot, journalTail, optional buildVersion/camera).
2. A snapshot that passes `runConsistencyChecks()` before persistence.
3. A journalTail that is an array (possibly empty) of valid JournalEntry objects.
4. If `buildVersion` is present, it is a non-empty string.
5. If `camera` is present, it is a valid MapViewState object (not garbage data).

**Grounded in:** `Savepoint` interface (replay.ts) + `createSavepoint()` return + `persistSavepoint()` validation.

**Catches:** BUG-446 (accept garbage buildings without structure validation; schema enforcement ensures valid blobs only).

---

### AC-9: Named-save index consistency (BUG-445)
The named-save metadata index (NAMED_SAVES_INDEX_KEY) MUST:
1. Be read at the start of every `writeNamedSave()` and `readNamedSave()` operation.
2. Reflect the set of keys actually in storage (no dangling entries, no orphans).
3. Cap at NAMED_SAVE_BLOB_CAP entries (currently 2); older saves are removed.
4. Be updated AFTER the blob write succeeds, not before (no index pointing to a blob that was never written).
5. On index write failure, the blob write is considered failed and rolled back (safeSetItem semantics).

**Grounded in:** `writeNamedSave()` (line 75, namedsaves.ts) → read index → write blob → write index (fail-safe order).

**Catches:** BUG-445 (index corruption can point to destroyed saves or create orphans).

---

### AC-10: Consistency checks on load (AC-1 subset for load, BUG-439)
When restoring a savepoint after load:
1. `RestoreResult` is returned with { success: boolean, state?, reason?, replayed? }.
2. If the snapshot consistency check fails: return { success: false, reason: "Snapshot failed consistency..." }.
3. If the journal replay is required (version change) and consistency fails after replay: return { success: false, reason: "Replay failed consistency..." }.
4. If both checks pass: return { success: true, state: restored, replayed: count }.
5. The caller (applyLoadedSave) checks success and aborts the load on failure.

**Grounded in:** `RestoreResult` interface (replay.ts) + `runConsistencyChecks(state)` calls (lines 241, 261).

**Catches:** BUG-439 (if consistency fails, load is aborted instead of silently proceeding with corrupt state).

---

## Test Coverage Required

The following red tests MUST be added and kept passing:

### Test: Save/Load Round Trip
- **Scenario:** Save a city in state S at tick T with population P and funds F.
- **Check:** Load the save back, verify state is byte-identical to S (tick, pop, funds, building roster).
- **File:** webconsole/test/gamesave.test.ts (NEW).
- **Related AC:** AC-1, AC-6.

### Test: Garbage parseGameSave Rejection
- **Scenarios:**
  - Missing format field.
  - format !== "metropolis-save/1".
  - Missing name / savedAt / buildVersion.
  - Missing savepoint.snapshot.
  - Missing snapshot.buildings array.
  - Missing journal.entries array.
  - buildings array contains objects without id/spec/position.
- **Check:** `parseGameSave(text)` returns { ok: false, reason: "<error>" } for each; never throws; reason is non-empty.
- **File:** webconsole/test/gamesave.test.ts.
- **Related AC:** AC-3, AC-8.

### Test: Empty Journal After Load Regression (BUG-439)
- **Scenario:** Save a city with a 10-action journal tail, load it back, then rebuild with a version change.
- **Check:** The rebuilt state reflects all 10 actions, not an empty journal. (Requires AC-6 full-journal capture.)
- **File:** webconsole/test/replay.test.ts.
- **Related AC:** AC-6.

### Test: Named-Save Collision Confirmation
- **Scenario:** Two named saves both map to slug "mycity" via cityNameToSlug(). Attempt to Save As the second without confirmation.
- **Check:** `readNamedSave(storage, 'mycity')` returns a non-null save; UI displays a confirmation prompt; only after confirm is `writeNamedSave()` called.
- **File:** webconsole/test/namedsaves.test.ts or component integration test.
- **Related AC:** AC-5.

### Test: Autosave Multi-Slot Rotation
- **Scenario:** SAVEPOINT_CAP = 3. Fire 4 autosaves in sequence.
- **Check:** All 3 slots are populated (not just slot 0); the 4th save rotates to slot 0, overwriting the 1st (oldest).
- **File:** webconsole/test/replay.test.ts.
- **Related AC:** AC-2, AC-4.

### Test: Savepoint Persist Verify
- **Scenario:** Call `persistSavepoint(storage, sp)` and immediately `readAllSavepoints(storage)`.
- **Check:** The returned savepoint equals the persisted one (round-trip through localStorage + JSON.parse).
- **File:** webconsole/test/replay.test.ts.
- **Related AC:** AC-1.

---

## Assumptions for Aaron / Bev

1. **Journal capture on save:** The current `buildCurrentSave()` uses `journalTail: []` (empty). AC-6 requires the FULL journal to be captured so rebuilds can replay all actions. If storage quota is the concern, Aaron may rule to: (a) store the full journal and accept the quota hit, (b) ensure the snapshot is byte-identical to the pre-save state so no replay is needed, or (c) cap the journal age so old saves naturally lose deep history. **Decision required before AC-6 dev starts.**

2. **SAVEPOINT_CAP increase:** Current code has `SAVEPOINT_CAP = 1` (single autosave slot). AC-2 requires at least 3 to protect against reload wipes and tab races. **Aaron to confirm the slot count.**

3. **Collision-confirm UX:** AC-5 requires a dialog before overwriting a named save. The current code (namedsaves.ts line 95) silently filters and overwrites. **UX/Aaron to specify the confirm dialog text and flow.**

4. **Consistency check schema for buildings:** AC-3/AC-8 validate that buildings in a snapshot have id/spec/position. The exact validation schema (required/optional fields, type bounds, spec-catalogue lookup) is **TBD**; this AC treats any object with those three fields as valid pending spec details.

5. **Version stamp in savepoint:** BUG-439 rebuild requires knowing if a version change occurred. The savepoint includes buildVersion (optional, for backward tolerance). **Aaron to confirm every save stamps buildVersion, no legacy exceptions.**

6. **Autosave interval:** AUTOSAVE_INTERVAL_MS is currently 30 seconds (test value). Prod spec is 10 minutes. **Aaron to confirm the interval and whether it is tunable at boot.**

7. **Named-save cap:** NAMED_SAVE_BLOB_CAP is currently 2 (keep 2, auto-purge older). AC-9 assumes this cap; **Aaron may override.**

8. **Test coverage for load path:** AC-10 mentions consistency checks on restore, which already exist in the code (runConsistencyChecks). The new test gap is the round-trip save-verify (AC-1), garbage input rejection (AC-3), and rebuild replay (AC-6). **Bev to assign test file ownership.**

---

**Document grounded in real functions:**
- `parseGameSave()`, `buildGameSave()`, `gameSaveText()` — gamesave.ts
- `createSavepoint()`, `persistSavepoint()`, `readAllSavepoints()`, `mostRecentSavepoint()`, `RestoreResult` — replay.ts
- `buildCurrentSave()`, `applyLoadedSave()`, `saveGame()`, `saveGameAs()`, `loadGame()`, `loadNamed()` — store.tsx
- `writeNamedSave()`, `readNamedSave()`, `cityNameToSlug()`, `renameNamedSave()` — namedsaves.ts
- `captureBeforeWipe()`, `captureOnUnload()` — captureBeforeWipe.ts
- `runConsistencyChecks()` — consistency.ts
- `AUTOSAVE_INTERVAL_MS`, `SAVEPOINT_CAP`, `NAMED_SAVE_BLOB_CAP` — replay.ts, namedsaves.ts
