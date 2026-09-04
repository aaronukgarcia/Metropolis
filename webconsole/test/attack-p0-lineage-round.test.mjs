// attack-p0-lineage-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against the combined BUG-687 (P0) + FEAT-2326609780 lineage-identity estate.
// Attacker is NOT the author.
//
// THE P0 BEING DEFENDED: Aaron's brand-new city was silently never saved while
// his old city resurrected on every boot, because savepoint slots were global
// and BUG-469's tick-only overwrite gate could never let a low-tick new city
// land over a high-tick old one. The estate's claim is that lineage identity
// makes that class IMPOSSIBLE. This file attacks that claim at the pure
// replay.ts/engine.ts level; the mount-level attacks (export/import round trip,
// refused Save As, and the end-to-end double-city kill-shot) live in
// attack-p0-lineage-round.test.tsx.
//
// Every assertion below is written so that the FIX being absent makes it fail
// with a message naming the exact mechanism.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  persistSavepoint,
  persistSavepointWithReason,
  createSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
  restoreFromSavepoint,
  restampSavepointsBuildVersion,
  migrateLegacySavepointsInPlace,
  mintLineageId,
  readCurrentLineageId,
  writeCurrentLineageId,
  LEGACY_LINEAGE_ID,
  CURRENT_LINEAGE_KEY,
  SAVEPOINT_KEY_PREFIX,
  SAVEPOINT_CAP,
} from '../src/sim/replay.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';

function memStorage(seed) {
  const m = new Map(seed ?? []);
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
    _map: m,
  };
}

/** Mirrors replay.ts's PRIVATE savepointKey — the convention every consumer duplicates. */
function keyOf(slot, lineageId) {
  if (!lineageId || lineageId === LEGACY_LINEAGE_ID) return `${SAVEPOINT_KEY_PREFIX}.${slot}`;
  return `${SAVEPOINT_KEY_PREFIX}.${lineageId}.${slot}`;
}

function pin(fn) {
  try {
    fn();
  } catch (e) {
    console.error('FINDING >>> ' + (e?.message ?? String(e)));
    throw e;
  }
}

// ---------------------------------------------------------------------------
// ATTACK 1 — LINEAGE MINTING DETERMINISM UNDER GENESIS REPLAY.
//
// mintLineageId() is `Math.random()` + `Date.now()`. GR#21 forbids that inside
// the reducer, and the estate's answer is: mint OUTSIDE, stamp onto the
// dispatched 'reset' action, journal the action. Replaying the SAME journal
// must therefore reproduce the SAME lineage id, byte for byte, forever.
// A mint that re-randomises on replay is a determinism divergence — an
// instant REJECT under GR#21.
// ---------------------------------------------------------------------------
test('ATTACK 1: a journalled reset reproduces the SAME lineage id on every genesis replay (GR#21 — the reducer must never re-randomise)', () => {
  const minted = mintLineageId();
  let j = emptyJournal();
  j = recordAction(j, 0, { type: 'place', spec: 'road', x: 4, y: 4 });
  j = recordAction(j, 1, { type: 'reset', lineageId: minted });
  j = recordAction(j, 2, { type: 'place', spec: 'road', x: 6, y: 6 });

  const a = replayFromGenesis(j);
  const b = replayFromGenesis(j);
  const c = replayFromGenesis(j);

  pin(() =>
    assert.equal(
      a.lineageId,
      minted,
      'the reset action carries the minted lineage id and the reducer must stamp exactly it — a mint INSIDE the reducer would produce something else',
    ),
  );
  pin(() =>
    assert.equal(a.lineageId, b.lineageId, 'two replays of the SAME journal produced DIFFERENT lineage ids — GR#21 determinism divergence'),
  );
  assert.equal(b.lineageId, c.lineageId);
  // And it must not be a mint-shaped accident: assert it against a genuinely
  // fresh mint, which must differ.
  assert.notEqual(minted, mintLineageId(), 'test setup: two mints must differ');
});

test('ATTACK 1b: an OLD journal whose reset predates the lineageId field replays to the reserved legacy lineage, never a crash and never a fresh mint', () => {
  let j = emptyJournal();
  j = recordAction(j, 1, { type: 'reset' }); // no lineageId — a pre-fix journal entry
  const a = replayFromGenesis(j);
  const b = replayFromGenesis(j);
  assert.equal(a.lineageId, undefined, 'a reset with no stamp must leave lineageId unset (= the reserved legacy lineage), not mint one');
  assert.equal(a.lineageId, b.lineageId);
  // And undefined must resolve to the SAME physical keyspace legacy saves use.
  assert.equal(keyOf(0, a.lineageId), keyOf(0, LEGACY_LINEAGE_ID));
});

// ---------------------------------------------------------------------------
// ATTACK 2 — THE REBUILD/HARD-RESET-REPLAY PATH.
//
// store.tsx's onRebuild (:2603) persists the genesis-replay result:
//     createSavepoint(result.state, [], now, running, camera, nextSaveSeq())
//     persistSavepoint(window.localStorage, rebuiltSave)
// `result.state` comes from replayFromGenesis*, which starts at
// `initialState()` — NO lineageId — and only ever acquires one if a 'reset'
// action is still IN the journal. Two ordinary shapes have no such action:
//   (a) a city that was never "Start Over"-ed (boot's own fresh branch and
//       loadDevCity1 mint at genesis WITHOUT journalling anything), and
//   (b) any city old enough that JOURNAL_CAP (50,000) has rolled the reset
//       entry off the front.
// In both, the rebuilt city is written to the LEGACY unnamespaced slots while
// `metropolis.currentLineage` still points at the live lineage.
// ---------------------------------------------------------------------------
test('ATTACK 2: a genesis rebuild of a city with no journalled reset LOSES its lineage and is persisted into the LEGACY namespace — the rebuilt city is invisible at the next boot', () => {
  const storage = memStorage();
  const lineage = mintLineageId();
  writeCurrentLineageId(storage, lineage);

  // The live city: minted at boot (freshStart/loadDevCity1/boot's fresh
  // branch all mint OUTSIDE the journal), then played.
  let live = { ...initialState(), lineageId: lineage, unlockedAll: true, funds: 5_000_000_000 };
  let j = emptyJournal();
  for (let i = 0; i < 6; i++) {
    const act = { type: 'place', spec: 'road', x: 4 + i, y: 4 };
    j = recordAction(j, live.tick, act);
    live = reducer(live, act);
  }
  assert.ok(persistSavepoint(storage, createSavepoint(live, [], new Date(2026, 8, 4, 10, 0), 'v-old', null, 5)));
  assert.ok(storage.getItem(keyOf(0, lineage)), 'test setup: the live city autosaves under its OWN namespaced slot');

  // The player now takes Rebuild (cross-build prompt) / hard-reset-replay.
  const rebuilt = replayFromGenesis(j);
  const rebuiltSave = createSavepoint(rebuilt, [], new Date(2026, 8, 4, 11, 0), 'v-new', null, 6);
  assert.equal(persistSavepoint(storage, rebuiltSave), true, 'the rebuild persists');

  pin(() =>
    assert.equal(
      rebuiltSave.lineageId,
      lineage,
      'THE ESTATE LEAKS AT THE REBUILD PATH: replayFromGenesis starts from initialState() (no lineageId) and only recovers one from a journalled ' +
        "'reset' action — a city that was never Start-Over-ed (boot's fresh branch and loadDevCity1 both mint OUTSIDE the journal), or one old " +
        'enough that JOURNAL_CAP=50000 rolled the reset entry off the front, replays to lineageId=undefined. store.tsx:2603 then persists that ' +
        "savepoint, so the REBUILT city lands in the LEGACY unnamespaced slots while metropolis.currentLineage still says '" +
        lineage +
        "'. The next boot reads the LIVE lineage's slots, finds the PRE-rebuild savepoint, and the rebuild silently never happened — Aaron's " +
        'exact symptom (play, rebuild, reload, get the old city back), reintroduced through the rebuild door. It also CLOBBERS the legacy ' +
        "keyspace the migration exists to preserve. Fix: thread the live lineage onto the rebuilt state (or onto createSavepoint) at store.tsx's onRebuild.",
    ),
  );

  // Prove the consequence, not just the stamp.
  const bootSees = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12, 0), readCurrentLineageId(storage)));
  pin(() =>
    assert.equal(
      bootSees.buildVersion,
      'v-new',
      'CONSEQUENCE: booting the current lineage after the rebuild restores the PRE-rebuild savepoint — the rebuilt city is unreachable.',
    ),
  );
});

// ---------------------------------------------------------------------------
// ATTACK 3 — restampSavepointsBuildVersion LOST ITS LINEAGE AT THE CALL SITE.
//
// The function GAINED a `lineageId` parameter in this estate. Its two live
// call sites (store.tsx :2686 onKeep, :2708 onResume) still call it with two
// arguments. For any NON-legacy lineage that restamps the WRONG keyspace, so
// the cross-build mismatch is never cleared — which is verbatim the BUG-468
// infinite "New build detected" prompt loop the parameter exists to prevent.
// ---------------------------------------------------------------------------
test('ATTACK 3: restampSavepointsBuildVersion called WITHOUT a lineage (store.tsx onKeep/onResume) restamps the wrong keyspace — BUG-468\'s infinite rebuild prompt returns for every namespaced city', () => {
  const storage = memStorage();
  const lineage = mintLineageId();
  writeCurrentLineageId(storage, lineage);
  const city = { ...initialState(), lineageId: lineage, tick: 900 };
  assert.ok(persistSavepoint(storage, createSavepoint(city, [], new Date(2026, 8, 4, 10), 'v-OLD-BUILD', null, 1)));

  // Exactly what store.tsx:2686 / :2708 do today.
  restampSavepointsBuildVersion(storage, 'v-NEW-BUILD');

  const afterKeep = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineage));
  pin(() =>
    assert.equal(
      afterKeep.buildVersion,
      'v-NEW-BUILD',
      "BUG-468 REGRESSION: restampSavepointsBuildVersion gained a lineageId parameter in this estate, but store.tsx's two live call sites " +
        '(onKeep :2686 and onResume :2708) were not updated and still pass only (storage, version). For a namespaced lineage it therefore ' +
        "restamps the LEGACY slots — which for this player are empty — and the current lineage's savepoint keeps its stale buildVersion. " +
        'The very next boot re-detects saved!=running and re-prompts "New build detected", forever: the exact infinite loop BUG-468 closed. ' +
        'Note the same call passing the lineage DOES work (asserted below), so this is a pure call-site omission.',
    ),
  );

  // Control: the same call WITH the lineage does the right thing — proving the
  // function is fine and only the call sites are wrong.
  restampSavepointsBuildVersion(storage, 'v-NEW-BUILD', lineage);
  assert.equal(
    mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineage)).buildVersion,
    'v-NEW-BUILD',
    'control: passing the lineage restamps correctly — the defect is the call site, not the function',
  );
});

// ---------------------------------------------------------------------------
// ATTACK 4 — LEGACY MIGRATION: IDEMPOTENCE + HOSTILE PRE-STAMPED VALUES.
// ---------------------------------------------------------------------------
test('ATTACK 4: migrateLegacySavepointsInPlace is idempotent, never touches a namespaced lineage, and REPAIRS a hostile mis-stamp on an unnamespaced slot', () => {
  const storage = memStorage();
  const other = mintLineageId();

  // A real pre-fix save at the unnamespaced keys.
  const legacyCity = { ...initialState(), tick: 5000 };
  assert.ok(persistSavepoint(storage, createSavepoint(legacyCity, [], new Date(2026, 8, 1, 9), 'v-old', null)));
  // A different, namespaced lineage's slots alongside it.
  const nsCity = { ...initialState(), tick: 12, lineageId: other };
  assert.ok(persistSavepoint(storage, createSavepoint(nsCity, [], new Date(2026, 8, 4, 9), 'v-old', null)));

  const before = storage._keys();
  const wrote1 = migrateLegacySavepointsInPlace(storage);
  const after1 = storage._keys();
  assert.equal(wrote1, true, 'the first run stamps the unnamespaced slot');
  assert.deepEqual(after1, before, 'MIGRATION MUST BE IN PLACE: it created or removed a key');

  const wrote2 = migrateLegacySavepointsInPlace(storage);
  assert.equal(wrote2, false, 'IDEMPOTENCE: a second run must be a complete no-op (it re-wrote already-stamped slots)');
  assert.deepEqual(storage._keys(), before);

  // The namespaced lineage must be untouched — no 'legacy' stamp on it.
  const ns = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 10), other));
  assert.equal(ns.lineageId, other, 'the migration CONTAMINATED a namespaced lineage with the legacy stamp');

  // HOSTILE: an unnamespaced slot hand-stamped with a FOREIGN lineage id. That
  // record is self-contradictory (it lives at the legacy keys but claims to be
  // someone else); it must be corrected to 'legacy', not honoured — otherwise
  // createSavepoint/persist would later route this record's rotation into a
  // namespace it does not physically occupy.
  const raw = JSON.parse(JSON.stringify({ ...createSavepoint(legacyCity, [], new Date(2026, 8, 1, 9), 'v-old', null), lineageId: 'L-IMPOSTOR' }));
  storage.setItem(keyOf(0), JSON.stringify(raw));
  migrateLegacySavepointsInPlace(storage);
  const repaired = JSON.parse(storage.getItem(keyOf(0)).startsWith('LZv1:') ? '{}' : storage.getItem(keyOf(0)));
  const stamped = repaired.lineageId ?? mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 1, 10), LEGACY_LINEAGE_ID))?.lineageId;
  assert.equal(
    stamped,
    LEGACY_LINEAGE_ID,
    'a self-contradictory record (foreign lineageId sitting at the UNNAMESPACED legacy keys) must be corrected to the legacy stamp',
  );
});

test('ATTACK 4b: a slot that exists BOTH namespaced and legacy-keyed keeps two independent cities — neither read nor write crosses over', () => {
  const storage = memStorage();
  const lineage = mintLineageId();
  const legacyCity = { ...initialState(), tick: 90_000 };
  const newCity = { ...initialState(), tick: 7, lineageId: lineage };
  assert.ok(persistSavepoint(storage, createSavepoint(legacyCity, [], new Date(2026, 8, 1, 9), 'v', null, 900)));
  assert.ok(persistSavepoint(storage, createSavepoint(newCity, [], new Date(2026, 8, 4, 9), 'v', null, 1)));
  migrateLegacySavepointsInPlace(storage);

  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 10), LEGACY_LINEAGE_ID)).snapshotTick, 90_000);
  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 10), lineage)).snapshotTick, 7);

  // And the low-tick/low-seq new city keeps autosaving successfully forever —
  // the RCA's exact refusal must be impossible.
  for (let i = 0; i < 40; i++) {
    const sp = createSavepoint({ ...newCity, tick: 7 + i }, [], new Date(2026, 8, 4, 10, i), 'v', null, 2 + i);
    const r = persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10, i));
    pin(() => assert.equal(r.ok, true, `autosave ${i} of the NEW city was refused (${r.reason}) — the P0 mechanism is alive`));
  }
  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), LEGACY_LINEAGE_ID)).snapshotTick, 90_000, 'the old city was clobbered');
});

// ---------------------------------------------------------------------------
// ATTACK 5 — currentLineage POINTER CORRUPTION. Boot must never brick.
// ---------------------------------------------------------------------------
test('ATTACK 5: a missing / empty / whitespace / garbage / JSON-object currentLineage pointer never throws and never bricks a restore', () => {
  for (const [label, value] of [
    ['missing', null],
    ['empty', ''],
    ['whitespace', '   '],
    ['garbage', ' ￿<script>'],
    ['json object', '{"lineageId":"L1"}'],
    ['enormous', 'L'.repeat(100_000)],
  ]) {
    const storage = memStorage();
    if (value !== null) storage.setItem(CURRENT_LINEAGE_KEY, value);
    let read;
    assert.doesNotThrow(() => {
      read = readCurrentLineageId(storage);
    }, `readCurrentLineageId threw on a ${label} pointer`);
    assert.equal(typeof read, 'string');
    if (value === null || value === '') assert.equal(read, LEGACY_LINEAGE_ID, `${label} must default to the reserved legacy lineage`);
    // Whatever it resolves to, reading and restoring must degrade, not throw.
    assert.doesNotThrow(() => readAllSavepoints(storage, new Date(), read), `readAllSavepoints threw on a ${label} pointer`);
    const r = restoreFromSavepoint(storage, read);
    assert.equal(r.success, false, `${label}: no savepoint exists for that lineage, so restore must fail cleanly`);
  }
});

test('ATTACK 5b: a pointer at a lineage with NO saves leaves every other lineage intact and readable (nothing is destroyed by the miss)', () => {
  const storage = memStorage();
  const real = mintLineageId();
  assert.ok(persistSavepoint(storage, createSavepoint({ ...initialState(), tick: 4242, lineageId: real }, [], new Date(2026, 8, 4, 9), 'v', null, 3)));
  writeCurrentLineageId(storage, 'L-does-not-exist');

  const pointed = readCurrentLineageId(storage);
  assert.equal(restoreFromSavepoint(storage, pointed).success, false, 'restore of a saveless lineage must fail, not throw');
  // The real lineage survives untouched.
  assert.equal(restoreFromSavepoint(storage, real).success, true, 'a pointer miss DESTROYED or hid a real lineage');
  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 10), real)).snapshotTick, 4242);
});

// ---------------------------------------------------------------------------
// ATTACK 6 — THE CLEAR-SLOTS EXACT-KEY CLAIM (ConfigMenu item 6).
// The estate claims the old bare-prefix clear destroyed EVERY city's autosaves
// and that it now clears only the current lineage's exact keys. Model both and
// prove the difference is observable — i.e. that a prefix-match mutation is
// caught by an assertion somebody actually has.
// ---------------------------------------------------------------------------
test('ATTACK 6: Clear-autosave-slots must delete ONLY the current lineage\'s exact keys — a prefix match (the old code, and the obvious mutation) destroys every other city', () => {
  const seed = () => {
    const storage = memStorage();
    const a = 'La1111';
    const b = 'Lb2222';
    // Deliberately make one lineage id a literal PREFIX of another — the exact
    // shape a prefix-match cannot distinguish even after "scoping" it.
    const c = 'La1111x';
    for (const [ln, tick] of [[undefined, 1000], [a, 10], [b, 20], [c, 30]]) {
      assert.ok(persistSavepoint(storage, createSavepoint({ ...initialState(), tick, lineageId: ln }, [], new Date(2026, 8, 4, 9), 'v', null, 1)));
    }
    return { storage, a, b, c };
  };

  // The SHIPPED behaviour (ConfigMenu.clearAutosaveSlots, modelled exactly).
  {
    const { storage, a, b, c } = seed();
    writeCurrentLineageId(storage, a);
    const lineageId = readCurrentLineageId(storage);
    const keyFor = (slot) => (lineageId === LEGACY_LINEAGE_ID ? `${SAVEPOINT_KEY_PREFIX}.${slot}` : `${SAVEPOINT_KEY_PREFIX}.${lineageId}.${slot}`);
    const slotCount = lineageId === LEGACY_LINEAGE_ID ? 8 : SAVEPOINT_CAP;
    for (let slot = 0; slot < slotCount; slot++) storage.removeItem(keyFor(slot));

    assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), a).length, 0, 'the current lineage was not cleared');
    pin(() => assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), b).length, 1, 'clearing lineage A destroyed lineage B'));
    pin(() =>
      assert.equal(
        readAllSavepoints(storage, new Date(2026, 8, 4, 10), c).length,
        1,
        "clearing lineage 'La1111' destroyed 'La1111x' — a lineage id that is a literal PREFIX of another is exactly what an exact-key clear must survive",
      ),
    );
    assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), LEGACY_LINEAGE_ID).length, 1, 'clearing a namespaced lineage destroyed the legacy city');
  }

  // THE MUTATION: revert to a prefix clear. It MUST break the assertions above.
  {
    const { storage, a, b, c } = seed();
    writeCurrentLineageId(storage, a);
    for (const k of storage._keys()) if (k.startsWith(SAVEPOINT_KEY_PREFIX)) storage.removeItem(k);
    assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), b).length, 0);
    assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), c).length, 0);
    assert.equal(
      readAllSavepoints(storage, new Date(2026, 8, 4, 10), LEGACY_LINEAGE_ID).length,
      0,
      'MUTATION CONTROL: the prefix clear must visibly destroy other lineages, proving the exact-key assertions above are load-bearing',
    );
  }
});

// ---------------------------------------------------------------------------
// ATTACK 7 — saveSeq CONTINUITY WHEN localStorage LOST THE SAVEPOINT BUT IDB
// STILL HAS IT (the rescue). The app recovers its counter from the BOOT
// savepoint (store.tsx: saveSeqRef = boot.bootSavepointMeta?.saveSeq ?? 0). On
// a localStorage-empty boot the local meta is null, so the counter restarts at
// 0 — and only the IDB swap's own `if (best.saveSeq > saveSeqRef.current)`
// line pulls it forward. Model the whole sequence and prove the NEXT persist
// after a rescue cannot be refused as stale.
// ---------------------------------------------------------------------------
test('ATTACK 7: after an IDB rescue of a lineage localStorage lost, the recovered saveSeq must keep the NEXT persist from being refused as stale', () => {
  const storage = memStorage();
  const lineage = mintLineageId();
  writeCurrentLineageId(storage, lineage);

  // The durable copy IDB still holds, at seq 500.
  const rescued = createSavepoint({ ...initialState(), tick: 8000, lineageId: lineage }, [], new Date(2026, 8, 4, 9), 'v', null, 500);
  // localStorage has NOTHING for this lineage (the wedge/eviction).
  assert.equal(readAllSavepoints(storage, new Date(2026, 8, 4, 10), lineage).length, 0);

  // Boot: no local savepoint -> counter starts at 0. The swap adopts the
  // rescue and pulls the counter forward (store.tsx's R2-F1 line).
  let saveSeqRef = 0;
  if (typeof rescued.saveSeq === 'number' && Number.isFinite(rescued.saveSeq) && rescued.saveSeq > saveSeqRef) saveSeqRef = rescued.saveSeq;
  pin(() => assert.equal(saveSeqRef, 500, 'the rescue did not pull the persist counter forward — every subsequent save would be numbered below the durable copy'));

  // The rescue is written back to localStorage (the swap's own persist), then
  // the session keeps autosaving.
  assert.equal(persistSavepointWithReason(storage, rescued, new Date(2026, 8, 4, 10)).ok, true);
  for (let i = 1; i <= 5; i++) {
    saveSeqRef += 1;
    const sp = createSavepoint({ ...initialState(), tick: 8000 + i, lineageId: lineage }, [], new Date(2026, 8, 4, 10, i), 'v', null, saveSeqRef);
    const r = persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10, i));
    pin(() => assert.equal(r.ok, true, `post-rescue autosave ${i} refused as ${r.reason} — seq continuity across the rescue is broken`));
  }
  assert.equal(mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineage)).saveSeq, 505);

  // MUTATION CONTROL: had the counter NOT been pulled forward, the next
  // persist must be visibly refused — proving the assertion above is real.
  {
    const s2 = memStorage();
    assert.equal(persistSavepointWithReason(s2, rescued, new Date(2026, 8, 4, 10)).ok, true);
    const naive = createSavepoint({ ...initialState(), tick: 8001, lineageId: lineage }, [], new Date(2026, 8, 4, 10, 1), 'v', null, 1);
    // fill the remaining slots so the rescue's slot is the overwrite target
    for (let s = 1; s < SAVEPOINT_CAP; s++) {
      assert.equal(
        persistSavepointWithReason(s2, createSavepoint({ ...initialState(), tick: 9000, lineageId: lineage }, [], new Date(2026, 8, 4, 10, 30), 'v', null, 501 + s), new Date(2026, 8, 4, 10, 30)).ok,
        true,
      );
    }
    const r = persistSavepointWithReason(s2, naive, new Date(2026, 8, 4, 10, 40));
    assert.equal(r.ok, false, 'MUTATION CONTROL: a counter that restarted at 1 after the rescue must be refused');
    assert.equal(r.reason, 'stale-overwrite');
  }
});

// ---------------------------------------------------------------------------
// ATTACK 8 — TWO TABS, SAME LINEAGE, RACING AUTOSAVES.
// Namespacing gives no protection here (same lineage = same keys), so the
// within-lineage saveSeq gate is the ONLY ordering mechanism. The claim to
// break: the newest history always survives and the older tab can never
// clobber it.
// ---------------------------------------------------------------------------
test('ATTACK 8: two tabs on the SAME lineage racing autosaves — the older tab can never clobber the newer history, and every refusal is reported', () => {
  const storage = memStorage();
  const lineage = mintLineageId();

  // Tab A has been playing: it is at seq 60, tick 6000.
  let seqA = 0;
  for (let i = 0; i < 60; i++) {
    seqA += 1;
    persistSavepoint(storage, createSavepoint({ ...initialState(), tick: 5940 + i, lineageId: lineage }, [], new Date(2026, 8, 4, 10, 0, i), 'v', null, seqA), new Date(2026, 8, 4, 10, 0, i));
  }
  const bestBefore = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), lineage));
  assert.equal(bestBefore.saveSeq, 60);

  // Tab B was opened long ago and booted from an old savepoint: its counter is
  // at 12. It now autosaves, interleaved with A, for 30 rounds.
  let seqB = 12;
  let refusedB = 0;
  let landedB = 0;
  for (let i = 0; i < 30; i++) {
    seqB += 1;
    const r = persistSavepointWithReason(storage, createSavepoint({ ...initialState(), tick: 1200 + i, lineageId: lineage }, [], new Date(2026, 8, 4, 10, 5, i), 'v', null, seqB), new Date(2026, 8, 4, 10, 5, i));
    if (r.ok) landedB += 1;
    else {
      refusedB += 1;
      assert.equal(r.reason, 'stale-overwrite', 'a within-lineage ordering refusal must be reported as such, never silently or as a quota error');
    }
    // A keeps going too.
    seqA += 1;
    assert.equal(
      persistSavepointWithReason(storage, createSavepoint({ ...initialState(), tick: 6000 + i, lineageId: lineage }, [], new Date(2026, 8, 4, 10, 5, i), 'v', null, seqA), new Date(2026, 8, 4, 10, 5, i)).ok,
      true,
      'the LEADING tab must never be refused',
    );
  }
  assert.ok(refusedB > 0, 'test setup: the trailing tab must actually be refused at least once');

  const all = readAllSavepoints(storage, new Date(2026, 8, 4, 12), lineage);
  const best = mostRecentSavepoint(all);
  pin(() =>
    assert.equal(
      best.saveSeq,
      seqA,
      `THE LEADING TAB'S NEWEST HISTORY MUST SURVIVE: after ${landedB} landed / ${refusedB} refused writes from the trailing tab, the freshest ` +
        `savepoint is seq ${best.saveSeq} (tick ${best.snapshotTick}) — the trailing tab clobbered the newer city.`,
    ),
  );
  pin(() => assert.ok(best.snapshotTick >= 6000, `the surviving savepoint is the trailing tab's stale city (tick ${best.snapshotTick})`));
});

// ---------------------------------------------------------------------------
// ATTACK 9 — MUTATIONS. Each removes one load-bearing piece of the fix; the
// suite's own claims must visibly redden. Run in-process against the real
// primitives so there is no "the test only checks the test" escape.
// ---------------------------------------------------------------------------
test('MUTATION 1: drop the namespacing (all lineages share the legacy keys) — the RCA defect returns and is CAUGHT', () => {
  const storage = memStorage();
  const oldCity = { ...initialState(), tick: 100_000 };
  const newLineage = mintLineageId();
  // Aaron's real shape: the old city occupies EVERY rotation slot.
  for (let s = 0; s < SAVEPOINT_CAP; s++) {
    assert.ok(persistSavepoint(storage, createSavepoint({ ...oldCity, tick: 100_000 + s }, [], new Date(2026, 8, 1, 9, s), 'v', null, 900 + s), new Date(2026, 8, 1, 9, s)));
  }

  // The mutation: strip lineageId off the new city's savepoints before persist
  // (i.e. savepointKey ignoring the lineage).
  let refused = 0;
  for (let i = 0; i < 40; i++) {
    const sp = createSavepoint({ ...initialState(), tick: 10 + i, lineageId: newLineage }, [], new Date(2026, 8, 4, 10, i), 'v', null, 1 + i);
    delete sp.lineageId; // <-- MUTATION
    if (!persistSavepointWithReason(storage, sp, new Date(2026, 8, 4, 10, i)).ok) refused += 1;
  }
  assert.ok(refused > 0, 'MUTATION NOT CAUGHT: with namespacing dropped, the new city\'s autosaves must be refused by the old city\'s tick gate');
  assert.equal(
    refused,
    40,
    'MUTATION NOT CAUGHT: with namespacing gone, EVERY autosave of the new city must be refused by the old city occupying every slot',
  );
  assert.ok(
    mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 11), LEGACY_LINEAGE_ID)).snapshotTick >= 100_000,
    'MUTATION NOT CAUGHT: the old city must resurrect once the namespacing is gone — this is the exact P0',
  );
});

test('MUTATION 2: drop the legacy migration — an existing player\'s saves must STILL be found (the migration is honesty, not correctness)', () => {
  const storage = memStorage();
  const legacyCity = { ...initialState(), tick: 77_000 };
  assert.ok(persistSavepoint(storage, createSavepoint(legacyCity, [], new Date(2026, 8, 1, 9), 'v', null)));
  // NO migration run at all (the mutation).
  const pointer = readCurrentLineageId(storage); // defaults to legacy
  const r = restoreFromSavepoint(storage, pointer);
  assert.equal(r.success, true, 'REAL DEFECT IF RED: without the migration an existing player must still boot their city — the unnamespaced keys ARE the legacy lineage');
  assert.equal(r.state.tick, 77_000);
  // And with the migration, byte-identical restore.
  migrateLegacySavepointsInPlace(storage);
  assert.equal(restoreFromSavepoint(storage, pointer).state.tick, 77_000);
});

test('MUTATION 3: allow a cross-lineage comparison in the overwrite gate — the new city loses its slot again and it is CAUGHT', () => {
  // Model the gate WITHOUT the namespacing scope: compare an incoming new-city
  // savepoint against the old city's, exactly as the pre-fix code did.
  const storage = memStorage();
  const old = createSavepoint({ ...initialState(), tick: 100_000 }, [], new Date(2026, 8, 1, 9), 'v', null, 900);
  for (let s = 0; s < SAVEPOINT_CAP; s++) storage.setItem(keyOf(s), JSON.stringify(old));
  const fresh = createSavepoint({ ...initialState(), tick: 12, lineageId: mintLineageId() }, [], new Date(2026, 8, 4, 9), 'v', null, 1);
  const mutated = { ...fresh };
  delete mutated.lineageId; // the cross-lineage comparison the fix forbids
  const r = persistSavepointWithReason(storage, mutated, new Date(2026, 8, 4, 10));
  assert.equal(r.ok, false, 'MUTATION NOT CAUGHT: a cross-lineage comparison must refuse the new city');
  assert.equal(r.reason, 'stale-overwrite');
  // With the lineage intact it lands, as the fix promises.
  assert.equal(persistSavepointWithReason(storage, fresh, new Date(2026, 8, 4, 10)).ok, true);
});

test('MUTATION 4: re-randomise the lineage on replay — genesis replay diverges and it is CAUGHT', () => {
  const minted = mintLineageId();
  let j = emptyJournal();
  j = recordAction(j, 1, { type: 'reset', lineageId: minted });
  // The shipped (correct) behaviour.
  assert.equal(replayFromGenesis(j).lineageId, replayFromGenesis(j).lineageId);

  // The mutation: a reducer that mints its own id instead of using the
  // action's. Modelled by mutating the journal entry between replays, which is
  // exactly what a nondeterministic mint amounts to.
  const mutatedA = { entries: [{ tick: 1, action: { type: 'reset', lineageId: mintLineageId() } }] };
  const mutatedB = { entries: [{ tick: 1, action: { type: 'reset', lineageId: mintLineageId() } }] };
  assert.notEqual(
    replayFromGenesis(mutatedA).lineageId,
    replayFromGenesis(mutatedB).lineageId,
    'MUTATION NOT CAUGHT: a per-replay mint must produce different lineage ids — the determinism assertion in ATTACK 1 is therefore load-bearing',
  );
});
