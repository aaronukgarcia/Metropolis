// attack-bug1151-r1-wedge6-realpass.test.mjs — independent round r1 (opus-round-r1-bug1151)
// attack suite for BUG-1151, kept for sustained value.
//
// 1. The EXACT review shape: 6 res_block in one section, the lowest id auto-
//    scaled to tier 9. On the pre-fix code the finder offered only the
//    lowest-id groups (res_midrise ids 100..103 gain -41, res_penthouse
//    100..104 gain -31) and a REAL reducer pass committed nothing. The fix
//    must form non-negative groups from the five tier-0 members AND the
//    real pass must actually commit a consolidate transaction (scaled
//    building survives, the five tier-0 ones are replaced, money is billed).
// 2. Reported ids stay ascending even when a scaled member IS selected (kills
//    the "drop the ascending re-sort" mutant the lane suite let survive), and
//    two equal-capacity scaled members tie-break by id.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { findOpportunities, sectionKeyOf } from '../src/sim/consolidator.ts';

function wedge6() {
  const road = [];
  for (let x = 0; x <= 40; x++) road.push({ id: 5000 + x, spec: 'road', x, y: 15, builtTick: -1000 });
  const blocks = [];
  for (let i = 0; i < 6; i++) {
    blocks.push({ id: 100 + i, spec: 'res_block', x: 16 + i * 2, y: 13, builtTick: -1000, ...(i === 0 ? { capacityTier: 9 } : {}) });
  }
  // residential capacity far away, so the CEIL-3 family-share ceiling is not under test
  const head = [];
  for (let i = 0; i < 20; i++) head.push({ id: 900 + i, spec: 'res_block', x: 200 + (i % 10) * 3, y: 200 + Math.floor(i / 10) * 3, builtTick: -1000 });
  const s = {
    ...initialState(),
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    population: 0,
    funds: 100_000_000,
    consolidatorEnabled: true,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    buildings: [...road, ...blocks, ...head],
    tick: TICKS_PER_MONTH - 1,
    nextId: 9000,
  };
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

test('BUG-1151 r1: 6-building review shape offers non-negative midrise/penthouse groups without the scaled id', () => {
  const s = wedge6();
  const opps = findOpportunities(s, [sectionKeyOf(16, 13)]).filter((o) => o.fromSpec === 'res_block');
  const mid = opps.find((o) => o.toSpec === 'res_midrise');
  const pent = opps.find((o) => o.toSpec === 'res_penthouse');
  assert.ok(mid && pent, 'both small rungs must be offered');
  assert.deepEqual(mid.buildingIds, [101, 102, 103, 104]);
  assert.deepEqual(pent.buildingIds, [101, 102, 103, 104, 105]);
  assert.ok(mid.capacityGain >= 0 && pent.capacityGain >= 0, `gains ${mid.capacityGain}/${pent.capacityGain}`);
});

test('BUG-1151 r1: a REAL reducer pass on the 6-building wedge commits the merge and bills it', () => {
  const s = wedge6();
  const t = reducer(s, { type: 'tick' });
  const pass = (t.consolidatorLog ?? [])[0];
  const consolidates = (pass?.transactions ?? []).filter((x) => x.kind === 'consolidate');
  assert.equal(consolidates.length, 1, 'exactly one consolidate transaction must commit');
  assert.ok(consolidates[0].buildCost > 0);
  const remaining = t.buildings.filter((b) => b.id >= 100 && b.id <= 105).map((b) => b.id);
  assert.deepEqual(remaining, [100], 'only the scaled member survives');
  assert.ok(t.buildings.some((b) => b.id >= 9000 && b.spec.startsWith('res_')), 'a residential successor was built');
  assert.ok(t.funds < s.funds, 'the merge was billed');
});

test('BUG-1151 r1: reported ids stay ascending when a scaled member must be included; scaled ties break by id', () => {
  // 5 res_block, ids 1 and 2 both tier 9: res_midrise (4) must take 3,4,5 plus ONE scaled member, and it must be id 1.
  const buildings = [];
  for (let i = 1; i <= 5; i++) buildings.push({ id: i, spec: 'res_block', x: i, y: 1, ...(i <= 2 ? { capacityTier: 9 } : {}) });
  const s = { ...initialState(), buildings, nextId: 1000, tick: 0 };
  const mid = findOpportunities(s, [0]).find((o) => o.fromSpec === 'res_block' && o.toSpec === 'res_midrise');
  assert.ok(mid);
  assert.deepEqual(mid.buildingIds, [1, 3, 4, 5]);
});
