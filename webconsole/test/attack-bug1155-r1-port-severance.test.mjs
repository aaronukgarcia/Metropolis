// attack-bug1155-r1-port-severance.test.mjs - BUG-1155 destructive round r1
// (opus-round-r1-bug1155). RED on the r1 build by design: it is the repro the
// rework must turn green.
//
// FINDING: on the exact live fixture (box 220,20,16,16, phase 7) the r1 fix
// stops claiming the minor port's inside tile 220,35 because its only server
// is the dual spur at 220,34. The plan now validates, so it EXECUTES - and
// planStaleGridRemovals then scraps an existing road on 220,35 (it is
// "stillServed" by adjacency to the dual). The port's outside road 220,36 is
// diagonal to every plan tile, so the game's own computeRoadConnectivity no
// longer reaches it: a dead-end stub outside the box is severed and anything
// on it goes offline. The pre-fix "island" was a real road-to-road join to
// the dual; the validator's own-tier component count was the false positive.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { planBox, planStaleGridRemovals } from '../src/sim/consolidatorReplan.ts';
import { computeRoadConnectivity } from '../src/sim/data.ts';

const BOX = { x0: 220, y0: 20, w: 16, h: 16 };
const P = (ox, oy, ix, iy, tier) => ({ outside: { x: ox, y: oy }, inside: { x: ix, y: iy }, tier });
const PORTS = [
  P(219, 25, 220, 25, 'aroad'),
  P(219, 28, 220, 28, 'minor'),
  P(236, 30, 235, 30, 'dual'),
  P(219, 34, 220, 34, 'dual'),
  P(220, 36, 220, 35, 'minor'),
];
const SPEC = { rail: 'rail', motorway: 'm20', dual: 'rd_dual', aroad: 'rd_aroad', minor: 'road' };

test('BUG-1155 r1: an executed plan keeps every port outside tile on the game road network', () => {
  const plan = planBox({ box: BOX, contents: [], ports: PORTS, rungs: [], seed: 1, latticePhase: 7 });
  assert.deepEqual(plan.invariantFailures, [], 'premise: the plan validates and will execute');
  // The existing in-box road under the port's inside tile, as the live city has it.
  const existing = { id: 9001, spec: 'road', x: 220, y: 35, tier: 'minor', residents: 0, jobs: 0, capacity: 0, protectedFromDemolition: false };
  const removed = new Set(planStaleGridRemovals(BOX, plan.tierTiles, [existing], PORTS, 7).map((r) => r.id));
  const buildings = [];
  let id = 1;
  for (const t of Object.keys(SPEC)) for (const p of plan.tierTiles[t]) buildings.push({ id: id++, spec: SPEC[t], x: p.x, y: p.y, builtTick: 0 });
  if (!removed.has(9001) && !plan.tierTiles.minor.some((p) => p.x === 220 && p.y === 35)) buildings.push({ id: id++, spec: 'road', x: 220, y: 35, builtTick: 0 });
  // World link through the west aroad port; a dead-end stub through the south minor port.
  buildings.push({ id: id++, spec: 'road', x: 219, y: 25, builtTick: 0 }, { id: id++, spec: 'm20', x: 218, y: 25, builtTick: 0 });
  for (const y of [36, 37, 38]) buildings.push({ id: id++, spec: 'road', x: 220, y, builtTick: 0 });
  const conn = new Set(computeRoadConnectivity({ buildings }).connectedRoadTiles);
  assert.equal(conn.has('220,34'), true, 'premise: the box network is connected');
  assert.equal(conn.has('220,36'), true, 'the south minor port outside road must stay connected');
});
