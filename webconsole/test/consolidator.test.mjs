// consolidator.test.mjs — FEAT-2326609761 inc1 DISCOVERY + SECTION AUDIT half.
// Covers: sector-grid exhaustive coverage, monthly-rotation partition,
// determinism (identical states + shuffled building order), correctness at
// two hand/scale-verifiable scales via independent re-derivation, GR#21
// purity (grepped), and the ladder/successor-rule/stranded-capacity
// invariants the acceptance doc (docs/planning/acceptance/FEAT-2326609761.md)
// and Aaron's BOW rulings specify. The THIRD scale (Aaron's real 29,831-
// building savepoint) lives in consolidator-real-savepoint.test.mjs — kept
// separate because it depends on a local-machine-only file and is skipped,
// not failed, when absent.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { initialState } from '../src/sim/engine.ts';
import { SPECS, offlineResidentsByReason } from '../src/sim/data.ts';
import {
  TILE_METRES,
  CONSOLIDATOR_SECTION_METRES,
  SECTION_TILES,
  SECTIONS_X,
  SECTIONS_Y,
  TOTAL_SECTIONS,
  MAP_W,
  MAP_H,
  sectionKeyOf,
  sectionOriginOf,
  sectionIndexOf,
  capacityFieldOf,
  capacityOf,
  tilesOf,
  tileDensityOf,
  familyKeyOf,
  isConsolidationSuccessor,
  consolidationLadder,
  monthlyScopeOf,
  twelfthIndexOf,
  findOpportunities,
  currentMonthOpportunities,
  strandedCapacityReport,
  findReconnectionOpportunities,
  topOpportunities,
  actionableStranded,
} from '../src/sim/consolidator.ts';
import { TICKS_PER_MONTH } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function baseState(overrides = {}) {
  const s = initialState();
  return { ...s, buildings: [], nextId: 1, ...overrides };
}

// ---------------------------------------------------------------------------
// §A Section grid — derivation and exhaustive coverage
// ---------------------------------------------------------------------------

test('SECTION_TILES is DERIVED (GR#15), never a bare literal', () => {
  assert.equal(TILE_METRES, 50);
  // Aaron's ruling (2026-09-03): 800m/16x16 tiles, overriding the original
  // "e.g. 200m" example — see consolidator.ts's CONSOLIDATOR_SECTION_METRES
  // comment for the real-savepoint measurement that drove the change (4x4
  // sections never held enough co-located pow_wind for ANY ladder rung; 16x16
  // is the smallest size that does, on the same real city).
  assert.equal(CONSOLIDATOR_SECTION_METRES, 800);
  assert.equal(SECTION_TILES, Math.round(CONSOLIDATOR_SECTION_METRES / TILE_METRES));
  assert.equal(SECTION_TILES, 16);
  // Re-derive with a different section metre value INDEPENDENTLY of the
  // module's own constant (proves the FORMULA, not just today's number).
  assert.equal(Math.round(400 / TILE_METRES), 8);
});

test('section grid dimensions are derived from MAP_W/MAP_H, never hand-computed', () => {
  assert.equal(SECTIONS_X, Math.ceil(MAP_W / SECTION_TILES));
  assert.equal(SECTIONS_Y, Math.ceil(MAP_H / SECTION_TILES));
  assert.equal(TOTAL_SECTIONS, SECTIONS_X * SECTIONS_Y);
  assert.equal(MAP_W, 440);
  assert.equal(MAP_H, 260);
});

test('EXHAUSTIVE: every tile of the real 440x260 map maps to exactly one section, no gaps, no overlaps', () => {
  const touchedKeys = new Set();
  for (let y = 0; y < MAP_H; y++) {
    for (let x = 0; x < MAP_W; x++) {
      const key = sectionKeyOf(x, y);
      assert.ok(key >= 0 && key < TOTAL_SECTIONS, `key ${key} for (${x},${y}) out of range`);
      const { x0, y0, w, h } = sectionOriginOf(key);
      // "no gaps": the tile must fall INSIDE the section's own claimed bounds.
      assert.ok(x >= x0 && x < x0 + w, `tile (${x},${y}) not within its own section's x-range`);
      assert.ok(y >= y0 && y < y0 + h, `tile (${x},${y}) not within its own section's y-range`);
      touchedKeys.add(key);
    }
  }
  // "no overlaps" is structural (sectionKeyOf is a pure function -> one tile,
  // one key, by construction) but confirm every section key in range is
  // actually reachable from SOME real tile (no phantom/unreachable keys).
  assert.equal(touchedKeys.size, TOTAL_SECTIONS, 'every section key 0..TOTAL_SECTIONS-1 must be reachable from some tile');
});

// ---------------------------------------------------------------------------
// §B The monthly rotation (Aaron's ruling 7)
// ---------------------------------------------------------------------------

test('monthly rotation: the 12-way twelfth partition covers every section exactly once', () => {
  const seenBy = new Map(); // key -> twelfth that claimed it
  for (let twelfth = 0; twelfth < 12; twelfth++) {
    const tick = twelfth * TICKS_PER_MONTH; // month index === twelfth for months 0..11
    const scope = monthlyScopeOf(tick);
    assert.equal(scope.twelfth, twelfth);
    const keys = twelfth === 11 ? Array.from({ length: TOTAL_SECTIONS }, (_, i) => i) : scope.sectionKeys;
    if (twelfth < 11) {
      for (const key of keys) {
        assert.ok(!seenBy.has(key), `section ${key} claimed by twelfth ${seenBy.get(key)} AND ${twelfth}`);
        seenBy.set(key, twelfth);
        assert.equal(key % 12, twelfth, 'a section in twelfth T must satisfy key % 12 === T');
      }
    }
  }
  // Months 0..10 (11 twelfths) must together claim every section whose
  // key % 12 !== 11 exactly once; the last twelfth (11) is NOT claimed by
  // the partition — it is covered only by month 12's full-map pass.
  for (let key = 0; key < TOTAL_SECTIONS; key++) {
    if (key % 12 === 11) {
      assert.ok(!seenBy.has(key), `section ${key} (twelfth 11) must not be claimed by months 0..10`);
    } else {
      assert.equal(seenBy.get(key), key % 12);
    }
  }
});

test('month 12 (twelfth index 11) covers the WHOLE map, not just its own twelfth', () => {
  const scope = monthlyScopeOf(11 * TICKS_PER_MONTH);
  assert.equal(scope.twelfth, 11);
  assert.equal(scope.full, true);
  assert.equal(scope.sectionKeys.length, TOTAL_SECTIONS);
  const asSet = new Set(scope.sectionKeys);
  assert.equal(asSet.size, TOTAL_SECTIONS);
});

test('the rotation wraps every 12 months: month 12 and month 24 are both twelfth 11 / full', () => {
  assert.equal(twelfthIndexOf(11 * TICKS_PER_MONTH), 11);
  assert.equal(twelfthIndexOf(23 * TICKS_PER_MONTH), 11);
  assert.equal(twelfthIndexOf(0), 0);
  assert.equal(twelfthIndexOf(12 * TICKS_PER_MONTH), 0);
});

// ---------------------------------------------------------------------------
// §C Determinism (GR#21)
// ---------------------------------------------------------------------------

function tinyFixtureBuildings() {
  // A hand-built, independently-checkable fixture: 4x pow_wind clustered in
  // one section (key 0), 2x res_hut in a DIFFERENT section (offset by a full
  // SECTION_TILES so this stays correct at any ruled section size), all
  // placed long enough ago (no builtTick) to read as online.
  return [
    { id: 1, spec: 'pow_wind', x: 0, y: 0 },
    { id: 2, spec: 'pow_wind', x: 1, y: 0 },
    { id: 3, spec: 'pow_wind', x: 2, y: 0 },
    { id: 4, spec: 'pow_wind', x: 3, y: 0 },
    { id: 5, spec: 'res_hut', x: SECTION_TILES, y: 0 },
    { id: 6, spec: 'res_hut', x: SECTION_TILES + 1, y: 0 },
  ];
}

test('determinism: identical state twice yields byte-identical section audit and opportunities', () => {
  const s = baseState({ buildings: tinyFixtureBuildings(), tick: 0 });
  const idx1 = sectionIndexOf(s);
  const idx2 = sectionIndexOf(s); // SAME object -> memoOnState cache hit, trivially identical
  assert.equal(idx1, idx2, 'memoOnState must return the SAME reference for the same state object');

  const opps1 = JSON.stringify(findOpportunities(s, Array.from(idx1.keys())));
  const opps2 = JSON.stringify(findOpportunities(s, Array.from(idx1.keys())));
  assert.equal(opps1, opps2);
});

test('determinism: shuffled buildings[] order produces the SAME audit and opportunities (no map-range-with-break sensitivity)', () => {
  const buildings = tinyFixtureBuildings();
  const shuffled = [buildings[4], buildings[1], buildings[5], buildings[0], buildings[3], buildings[2]];
  assert.notDeepEqual(buildings.map((b) => b.id), shuffled.map((b) => b.id), 'sanity: the shuffle actually reordered');

  const sA = baseState({ buildings, tick: 0 });
  const sB = baseState({ buildings: shuffled, tick: 0 });

  const auditA = JSON.stringify(
    Array.from(sectionIndexOf(sA).entries()).sort(([ka], [kb]) => ka - kb),
  );
  const auditB = JSON.stringify(
    Array.from(sectionIndexOf(sB).entries()).sort(([ka], [kb]) => ka - kb),
  );
  assert.equal(auditA, auditB, 'section audit must be independent of s.buildings array order');

  const allKeys = Array.from(sectionIndexOf(sA).keys());
  assert.equal(
    JSON.stringify(findOpportunities(sA, allKeys)),
    JSON.stringify(findOpportunities(sB, allKeys)),
  );
});

// ---------------------------------------------------------------------------
// §D GR#21 purity — grepped straight from the source file
// ---------------------------------------------------------------------------

test('GR#21: consolidator.ts contains no Date.now/Math.random/performance.now/localStorage CALLS, and no bare `break` inside a for-of loop', () => {
  const src = fs.readFileSync(path.join(__dirname, '..', 'src', 'sim', 'consolidator.ts'), 'utf8');
  // Strip line comments before scanning for CALL SITES — the module's own
  // header/doc comments legitimately NAME these forbidden APIs while
  // explaining that none of them are ever invoked; only an actual call site
  // (a real regression) should fail this test.
  const codeOnly = src
    .replace(/\/\*[\s\S]*?\*\//g, '') // strip block comments (incl. JSDoc)
    .split('\n')
    .map((line) => line.replace(/\/\/.*$/, '')) // strip line comments
    .join('\n');
  for (const forbidden of ['Date.now(', 'Math.random(', 'performance.now(', 'localStorage.']) {
    assert.ok(!codeOnly.includes(forbidden), `consolidator.ts must never call ${forbidden}`);
  }
  // The "map-range-with-break" blind spot: a `break` STATEMENT (word-boundary
  // `break` followed by `;`, not a substring like "breakdown") anywhere in
  // real code is the historically dangerous pattern when paired with
  // unsorted Map/Set iteration. This module has no `break` statement at all
  // (every early exit is a `continue`/`return`) — the simplest possible
  // proof of absence, checked with comments stripped so a doc comment
  // NAMING the forbidden pattern (as this file's own header does) can't
  // trip the check.
  assert.ok(!/\bbreak\s*;/.test(codeOnly), 'no `break;` statement should appear anywhere in consolidator.ts (code, not comments)');
});

// ---------------------------------------------------------------------------
// §E Ladder / successor rule (AC-7..AC-11)
// ---------------------------------------------------------------------------

test('capacityFieldOf excludes AC-20 protected classes (road/motorway/rail/station/pylon/landmark)', () => {
  assert.equal(capacityFieldOf(SPECS.road), null, 'road has a `capacity` field but must never be treated as a consolidation family');
  assert.equal(capacityFieldOf(SPECS.rd_aroad), null);
  if (SPECS.m20) assert.equal(capacityFieldOf(SPECS.m20), null);
  assert.equal(capacityFieldOf(SPECS.pow_wind), 'mw');
  assert.equal(capacityFieldOf(SPECS.res_hut), 'residents');
});

test('AC-7: familyKeyOf treats tag/stage as load-bearing (wat_clean vs wat_waste, nursery vs primary)', () => {
  if (SPECS.wat_clean && SPECS.wat_waste) {
    assert.notEqual(familyKeyOf(SPECS.wat_clean), familyKeyOf(SPECS.wat_waste));
  }
  if (SPECS.edu_nursery && SPECS.edu_primary) {
    assert.notEqual(familyKeyOf(SPECS.edu_nursery), familyKeyOf(SPECS.edu_primary));
  }
});

test('ASM-1497: hea_eldercare is never a consolidation source or successor of anything', () => {
  if (!SPECS.hea_eldercare) return;
  for (const other of Object.values(SPECS)) {
    if (other.id === 'hea_eldercare') continue;
    assert.equal(isConsolidationSuccessor(SPECS.hea_eldercare, other), false);
    assert.equal(isConsolidationSuccessor(other, SPECS.hea_eldercare), false);
  }
  const ladder = consolidationLadder();
  assert.ok(!ladder.some((r) => r.from === 'hea_eldercare' || r.to === 'hea_eldercare'));
});

test('AC-9: the ladder is derived (never hand-listed) and every entry satisfies AC-8 independently re-checked', () => {
  const ladder = consolidationLadder();
  assert.ok(ladder.length > 0, 'the real catalogue must produce at least one consolidation rung');
  for (const { from, to, groupSize } of ladder) {
    const a = SPECS[from];
    const b = SPECS[to];
    assert.ok(a && b, `${from}/${to} must both exist in SPECS`);
    // Independent re-derivation of AC-8's five rules, using ONLY capacityOf/
    // tileDensityOf/familyKeyOf (not isConsolidationSuccessor itself).
    assert.equal(familyKeyOf(a), familyKeyOf(b), `${from}->${to} must share a family`);
    assert.ok(capacityOf(b) >= 4 * capacityOf(a), `${from}->${to} must replace a group, not a pair`);
    assert.ok(tileDensityOf(b) >= tileDensityOf(a), `${from}->${to} must never lose tile density`);
    const bPollutes = b.tag === 'pollution';
    const aPollutes = a.tag === 'pollution';
    assert.ok(!(bPollutes && !aPollutes), `${from}->${to} must not be a pollution regression`);
    // FLOOR, not ceil (round-1 destructive finding 1, 2026-09-04): the
    // largest group the successor can fully absorb, never a group whose
    // combined capacity exceeds the successor's (see groupSizeOf's doc).
    assert.equal(groupSize, Math.floor(capacityOf(b) / capacityOf(a)), 'groupSize must be derived (floor), never chosen');
    assert.ok(groupSize * capacityOf(a) <= capacityOf(b), `${from}->${to}: capacity must never fall (group capacity must not exceed the successor's)`);
  }
  // Sorted, deterministic output (from asc, then to asc).
  const sorted = ladder.slice().sort((x, y) => (x.from < y.from ? -1 : x.from > y.from ? 1 : x.to < y.to ? -1 : x.to > y.to ? 1 : 0));
  assert.deepEqual(ladder, sorted);
});

// ---------------------------------------------------------------------------
// §F Correctness at scale — independent re-derivation, TWO scales (the third,
// Aaron's real savepoint, lives in consolidator-real-savepoint.test.mjs)
// ---------------------------------------------------------------------------

test('SCALE 1 (tiny, hand-verified): section audit matches hand computation exactly', () => {
  const s = baseState({ buildings: tinyFixtureBuildings(), tick: 0 });
  const idx = sectionIndexOf(s);
  const section0 = idx.get(sectionKeyOf(0, 0));
  assert.ok(section0);
  assert.equal(section0.countBySpec.pow_wind, 4);
  assert.equal(section0.tilesUsed, 4 * tilesOf(SPECS.pow_wind));
  assert.deepEqual(section0.buildingIds, [1, 2, 3, 4]);
  const expectedFamily = familyKeyOf(SPECS.pow_wind);
  assert.equal(section0.capacityByFamily[expectedFamily], 4 * capacityOf(SPECS.pow_wind));

  const section2 = idx.get(sectionKeyOf(SECTION_TILES, 0));
  assert.ok(section2);
  assert.equal(section2.countBySpec.res_hut, 2);
  assert.equal(section2.hasResidents, true);
});

test('SCALE 2 (~13k buildings, scale/fixture.mjs): capacityByFamily sums and building-id partition match an INDEPENDENT brute-force re-derivation', async () => {
  const { buildScaleFixture } = await import('./scale/fixture.mjs');
  const fixture = buildScaleFixture();

  const idx = sectionIndexOf(fixture);

  // Independent re-derivation #1: every building id appears in EXACTLY one
  // section's buildingIds (a partition), computed by a brute-force pass that
  // does NOT reuse sectionIndexOf's own bucketing code.
  const idToSection = new Map();
  for (const b of fixture.buildings) {
    const sx = Math.floor(b.x / SECTION_TILES);
    const sy = Math.floor(b.y / SECTION_TILES);
    idToSection.set(b.id, sy * SECTIONS_X + sx);
  }
  let checked = 0;
  for (const [key, audit] of idx) {
    for (const id of audit.buildingIds) {
      assert.equal(idToSection.get(id), key, `building ${id} bucketed into ${key} but brute-force says ${idToSection.get(id)}`);
      checked++;
    }
  }
  assert.equal(checked, fixture.buildings.length, 'every building must be counted exactly once across all sections');

  // Independent re-derivation #2: total capacity-by-family across all
  // sections equals a plain O(buildings) sum with no section bucketing at all.
  const totalByFamily = {};
  for (const audit of idx.values()) {
    for (const [fam, cap] of Object.entries(audit.capacityByFamily)) {
      totalByFamily[fam] = (totalByFamily[fam] ?? 0) + cap;
    }
  }
  const { capacityAtTier: capacityAtTierFn } = await import('../src/sim/data.ts');
  const bruteForceByFamily = {};
  for (const b of fixture.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const field = capacityFieldOf(sp);
    if (field == null) continue;
    const fam = familyKeyOf(sp);
    // AC-7's field-aware capacity: capacityAtTier only special-cases
    // residents/jobs for an untiered spec (data.ts's own documented
    // fallback) — every other capacity field (mw/served/wasteCapacity/
    // processCapacity/tourism/capacity) needs the DIRECT field read for a
    // spec with no capacityTiers array. This mirrors consolidator.ts's own
    // buildingCapacityOf semantics independently (not by calling it), which
    // IS the AC-7 spec truth, not an implementation-sharing shortcut.
    const tiers = sp.capacityTiers;
    const cap =
      tiers && tiers.length > 0
        ? capacityAtTierFn(sp, b.capacityTier ?? 0)
        : (typeof (sp)[field] === 'number' ? (sp)[field] : 0);
    bruteForceByFamily[fam] = (bruteForceByFamily[fam] ?? 0) + cap;
  }
  assert.deepEqual(totalByFamily, bruteForceByFamily);
});

test('PERF: median fresh sectionIndexOf build on the ~13k-building scale fixture stays well under the real-savepoint bound', async () => {
  const { buildScaleFixture } = await import('./scale/fixture.mjs');
  const fixture = buildScaleFixture();
  const N = 5;
  const times = [];
  for (let i = 0; i < N; i++) {
    const clone = { ...fixture, buildings: fixture.buildings.slice() };
    const t0 = performance.now();
    sectionIndexOf(clone);
    times.push(performance.now() - t0);
  }
  times.sort((a, b) => a - b);
  const median = times[Math.floor(times.length / 2)];
  // 13k buildings is <半 Aaron's real 29,831-building city; bound is a
  // fraction of the real-savepoint SECTION_INDEX_FRESH_BOUND_MS (250ms).
  assert.ok(median < 150, `median fresh sectionIndexOf at 13k buildings: ${median.toFixed(2)}ms, expected < 150ms`);
});

// ---------------------------------------------------------------------------
// §G Stranded capacity (Aaron's mid-build ruling)
// ---------------------------------------------------------------------------

test('stranded capacity: construction / road-adjacent / road-connected are classified into three distinct buckets, cross-checked against the SSOT', () => {
  const tick = 100;
  // Coordinates are spaced in SECTION_TILES multiples (not hardcoded tile
  // offsets) so this test is correct at ANY section size — it must keep
  // passing unmodified when Aaron's ruling moves SECTION_TILES again.
  const buildings = [
    // 1. Fully online: built long ago, road-adjacent AND that road is connected.
    { id: 1, spec: 'res_hut', x: 0, y: 1, builtTick: 0 },
    { id: 2, spec: 'road', x: 0, y: 0, builtTick: 0 },
    // 2. Still under construction (builtTick very recent) — same section as #1 is fine, only the CITY-WIDE total is asserted for this bucket.
    { id: 3, spec: 'res_hut', x: 1, y: 1, builtTick: tick - 1 },
    // 3. Road-adjacent FAIL: built, no road tile touching it anywhere. Placed
    //    TWO full sections away from everything else (x = 2 * SECTION_TILES).
    { id: 4, spec: 'res_hut', x: 2 * SECTION_TILES, y: 0, builtTick: 0 },
    // 4. Road-connected FAIL: built, adjacent to a road tile, but that road
    //    tile is NOT in the connected set. Placed FOUR full sections away —
    //    a THIRD distinct section from both #1/#2/#3 and #4.
    { id: 5, spec: 'res_hut', x: 4 * SECTION_TILES, y: 1, builtTick: 0 },
    { id: 6, spec: 'road', x: 4 * SECTION_TILES, y: 0, builtTick: 0 },
  ];
  const s = baseState({
    buildings,
    tick,
    roadConnectivity: { connectedRoadTiles: ['0,0'] }, // only building 2's road tile is connected
  });

  const idx = sectionIndexOf(s);
  const stranded = Array.from(idx.values()).reduce(
    (acc, a) => ({
      constructionCount: acc.constructionCount + a.stranded.constructionCount,
      constructionCapacity: acc.constructionCapacity + a.stranded.constructionCapacity,
      roadAdjacentFailCount: acc.roadAdjacentFailCount + a.stranded.roadAdjacentFailCount,
      roadAdjacentFailCapacity: acc.roadAdjacentFailCapacity + a.stranded.roadAdjacentFailCapacity,
      roadConnectedFailCount: acc.roadConnectedFailCount + a.stranded.roadConnectedFailCount,
      roadConnectedFailCapacity: acc.roadConnectedFailCapacity + a.stranded.roadConnectedFailCapacity,
    }),
    { constructionCount: 0, constructionCapacity: 0, roadAdjacentFailCount: 0, roadAdjacentFailCapacity: 0, roadConnectedFailCount: 0, roadConnectedFailCapacity: 0 },
  );

  assert.equal(stranded.constructionCount, 1);
  assert.equal(stranded.roadAdjacentFailCount, 1);
  assert.equal(stranded.roadConnectedFailCount, 1);

  const ssot = offlineResidentsByReason(s);
  assert.equal(stranded.constructionCapacity, ssot.construction, 'construction bucket must match offlineResidentsByReason exactly');
  assert.equal(
    stranded.roadAdjacentFailCapacity + stranded.roadConnectedFailCapacity,
    ssot.disconnected,
    'the two road-fail buckets must SUM to offlineResidentsByReason(s).disconnected exactly (GR#3)',
  );

  const report = strandedCapacityReport(s);
  assert.equal(report.totalActionableCapacity, ssot.disconnected);
  assert.equal(report.clusterCount, 2, 'the road-adjacent-fail and road-connected-fail buildings sit in two different sections');
});

test('reconnection opportunities rank strictly ahead of density-consolidation opportunities in topOpportunities', () => {
  const tick = 100;
  // Pick the SMALLEST real groupSize rung off the actual derived ladder (not
  // a hand-picked spec pair) and place exactly that many `from` instances
  // packed into one section — guarantees a REAL consolidation opportunity
  // fires regardless of SECTION_TILES's current value or catalogue tuning,
  // so this test actually exercises both branches instead of skipping one.
  const ladder = consolidationLadder();
  const cheapestRung = ladder.slice().sort((a, b) => a.groupSize - b.groupSize)[0];
  assert.ok(cheapestRung, 'the real catalogue must produce at least one ladder rung to test against');
  const groupSize = cheapestRung.groupSize;
  const tilesPerRow = Math.max(1, Math.floor(Math.sqrt(SECTION_TILES * SECTION_TILES)));
  const buildings = [
    // Stranded (no road anywhere), placed well clear of the consolidation cluster.
    { id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: 0 },
  ];
  // Pack `groupSize` instances of the cheapest rung's `from` spec into a
  // single section far from the stranded building, wrapping rows so a large
  // groupSize still fits within the section's own tile budget.
  const farSectionX = SECTIONS_X > 4 ? 4 * SECTION_TILES : SECTION_TILES; // a different section from id 1
  for (let i = 0; i < groupSize; i++) {
    const dx = i % tilesPerRow;
    const dy = Math.floor(i / tilesPerRow);
    buildings.push({ id: 100 + i, spec: cheapestRung.from, x: farSectionX + dx, y: dy });
  }
  const s = baseState({ buildings, tick, roadConnectivity: { connectedRoadTiles: [] } });
  const allKeys = Array.from(sectionIndexOf(s).keys());
  const top = topOpportunities(s, allKeys, 50);
  const reconnectIdxs = top.map((o, i) => (o.kind === 'reconnect' ? i : -1)).filter((i) => i >= 0);
  const consolidateIdxs = top.map((o, i) => (o.kind === 'consolidate' ? i : -1)).filter((i) => i >= 0);
  assert.ok(reconnectIdxs.length > 0, 'the stranded res_hut must produce a reconnection opportunity');
  assert.ok(consolidateIdxs.length > 0, 'the packed group must produce a consolidation opportunity');
  assert.ok(Math.max(...reconnectIdxs) < Math.min(...consolidateIdxs), 'every reconnection opportunity must outrank every consolidation opportunity');
});

test('actionableStranded excludes the self-resolving construction bucket', () => {
  const b = {
    constructionCount: 5,
    constructionCapacity: 500,
    roadAdjacentFailCount: 2,
    roadAdjacentFailCapacity: 200,
    roadConnectedFailCount: 3,
    roadConnectedFailCapacity: 300,
  };
  assert.equal(actionableStranded(b), 500);
});
