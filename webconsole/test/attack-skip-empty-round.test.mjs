// attack-skip-empty-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against the skip-empty-land glide estate (consolidatorGlide.ts's `columns`
// field, data.ts's occupiedColumnsOf memo, engine.ts's
// sectionKeysForGlideWindow wiring, MapView.tsx's ants-box + scope-grid
// consumers). The attacker is NOT the author (GR#23 independence amendment).
//
// Priorities per the dispatch brief:
//   1. Determinism/save-load (shuffle, replay) — covered extensively by the
//      author's own consolidator-glide-skip-empty.test.mjs; re-verified here
//      only where a gap was found (mid-sweep genesis-replay via the REAL
//      reducer/tick path, not a hand-rolled fixture).
//   2. Mid-sweep shift pathology: starvation / double-audit quantification.
//   3. THE MEMO: measure fold invocation count over 100 ticks on a static
//      city THROUGH THE REAL advance()/reducer path (not a synthetic call to
//      occupiedColumnsOf) — this is the highest-risk finding class named in
//      the brief (a BUG-642-class regression would mean an O(buildings) fold
//      runs every tick even when nothing was built).
//   4. Footprint truth — grandfathered tunnel footprint override.
//   5. Mutation tests: unsorted columns, memo removed, fallback removed.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { glideGridOf, glideWindowOf, glideWindowForDay } from '../src/sim/consolidatorGlide.ts';
import { occupiedColumnsOf } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH } from '../src/sim/engine.ts';

function cityWithBuildings(buildings) {
  return { ...initialState(), buildings, consolidatorEnabled: true, consolidatorMode: 'glide' };
}

// ===========================================================================
// PRIORITY 3 — THE MEMO, measured through the REAL tick path.
// ===========================================================================

test('MEASURE: turning glide-mode consolidator ON adds only ONE fold worth of buildings-array reads over 100 static ticks, not 100 folds (the memo-per-tick risk class, BUG-642 family)', () => {
  // Total raw array-index-read traffic through the real advance()/reducer
  // tick path is dominated by MANY pre-existing O(buildings) consumers
  // (road connectivity, monitors, and — once enabled — the consolidator's
  // own findOpportunities/sectionIndexOf/buildingByIdOf scans, ALL of which
  // are independently memoised on buildings identity per their own doc
  // comments) — so counting raw reads in isolation is not a clean signal
  // for occupiedColumnsOf specifically (an earlier draft of this test made
  // exactly that mistake and false-alarmed). The clean signal is the DELTA
  // between an OFF baseline (glide-mode work does not run at all) and an ON
  // run over the IDENTICAL static city: if occupiedColumnsOf's memo is
  // intact, that delta is O(buildings) ONCE (the first tick's cold fold),
  // not O(buildings * ticks).
  function countingBuildingsOf(n) {
    const raw = [];
    for (let x = 0; x < n; x++) raw.push({ id: x + 1, spec: 'fire_post', x: x % 400, y: (x * 3) % 200, builtTick: -1000 });
    let reads = 0;
    const proxy = new Proxy(raw, {
      get(target, prop, receiver) {
        if (typeof prop === 'string' && /^\d+$/.test(prop)) reads++;
        return Reflect.get(target, prop, receiver);
      },
    });
    return { proxy, get reads() { return reads; } };
  }

  const N = 500;
  const TICKS = 100;

  const off = countingBuildingsOf(N);
  let sOff = { ...initialState(), buildings: off.proxy, consolidatorEnabled: false };
  for (let i = 0; i < TICKS; i++) sOff = reducer(sOff, { type: 'tick' });

  const on = countingBuildingsOf(N);
  let sOn = { ...initialState(), buildings: on.proxy, consolidatorEnabled: true, consolidatorMode: 'glide' };
  for (let i = 0; i < TICKS; i++) sOn = reducer(sOn, { type: 'tick' });

  assert.equal(sOff.buildings, off.proxy, 'OFF run: buildings array identity must survive 100 no-op ticks (precondition for the memo to have anything to hit on)');
  assert.equal(sOn.buildings, on.proxy, 'ON run: buildings array identity must survive 100 no-op ticks (precondition for the memo to have anything to hit on)');

  const delta = on.reads - off.reads;
  // A per-tick (unmemoised) fold would add ~TICKS * N extra reads (here
  // 100 * 500 = 50,000+). A one-time cold fold adds ~a small constant
  // multiple of N (occupiedColumnsOf's own loop touches each element a
  // handful of times: SPECS lookup, footprintOf, x read). Threshold set at
  // 10*N as a generous margin above a single fold, and two orders of
  // magnitude below what an every-tick regression would produce.
  assert.ok(delta < 10 * N, `enabling glide-mode consolidator added ${delta} buildings-array reads over ${TICKS} ticks at N=${N} — expected O(N) once (~<${10 * N}), got a magnitude consistent with an UNMEMOISED per-tick fold (BUG-642-class regression) if this fails`);
});

test('MUTATION-PROVE: the above test WOULD fail if the memo were removed (calling occupiedColumnsOf-equivalent fold unmemoised every tick)', () => {
  // Scratch re-derivation (GR#24 — never edit the shipped memo to prove
  // this): a naive per-call fold, invoked 100 times over a 200-building
  // array, does 100x the index reads a memoised version would.
  const raw = [];
  for (let x = 0; x < 200; x++) raw.push({ x });
  let indexReads = 0;
  const countingBuildings = new Proxy(raw, {
    get(target, prop, receiver) {
      if (typeof prop === 'string' && /^\d+$/.test(prop)) indexReads++;
      return Reflect.get(target, prop, receiver);
    },
  });
  function unmemoisedFold(buildings) {
    const seen = new Set();
    for (const b of buildings) seen.add(b.x);
    return [...seen].sort((a, b) => a - b);
  }
  for (let i = 0; i < 100; i++) unmemoisedFold(countingBuildings);
  assert.ok(indexReads >= 100 * raw.length, `sanity: an unmemoised fold over 100 calls must read the array >= 100x length (got ${indexReads})`);
});

test('MEMO CORRECTNESS UNDER MUTATION: a building placed mid-run correctly invalidates the memo (new buildings array -> fresh fold), not a stale cached column set', () => {
  const buildings1 = [{ id: 1, spec: 'fire_post', x: 5, y: 5, builtTick: -1000 }];
  const s1 = cityWithBuildings(buildings1);
  const cols1 = occupiedColumnsOf(s1);
  assert.deepEqual(cols1, [5]);

  // A genuinely new buildings array (as every reducer 'place' produces via
  // spread) with an ADDITIONAL building must be seen as occupying the new
  // column too — never silently reusing the stale [5]-only cached result.
  const buildings2 = [...buildings1, { id: 2, spec: 'fire_post', x: 99, y: 5, builtTick: -999 }];
  const s2 = { ...s1, buildings: buildings2 };
  const cols2 = occupiedColumnsOf(s2);
  assert.deepEqual(cols2, [5, 99], 'a fresh buildings array reference must recompute, never return the stale single-column cache');
});

// ===========================================================================
// PRIORITY 2 — MID-SWEEP SHIFT PATHOLOGY: starvation / double-audit.
// ===========================================================================

test('MID-SWEEP: a column is never PERMANENTLY starved by a static occupied-column set — every occupied column is visited exactly once per full sweep', () => {
  const buildings = [];
  let id = 1;
  const occupiedXs = [3, 47, 200, 439, 12, 300, 88, 150];
  for (const x of occupiedXs) buildings.push({ id: id++, spec: 'fire_post', x, y: 0, builtTick: -1000 });
  const s = cityWithBuildings(buildings);
  const columns = occupiedColumnsOf(s);
  const sectionTiles = 16;
  const grid = glideGridOf(sectionTiles, columns);

  // Over exactly one full sweep (positionsPerPass days), every x0 that
  // appears must be a member of `grid.columns`, and — since xPositions ==
  // columns.length and the y-loop wraps only after xPositions steps — every
  // column must appear EXACTLY yPositions times (once per row), no more, no
  // less. A skip/double-visit defect would show up as a count != yPositions
  // for some column.
  const visitCounts = new Map();
  for (let day = 0; day < grid.positionsPerPass; day++) {
    const win = glideWindowOf(day, grid);
    visitCounts.set(win.x0, (visitCounts.get(win.x0) ?? 0) + 1);
  }
  assert.equal(visitCounts.size, grid.columns.length, 'every occupied column must be visited at least once per sweep');
  for (const col of grid.columns) {
    assert.equal(visitCounts.get(col), grid.yPositions, `column ${col} should be visited exactly yPositions=${grid.yPositions} times per sweep, got ${visitCounts.get(col)}`);
  }
});

test('MID-SWEEP: a column added DURING a sweep (mid-run mutation) is picked up on the very NEXT call, never starved for a full extra pass', () => {
  // Start with a narrow occupied set, run "day 0" against it, then simulate
  // a building appearing in a brand-new column and confirm the VERY NEXT
  // glide call (which re-derives columns from the caller-supplied, freshly
  // recomputed list — exactly how engine.ts calls occupiedColumnsOf(s) once
  // per tick from the CURRENT s) sees it — no persisted "resume position"
  // that could desync (per the module's own doc comment).
  const colsBefore = [10, 20, 30];
  const gridBefore = glideGridOf(4, colsBefore);
  const winDay0 = glideWindowOf(0, gridBefore);
  assert.equal(winDay0.x0, 10);

  const colsAfter = [10, 20, 25, 30]; // a building appeared at column 25
  const gridAfter = glideGridOf(4, colsAfter);
  const winDay1 = glideWindowOf(1, gridAfter);
  assert.equal(winDay1.x0, 20, 'day 1 against the updated grid should be the second column in the NEW set');
  // Column 25 IS reachable within this same sweep (day 2), not deferred to
  // a later pass.
  const winDay2 = glideWindowOf(2, gridAfter);
  assert.equal(winDay2.x0, 25, 'the newly-occupied column must be reachable within the current sweep, not starved a full pass');
});

test('MID-SWEEP: no column is ever double-audited on CONSECUTIVE days within one sweep (glide advances by exactly one position per day, never revisits before wrapping)', () => {
  const columns = [5, 15, 25, 35, 45];
  const grid = glideGridOf(8, columns);
  const seenThisRow = new Set();
  let lastY0 = -1;
  for (let day = 0; day < grid.xPositions; day++) {
    const win = glideWindowOf(day, grid);
    if (win.y0 !== lastY0) {
      seenThisRow.clear();
      lastY0 = win.y0;
    }
    assert.ok(!seenThisRow.has(win.x0), `day ${day}: column ${win.x0} audited twice within the same row before wrapping`);
    seenThisRow.add(win.x0);
  }
});

// ===========================================================================
// PRIORITY 5 — MUTATION TESTS.
// ===========================================================================

test('MUTATION: unsorted/undeduplicated columns array would break the "visited exactly once per row" invariant (proves the sort+dedup in glideGridOf matters)', () => {
  // glideGridOf ALWAYS sorts+dedups internally; to prove that step is load
  // bearing, bypass it by constructing a GlideGrid object directly (scratch
  // object, not editing production code) with a deliberately unsorted,
  // duplicated columns array, and show the derived xPositions/positionsPerPass
  // math (still trusting `columns.length` as `xPositions`) still produces a
  // valid dense sweep ONLY when the array is a proper set — a duplicate
  // entry silently reduces the number of DISTINCT columns actually visited
  // per row below what `xPositions` claims, corrupting `positionsPerPass`
  // arithmetic invisibly (yPositions computed from the wrong total).
  const badColumns = [30, 10, 10, 20]; // unsorted AND duplicated
  const grid = { sectionTiles: 4, xPositions: badColumns.length, yPositions: 10, positionsPerPass: badColumns.length * 10, columns: badColumns };
  const distinctVisited = new Set();
  for (let day = 0; day < grid.xPositions; day++) {
    const win = glideWindowOf(day, grid);
    distinctVisited.add(win.x0);
  }
  assert.ok(distinctVisited.size < grid.xPositions, 'sanity: an unsorted+duplicated columns array visits FEWER distinct columns than xPositions claims — proves glideGridOf must sort+dedup, which it does (see the shipped implementation)');

  // The REAL glideGridOf, given the same raw (unsorted, duplicated) input as
  // nonEmptyColumns, must correct this.
  const goodGrid = glideGridOf(4, badColumns);
  assert.deepEqual(goodGrid.columns, [10, 20, 30], 'glideGridOf must sort+dedup nonEmptyColumns, not trust caller order');
  assert.equal(goodGrid.xPositions, 3);
});

test('MUTATION: removing the empty-after-clamp [0] fallback would make glideGridOf divide-by-zero / index out-of-range on a genesis city', () => {
  // Scratch re-derivation of what glideGridOf would do WITHOUT the `if
  // (columns.length === 0) columns = [0];` fallback line, to prove that
  // line is load-bearing rather than dead defence-in-depth.
  const nonEmptyColumns = []; // genesis: zero buildings
  const valid = new Set();
  for (const c of nonEmptyColumns) valid.add(c);
  const naiveColumns = Array.from(valid).sort((a, b) => a - b);
  assert.equal(naiveColumns.length, 0, 'sanity: without the fallback, columns would be empty');
  // xPositions would be 0, and glideWindowOf's `dayInPass % grid.xPositions`
  // would be a division by zero (NaN) the very first call.
  const totalWouldBe = naiveColumns.length * 10;
  assert.equal(totalWouldBe, 0, 'sanity: positionsPerPass would be 0 without the fallback -> % 0 -> NaN in glideWindowOf');

  // The REAL glideGridOf must never do this.
  const realGrid = glideGridOf(4, nonEmptyColumns);
  assert.deepEqual(realGrid.columns, [0]);
  assert.ok(Number.isFinite(realGrid.positionsPerPass) && realGrid.positionsPerPass > 0);
  const win = glideWindowOf(0, realGrid);
  assert.ok(Number.isFinite(win.x0) && Number.isFinite(win.y0), 'genesis fallback must never produce NaN');
});

// ===========================================================================
// PRIORITY 1 (gap-fill) — genesis replay THROUGH THE REAL REDUCER with a
// mid-sweep placement, not a hand-rolled fixture (the author's own
// genesis-replay coverage for this feature is at the consolidatorGlide.ts
// pure-function level; this drives the actual engine.ts wiring).
// ===========================================================================

test('GENESIS REPLAY: a journal with a mid-sweep placement (glide-mode consolidator enabled) replays byte-identically live vs replayed', () => {
  let live = cityWithBuildings([
    { id: 1, spec: 'fire_post', x: 5, y: 5, builtTick: -1000 },
  ]);
  const journal = [];
  const record = (action) => {
    journal.push(action);
    live = reducer(live, action);
  };

  for (let i = 0; i < 10; i++) record({ type: 'tick' });
  record({
    type: 'place',
    spec: 'fire_post',
    x: 200,
    y: 5,
  });
  for (let i = 0; i < 20; i++) record({ type: 'tick' });

  // Replay: start from the SAME genesis (initialState + the same manual
  // buildings seed can't be replayed via journal since seeding predates the
  // journal in this synthetic harness) — so replay the journal against an
  // independently-constructed identical starting state.
  let replayed = cityWithBuildings([{ id: 1, spec: 'fire_post', x: 5, y: 5, builtTick: -1000 }]);
  for (const action of journal) replayed = reducer(replayed, action);

  assert.deepEqual(replayed.buildings, live.buildings, 'replayed buildings must match live buildings exactly');
  assert.equal(replayed.tick, live.tick);
  assert.equal(replayed.funds, live.funds);
  assert.deepEqual(replayed.consolidatorLog, live.consolidatorLog, 'consolidator pass log (driven by the glide cursor) must match exactly between live and replayed runs');
});
