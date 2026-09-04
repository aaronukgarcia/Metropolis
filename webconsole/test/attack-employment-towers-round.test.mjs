// attack-employment-towers-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND
// (GR#23, attacker != author) against FEAT-2326609763 (off_tower_canary /
// off_tower_marina) and BUG-652 (job counts for land_airport / hea_teaching /
// uni / land_stadium / land_tunnel / station_ashford + the new
// KIND_TO_WAGE_SECTOR.landmark mapping + jobsAtTier()).
//
// This file is deliberately NOT a re-run of the author's own suite. It
// encodes the things the author's suite does NOT check:
//
//   A1  jobsAtTier()'s FLAT-vs-SCALE predicate is checked against the WHOLE
//       catalogue, mechanically, rather than against the four specs the
//       author had in mind — including the forward-looking trap that a
//       jobs-sized capacityTiers ladder on a spec that also carries
//       residents/children/served would be silently refused its growth.
//   A2  every zero-jobs landmark (59 of them) must cost EXACTLY £0 of wages
//       now that the whole `landmark` KIND has a wage sector.
//   A3  jobs conservation: sum(totalJobsBySector) === totalJobs(), and
//       sum(filledJobsBySector) === the scalar filled headcount, with every
//       newly-jobbed class online at once (no double count, no silent drop).
//   A4  the two new towers must actually SCALE with their ladder, and
//       hea_teaching must stay FLAT across every tier index (the collision).
//   A5  FISCAL BLAST RADIUS on an OLD SAVE — the finding this round exists
//       to pin down. A pre-existing, SOLVENT mid-game city that already owns
//       ONE land_airport is flipped to a large per-tick deficit purely by
//       loading this build. Encoded as an explicit, documented characterisation
//       bound so nobody can widen it silently.
//   A6  determinism (GR#21) + genesis-replay byte-identity for a journal that
//       contains the new specs.
//   A7  grand_terminus (the ONE pre-existing served+jobs spec) must be
//       byte-unchanged — jobsAtTier() must not have moved a spec that is not
//       part of this bug.
//   A8  side-effect characterisation: giving a spec `jobs` also feeds
//       fittingTier()'s throughput proxy, so four specs' auto-connect road
//       tier changed. Pinned so the change is visible, not silent.
//
// RED PROOFS (run by this round, GR#24 scratch-copy discipline — `cp f f.bak`,
// mutate via a scripted exact-string replace, `mv f.bak f`; never a git
// command). Each mutation was applied to a scratch copy of src/sim/data.ts /
// src/sim/fiscal.ts, this file was run, then the scratch copy was restored:
//   M1  jobsAtTier() reverted to a bare `capacityAtTier(sp, tier)` (the
//       pre-fix code)  -> A4's hea_teaching flatness assertions FAIL
//       (1,450 becomes the 120,000-sized served ladder value) and A3's
//       conservation numbers move.
//   M2  totalChildrenCapacity() reverted to the blind capacityAtTier() call
//       -> A9's uni assertion FAILS (6,000 children becomes uni's 650 jobs).
//   M3  the six new `jobs:` fields stripped from SPECS -> A3/A5 FAIL.
//   M4  `landmark: 'tertiary'` removed from KIND_TO_WAGE_SECTOR -> A3's
//       conservation FAILS (landmark jobs count toward totalJobs but vanish
//       from every sector bucket, i.e. jobs with no wage bill).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  capacityAtTier,
  fittingTier,
  placementCost,
  totalJobs,
  totalJobsBySector,
  filledJobsBySector,
  totalChildrenCapacity,
  unemploymentOf,
  WORKING_AGE_FRACTION,
} from '../src/sim/data.ts';
import { KIND_TO_WAGE_SECTOR, sectorWagesPerTick, SECTOR_WAGE_PER_MONTH } from '../src/sim/fiscal.ts';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';

const SECTORS = ['primary', 'secondary', 'tertiary', 'public'];
const NEW_JOB_SPECS = ['land_airport', 'uni', 'hea_teaching', 'land_stadium', 'land_tunnel', 'station_ashford'];
const NEW_TOWERS = ['off_tower_canary', 'off_tower_marina'];

/** Injects an already-complete, already-online building — the exact shape a
 *  LOADED SAVE's buildings carry (no `builtTick`, so computeIsOnline() treats
 *  them as always online). This is the "old save" model: the save format only
 *  stores the spec ID, so every stat is re-resolved from SPECS at load. */
function inject(state, spec, x, y) {
  const id = state.nextId ?? state.buildings.length + 1;
  return { ...state, nextId: id + 1, buildings: [...state.buildings, { id, spec, x, y }] };
}

function flows(s) {
  const f = computeFlows(s);
  const inflow = f.inflows.reduce((a, b) => a + b.value, 0);
  const outflow = f.outflows.reduce((a, b) => a + b.value, 0);
  const wages = (f.outflows.find((o) => o.label === 'Wages') ?? { value: 0 }).value;
  return { inflow, outflow, net: inflow - outflow, wages };
}

// ══════════════════════════════════════════════════════════════════════════
// A1 — jobsAtTier()'s FLAT/SCALE predicate, checked against the WHOLE catalogue
// ══════════════════════════════════════════════════════════════════════════

test('A1: every jobs-bearing spec in the catalogue lands on the correct jobsAtTier branch, and no spec that OUGHT to scale its jobs is silently pinned flat', () => {
  const flat = [];
  const scaling = [];
  for (const sp of Object.values(SPECS)) {
    if (!sp.jobs) continue;
    const sharesWithOther = sp.residents != null || sp.children != null || sp.served != null;
    (sharesWithOther ? flat : scaling).push(sp);
  }

  // The FLAT branch is only correct while none of those specs carries a
  // capacityTiers ladder that is actually sized for its JOBS. A ladder whose
  // base equals sp.jobs is a jobs ladder; a ladder whose base equals
  // residents/children/served belongs to that other field.
  for (const sp of flat) {
    const ladder = sp.capacityTiers;
    if (!ladder) continue;
    assert.notEqual(
      ladder[0],
      sp.jobs,
      `${sp.id}: carries jobs=${sp.jobs} AND a capacityTiers ladder whose base equals it — jobsAtTier() would pin this spec's jobs FLAT while its ladder grows, a silent capacity defect. Either drop the shared field or teach jobsAtTier() a per-field ladder.`,
    );
    // Positive statement of the ONE spec in this position today.
    assert.equal(sp.id, 'hea_teaching', `unexpected flat-branch spec with a ladder: ${sp.id}`);
    assert.equal(ladder[0], sp.served, 'hea_teaching\'s ladder must be its `served` ladder, not a jobs ladder');
  }

  // The SCALE branch is only correct while each ladder's base IS the job
  // count — otherwise capacityAtTier() would return a foreign figure.
  for (const sp of scaling) {
    if (!sp.capacityTiers) continue;
    assert.equal(
      sp.capacityTiers[0],
      sp.jobs,
      `${sp.id}: scale-branch spec whose capacityTiers base (${sp.capacityTiers[0]}) is not its jobs figure (${sp.jobs}) — capacityAtTier() would report a foreign capacity as its job count`,
    );
  }

  // Exact membership, so a future spec joining either branch is a visible diff.
  assert.deepEqual(
    flat.map((sp) => sp.id).sort(),
    ['grand_terminus', 'hea_teaching', 'station_ashford', 'uni'],
    'the FLAT branch membership changed — re-audit jobsAtTier()',
  );
  assert.ok(
    NEW_TOWERS.every((id) => scaling.some((sp) => sp.id === id)),
    'both new towers must be on the SCALE branch (jobs is their only capacity dimension)',
  );
});

test('A4: the new towers SCALE with their ladder, hea_teaching stays FLAT at 1,450 across every tier, grand_terminus is byte-unchanged', () => {
  const at = (spec, tier) => {
    let s = { ...initialState(), buildings: [], population: 10_000_000 };
    s = inject(s, spec, 1, 1);
    s = { ...s, buildings: s.buildings.map((b) => ({ ...b, capacityTier: tier })) };
    return totalJobs(s);
  };
  assert.equal(at('off_tower_marina', 0), 20000);
  assert.equal(at('off_tower_marina', 1), 22000);
  assert.equal(at('off_tower_marina', 9), 47159, 'the marina tower must reach its ladder ceiling');
  assert.equal(at('off_tower_marina', 99), 47159, 'over-index must clamp at the last tier, not NaN');
  assert.equal(at('off_tower_canary', 0), 10000);
  assert.equal(at('off_tower_canary', 9), 23579);

  // The collision the fix exists for: hea_teaching's ladder is its SERVED
  // ladder (120,000-based). Its jobs must be 1,450 at EVERY tier index.
  for (const tier of [0, 1, 5, 9, 99, -1, NaN]) {
    assert.equal(at('hea_teaching', tier), 1450, `hea_teaching must report 1,450 jobs at tier ${tier}, never its served ladder value`);
  }

  // A7 — the ONE pre-existing served+jobs spec must be untouched by this fix.
  assert.equal(at('grand_terminus', 0), 60, 'grand_terminus must still report exactly its catalogue jobs figure');
  assert.equal(at('grand_terminus', 9), 60, 'grand_terminus has no ladder; its jobs must not move with a tier index');
});

test('A9: totalChildrenCapacity reads `children`, never uni\'s new `jobs` figure, and never double-counts a laddered school', () => {
  const cap = (spec) => {
    let s = { ...initialState(), buildings: [], population: 100_000 };
    s = inject(s, spec, 1, 1);
    return totalChildrenCapacity(s);
  };
  assert.equal(cap('uni'), 6000, 'uni must contribute its 6,000 children places, not its 650 jobs');
  assert.equal(cap('col_sixth'), 1500, 'col_sixth (no ladder) must contribute its 1,500 places');
  assert.equal(cap('edu_city'), 2000, 'edu_city (laddered) must still contribute its tier-0 ladder value');
  assert.notEqual(cap('uni'), SPECS.uni.jobs, 'uni\'s children capacity must never equal its jobs figure');
});

// ══════════════════════════════════════════════════════════════════════════
// A2 — the whole `landmark` kind now has a wage sector: prove zero-jobs
//      landmarks still cost exactly zero
// ══════════════════════════════════════════════════════════════════════════

test('A2: every zero-jobs landmark costs EXACTLY £0 of wages and contributes EXACTLY 0 jobs, despite landmark now being a mapped wage sector', () => {
  const zeroJob = Object.values(SPECS).filter((sp) => sp.kind === 'landmark' && !sp.jobs).map((sp) => sp.id);
  assert.ok(zeroJob.length > 20, `expected the bulk of the landmark family to remain jobless (got ${zeroJob.length})`);
  assert.ok(!zeroJob.some((id) => NEW_JOB_SPECS.includes(id)), 'the three newly-jobbed landmarks must not appear in the zero-jobs set');

  let s = { ...initialState(), buildings: [], population: 5_000_000 };
  zeroJob.forEach((id, i) => { s = inject(s, id, 1, i); });

  assert.equal(totalJobs(s), 0, 'a city of nothing but jobless landmarks must have exactly 0 jobs');
  const bySector = totalJobsBySector(s);
  for (const sector of SECTORS) assert.equal(bySector[sector], 0, `${sector} must be exactly 0`);
  assert.equal(sectorWagesPerTick(filledJobsBySector(s)).totalPerTick, 0, 'a jobless-landmark city must pay exactly £0 of wages');
  assert.equal(KIND_TO_WAGE_SECTOR.landmark, 'tertiary', 'sanity: the mapping under test is actually present');
});

// ══════════════════════════════════════════════════════════════════════════
// A3 — conservation / no double count
// ══════════════════════════════════════════════════════════════════════════

test('A3: with every newly-jobbed class AND both towers online at once, sum(totalJobsBySector) === totalJobs and sum(filledJobsBySector) === the scalar filled headcount', () => {
  for (const population of [0, 1, 100_000, 1_000_000, 10_000_000]) {
    let s = { ...initialState(), buildings: [], population };
    [...NEW_JOB_SPECS, ...NEW_TOWERS, 'off_towers_downtown', 'ind_estate', 'mine_deep', 'com_mall', 'grand_terminus'].forEach((spec, i) => {
      s = inject(s, spec, 1, i);
    });

    const total = totalJobs(s);
    const bySector = totalJobsBySector(s);
    const sum = SECTORS.reduce((a, k) => a + bySector[k], 0);
    assert.equal(sum, total, `pop=${population}: sector sum (${sum}) must equal totalJobs (${total}) exactly`);

    const filled = filledJobsBySector(s);
    const filledSum = SECTORS.reduce((a, k) => a + filled[k], 0);
    const expectedFilled = Math.max(0, Math.round(Math.min(population * WORKING_AGE_FRACTION, total)));
    assert.equal(filledSum, expectedFilled, `pop=${population}: apportioned filled jobs (${filledSum}) must equal min(workers, capacity) (${expectedFilled}) exactly — no invented or lost headcount`);
    for (const sector of SECTORS) {
      assert.ok(filled[sector] <= bySector[sector], `pop=${population}: ${sector} filled (${filled[sector]}) must never exceed capacity (${bySector[sector]})`);
    }

    // Wages must equal the hand-computed sum of the per-sector lines.
    const w = sectorWagesPerTick(filled);
    const hand = SECTORS.reduce((a, k) => a + Math.round((filled[k] * SECTOR_WAGE_PER_MONTH[k]) / 30), 0);
    assert.equal(w.totalPerTick, hand, `pop=${population}: wage total must equal the hand-computed per-sector sum`);
  }
});

test('A3b: each newly-jobbed spec contributes its OWN catalogue figure to totalJobs exactly once — measured one spec at a time against an empty city', () => {
  const expected = { land_airport: 76000, uni: 650, hea_teaching: 1450, land_stadium: 250, land_tunnel: 1800, station_ashford: 200, off_tower_canary: 10000, off_tower_marina: 20000 };
  for (const [spec, jobs] of Object.entries(expected)) {
    let s = { ...initialState(), buildings: [], population: 10_000_000 };
    s = inject(s, spec, 1, 1);
    assert.equal(totalJobs(s), jobs, `${spec} must contribute exactly ${jobs} jobs`);
    const bySector = totalJobsBySector(s);
    assert.equal(SECTORS.reduce((a, k) => a + bySector[k], 0), jobs, `${spec}: its jobs must reach exactly one sector bucket (landmark drop / double count guard)`);
    // Two of them must land where the wage table actually charges for them.
    const sector = KIND_TO_WAGE_SECTOR[SPECS[spec].kind];
    assert.equal(bySector[sector], jobs, `${spec}: must land in the ${sector} bucket`);
  }
});

// ══════════════════════════════════════════════════════════════════════════
// A5 — THE FINDING: fiscal blast radius on an existing save
// ══════════════════════════════════════════════════════════════════════════

/** A mid-game city that is SOLVENT before this change: 200 estates of housing,
 *  60,000 people, no job buildings other than the airport it already owns.
 *  Measured pre-fix (scratch-reverted data.ts/fiscal.ts, this round): net
 *  +£21,250/tick, funds still growing at tick 400. */
function quietTownWithAirport(funds = 30_000_000) {
  let s = { ...initialState(), funds, population: 60_000, unlockedLevel: 20, level: 20 };
  for (let i = 0; i < 200; i++) s = inject(s, 'res_estate', 100 + (i % 60) * 5, 5 + Math.floor(i / 60) * 4);
  s = inject(s, 'land_airport', 300, 5);
  return s;
}

test('A5 (FINDING): one already-owned land_airport now costs a 60k-population city £1.87m/tick in wages against ~£136k of total inflow — a solvent save is flipped to a terminal deficit purely by loading this build', () => {
  const s = quietTownWithAirport();
  const f = flows(s);

  // The airport alone fills the ENTIRE workforce: 60,000 * 0.55 = 33,000
  // workers against 76,000 job slots, so filled === workers.
  const workers = Math.round(60_000 * WORKING_AGE_FRACTION);
  assert.equal(totalJobs(s), 76000, 'the airport is the city\'s only employer');
  assert.equal(unemploymentOf(s), 0, 'the airport alone saturates a 60k city\'s workforce');
  assert.equal(f.wages, Math.round((workers * SECTOR_WAGE_PER_MONTH.tertiary) / 30), 'wages must be the whole workforce at the tertiary rate');
  assert.equal(f.wages, 1_870_000, 'characterisation: £1,870,000/tick of wages from ONE pre-existing building');

  // The offsetting income is negligible: the wage bill is more than an order
  // of magnitude larger than the city's ENTIRE inflow.
  assert.ok(f.wages > f.inflow * 10, `the new wage bill (£${f.wages}) must be flagged while it exceeds 10x the city's whole inflow (£${f.inflow})`);
  assert.ok(f.net < 0, 'this city is in deficit');
  assert.ok(f.net < -1_500_000, `characterisation: net is £${f.net}/tick (pre-fix this same city ran +£21,250/tick)`);

  // And it actually bankrupts, fast, from a plausible mid-game treasury.
  let run = s;
  let insolventAt = null;
  for (let t = 0; t < 60 && insolventAt === null; t++) {
    run = reducer(run, { type: 'tick' });
    if (run.funds <= 0) insolventAt = t + 1;
  }
  assert.ok(insolventAt !== null && insolventAt <= 20, `a £30m mid-game treasury must be shown to be wiped out quickly (insolvent at tick ${insolventAt})`);
});

test('A5b: the two new TOWERS are free to place (category "zones") — their real price is the wage bill, not the catalogue sticker', () => {
  for (const id of NEW_TOWERS) {
    assert.equal(placementCost(SPECS[id]), 0, `${id} is a zone: the catalogue cost is never charged`);
  }
  // One marina tower at its base tier costs more per tick in wages than 180
  // times its own upkeep — the number the balance pass needs to see.
  const baseWage = Math.round((SPECS.off_tower_marina.jobs * SECTOR_WAGE_PER_MONTH.tertiary) / 30);
  assert.equal(baseWage, 1_133_333, 'characterisation: one marina tower at tier 0 = £1,133,333/tick of wages');
  assert.ok(baseWage > SPECS.off_tower_marina.upkeep * 150, 'the wage bill dwarfs the upkeep line the player is shown');
  // At the top of its auto-scale ladder it is worse again.
  const maxWage = Math.round((capacityAtTier(SPECS.off_tower_marina, 9) * SECTOR_WAGE_PER_MONTH.tertiary) / 30);
  assert.equal(maxWage, 2_672_343, 'characterisation: a fully auto-scaled marina tower = £2,672,343/tick');
});

// ══════════════════════════════════════════════════════════════════════════
// A6 — determinism + genesis replay
// ══════════════════════════════════════════════════════════════════════════

test('A6: 200 ticks with every new spec placed is deterministic (identical funds/population/jobs across two independent runs)', () => {
  const run = () => {
    let s = { ...initialState(), funds: 5_000_000_000, population: 300_000, unlockedLevel: 20, level: 20 };
    [...NEW_JOB_SPECS, ...NEW_TOWERS].forEach((spec, i) => { s = inject(s, spec, 1, i); });
    for (let t = 0; t < 200; t++) s = reducer(s, { type: 'tick' });
    return { funds: s.funds, population: s.population, jobs: totalJobs(s), tick: s.tick, buildings: s.buildings.length };
  };
  const a = run();
  const b = run();
  assert.deepEqual(a, b, 'two identical 200-tick runs must agree exactly (GR#21)');
  assert.ok(Number.isFinite(a.funds) && Number.isFinite(a.population), 'no NaN after 200 ticks');
});

test('A6b: a genesis journal containing the new towers replays byte-identically and reconstructs the live state exactly', () => {
  const script = [
    { type: 'debugFunds', amount: 5_000_000_000 },
    { type: 'unlockAll' },
    { type: 'placeRoadPath', spec: 'road', tiles: Array.from({ length: 60 }, (_, x) => ({ x, y: 3 })) },
    { type: 'place', spec: 'off_tower_canary', x: 5, y: 5 },
    { type: 'tick' },
    { type: 'place', spec: 'off_tower_marina', x: 20, y: 5 },
    { type: 'tick' },
    { type: 'place', spec: 'land_stadium', x: 35, y: 5 },
    { type: 'tick' },
    { type: 'tick' },
  ];
  let journal = emptyJournal();
  let live = initialState();
  for (const action of script) {
    journal = recordAction(journal, live.tick, action);
    live = reducer(live, action);
  }
  const r1 = replayFromGenesis(journal);
  const r2 = replayFromGenesis(journal);
  assert.equal(stableStringify(r1), stableStringify(r2), 'replaying the same journal twice must be byte-identical');
  assert.equal(stableStringify(r1), stableStringify(live), 'genesis replay must reconstruct the live state exactly');
  assert.ok(r1.buildings.some((b) => b.spec === 'off_tower_marina'), 'the replayed city must contain the new tower');
});

// ══════════════════════════════════════════════════════════════════════════
// A8 — side-effect characterisation: `jobs` also feeds fittingTier()
// ══════════════════════════════════════════════════════════════════════════

test('A8: giving these specs `jobs` also raises their auto-connect road tier (fittingTier reads jobs as its throughput proxy) — pinned so the change is visible, not silent', () => {
  const without = (sp) => fittingTier({ ...sp, jobs: undefined });
  const changed = {};
  for (const id of [...NEW_JOB_SPECS, ...NEW_TOWERS]) {
    const sp = SPECS[id];
    const before = without(sp);
    const after = fittingTier(sp);
    if (before !== after) changed[id] = `${before}->${after}`;
  }
  assert.deepEqual(
    changed,
    // FEAT-2326609781 (2026-09-04): land_tunnel dropped out of this set when
    // Aaron's ruling grew its footprint 3×3 → 6×4 — area 24 alone now clears
    // the tier-5 threshold, so `jobs` no longer MOVES its tier (it was
    // already 5 without them). The re-audit this assert demands was done:
    // the tunnel still gets a motorway connector, just from footprint.
    { land_stadium: '4->5', hea_teaching: '4->5', station_ashford: '3->4' },
    'the set of specs whose auto-connect road tier moved as a side-effect of BUG-652 changed — re-audit (a tier-5 connector is a motorway, not a dual carriageway)',
  );
});
