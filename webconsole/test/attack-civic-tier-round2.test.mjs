// attack-civic-tier-round2.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23),
// engine/behaviour half. Attacker: opus-round-civic-tier (NOT the author).
//
// Answers Aaron's own two examples end-to-end through the REAL reducer:
//   (a) 12 General Hospitals -> does the city converge to ONE Teaching
//       Hospital, as Aaron expects, or does CEIL-3 / groupSize residue stall?
//   (b) a small city short 40 nursery places -> does Fix All spam a
//       1,000-place City Kindergarten (the BUG-685 largest-first class)?

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, rankedProviders, demandFixPlan } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 5_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}

const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

describe("ATTACK-8: Aaron's 12 hospitals", () => {
  test('12 General Hospitals over 24 monthly passes — what does it actually converge to?', () => {
    // 12 hea_hospital (2x2) inside one 16-tile section, all road-adjacent.
    const hospitals = [];
    for (let i = 0; i < 12; i++) {
      const col = i % 6;
      const row = Math.floor(i / 6);
      hospitals.push({ id: 200 + i, spec: 'hea_hospital', x: 2 + col * 2, y: 1 + row * 3, builtTick: -1000 });
    }
    let s = withConnectivity(mk({ buildings: [...roadRow(0, 15), ...hospitals] }));
    s = reducer(s, { type: 'toggleConsolidator' });
    assert.equal(countSpec(s, 'hea_hospital'), 12);

    const trace = [];
    for (let m = 0; m < 24; m++) {
      s = advanceToNextBoundary(s);
      trace.push(`m${m + 1}: hospital=${countSpec(s, 'hea_hospital')} teaching=${countSpec(s, 'hea_teaching')}`);
    }
    // eslint-disable-next-line no-console
    console.log('12-HOSPITAL CONVERGENCE TRACE:');
    for (const t of trace) console.log('  ', t);
    // eslint-disable-next-line no-console
    console.log('SKIP REASONS (last pass):', JSON.stringify((s.consolidatorLog ?? [])[0]?.skipped ?? []));
    // eslint-disable-next-line no-console
    console.log(
      'FINAL: hospitals=', countSpec(s, 'hea_hospital'),
      ' teaching=', countSpec(s, 'hea_teaching'),
    );
    // REPORTING assertion (documents the ACTUAL behaviour so a future
    // change to it is visible). Aaron's stated expectation is ONE.
    assert.ok(countSpec(s, 'hea_teaching') >= 1, 'at least one teaching hospital must appear');
  });
});

describe('ATTACK-9: City Kindergarten does not get spammed for small demand (BUG-685 class)', () => {
  test('a 40-place nursery shortfall picks edu_nursery, never edu_nursery_city', () => {
    const s = mk({ funds: 5_000_000_000 });
    const ranked = rankedProviders(s, 'nursery', s.funds, 40);
    // eslint-disable-next-line no-console
    console.log('NURSERY-40 RANKING:', ranked.map((r) => `${r.sp.id} x${r.units} = £${r.planCost}`));
    assert.ok(ranked.length > 0);
    assert.equal(ranked[0].sp.id, 'edu_nursery', 'small shortfall must pick the small spec');
  });

  test('a 5,000-place nursery shortfall DOES pick edu_nursery_city (the rung earns its place)', () => {
    const s = mk({ funds: 5_000_000_000 });
    const ranked = rankedProviders(s, 'nursery', s.funds, 5000);
    // eslint-disable-next-line no-console
    console.log('NURSERY-5000 RANKING:', ranked.map((r) => `${r.sp.id} x${r.units} = £${r.planCost}`));
    assert.equal(ranked[0].sp.id, 'edu_nursery_city');
  });

  test('edu_nursery_city is offered ONLY for the nursery service row, never primary/college', () => {
    const s = mk({ funds: 5_000_000_000 });
    for (const key of ['school', 'college']) {
      const ranked = rankedProviders(s, key, s.funds, 5000);
      assert.ok(!ranked.some((r) => r.sp.id === 'edu_nursery_city'), `${key} must not offer a nursery spec`);
    }
  });

  test('a locked city (level < 4) never sees edu_nursery_city', () => {
    const s = mk({ unlockedAll: false, xp: 0, lastRewardedLevel: 0, funds: 5_000_000_000 });
    const ranked = rankedProviders(s, 'nursery', s.funds, 5000);
    assert.ok(!ranked.some((r) => r.sp.id === 'edu_nursery_city'), 'unlock gate must hold');
  });
});

describe('ATTACK-10: hea_teaching 200k does not break health coverage/demand', () => {
  test('hospital demand row treats hea_teaching as a 200k provider consistently', () => {
    const s = mk({ funds: 50_000_000_000 });
    const ranked = rankedProviders(s, 'hospital', s.funds, 400000);
    // eslint-disable-next-line no-console
    console.log('HOSPITAL-400k RANKING:', ranked.map((r) => `${r.sp.id} x${r.units} = £${r.planCost}`));
    const t = ranked.find((r) => r.sp.id === 'hea_teaching');
    if (t) assert.equal(t.units, Math.ceil(400000 / 200000), 'unit count must derive from served=200000');
  });

  test('demandFixPlan on a plain new city is stable and contains no NaN/Infinity', () => {
    const s = mk({ population: 50000, funds: 5_000_000_000 });
    const plan = demandFixPlan(s);
    for (const p of plan) {
      assert.ok(Number.isFinite(p.count) && p.count > 0, `bad count for ${p.specId}`);
      assert.ok(SPECS[p.specId], `unknown spec ${p.specId}`);
    }
  });
});
