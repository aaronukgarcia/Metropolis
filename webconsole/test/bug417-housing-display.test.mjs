// BUG-417 — housing-capacity DISPLAY honesty.
//
// residentsCapacity() sums ALL residential buildings (incl. offline /
// under-construction), but engine population growth only fills ONLINE dwellings.
// The DISPLAY layer must show the ONLINE figure as the honest headline plus a
// "+N under construction" breakdown (= gross − online). These tests assert the
// pure helpers behind that display, on concrete built scenarios.
//
// Pure state -> value (GR#21). Each test can FAIL: swap the helper for the gross
// residentsCapacity and #1/#3 break; drop the online gate and #2 stays but the
// split in #1 goes wrong.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  residentsCapacity,
  onlineResidentsCapacity,
  underConstructionResidents,
} from '../src/sim/data.ts';

function minimalState(overrides = {}) {
  return {
    tick: 100,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 0,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    fundsAtTickStart: 10000000,
    fundsAtTickEnd: 10000000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    ...overrides,
  };
}

const HUT = SPECS.res_hut.residents ?? 8;

// A residential building with builtTick=null is instantly ONLINE (isOnline
// short-circuits). One stamped with builtTick=s.tick is mid-construction ->
// OFFLINE. This mirrors the engine's isOnline gate the growth loop uses.
function onlineHut(id) {
  return { id, spec: 'res_hut', x: id, y: 0, builtTick: null };
}
function buildingHut(id, tick) {
  return { id, spec: 'res_hut', x: id, y: 0, builtTick: tick };
}

test('BUG-417 #1: with some dwellings under construction, online < gross and the split is exactly the difference', () => {
  const tick = 100;
  const s = minimalState({
    tick,
    buildings: [
      onlineHut(1),
      onlineHut(2),
      onlineHut(3), // 3 online
      buildingHut(4, tick), // 2 under construction (built this tick, not yet online)
      buildingHut(5, tick),
    ],
  });

  const gross = residentsCapacity(s);
  const online = onlineResidentsCapacity(s);
  const uc = underConstructionResidents(s);

  assert.equal(gross, 5 * HUT, 'gross counts all 5 residential buildings');
  assert.equal(online, 3 * HUT, 'online counts only the 3 online dwellings');
  assert.ok(online < gross, 'online capacity is strictly below the gross total');
  assert.equal(uc, 2 * HUT, 'under-construction split = 2 dwellings worth');
  assert.equal(uc, gross - online, 'under-construction split equals gross − online exactly');
});

test('BUG-417 #2: when all residential are online, online == gross and under-construction is 0', () => {
  const s = minimalState({
    buildings: [onlineHut(1), onlineHut(2), onlineHut(3), onlineHut(4)],
  });

  assert.equal(onlineResidentsCapacity(s), residentsCapacity(s), 'online equals gross when all online');
  assert.equal(underConstructionResidents(s), 0, 'nothing under construction');
});

test('BUG-417 #3: the headline display figure is the ONLINE value, not the gross', () => {
  const tick = 100;
  const s = minimalState({
    tick,
    buildings: [onlineHut(1), buildingHut(2, tick), buildingHut(3, tick)],
  });

  // What the "Housing cap" tile / debug headline shows.
  const headline = onlineResidentsCapacity(s);
  assert.equal(headline, 1 * HUT, 'headline is the online capacity');
  assert.notEqual(headline, residentsCapacity(s), 'headline is NOT the gross residentsCapacity');
  assert.equal(headline + underConstructionResidents(s), residentsCapacity(s), 'headline + under-construction reconstructs the gross total');
});

test('BUG-417 #4: helpers are deterministic — same state yields identical values across calls', () => {
  const tick = 100;
  const s = minimalState({
    tick,
    buildings: [onlineHut(1), onlineHut(2), buildingHut(3, tick)],
  });

  for (let i = 0; i < 5; i++) {
    assert.equal(onlineResidentsCapacity(s), 2 * HUT, 'online stable');
    assert.equal(underConstructionResidents(s), 1 * HUT, 'under-construction stable');
    assert.equal(residentsCapacity(s), 3 * HUT, 'gross stable');
  }
  // Never negative even if (hypothetically) online exceeded gross.
  assert.ok(underConstructionResidents(s) >= 0, 'split is never negative');
});
