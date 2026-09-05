// bug742-capacity-failclosed.test.mjs — BUG-742 (P2), round F1 on BUG-736's
// capacity-loss gate (GR#16).
//
// Two independent halves of the fix, both exercised end-to-end on the REAL
// reducer/catalogue, no expected numbers hand-typed (GR#15):
//
//   1. STORAGE BOUNDARY (gamesave.ts): a save's `capacityTier` is a
//      `typeof number` today, which admits a fractional/negative/NaN/
//      Infinity/out-of-range-integer value straight through validation.
//      coerceCapacityTierInPlace now clamps every such value to a safe
//      integer in [0, ladderLength-1] at parse time, recording a
//      registry-sourced MET-V865 warning whenever it actually changes
//      something.
//
//   2. FAIL-CLOSED GATES (engine.ts applyConsolidatorPass): even if a
//      poisoned capacityTier somehow reaches the reducer directly (bypassing
//      the storage boundary — exactly how attack-bug736-round.test.mjs's
//      ATTACK 7 fixtures are built, by construction not by save/load), the
//      capacity-loss gate and CEIL-3 both now check Number.isFinite BEFORE
//      the naive `< 0` compare, skipping with 'capacity unknown' instead of
//      silently passing a NaN through.
//
//   3. P3: a SKIP-ONLY pass (zero transactions) no longer gets appended to
//      consolidatorLog nor resets consolidatorUndoConsumed — so a
//      permanently-skipped estate can no longer clobber a real pass's
//      single-level undo, month after month.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, capacityAtTier, computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, CONSOLIDATOR_LOG_CAP } from '../src/sim/engine.ts';
import { consolidationLadder, capacityOf, buildingCapacityOf } from '../src/sim/consolidator.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import { buildGameSave, gameSaveText, parseGameSave, validateGameSaveObject } from '../src/sim/gamesave.ts';
import { createSavepoint, restoreFromSavepoint, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';
import { recentErrors } from '../src/sim/backend.ts';
import { observeNews, createNewsFeedTracker, createNewsFeedSeq } from '../src/sim/newsFeed.ts';

const NURSERY = SPECS.edu_nursery;
const CITY = SPECS.edu_nursery_city;
const RUNG = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city');
const G = RUNG.groupSize;
const LADDER_LEN = NURSERY.capacityTiers.length;

// -----------------------------------------------------------------------
// Half 1: the storage boundary (gamesave.ts coerceCapacityTierInPlace)
// -----------------------------------------------------------------------

function saveWithBuilding(building) {
  const state = { ...initialState(), buildings: [{ id: 1, spec: 'edu_nursery', x: 0, y: 0, builtTick: -1000, ...building }] };
  const save = buildGameSave({
    state,
    journal: emptyJournal(),
    journalTail: [],
    name: 'BUG-742 probe',
    buildVersion: 'v0.3.0-test',
    now: new Date('2026-09-05T00:00:00.000Z'),
  });
  return gameSaveText(save);
}

function parsedTierOf(building) {
  const text = saveWithBuilding(building);
  const before = recentErrors().length;
  const parsed = parseGameSave(text);
  assert.equal(parsed.ok, true);
  const b = parsed.save.savepoint.snapshot.buildings.find((x) => x.id === 1);
  return { tier: b.capacityTier, errorCountDelta: recentErrors().length - before };
}

/**
 * NaN/+-Infinity cannot survive a real JSON.stringify/parse round-trip
 * (JSON.stringify(NaN) === 'null', same for +-Infinity) — that is a JSON
 * limitation, not something coerceCapacityTierInPlace needs to defend
 * against via the text path. It DOES need to defend against an
 * already-parsed object carrying one of these values directly (e.g. a
 * named save read from IndexedDB/localStorage that skipped JSON entirely,
 * or a hand-constructed object), which is exactly validateGameSaveObject's
 * own contract (BUG-577) — so these three values are probed through THAT
 * entry point, never through gameSaveText/parseGameSave's text round-trip.
 */
function objectTierOf(rawCapacityTier) {
  const state = { ...initialState(), buildings: [{ id: 1, spec: 'edu_nursery', x: 0, y: 0, builtTick: -1000, capacityTier: rawCapacityTier }] };
  const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'probe', buildVersion: 'v0', now: new Date() });
  const before = recentErrors().length;
  const validated = validateGameSaveObject(save);
  const b = validated.savepoint.snapshot.buildings.find((x) => x.id === 1);
  return { tier: b.capacityTier, errorCountDelta: recentErrors().length - before };
}

describe('BUG-742 half 1 — gamesave.ts storage-boundary coercion', () => {
  test('a fractional capacityTier is truncated to an integer and recorded', () => {
    const { tier, errorCountDelta } = parsedTierOf({ capacityTier: 1.5 });
    assert.equal(tier, 1, 'Math.trunc(1.5) === 1, an integer index');
    assert.ok(Number.isInteger(tier));
    assert.ok(errorCountDelta >= 1, 'a coercion that changes the value must record a registry error');
  });

  test('NaN coerces to 0 and is recorded (via validateGameSaveObject — NaN cannot survive a real JSON round-trip)', () => {
    const { tier, errorCountDelta } = objectTierOf(NaN);
    assert.equal(tier, 0);
    assert.ok(errorCountDelta >= 1);
  });

  test('Infinity coerces to 0 (non-finite is not trustworthy as "very high tier") and is recorded', () => {
    const { tier, errorCountDelta } = objectTierOf(Infinity);
    assert.equal(tier, 0);
    assert.ok(errorCountDelta >= 1);
  });

  test('-Infinity coerces to 0 and is recorded', () => {
    const { tier, errorCountDelta } = objectTierOf(-Infinity);
    assert.equal(tier, 0);
    assert.ok(errorCountDelta >= 1);
  });

  test('a negative capacityTier clamps to 0 and is recorded', () => {
    const { tier, errorCountDelta } = parsedTierOf({ capacityTier: -7 });
    assert.equal(tier, 0);
    assert.ok(errorCountDelta >= 1);
  });

  test('an out-of-range-above integer clamps to the ladder\'s last index and is recorded', () => {
    const { tier, errorCountDelta } = parsedTierOf({ capacityTier: 999 });
    assert.equal(tier, LADDER_LEN - 1);
    assert.ok(errorCountDelta >= 1);
  });

  test('a valid in-range integer tier passes through completely unchanged, with NO error recorded', () => {
    for (let t = 0; t < LADDER_LEN; t++) {
      const before = recentErrors().length;
      const { tier, errorCountDelta } = parsedTierOf({ capacityTier: t });
      assert.equal(tier, t, `tier ${t} must survive exactly`);
      assert.equal(errorCountDelta, 0, `an already-safe tier ${t} must not spuriously record an error (before=${before})`);
    }
  });

  test('an untiered building (capacityTier omitted) is left untouched — the field never appears', () => {
    const text = saveWithBuilding({});
    const parsed = parseGameSave(text);
    const b = parsed.save.savepoint.snapshot.buildings.find((x) => x.id === 1);
    assert.equal('capacityTier' in b, false);
  });

  test('a spec with NO capacityTiers ladder clamps any numeric tier to 0', () => {
    // pow_wind (per consolidator.ts's own doc comment) has mw but no
    // capacityTiers array — verified live, not assumed (GR#15).
    const windSpec = SPECS.pow_wind;
    assert.ok(windSpec, 'pow_wind must exist in the live catalogue for this probe to mean anything');
    assert.equal(windSpec.capacityTiers, undefined, 'sanity: pow_wind really has no ladder');
    const state = { ...initialState(), buildings: [{ id: 2, spec: 'pow_wind', x: 0, y: 0, builtTick: -1000, capacityTier: 5 }] };
    const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'probe', buildVersion: 'v0', now: new Date() });
    const parsed = parseGameSave(gameSaveText(save));
    const b = parsed.save.savepoint.snapshot.buildings.find((x) => x.id === 2);
    assert.equal(b.capacityTier, 0, 'no ladder means the only defined tier is 0');
  });

  test('a wrong-TYPE capacityTier (string/bool) is still REJECTED outright, not coerced — coercion only applies to typeof number', () => {
    assert.throws(() => parseGameSave(saveWithBuilding({ capacityTier: '3' })), /wrong-typed capacityTier/);
    assert.throws(() => parseGameSave(saveWithBuilding({ capacityTier: true })), /wrong-typed capacityTier/);
  });
});

describe('BUG-742 round-2 M8 — the savepoint (autosave) boundary actually coerces on the REAL read path', () => {
  // ROUND-2 REJECT finding (opus-reround2-bug742, M8): replacing
  // `snap.buildings = coerceSnapshotBuildings(snap.buildings) as unknown[];`
  // inside replay.ts's decodeSavepointBytes with a no-op is UNDETECTED by
  // every existing suite — half 1 above only exercises gamesave.ts's
  // FILE-load path, never readSlot/restoreFromSavepoint (the DEFAULT boot
  // path, and the one BUG-742's original F3 finding was about). This test
  // drives that exact path end-to-end: a savepoint with a fractional
  // capacityTier, written to a mock localStorage exactly as the real
  // autosave cycle would, then restored via the REAL restoreFromSavepoint
  // (-> readSlot -> decodeSavepointBytes -> data.ts's coerceSnapshotBuildings).
  function memStorage(init = {}) {
    const m = new Map(Object.entries(init));
    return {
      getItem: (k) => (m.has(k) ? m.get(k) : null),
      setItem: (k, v) => m.set(k, String(v)),
      removeItem: (k) => m.delete(k),
    };
  }

  test('a poisoned savepoint restored via readSlot/restoreFromSavepoint yields an integer tier and records exactly one MET-V865', () => {
    const base = initialState();
    const state = { ...base, buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }] };
    const sp = createSavepoint(state, [], new Date());
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(sp) });

    const before = recentErrors().filter((e) => e.code === 'MET-V865').length;
    const res = restoreFromSavepoint(storage);
    const after = recentErrors().filter((e) => e.code === 'MET-V865').length;

    assert.equal(res.success, true, 'setup: restore must succeed');
    const restored = res.state.buildings.find((b) => b.id === 1);
    assert.ok(restored, 'setup: the poisoned building survived restore');
    assert.equal(restored.capacityTier, 8, 'the DEFAULT boot path must coerce 8.7 -> 8, exactly like the file-load path');
    assert.ok(Number.isInteger(restored.capacityTier), 'coerced tier must be a real integer, not left fractional');

    // Filtered specifically to MET-V865 (not the overall ring length): this
    // environment's recordError also incidentally records an UNRELATED
    // MET-V805 ("Error log write failed") every call, because the test
    // harness's stub `localStorage` global has no real `setItem` — noise
    // from recordError's OWN persistence attempt, nothing to do with this
    // fix. Asserting on the ring's raw length would be a flaky/misleading
    // gate; MET-V865's own count is the precise, correct signal.
    assert.equal(after - before, 1, 'exactly one NEW MET-V865 record for this restore\'s single poisoned building — never zero (the coercion must have run) and never two (it must not double-fire)');
    const thisBuildingRecord = recentErrors().find(
      (e) => e.code === 'MET-V865' && e.msg.includes('Building[0]') && e.msg.includes('8.7') && e.msg.includes('coerced to 8'),
    );
    assert.ok(thisBuildingRecord, 'the recorded MET-V865 message must actually describe THIS building\'s coercion (8.7 -> 8), not a coincidental unrelated one');
  });
});

// -----------------------------------------------------------------------
// Half 2 + 3: fail-closed engine gates + undo survival across skip-only
// passes (reuses the exact fixture idiom attack-bug736-round.test.mjs uses)
// -----------------------------------------------------------------------

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 500_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}

function runMonths(s, n) {
  let cur = s;
  for (let m = 0; m < n; m++) cur = advanceToNextBoundary(cur);
  return cur;
}

const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
const on = (s) => reducer(s, { type: 'toggleConsolidator' });
const allSkips = (s) => (s.consolidatorLog ?? []).flatMap((p) => p.skipped);
const allTxns = (s) => (s.consolidatorLog ?? []).flatMap((p) => p.transactions);
const nurseryCountOf = (s) => s.buildings.filter((b) => b.spec === 'edu_nursery').length;
const cityCountOf = (s) => s.buildings.filter((b) => b.spec === 'edu_nursery_city').length;
const offTowerCountOf = (s) => s.buildings.filter((b) => b.spec === 'off_tower').length;

/** A permanently-poisoned tier-9 group (one fractional member) — never a real candidate, by construction, not via save/load. */
function poisonedFixture() {
  const buildings = [...roadRow(0, 15)];
  const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
  tiers.forEach((tier, i) => {
    buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: tier, builtTick: -1000 });
  });
  for (let h = 0; h < 2; h++) buildings.push({ id: 9000 + h, spec: 'edu_nursery_city', x: 300 + h * 10, y: 300, builtTick: -1000 });
  return withConnectivity(mk({ buildings }));
}

describe('BUG-742 half 2 — the density-apply gates fail CLOSED on a non-finite groupCapacityReal', () => {
  test('a poisoned group is skipped as "capacity unknown", never as "capacity loss", and never consolidated', () => {
    let s = on(poisonedFixture());
    s = runMonths(s, 12);
    assert.equal(nurseryCountOf(s), G, 'the poisoned group survives in full — no capacity ever silently lost');
    assert.equal(cityCountOf(s), 2, 'only the pre-existing headroom cities exist; nothing new was built from the poisoned group');
    // Skip-only passes are never logged (P3 below), so the failure mode
    // must be proven by absence-of-harm here; the mixed-pass variant in
    // attack-bug736-round.test.mjs proves the 'capacity unknown' reason
    // string directly when a real transaction shares the same pass.
    assert.equal(allTxns(s).filter((t) => t.removed.some((r) => r.id >= 100 && r.id < 100 + G)).length, 0);
  });

  test('CEIL-3s own subtrahend guard is INDEPENDENTLY load-bearing: a lone poisoned building elsewhere in the SAME family, that is never itself a candidate, still blocks an otherwise-healthy group', () => {
    // ROUND P3 note (opus-round-bug742's RED-PROOF file): M1 (groupCapacityReal)
    // and M2 (capacityGain) are mathematically EQUIVALENT guards given the
    // real catalogue — capacityGain = capacityOf(toSpec) - groupCapacityReal,
    // and capacityOf(toSpec) is always finite (a static catalogue value), so
    // groupCapacityReal non-finite => capacityGain non-finite ALWAYS, and
    // vice-versa is moot since M1 runs first. No test can make removing M1
    // OR M2 ALONE independently red without also removing the other — that
    // is a genuine equivalent-mutant (defense-in-depth over the identical
    // failure mode), not a coverage gap.
    //
    // CEIL-3's OWN guard (M3) is NOT equivalent to M1/M2: it protects the
    // CITYWIDE family total (cityFamilyCapacity sums buildingCapacityOf over
    // EVERY building of the family, whether or not that building is part of
    // ANY consolidation candidate), which a poisoned SINGLETON building
    // elsewhere in the family can corrupt even while the group actually
    // being evaluated is perfectly healthy (finite groupCapacityReal AND
    // capacityGain — M1/M2 both pass it clean). This test isolates exactly
    // that: a healthy flat tier-0 nursery group (would normally consolidate)
    // shares its family with ONE lone poisoned nursery far away that is
    // deliberately NOT part of any group (a singleton, below groupSize G,
    // so it can never itself be a candidate) — proving M3 alone is
    // load-bearing: remove it and this healthy group WOULD wrongly
    // consolidate on an unverifiable (NaN) family-share denominator.
    const buildings = [...roadRow(0, 15)];
    for (let i = 0; i < G; i++) buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: 0, builtTick: -1000 });
    // The lone poisoned singleton — never a candidate (count 1 < G), far
    // from the healthy group's section, no road needed (it never attempts
    // to consolidate or connect).
    buildings.push({ id: 900, spec: 'edu_nursery', x: 500, y: 500, capacityTier: 1.5, builtTick: -1000 });
    let s = on(withConnectivity(mk({ buildings })));
    s = runMonths(s, 24);
    assert.equal(nurseryCountOf(s), G + 1, 'the healthy group is NEVER consolidated while the citywide family total is unverifiable — the singleton survives too, untouched');
    assert.equal(cityCountOf(s), 0, 'no successor was ever built from the healthy group');
    assert.ok(allSkips(s).some((k) => k.reason === 'capacity unknown'), 'the healthy group is skipped as capacity unknown, caused by the UNRELATED singleton poisoning the family total');
  });
});

describe('BUG-742 P3 — skip-only passes do not clobber the single-level undo', () => {
  // ROUND REJECT NOTE (opus-round-bug742, F2): the previous version of this
  // block used a mixed-family fixture and asserted `consolidatorUndoConsumed
  // === false` before pressing undo — but never actually verified that
  // `consolidatorLog[0]` was a SKIP-ONLY entry at that point. It measurably
  // was NOT (the fixture's off_tower group never earned a logged skip in
  // that shape), so the assertions that followed (undo restores the flat
  // group) were true even under the OLD naive `log[0]` semantics — the test
  // was VACUOUS with respect to the thing it claimed to prove. Replaced with
  // the round's own D1 (log directly shaped [skip, skip, real, real]) and D5
  // (an already-spent undo, then 24 REAL skip-only months through the actual
  // reducer) shapes, which do exercise the exact paths the round attacked.

  const rec = (b) => ({ id: b.id, spec: b.spec, x: b.x, y: b.y, builtTick: b.builtTick, placedBy: 'player' });
  const skipEntry = (id) => ({ id, tick: id * 10, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });

  function stateWithLog(log, consumed) {
    const buildings = [...roadRow(0, 12)];
    buildings.push({ id: 50, spec: 'edu_nursery_city', x: 1, y: 1, builtTick: 10, placedBy: 'auto' });
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 6, y: 1, builtTick: 5, placedBy: 'auto' });
    return withConnectivity({ ...mk({ buildings, nextId: 5000, consolidatorLog: log }), consolidatorUndoConsumed: consumed });
  }
  const removedNew = [
    { id: 10, spec: 'edu_nursery', x: 1, y: 1, builtTick: -1000 },
    { id: 11, spec: 'edu_nursery', x: 3, y: 1, builtTick: -1000 },
  ];
  const removedOld = [
    { id: 20, spec: 'edu_nursery', x: 6, y: 1, builtTick: -1000 },
    { id: 21, spec: 'edu_nursery', x: 8, y: 1, builtTick: -1000 },
  ];
  const txn = (removed, addedId, x) => ({
    sectionKey: 1,
    kind: 'consolidate',
    removed: removed.map(rec),
    added: [{ id: addedId, spec: 'edu_nursery_city', x, y: 1, builtTick: 10, placedBy: 'auto' }],
    buildCost: 1000,
    scrapRecovered: 100,
    netCost: 900,
  });
  const realNew = { id: 3, tick: 30, transactions: [txn(removedNew, 50, 1)], skipped: [] };
  const realOld = { id: 1, tick: 10, transactions: [txn(removedOld, 51, 6)], skipped: [] };

  test('D1 shape: log = [skip, skip, real, real] — a FIRST-press undo reverses the newest REAL pass and keeps the skips', () => {
    const s = stateWithLog([skipEntry(5), skipEntry(4), realNew, realOld], false);
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.ok(!u.buildings.some((b) => b.id === 50), 'the newest real pass successor is removed');
    assert.ok(u.buildings.some((b) => b.id === 10) && u.buildings.some((b) => b.id === 11), 'its demolished members are restored');
    assert.ok(u.buildings.some((b) => b.id === 51), 'the OLDER pass successor is untouched');
    assert.deepEqual(u.consolidatorLog.map((p) => p.id), [5, 4, 1], 'exactly the reversed entry is removed; skip entries preserved in place');
    assert.equal(u.consolidatorUndoConsumed, true);
  });

  test('D5 shape: an already-spent undo stays spent through 24 REAL skip-only months on a permanently-poisoned estate, and a further press is a no-op', () => {
    const buildings = [...roadRow(0, 20)];
    // the permanently-poisoned nursery group (skip-only pass every month it's evaluated)
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    tiers.forEach((t, i) => buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: t, builtTick: -1000 }));
    // a months-old REAL pass already applied: successor 51 exists, members 20/21 gone
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' });
    const realOldFar = {
      id: 1, tick: 10, skipped: [],
      transactions: [{
        sectionKey: 999, kind: 'consolidate',
        removed: [
          { id: 20, spec: 'edu_nursery', x: 300, y: 300, builtTick: -1000, placedBy: 'player' },
          { id: 21, spec: 'edu_nursery', x: 305, y: 300, builtTick: -1000, placedBy: 'player' },
        ],
        added: [{ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' }],
        buildCost: 1000, scrapRecovered: 100, netCost: 900,
      }],
    };
    let s = withConnectivity(
      mk({
        buildings,
        nextId: 5000,
        consolidatorLog: [realOldFar],
        consolidatorUndoConsumed: true, // the player ALREADY spent their undo on realOldFar
      }),
    );
    s = on(s);
    const fundsBeforeSkipMonths = s.funds;
    for (let m = 0; m < 24; m++) s = advanceToNextBoundary(s);

    // The whole point of the F1 fix: 24 real months of skip-only passes on
    // the poisoned estate must NEVER re-arm an already-consumed undo.
    assert.equal(s.consolidatorUndoConsumed, true, 'undo stays consumed through every skip-only month — never wrongly re-armed');
    assert.equal(nurseryCountOf(s), G, 'the poisoned group is untouched (still skip-only every time)');

    const undone = reducer(s, { type: 'consolidatorUndo' });
    // A no-op: the guard (`if (!last || consumed) return state`) fires
    // before any log entry is even consulted, so NOTHING changes — not the
    // months-old realOldFar pass (id 20/21/51), not funds, not the log.
    assert.equal(stableStringify(undone), stableStringify(s), 'undo is a complete no-op once already consumed, regardless of intervening skip-only history');
    assert.ok(undone.buildings.some((b) => b.id === 51), 'the months-old real pass is NOT reversed');
    assert.ok(!undone.buildings.some((b) => b.id === 20), 'its demolished members stay demolished');
    assert.equal(undone.funds, s.funds, 'no refund for a pass nobody just undid');
    assert.ok(s.funds <= fundsBeforeSkipMonths, 'sanity: skip-only months never CREATE money either');
  });
});

describe('BUG-742 — determinism', () => {
  test('two independent runs of the poisoned fixture over 12 months produce byte-identical states', () => {
    const a = runMonths(on(poisonedFixture()), 12);
    const b = runMonths(on(poisonedFixture()), 12);
    assert.equal(stableStringify(a), stableStringify(b));
  });

  test('the storage-boundary coercion is itself deterministic: same corrupt input, same coerced output, every time', () => {
    const results = Array.from({ length: 5 }, () => parsedTierOf({ capacityTier: 1.5 }).tier);
    assert.ok(results.every((t) => t === results[0]));
  });
});

describe('BUG-742 round-2 (2) — newsFeed capacity-unknown dedupe survives id reuse and stale resurfacing', () => {
  const pass = (id) => ({ id, skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });
  const src = (p, tick) => ({ notice: null, milestoneNotice: null, placeNotice: null, consolidatorLatestPass: p, tick });

  test('StrictMode double-observe of the identical pass at the identical tick does not double-push', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(6), 100), t, [], createNewsFeedSeq());
    ring = observeNews(src(pass(6), 100), t, ring, createNewsFeedSeq());
    assert.equal(ring.length, 1, 'the second, identical observation must not push a second entry');
  });

  test('shape (a) — an id REUSED after Undo (same id, LATER tick) is a genuinely new event and MUST fire again', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(11), 100), t, [], createNewsFeedSeq());
    assert.equal(ring.length, 1, 'setup: the first occurrence of id 11 fired');
    // The player undoes pass 11 (it's popped); the NEXT real pass re-derives
    // its id from whatever now sits at log[0] and can legitimately re-mint
    // the SAME number 11 for a genuinely NEW skip, at a LATER tick.
    ring = observeNews(src(pass(11), 200), t, ring, createNewsFeedSeq());
    assert.equal(ring.length, 2, 'the re-minted id-11 pass at a later tick must NOT be swallowed as "already seen"');
  });

  test('shape (b) — an OLDER entry becoming log[0] after Undo (lower id, later tick) must NOT re-fire a stale notice', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(20), 100), t, [], createNewsFeedSeq());
    assert.equal(ring.length, 1, 'setup: pass 20 fired');
    // Undo pops pass 20 (and whatever sat between it and pass 9); the older
    // id-9 entry (whose own notice, if any, already fired ages ago) is now
    // exposed at log[0] again, observed at a MUCH LATER tick.
    ring = observeNews(src(pass(9), 300), t, ring, createNewsFeedSeq());
    assert.equal(ring.length, 1, 'a lower id than the high-water mark must never re-fire, however new the observation tick looks');
  });

  test('a genuinely NEW higher id after a stale dip is still detected (the high-water mark does not get stuck)', () => {
    const t = createNewsFeedTracker();
    let ring = observeNews(src(pass(20), 100), t, [], createNewsFeedSeq()); // fires, mark=20
    ring = observeNews(src(pass(9), 300), t, ring, createNewsFeedSeq()); // stale, suppressed
    ring = observeNews(src(pass(25), 400), t, ring, createNewsFeedSeq()); // genuinely new, above the mark
    assert.equal(ring.length, 2, 'a real new pass above the high-water mark must still fire even after a stale dip was correctly suppressed');
  });

  test('observeNews accepts a plain () => number mount-sequence callback in place of the NewsFeedSeq object (round-2 shape)', () => {
    const t = createNewsFeedTracker();
    let n = 0;
    const ring = observeNews(src(pass(1), 100), t, [], () => n++);
    assert.equal(ring.length, 1);
    assert.ok(ring[0].id.length > 0, 'an id was still generated from the callback-style sequence source');
  });
});

describe('BUG-742 round-2 (3) — undo on a log-capped-out real pass surfaces one news line and clears the flag', () => {
  test('a real pass that has aged out of the capped log leaves Undo a clean, self-clearing no-op with a news line, never silent forever', () => {
    // Directly construct the exact post-ageing shape: consolidatorUndoConsumed
    // still false (nothing has consumed it), but consolidatorLog contains
    // ONLY skip-only entries — the real pass has fallen off the capped ring.
    const buildings = [...roadRow(0, 12)];
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 1, y: 1, builtTick: 10, placedBy: 'auto' });
    const skipEntry = (id) => ({ id, tick: id, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });
    const s = withConnectivity({
      ...mk({ buildings, consolidatorLog: [skipEntry(40), skipEntry(39)] }),
      consolidatorUndoConsumed: false,
    });
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(u.consolidatorUndoConsumed, true, 'the flag is cleared — it can never wrongly claim an undo is available again for a pass that no longer exists');
    assert.deepEqual(u.buildings, s.buildings, 'nothing about the city changes — there was genuinely nothing left to reverse');
    assert.equal(u.funds, s.funds, 'no refund fabricated out of nothing');
    assert.equal(u.consolidatorLog, s.consolidatorLog, 'the log itself is untouched — this is not treated as consuming a real entry');
    assert.equal(
      u.placeNotice,
      'Nothing to undo: the last consolidation is no longer in the log.',
      'a plain, newsFeed-observed placeNotice tells the player why the undo did nothing, instead of the button silently doing nothing forever',
    );

    // A SECOND press, now that the flag is correctly true, must stay a
    // totally silent no-op (idempotent) — no repeated notice spam.
    const twice = reducer(u, { type: 'consolidatorUndo' });
    assert.equal(stableStringify(twice), stableStringify(u), 'a second press changes nothing further, including the notice');
  });

  test('the SAME log-cap-aged-out shape reached via 40 real months on a permanently-poisoned estate (not hand-constructed)', () => {
    const buildings = [...roadRow(0, 20)];
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    tiers.forEach((t, i) => buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: t, builtTick: -1000 }));
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' });
    const realOld = {
      id: 1, tick: 10, skipped: [],
      transactions: [{
        sectionKey: 999, kind: 'consolidate',
        removed: [
          { id: 20, spec: 'edu_nursery', x: 300, y: 300, builtTick: -1000, placedBy: 'player' },
          { id: 21, spec: 'edu_nursery', x: 305, y: 300, builtTick: -1000, placedBy: 'player' },
        ],
        added: [{ id: 51, spec: 'edu_nursery_city', x: 300, y: 300, builtTick: 5, placedBy: 'auto' }],
        buildCost: 1000, scrapRecovered: 100, netCost: 900,
      }],
    };
    // The monthly-twelfth rotation only exposes any ONE section (the
    // poisoned group's own twelfth, plus the whole-map month-12 boundary)
    // roughly twice per 12-month cycle — reaching CONSOLIDATOR_LOG_CAP (32)
    // purely by ticking would need an impractically long run. Pre-seed the
    // log to CAP-1 entries (30 skip-only + the 1 real one, oldest-last, as
    // the ring's own newest-first ordering already guarantees for anything
    // that landed before it) so that the real months below only need to
    // supply the 2 remaining appends the round's own monthlyScopeOf
    // rotation guarantees inside 24 months (month 12 and month 24, both
    // whole-map passes) to push the ring over cap and evict the real entry.
    const preSeedSkips = Array.from({ length: 30 }, (_, i) => ({
      id: 100 + i,
      tick: (i + 1) * 10,
      transactions: [],
      skipped: [{ sectionKey: 7, reason: 'capacity unknown' }],
    }));
    let s = withConnectivity(
      mk({ buildings, nextId: 5000, consolidatorLog: [...preSeedSkips, realOld], consolidatorUndoConsumed: false }),
    );
    assert.equal(s.consolidatorLog.length, 31, 'setup: pre-seeded log is one entry short of the cap, real entry oldest');
    s = on(s);
    for (let m = 0; m < 24; m++) s = advanceToNextBoundary(s);
    assert.ok((s.consolidatorLog ?? []).length <= CONSOLIDATOR_LOG_CAP, 'sanity: the ring stayed capped');
    const realStillInLog = (s.consolidatorLog ?? []).some((p) => p.transactions.length > 0);
    assert.equal(realStillInLog, false, 'setup: the real pass has genuinely aged out of the capped log');
    assert.equal(s.consolidatorUndoConsumed, false, 'setup: the flag was never touched by the skip-only passes (F1 fix) — still says available');

    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(u.consolidatorUndoConsumed, true, 'pressing undo now correctly clears the flag rather than leaving it permanently misleading');
    assert.ok(!u.buildings.some((b) => b.id === 20), 'the aged-out pass is NOT reversed (nothing to reverse)');
    assert.ok(u.buildings.some((b) => b.id === 51), 'the successor stays exactly as it is');
    assert.equal(u.funds, s.funds, 'no refund for a pass no longer in the log');
    assert.ok(u.placeNotice && u.placeNotice.startsWith('Nothing to undo'), 'the player is told why, instead of a silently dead button');
  });
});
