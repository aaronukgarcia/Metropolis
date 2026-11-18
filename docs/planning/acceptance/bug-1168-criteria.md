# BUG-1168 acceptance criteria: the Rebuild prompt overwrites the player's city before the player sees what it did

**BOW:** BUG-1168 (P1, open). **Written:** 2026-11-18, BA pass for the backlog comb (FEAT-9995385605).
**Sources read:** `node claude-bow.js show BUG-1168` (title only: no description, no comments), `BUG-1156` (done, all r1 to r5 comments), `webconsole/src/sim/store.tsx` (`onRebuild` 3341-3562, `onKeep` 3564, `onFresh` 3586, `onResume` 3592; boot restore 985-1010), `webconsole/src/components/RebuildPrompt.tsx`, `webconsole/src/sim/genesisReplay.ts` (`rebuildReport` 617).
**Existing criteria doc:** none. **Duplicate check:** BUG-1156 (done) fixed the lineage pointer and the Cities list. Its r3 comment shows it only stopped the prompt from replaying over a *scratch* city (`pendingRebuild null`). Nothing on main touches the prompt's write for an adopted or own-lineage city, and no code or test mentions BUG-1168 (`grep -rn BUG-1168` over src, test, docs). So this is not a duplicate and not yet fixed. It was last confirmed by the BUG-1156 r1 probe and its control run on 2026-11-01; that evidence is not in the repo and the comb could not re-run it.

## 1. What the player sees today

1. The player restores a city saved by an older build (tick 900, funds 9.99M). The boot shows the modal "New build detected" with Rebuild / Keep / Start fresh (`RebuildPrompt.tsx:200-260`).
2. The player picks **Rebuild**. `onRebuild` replays the **hot journal** from genesis (`store.tsx:3353`: `replayFromGenesisDefensiveChunked(hotJournalRef.current ?? journal)`). The journal is one shared store, not one per city. After an adoption, a hard reset, a journal cap roll-off or a scratch city, it does not describe this city (BUG-1156 r1 and r3 comments: "shared journal key hazard"). The replay can end at tick 1 with funds 1,500,000.
3. When the replay finishes, the code **writes the result over the city on disk before the report is shown**: `persistSavepointForced(window.localStorage, rebuiltSave)` at `store.tsx:3478`, with a saveSeq that supersedes every slot of the lineage (comment at 3454-3470: "a DELIBERATE replace-the-city boundary").
4. The report then shows old and new figures (`RebuildPrompt.tsx:318-345`, tick 900 to 1, funds 9.99M to 1.5M) with exactly one button: **Resume on <build>** (`:351`). There is no way back. The Keep and Start fresh buttons are gone in this phase. Reload also restores the replay, because the disk now holds it.

That is the data loss: the player sees the damage only after it is already the newest save, and the only offered action accepts it.

## 2. What the player should see

The replay result is a **proposal**. Nothing replaces the city on disk until the player has seen the old and new figures and chosen to accept. Cancelling or reloading before then leaves the old city exactly as it was. If the replay cannot reproduce the city (it ends far behind the saved tick), the player is told so in plain words, and the default action keeps the old city.

## 3. Acceptance criteria

All tests below are webconsole tests (`webconsole/test/*.test.mjs` or `.test.tsx`, run with the scoped runner on the files touched, per the project's test-scope contract). The fixture is one real city: tick 900, funds 9,990,000, saved under build A, running build B, with a journal that does **not** cover it (journal shorter than the saved tick: for example a journal holding only a genesis reset).

**AC-1. The prompt itself writes nothing.** Mount the store with a cross-build save. With the Rebuild prompt showing and no button pressed, every localStorage and IndexedDB key that holds the city is byte-identical to before the mount, apart from the keys boot already writes today (list them in the test).
- Expected: byte compare equal.
- Can-fail proof: on main today this passes (the prompt writes nothing before the click). Keep it as the base line; it goes red if a later change writes at mount.

**AC-2. Accepting Rebuild does not write until the player accepts the report.** Press Rebuild, let the replay finish, stop at the report phase. At that moment the saved city (all ring slots plus IndexedDB copy) is byte-identical to before the click, and a reload boots the old city at tick 900 with funds 9,990,000.
- Expected: reload at the report phase gives tick 900 / funds 9.99M, with the Rebuild prompt showing again.
- Can-fail proof: this is the regression test. On main it fails, because `persistSavepointForced` has already run at the report phase and a reload boots tick 1. Mutation: put the forced write back before `setRebuildPhase('report')`; the test goes red.

**AC-3. The report offers a way back.** In the report phase the player has two choices: accept the rebuilt city, or discard the rebuild and keep the old city. Choosing keep leaves the disk byte-identical to before the click (AC-2's baseline), clears the pending rebuild, and does not loop the prompt on the next boot.
- Expected: two buttons, `Resume on <build>` and a keep or discard button; after keep, reload boots tick 900 without the prompt (the keep path re-stamps the build, as `onKeep` does today).
- Can-fail proof: remove the second button; the test cannot find it and fails.

**AC-4. Accepting writes the rebuilt city exactly once, and it survives reload.** Press Rebuild, then Resume. Reload boots the rebuilt city (tick and funds equal to the report's "new" column), with no prompt.
- Expected: reload figures equal the report's new column, to the unit.
- Can-fail proof: skip the write on accept; reload boots the old city and the test is red. Fail the write (full storage); the player sees the existing "saving the rebuilt city failed" message and the old city is still there (keep the current behaviour pinned).

**AC-5. A replay that falls far short is flagged, and keep is the default.** If the replay's final tick is below the saved tick by more than a stated fraction (placeholder: any shortfall at all while the saved city's tick is above 0; see Q2), the report says in words that the journal does not cover the saved city and shows keep as the primary button. The test fixture in section 3 triggers it.
- Expected: report text contains a sentence naming the shortfall in ticks; primary button is keep.
- Can-fail proof: set the threshold so it never triggers; the text is missing and the test is red.

**AC-6. Cancel at the prompt and after the report both leave IndexedDB and the journal unchanged.** Byte compare the IndexedDB savepoint stores and the journal key before and after Keep and after Discard.
- Expected: equal, except the build stamp that `onKeep` re-stamps (name the fields in the test).
- Can-fail proof: write the journal flush (`store.tsx:3490`) before the report; the journal key changes and the test is red.

**AC-7. The same holds for an adopted lineage and an own-lineage city.** Run AC-2 and AC-4 twice: once for a city adopted through File > Cities (BUG-1156), once for a city whose lineage was never adopted. Both must behave the same.
- Can-fail proof: the bug reproduced on both in the BUG-1156 r1 probe and its control run, so each must be red on main today and green after the fix.

**AC-8. Closing the tab mid-replay keeps the old city.** Press Rebuild, abandon the page while the replay is running (unmount the store). Reload boots tick 900. (The stall and retry paths already exist and must keep working: pin one test.)
- Can-fail proof: move the write to the start of the replay; red.

## 4. Out of scope

- Fixing the shared journal (one journal for all lineages). That is a separate hazard BUG-1156 named ("candidate follow-up item"): a rebuild of an adopted city reads another city's journal. This item only makes it **harmless**; whether the journal is made per-lineage needs its own item (Q3).
- BUG-1170 (two tabs share one scratch lineage).
- The Rebuild feature's own design (FEAT-1972079897, hard-reset replay) and what the replay does under new rules.
- Go engine saves. This is the webconsole only.

## 5. Unstated assumptions

1. "Before the player sees the outcome" means the forced write at `store.tsx:3478` runs before `setRebuildPhase('report')`. I read that in the code; the BUG-1156 r1 probe is the only run that showed the loss and I could not re-run it.
2. The old city is still on disk at the report phase in an older ring slot, but the forced write re-stamps the slots so the replay wins the boot. Whether the old slot can be recovered by hand today is unverified.
3. A tick-1 result is a loss, not a legitimate "corrected" city. The report copy today calls every difference "the corrected simulation, not a bug" (`RebuildPrompt.tsx:321`). That line is wrong when the journal is missing.

## 6. Questions only Aaron can answer

**Q1. When should the rebuilt city be written?**
a) On accept only (recommended: the prompt's own text says "choices", and the report should be one).
b) Immediately as now, but keep the previous city as a recoverable "before rebuild" entry in File > Cities.
c) Both: write at once under a new city entry and leave the old city untouched.

**Q2. How far short may a rebuild fall before the game warns?**
a) Any shortfall in tick (recommended: a rebuild that loses time is never the "corrected city").
b) More than 5 percent short.
c) Warn on funds or population differences too (noisy: the report already shows them).

**Q3. The journal is shared by every city. Fix it in this item?**
a) No. Make the write safe here (recommended, small); file one item for a journal per city.
b) Yes, journal per city in this item (larger; touches every persistence path).
c) Block Rebuild when the journal does not cover the saved tick, and say why.

## 7. Size

One lane, small to medium: change `onRebuild`/`onResume`/`onKeep` in `store.tsx` and the report phase in `RebuildPrompt.tsx`, plus the test file. The 8 tests above are one file. The destructive round must try a full-storage case and a reload at each phase. Not in the one lane: the per-city journal (Q3 option b), which would be its own epic child.
