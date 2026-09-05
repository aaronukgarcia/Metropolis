// attack-bug742-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND on BUG-742
// (attacker != author). Probes the GR#16 completeness of the storage-boundary
// coercion, the coercion semantics, the fail-closed engine gates, and the
// undo change.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, capacityAtTier } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { consolidationLadder, buildingCapacityOf } from '../src/sim/consolidator.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import { buildGameSave, gameSaveText, parseGameSave, validateGameSaveObject } from '../src/sim/gamesave.ts';
import { createSavepoint, restoreFromSavepoint, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';
import { recentErrors } from '../src/sim/backend.ts';

const NURSERY = SPECS.edu_nursery;
const RUNG = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city');
const G = RUNG.groupSize;

function memStorage(init = {}) {
  const m = new Map(Object.entries(init));
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => m.set(k, String(v)),
    removeItem: (k) => m.delete(k),
    _map: m,
  };
}

// ---------------------------------------------------------------------------
// A1 — GR#16 COMPLETENESS: does a poisoned capacityTier reach the engine by a
// path OTHER than validateBuildingElement?  The autosave/savepoint restore is
// the DEFAULT boot path (replay.ts readSlot -> `JSON.parse(decode(raw)) as
// Savepoint`), and it never calls the new coercion.
// ---------------------------------------------------------------------------
describe('ATTACK A — storage boundary completeness (GR#16)', () => {
  test('A1: the savepoint (autosave) boot path admits a fractional capacityTier verbatim', () => {
    const base = initialState();
    const state = {
      ...base,
      buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }],
    };
    const sp = createSavepoint(state, [], new Date());
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(sp) });
    const before = recentErrors().length;
    const res = restoreFromSavepoint(storage);
    const delta = recentErrors().length - before;
    console.log('A1 restore result:', res.success, res.reason ?? '');
    if (res.success) {
      const b = res.state.buildings.find((x) => x.id === 1);
      console.log('A1 tier after restore:', b?.capacityTier, 'errors recorded:', delta);
      console.log('A1 buildingCapacityOf:', buildingCapacityOf(NURSERY, b?.capacityTier ?? 0));
    }
  });

  test('A2: the *file* path really does coerce (control for A1)', () => {
    const base = initialState();
    const state = { ...base, buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }] };
    const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date() });
    const parsed = parseGameSave(gameSaveText(save));
    const b = parsed.save.savepoint.snapshot.buildings.find((x) => x.id === 1);
    assert.equal(b.capacityTier, 8, 'control: file path coerces 8.7 -> 8');
  });

  test('A3: round-trip convergence — coerce, re-save, re-load: no drift', () => {
    const base = initialState();
    const state = { ...base, buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }] };
    const s1 = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date('2026-01-01') });
    const p1 = parseGameSave(gameSaveText(s1));
    const t1 = p1.save.savepoint.snapshot.buildings[0].capacityTier;
    const s2 = buildGameSave({ state: p1.save.savepoint.snapshot, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date('2026-01-01') });
    const p2 = parseGameSave(gameSaveText(s2));
    const t2 = p2.save.savepoint.snapshot.buildings[0].capacityTier;
    assert.equal(t1, 8);
    assert.equal(t2, t1, 'idempotent: second round trip does not move the tier again');
  });

  test('A4: coercion direction — does it ever INFLATE the players capacity?', () => {
    const rows = [];
    for (const raw of [8.7, 0.9, -3.2, 1e9, Infinity, -Infinity, NaN, -0.0001]) {
      const base = initialState();
      const state = { ...base, buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: raw }] };
      const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date() });
      const v = validateGameSaveObject(save);
      const coerced = v.savepoint.snapshot.buildings[0].capacityTier;
      rows.push([String(raw), coerced, buildingCapacityOf(NURSERY, coerced), capacityAtTier(NURSERY, raw)]);
    }
    console.log('A4 raw -> coerced -> capacity(coerced) -> capacityAtTier(raw) BEFORE fix:');
    for (const r of rows) console.log('   ', r.join('  |  '));
  });

  test('A5: a no-ladder / unknown spec loses a nonzero tier (information destroyed?)', () => {
    const noLadder = Object.values(SPECS).find((s) => !s.capacityTiers || s.capacityTiers.length === 0);
    console.log('A5 no-ladder spec chosen:', noLadder?.id);
    const base = initialState();
    const state = {
      ...base,
      buildings: [
        { id: 1, spec: noLadder.id, x: 5, y: 5, builtTick: -1000, capacityTier: 4 },
      ],
    };
    const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date() });
    const v = validateGameSaveObject(save);
    console.log('A5 tier 4 on a no-ladder spec ->', v.savepoint.snapshot.buildings[0].capacityTier);
  });
});

// ---------------------------------------------------------------------------
// B — IMPACT of the A1 gap: what does a savepoint-borne poisoned tier do to
// the player's city, beyond the (correctly fail-closed) consolidator?
// ---------------------------------------------------------------------------
import { totalChildrenCapacity } from '../src/sim/data.ts';

describe('ATTACK B — blast radius of a savepoint-borne poisoned tier', () => {
  test('B1: citywide children capacity after an autosave restore', () => {
    const base = initialState();
    const state = {
      ...base,
      buildings: [
        { id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 },
        { id: 2, spec: 'edu_nursery', x: 8, y: 5, builtTick: -1000, capacityTier: 3 },
      ],
    };
    const sp = createSavepoint(state, [], new Date());
    const storage = memStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(sp) });
    const res = restoreFromSavepoint(storage);
    assert.equal(res.success, true);
    const cap = totalChildrenCapacity(res.state);
    console.log('B1 citywide children capacity after restore:', cap);
    console.log('B1 is NaN?', Number.isNaN(cap));
  });

  test('B2: does the poisoned tier survive/propagate through ticking (re-poisoning the next autosave)?', () => {
    const base = initialState();
    let s = {
      ...base,
      buildings: [{ id: 1, spec: 'edu_nursery', x: 5, y: 5, builtTick: -1000, capacityTier: 8.7 }],
    };
    for (let i = 0; i < TICKS_PER_MONTH * 2; i++) s = reducer(s, { type: 'tick' });
    console.log('B2 tier after 2 months of ticks:', s.buildings.find((b) => b.id === 1)?.capacityTier);
  });
});

describe('ATTACK B2 — connected-city blast radius', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  function city(tier) {
    const buildings = [];
    for (let x = 0; x <= 12; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    buildings.push({ id: 1, spec: 'edu_nursery', x: 1, y: 1, builtTick: -1000, capacityTier: tier });
    buildings.push({ id: 2, spec: 'edu_nursery', x: 3, y: 1, builtTick: -1000, capacityTier: 3 });
    return withConn({ ...initialState(), unlockedAll: true, buildings, funds: 1e9 });
  }
  test('B3: a poisoned tier makes citywide children capacity NaN in a CONNECTED city', () => {
    const clean = totalChildrenCapacity(city(8));
    const poisoned = totalChildrenCapacity(city(8.7));
    console.log('B3 clean:', clean, ' poisoned:', poisoned, ' NaN?', Number.isNaN(poisoned));
  });
});

// ---------------------------------------------------------------------------
// D — the undo change (fix item (c)): nearest-entry-with-transactions.
// ---------------------------------------------------------------------------
describe('ATTACK D — undoLastConsolidatorPass', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  const rec = (b) => ({ id: b.id, spec: b.spec, x: b.x, y: b.y, builtTick: b.builtTick, placedBy: 'player', ...(b.capacityTier != null ? { capacityTier: b.capacityTier } : {}) });

  // A city where a REAL pass already merged 2 nurseries (ids 10,11) into a
  // city kindergarten (id 50), and an OLDER real pass merged 2 more (20,21)
  // into id 51 — plus two skip-only entries logged on top.
  function stateWithLog(log, consumed) {
    const buildings = [];
    for (let x = 0; x <= 12; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    buildings.push({ id: 50, spec: 'edu_nursery_city', x: 1, y: 1, builtTick: 10, placedBy: 'auto' });
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 6, y: 1, builtTick: 5, placedBy: 'auto' });
    return withConn({ ...initialState(), unlockedAll: true, funds: 1e9, nextId: 5000, buildings, consolidatorLog: log, consolidatorUndoConsumed: consumed });
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
  const skipEntry = (id) => ({ id, tick: id * 10, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] });
  const realNew = { id: 3, tick: 30, transactions: [txn(removedNew, 50, 1)], skipped: [] };
  const realOld = { id: 1, tick: 10, transactions: [txn(removedOld, 51, 6)], skipped: [] };

  test('D1: [skip, skip, real, real] — undo removes exactly the newest REAL pass and keeps the skips', () => {
    const s = stateWithLog([skipEntry(5), skipEntry(4), realNew, realOld], false);
    const u = reducer(s, { type: 'consolidatorUndo' });
    const ids = u.buildings.map((b) => b.id).sort((a, b) => a - b);
    console.log('D1 building ids after undo:', ids.filter((i) => i < 1000));
    console.log('D1 log ids after undo:', u.consolidatorLog.map((p) => p.id));
    assert.ok(!u.buildings.some((b) => b.id === 50), 'the newest real pass successor is removed');
    assert.ok(u.buildings.some((b) => b.id === 10) && u.buildings.some((b) => b.id === 11), 'its demolished members are restored');
    assert.ok(u.buildings.some((b) => b.id === 51), 'the OLDER pass successor is untouched');
    assert.deepEqual(u.consolidatorLog.map((p) => p.id), [5, 4, 1], 'exactly the reversed entry is removed; skip entries preserved in place');
    assert.equal(u.consolidatorUndoConsumed, true);
  });

  test('D2: REGRESSION — after undoing the newest real pass, a later SKIP-ONLY pass re-arms undo and the new code reverses a STALE real pass', () => {
    // Step 1: the player undoes the newest real pass.
    const s0 = stateWithLog([realNew, realOld], false);
    const s1 = reducer(s0, { type: 'consolidatorUndo' });
    assert.equal(s1.consolidatorUndoConsumed, true);
    assert.deepEqual(s1.consolidatorLog.map((p) => p.id), [1], 'only the OLD real pass remains in the log');
    // Step 2: a later skip-only pass lands (engine.ts line ~3511 resets
    // consolidatorUndoConsumed to false for ANY pass log, skip-only included)
    // and is unshifted onto the log.
    const s2 = { ...s1, consolidatorLog: [skipEntry(9), ...s1.consolidatorLog], consolidatorUndoConsumed: false };
    // Step 3: the player presses Undo again.
    const s3 = reducer(s2, { type: 'consolidatorUndo' });
    console.log('D2 ids after 2nd undo:', s3.buildings.map((b) => b.id).filter((i) => i < 1000));
    console.log('D2 funds delta:', s3.funds - s2.funds);
    const staleReversed = s3.buildings.some((b) => b.id === 20) && !s3.buildings.some((b) => b.id === 51);
    console.log('D2 stale (months-old) pass reversed?', staleReversed);
  });

  test('D3: a corrupt/legacy log entry missing `transactions` ANYWHERE in the log', () => {
    const s = stateWithLog([realNew, { id: 2, tick: 20, skipped: [] }, realOld], false);
    let threw = null;
    try {
      reducer(s, { type: 'consolidatorUndo' });
    } catch (e) {
      threw = e;
    }
    console.log('D3 threw?', threw ? String(threw).slice(0, 120) : 'no');
  });
});

describe('ATTACK D4 — the stale-undo regression can put two buildings on one tile', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  const rec = (b) => ({ id: b.id, spec: b.spec, x: b.x, y: b.y, builtTick: b.builtTick, placedBy: 'player' });
  test('D4', () => {
    const buildings = [];
    for (let x = 0; x <= 20; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    // successor of the OLD pass, at (2,1); the old pass ALSO demolished a
    // member that stood far away at (15,1).
    buildings.push({ id: 51, spec: 'edu_nursery_city', x: 2, y: 1, builtTick: 5, placedBy: 'auto' });
    // the player has since built a NEW nursery on the freed (15,1) tile.
    buildings.push({ id: 60, spec: 'edu_nursery', x: 15, y: 1, builtTick: 40, placedBy: 'player' });
    const realOld = {
      id: 1,
      tick: 10,
      transactions: [{
        sectionKey: 1,
        kind: 'consolidate',
        removed: [rec({ id: 20, spec: 'edu_nursery', x: 2, y: 1, builtTick: -1000 }), rec({ id: 21, spec: 'edu_nursery', x: 15, y: 1, builtTick: -1000 })],
        added: [{ id: 51, spec: 'edu_nursery_city', x: 2, y: 1, builtTick: 5, placedBy: 'auto' }],
        buildCost: 1000, scrapRecovered: 100, netCost: 900,
      }],
      skipped: [],
    };
    const skipOnly = { id: 9, tick: 90, transactions: [], skipped: [{ sectionKey: 7, reason: 'capacity unknown' }] };
    const s = withConn({ ...initialState(), unlockedAll: true, funds: 1e9, nextId: 5000, buildings, consolidatorLog: [skipOnly, realOld], consolidatorUndoConsumed: false });
    const u = reducer(s, { type: 'consolidatorUndo' });
    const at151 = u.buildings.filter((b) => b.x === 15 && b.y === 1);
    console.log('D4 buildings occupying tile (15,1) after the stale undo:', at151.map((b) => `${b.id}:${b.spec}`));
  });
});

describe('ATTACK D5 — end-to-end reachability of the stale undo through the REAL engine', () => {
  const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
  const advanceToNextBoundary = (s) => { let c = s; do { c = reducer(c, { type: 'tick' }); } while (c.tick % TICKS_PER_MONTH !== 0); return c; };
  test('D5: a poisoned (permanently-skipped) estate re-arms undo, and the next press reverses a months-old pass', () => {
    const buildings = [];
    for (let x = 0; x <= 20; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
    // the permanently-poisoned nursery group (skip-only pass every month)
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    tiers.forEach((t, i) => buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: t, builtTick: -1000 }));
    // a months-old REAL pass already applied: successor 51 exists, members 20/21 gone
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
    let s = withConn({
      ...initialState(), unlockedAll: true, funds: 5e8, nextId: 5000, buildings,
      roadMonitors: [], buildingMonitors: [], population: 0, tick: 0,
      consolidatorEnabled: false, consolidatorMode: 'monthly-twelfth',
      xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL), lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
      consolidatorLog: [realOld],
      consolidatorUndoConsumed: true, // the player ALREADY spent their undo
    });
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 24; m++) s = advanceToNextBoundary(s);
    const skipOnlyOnTop = (s.consolidatorLog[0]?.transactions.length ?? 0) === 0;
    console.log('D5 log length:', s.consolidatorLog.length, 'top entry is skip-only:', skipOnlyOnTop, 'undoConsumed re-armed:', s.consolidatorUndoConsumed === false);
    const u = reducer(s, { type: 'consolidatorUndo' });
    const staleReversed = u.buildings.some((b) => b.id === 20) && !u.buildings.some((b) => b.id === 51);
    console.log('D5 stale months-old pass reversed by a single Undo press:', staleReversed, ' funds delta:', u.funds - s.funds);
  });
});

describe('ATTACK E — coercion side effects and save/load determinism', () => {
  test('E1: coerceCapacityTierInPlace mutates the CALLERS object (aliasing)', () => {
    const live = { ...initialState(), buildings: [{ id: 1, spec: 'edu_nursery', x: 1, y: 1, builtTick: -1000, capacityTier: 8.7 }] };
    const save = buildGameSave({ state: live, journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date() });
    validateGameSaveObject(save);
    console.log('E1 live state building tier after validating a save built from it:', live.buildings[0].capacityTier);
  });

  test('E2: determinism across the save/load boundary with a poisoned member', () => {
    const mkCity = () => {
      const buildings = [];
      for (let x = 0; x <= 12; x++) buildings.push({ id: 1000 + x, spec: 'road', x, y: 0, builtTick: -1000 });
      buildings.push({ id: 1, spec: 'edu_nursery', x: 1, y: 1, builtTick: -1000, capacityTier: 8.7 });
      return { ...initialState(), unlockedAll: true, funds: 1e9, buildings };
    };
    const roundTrip = () => {
      const save = buildGameSave({ state: mkCity(), journal: emptyJournal(), journalTail: [], name: 'p', buildVersion: 'v0', now: new Date('2026-01-01') });
      let s = parseGameSave(gameSaveText(save)).save.savepoint.snapshot;
      for (let i = 0; i < TICKS_PER_MONTH; i++) s = reducer(s, { type: 'tick' });
      return stableStringify(s);
    };
    assert.equal(roundTrip(), roundTrip(), 'byte-identical through save -> coerce -> load -> 1 month');
  });
});
