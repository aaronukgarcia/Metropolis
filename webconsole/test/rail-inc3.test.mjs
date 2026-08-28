// rail-inc3.test.mjs — FEAT-1972079902 inc3: auto-branch-lining on gateway placement.
//
// On placing a GATEWAY (Ashford International `station_ashford` / International
// Airport `land_airport`), the engine deterministically lays a branch line to the
// nearest slow-'rail' line AND the nearest 'hs1' line, routing AROUND buildings,
// via journaled reducer mutations that re-derive on genesis replay.
//
// Determinism is the crux (GR#21): planRailBranch is the SAME level-synchronous BFS
// idiom as road inc1's planConnector — strict (x,y) tie-break, fixed direction
// ordinal, cost budget, no Date/Math.random.
//
// RED proof (scratch cp/mv, NEVER git): shuffle planRailBranch's frontier before the
// goal scan and the "router determinism" test goes RED; restoring railConnect.ts
// returns it GREEN. Demonstrated at build time with cp/mv, never a git revert.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { planRailBranch, RAIL_BRANCH_BUDGET } from '../src/sim/railConnect.ts';
import { SPECS, MAP_W, MAP_H } from '../src/sim/data.ts';
import { initialState, reducer, isGatewaySpec, NO_RAIL_ROUTE_NOTICE } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const key = (x, y) => `${x},${y}`;

// A clean board: initialState() but with the starter city REPLACED by an explicit
// building list, unlockedAll so gateways (unlock 5/6) are placeable.
function board(buildings) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, buildings, nextId: maxId + 1, roadNotice: null, railNotice: null };
}

// Lay a horizontal line of 1×1 tiles of `spec` at row y, x in [x0,x1).
function line(spec, y, x0, x1, startId) {
  const out = [];
  let id = startId;
  for (let x = x0; x < x1; x++) out.push({ id: id++, spec, x, y, builtTick: 0 });
  return out;
}

// Footprint cells of a spec placed at (gx,gy).
function footprintCells(specId, gx, gy) {
  const sp = SPECS[specId];
  const cells = [];
  for (let dx = 0; dx < sp.w; dx++) for (let dy = 0; dy < sp.h; dy++) cells.push({ x: gx + dx, y: gy + dy });
  return cells;
}

const ORTHO = [
  [0, -1],
  [-1, 0],
  [1, 0],
  [0, 1],
];

// Is `cells` a single orthogonally-connected component?
function isContiguous(cells) {
  if (cells.length === 0) return false;
  const set = new Set(cells.map((c) => key(c.x, c.y)));
  const seen = new Set([key(cells[0].x, cells[0].y)]);
  const stack = [cells[0]];
  while (stack.length) {
    const c = stack.pop();
    for (const [dx, dy] of ORTHO) {
      const k = key(c.x + dx, c.y + dy);
      if (set.has(k) && !seen.has(k)) {
        seen.add(k);
        stack.push({ x: c.x + dx, y: c.y + dy });
      }
    }
  }
  return seen.size === cells.length;
}

function anyAdjacent(cells, targetSet) {
  for (const c of cells) {
    for (const [dx, dy] of ORTHO) if (targetSet.has(key(c.x + dx, c.y + dy))) return true;
  }
  return false;
}

// New buildings added by the reducer, split by spec.
function newTilesOf(before, after, spec) {
  const beforeIds = new Set(before.buildings.map((b) => b.id));
  return after.buildings.filter((b) => !beforeIds.has(b.id) && b.spec === spec);
}

// ===================== gateway trigger set =====================

test('gateway trigger: exactly Ashford International + International Airport', () => {
  assert.equal(isGatewaySpec(SPECS.station_ashford), true, 'station_ashford is a gateway');
  assert.equal(isGatewaySpec(SPECS.land_airport), true, 'land_airport is a gateway');
  assert.equal(isGatewaySpec(SPECS.station_sanderling), false, 'a normal station is NOT a gateway');
  assert.equal(isGatewaySpec(SPECS.res_hut), false, 'a house is NOT a gateway');
  assert.equal(isGatewaySpec(undefined), false, 'undefined spec is not a gateway');
});

// ===================== 1. both-lines-connected =====================

test('both-lines-connected: a gateway with a rail line and an hs1 line branches to EACH', () => {
  // rail line at y=10, hs1 line at y=30 (both x 0..59). Gateway between them at y=18.
  const rail = line('rail', 10, 0, 60, 1);
  const hs1 = line('hs1', 30, 0, 60, 1000);
  const s = board([...rail, ...hs1]);

  const gx = 30;
  const gy = 18;
  const after = reducer(s, { type: 'place', spec: 'station_ashford', x: gx, y: gy });

  assert.ok(
    after.buildings.some((b) => b.spec === 'station_ashford' && b.x === gx && b.y === gy),
    'the gateway was placed'
  );

  const railBranch = newTilesOf(s, after, 'rail');
  const hs1Branch = newTilesOf(s, after, 'hs1');
  assert.ok(railBranch.length > 0, 'a branch of rail tiles was laid to the slow line');
  assert.ok(hs1Branch.length > 0, 'a branch of hs1 tiles was laid to the high-speed line');

  const foot = new Set(footprintCells('station_ashford', gx, gy).map((c) => key(c.x, c.y)));
  const railLineSet = new Set(rail.map((b) => key(b.x, b.y)));
  const hs1LineSet = new Set(hs1.map((b) => key(b.x, b.y)));

  // Each branch is a contiguous chain, station-adjacent at one end, line-adjacent at the other.
  assert.ok(isContiguous(railBranch), 'rail branch is one contiguous path');
  assert.ok(anyAdjacent(railBranch, foot), 'rail branch touches the station');
  assert.ok(anyAdjacent(railBranch, railLineSet), 'rail branch touches the slow-rail line');

  assert.ok(isContiguous(hs1Branch), 'hs1 branch is one contiguous path');
  assert.ok(anyAdjacent(hs1Branch, foot), 'hs1 branch touches the station');
  assert.ok(anyAdjacent(hs1Branch, hs1LineSet), 'hs1 branch touches the HS1 line');

  assert.equal(after.railNotice, null, 'both branches connected — no notice');
});

test('gateway trigger includes the AIRPORT (and a non-gateway lays nothing)', () => {
  const rail = line('rail', 90, 0, 120, 1);
  const s = board(rail);
  // land_airport is 70×70; footprint y10..79, rail below at y90.
  const after = reducer(s, { type: 'place', spec: 'land_airport', x: 10, y: 10 });
  assert.ok(after.buildings.some((b) => b.spec === 'land_airport'), 'airport placed');
  assert.ok(newTilesOf(s, after, 'rail').length > 0, 'airport auto-branched to the rail line');

  // A non-gateway building never triggers rail branching.
  const s2 = board(line('rail', 20, 0, 60, 1));
  const after2 = reducer(s2, { type: 'place', spec: 'res_hut', x: 30, y: 10 });
  assert.equal(newTilesOf(s2, after2, 'rail').length, 0, 'a house lays no rail branch');
  assert.equal(after2.railNotice, null, 'non-gateway clears the rail notice');
});

// ===================== 2. router determinism =====================

test('router determinism: same board + placement twice → byte-identical tiles + ledger', () => {
  const mk = () => {
    const rail = line('rail', 10, 0, 60, 1);
    const hs1 = line('hs1', 30, 0, 60, 1000);
    // An obstacle straddling the direct routes so a tie-break actually matters.
    const wall = [{ id: 5000, spec: 'com_shop', x: 31, y: 15, builtTick: 0 }];
    return board([...rail, ...hs1, ...wall]);
  };
  const a = reducer(mk(), { type: 'place', spec: 'station_ashford', x: 30, y: 18 });
  const b = reducer(mk(), { type: 'place', spec: 'station_ashford', x: 30, y: 18 });

  assert.equal(JSON.stringify(a.buildings), JSON.stringify(b.buildings), 'identical laid tiles');
  assert.equal(a.funds, b.funds, 'identical spend');
  assert.equal(JSON.stringify(a.ledger), JSON.stringify(b.ledger), 'identical ledger');
  assert.equal(a.railNotice, b.railNotice, 'identical notice');
});

// Direct router-level determinism + the pure planner's tie-break.
test('planRailBranch: two independent runs produce the identical plan', () => {
  const mk = () => ({
    occupied: new Set([key(5, 5), key(5, 2)]),
    targets: new Set([key(5, 2)]),
    bx: 5,
    by: 5,
    bw: 1,
    bh: 1,
    mapW: MAP_W,
    mapH: MAP_H,
    budget: RAIL_BRANCH_BUDGET,
  });
  const r1 = planRailBranch(mk());
  const r2 = planRailBranch(mk());
  assert.equal(r1.blocked, false);
  assert.equal(r1.connected, false);
  assert.deepEqual(r1, r2, 'independent runs identical');
  // The branch reaches a cell adjacent to the target line tile at (5,2): ends at (5,3).
  assert.deepEqual(r1.path[r1.path.length - 1], { x: 5, y: 3 }, 'branch ends adjacent to the line');
});

// ===================== 3. around-buildings =====================

test('around-buildings: an obstacle forces a detour; no branch tile lands on an occupied cell', () => {
  const rail = line('rail', 10, 0, 60, 1);
  // A wall just above the gateway footprint blocks the straight climb, forcing a
  // detour around its ends. Gateway station_ashford at (30,16) → footprint y16..17.
  const wall = line('com_shop', 15, 28, 36, 2000); // x 28..35 at y15
  const s = board([...rail, ...wall]);

  const gx = 30;
  const gy = 16;
  const before = s.buildings.length;
  const after = reducer(s, { type: 'place', spec: 'station_ashford', x: gx, y: gy });

  const railBranch = newTilesOf(s, after, 'rail');
  assert.ok(railBranch.length > 0, 'a detour branch was laid');
  assert.ok(after.buildings.length > before + 1, 'more than just the gateway was added');

  const occupied = new Set(
    s.buildings.flatMap((b) => {
      const sp = SPECS[b.spec];
      const cells = [];
      for (let dx = 0; dx < sp.w; dx++) for (let dy = 0; dy < sp.h; dy++) cells.push(key(b.x + dx, b.y + dy));
      return cells;
    })
  );
  // No branch tile may sit on ANY pre-existing occupied cell (buildings impassable).
  for (const t of railBranch) assert.ok(!occupied.has(key(t.x, t.y)), `branch tile ${t.x},${t.y} must be empty`);
  // Specifically it must avoid the wall row.
  assert.ok(!railBranch.some((t) => t.y === 15 && t.x >= 28 && t.x <= 35), 'must route around the wall');

  // …and still connect station → rail line.
  const foot = new Set(footprintCells('station_ashford', gx, gy).map((c) => key(c.x, c.y)));
  const railLineSet = new Set(rail.map((b) => key(b.x, b.y)));
  assert.ok(isContiguous(railBranch), 'detour branch is contiguous');
  assert.ok(anyAdjacent(railBranch, foot), 'branch touches the station');
  assert.ok(anyAdjacent(railBranch, railLineSet), 'branch reaches the rail line');
});

// ===================== 4. blocked → notice =====================

test('blocked→notice: a fully walled-off gateway lays NO branch, sets the notice, no crash', () => {
  // A rail line + hs1 line far away, and the gateway boxed in on every orthogonal
  // side so NO branch can escape its footprint. station_ashford at (30,30), 4×2.
  const rail = line('rail', 5, 0, 60, 1);
  const hs1 = line('hs1', 8, 0, 60, 1000);
  const gx = 30;
  const gy = 30;
  const walls = [];
  let id = 3000;
  // top (y29) + bottom (y32) across x30..33
  for (let x = gx; x < gx + 4; x++) {
    walls.push({ id: id++, spec: 'com_shop', x, y: gy - 1, builtTick: 0 });
    walls.push({ id: id++, spec: 'com_shop', x, y: gy + 2, builtTick: 0 });
  }
  // left (x29) + right (x34) across y30..31
  for (let y = gy; y < gy + 2; y++) {
    walls.push({ id: id++, spec: 'com_shop', x: gx - 1, y, builtTick: 0 });
    walls.push({ id: id++, spec: 'com_shop', x: gx + 4, y, builtTick: 0 });
  }
  const s = board([...rail, ...hs1, ...walls]);

  const railBefore = s.buildings.filter((b) => b.spec === 'rail').length;
  const hs1Before = s.buildings.filter((b) => b.spec === 'hs1').length;
  const fundsBefore = s.funds;

  let after;
  assert.doesNotThrow(() => {
    after = reducer(s, { type: 'place', spec: 'station_ashford', x: gx, y: gy });
  }, 'a fully blocked gateway must not crash');

  assert.ok(after.buildings.some((b) => b.spec === 'station_ashford'), 'gateway still placed');
  assert.equal(after.buildings.filter((b) => b.spec === 'rail').length, railBefore, 'no rail branch tiles laid');
  assert.equal(after.buildings.filter((b) => b.spec === 'hs1').length, hs1Before, 'no hs1 branch tiles laid');
  assert.equal(after.railNotice, NO_RAIL_ROUTE_NOTICE, 'a "no rail route" notice is surfaced');

  // Conservation: the branch spent nothing (station_ashford's own place cost aside).
  const stationCost = SPECS.station_ashford.cost;
  assert.equal(after.funds, fundsBefore - stationCost, 'only the gateway cost was charged; no branch spend');
  const branchLedger = after.ledger.filter((e) => e.label.startsWith('Rail branch'));
  assert.equal(branchLedger.length, 0, 'no branch ledger entries when nothing was laid');
});

// ===================== 5. missing-line =====================

test('missing-line: no hs1 on the map → only the rail branch is laid, no crash', () => {
  const rail = line('rail', 10, 0, 60, 1); // rail present, NO hs1 anywhere
  const s = board(rail);
  let after;
  assert.doesNotThrow(() => {
    after = reducer(s, { type: 'place', spec: 'station_ashford', x: 30, y: 18 });
  });
  assert.ok(newTilesOf(s, after, 'rail').length > 0, 'the rail branch was laid');
  assert.equal(newTilesOf(s, after, 'hs1').length, 0, 'no hs1 branch (that line is absent)');
  assert.equal(after.railNotice, null, 'absent line is skipped silently — no notice');
});

test('missing-line: NO rail lines at all → nothing laid, no notice, no crash', () => {
  const s = board([{ id: 1, spec: 'res_hut', x: 100, y: 100, builtTick: 0 }]);
  let after;
  assert.doesNotThrow(() => {
    after = reducer(s, { type: 'place', spec: 'station_ashford', x: 30, y: 18 });
  });
  assert.equal(newTilesOf(s, after, 'rail').length, 0, 'no rail branch');
  assert.equal(newTilesOf(s, after, 'hs1').length, 0, 'no hs1 branch');
  assert.equal(after.railNotice, null, 'both lines absent → skipped silently, no notice');
});

// ===================== 6. journal / conservation + genesis replay =====================

// Drive actions from genesis (starter city) mirroring the store: record with the
// pre-dispatch tick, then advance the live state through the pure reducer.
function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// Starter city has rail at y=84 and hs1 at y=205 (both full width). A gateway placed
// BETWEEN them at y=140 (clear column x=300) branches UP to rail and DOWN to hs1.
const REPLAY_SCRIPT = [
  { type: 'unlockAll' }, // station_ashford is unlock 5 — god-mode makes it placeable at genesis xp
  { type: 'place', spec: 'station_ashford', x: 300, y: 140 },
  { type: 'tick' },
];

test('conservation: a gateway auto-lay + a tick keeps the tick-boundary invariant', () => {
  const { liveState } = driveAndRecord(REPLAY_SCRIPT);
  const report = runConsistencyChecks(liveState);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(check.ok, true, 'conservation holds after gateway auto-branch + tick');
});

test('genesis replay reproduces the gateway branches byte-identically', () => {
  const { journal, liveState } = driveAndRecord(REPLAY_SCRIPT);
  // Sanity: the live run actually laid BOTH branches in the starter city.
  const genesis = initialState();
  const railGenesis = genesis.buildings.filter((b) => b.spec === 'rail').length;
  const hs1Genesis = genesis.buildings.filter((b) => b.spec === 'hs1').length;
  assert.ok(
    liveState.buildings.filter((b) => b.spec === 'rail').length > railGenesis,
    'a rail branch was laid over the genesis rail line'
  );
  assert.ok(
    liveState.buildings.filter((b) => b.spec === 'hs1').length > hs1Genesis,
    'an hs1 branch was laid over the genesis hs1 line'
  );

  const replayed = replayFromGenesis(journal);
  assert.deepEqual(replayed, liveState, 'genesis replay reconstructs the exact branched city');
});

test('RED proof: a mutated genesis diverges — the fidelity assertion can fail', () => {
  const { journal, liveState } = driveAndRecord(REPLAY_SCRIPT);
  const broken = (() => {
    let state = { ...initialState(), funds: initialState().funds + 1 };
    for (const e of journal.entries) state = reducer(state, e.action);
    return state;
  })();
  assert.notDeepEqual(broken, liveState, 'RED: a mutated genesis must diverge');
  assert.deepEqual(replayFromGenesis(journal), liveState, 'GREEN: the real replayer matches');
});
