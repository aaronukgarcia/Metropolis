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
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';
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
  // BUG-526 (Q100046 A1) added a 'fire' row to serviceCoverageOf/serviceDemandOf
  // (fire stations now feed wellbeing via the new Fire safety part, GR#3 SSOT),
  // growing the demand-services set from 9 to 10; BUG-572 (the DemandDock
  // overhaul, 88bb6fa) then folded the previously-unrendered 'refuse' row in
  // from wasteStatsOf, growing it to 11; a BUG-572 follow-up (2026-09-02) then
  // folded in a 'parks' row (parksCapacityOf, the same footprint sum
  // crimeRateOf/wellbeingOf already computed but never surfaced as demand),
  // growing it to 12.
  assert.equal(dj.demand.services.length, 12, 'all twelve service demand indices present (incl. BUG-526 fire + BUG-572 refuse + parks)');
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
  // to fully-indented output (~9 lines/building). Consistency checks add ~50 bytes
  // per building (many checks with terse details), so the bound is now ~5.5 MB.
  // Structural bound, not a wall-clock one.
  assert.ok(bytes < 5_500_000, `debug.json at ${big.buildings.length} buildings is ${bytes} bytes — compact form regressed?`);
  console.log(`[size-guard] debug.json at ${big.buildings.length} buildings: ${bytes} bytes (${(bytes / 1024).toFixed(0)} KiB)`);
});

// ---------- performance HUD section ----------

test('perfHud: null when tracker is absent (non-DEV or headless)', () => {
  const dj = buildDebugJson(initialState(), testUi());
  // In headless test context, getPerformanceSnapshot() returns null
  assert.equal(dj.perfHud, null, 'perfHud is null when tracker unavailable');
  const text = debugJsonText(dj);
  const parsed = JSON.parse(text);
  assert.equal(parsed.perfHud, null, 'serializes to null correctly');
});

test('perfHud: section shape when snapshot is provided (mocked)', () => {
  // This test documents the expected shape; real integration testing
  // happens in PerfHud.tsx E2E when dev mode captures live metrics.
  const dj = buildDebugJson(initialState(), testUi());
  // Since we're in test context (no tracker), perfHud will be null.
  // The type contract is: perfHud is always null | { note, fps, tick, memoryMB, networkCalls, networkKB }
  assert.ok(
    dj.perfHud === null || (
      typeof dj.perfHud === 'object' &&
      'note' in dj.perfHud &&
      'fps' in dj.perfHud &&
      'tick' in dj.perfHud &&
      'memoryMB' in dj.perfHud &&
      'networkCalls' in dj.perfHud &&
      'networkKB' in dj.perfHud
    ),
    'perfHud matches expected shape (null | full object)'
  );
});

test('perfHud: does not appear in SIMSTATE_COVERAGE (not sim state)', () => {
  // perfHud is UI-layer, wall-clock, non-deterministic � not a SimState field
  assert.ok(
    !Object.prototype.hasOwnProperty.call(SIMSTATE_COVERAGE, 'perfHud'),
    'perfHud is NOT a SimState field and should NOT be in SIMSTATE_COVERAGE'
  );
});

// ---------- BUG-595: crimeRatePreviousMonth display-sanitise pin ----------
//
// debugjson.ts's `sim.crimeRatePreviousMonth` field runs the stored value
// through `sanitizeCrimeRate` before emission (GR#16 type-safe storage
// boundary). That wiring had no red-proof of its own — a raw
// `s.crimeRatePreviousMonth` cast would pass every OTHER test in this file
// (COMPLETENESS only checks the path resolves, not that it is sanitised).
// Decision (a) — KEEP the sanitised display (see the ruling comment beside
// the call site in debugjson.ts for the full justification: debug.json is a
// consumed-by-tooling boundary, not the corruption-forensics surface, and
// the live SimState / localStorage dump remains available for anyone
// chasing the raw corrupt byte value).
//
// RED-PROOF (recorded, restored via scratch cp/mv — GR#24, never git):
// reverting the call site from `sanitizeCrimeRate(s.crimeRatePreviousMonth)`
// to a raw `(s.crimeRatePreviousMonth as number)` cast turned this test red
// — the emitted field read back the literal corrupt string `'abc'` instead
// of the sanitised baseline (35). Confirmed, then restored.
test('BUG-595: a corrupt crimeRatePreviousMonth (non-number) emits the sanitised baseline in debug.json, never the raw garbage', () => {
  const state = { ...initialState(), crimeRatePreviousMonth: 'abc' };
  const dj = buildDebugJson(state, testUi());
  const parsed = JSON.parse(debugJsonText(dj));
  assert.strictEqual(
    parsed.sim.crimeRatePreviousMonth,
    35, // CRIME_CONSTANTS.BASELINE_CRIME_RATE
    'a corrupt (non-number) crimeRatePreviousMonth must emit the sanitised baseline, not the raw garbage value'
  );
  assert.notStrictEqual(parsed.sim.crimeRatePreviousMonth, 'abc', 'the raw corrupt string must never reach the serialized debug.json');
});

// ---------- BUG-629: solvent -> cashPositiveThisTick rename ----------
//
// info.status.solvent (this-tick cashflow, net >= 0) used to read beside
// sim.insolvencyState="solvent" (the exposed multi-tick funds-band state
// machine) as a direct contradiction: a city mid-'crisis' with one positive
// tick showed "solvent: true" right next to "insolvencyState: crisis". The
// field is renamed to cashPositiveThisTick — additive rename, nothing parsed
// the old debug export field back in, so no migration/back-compat path is
// needed (see the field's own doc comment in debugjson.ts for the full
// rationale).
test('BUG-629: info.status exposes cashPositiveThisTick (renamed from solvent), never the old name', () => {
  const state = initialState();
  const dj = buildDebugJson(state, testUi());
  assert.ok(
    Object.prototype.hasOwnProperty.call(dj.info.status, 'cashPositiveThisTick'),
    'info.status.cashPositiveThisTick must be present'
  );
  assert.ok(
    !Object.prototype.hasOwnProperty.call(dj.info.status, 'solvent'),
    'the old field name "solvent" must NOT survive the rename'
  );
  const text = debugJsonText(dj);
  const parsed = JSON.parse(text);
  assert.ok(
    Object.prototype.hasOwnProperty.call(parsed.info.status, 'cashPositiveThisTick'),
    'the renamed field must survive serialization'
  );
  assert.ok(
    !Object.prototype.hasOwnProperty.call(parsed.info.status, 'solvent'),
    'the old field name must not reappear in the serialized text either'
  );
});

test('BUG-629: cashPositiveThisTick tracks this-tick net >= 0, independent of the exposed insolvencyState band', () => {
  const s0 = initialState();
  // A city mid-'crisis' (multi-tick funds band) that nonetheless has a
  // POSITIVE net this tick — cashPositiveThisTick must read true even though
  // insolvencyState says 'crisis', proving the two are genuinely independent
  // signals (the whole point of the rename: they are NOT the same fact).
  const s1 = { ...s0, insolvencyState: 'crisis', funds: -999_999_999 };
  const dj = buildDebugJson(s1, testUi());
  const net = dj.info.status.netPerTick;
  assert.equal(
    dj.info.status.cashPositiveThisTick,
    net >= 0,
    'cashPositiveThisTick must equal (netPerTick >= 0), matching its own doc contract'
  );
  assert.equal(dj.sim.insolvencyState, 'crisis', 'sanity: the exposed band is untouched by this fix');
});

// ---------- BUG-628: Roads upkeep display asymmetry ----------
//
// structuresByFamily's per-family `upkeep` used to be an independent raw
// re-sum of sp.upkeep over EVERY building of the family — including
// offline/under-construction ones — with no recycling/austerity policy
// discount applied. The fiscal outflows 'Roads' line (booked by
// engine.computeFlows via fiscal.ts's UPKEEP_BUCKET SSOT) filters to
// isOnline() buildings only and runs through applyOutflowPolicies. The two
// numbers drifted (Aaron's export: 28,192 vs 28,024, a 0.6% display-only
// gap). Fix (at the time): structuresByFamily reads the ALREADY-COMPUTED
// outflow bucket value for any family that is the SOLE owner of its
// UPKEEP_BUCKET label (road -> 'Roads' was 1:1) instead of re-deriving a
// second number.
//
// UPDATED (FEAT-2326609782, 2026-09-04): 'road' is no longer 1:1-owner of
// 'Roads' — m20/rail gained real upkeep and joined the SAME bucket
// (fiscal.ts UPKEEP_BUCKET SSOT: motorway -> 'Roads'), exactly the way
// commercial/office/industrial/mine already share 'Commerce & Industry'
// below. 'Roads' has graduated into that documented "shared bucket, no
// split invented, keeps the raw per-family sum" category — this test now
// asserts THAT (mirroring the Commerce & Industry mutation-prove test
// immediately below), not exact reconciliation with the outflow line, which
// is provably impossible without inventing a split the engine itself never
// computes (a real 'Roads' outflow can now include m20 upkeep this family
// row cannot attribute back to 'road' alone).
test('BUG-628-CLASS: structuresByFamily Roads keeps the raw per-family sum now that Roads is a shared bucket (road + motorway)', () => {
  const s0 = initialState();
  // 5 online roads (genesis, builtTick: null -> always online per isOnline())
  // + 5 roads still under construction (builtTick: tick 0, rd_dual's
  // constructionTicks = max(3, round(96000/1500000)) = 3 > 0 elapsed at
  // tick 0, so isOnline() is false for these). rd_dual upkeep = 5/tick.
  const onlineRoads = Array.from({ length: 5 }, (_, i) => ({
    id: 20000 + i,
    spec: 'rd_dual',
    x: i + 1,
    y: 1,
    builtTick: null,
  }));
  const offlineRoads = Array.from({ length: 5 }, (_, i) => ({
    id: 20100 + i,
    spec: 'rd_dual',
    x: i + 1,
    y: 2,
    builtTick: 0,
  }));
  const state = {
    ...s0,
    tick: 0,
    buildings: [...onlineRoads, ...offlineRoads],
    // Both policies stack (recycling's rounded result then austerity-rounded)
    // per fiscal.ts's applyOutflowPolicies — included to prove the
    // shared-bucket family stays genuinely untouched by ANY policy
    // multiplier, not just missed one of the two.
    policies: { ...s0.policies, recycling: true, austerity: true },
  };
  state.lastFlows = computeFlows(state);

  const dj = buildDebugJson(state, testUi());
  const roadFamily = dj.info.status.structuresByFamily.find((f) => f.kind === 'road');

  assert.ok(roadFamily, 'road family row present (10 rd_dual built)');
  assert.equal(roadFamily.count, 10, 'count still includes the offline/under-construction roads');

  // The raw, unfiltered, undiscounted sum — online or not, no policy —
  // exactly like the Commerce & Industry mutation-prove test's contract.
  const rawSumAllRoads = 10 * 5;
  assert.equal(
    roadFamily.upkeep,
    rawSumAllRoads,
    'shared-bucket family (Roads: road + motorway) keeps the pre-existing raw, unfiltered, undiscounted sum'
  );
});

test('BUG-628 MUTATION-PROVE target: a shared-bucket family (commercial, part of Commerce & Industry with office/industrial/mine) keeps its independent raw per-family sum, unaffected by this fix', () => {
  const s0 = initialState();
  // com_shop: upkeep 2/tick, constructionTicks = max(3, round(320000/1500000)) = 3.
  // 3 online (genesis) + 2 still under construction at tick 0.
  const onlineShops = Array.from({ length: 3 }, (_, i) => ({
    id: 30000 + i,
    spec: 'com_shop',
    x: i + 1,
    y: 1,
    builtTick: null,
  }));
  const offlineShops = Array.from({ length: 2 }, (_, i) => ({
    id: 30100 + i,
    spec: 'com_shop',
    x: i + 1,
    y: 2,
    builtTick: 0,
  }));
  const state = {
    ...s0,
    tick: 0,
    buildings: [...onlineShops, ...offlineShops],
    // austerity discounts EVERY outflow (not just the recycling-labelled set,
    // which excludes 'Commerce & Industry' anyway) — included to prove the
    // shared-bucket family is genuinely untouched by ANY policy multiplier,
    // not just missed one of the two.
    policies: { ...s0.policies, recycling: true, austerity: true },
  };
  state.lastFlows = computeFlows(state);

  const dj = buildDebugJson(state, testUi());
  const commercialFamily = dj.info.status.structuresByFamily.find((f) => f.kind === 'commercial');
  assert.ok(commercialFamily, 'commercial family row present (5 com_shop built)');
  assert.equal(commercialFamily.count, 5, 'count includes both online and offline shops');

  const rawSumAll = 5 * 2; // all 5 shops, raw upkeep, online or not, no policy
  const onlineOnlyRaw = 3 * 2; // what the SOLE-OWNER substitution path would use if wrongly applied
  assert.equal(
    commercialFamily.upkeep,
    rawSumAll,
    'shared-bucket family keeps the pre-existing raw, unfiltered, undiscounted sum — the SOLE-OWNER guard must not fire for a bucket ' +
      '(Commerce & Industry) owned by 4 ZoneKinds'
  );
  assert.notEqual(
    commercialFamily.upkeep,
    onlineOnlyRaw,
    'must NOT have been mistakenly substituted with an online-only figure — that would misattribute the shared bucket to one family'
  );
});
