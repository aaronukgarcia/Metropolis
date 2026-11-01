// BUG-1172 r2: does a ports-only input change exist in-engine (so a key without ports would false-hit)?
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf, replanDiscardMemoTestSeam } from '../src/sim/engine.ts';
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
const canon = (s) => JSON.stringify(s, (_k, v) => (v instanceof Map ? ['__map', [...v.entries()]] : v instanceof Set ? ['__set', [...v]] : v));

test('ports-only variant exists; with the shipped key it is a miss and warm==cold', () => {
  let captured = null;
  replanDiscardMemoTestSeam((input) => { captured = input; return planBox(input); });
  let s = fixture(), pre = s;
  for (let i = 0; i < 200 && !captured; i++) { pre = s; s = reducer(s, { type: 'tick' }); }
  assert.ok(captured);
  const A = captured; const box = A.box;
  const occ = new Set(pre.buildings.map((b) => `${b.x},${b.y}`));
  const halo = [];
  for (let x = box.x0 - 1; x <= box.x0 + box.w; x++) for (const y of [box.y0 - 1, box.y0 + box.h]) halo.push({ x, y });
  for (let y = box.y0; y < box.y0 + box.h; y++) for (const x of [box.x0 - 1, box.x0 + box.w]) halo.push({ x, y });
  let found = null;
  for (const t of halo) {
    if (occ.has(`${t.x},${t.y}`) || t.x < 0 || t.y < 0) continue;
    const st0 = { ...pre, buildings: [...pre.buildings, { id: 90000, spec: 'road', x: t.x, y: t.y, builtTick: -1000 }] };
    const st = { ...st0, roadConnectivity: computeRoadConnectivity(st0) };
    captured = null; reducer(st, { type: 'tick' });
    const B = captured;
    if (!B) continue;
    const same = (k) => JSON.stringify(A[k]) === JSON.stringify(B[k]);
    if (same('box') && same('contents') && same('latticePhase') && same('seed') && same('cityWideGroups') && same('rungs') && !same('ports')) { found = { t, st, B }; break; }
  }
  console.log('PORTSONLY', JSON.stringify({ found: !!found, tile: found?.t, portsA: A.ports.length, portsB: found?.B.ports.length }));
  assert.ok(found, 'a ports-only variant exists in-engine');
  const jsonA = JSON.stringify(A);
  const fail = (input) => JSON.stringify(input) === jsonA;
  replanDiscardMemoTestSeam((input) => { const p = planBox(input); return fail(input) ? { ...p, invariantFailures: ['forced'] } : p; });
  const coldB = reducer(found.st, { type: 'tick' });
  let calls = 0;
  replanDiscardMemoTestSeam((input) => { calls++; const p = planBox(input); return fail(input) ? { ...p, invariantFailures: ['forced'] } : p; });
  reducer(pre, { type: 'tick' });
  const c1 = calls;
  const warmB = reducer(found.st, { type: 'tick' });
  console.log('PORTSONLY_HIT', JSON.stringify({ hit: calls === c1, same: canon(warmB) === canon(coldB) }));
  assert.equal(calls, c1 + 1, 'ports-only change is a miss (re-planned)');
  assert.equal(canon(warmB), canon(coldB));
  replanDiscardMemoTestSeam(null);
});
