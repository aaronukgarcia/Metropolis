// attack-consolidator-inc1-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND
// (GR#23, attacker != author) on the CONSOLIDATOR inc1 READ-ONLY estate
// (FEAT-2326609761, docs/planning/acceptance/FEAT-2326609761.md +
// BOW rulings). This file proves the round's findings mechanically; the
// verdict and prose write-up live on the BOW item.
//
// SCOPE: this attack targets the "can the numbers lie to Aaron" risk class
// first (his tab, his screen, his money), then the reconnection estimator,
// then geometry/toggle/mailbox/perf/rotation as secondary checks.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { initialState } from '../src/sim/engine.ts';
import { SPECS, placementCost, offlineResidentsByReason } from '../src/sim/data.ts';
import {
  SECTION_TILES,
  SECTIONS_X,
  SECTIONS_Y,
  TOTAL_SECTIONS,
  MAP_W,
  MAP_H,
  sectionKeyOf,
  sectionOriginOf,
  capacityOf,
  tilesOf,
  consolidationLadder,
  familyKeyOf,
  findOpportunities,
  findReconnectionOpportunities,
  strandedCapacityReport,
  monthlyScopeOf,
  CONSOLIDATOR_SCRAP_FRACTION,
} from '../src/sim/consolidator.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CONSOLIDATOR_SRC = path.join(__dirname, '..', 'src', 'sim', 'consolidator.ts');

function baseState(overrides = {}) {
  const s = initialState();
  return { ...s, buildings: [], nextId: 1, ...overrides };
}

// ===========================================================================
// PRIORITY 1 — CAN THE NUMBERS LIE TO AARON?
//
// FINDING (critical): groupSizeOf(a, b) = Math.ceil(capacityOf(b) / capacityOf(a)).
// For any non-exact ratio this picks the SMALLEST n such that n*capA >= capB
// (i.e. AT LEAST as much group capacity as the successor) rather than the
// LARGEST n such that n*capA <= capB (i.e. never more group capacity than the
// successor provides). That is backwards: it guarantees the group's combined
// capacity is >= the successor's capacity whenever the ratio isn't exact,
// which means every non-exact rung LOSES capacity when consolidated — the
// exact opposite of AC-8 rule 4's stated purpose ("never lose density...
// capacity never falls") and the module's own doc comment on
// ConsolidationOpportunity.capacityGis ("always >= 0 by the successor rule").
// findOpportunities then reports this loss as `capacityGain: 0` via a bare
// `Math.max(0, ...)` clamp — hiding the loss rather than surfacing it. This
// is exactly the "can the numbers lie to Aaron" failure mode: the tab would
// show a real net-negative transaction as a free, zero-impact, or even
// gainful one.
// ===========================================================================

test('CRITICAL: the generated ladder must never lose capacity per rung (AC-8 rule 4 / the "capacity never falls" contract) — currently ~55% of rungs DO', () => {
  const ladder = consolidationLadder();
  assert.ok(ladder.length > 0);
  const violations = [];
  for (const { from, to, groupSize } of ladder) {
    const a = SPECS[from];
    const b = SPECS[to];
    const groupTotal = groupSize * capacityOf(a);
    if (groupTotal > capacityOf(b)) {
      violations.push({ from, to, groupSize, groupTotal, successorCapacity: capacityOf(b), lost: groupTotal - capacityOf(b) });
    }
  }
  if (violations.length > 0) {
    console.log(`\n[ATTACK FINDING] ${violations.length}/${ladder.length} ladder rungs LOSE capacity if ever applied:`);
    for (const v of violations) {
      console.log(`  ${v.from} x${v.groupSize} = ${v.groupTotal}  ->  ${v.to} (${v.successorCapacity})  LOST ${v.lost}`);
    }
  }
  // This assertion is EXPECTED TO FAIL today — it documents the defect. The
  // correct fix is groupSizeOf = Math.floor(capacityOf(b) / capacityOf(a))
  // (the largest group whose combined capacity the successor can still
  // cover), not Math.ceil. Left RED deliberately: a green run here would
  // mean the defect was silently "fixed" by loosening this assertion rather
  // than the source.
  assert.deepEqual(violations, [], 'every ladder rung must satisfy groupSize * capacityOf(from) <= capacityOf(to) — capacity must never fall');
});

// POST-FIX (round-1 finding 1 resolved 2026-09-04): groupSizeOf now FLOORS
// (largest n with n*capacityOf(A) <= capacityOf(B)), not ceils. These two
// tests were the round's hand-verified evidence the ceil formula LIED —
// updated here to assert the CORRECTED arithmetic the fix produces (fixing
// the source changed these rungs' true numbers, not just their honesty:
// pow_wind->pow_offshore is now 37 turbines, not 38, and is a genuine +4MW
// GAIN; res_hut->res_block is now 7, not 8, and a genuine +4-resident gain).
test('HAND RECOST 1 (the offshore rung, task-assigned, POST-FIX): pow_wind -> pow_offshore now floors to 37 turbines and reports an HONEST +4 MW gain, never a hidden loss', () => {
  const wind = SPECS.pow_wind;
  const offshore = SPECS.pow_offshore;
  // BUG-648 (landed under this round): pow_wind dropped 8 -> 6 MW for
  // real-world realism, so the hand arithmetic here DERIVES from the live
  // catalogue (GR#15) instead of the 8-MW literals this test was born with.
  // At 6 MW the division goes EXACT (floor(300/6) = 50, 50 x 6 = 300), so
  // the honest gain is 0 -- still >= 0, which is the property under test.
  const windMw = capacityOf(wind);
  assert.ok(windMw > 0 && Number.isFinite(windMw), 'pow_wind has a real MW capacity');
  assert.equal(capacityOf(offshore), 300, 'pow_offshore is 300 MW/instance');

  const ladder = consolidationLadder();
  const rung = ladder.find((r) => r.from === 'pow_wind' && r.to === 'pow_offshore');
  assert.ok(rung, 'pow_wind -> pow_offshore must be a real generated rung (matches the AC-9 finding in the acceptance doc)');
  assert.equal(rung.groupSize, Math.floor(capacityOf(offshore) / windMw), 'the corrected formula: the LARGEST group the successor can fully absorb');

  // Hand arithmetic, derived: group capacity = groupSize x live turbine MW.
  const groupCapacity = rung.groupSize * windMw;
  const trueDelta = capacityOf(offshore) - groupCapacity;
  assert.ok(trueDelta >= 0, 'the TRUE effect of this consolidation is never a capacity loss (was -4 MW under the pre-fix ceil)');

  // Build a section with exactly groupSize pow_wind and run the finder.
  const buildings = [];
  for (let i = 0; i < rung.groupSize; i++) buildings.push({ id: i + 1, spec: 'pow_wind', x: i % 16, y: Math.floor(i / 16), builtTick: 0 });
  const s = baseState({ buildings });
  const opp = findOpportunities(s, [sectionKeyOf(0, 0)]).find((o) => o.fromSpec === 'pow_wind' && o.toSpec === 'pow_offshore');
  assert.ok(opp, 'the opportunity must be found (section holds exactly groupSize turbines)');

  // Hand recost the MONEY too (Aaron's R2/AC-22, mirrored read-only here):
  const buildCost = placementCost(offshore); // sp.cost, category 'services' != 'zones'
  const scrapPerUnit = Math.round(placementCost(wind) * CONSOLIDATOR_SCRAP_FRACTION);
  const scrapRecovered = scrapPerUnit * rung.groupSize;
  assert.equal(buildCost, 225000000);
  // Derived, not hardcoded (BUG-648 changed the turbine's cost under this
  // test): scrap is CONSOLIDATOR_SCRAP_FRACTION of the live placement cost.
  assert.equal(scrapPerUnit, Math.round(placementCost(wind) * CONSOLIDATOR_SCRAP_FRACTION));
  assert.ok(scrapPerUnit > 0, 'a turbine has a real positive scrap value');
  assert.ok(scrapRecovered > 0 && Number.isFinite(scrapRecovered), 'derived scrap total is a real figure');
  assert.ok(buildCost - scrapRecovered > 0, 'the offshore consolidation is a real net spend, not a money printer');
  assert.equal(opp.buildCost, buildCost);
  assert.equal(opp.scrapRecovered, scrapRecovered);
  assert.equal(opp.netCost, buildCost - scrapRecovered);

  // THE FIX: capacityGain is now the true, unclamped, non-negative value.
  assert.equal(opp.capacityGain, trueDelta, 'the panel must report the TRUE gain (derived, >= 0), not a clamped stand-in');
  console.log(`[POST-FIX] pow_wind->pow_offshore: TRUE capacity delta = ${trueDelta} MW; PANEL shows capacityGain = ${opp.capacityGain} (now honest — floor-derived group for GBP ${opp.netCost.toLocaleString()} net, a real gain).`);
});

test('HAND RECOST 2 (the "free residential" rung, POST-FIX): res_hut -> res_block now floors to 7, reported as netCost 0 AND an honest +4-resident capacity GAIN', () => {
  const hut = SPECS.res_hut;
  const block = SPECS.res_block;
  assert.equal(capacityOf(hut), 8);
  assert.equal(capacityOf(block), 60);
  assert.equal(placementCost(hut), 0, 'zoned spec: placementCost is 0 (category "zones")');
  assert.equal(placementCost(block), 0);

  const ladder = consolidationLadder();
  const rung = ladder.find((r) => r.from === 'res_hut' && r.to === 'res_block');
  assert.ok(rung);
  assert.equal(rung.groupSize, 7, 'floor(60/8) = 7.5 -> 7 (the corrected formula)');

  const buildings = [];
  for (let i = 0; i < 7; i++) buildings.push({ id: i + 1, spec: 'res_hut', x: i, y: 0, builtTick: 0 });
  const s = baseState({ buildings });
  const opp = findOpportunities(s, [sectionKeyOf(0, 0)]).find((o) => o.fromSpec === 'res_hut' && o.toSpec === 'res_block');
  assert.ok(opp);
  assert.equal(opp.groupCount, 7);
  assert.equal(opp.buildCost, 0);
  assert.equal(opp.scrapRecovered, 0);
  assert.equal(opp.netCost, 0);

  const trueDelta = capacityOf(block) - 7 * capacityOf(hut); // 60 - 56 = +4
  assert.equal(trueDelta, 4);
  assert.equal(opp.capacityGain, 4, 'reported as a free move that is ALSO a genuine +4-resident capacity gain — no longer a hidden loss');
  console.log(`[POST-FIX] res_hut->res_block (x7): netCost=0 (zoning is free), TRUE capacity delta=${trueDelta}, PANEL capacityGain=${opp.capacityGain}. A background process can no longer delete resident capacity for GBP 0.`);
});

// SUPERSEDED (FEAT-2326609761 civic-tier round, F1, 2026-09-05): this test
// originally asserted "8x hea_clinic -> hea_hospital" as an exact-capacity
// CONTROL CASE, on the assumption that sharing kind:'health' +
// capacityField:'served' was sufficient to make clinic a legitimate
// successor of hospital. That assumption is Aaron's own explicit design
// ruling in reverse: "clinics/ambulances are local coverage, never merge
// them into one hospital." The civic-tier round added a `careTier` data
// discriminator ('local' for hea_clinic/hea_ambulance, 'regional' for
// hea_hospital/hea_teaching) that familyKeyOf now folds in, so clinic and
// hospital are no longer the same family and this rung no longer exists —
// correctly, per the corrected design. The assertions below are the
// mutation-proof for that fix: deleting `careTier` from the catalogue (or
// removing it from familyKeyOf) makes this test RED again by resurrecting
// the old (wrong) rung.
test('HAND RECOST 3 SUPERSEDED (F1 fix): hea_clinic -> hea_hospital is NO LONGER a successor — careTier separates local coverage from hospital-tier', () => {
  const clinic = SPECS.hea_clinic;
  const hospital = SPECS.hea_hospital;
  assert.equal(capacityOf(clinic), 5000);
  assert.equal(capacityOf(hospital), 40000);
  assert.equal(clinic.careTier, 'local', 'hea_clinic must be tagged local-coverage');
  assert.equal(hospital.careTier, 'regional', 'hea_hospital must be tagged hospital-tier');
  assert.notEqual(familyKeyOf(clinic), familyKeyOf(hospital), 'careTier must split the family despite matching kind+capacityField');

  const ladder = consolidationLadder();
  const rung = ladder.find((r) => r.from === 'hea_clinic' && r.to === 'hea_hospital');
  assert.equal(rung, undefined, 'a hospital consolidating away local clinic coverage must never be offered');

  const buildings = [];
  for (let i = 0; i < 8; i++) buildings.push({ id: i + 1, spec: 'hea_clinic', x: i, y: 0, builtTick: 0 });
  const s = baseState({ buildings });
  const opp = findOpportunities(s, [sectionKeyOf(0, 0)]).find((o) => o.fromSpec === 'hea_clinic' && o.toSpec === 'hea_hospital');
  assert.equal(opp, undefined, 'no clinic-into-hospital opportunity may be surfaced to the tab');
});

// ===========================================================================
// PRIORITY 2 — THE RECONNECTION COST ESTIMATOR
//
// The estimator is explicitly documented as "APPROXIMATE... NOT a real
// pathfind": a Chebyshev ring search over the SECTION GRID (476 sections at
// 16x16 tiles), never inspecting actual tiles, buildings, or water between
// the stranded cluster and the nearest connected-road SECTION. We construct
// a stranded cluster whose cheapest REAL path is blocked by a wall of
// buildings and compare the module's estimate against an independent
// ground-truth BFS shortest path over the real (obstacle-aware) tile grid.
// ===========================================================================

/** Independent ground-truth BFS: shortest orthogonal path length (in tiles)
 * from `start` to `goal`, treating any tile in `blocked` as impassable.
 * This NEVER calls into consolidator.ts or data.ts's road logic — it is a
 * from-scratch oracle so the comparison is genuinely independent. */
function bfsTileDistance(start, goal, blocked, w, h) {
  const key = (x, y) => y * w + x;
  const dist = new Map([[key(start[0], start[1]), 0]]);
  const queue = [start];
  let qi = 0;
  while (qi < queue.length) {
    const [x, y] = queue[qi++];
    const d = dist.get(key(x, y));
    if (x === goal[0] && y === goal[1]) return d;
    for (const [dx, dy] of [[1, 0], [-1, 0], [0, 1], [0, -1]]) {
      const nx = x + dx, ny = y + dy;
      if (nx < 0 || ny < 0 || nx >= w || ny >= h) continue;
      if (blocked.has(key(nx, ny))) continue;
      const nk = key(nx, ny);
      if (dist.has(nk)) continue;
      dist.set(nk, d + 1);
      queue.push([nx, ny]);
    }
  }
  return Infinity; // unreachable within the grid
}

test('ATTACK: the ring-search reconnection estimate can be off by 30x+ when the real path is obstacle-blocked (a "10x lie" is not a worst case, it is an UNDER-statement)', () => {
  // Layout: stranded residential at (0,0). A "connected" road tile at (9,0)
  // -- SAME 16x16 section (radius 0 in the ring search), so the module's
  // estimate is the section-level FLOOR: max(1,0) * SECTION_TILES * roadCost.
  // A solid wall of buildings blocks the direct route at x=8 for y=0..258,
  // leaving the only gap at y=259 (the far side of the whole 260-tile map),
  // so the REAL shortest path must detour the length of the map.
  const wallX = 8;
  const gapY = MAP_H - 1; // 259
  const buildings = [
    { id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: 0 },
    { id: 2, spec: 'road', x: 9, y: 0, builtTick: 0 },
  ];
  let nextId = 3;
  for (let y = 0; y < MAP_H; y++) {
    if (y === gapY) continue; // the single opening
    buildings.push({ id: nextId++, spec: 'ind_factory', x: wallX, y, builtTick: 0 });
  }
  const s = baseState({ buildings, tick: 1000, roadConnectivity: { connectedRoadTiles: ['9,0'] } });

  const key = sectionKeyOf(0, 0);
  const opps = findReconnectionOpportunities(s, [key]);
  assert.equal(opps.length, 1, 'the section must be flagged as having actionable stranded capacity');
  const opp = opps[0];
  assert.equal(opp.approxSpurSections, 0, 'the connected road tile is in the SAME 16x16 section as the stranded building');
  const roadCost = placementCost(SPECS.road);
  const claimedTiles = SECTION_TILES; // Math.max(1,0) * SECTION_TILES
  assert.equal(opp.estimatedReconnectCost, claimedTiles * roadCost);

  // Ground truth: BFS from a tile adjacent to the stranded building (1,0) to
  // the connected road tile (9,0), treating every building footprint
  // (except the target road tile itself and the stranded building, which are
  // start/goal, not obstacles to route THROUGH) as blocked.
  const blocked = new Set();
  for (const b of buildings) {
    if (b.id === 1 || b.id === 2) continue; // start/goal are not obstacles
    blocked.add(b.y * MAP_W + b.x);
  }
  const realTiles = bfsTileDistance([1, 0], [9, 0], blocked, MAP_W, MAP_H);
  assert.ok(Number.isFinite(realTiles), 'a real route must exist via the single gap');

  const claimedTileEquivalent = opp.estimatedReconnectCost / roadCost;
  const underestimateRatio = realTiles / claimedTileEquivalent;
  console.log(`[ATTACK] reconnection estimator: claims ${claimedTileEquivalent} tiles (GBP ${opp.estimatedReconnectCost}); REAL obstacle-aware shortest path is ${realTiles} tiles (GBP ${realTiles * roadCost}). Underestimate factor: ${underestimateRatio.toFixed(1)}x.`);
  assert.ok(underestimateRatio >= 10, `expected at least a 10x underestimate, got ${underestimateRatio.toFixed(1)}x`);
});

test('geometric-only underestimate: even with ZERO obstacles, the section-radius estimate can be ~2x off simply from ring-search coarseness', () => {
  // Stranded building at the FAR corner of section 0 from its neighbour;
  // connected road at the FAR corner of the neighbouring section — both
  // "radius 1" in section terms, but the true tile distance is nearly
  // 2 x SECTION_TILES, not 1 x SECTION_TILES.
  const strandedX = 0; // near-side edge of section (0,0)
  const strandedY = 0;
  const roadX = 2 * SECTION_TILES - 1; // 31 (far corner of the adjacent section (1,0))
  const roadY = SECTION_TILES - 1; // 15
  const buildings = [
    { id: 1, spec: 'res_hut', x: strandedX, y: strandedY, builtTick: 0 },
    { id: 2, spec: 'road', x: roadX, y: roadY, builtTick: 0 },
  ];
  const s = baseState({ buildings, tick: 1000, roadConnectivity: { connectedRoadTiles: [`${roadX},${roadY}`] } });
  const opps = findReconnectionOpportunities(s, [sectionKeyOf(strandedX, strandedY)]);
  assert.equal(opps.length, 1);
  const opp = opps[0];
  assert.equal(opp.approxSpurSections, 1);
  const roadCost = placementCost(SPECS.road);
  assert.equal(opp.estimatedReconnectCost, 1 * SECTION_TILES * roadCost);

  const trueChebyshev = Math.max(Math.abs(roadX - strandedX), Math.abs(roadY - strandedY));
  console.log(`[ATTACK] geometry-only case: claims ${SECTION_TILES} tiles, true Chebyshev distance is ${trueChebyshev} tiles (${(trueChebyshev / SECTION_TILES).toFixed(2)}x) — with NO obstacles at all.`);
  assert.ok(trueChebyshev > SECTION_TILES, 'the section-grid radius estimate underestimates even the straight-line distance once you are near a section boundary');
});

// ===========================================================================
// PRIORITY 3 — SECTION GEOMETRY (independent re-verification at 800m/16x16)
// ===========================================================================

test('SECTIONS_X/Y and TOTAL_SECTIONS match the documented 28x17=476 grid with partial edge sections (440/16=27.5, 260/16=16.25)', () => {
  assert.equal(SECTION_TILES, 16);
  assert.equal(SECTIONS_X, 28);
  assert.equal(SECTIONS_Y, 17);
  assert.equal(TOTAL_SECTIONS, 476);
  // Both axes must have a partial final section.
  const lastCol = sectionOriginOf(SECTIONS_X - 1);
  const lastRow = sectionOriginOf((SECTIONS_Y - 1) * SECTIONS_X);
  assert.equal(lastCol.w, MAP_W - lastCol.x0);
  assert.ok(lastCol.w < SECTION_TILES, 'last column section must be a partial (440 - 27*16 = 8 tiles)');
  assert.equal(lastCol.w, 8);
  assert.equal(lastRow.h, MAP_H - lastRow.y0);
  assert.ok(lastRow.h < SECTION_TILES, 'last row section must be a partial (260 - 16*16 = 4 tiles)');
  assert.equal(lastRow.h, 4);
});

test('EXHAUSTIVE independent re-derivation: every one of the 440*260 tiles maps to exactly one section key, and every section key\'s tile set is a disjoint, contiguous, correctly-clipped rectangle', () => {
  const owner = new Int32Array(MAP_W * MAP_H).fill(-1);
  for (let y = 0; y < MAP_H; y++) {
    for (let x = 0; x < MAP_W; x++) {
      const k = sectionKeyOf(x, y);
      assert.ok(k >= 0 && k < TOTAL_SECTIONS, `section key ${k} for tile (${x},${y}) out of range`);
      owner[y * MAP_W + x] = k;
    }
  }
  assert.ok(!owner.includes(-1), 'every tile must be owned by exactly one section');

  // Re-derive each section's rectangle independently from raw tile ownership
  // and cross-check against sectionOriginOf's formula.
  const seen = new Map();
  for (let y = 0; y < MAP_H; y++) {
    for (let x = 0; x < MAP_W; x++) {
      const k = owner[y * MAP_W + x];
      let bounds = seen.get(k);
      if (!bounds) { bounds = { minX: x, maxX: x, minY: y, maxY: y, count: 0 }; seen.set(k, bounds); }
      bounds.minX = Math.min(bounds.minX, x);
      bounds.maxX = Math.max(bounds.maxX, x);
      bounds.minY = Math.min(bounds.minY, y);
      bounds.maxY = Math.max(bounds.maxY, y);
      bounds.count += 1;
    }
  }
  for (const [k, bounds] of seen) {
    const { x0, y0, w, h } = sectionOriginOf(k);
    assert.equal(bounds.minX, x0, `section ${k} minX mismatch`);
    assert.equal(bounds.minY, y0, `section ${k} minY mismatch`);
    assert.equal(bounds.maxX, x0 + w - 1, `section ${k} maxX mismatch (partial-edge clip)`);
    assert.equal(bounds.maxY, y0 + h - 1, `section ${k} maxY mismatch (partial-edge clip)`);
    assert.equal(bounds.count, w * h, `section ${k} tile count must equal w*h exactly (no gaps, no overlaps)`);
  }
  assert.equal(seen.size, TOTAL_SECTIONS, 'every one of the 476 sections must actually be reachable by some real tile');
});

// ===========================================================================
// PRIORITY 4 — THE TOGGLE (localStorage grep, independent of the author's own test)
// ===========================================================================

test('GR#21/ASM-1504: zero localStorage references anywhere in the consolidator read-only module or its UI surface', () => {
  const files = [
    CONSOLIDATOR_SRC,
    path.join(__dirname, '..', 'src', 'components', 'left', 'tabs', 'consolidatorTab.tsx'),
    path.join(__dirname, '..', 'src', 'sim', 'consolidatorFocus.ts'),
  ];
  for (const f of files) {
    const text = fs.readFileSync(f, 'utf8');
    // Match actual USAGE (localStorage.getItem/.setItem/.removeItem/[...]),
    // never the bare word — the module's own header comment legitimately
    // SAYS "no localStorage" as a rule statement, which would false-positive
    // a naive substring match (mirrors consolidator.test.mjs's own grep
    // idiom of matching `localStorage.` as a call-site pattern).
    assert.doesNotMatch(text, /localStorage\.\w|localStorage\[/, `${path.basename(f)} must never CALL localStorage`);
  }
});

test('MapView.tsx consolidator overlay reads the toggle from SimState, never localStorage, and is gated on it', () => {
  const mapViewSrc = fs.readFileSync(path.join(__dirname, '..', 'src', 'components', 'MapView.tsx'), 'utf8');
  const overlayBlock = mapViewSrc.slice(mapViewSrc.indexOf('FEAT-2326609761'));
  assert.doesNotMatch(overlayBlock, /localStorage/);
  assert.match(overlayBlock, /state\.consolidatorEnabled/, 'the overlay must be gated on SimState.consolidatorEnabled');
});

// ===========================================================================
// PRIORITY 7 — MONTHLY ROTATION: what the tab actually shows
// ===========================================================================

test('POST-FIX (round-1 finding 3): the Consolidator tab now wires currentMonthOpportunities AND keeps two distinct, honestly-labelled opportunity lists (month scope vs whole map)', () => {
  const tabSrc = fs.readFileSync(path.join(__dirname, '..', 'src', 'components', 'left', 'tabs', 'consolidatorTab.tsx'), 'utf8');
  // The exported convenience wrapper must actually be imported and called —
  // it was exported-but-dead before the fix.
  assert.match(tabSrc, /currentMonthOpportunities/, 'currentMonthOpportunities must be imported and used, not dead code');
  // The tab must compute a SCOPED list (topOpportunities against
  // scope.sectionKeys) for the "Month N scope" header, and a SEPARATE
  // whole-map list under its OWN honestly-labelled heading — never one
  // list serving both headers.
  assert.match(tabSrc, /topOpportunities\(state, scope\.sectionKeys, TOP_LIMIT\)/, 'the month-scope list must be built from scope.sectionKeys, not the whole map');
  assert.match(tabSrc, /topOpportunities\(state, allKeys, TOP_LIMIT\)/, 'a separate whole-map list may still exist...');
  assert.match(tabSrc, /Whole-map opportunities \(informational/, '...but ONLY under its own heading that says it is whole-map/informational, never under the Month-N-scope heading');
  assert.match(tabSrc, /Month \{frame\.twelfth \+ 1\} scope/, 'the month-scope heading still exists, now truthfully paired with the scoped list above');
});

test('twelfthIndexOf/monthlyScopeOf: month 0 vs month 11 (whole-map) both usable and distinguishable, independent of the tab wiring bug above', () => {
  const scope0 = monthlyScopeOf(0);
  assert.equal(scope0.twelfth, 0);
  assert.equal(scope0.full, false);
  assert.ok(scope0.sectionKeys.length < TOTAL_SECTIONS);
  const TICKS_PER_MONTH = 30;
  const scope11 = monthlyScopeOf(11 * TICKS_PER_MONTH);
  assert.equal(scope11.twelfth, 11);
  assert.equal(scope11.full, true);
  assert.equal(scope11.sectionKeys.length, TOTAL_SECTIONS);
});

// ===========================================================================
// STRANDED-CAPACITY CROSS-CHECK against the SSOT, on a bigger synthetic city
// (independent re-derivation, not reusing the module's own bucketing code)
// ===========================================================================

test('stranded capacity total independently cross-checked against offlineResidentsByReason across a mixed 40-building synthetic city', () => {
  const buildings = [];
  let id = 1;
  // 10 fully online (road-adjacent + connected).
  for (let i = 0; i < 10; i++) {
    buildings.push({ id: id++, spec: 'res_hut', x: i, y: 1, builtTick: 0 });
  }
  buildings.push({ id: id++, spec: 'road', x: 0, y: 0, builtTick: 0 });
  // 10 road-adjacent-fail (far away, isolated, no road at all).
  for (let i = 0; i < 10; i++) {
    buildings.push({ id: id++, spec: 'res_hut', x: 3 * SECTION_TILES + i, y: 0, builtTick: 0 });
  }
  // 10 road-connected-fail (adjacent to an UNCONNECTED road tile).
  for (let i = 0; i < 10; i++) {
    buildings.push({ id: id++, spec: 'res_hut', x: 6 * SECTION_TILES + i, y: 1, builtTick: 0 });
    buildings.push({ id: id++, spec: 'road', x: 6 * SECTION_TILES + i, y: 0, builtTick: 0 });
  }
  const s = baseState({ buildings, tick: 1000, roadConnectivity: { connectedRoadTiles: ['0,0'] } });
  const report = strandedCapacityReport(s);
  const ssot = offlineResidentsByReason(s);
  assert.equal(report.totalActionableCapacity, ssot.disconnected, 'GR#3: must never drift from the Housing tab\'s own SSOT number');
});
