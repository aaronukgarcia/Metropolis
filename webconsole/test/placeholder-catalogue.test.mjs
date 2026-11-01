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
import { SPECS, PALETTE_FLAT, PH, isPlaceable } from '../src/sim/data.ts';
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

// 2026-11-01 (FEAT-9995385590): the LAST 87 roadmap placeholders became real
// buildings, so the shipped catalogue now holds ZERO placeholder specs. The
// placeholder MECHANISM (never placeable, zero stats, sorted last, never in the sim)
// must stay pinned for future roadmap entries, so these tests run against SYNTHETIC
// placeholders built with the shipped PH() helper and injected into SPECS here.
// (SPECS is a plain mutable record; node --test isolates each file in its own
// process, so the injection cannot leak into another suite.)
const SHIPPED_PLACEHOLDERS = Object.values(SPECS).filter((sp) => sp.placeholder === true).map((sp) => sp.id);

const PLACEHOLDER_IDS = ['ph_syn_power', 'ph_syn_transport', 'ph_syn_industry'];
SPECS.ph_syn_power = PH('ph_syn_power', 'power', 'Synthetic Placeholder Power', 'test-only', 2, 2, '#888888', 'services', 3);
SPECS.ph_syn_transport = PH('ph_syn_transport', 'transport', 'Synthetic Placeholder Transport', 'test-only', 2, 2, '#888888', 'services', 1);
SPECS.ph_syn_industry = PH('ph_syn_industry', 'industrial', 'Synthetic Placeholder Industry', 'test-only', 3, 3, '#888888', 'zones', 5);

test('the shipped catalogue holds ZERO placeholder specs (all 87 roadmap entries are real)', () => {
  assert.deepEqual(SHIPPED_PLACEHOLDERS, [], 'a shipped spec is flagged placeholder:true; either build it or add it to a roadmap lane deliberately');
});

test('the shipped palette lists no placeholder id (nothing greyed-out ships today)', () => {
  const shipped = new Set(SHIPPED_PLACEHOLDERS);
  for (const id of PALETTE_FLAT) assert.equal(shipped.has(id), false, id);
});

test('PH() builds a spec flagged placeholder===true', () => {
  for (const id of PLACEHOLDER_IDS) {
    const sp = SPECS[id];
    assert.ok(sp, `placeholder "${id}" missing from SPECS`);
    assert.equal(sp.placeholder, true, `"${id}" must be flagged placeholder:true`);
  }
});

test('FEAT-1972079901 Three Gorges Dam is a REAL (graduated) mega-power spec', () => {
  const dam = SPECS.pow_hydro;
  assert.ok(dam, 'pow_hydro (Three Gorges Dam) missing');
  // Aaron 2026-09-04: renamed from "Five Gorges Dam" — the id stays pow_hydro.
  assert.equal(dam.name, 'Three Gorges Dam');
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

test('palette has no duplicate ids across families', () => {
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
  for (const id of PLACEHOLDER_IDS) {
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
  for (const id of PLACEHOLDER_IDS) {
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
  const clipboard = { w: 5, h: 5, items: [{ spec: 'res_hut', dx: 0, dy: 0 }, { spec: 'ph_syn_power', dx: 2, dy: 0 }] };
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
    buildings: [...clean.buildings, { id: 999001, spec: 'ph_syn_power', x: 200, y: 120, builtTick: 0 }],
  };
  const report = runConsistencyChecks(dirty);
  assert.ok(report.failures > 0, 'a placeholder building must make the state inconsistent');
  const check = report.checks.find((c) => c.id === 'placeholder.none-in-sim');
  assert.ok(check, 'the universal placeholder check must run');
  assert.equal(check.ok, false, 'the placeholder building must fail the check');
  assert.match(check.detail, /ph_syn_power/, 'the failing detail names the offending placeholder spec');
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
    buildings: [...genesis.buildings, { id: 999003, spec: 'ph_syn_transport', x: 200, y: 120, builtTick: 0 }],
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
