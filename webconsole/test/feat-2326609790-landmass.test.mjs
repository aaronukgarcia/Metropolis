// feat-2326609790-landmass.test.mjs — FEAT-2326609790 (Aaron, 2026-09-05,
// verbatim: "double the land mass we need more room now").
//
// Covers the acceptance criteria for the grid resize (440x260 -> 624x368,
// grown EAST and SOUTH only):
//   1. SSOT: MAP_W/MAP_H are the SAME values whether read via data.ts,
//      consolidator.ts, or grid.ts directly.
//   2. Genesis linework (m20/rail/hs1) spans the FULL new width, including
//      the new eastern extension past the old 440-tile boundary.
//   3. A building at (600,350) is a valid placement; a building at
//      (624,368) is rejected as out of bounds (MAP_W/MAP_H are exactly
//      624/368, so x=624/y=368 are one past the last valid index).
//   4. Save compatibility: a savepoint captured under the ORIGINAL 440x260
//      grid (gridW/gridH stamped, or entirely unstamped like a genuinely
//      legacy save) loads unchanged on this 624x368 build — every
//      coordinate byte-for-byte identical, nothing clamped or translated.
//   5. Save compatibility, the dangerous direction: a savepoint stamped
//      with a LARGER grid than this build defines is refused loudly
//      (MET-V873), never silently truncated.
//   6. Road connectivity reaches the new east edge.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { MAP_W as DATA_MAP_W, MAP_H as DATA_MAP_H, computeRoadConnectivity } from '../src/sim/data.ts';
import { MAP_W as CONSOLIDATOR_MAP_W, MAP_H as CONSOLIDATOR_MAP_H } from '../src/sim/consolidator.ts';
import { MAP_W as GRID_MAP_W, MAP_H as GRID_MAP_H } from '../src/sim/grid.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { createSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail, savepointKey } from '../src/sim/replay.ts';
import { encode } from '../src/sim/saveCodec.ts';
import { buildGameSave, parseGameSave } from '../src/sim/gamesave.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import { RAIL_BRANCH_BUDGET } from '../src/sim/railConnect.ts';

const MET_V873 = 'MET-V873';

// ── (1) SSOT ─────────────────────────────────────────────────────────────

test('SSOT: MAP_W/MAP_H are identical whether imported from data.ts, consolidator.ts, or grid.ts', () => {
  assert.equal(DATA_MAP_W, GRID_MAP_W);
  assert.equal(DATA_MAP_H, GRID_MAP_H);
  assert.equal(CONSOLIDATOR_MAP_W, GRID_MAP_W);
  assert.equal(CONSOLIDATOR_MAP_H, GRID_MAP_H);
});

test('the grid is the doubled 624x368 size (2.01x area of the original 440x260), aspect preserved', () => {
  assert.equal(DATA_MAP_W, 624);
  assert.equal(DATA_MAP_H, 368);
  const originalArea = 440 * 260;
  const newArea = DATA_MAP_W * DATA_MAP_H;
  assert.ok(newArea / originalArea > 2.0 && newArea / originalArea < 2.05, `area ratio ${(newArea / originalArea).toFixed(3)} should be ~2x`);
  const originalAspect = 440 / 260;
  const newAspect = DATA_MAP_W / DATA_MAP_H;
  assert.ok(Math.abs(originalAspect - newAspect) < 0.01, `aspect ratio drifted: ${originalAspect} vs ${newAspect}`);
});

test('RAIL_BRANCH_BUDGET exceeds the whole board cell count (MAP_W*MAP_H), derived, never a stale hand-raised literal', () => {
  // Round finding (opus-round-landmass): reverting RAIL_BRANCH_BUDGET to its
  // pre-resize value of 200000 must go RED here, because 200000 < 624*368 =
  // 229632. This assertion derives the bound from the REAL MAP_W/MAP_H
  // (GR#15) rather than hardcoding today's 229,632, so it stays a live
  // regression gate across any future grid resize too.
  assert.ok(
    RAIL_BRANCH_BUDGET > DATA_MAP_W * DATA_MAP_H,
    `RAIL_BRANCH_BUDGET (${RAIL_BRANCH_BUDGET}) must exceed the whole board's cell count (${DATA_MAP_W * DATA_MAP_H}) or a genuinely-reachable branch on the far side of the map can be wrongly declared blocked`
  );
});

// ── (2) Genesis linework spans the full new width ───────────────────────

test('genesis m20/rail/hs1 linework spans the FULL new width, including the new eastern extension', () => {
  const s = initialState();
  const hasAt = (spec, y, x) => s.buildings.some((b) => b.spec === spec && b.x === x && b.y === y);

  // Sample the far west edge, an interior point PAST the old 440-tile
  // boundary (proves the new land is actually reachable, not just the old
  // sub-rectangle), and the new far east edge.
  const samples = [0, 439, 500, DATA_MAP_W - 1];
  for (const x of samples) {
    assert.ok(hasAt('m20', 56, x), `m20 (north carriageway) missing at x=${x}`);
    assert.ok(hasAt('m20', 58, x), `m20 (south carriageway) missing at x=${x}`);
    assert.ok(hasAt('rail', 84, x), `rail missing at x=${x}`);
    assert.ok(hasAt('hs1', 205, x), `hs1 missing at x=${x}`);
  }
  // And nothing beyond the map's own width.
  assert.ok(!hasAt('m20', 56, DATA_MAP_W), 'm20 must not exist one tile past the map edge');
});

// ── (3) Placement bounds at the new edge ────────────────────────────────

test('a building at (600,350) is a valid placement inside the new 624x368 grid', () => {
  const s = initialState();
  const next = reducer(s, { type: 'place', spec: 'road', x: 600, y: 350 });
  assert.equal(next.buildings.length, s.buildings.length + 1, 'the road must actually be placed');
  const placed = next.buildings.find((b) => b.x === 600 && b.y === 350 && b.spec === 'road');
  assert.ok(placed, 'the placed building must be at exactly (600,350)');
});

test('a building at (624,368) is rejected as out of bounds (MAP_W/MAP_H are exactly 624/368)', () => {
  const s = initialState();
  const next = reducer(s, { type: 'place', spec: 'road', x: DATA_MAP_W, y: DATA_MAP_H });
  assert.equal(next.buildings.length, s.buildings.length, 'nothing must be placed out of bounds');
  assert.match(next.placeNotice ?? '', /out of bounds/i, 'a non-silent notice must explain the rejection');
});

// ── (4) Save compatibility: an old 440x260 savepoint loads unchanged ────

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
  };
}

/**
 * Builds a snapshot that is exactly what the ORIGINAL 440x260 build would
 * have produced: the real initialState() furniture, filtered down to the
 * sub-rectangle x in [0,440) / y in [0,260) that WAS the entire old map.
 * m20/rail/hs1 map furniture pays no upkeep (builtTick<=0), so removing the
 * tiles that only exist because this build's grid is wider does not change
 * funds/flows — the truncated snapshot stays exactly as internally
 * consistent as the untruncated one.
 */
function old440x260Snapshot() {
  const full = initialState();
  const buildings = full.buildings.filter((b) => b.x < 440 && b.y < 260);
  assert.ok(buildings.length < full.buildings.length, 'sanity: truncation must actually drop the new eastern/southern furniture');
  return { ...full, buildings };
}

test('a legacy savepoint with NO gridW/gridH stamp (pre-FEAT-2326609790) loads with every coordinate unchanged', () => {
  const storage = memStorage();
  const snapshot = old440x260Snapshot();
  const sp = createSavepoint(snapshot, [], new Date('2026-09-05T00:00:00.000Z'));
  delete sp.gridW;
  delete sp.gridH;
  storage.setItem(savepointKey(0), encode(JSON.stringify(sp)));

  const result = restoreFromSavepoint(storage);
  assert.equal(result.success, true, `restore must succeed for an unstamped legacy save: ${result.reason}`);
  // Every original building's (x,y) must survive verbatim — no clamp, no
  // translation, exactly the coordinates it was saved with.
  for (const b of snapshot.buildings) {
    const match = result.state.buildings.find((rb) => rb.id === b.id);
    assert.ok(match, `building id=${b.id} must survive restore`);
    assert.equal(match.x, b.x, `building id=${b.id} x must be unchanged`);
    assert.equal(match.y, b.y, `building id=${b.id} y must be unchanged`);
  }
});

test('a savepoint explicitly stamped gridW:440/gridH:260 loads unchanged on this 624x368 build', () => {
  const storage = memStorage();
  const snapshot = old440x260Snapshot();
  const sp = createSavepoint(snapshot, [], new Date('2026-09-05T00:00:00.000Z'));
  sp.gridW = 440;
  sp.gridH = 260;
  storage.setItem(savepointKey(0), encode(JSON.stringify(sp)));

  const result = restoreFromSavepoint(storage);
  assert.equal(result.success, true, `restore must succeed for a 440x260-stamped save: ${result.reason}`);
  assert.equal(result.state.buildings.length, snapshot.buildings.length, 'building count must be preserved exactly (expansion is a superset, nothing dropped)');
});

// ── (5) Save compatibility: a LARGER-grid savepoint is refused loudly ───
// Round finding (opus-round-landmass): two of the three MET-V873 gates
// (prepareRestoreForChunkedTail — the PRIMARY boot path — and gamesave.ts's
// File->Open path) were untested, and the one test that existed matched on
// prose OR the code, which passes even if the code is silently dropped.
// Every assertion below now pins the CODE itself, not the prose alternative.

test('restoreFromSavepoint (fallback boot path) refuses a larger-grid savepoint and the reason carries MET-V873', () => {
  const storage = memStorage();
  const snapshot = initialState();
  const sp = createSavepoint(snapshot, [], new Date('2026-09-05T00:00:00.000Z'));
  // Simulate a save produced by some FUTURE build with an even bigger map.
  sp.gridW = DATA_MAP_W + 200;
  sp.gridH = DATA_MAP_H + 200;
  storage.setItem(savepointKey(0), encode(JSON.stringify(sp)));

  const result = restoreFromSavepoint(storage);
  assert.equal(result.success, false, 'a bigger-grid save must be refused, not silently truncated');
  assert.ok(result.reason && result.reason.startsWith(MET_V873), `reason must be PREFIXED with the code ${MET_V873}, got: ${result.reason}`);
});

test('prepareRestoreForChunkedTail (the PRIMARY boot path, store.tsx calls this FIRST) refuses a larger-grid savepoint and the reason carries MET-V873', () => {
  const storage = memStorage();
  const snapshot = initialState();
  const sp = createSavepoint(snapshot, [], new Date('2026-09-05T00:00:00.000Z'));
  sp.gridW = DATA_MAP_W + 200;
  sp.gridH = DATA_MAP_H + 200;
  storage.setItem(savepointKey(0), encode(JSON.stringify(sp)));

  const result = prepareRestoreForChunkedTail(storage);
  assert.equal(result.success, false, 'the primary boot path must also refuse a bigger-grid save, not silently truncate it');
  assert.ok(result.reason && result.reason.startsWith(MET_V873), `reason must be PREFIXED with the code ${MET_V873}, got: ${result.reason}`);
});

test('gamesave.ts File->Open (parseGameSave) refuses a larger-grid savepoint with a thrown error carrying .code === MET-V873', () => {
  const state = initialState();
  const save = buildGameSave({
    state,
    journal: emptyJournal(),
    journalTail: [],
    name: 'test-city.json',
    buildVersion: 'v0.0.0-test',
  });
  // Simulate a save produced by some FUTURE build with an even bigger map.
  save.savepoint.gridW = DATA_MAP_W + 200;
  save.savepoint.gridH = DATA_MAP_H + 200;
  const text = JSON.stringify(save);

  assert.throws(
    () => parseGameSave(text),
    (err) => {
      assert.equal(err.code, MET_V873, `thrown error .code must be exactly ${MET_V873}, got: ${err.code}`);
      return true;
    },
    'File->Open must refuse a bigger-grid save by throwing a MET-V873-coded error, not silently truncating'
  );
});

// ── (6) Road connectivity reaches the new east edge ─────────────────────

test('road connectivity reaches the new east edge, orthogonally adjacent to the m20 trunk line', () => {
  const eastX = DATA_MAP_W - 2; // well inside the new eastern extension
  const buildings = [
    { id: 1, spec: 'm20', x: eastX, y: 56, builtTick: 0 },
    { id: 2, spec: 'm20', x: eastX, y: 58, builtTick: 0 },
    { id: 3, spec: 'road', x: eastX, y: 57, builtTick: 0 }, // touches both m20 rows
  ];
  const s = { ...initialState(), buildings };
  const { connectedRoadTiles } = computeRoadConnectivity(s);
  assert.ok(
    connectedRoadTiles.includes(`${eastX},57`),
    `road tile at the new east edge (${eastX},57) must be connected via the m20 trunk seed`
  );
});
