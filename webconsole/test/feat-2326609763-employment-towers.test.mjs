// feat-2326609763-employment-towers.test.mjs — FEAT-2326609763: two
// ultra-dense employment towers (off_tower_canary ~10,000 jobs,
// off_tower_marina ~20,000 jobs, both 9x9), mirroring res_tower_nyc/
// res_tower_sgp's shape exactly, closing the measured 3x jobs-vs-housing
// density gap (res_tower_sgp 247 residents/tile vs off_towers_downtown's
// 80 jobs/tile ceiling).
//
// SPLIT NOTE (round result, 2026-09-03): this feature originally shipped
// together with BUG-652 (real job counts for land_airport/uni/hea_teaching/
// land_stadium/land_tunnel/station_ashford). BUG-652 was REJECTED on
// rollout — it retroactively re-prices buildings a player ALREADY OWNS
// (measured: a solvent 60k-pop city with one land_airport goes from
// +£21,250/tick to -£1,806,950/tick and insolvent by tick 9). These two
// towers are brand-new specs nobody owns yet, so they carry NONE of that
// retroactive risk and ship alone. BUG-652's job fields, the
// KIND_TO_WAGE_SECTOR landmark mapping, the jobsAtTier() collision guard,
// and the totalChildrenCapacity() fix are all held back in a separate,
// unapplied patch pending a phasing decision (grandfathering / migration
// with a matching tax credit / level-gating) — see that patch's own header
// for the full accounting. Confirmed by direct code inspection + a live
// test run: off_tower_canary/off_tower_marina carry `jobs` as their ONLY
// capacity field (no residents/children/served alongside it), so they need
// NONE of the jobsAtTier() guard — the plain pre-existing capacityAtTier()
// scaling in totalJobs()/totalJobsBySector() (unmodified by this diff)
// already reads their tier-grown job count correctly.
//
// RED SELF-PROOF (verification-standards convention, GR#24 scratch-copy
// discipline — never a git command): with a scratch copy of data.ts
// (`cp src/sim/data.ts src/sim/data.ts.bak`), deleting the
// `off_tower_canary:`/`off_tower_marina:` SPECS lines was confirmed to turn
// "both employment towers exist" and the density-ordering test RED, then
// the scratch copy was restored (`mv src/sim/data.ts.bak src/sim/data.ts`).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  PALETTE,
  PALETTE_FLAT,
  HEIGHT_CAP_STOREYS,
  capacityAtTier,
  totalJobs,
  totalJobsBySector,
  WORKING_AGE_FRACTION,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

// Places `spec` at (x,y), roads it, ticks it fully online, returns the state.
// NOTE: initialState() ships a genesis starter city occupying roughly
// y in [56, 206] across the full map width (motorway/rail/road/buildings) —
// callers of this helper must pick (x, y) that stays clear of that band, or
// 'place' silently no-ops on a tile collision (no thrown error, no notice —
// the existing catalogue's placement contract). The call site below is
// deliberately chosen to stay clear.
function placeAndBringOnline(spec, x, y) {
  let s = initialState();
  const dispatch = (action) => { s = reducer(s, action); };
  dispatch({ type: 'debugFunds', amount: 5_000_000_000 });
  dispatch({ type: 'unlockAll' });
  const roadY = y - 2;
  const sp = SPECS[spec];
  const roadTiles = [];
  for (let rx = 0; rx <= x + sp.w + 2; rx++) roadTiles.push({ x: rx, y: roadY });
  roadTiles.push({ x: x + 1, y: roadY + 1 });
  dispatch({ type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
  dispatch({ type: 'place', spec, x, y });
  const ticksToBuild = Math.max(3, Math.round(sp.cost / 1_500_000));
  for (let i = 0; i < ticksToBuild + 5; i++) dispatch({ type: 'tick' });
  return s;
}

const NEW_TOWER_IDS = ['off_tower_canary', 'off_tower_marina'];

// ========== tower spec shape ==========

test('FEAT-2326609763: both employment towers exist in SPECS with the intended scale, 9x9, office/zones', () => {
  const canary = SPECS.off_tower_canary;
  const marina = SPECS.off_tower_marina;
  assert.ok(canary, 'off_tower_canary missing from SPECS');
  assert.ok(marina, 'off_tower_marina missing from SPECS');
  for (const sp of [canary, marina]) {
    assert.equal(sp.kind, 'office', `${sp.id} must be kind office (routes into KIND_TO_WAGE_SECTOR's tertiary bucket)`);
    assert.equal(sp.category, 'zones', `${sp.id} must be a free-to-place zone, like the rest of the office family`);
    assert.equal(sp.w, 9, `${sp.id} must be 9x9`);
    assert.equal(sp.h, 9, `${sp.id} must be 9x9`);
  }
  assert.equal(canary.jobs, 10000, 'off_tower_canary must carry ~10,000 jobs');
  assert.equal(marina.jobs, 20000, 'off_tower_marina must carry ~20,000 jobs');
  assert.equal(marina.jobs, canary.jobs * 2, 'Marina Bay is the 2x-scale sibling of Canary Wharf, per the brief');
});

test('FEAT-2326609763: footprints are honestly larger than every existing office spec', () => {
  const existingMaxArea = Math.max(
    ...Object.values(SPECS)
      .filter((sp) => sp.kind === 'office' && !NEW_TOWER_IDS.includes(sp.id))
      .map((sp) => sp.w * sp.h),
  );
  for (const id of NEW_TOWER_IDS) {
    const sp = SPECS[id];
    const area = sp.w * sp.h;
    assert.ok(area > existingMaxArea, `${id}: footprint ${area} tiles must exceed the largest existing office footprint (${existingMaxArea})`);
  }
});

test('FEAT-2326609763: jobs-per-tile is monotonically increasing across the office density ladder and exceeds off_towers_downtown\'s 80/tile ceiling', () => {
  const perTile = (sp) => sp.jobs / (sp.w * sp.h);
  const businesspark = perTile(SPECS.off_businesspark);
  const downtown = perTile(SPECS.off_towers_downtown);
  const canary = perTile(SPECS.off_tower_canary);
  const marina = perTile(SPECS.off_tower_marina);

  assert.equal(downtown, 80, 'sanity: off_towers_downtown must still be exactly 80 jobs/tile (2000/25)');
  assert.ok(businesspark < downtown, 'off_businesspark must be less dense than off_towers_downtown');
  assert.ok(downtown < canary, 'off_tower_canary must exceed the previous 80/tile ceiling');
  assert.ok(canary < marina, 'off_tower_marina must be denser than off_tower_canary (it is the bigger sibling)');
  assert.ok(marina > 80, `off_tower_marina (${marina.toFixed(1)}/tile) must exceed off_towers_downtown's 80/tile`);
});

test('FEAT-2326609763: cost-per-job and upkeep-per-job are extrapolated from the existing office family, not invented', () => {
  const familyIds = ['off_businesspark', 'off_towers_downtown'];
  const costPerJob = (sp) => sp.cost / sp.jobs;
  const upkeepPerJob = (sp) => sp.upkeep / sp.jobs;
  const costs = familyIds.map((id) => costPerJob(SPECS[id]));
  const upkeeps = familyIds.map((id) => upkeepPerJob(SPECS[id]));
  const costLo = Math.min(...costs) * 0.9;
  const costHi = Math.max(...costs) * 1.1;
  const upLo = Math.min(...upkeeps) * 0.9;
  const upHi = Math.max(...upkeeps) * 1.1;
  for (const id of NEW_TOWER_IDS) {
    const sp = SPECS[id];
    const cpj = costPerJob(sp);
    const upj = upkeepPerJob(sp);
    assert.ok(cpj >= costLo && cpj <= costHi, `${id}: cost/job £${cpj.toFixed(2)} outside family-derived band [${costLo.toFixed(2)}, ${costHi.toFixed(2)}]`);
    assert.ok(upj >= upLo && upj <= upHi, `${id}: upkeep/job ${upj.toFixed(4)} outside family-derived band [${upLo.toFixed(4)}, ${upHi.toFixed(4)}]`);
  }
});

test('FEAT-2326609763: unlock levels sit above off_towers_downtown (the previous office ceiling) and within 1..20', () => {
  const downtownUnlock = SPECS.off_towers_downtown.unlock;
  assert.ok(SPECS.off_tower_canary.unlock > downtownUnlock);
  assert.ok(SPECS.off_tower_marina.unlock > downtownUnlock);
  assert.ok(SPECS.off_tower_marina.unlock >= SPECS.off_tower_canary.unlock, 'the bigger Marina Bay hub unlocks at or after Canary Wharf');
  for (const id of NEW_TOWER_IDS) {
    assert.ok(Number.isInteger(SPECS[id].unlock) && SPECS[id].unlock >= 1 && SPECS[id].unlock <= 20);
  }
});

test('FEAT-2326609763: both towers appear in the Offices palette family exactly once, no flat-list duplicates', () => {
  const offices = PALETTE.find((f) => f.title === 'Offices');
  assert.ok(offices);
  for (const id of NEW_TOWER_IDS) {
    assert.ok(offices.items.includes(id), `${id} must be listed under Offices`);
    const fams = PALETTE.filter((f) => f.items.includes(id));
    assert.equal(fams.length, 1, `${id} must appear in exactly one PALETTE family`);
  }
  assert.equal(new Set(PALETTE_FLAT).size, PALETTE_FLAT.length, 'no duplicate id across the flat palette (BUG-385 class)');
});

test('FEAT-2326609763: capacityAtTier(sp, 0) equals sp.jobs; capacityTiers ladder is present and increasing', () => {
  for (const id of NEW_TOWER_IDS) {
    const sp = SPECS[id];
    assert.equal(capacityAtTier(sp, 0), sp.jobs, `${id}: tier-0 capacity must equal the base jobs figure`);
    assert.ok(Array.isArray(sp.capacityTiers) && sp.capacityTiers.length === 10, `${id}: must carry a 10-tier capacityTiers ladder`);
    for (let i = 1; i < sp.capacityTiers.length; i++) {
      assert.ok(sp.capacityTiers[i] > sp.capacityTiers[i - 1], `${id}: capacityTiers must be strictly increasing`);
    }
  }
  assert.ok(HEIGHT_CAP_STOREYS.off_tower_canary >= 1, 'off_tower_canary must carry a HEIGHT_CAP_STOREYS entry');
  assert.ok(HEIGHT_CAP_STOREYS.off_tower_marina >= 1, 'off_tower_marina must carry a HEIGHT_CAP_STOREYS entry');
});

test('FEAT-2326609763: off_tower_marina places, comes online, and is counted by totalJobs/totalJobsBySector via the real reducer (proves it needs no jobsAtTier guard — bare capacityAtTier already reads it correctly)', () => {
  const s = placeAndBringOnline('off_tower_marina', 8, 12);
  assert.ok(s.buildings.some((b) => b.spec === 'off_tower_marina'), 'must actually enter the sim');
  assert.ok(totalJobs(s) >= 20000, 'totalJobs must include the tower\'s 20,000 jobs');
  const bySector = totalJobsBySector(s);
  assert.ok(bySector.tertiary >= 20000, 'off_tower_marina (kind office) must land in the tertiary sector bucket');
});

// ========== 3M-population land-budget improvement ==========

test('FEAT-2326609763: full employment for a 3M-population city needs materially less land with the new tower than with off_towers_downtown alone', () => {
  const workers = 3_000_000 * WORKING_AGE_FRACTION;
  const mapTiles = MAP_W * MAP_H;
  const oldJobsPerTile = SPECS.off_towers_downtown.jobs / (SPECS.off_towers_downtown.w * SPECS.off_towers_downtown.h);
  const newJobsPerTile = SPECS.off_tower_marina.jobs / (SPECS.off_tower_marina.w * SPECS.off_tower_marina.h);
  const oldTiles = workers / oldJobsPerTile;
  const newTiles = workers / newJobsPerTile;
  const oldPct = oldTiles / mapTiles;
  const newPct = newTiles / mapTiles;

  assert.ok(oldPct > 0.15, `sanity: the OLD ceiling should need a large fraction of the map (got ${(oldPct * 100).toFixed(1)}%)`);
  assert.ok(newTiles < oldTiles * 0.5, `new tile requirement (${Math.ceil(newTiles)}) must be materially (>2x) lower than the old requirement (${Math.ceil(oldTiles)})`);
  assert.ok(newPct < 0.10, `new land budget (${(newPct * 100).toFixed(1)}% of the map) must be a plausible, single-digit-percent fraction`);
});
