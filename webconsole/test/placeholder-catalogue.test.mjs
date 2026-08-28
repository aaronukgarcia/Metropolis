// placeholder-catalogue.test.mjs — FEAT-1972079877 / FEAT-1972079901:
// greyed-out "coming soon" roadmap placeholders in the build catalogue.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped catalogue
// and the exact shipped placement gate — no copy, no drift.
//
// RED proof (via scratch-copy, NEVER git): making placeholders placeable (delete
// the `if (sp.placeholder) return false;` line in isPlaceable) turns the
// "placeholder is not placeable" test RED. See the report for the cp/mv dance.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, PALETTE, PALETTE_FLAT, isPlaceable } from '../src/sim/data.ts';
import { initialState, specUnlocked, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { restoreFromSavepoint, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';

/** In-memory Web-Storage mock for restore tests. */
function mockStorage(seed = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, v),
    removeItem: (k) => map.delete(k),
  };
}

// The exact roadmap placeholders this feature adds (mirrors the task list).
// FEAT-1972079907 inc1: rd_avenue/rd_dual/rd_aroad GRADUATED to real placeable
// road tiers (roadTier/capacity + real cost), so they are no longer placeholders.
const PLACEHOLDER_IDS = [
  // Network
  'rail_branch',
  // Transport
  'trans_parkride', 'trans_interchange', 'rail_freightyard', 'ev_charging_hub',
  // Estates — FEAT-1972079900 inc1: res_estate/off_businesspark/ind_estate GRADUATED
  // to real placeable estate-scale specs (residents/jobs + real cost/upkeep), so they
  // are no longer placeholders.
  // Retail — FEAT-1972079900 inc1: com_hypermarket GRADUATED to the real out-of-town
  // retail estate.
  'com_discounter', 'com_darkstore',
  // Industry & Farms
  'ind_chemworks', 'ind_refinery', 'ind_fulfilment', 'ind_parcelhub',
  'farm_dairy', 'farm_abattoir', 'harbour_fishing',
  // Mining
  'mine_chalk', 'mine_clay', 'mine_coal',
  // Leisure
  'lei_gym', 'lei_sportsground', 'lei_stables',
  // Power — FEAT-1972079901: pow_hydro (Five Gorges Dam) GRADUATED to a real
  // placeable mega-hydro generator, so it is no longer a placeholder. pow_hvdc
  // (transmission link) and pow_reprocess (fuel reprocessing) stay placeholders.
  'pow_hvdc', 'pow_reprocess',
  // Water & Waste
  // FEAT-1972079906 inc1: waste_depot GRADUATED (collection depot).
  // FEAT-1972079906 inc2: the four PROCESSING specs (waste_landfill /
  // waste_incinerator / waste_recycling / waste_compost) GRADUATED to real
  // placeable buildings (processCapacity + real cost/upkeep), so they are no
  // longer placeholders.
  // Health & Deathcare
  'death_cemetery', 'death_crematorium', 'air_heliport',
  // Fire & Rescue
  'air_fire_helibase',
  // Police & Justice
  'air_police_helibase',
  // Landmarks
  'land_containerport', 'land_ferryterminal', 'land_cern', 'land_gigafactory', 'land_semifab',
];

test('every planned placeholder exists in SPECS with placeholder===true', () => {
  for (const id of PLACEHOLDER_IDS) {
    const sp = SPECS[id];
    assert.ok(sp, `placeholder "${id}" missing from SPECS`);
    assert.equal(sp.placeholder, true, `"${id}" must be flagged placeholder:true`);
  }
});

test('FEAT-1972079901 Five Gorges Dam is a REAL (graduated) mega-power spec', () => {
  const dam = SPECS.pow_hydro;
  assert.ok(dam, 'pow_hydro (Five Gorges Dam) missing');
  assert.equal(dam.name, 'Five Gorges Dam');
  assert.equal(dam.kind, 'power');
  // GRADUATED: no longer a placeholder — it is a real, placeable generator.
  assert.notEqual(dam.placeholder, true);
  assert.ok(dam.mw > 0, 'the dam generates power');
  assert.ok(dam.cost > 0 && dam.upkeep > 0, 'the dam has real build/running costs');
});

test('placeholders carry ZERO / safe sim stats (no cost, upkeep, or capacity)', () => {
  for (const id of PLACEHOLDER_IDS) {
    const sp = SPECS[id];
    assert.equal(sp.cost, 0, `${id}: placeholder cost must be 0`);
    assert.equal(sp.upkeep, 0, `${id}: placeholder upkeep must be 0`);
    // No capacity / production fields that would let it affect the sim.
    for (const field of ['served', 'jobs', 'mw', 'residents', 'children', 'tourism']) {
      assert.equal(sp[field], undefined, `${id}: placeholder must not set ${field}`);
    }
    assert.equal(sp.tag, undefined, `${id}: placeholder must not set a pollution/clean/waste tag`);
  }
});

test('the ~45 placeholders are exactly the new placeholder specs (no stray flags)', () => {
  const flagged = Object.values(SPECS).filter((sp) => sp.placeholder === true).map((sp) => sp.id).sort();
  assert.deepEqual(flagged, [...PLACEHOLDER_IDS].sort(), 'the set of placeholder-flagged specs must match the roadmap list');
  assert.equal(flagged.length, 32, 'expected 32 roadmap placeholders (3 roads graduated in FEAT-1972079907 inc1; waste_depot graduated in FEAT-1972079906 inc1; the 4 waste PROCESSING specs graduated in FEAT-1972079906 inc2; the 4 estates res_estate/off_businesspark/ind_estate/com_hypermarket graduated in FEAT-1972079900 inc1; pow_hydro/Five Gorges Dam graduated in FEAT-1972079901)');
});

test('every placeholder appears in exactly ONE palette family (roadmap is visible)', () => {
  const counts = new Map();
  for (const fam of PALETTE) for (const id of fam.items) counts.set(id, (counts.get(id) ?? 0) + 1);
  for (const id of PLACEHOLDER_IDS) {
    assert.equal(counts.get(id), 1, `placeholder "${id}" must appear in exactly one family`);
  }
  assert.equal(new Set(PALETTE_FLAT).size, PALETTE_FLAT.length, 'no duplicate ids across families');
});

// ---- the placement gate: a placeholder is NEVER placeable ----

test('GATE: a placeholder is NOT placeable even with unlockedAll=true', () => {
  const s = { ...initialState(), unlockedAll: true };
  for (const id of PLACEHOLDER_IDS) {
    const sp = SPECS[id];
    // The underlying unlock gate would say "yes" under god-mode...
    assert.equal(specUnlocked(s, sp), true, `precondition: ${id} is unlockedAll-true under god-mode`);
    // ...but isPlaceable overrides that: placeholders are never placeable.
    assert.equal(isPlaceable(s, sp), false, `${id}: a placeholder must never be placeable`);
  }
});

test('GATE: a real (non-placeholder) spec is unaffected — still placeable per the existing gate', () => {
  const s = { ...initialState(), unlockedAll: true };
  for (const sp of Object.values(SPECS)) {
    if (sp.placeholder) continue;
    // Real specs track specUnlocked exactly (behaviour-preserving).
    assert.equal(isPlaceable(s, sp), specUnlocked(s, sp), `${sp.id}: real spec must follow the existing unlock gate`);
  }
  // And a concrete real spec IS placeable under god-mode.
  assert.equal(isPlaceable(s, SPECS.res_hut), true, 'res_hut (real) is placeable under unlockedAll');
});

test('GATE: without unlockedAll, a real spec follows unlock<=level and a placeholder stays false', () => {
  const s = initialState();
  assert.equal(isPlaceable(s, SPECS.road), specUnlocked(s, SPECS.road), 'road follows the unlock gate');
  for (const id of PLACEHOLDER_IDS) {
    assert.equal(isPlaceable(s, SPECS[id]), false, `${id}: placeholder never placeable at seed level either`);
  }
});

// ---- the AUTHORITATIVE gate: the reducer must refuse to place a placeholder ----
// (The UI isPlaceable() only guards the button; the round proved a placeholder
//  could still be placed by dispatching {type:'place'} directly. The reducer is
//  the gate that keeps it out of the running sim via EVERY path — replay,
//  genesis-replay, debug console included.)

test('REDUCER: dispatching place for a placeholder with unlockedAll does NOT enter the sim (no building, no XP, no spend)', () => {
  // Free empty tile away from the seeded map corner; unlockedAll=true so the ONLY
  // thing that can stop the placement is the placeholder guard itself.
  const s = { ...initialState(), unlockedAll: true };
  for (const id of ['pow_reprocess', 'ind_chemworks', 'land_cern', 'rail_branch', 'ind_refinery']) {
    const sp = SPECS[id];
    assert.equal(specUnlocked(s, sp), true, `precondition: ${id} passes the unlock gate under god-mode`);
    const after = reducer(s, { type: 'place', spec: id, x: 200, y: 120 });
    assert.equal(after.buildings.length, s.buildings.length, `${id}: no building may be added`);
    assert.equal(after.xp, s.xp, `${id}: no XP may be awarded`);
    assert.equal(after.funds, s.funds, `${id}: no funds may be spent`);
    assert.equal(after.nextId, s.nextId, `${id}: nextId must not advance`);
    // Whole state must be untouched — the reducer returns `state` unchanged.
    assert.deepEqual(after, s, `${id}: reducer must return state untouched for a placeholder place`);
  }
});

test('REDUCER: a real spec CAN still be placed on a free tile under unlockedAll (guard is placeholder-specific)', () => {
  const s = { ...initialState(), unlockedAll: true };
  const after = reducer(s, { type: 'place', spec: 'res_hut', x: 200, y: 120 });
  assert.equal(after.buildings.length, s.buildings.length + 1, 'real spec res_hut places (control: guard is not over-broad)');
});

// ---- second insertion path: stampRegion (clone-stamp) ----
// A clone-stamp places every clipboard item into buildings[]. A crafted debug-JSON
// clipboard (setClipboard / a journaled+replayed stampRegion) could smuggle a
// placeholder in. The reducer must refuse the whole stamp.

test('REDUCER: stampRegion with a placeholder clipboard item does NOT enter the sim (whole stamp refused)', () => {
  const s = { ...initialState(), unlockedAll: true };
  for (const id of ['pow_reprocess', 'land_gigafactory', 'rail_freightyard']) {
    const clipboard = { w: 5, h: 5, items: [{ spec: id, dx: 0, dy: 0 }] };
    const after = reducer(s, { type: 'stampRegion', clipboard, x: 200, y: 120 });
    assert.equal(after.buildings.length, s.buildings.length, `${id}: stampRegion must not add a placeholder building`);
    assert.equal(after.xp, s.xp, `${id}: stampRegion must not award XP for a placeholder`);
    assert.equal(after.funds, s.funds, `${id}: stampRegion must not spend for a placeholder`);
    assert.deepEqual(after, s, `${id}: reducer must return state untouched for a placeholder stamp`);
  }
});

test('REDUCER: stampRegion refuses a MIXED clipboard (real + placeholder) all-or-nothing — no partial place', () => {
  const s = { ...initialState(), unlockedAll: true };
  // A real spec next to a placeholder: the presence of the placeholder rejects the
  // entire stamp, so the real one is NOT smuggled in alongside it.
  const clipboard = { w: 5, h: 5, items: [{ spec: 'res_hut', dx: 0, dy: 0 }, { spec: 'pow_reprocess', dx: 2, dy: 0 }] };
  const after = reducer(s, { type: 'stampRegion', clipboard, x: 200, y: 120 });
  assert.deepEqual(after, s, 'a mixed stamp containing any placeholder is rejected whole');
});

test('REDUCER: stampRegion with only REAL clipboard items still stamps (control: guard is not over-broad)', () => {
  const s = { ...initialState(), unlockedAll: true };
  const clipboard = { w: 5, h: 5, items: [{ spec: 'res_hut', dx: 0, dy: 0 }] };
  const after = reducer(s, { type: 'stampRegion', clipboard, x: 200, y: 120 });
  assert.equal(after.buildings.length, s.buildings.length + 1, 'a real clone-stamp still places');
});

// ---- UNIVERSAL CATCH: runConsistencyChecks flags any placeholder building ----
// A placeholder is a valid SPECS entry (colour/dims/family), so every shape check
// passes for it — this is the single authoritative net that declares a placeholder
// building an INVALID state, catching every current AND future insertion path.

test('CONSISTENCY: a placeholder building is flagged as a consistency FAILURE', () => {
  const clean = initialState();
  const before = runConsistencyChecks(clean);
  assert.equal(before.failures, 0, 'precondition: a pristine city is consistent');

  const dirty = {
    ...clean,
    buildings: [...clean.buildings, { id: 999001, spec: 'pow_reprocess', x: 200, y: 120, builtTick: 0 }],
  };
  const report = runConsistencyChecks(dirty);
  assert.ok(report.failures > 0, 'a placeholder building must make the state inconsistent');
  const check = report.checks.find((c) => c.id === 'placeholder.none-in-sim');
  assert.ok(check, 'the universal placeholder check must run');
  assert.equal(check.ok, false, 'the placeholder building must fail the check');
  assert.match(check.detail, /pow_reprocess/, 'the failing detail names the offending placeholder spec');
});

test('CONSISTENCY: real buildings are NOT flagged by the placeholder check (not over-broad)', () => {
  const s = {
    ...initialState(),
    buildings: [...initialState().buildings, { id: 999002, spec: 'res_hut', x: 200, y: 120, builtTick: 0 }],
  };
  const report = runConsistencyChecks(s);
  assert.equal(report.failures, 0, 'a real building must remain consistent');
  const check = report.checks.find((c) => c.id === 'placeholder.none-in-sim');
  assert.ok(check && check.ok === true, 'the aggregate placeholder check passes when all buildings are real');
});

// ---- CLOSE PATH 6: restoreFromSavepoint must not admit a placeholder building ----
// The snapshot's buildings[] is used verbatim (only the journal tail replays through
// the guarded reducer), so a crafted savepoint could smuggle a placeholder straight
// in. The restore filter drops it (drop-and-continue: the legit savepoint still loads).

test('RESTORE: a crafted savepoint with a placeholder building restores with NO placeholder in buildings[]', () => {
  const genesis = initialState();
  const snapshot = {
    ...genesis,
    buildings: [...genesis.buildings, { id: 999003, spec: 'rail_branch', x: 200, y: 120, builtTick: 0 }],
  };
  const savepoint = {
    savedAt: new Date().toISOString(),
    snapshotTick: snapshot.tick,
    snapshot,
    journalTail: [],
  };
  const storage = mockStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(savepoint) });

  const result = restoreFromSavepoint(storage);
  // Drop-and-continue: the legit savepoint still loads successfully...
  assert.equal(result.success, true, `restore should succeed (drop-and-continue); got: ${result.reason}`);
  // ...but the placeholder building is gone.
  assert.ok(
    result.state.buildings.every((b) => SPECS[b.spec]?.placeholder !== true),
    'no placeholder building may survive a restore'
  );
  // Exactly the one placeholder was dropped; the real genesis buildings survive.
  assert.equal(result.state.buildings.length, genesis.buildings.length, 'only the placeholder was dropped');
});
