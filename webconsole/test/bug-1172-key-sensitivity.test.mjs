// bug-1172-key-sensitivity.test.mjs — BUG-1172 r2 rework. The discard memo key
// (replanDiscardKeyOf) must be sensitive to EVERY planner input field: a false
// hit changes state (a sound plan is discarded). One realistic planner input is
// captured from the real engine, then exactly one field is varied at a time.
import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, replanDiscardMemoTestSeam, replanDiscardKeyOf } from '../src/sim/engine.ts';
import { planBox } from '../src/sim/consolidatorReplan.ts';

function mk(over) {
  return { ...initialState(), unlockedAll: true, roadMonitors: [], buildingMonitors: [], buildings: [], population: 0, funds: 1_000_000_000, tick: 0,
    consolidatorEnabled: false, consolidatorLayoutEnabled: true, consolidatorLog: [], consolidatorMode: 'glide',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL), lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)), ...over };
}
function healthy(s) {
  let cur = { ...s, consolidatorLayoutEnabled: false };
  cur = reducer(cur, { type: 'tick' });
  return { ...cur, consolidatorLayoutEnabled: s.consolidatorLayoutEnabled, tick: s.tick, consolidatorLog: s.consolidatorLog ?? [] };
}
function fixture() {
  const buildings = [];
  let id = 1;
  for (let x = 0; x < 32; x++) buildings.push({ id: id++, spec: 'road', x, y: 8, builtTick: -1000 });
  const base = mk({ buildings, population: 1_000, funds: 500_000_000 });
  const s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
  return reducer(healthy(s), { type: 'toggleConsolidator' });
}
function captureInput() {
  let captured = null;
  replanDiscardMemoTestSeam((input) => { captured = input; return planBox(input); });
  let s = fixture();
  for (let i = 0; i < 200 && !captured; i++) s = reducer(s, { type: 'tick' });
  assert.ok(captured, 'captured a real planner input from the engine');
  return captured;
}
const clone = (o) => JSON.parse(JSON.stringify(o));

afterEach(() => replanDiscardMemoTestSeam(null));

const VARIANTS = {
  box: (i) => ({ ...i, box: { ...i.box, x0: i.box.x0 + 1 } }),
  ports: (i) => ({ ...i, ports: [...i.ports, { x: 9999, y: 9999 }] }),
  latticePhase: (i) => ({ ...i, latticePhase: (i.latticePhase ?? 0) + 1 }),
  seed: (i) => ({ ...i, seed: i.seed + 1 }),
  contents: (i) => ({ ...i, contents: [...i.contents, { sentinel: 'extra-content' }] }),
  cityWideGroups: (i) => ({ ...i, cityWideGroups: [...(i.cityWideGroups ?? []), { sentinel: 'extra-group' }] }),
  rungs: (i) => ({ ...i, rungs: [...i.rungs, { sentinel: 'extra-rung' }] }),
};

test('structurally equal inputs built separately share one key (and the key is a string)', () => {
  const a = captureInput();
  const b = clone(a);
  assert.equal(typeof replanDiscardKeyOf(a), 'string');
  assert.equal(replanDiscardKeyOf(a), replanDiscardKeyOf(b));
  // undefined latticePhase / cityWideGroups default to 0 / [] identically.
  const bare = { ...a, latticePhase: undefined, cityWideGroups: undefined };
  const dflt = { ...a, latticePhase: 0, cityWideGroups: [] };
  assert.equal(replanDiscardKeyOf(bare), replanDiscardKeyOf(dflt));
});

for (const [field, vary] of Object.entries(VARIANTS)) {
  test(`key is sensitive to ${field}: varying ONLY that field changes the key`, () => {
    const a = captureInput();
    const v = vary(clone(a));
    for (const k of Object.keys(VARIANTS)) {
      if (k !== field) assert.equal(JSON.stringify(v[k] ?? null), JSON.stringify(a[k] ?? null), `variant left ${k} untouched`);
    }
    assert.notEqual(replanDiscardKeyOf(v), replanDiscardKeyOf(a), `${field} must be part of the discard key`);
  });
}
