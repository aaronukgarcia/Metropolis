// attack-feat764-inc2-round.test.mjs — FEAT-2326609764 inc2 (DIRTY TRACKING),
// independent destructive round `opus-round-feat764-inc2` (GR#23, attacker !=
// author), 2026-09-11.
//
// ── READ THIS FIRST ─────────────────────────────────────────────────────────
// This file has TWO halves and is DELIBERATELY RED as landed:
//
//   §A REGRESSION PINS — green against the lane as attacked. Each one was
//      proven to RED under a named scratch mutation (never a git command,
//      GR#24). These close the five mutants that SURVIVED the author suites
//      (BUG-1070) and lock the properties that DO hold.
//
//   §B OPEN DEFECT REPRODUCTIONS — RED against the lane as attacked. Each is
//      a minimal, deterministic reproduction of one P1 finding from the
//      round. They are the acceptance test for the rework: the increment is
//      not re-roundable until all four are green.
//        BUG-1064  relocate mid-construction across sectors -> permanent stale
//        BUG-1065  module-global completion schedule -> two timelines corrupt
//        BUG-1066  baseStateOf retains every historical SimState (leak)
//        BUG-1067  advance()'s unconditional empty mark defeats AC-7's null
//                  fail-safe; reset/newGame reports the OLD city's aggregates
//
// Every comparison here is made against the whole-city oracle
// (totalJobsWholeCity / the flag-OFF cold sector index), never against the
// partitioned path itself — the BUG-1019 lesson from the inc1 round.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { initialState, reducer } from '../src/sim/engine.ts';
import { totalJobsWholeCity } from '../src/sim/data.ts';
import {
  sectorKeyOf,
  sectorIndexOf,
  dirtySectorsSince,
  totalJobsPartitioned,
  __resetDirtyTrackingForTest,
  __setPartitionedDerivationsForTest,
} from '../src/sim/sectorPartition.ts';

// ── shared helpers ──────────────────────────────────────────────────────────

/** Canonical, order-independent serialisation of a sector index. */
function serialiseIndex(index) {
  return [...index.keys()]
    .sort((a, b) => a - b)
    .map((k) => {
      const a = index.get(k);
      return `${k}:${a.buildingCount}:${a.jobs}:${a.x0},${a.y0}`;
    })
    .join('|');
}

/**
 * The COLD index for the same state, obtained through the flag-OFF path —
 * a genuinely independent full rebuild (its own memo, its own WeakMap), so
 * this is an oracle and not the partitioned path compared against itself.
 */
function coldIndexOf(s) {
  __setPartitionedDerivationsForTest(false);
  try {
    return sectorIndexOf(s);
  } finally {
    __setPartitionedDerivationsForTest(true);
  }
}

/** Assert the flag-ON incremental answer equals the whole-city oracle. */
function assertNotStale(s, where) {
  __setPartitionedDerivationsForTest(true);
  const incremental = serialiseIndex(sectorIndexOf(s));
  const partitionedJobs = totalJobsPartitioned(s);
  const cold = serialiseIndex(coldIndexOf(s));
  const wholeJobs = totalJobsWholeCity(s);
  assert.equal(partitionedJobs, wholeJobs, `${where}: partitioned totalJobs diverged from the whole-city oracle`);
  assert.equal(incremental, cold, `${where}: the incremental sector index diverged from a cold rebuild`);
}

function freshCity() {
  __resetDirtyTrackingForTest();
  __setPartitionedDerivationsForTest(true);
  return { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
}

/** Road spine across three sectors, then settled so nothing is pending. */
function settledCity() {
  let s = freshCity();
  for (let x = 0; x < 60; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
  for (let t = 0; t < 25; t++) s = reducer(s, { type: 'tick' });
  return s;
}

// ════════════════════════════════════════════════════════════════════════════
// §A REGRESSION PINS — green today, each proven RED under its named mutant
// ════════════════════════════════════════════════════════════════════════════

test("A1 (kills 'FULL stickiness removed'): a later PRECISE mark in the same dispatch can never downgrade a dispatch that already escalated", () => {
  let s = settledCity();
  // placeMany recurses through reduceCore('place', ...) once per tile, sharing
  // ONE accumulator. Tile 1 sits far from any road so autoConnect lays a
  // connector -> naive road rule -> 'FULL'. Tile 2 is road-adjacent, so its
  // own case marks PRECISELY, AFTER the escalation.
  const next = reducer(s, { type: 'placeMany', spec: 'com_market', tiles: [{ x: 100, y: 100 }, { x: 2, y: 11 }] });
  assert.notEqual(next, s, 'sanity: the batch actually placed something');
  assert.equal(
    dirtySectorsSince(next),
    null,
    "a dispatch escalated to FULL must stay FULL — a later precise mark must not downgrade it back to a narrow set",
  );
  assertNotStale(next, 'A1');
});

test("A2 (kills 'stampRegion marks nothing'): a road-free stamp marks exactly its removed + placed sectors, and is never silently unmarked", () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 2, y: 11 });
  for (let t = 0; t < 6; t++) s = reducer(s, { type: 'tick' });
  const next = reducer(s, {
    type: 'stampRegion',
    x: 30,
    y: 12,
    clipboard: { items: [{ spec: 'com_market', dx: 0, dy: 0 }, { spec: 'ind_light', dx: 3, dy: 0 }] },
  });
  assert.notEqual(next, s, 'sanity: the stamp actually landed');
  const dirty = dirtySectorsSince(next);
  assert.notEqual(dirty, null, 'a road-free stamp must produce a PRECISE dirty set, not the full-recompute fallback');
  assert.ok(dirty.has(sectorKeyOf(30, 12)), "the stamp's own landing sector must be marked");
  assertNotStale(next, 'A2');
});

test("A3 (kills 'completion schedule NOT rebuilt on hydrate'): a loaded save's under-construction buildings still dirty their sector when they complete", () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 2, y: 11 }); // ct = 3
  const saved = JSON.parse(JSON.stringify(s));
  // A NEW SESSION loads that save: the schedule this module holds must be
  // empty, because nothing in this session ever placed that building. If the
  // hydrate path does not rebuild the schedule from the loaded buildings, the
  // completion tick marks nothing at all (AC-10's own stated requirement).
  // Reusing the SAME session's schedule here would make the test vacuous —
  // its leftover entry happens to be correct for an identical clone.
  __resetDirtyTrackingForTest();
  __setPartitionedDerivationsForTest(true);
  const loaded = reducer({ ...initialState(), unlockedAll: true }, { type: 'hydrate', state: saved, source: 'load' });
  assert.equal(dirtySectorsSince(loaded), null, 'sanity: a load commits the AC-7 full-recompute fallback');
  let x = loaded;
  for (let t = 0; t < 8; t++) {
    x = reducer(x, { type: 'tick' });
    assertNotStale(x, `A3 tick+${t + 1}`);
  }
  assert.ok(totalJobsWholeCity(x) > 0, 'sanity: the building did finish construction and is online');
});

test("A4 (kills 'upgradedSectorKeys ignored' and 'consolidator escalation removed'): advance() must consume both of its own building-mutating results", async () => {
  // A structural pin, deliberately: driving a real capacity-tier step or a
  // real consolidator pass needs a monthly-boundary city that is far more
  // expensive than the property being protected. Both mutants survived every
  // behavioural suite in the round (BUG-1070) — this is the cheap backstop
  // until inc3's Layer S/G split makes an end-to-end test affordable.
  const { readFile } = await import('node:fs/promises');
  const src = await readFile(new URL('../src/sim/engine.ts', import.meta.url), 'utf8');
  assert.match(
    src,
    /markSectorsDirty\(buildingScale\.upgradedSectorKeys\)/,
    "AC-8's auto-scale tier-change row: advance() must mark the sectors evaluateBuildingMonitors actually stepped",
  );
  assert.match(
    src,
    /if \(s\.buildings !== buildingsBeforeConsolidator\) markAllSectorsDirty\(\)/,
    'a consolidator pass that changed buildings must escalate (it demolishes and builds citywide)',
  );
  assert.match(
    src,
    /if \(s\.buildings !== beforeBuildings\) markAllSectorsDirty\(\)/,
    'sweepOrphanConnects laying connector tiles must escalate',
  );
});

test('A5: with the flag OFF the cold path ignores the side-band entirely — a wrong dirty entry cannot corrupt it', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 2, y: 11 });
  for (let t = 0; t < 6; t++) s = reducer(s, { type: 'tick' });
  __setPartitionedDerivationsForTest(false);
  const cold = serialiseIndex(sectorIndexOf(s));
  // Whatever the side-band says (and inc2 does commit real entries here), the
  // flag-OFF answer must be a pure function of s.buildings — AC-21.
  const recomputed = serialiseIndex(sectorIndexOf({ ...s }));
  assert.equal(cold, recomputed, 'the flag-OFF index must be a pure function of the state, side-band or not');
  __setPartitionedDerivationsForTest(true);
});

// ════════════════════════════════════════════════════════════════════════════
// §B OPEN DEFECT REPRODUCTIONS — RED until the named BUG is fixed
// ════════════════════════════════════════════════════════════════════════════

test('B1 BUG-1064 (P1): relocating a MID-CONSTRUCTION building across sectors must not leave the completion schedule on the old sector', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 2, y: 11 }); // ct = 3
  const b = s.buildings.find((x) => x.x === 2 && x.y === 11);
  assert.ok(b, 'sanity: the building was placed');
  s = reducer(s, { type: 'pickup', id: b.id });
  s = reducer(s, { type: 'relocate', x: 42, y: 11 });
  assert.notEqual(sectorKeyOf(2, 11), sectorKeyOf(42, 11), 'sanity: the move crosses a sector boundary');
  for (let t = 0; t < 8; t++) {
    s = reducer(s, { type: 'tick' });
    assertNotStale(s, `B1 tick+${t + 1}`);
  }
  assert.equal(totalJobsWholeCity(s), 25, 'sanity: the relocated building completed and is online');
});

test('B2 BUG-1065 (P1): two SimStates advancing in ONE process must not consume each other\'s completion-schedule entries', () => {
  const base = settledCity();
  let A = reducer(base, { type: 'place', spec: 'com_market', x: 2, y: 11 });
  let B = reducer(base, { type: 'place', spec: 'com_market', x: 42, y: 11 });
  for (let t = 0; t < 8; t++) {
    A = reducer(A, { type: 'tick' });
    B = reducer(B, { type: 'tick' });
    assertNotStale(A, `B2 timeline A tick+${t + 1}`);
    assertNotStale(B, `B2 timeline B tick+${t + 1}`);
  }
});

test('B3 BUG-1066 (P1): the dirty side-band must not retain every historical SimState', { skip: typeof global.gc !== 'function' ? 'run with --expose-gc' : false }, async () => {
  let s = settledCity();
  let old = reducer(s, { type: 'tick' });
  const ref = new WeakRef(old);
  // `cur` DESCENDS from `old`; every other strong reference to `old` is
  // dropped. Only the feature's own side-band can still be holding it.
  let cur = old;
  old = null;
  s = null;
  for (let t = 0; t < 50; t++) cur = reducer(cur, { type: 'tick' });
  global.gc();
  global.gc();
  await new Promise((r) => setTimeout(r, 50));
  global.gc();
  assert.equal(
    ref.deref(),
    undefined,
    'a SimState 50 ticks in the past must be collectable — baseStateOf.set(next, prev) makes a WeakMap VALUE chain that retains the whole session',
  );
});

test('B4 BUG-1067 (P1): reset / new game must not report the previous city\'s aggregates', () => {
  let s = settledCity();
  for (let y = 11; y <= 14; y++) for (const x of [2, 22, 42]) s = reducer(s, { type: 'place', spec: 'com_market', x, y });
  for (let t = 0; t < 25; t++) s = reducer(s, { type: 'tick' });
  assert.ok(totalJobsWholeCity(s) > 0, 'sanity: the city being wiped had jobs');
  const afterReset = reducer(s, { type: 'reset' });
  assertNotStale(afterReset, 'B4 immediately after reset');
});

// ════════════════════════════════════════════════════════════════════════════
// §C ROUND 2 — `opus-reround-feat764-inc2`, 2026-09-11, worktree lane-764-v2
// cut from main c4477c6. Added by the r2 attacker (still attacker != author).
//
//   C1  OPEN DEFECT REPRODUCTION — RED as landed. BUG-1091 (P1).
//   C2..C9  REGRESSION PINS — green today; each proven RED under a named
//           scratch mutant (cp/sed/mv, md5-verified restore; never a git
//           command — GR#24).
//
// Every comparison is against the whole-city oracle (totalJobsWholeCity /
// the flag-OFF cold sector index), never the partitioned path itself.
// ════════════════════════════════════════════════════════════════════════════

import { constructionTicks, placementCost, SPECS } from '../src/sim/data.ts';

/** constructionTicks 20, jobs 800 — long enough to still be building when a save is taken. */
const SLOW_SPEC = 'com_hypermarket';
const SLOW_CT = constructionTicks(SPECS[SLOW_SPEC]);

function mulberry32(a) {
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('C1 BUG-1091 (P1): a savepoint-restored city (the production boot path) must not go permanently stale', () => {
  // store.tsx's [boot] useReducer INITIALIZER hands the reducer the restored
  // state DIRECTLY (store.tsx:948 `state: sanitizeTreasury(restoreResult.state)`,
  // and the chunked prepareRestoreForChunkedTail branch above it). There is NO
  // 'hydrate' dispatch on that path, so rebuildCompletionScheduleFromScratch
  // never runs and the state has no side-band entry. A plain object copy is an
  // exact stand-in: same buildings, new identity, no WeakMap entry.
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  assert.ok(SLOW_CT > 0 && (SPECS[SLOW_SPEC].jobs ?? 0) > 0, 'fixture sanity: the spec must take time to build AND carry jobs');

  const booted = { ...s };
  assert.equal(dirtySectorsSince(booted), null, 'sanity: a state the reducer has never produced must read null');

  let live = booted;
  for (let t = 0; t < SLOW_CT + 5; t++) live = reducer(live, { type: 'tick' });

  // AC-7's whole promise: fail-safe, never fail-stale. The schedule baseline
  // for a state with no side-band entry is UNKNOWN, not EMPTY — but
  // markSectorsDirty's null->empty-Set promotion turns every quiet tick into a
  // PRECISE "nothing changed", so the completion is never marked.
  assertNotStale(live, 'C1 after booting a savepoint-shaped state and ticking past completion');
});

test('C2 (kills "schedule not copy-on-write"): two timelines branched from ONE state, interleaved A,B,A,B and A,A,B,B', () => {
  for (const interleaved of [true, false]) {
    const base = settledCity();
    let A = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
    let B = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 45, y: 11 });
    if (interleaved) {
      for (let t = 0; t < SLOW_CT + 5; t++) { A = reducer(A, { type: 'tick' }); B = reducer(B, { type: 'tick' }); }
    } else {
      for (let t = 0; t < SLOW_CT + 5; t++) A = reducer(A, { type: 'tick' });
      for (let t = 0; t < SLOW_CT + 5; t++) B = reducer(B, { type: 'tick' });
    }
    assertNotStale(A, `C2 timeline A (${interleaved ? 'ABAB' : 'AABB'})`);
    assertNotStale(B, `C2 timeline B (${interleaved ? 'ABAB' : 'AABB'})`);
  }
});

test('C3: a timeline abandoned mid-construction, then a NEW branch cut from the OLDER state', () => {
  const base = settledCity();
  let abandoned = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  for (let t = 0; t < 10; t++) abandoned = reducer(abandoned, { type: 'tick' });
  abandoned = null; // dropped on the floor, exactly as an undo/redo discard does

  let fork = reducer(base, { type: 'place', spec: SLOW_SPEC, x: 25, y: 11 });
  for (let t = 0; t < SLOW_CT + 5; t++) fork = reducer(fork, { type: 'tick' });
  assertNotStale(fork, 'C3 branch cut from an older state after an abandoned one');
});

test('C4 (kills "reschedule on relocate removed"): relocate a MID-CONSTRUCTION building to ANOTHER sector and leave it there (BUG-1139: no return trip)', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: SLOW_SPEC, x: 5, y: 11 });
  const id = s.buildings[s.buildings.length - 1].id;
  const homeKey = sectorKeyOf(5, 11);
  const awayKey = sectorKeyOf(45, 11);
  assert.notEqual(homeKey, awayKey, 'fixture sanity: the two positions must be in different sectors');

  for (let t = 0; t < 3; t++) s = reducer(s, { type: 'tick' });
  s = reducer(s, { type: 'pickup', id });
  s = reducer(s, { type: 'relocate', x: 45, y: 11 });
  assert.equal(s.buildings.find((b) => b.id === id).x, 45, 'sanity: the relocate must actually have moved the building');

  for (let t = 0; t < SLOW_CT + 5; t++) s = reducer(s, { type: 'tick' });
  assert.ok(totalJobsWholeCity(s) > 0, 'sanity: the relocated building is online by now');
  assertNotStale(s, 'C4 after one cross-sector mid-construction relocate that does not return home');
});

test('C5 (kills "post-consolidator diff removed"): consolidator GLIDE, 300 ticks, ~3k buildings, flag ON, differential every 5 ticks', () => {
  let s = freshCity();
  s = { ...s, funds: 50_000_000_000 };
  const rnd = mulberry32(7);
  for (const y of [10, 20, 30]) for (let x = 0; x < 120; x++) s = reducer(s, { type: 'place', spec: 'road', x, y });
  const specs = ['res_house', 'com_shop', 'ind_plant', 'res_house', 'com_shop', 'off_block'];
  for (let i = 0; i < 1200; i++) {
    const x = 1 + Math.floor(rnd() * 115);
    s = reducer(s, { type: 'place', spec: specs[i % specs.length], x, y: [11, 21, 31][i % 3] });
  }
  s = reducer(s, { type: 'debugXp', amount: 10_000_000 });
  s = reducer(s, { type: 'toggleConsolidator' });
  s = reducer(s, { type: 'setConsolidatorMode', mode: 'glide' });
  assert.equal(s.consolidatorEnabled, true, 'sanity: the consolidator must actually be ON');
  assert.equal(s.consolidatorMode, 'glide', 'sanity: glide mode must be set');
  const buildingsBefore = s.buildings.length;
  const logBefore = (s.consolidatorLog ?? []).length;

  for (let t = 0; t < 300; t++) {
    s = reducer(s, { type: 'tick' });
    if (t % 5 === 0) assertNotStale(s, `C5 tick ${t}`);
  }
  assertNotStale(s, 'C5 final');
  // NON-VACUITY: the run is worthless unless the consolidator really demolished
  // and built. Measured on the lane as attacked: 32 log entries, 3063 -> 3115.
  assert.ok((s.consolidatorLog ?? []).length - logBefore >= 5, 'non-vacuity: the consolidator must have run real passes');
  assert.notEqual(s.buildings.length, buildingsBefore, 'non-vacuity: the consolidator must have changed the building population');
});

test('C6: road sever then reconnect keeps the gated layer honest', () => {
  let s = settledCity();
  s = reducer(s, { type: 'place', spec: 'com_market', x: 5, y: 11 });
  for (let t = 0; t < 25; t++) s = reducer(s, { type: 'tick' });
  assertNotStale(s, 'C6 before the sever');
  const severTile = s.buildings.find((b) => b.x === 20 && b.y === 10);
  assert.ok(severTile, 'fixture sanity: the spine must have a road tile at (20,10) to sever');
  s = reducer(s, { type: 'bulldoze', x: 20, y: 10 });
  for (let t = 0; t < 3; t++) s = reducer(s, { type: 'tick' });
  assertNotStale(s, 'C6 severed');
  s = reducer(s, { type: 'place', spec: 'road', x: 20, y: 10 });
  for (let t = 0; t < 3; t++) s = reducer(s, { type: 'tick' });
  assertNotStale(s, 'C6 reconnected');
});

test('C7 (kills "flag-OFF side-band does work"): with the flag OFF, not one side-band record is ever written', () => {
  __resetDirtyTrackingForTest();
  __setPartitionedDerivationsForTest(false);
  const originalSet = WeakMap.prototype.set;
  let sideBandWrites = 0;
  // A side-band record is structurally identifiable: exactly the three fields
  // SectorSideBand declares. memoOnState's own caches never write that shape.
  WeakMap.prototype.set = function patched(key, value) {
    if (value && typeof value === 'object' && 'dirty' in value && 'baseIndex' in value && 'schedule' in value) sideBandWrites++;
    return originalSet.call(this, key, value);
  };
  try {
    let s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
    for (let x = 0; x < 40; x++) s = reducer(s, { type: 'place', spec: 'road', x, y: 10 });
    for (let x = 0; x < 5; x++) s = reducer(s, { type: 'place', spec: 'com_market', x, y: 11 });
    for (let t = 0; t < 30; t++) s = reducer(s, { type: 'tick' });
  } finally {
    WeakMap.prototype.set = originalSet;
    __setPartitionedDerivationsForTest(true);
  }
  assert.equal(sideBandWrites, 0, 'AC-21 / BUG-1069: the flag-OFF path must do ZERO side-band work, not merely produce the same answer');
});

test('C8 (kills "AC-7 fail-safe removed"): a structuredClone-shaped state reads null and gets a FULL recompute', () => {
  let s = settledCity();
  for (const x of [2, 22, 42]) s = reducer(s, { type: 'place', spec: 'com_market', x, y: 11 });
  for (let t = 0; t < 25; t++) s = reducer(s, { type: 'tick' });
  // Same field values, brand-new object identity and a brand-new buildings
  // array — the shape a worker postMessage / structuredClone / hand-built
  // fixture produces. No side-band entry can exist for it.
  const cloned = { ...s, buildings: s.buildings.map((b) => ({ ...b })) };
  assert.equal(dirtySectorsSince(cloned), null, 'AC-7: an un-dispatched state must read null, never a stale Set');
  assertNotStale(cloned, 'C8 cloned state');
});

test('C9: the NOT_TRACKED cases really do fall back to null, not to a precise empty set', () => {
  // 2026-10-30 re-cut: main made com_hypermarket placementCost 0, so it can no
  // longer drive sellAsset (which returns the state untouched at cost 0 and
  // would make this test vacuous). Use a spec that is genuinely PAID on main,
  // with funds topped up so placement cannot be refused.
  const PAID_SPEC = 'bus_station';
  assert.ok(placementCost(SPECS[PAID_SPEC]) > 0, 'sanity: the sell spec must carry a real placement cost');
  const PAID_CT = constructionTicks(SPECS[PAID_SPEC]);
  let s = { ...settledCity(), funds: 1e13 };
  for (const x of [2, 22, 42]) s = reducer(s, { type: 'place', spec: PAID_SPEC, x, y: 11 });
  for (let t = 0; t < PAID_CT + 5; t++) s = reducer(s, { type: 'tick' });
  const id = s.buildings.find((b) => b.spec === PAID_SPEC).id;
  const sold = reducer(s, { type: 'sellAsset', id });
  assert.notEqual(sold, s, 'sanity: sellAsset must have changed the state');
  assert.equal(dirtySectorsSince(sold), null, 'NOT_TRACKED sellAsset must commit null (full recompute), never a precise set');
  assertNotStale(sold, 'C9 after sellAsset');
  const undone = reducer(sold, { type: 'consolidatorUndo' });
  assertNotStale(undone, 'C9 after consolidatorUndo');
});

// ════════════════════════════════════════════════════════════════════════════
// BUG-1139 — dedicated pin for the BUG-1068 post-consolidator schedule diff
// ════════════════════════════════════════════════════════════════════════════
// C5's 3k-building glide fixture never kills the "diff removed" mutant: it only
// builds roads/short tiers and every completion lands on a tick another FULL
// escalation covers. This fixture is the minimum that DOES: the month-12
// whole-map pass consolidates 8 com_market into a com_mall (constructionTicks
// = 20), which completes 6 ticks later on a QUIET tick. The ONLY thing that can
// mark the successor's sector is the schedule diff in advance().
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { xpForLevel, levelOf, CONSOLIDATOR_UNLOCK_LEVEL, TICKS_PER_MONTH } from '../src/sim/engine.ts';
import { monthlyScopeOf } from '../src/sim/consolidator.ts';

test('C-1068 (kills "BUG-1068 post-consolidator schedule diff removed"): a consolidator-built successor completes on a quiet tick and its sector is re-aggregated', () => {
  __resetDirtyTrackingForTest();
  __setPartitionedDerivationsForTest(true);
  const roads = [];
  for (let x = 0; x <= 80; x++) roads.push({ id: 5000 + x, spec: 'road', x, y: 202, builtTick: -1000 });
  const posts = [];
  for (let i = 0; i < 8; i++) posts.push({ id: 100 + i, spec: 'com_market', x: 16 + 2 * i, y: 200, builtTick: -1000 });
  // Headroom: 6 sections x 7 markets (below the group size of 8, so none of THEM consolidate) keeps
  // the 220-job mall under CONSOLIDATOR_MAX_FAMILY_SHARE (50%) of the commercial family, else the
  // pass is skipped with 'family share ceiling' and nothing is ever built.
  const headroom = [];
  for (let k = 0; k < 6; k++) for (let j = 0; j < 7; j++) headroom.push({ id: 900 + k * 10 + j, spec: 'com_market', x: 96 + 16 * k + 2 * j, y: 240, builtTick: -1000 });
  const base = initialState();
  let s = {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [...roads, ...posts, ...headroom],
    nextId: 9000,
    population: 0,
    funds: 1_000_000_000,
    tick: 0,
    consolidatorEnabled: true,
    consolidatorMode: 'glide',
    consolidatorLog: [],
    consolidatorLayoutEnabled: false,
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
  };
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };

  let boundary = null;
  for (let m = 1; m <= 12; m++) {
    if (monthlyScopeOf(m * TICKS_PER_MONTH).full) { boundary = m * TICKS_PER_MONTH; break; }
  }
  assert.ok(boundary != null, 'sanity: a whole-map boundary exists');
  const fireStationCtTicks = constructionTicks(SPECS.com_mall);
  const postsBefore = s.buildings.filter((b) => b.spec === 'com_market').length;
  const stationsBefore = s.buildings.filter((b) => b.spec === 'com_mall').length;
  for (let t = 0; t < boundary + fireStationCtTicks + 4; t++) {
    s = reducer(s, { type: 'tick' });
    if (s.tick >= boundary) assertNotStale(s, `C-1068 tick ${s.tick}`);
  }
  // NON-VACUITY: the consolidator really consolidated, and the successor really completed.
  assert.ok(s.buildings.filter((b) => b.spec === 'com_market').length < postsBefore, 'non-vacuity: com_markets were consolidated');
  assert.ok(s.buildings.filter((b) => b.spec === 'com_mall').length > stationsBefore, 'non-vacuity: a com_mall successor exists');
  assert.ok(totalJobsWholeCity(s) > 0, 'non-vacuity: city has jobs');
});
