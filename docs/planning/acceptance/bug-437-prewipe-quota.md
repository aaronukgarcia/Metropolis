# BUG-437: GR#27 preWipeArchive QuotaExceeded blocks Start Over

**Bug:** A city whose full debug JSON cannot fit in `localStorage` throws on
`setItem('metropolis.preWipeArchive')`. Start Over abort-captures (correct) and
never wipes (correct under GR#27) — but the player is stuck: there is no compact
payload and no durable fallback, so capture can never succeed.

**Mkey:** BUG-437

**Relates:** BUG-420 (done — fail-closed capture exists), BUG-436 (rebuild
`persistSavepoint` return ignored), GR#27 (inviolable), northstar waypoint 2/3.

## Evidence (why this is P1)

Aaron dogfood, Start Over on a running city:

```
[reset-abort] Start Over aborted — pre-wipe debug capture failed: Failed to
execute 'setItem' on 'Storage': Setting the value of 'metropolis.preWipeArchive'
exceeded the quota.. State left intact.
heap: tick 2,039 · funds 18,804,722 · pop 0 · speed 1
```

Cause: `captureBeforeWipe` stringifies the **full** `DebugJson` (including
`runConsistencyChecks` O(buildings) colour/spec/position `ok: true` rows) into
one ring under `PREWIPE_ARCHIVE_KEY`. `attemptWipe` then throws; `store.tsx`
records `type: 'reset-abort'` and does not dispatch `{type:'reset'}`. GR#27 held.
The gap is that nothing then **makes capture succeed** — no compact form, no
IndexedDB / auto-download fallback — so Start Over is permanently blocked on any
city that exceeds the ~5MB `localStorage` quota.

**Forbidden fix:** skip capture on quota and wipe anyway. GR#27 is inviolable.

## Design

- **Compact first.** The pre-wipe **archive** payload is a compact snapshot, not
  the live debug-screen dump. Compact **must not** include per-building
  consistency `ok: true` rows (`colour.${id}.defined`, `spec.${id}.exists`,
  `building.${id}.position`, `tier.${id}.valid`, and any other O(buildings)
  ok-rows from `runConsistencyChecks`). Consistency in the archive is
  failures-only, or the `checks` array is omitted when `failures === 0`.
  Comparison spine **must** remain: tick, funds, population, building counts
  (`debug.sim.tick` / `debug.sim.funds` / `debug.sim.population` /
  `debug.buildings.count`, plus the envelope `tick`). Further elision (pretty
  indent, `perfHud`, full `buildings.list`, `roadConnectivity` tile lists) is
  allowed **if and only if** that spine survives.
- **Durable fallback.** If even the compact ring cannot be written to
  `localStorage` (QuotaExceeded / 5MB cap), persist **this** capture durably via
  IndexedDB **or** auto-download of the snapshot file, then — and only then —
  run the wipe. Start Over may become async; `applyWipe` still must not run
  until persist of **this** capture has succeeded.
- **Fail-closed remainder.** If `localStorage` throws **and** every fallback is
  absent or also throws, capture throws, `applyWipe` is never called, SimState
  is the same reference, and the existing `reset-abort` path fires. Same
  contract as BUG-420; this item does not weaken it.
- **Ring unchanged.** `PREWIPE_CAP` (exported, currently 10) still caps the
  archive; newest last; oldest dropped. Compact entries of a ~2k-building city
  must not blow a simulated 5MB `setItem` quota at that cap.
- **BUG-436 sibling (rebuild honesty).** `persistSavepoint` already returns
  `false` on QuotaExceeded (swallowed inside `replay.ts` — that is correct for
  fail-open autosave). `store.tsx` `onRebuild` currently ignores that boolean
  and `setRebuildPhase('report')` anyway. If persist returns `false`, do **not**
  claim rebuild complete; `recordError`; leave phase honest (`stalled` or back
  to `prompt` — not `'report'`).

Live debug.json download (FEAT-1972079886) may stay full. Compact is an
**archive** concern.

## Acceptance Criteria

- **AC-1 (quota city still captures, then Start Over proceeds).** A city whose
  **full** (uncompacted) debug JSON exceeds a simulated 5MB `setItem` quota
  still persists a durable pre-wipe snapshot, **then** Start Over wipes
  (post-wipe state equals `initialState()` on tick/funds/buildings). Check: a
  storage mock rejects any `setItem` whose value length is `> 5 * 1024 * 1024`;
  feed it a city whose `JSON.stringify(full DebugJson)` is over that cap;
  `attemptWipe` / `resetWithCapture` must write a readable snapshot (compact
  ring and/or fallback) **and** apply the reset. **Mutation:** with compact
  **and** fallback deleted (write the full dump to the capped `setItem`, no
  other persist), Start Over stays blocked — same-reference state, no wipe.
  **False-pass:** unlimited storage (today's `capture-before-wipe.test.mjs`
  happy path) or a wipe that runs after a failed persist.

- **AC-2 (compact archive has no per-building consistency.ok rows).** The
  persisted pre-wipe entry does not contain per-building `ok: true` consistency
  rows. Failures-only, or omit `checks`. Pre-wipe vs post-wipe comparison of
  tick, funds, population, and building counts is still possible from the
  archived spine (paths above). Check: archive a city with `buildings.length` on
  the order of Aaron's dogfood scale (~2k — assert against the constructed
  state's own `buildings.length`, GR#15, do not hardcode 2000 as the expected);
  walk `debug.consistency.checks` (if present) and assert every remaining row
  has `ok === false`; assert spine fields are present and equal the pre-wipe
  SimState. **False-pass:** asserting `failures === 0` without asserting the
  ok-rows are gone; or renaming ok-rows so a key-absence check is green.

- **AC-3 (ring still holds at PREWIPE_CAP without blowing quota).** Compact
  entries of a ~2k-building city, counted `PREWIPE_CAP` deep (read the exported
  cap; do not restate it as a magic expected), fit under the simulated 5MB
  `setItem` quota for `PREWIPE_ARCHIVE_KEY`. Newest last; oldest dropped; length
  equals `PREWIPE_CAP` after `PREWIPE_CAP + n` captures. Check: cap-capped
  storage; `PREWIPE_CAP + 3` compact captures of a ~2k-building city; final
  `setItem` value length `≤ 5 * 1024 * 1024`; archive length equals
  `PREWIPE_CAP`; first/last envelope ticks match the kept window. **False-pass:**
  the existing ring test on `initialState()` / a 1-hut city — that never stresses
  quota.

- **AC-4 (throwing storage, no fallback → fail-closed).** Capture failure with
  **no** working fallback still fail-closes: SimState same reference
  (funds/tick/buildings intact), wipe not applied, `store.tsx` reset path
  records `recordError(..., { type: 'reset-abort' })` and the existing
  "Start Over aborted — pre-wipe debug capture failed … State left intact"
  message. GR#27. Check: `throwingStorage()` with fallback disabled / absent;
  `resetWithCapture` returns the input reference; `attemptWipe`'s `applyWipe`
  is not called (spy). **False-pass:** a fallback that returns success without
  writing anything durable; or catching the throw and wiping anyway.

- **AC-5 (rebuild persistSavepoint false must not claim complete — BUG-436).**
  In `store.tsx` `onRebuild`, if `persistSavepoint(...)` returns `false`, do
  **not** `setRebuildPhase('report')`; `recordError` (rebuild persist failed;
  old snapshot remains the durable one). Phase stays `'stalled'` or returns to
  `'prompt'` — never `'report'`. Check: inject a `persistSavepoint` that returns
  `false`; assert phase ≠ `'report'` and that an error was recorded. **False-pass:**
  only re-testing `persistSavepoint` itself (already green in
  `journal.test.mjs`) without asserting the `store.tsx` wiring of the boolean.

- **AC-6 (determinism — compact capture does not enter SimState).** Compact /
  fallback capture adds no `Date.now` / `Math.random` into SimState. Envelope
  `capturedAtMs` / debug `generatedAtMs` may take an injected `nowMs` (existing
  BUG-420 contract). Check: clone state; call capture with injected `nowMs`;
  `Date.now` call count is 0 when `nowMs` is passed; `deepEqual` state vs clone;
  `JSON.stringify(state)` does not contain the injected timestamp.
  **False-pass:** asserting the envelope has no timestamp (the envelope is
  allowed to; SimState is not).

## Out of scope

- Skipping capture on quota so Start Over can proceed (GR#27 violation; instant
  reject).
- Weakening BUG-420: throwing storage with no fallback must still abort the wipe.
- The `beforeunload` path (`captureOnUnload`, BUG-427) — fail-open by design;
  this item is the in-app Start Over / `reset` fail-closed path.
- Shrinking the live debug-screen download (FEAT-1972079886) unless the coder
  reuses one compact helper; the human dump may stay full.
- Changing `runConsistencyChecks` itself for the live consistency panel — only
  what is **archived** must drop ok-rows.
- Raising / detecting the browser's real quota; tests simulate 5MB.
- Savepoint / journal quota policy beyond the rebuild-honesty wiring in AC-5.
- Go engine modules / `data/errors.json` (webconsole `reset-abort` envelope
  already exists).
