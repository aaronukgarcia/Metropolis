// debugjson.test.mjs — FEAT-1972079886: full-state debug.json capture.
//
// Run with `npm test` (node --test). buildDebugJson is pure (state + ui in,
// data out), so these tests exercise the exact shipped serializer.
//
// The headline test is COMPLETENESS: it walks the RUNTIME keys of a real
// SimState and asserts every one (a) has a mapping in SIMSTATE_COVERAGE and
// (b) that mapping's dot-path resolves to a property that actually survives
// serialization (resolved against JSON.parse of the emitted text, so a mapping
// whose value is `undefined` — silently dropped by JSON.stringify — also goes
// RED). A future SimState field that nobody serializes fails this test the
// moment it exists. RED-proven by scratch-removing the `lastRewardedLevel`
// line from the builder's sim section: the test fails with
// "path sim.lastRewardedLevel missing".

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildDebugJson,
  debugJsonText,
  SIMSTATE_COVERAGE,
  DEBUG_JSON_FORMAT,
} from '../src/sim/debugjson.ts';
import { EMPTY_MAP_UI } from '../src/sim/uistate.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { powerStats } from '../src/sim/data.ts';

/** Fixed, deterministic non-sim inputs for the builder. */
function testUi(overrides = {}) {
  return {
    appVersion: 'v9.9.9-test',
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: 42, showWater: true },
    errors: [{ at: 1_699_999_999_000, msg: 'test error' }],
    ...overrides,
  };
}

/** Walk a dot-path, distinguishing "present (even if null)" from "missing". */
function resolvePath(obj, path) {
  let cur = obj;
  for (const seg of path.split('.')) {
    if (cur == null || typeof cur !== 'object' || !(seg in cur)) return { present: false };
    cur = cur[seg];
  }
  return { present: true, value: cur };
}

// ---------- completeness: every SimState key is represented ----------

test('COMPLETENESS: every runtime SimState key has a coverage mapping whose path survives into the serialized JSON', () => {
  const state = initialState();
  const dj = buildDebugJson(state, testUi());
  // Resolve against the PARSED emitted text, not the in-memory object, so an
  // `undefined` mapping (dropped by JSON.stringify) cannot pass.
  const parsed = JSON.parse(debugJsonText(dj));

  for (const key of Object.keys(state)) {
    assert.ok(
      Object.prototype.hasOwnProperty.call(SIMSTATE_COVERAGE, key),
      `SimState key "${key}" has NO entry in SIMSTATE_COVERAGE — decide where it lands in debug.json`
    );
    const path = SIMSTATE_COVERAGE[key];
    assert.ok(
      resolvePath(parsed, path).present,
      `SimState key "${key}": coverage path ${path} missing from the serialized debug.json`
    );
  }
});

test('COMPLETENESS: coverage paths resolve even for null-valued fields (notice, clipboard, movingId)', () => {
  const state = initialState();
  assert.equal(state.notice, null, 'precondition: fresh city has no level-up notice');
  assert.equal(state.clipboard, null, 'precondition: fresh city has no clipboard');
  const parsed = JSON.parse(debugJsonText(buildDebugJson(state, testUi())));
  assert.equal(resolvePath(parsed, SIMSTATE_COVERAGE.notice).value, null);
  assert.equal(resolvePath(parsed, SIMSTATE_COVERAGE.clipboard).value, null);
  assert.equal(resolvePath(parsed, SIMSTATE_COVERAGE.movingId).value, null);
});

// ---------- determinism ----------

test('DETERMINISM: identical state + ui inputs produce byte-identical JSON text', () => {
  const state = initialState();
  const a = debugJsonText(buildDebugJson(state, testUi()));
  const b = debugJsonText(buildDebugJson(structuredClone(state), testUi()));
  assert.equal(a, b, 'same inputs must serialize to the same bytes');
});

test('DETERMINISM: generatedAt derives from the frame time, not the wall clock', () => {
  const dj = buildDebugJson(initialState(), testUi({ frameAtMs: 1_700_000_000_000 }));
  assert.equal(dj.meta.generatedAtMs, 1_700_000_000_000);
  assert.equal(dj.meta.generatedAt, new Date(1_700_000_000_000).toISOString());
  assert.equal(dj.snapshotFrame.takenAtMs, 1_700_000_000_000);
});

// ---------- raw numbers ----------

test('RAW NUMBERS: no currency-formatted figure anywhere in the serialized JSON', () => {
  const state = initialState();
  const text = debugJsonText(buildDebugJson(state, testUi()));
  // The Units tab registry legitimately names the unit "pound (£)" — that is
  // data, not formatting. What must never appear is a fmtMoney-style FIGURE:
  // £ (optionally signed/spaced) directly against a digit, e.g. "£12,345".
  assert.ok(!/£\s*-?[\d,]/.test(text), 'debug.json is a machine artefact — no £-formatted figures');
  const parsed = JSON.parse(text);
  assert.equal(typeof parsed.sim.funds, 'number', 'funds is a raw number, not a formatted string');
  assert.equal(typeof parsed.fiscal.overview.treasury, 'number');
  assert.equal(typeof parsed.flows.incomePerTick, 'number');
});

// ---------- structure / content spot checks ----------

test('buildings: list length and count both equal the sim building count; byKind sums to it', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'place', spec: 'res_hut', x: 5, y: 5 });
  const dj = buildDebugJson(s1, testUi());
  assert.equal(dj.buildings.list.length, s1.buildings.length);
  assert.equal(dj.buildings.count, s1.buildings.length);
  const kindSum = Object.values(dj.buildings.byKind).reduce((a, b) => a + b, 0);
  assert.equal(kindSum, s1.buildings.length, 'per-kind counts sum to the total');
  const placed = dj.buildings.list.find((b) => b.spec === 'res_hut');
  assert.ok(placed, 'the placed building appears in the full list');
  assert.equal(placed.x, 5);
  assert.equal(placed.y, 5);
});

test('demand carries power need/capacity in MW matching powerStats', () => {
  const s = initialState();
  const dj = buildDebugJson(s, testUi());
  const pw = powerStats(s);
  assert.equal(dj.demand.power.needMw, pw.need);
  assert.equal(dj.demand.power.capMw, pw.cap);
  assert.equal(dj.demand.services.length, 9, 'all nine service demand indices present');
});

test('info tabs: experience ladder spans levels 1-20; policy rows reflect toggles; milestones evaluated', () => {
  const s0 = initialState();
  const s1 = reducer(s0, { type: 'policy', id: 'recycling' });
  const dj = buildDebugJson(s1, testUi());
  assert.equal(dj.info.experience.ladder.length, 20);
  assert.deepEqual(dj.info.experience.ladder.map((l) => l.level), Array.from({ length: 20 }, (_, i) => i + 1));
  const rec = dj.info.policy.find((p) => p.id === 'recycling');
  assert.equal(rec.on, true, 'toggled policy reads on');
  assert.equal(dj.info.policy.filter((p) => p.on).length, 1);
  assert.equal(dj.info.milestones.all.length, 6);
  assert.ok(Array.isArray(dj.info.milestones.achieved));
});

test('map/ui passthrough: camera, selection, water layer, errors and app version land verbatim', () => {
  const dj = buildDebugJson(initialState(), testUi());
  assert.deepEqual(dj.map.view, { zoom: 3.5, cx: 150, cy: 70 });
  assert.equal(dj.map.selectedBuildingId, 42);
  assert.equal(dj.map.showWater, true);
  assert.deepEqual(dj.errors, [{ at: 1_699_999_999_000, msg: 'test error' }]);
  assert.equal(dj.meta.appVersion, 'v9.9.9-test');
  assert.equal(dj.meta.format, DEBUG_JSON_FORMAT);
});

test('pre-mount map state (EMPTY_MAP_UI) serializes without crashing: null view, no selection', () => {
  const dj = buildDebugJson(initialState(), testUi({ map: EMPTY_MAP_UI }));
  assert.equal(dj.map.view, null);
  assert.equal(dj.map.selectedBuildingId, null);
  assert.equal(dj.map.showWater, false);
});

// ---------- serialized text: valid, complete, compact buildings ----------

test('debugJsonText round-trips: parses back deep-equal to the built object', () => {
  const s = reducer(initialState(), { type: 'place', spec: 'res_hut', x: 5, y: 5 });
  const dj = buildDebugJson(s, testUi());
  assert.deepEqual(JSON.parse(debugJsonText(dj)), JSON.parse(JSON.stringify(dj)),
    'the compact-buildings splice must not alter the data');
});

test('buildings render one compact line each (size guard for large cities)', () => {
  const s = initialState();
  const dj = buildDebugJson(s, testUi());
  const text = debugJsonText(dj);
  const rows = text.split('\n').filter((l) => /^ {6}\{"id":/.test(l));
  assert.equal(rows.length, dj.buildings.count, 'exactly one compact row per building');
});

test('SIZE GUARD: ~7k-building city serializes, round-trips, and reports its byte size', () => {
  const s = initialState();
  const extra = [];
  let id = 1_000_000;
  for (let i = 0; i < 7000; i++) {
    extra.push({ id: id++, spec: 'res_hut', x: (i % 200) + 2, y: Math.floor(i / 200) + 2, builtTick: 0 });
  }
  const big = { ...s, buildings: [...s.buildings, ...extra] };
  const dj = buildDebugJson(big, testUi());
  assert.equal(dj.buildings.list.length, big.buildings.length);
  const text = debugJsonText(dj);
  const bytes = Buffer.byteLength(text, 'utf8');
  const parsed = JSON.parse(text);
  assert.equal(parsed.buildings.list.length, big.buildings.length, 'no building lost at scale');
  // One compact line per building keeps the file linear in the city size, and
  // a building row is ~100 bytes — 8 MB would mean the compact form regressed
  // to fully-indented output (~9 lines/building). Structural bound, not a
  // wall-clock one.
  assert.ok(bytes < 4_000_000, `debug.json at ${big.buildings.length} buildings is ${bytes} bytes — compact form regressed?`);
  console.log(`[size-guard] debug.json at ${big.buildings.length} buildings: ${bytes} bytes (${(bytes / 1024).toFixed(0)} KiB)`);
});
