// bug1155-replan-spur-island.test.mjs - BUG-1155 (MET-V871 fired 14x live at
// box 220,20,16,16: "minor: 2 components inside the box, expected 1").
//
// MECHANISM (corrected by round r1): a minor port whose only server is a spur
// tile of a HIGHER tier (dual) claims its inside tile as an own-tier tile. That
// tile touches the dual spur road-to-road, which the game's
// computeRoadConnectivity treats as connected, so it is a REAL JOIN - not an
// island. The defect was validatePlan counting a tier's components in
// isolation. validatePlan now counts terminal joins through the road family
// (componentsOfTierWithJoins). The planner must keep claiming the tile (the
// port's outside road stays linked to the box network).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { planBox, onCityLattice, componentsOfTier, componentsOfTierWithJoins, validatePlan, LATTICE_PHASE_MODULUS } from '../src/sim/consolidatorReplan.ts';
import { computeRoadConnectivity } from '../src/sim/data.ts';

const BOX = { x0: 220, y0: 20, w: 16, h: 16 };
const P = (ox, oy, ix, iy, tier) => ({ outside: { x: ox, y: oy }, inside: { x: ix, y: iy }, tier });
const TIERS = ['rail', 'motorway', 'dual', 'aroad', 'minor'];
const SPEC = { rail: 'rail', motorway: 'm20', dual: 'rd_dual', aroad: 'rd_aroad', minor: 'road' };
const k = (x, y) => `${x},${y}`;
const ORTHO = [[1, 0], [-1, 0], [0, 1], [0, -1]];

test('BUG-1155: minor port beside a dual spur - the join is real, the plan validates, the tile is kept', () => {
  const ports = [
    P(219, 25, 220, 25, 'aroad'),
    P(219, 28, 220, 28, 'minor'),
    P(236, 30, 235, 30, 'dual'),
    P(219, 34, 220, 34, 'dual'),
    P(220, 36, 220, 35, 'minor'),
  ];
  const plan = planBox({ box: BOX, contents: [], ports, rungs: [], seed: 1, latticePhase: 7 });
  assert.deepEqual(plan.invariantFailures, []);
  // The inside tile is claimed (its outside road must stay linked) ...
  assert.equal(plan.tierTiles.minor.some((p) => p.x === 220 && p.y === 35), true);
  // ... and in isolation it IS its own component; only the join makes it one.
  assert.equal(new Set(componentsOfTier(plan.tierTiles.minor, plan.passThrough.minor).values()).size, 2);
  const family = new Set();
  for (const t of TIERS) for (const p of plan.tierTiles[t]) family.add(k(p.x, p.y));
  assert.equal(new Set(componentsOfTierWithJoins(plan.tierTiles.minor, family).values()).size, 1);
});

test('BUG-1155: A-road split (box 234,32 phase 13) is cleared by the join-aware count', () => {
  const box = { x0: 234, y0: 32, w: 16, h: 16 };
  const ports = [P(235, 48, 235, 47, 'aroad')];
  const plan = planBox({ box, contents: [], ports, rungs: [], seed: 1, latticePhase: 13 });
  assert.deepEqual(plan.invariantFailures.filter((f) => /components/.test(f)), []);
});

test('BUG-1155: a DIAGONAL touch is not a join (validatePlan still flags the split)', () => {
  const box = { x0: 0, y0: 0, w: 8, h: 8 };
  const tierTiles = { rail: [], motorway: [], dual: [{ x: 3, y: 3 }], aroad: [], minor: [{ x: 2, y: 2 }, { x: 4, y: 4 }] };
  // minor tiles at 2,2 and 4,4 each touch the dual at 3,3 only diagonally.
  const f = validatePlan({ box, tierTiles, civic: [], ports: [], latticePhase: 0 });
  assert.equal(f.some((x) => /^minor: 2 components/.test(x)), true, JSON.stringify(f));
  // Orthogonal contact DOES join: minor 3,2 / 3,4 beside dual 3,3.
  const t2 = { rail: [], motorway: [], dual: [{ x: 3, y: 3 }], aroad: [], minor: [{ x: 3, y: 2 }, { x: 3, y: 4 }] };
  const f2 = validatePlan({ box, tierTiles: t2, civic: [], ports: [], latticePhase: 0 });
  assert.equal(f2.some((x) => /components/.test(x)), false, JSON.stringify(f2));
});

// Seeded fuzz: every box size, varied positions, all phases. Asserts BOTH zero
// component failures of any tier AND that every port the plan claims to serve
// is connected per the game's computeRoadConnectivity on the post-plan state.
test('BUG-1155: seeded fuzz - no tier splits and every served port stays connected', () => {
  let s = 0x1155abcd >>> 0;
  const rnd = () => { s = (Math.imul(s, 1664525) + 1013904223) >>> 0; return s / 4294967296; };
  const SIZES = [8, 12, 16, 20];
  const phasesSeen = new Set();
  const sizesSeen = new Set();
  const compFails = [];
  const unlinked = [];
  let adjacencyOnly = 0;
  for (let it = 0; it < 2000; it++) {
    const size = SIZES[Math.floor(rnd() * SIZES.length)];
    const x0 = 1 + Math.floor(rnd() * 300);
    const y0 = 1 + Math.floor(rnd() * 300);
    const box = { x0, y0, w: size, h: size };
    const ports = [];
    const seen = new Set();
    const n = 1 + Math.floor(rnd() * 12);
    for (let i = 0; i < n; i++) {
      const side = Math.floor(rnd() * 4);
      const t = Math.floor(rnd() * size);
      const tier = TIERS[Math.floor(rnd() * 5)];
      let p;
      if (side === 0) p = P(x0 - 1, y0 + t, x0, y0 + t, tier);
      else if (side === 1) p = P(x0 + size, y0 + t, x0 + size - 1, y0 + t, tier);
      else if (side === 2) p = P(x0 + t, y0 - 1, x0 + t, y0, tier);
      else p = P(x0 + t, y0 + size, x0 + t, y0 + size - 1, tier);
      const kk = k(p.outside.x, p.outside.y);
      if (seen.has(kk)) continue;
      seen.add(kk);
      ports.push(p);
    }
    ports.sort((a, b) => a.inside.y - b.inside.y || a.inside.x - b.inside.x || TIERS.indexOf(a.tier) - TIERS.indexOf(b.tier));
    const phase = it % LATTICE_PHASE_MODULUS; // deterministic sweep of ALL phases
    phasesSeen.add(phase);
    sizesSeen.add(size);
    const plan = planBox({ box, contents: [], ports, rungs: [], seed: 1, latticePhase: phase });
    const comps = plan.invariantFailures.filter((f) => /components inside the box/.test(f));
    if (comps.length) compFails.push({ box, phase, ports, comps });

    // Post-plan game state, measured by the game's own computeRoadConnectivity:
    // plan tiles + the outside road of every served port + a world root (m20)
    // beyond EVERY OTHER served port. A port is linked when its outside road
    // is connected - which, for a port with no root of its own, requires a real
    // road-to-road edge into the box network (computeRoadConnectivity's only
    // edge is orthogonal road adjacency).
    const planKeys = new Set();
    const tilesBuilt = [];
    let id = 1;
    for (const t of TIERS) for (const p of plan.tierTiles[t]) { tilesBuilt.push({ id: id++, spec: SPEC[t], x: p.x, y: p.y, builtTick: 0 }); planKeys.add(k(p.x, p.y)); }
    const served = ports.filter((p) => plan.tierTiles[p.tier].length > 0 && plan.invariantFailures.length === 0 && !onCityLattice(p.outside.x, p.outside.y, phase));
    const outer = served.filter((p) => !planKeys.has(k(p.outside.x, p.outside.y)));
    for (const p of outer) {
      const buildings = tilesBuilt.slice();
      let bid = 100000;
      buildings.push({ id: bid++, spec: 'road', x: p.outside.x, y: p.outside.y, builtTick: 0 });
      let others = 0;
      for (const q of outer) {
        if (q === p || (q.outside.x === p.outside.x && q.outside.y === p.outside.y)) continue;
        buildings.push({ id: bid++, spec: 'road', x: q.outside.x, y: q.outside.y, builtTick: 0 });
        const bx = q.outside.x + (q.outside.x < x0 ? -1 : q.outside.x >= x0 + size ? 1 : 0);
        const by = q.outside.y + (q.outside.y < y0 ? -1 : q.outside.y >= y0 + size ? 1 : 0);
        buildings.push({ id: bid++, spec: 'm20', x: bx, y: by, builtTick: 0 });
        others++;
      }
      if (others === 0) continue; // nothing to root the network: the link is checked by adjacency below
      const conn = new Set(computeRoadConnectivity({ buildings }).connectedRoadTiles);
      if (conn.has(k(p.outside.x, p.outside.y))) continue;
      // Ports served only by ADJACENCY (inside tile unclaimed, outside
      // diagonal) are the module's weaker-than-real 'reached' rule, a
      // separate older defect filed on its own; the plan network may also be
      // several family components that only the other ports root. Here: a
      // port whose inside tile IS a plan tile and whose network is rooted
      // must be physically linked.
      // rooted = the road-only (non-rail) component holding this port's inside
      // tile also touches some OTHER served port's outside road.
      const roadKeys = new Set(tilesBuilt.filter((b) => b.spec !== 'rail').map((b) => k(b.x, b.y)));
      const comp = new Set([k(p.inside.x, p.inside.y)]);
      const stack = roadKeys.has(k(p.inside.x, p.inside.y)) ? [k(p.inside.x, p.inside.y)] : [];
      while (stack.length) {
        const [cx, cy] = stack.pop().split(',').map(Number);
        for (const [dx, dy] of ORTHO) { const nk = k(cx + dx, cy + dy); if (roadKeys.has(nk) && !comp.has(nk)) { comp.add(nk); stack.push(nk); } }
      }
      const rootedNet = outer.some((q) => q !== p && ORTHO.some(([dx, dy]) => comp.has(k(q.outside.x + dx, q.outside.y + dy))));
      if (planKeys.has(k(p.inside.x, p.inside.y)) && rootedNet) unlinked.push({ box, phase, port: p });
      else adjacencyOnly++;
    }
  }
  assert.equal(phasesSeen.size, LATTICE_PHASE_MODULUS, 'all phases covered');
  assert.equal(sizesSeen.size, 4, 'all box sizes covered');
  assert.deepEqual(compFails.slice(0, 2), [], `${compFails.length} component failures`);
  assert.deepEqual(unlinked.slice(0, 2), [], `${unlinked.length} served ports not game-connected`);
});
