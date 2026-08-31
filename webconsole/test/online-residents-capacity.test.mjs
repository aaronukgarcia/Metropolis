import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, onlineResidentsCapacity } from '../src/sim/data.ts';

function minimalState(overrides = {}) {
  return {
    tick: 10,
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

test('BUG-417: online hut counts toward display housing cap', () => {
  const hut = SPECS.res_hut.residents ?? 8;
  const s = minimalState({
    buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: null }],
  });
  assert.equal(onlineResidentsCapacity(s), hut);
});

test('BUG-417: under-construction hut (builtTick = now) does not count', () => {
  const now = 10;
  const s = minimalState({
    tick: now,
    buildings: [{ id: 1, spec: 'res_hut', x: 0, y: 0, builtTick: now }],
  });
  assert.equal(onlineResidentsCapacity(s), 0);
});
