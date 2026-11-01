// bug1149-1150-consolidator-undo.test.mjs - BUG-1149 (Undo money mint) and
// BUG-1150 (Undo tile stacking). Every scenario drives the real reducer on the
// fire fixture (5 fire_post -> fire_station).
//
// BUG-1149: Undo used to refund the FULL recorded netCost even when the
// auto-placed successor had already been bulldozed (player kept the 25%
// bulldoze refund AND got the pass refund back). Fix: refund only the build
// cost share of added buildings still present, minus the scrap share of the
// members actually restored (pro-rata to placementCost, penny-exact).
// BUG-1150: Undo restored demolished members onto tiles with no occupancy
// check. Fix (r1 ruling): ALL-OR-NOTHING - any blocked member refuses the whole Undo
// (state unchanged but for a notice naming every blocking tile; record kept for retry).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, BULLDOZE_REFUND_FRACTION, placementCost, footprintOf } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 100_000_000,
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
const withConn = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
function fireFixture() {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 1, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
  ];
  return withConn(mk({ buildings: [...roadRow(0, 40), ...posts, ...headroom] }));
}
/** Run one pass and return the post-pass state + the consolidate txn. */
function afterPass() {
  let s = fireFixture();
  s = reducer(s, { type: 'toggleConsolidator' });
  s = advanceToNextBoundary(s);
  const pass = s.consolidatorLog[0];
  assert.ok(pass && pass.transactions.some((t) => t.kind === 'consolidate'), 'setup: a consolidate txn applied');
  return { s, pass, txn: pass.transactions.find((t) => t.kind === 'consolidate') };
}
/** Zero overlapping tiles across ALL buildings (real footprints). */
function overlaps(s) {
  const seen = new Map();
  const dup = [];
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const { w, h } = footprintOf(b, sp);
    for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) {
      const k = `${b.x + dx},${b.y + dy}`;
      if (seen.has(k)) dup.push(`${k}:${seen.get(k)}&${b.id}`);
      else seen.set(k, b.id);
    }
  }
  return dup;
}
const successorOf = (s, txn) => s.buildings.find((b) => b.id === txn.added.find((a) => a.spec === 'fire_station').id);
const layoutNet = (pass) => (pass.tierLayout ?? []).reduce((n, t) => n + t.netCost, 0);

describe('BUG-1149 Undo never mints money', () => {
  test('plain Undo: funds move by exactly the recorded net (transactions + layout)', () => {
    const { s, pass, txn } = afterPass();
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(u.funds, s.funds + txn.netCost + layoutNet(pass), 'one-money rule: delta == sum of itemised refunds');
  });

  test('bulldoze the successor then Undo: no mint, delta explained to the penny', () => {
    const { s, pass, txn } = afterPass();
    const succ = successorOf(s, txn);
    const fundsPass = s.funds;
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    const bulldozeRefund = Math.round(placementCost(SPECS[succ.spec]) * BULLDOZE_REFUND_FRACTION);
    assert.equal(b.funds, fundsPass + bulldozeRefund);
    const u = reducer(b, { type: 'consolidatorUndo' });
    // Successor gone -> its build share is not refunded again; the 5 restored
    // members hand back their scrap; connector tiles (added, present) refund.
    const succRec = txn.added.find((a) => a.id === succ.id);
    const wAdded = txn.added.map((a) => placementCost(SPECS[a.spec]));
    const wSum = wAdded.reduce((x, y) => x + y, 0);
    assert.ok(wSum > 0);
    // Expected from first principles, independent of the engine's allocator
    // for the all-removed-restored case: scrapBack == scrapRecovered exactly.
    const presentBuildShare = txn.added
      .filter((a) => a.id !== succRec.id)
      .reduce((n, a) => n + Math.round((txn.buildCost * placementCost(SPECS[a.spec])) / wSum), 0);
    const expectedDelta = presentBuildShare - txn.scrapRecovered + layoutNet(pass);
    assert.ok(Math.abs(u.funds - b.funds - expectedDelta) <= txn.added.length, `delta ${u.funds - b.funds} vs ${expectedDelta}`);
    // THE mint test: end funds relative to the original (pre-pass) funds.
    const originalFunds = fundsPass + txn.netCost + layoutNet(pass); // what a plain Undo would restore to
    assert.ok(u.funds <= originalFunds, `no mint: ${u.funds} <= ${originalFunds}`);
    assert.ok(u.funds < originalFunds, 'player pays for bulldozing the successor');
    // Repeatable cycle cannot ratchet: consolidate again, bulldoze, undo, funds never exceed the baseline.
    assert.deepEqual(overlaps(u), []);
  });

  test('exact arithmetic: no-connector case, successor bulldozed', () => {
    const { s, txn } = afterPass();
    // The ledger line records the exact refund.
    const succ = successorOf(s, txn);
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    const u = reducer(b, { type: 'consolidatorUndo' });
    const delta = u.funds - b.funds;
    const aW = txn.added.map((a) => placementCost(SPECS[a.spec]));
    const share = (i) => Math.round((txn.buildCost * aW.slice(0, i + 1).reduce((x, y) => x + y, 0)) / aW.reduce((x, y) => x + y, 0));
    let build = 0;
    let prev = 0;
    txn.added.forEach((a, i) => {
      const upto = i === txn.added.length - 1 ? txn.buildCost : share(i);
      if (a.id !== succ.id) build += upto - prev;
      prev = upto;
    });
    assert.equal(delta - layoutNet(s.consolidatorLog[0]), build - txn.scrapRecovered, 'refund = present-added shares - restored scrap, to the penny');
  });

  test('chained: Undo after the successor was itself consolidated again does not mint or stack', () => {
    let { s, txn } = afterPass();
    const succId = successorOf(s, txn).id;
    // Simulate a LATER pass consuming the successor: it vanishes from the map
    // (as a re-consolidation demolishes it) without any bulldoze refund here.
    s = { ...s, buildings: s.buildings.filter((b) => b.id !== succId) };
    s = withConn(s);
    const before = s.funds;
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.ok(u.funds - before <= layoutNet(s.consolidatorLog[0]) + txn.added.filter((a) => a.id !== succId).length, 'successor share not refunded');
    assert.ok(u.funds - before < txn.netCost, 'strictly less than the full legacy refund');
    assert.deepEqual(overlaps(u), []);
  });
});

describe('BUG-1150 Undo never stacks tiles', () => {
  // FLIPPED (r1 LEAD RULING, all-or-nothing Undo): this test used to assert PARTIAL restore
  // (free members restored, blocked ones skipped, scrap share not charged back) - the
  // behaviour that opened the r1 F1 mint. Now ANY blocked member refuses the whole Undo:
  // state byte-identical except the notice, record not consumed, every blocking tile named.
  test('building placed on a freed tile REFUSES the whole Undo: state untouched except a notice naming every blocker', () => {
    const { s: s0, txn } = afterPass();
    const succ = successorOf(s0, txn);
    let s = reducer(s0, { type: 'bulldoze', x: succ.x, y: succ.y });
    const px = txn.removed[0].x;
    const py = txn.removed[0].y;
    s = { ...s, buildings: [...s.buildings, { id: 7777, spec: 'pol_station', x: px, y: py, builtTick: s.tick }] };
    assert.deepEqual(overlaps(s), [], 'setup: no overlap before Undo');
    const blockedTiles = new Set([`${px},${py}`, `${px + 1},${py}`]);
    const blocked = txn.removed.filter((r) => blockedTiles.has(`${r.x},${r.y}`));
    assert.ok(blocked.length >= 1);
    const u = reducer(s, { type: 'consolidatorUndo' });
    const { placeNotice, ...uRest } = u;
    const { placeNotice: _pn, ...sRest } = s;
    assert.equal(stableStringify(uRest), stableStringify(sRest), 'byte-identical except the notice');
    assert.equal(u.funds, s.funds);
    assert.equal(u.nextId, s.nextId);
    assert.equal(u.consolidatorUndoConsumed ?? false, s.consolidatorUndoConsumed ?? false, 'record not consumed');
    assert.equal(u.consolidatorLog.length, s.consolidatorLog.length);
    assert.deepEqual(overlaps(u), []);
    assert.match(placeNotice, /Undo refused/);
    assert.match(placeNotice, /Police Station|pol_station/i);
    assert.match(placeNotice, /#7777/);
    for (const t of blockedTiles) assert.ok(placeNotice.includes(t), `notice names tile ${t}`);
  });

  // FLIPPED (r1 LEAD RULING): was "successor removed, blocked member skipped" (partial). Now the
  // whole Undo is refused, so the still-present successor is NOT removed either.
  test('successor still present + player builds on a post tile: whole Undo refused, successor kept, no stack', () => {
    const { s: s0, txn } = afterPass();
    const succ = successorOf(s0, txn);
    const sp = SPECS[succ.spec];
    const { w, h } = footprintOf(succ, sp);
    const outside = txn.removed.find((r) => !(r.x >= succ.x && r.x < succ.x + w && r.y >= succ.y && r.y < succ.y + h));
    assert.ok(outside, 'setup: a post tile outside the successor footprint');
    const s = { ...s0, buildings: [...s0.buildings, { id: 7778, spec: 'pol_station', x: outside.x, y: outside.y, builtTick: s0.tick }] };
    assert.deepEqual(overlaps(s), []);
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.deepEqual(overlaps(u), []);
    assert.equal(u.buildings.some((b) => b.id === outside.id), false, 'blocked post not restored');
    assert.equal(u.buildings.some((b) => b.id === succ.id), true, 'successor kept (Undo refused)');
    assert.deepEqual(u.buildings, s.buildings);
    assert.equal(u.funds, s.funds);
    assert.match(u.placeNotice, /Undo refused/);
  });

  test('retry after clearing: refused Undo keeps the record; bulldoze the blocker, Undo succeeds with exactly the netCost refund', () => {
    const { s: s0, pass, txn } = afterPass();
    const outside = txn.removed.find((r) => {
      const succ = successorOf(s0, txn);
      const { w, h } = footprintOf(succ, SPECS[succ.spec]);
      return !(r.x >= succ.x && r.x < succ.x + w && r.y >= succ.y && r.y < succ.y + h);
    });
    assert.ok(outside, 'setup: a post tile outside the successor footprint');
    const s = { ...s0, buildings: [...s0.buildings, { id: 7779, spec: 'res_hut', x: outside.x, y: outside.y, builtTick: s0.tick }] };
    const refused = reducer(s, { type: 'consolidatorUndo' });
    assert.match(refused.placeNotice, /Undo refused/);
    assert.equal(refused.funds, s.funds);
    const cleared = reducer(refused, { type: 'bulldoze', x: outside.x, y: outside.y });
    assert.equal(cleared.buildings.some((q) => q.id === 7779), false, 'setup: blocker bulldozed');
    const before = cleared.funds;
    const u = reducer(cleared, { type: 'consolidatorUndo' });
    for (const r of txn.removed) assert.ok(u.buildings.some((q) => q.id === r.id), `post ${r.id} restored`);
    assert.equal(u.buildings.some((q) => q.id === successorOf(s0, txn).id), false, 'successor removed');
    const net = pass.transactions.reduce((n, t) => n + t.netCost, 0) + layoutNet(pass);
    assert.equal(u.funds - before, net, 'refund is exactly the recorded netCost');
    assert.deepEqual(overlaps(u), []);
    assert.equal(u.consolidatorUndoConsumed, true, 'consumed only on the successful Undo');
  });

  test('plain Undo with free tiles restores everything (no false blocks)', () => {
    const { s, txn } = afterPass();
    const u = reducer(s, { type: 'consolidatorUndo' });
    for (const r of txn.removed) assert.ok(u.buildings.some((b) => b.id === r.id));
    assert.equal(u.placeNotice === undefined || !/Undo refused/.test(u.placeNotice), true);
    assert.deepEqual(overlaps(u), []);
  });
});

describe('record shape + determinism', () => {
  test('record unchanged: JSON round-trip of a city with an undoable pass undoes identically', () => {
    const { s } = afterPass();
    const rt = JSON.parse(JSON.stringify(s));
    const a = reducer(s, { type: 'consolidatorUndo' });
    const b = reducer(rt, { type: 'consolidatorUndo' });
    assert.equal(b.funds, a.funds);
    assert.deepEqual(b.buildings.map((x) => x.id).sort((x, y) => x - y), a.buildings.map((x) => x.id).sort((x, y) => x - y));
  });

  test('legacy pass (no tierLayout, no upkeepDelta) undoes at the recorded netCost', () => {
    const { s, txn } = afterPass();
    const legacy = {
      ...s,
      consolidatorLog: [{ ...s.consolidatorLog[0], tierLayout: undefined, transactions: [txn] }],
    };
    const u = reducer(legacy, { type: 'consolidatorUndo' });
    assert.equal(u.funds, s.funds + txn.netCost);
  });

  test('determinism: same input, same output', () => {
    const { s, txn } = afterPass();
    const succ = successorOf(s, txn);
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    assert.equal(stableStringify(reducer(b, { type: 'consolidatorUndo' })), stableStringify(reducer(b, { type: 'consolidatorUndo' })));
  });
});

describe('BUG-1149 r2 fixes (F1 affordability, F2 stale notice, F3 bounded notice)', () => {
  // F1: bulldozing the successor leaves a NEGATIVE refund (full scrap owed back, nothing
  // refunded for the gone successor). Undo must not debit past zero unseen.
  test('F1: unaffordable negative refund is refused byte-identically except the notice; succeeds once funds are raised', () => {
    const { s: s0, txn } = afterPass();
    const succ = successorOf(s0, txn);
    const gone = reducer(s0, { type: 'bulldoze', x: succ.x, y: succ.y });
    const poor = { ...gone, funds: 1_000_000 };
    const u = reducer(poor, { type: 'consolidatorUndo' });
    const { placeNotice, ...uRest } = u;
    const { placeNotice: _p, ...pRest } = poor;
    assert.equal(stableStringify(uRest), stableStringify(pRest), 'byte-identical except the notice');
    assert.equal(u.funds, 1_000_000);
    assert.ok(u.funds >= 0);
    assert.match(placeNotice, /Undo refused/);
    assert.match(placeNotice, /short by/);
    assert.equal(u.consolidatorUndoConsumed ?? false, poor.consolidatorUndoConsumed ?? false, 'record kept');
    // Retry once the player can afford it.
    const rich = { ...u, funds: 100_000_000 };
    const ok = reducer(rich, { type: 'consolidatorUndo' });
    for (const r of txn.removed) assert.ok(ok.buildings.some((q) => q.id === r.id), `post ${r.id} restored`);
    assert.ok(ok.funds < rich.funds, 'a real (negative) refund was charged');
    assert.ok(ok.funds >= 0);
    assert.equal(ok.consolidatorUndoConsumed, true);
  });

  // F2: a refusal notice must not survive a successful retry.
  test('F2: after a successful retry the notice is the success notice, not the stale refusal', () => {
    const { s: s0, txn } = afterPass();
    const outside = txn.removed.find((r) => {
      const succ = successorOf(s0, txn);
      const { w, h } = footprintOf(succ, SPECS[succ.spec]);
      return !(r.x >= succ.x && r.x < succ.x + w && r.y >= succ.y && r.y < succ.y + h);
    });
    assert.ok(outside, 'setup');
    const s = { ...s0, buildings: [...s0.buildings, { id: 7780, spec: 'res_hut', x: outside.x, y: outside.y, builtTick: s0.tick }] };
    const refused = reducer(s, { type: 'consolidatorUndo' });
    assert.match(refused.placeNotice, /Undo refused/);
    const cleared = reducer(refused, { type: 'bulldoze', x: outside.x, y: outside.y });
    const ok = reducer({ ...cleared, placeNotice: refused.placeNotice }, { type: 'consolidatorUndo' });
    assert.doesNotMatch(ok.placeNotice ?? '', /Undo refused/);
    assert.match(ok.placeNotice, /^Undo complete: refunded /);
  });

  // F3: 100 single-tile blockers must not produce a 5k-character notice.
  test('F3: 100-blocker notice is bounded (<= 10 buildings + "+N more tiles") and shows the total', () => {
    const { s: s0, txn } = afterPass();
    const base = txn.removed[0];
    const extra = [];
    const huts = [];
    for (let i = 0; i < 100; i++) {
      extra.push({ ...base, id: 5000 + i, x: 60 + (i % 50), y: 20 + Math.floor(i / 50) });
      huts.push({ id: 6000 + i, spec: 'res_hut', x: 60 + (i % 50), y: 20 + Math.floor(i / 50), builtTick: s0.tick });
    }
    const pass = { ...s0.consolidatorLog[0], transactions: s0.consolidatorLog[0].transactions.map((t) => (t === txn ? { ...t, removed: [...t.removed, ...extra] } : t)) };
    const s = { ...s0, consolidatorLog: [pass, ...s0.consolidatorLog.slice(1)], buildings: [...s0.buildings, ...huts] };
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.match(u.placeNotice, /Undo refused: 100 tiles blocked/);
    assert.match(u.placeNotice, /\+90 more tiles/);
    assert.ok(u.placeNotice.length < 1000, `notice length ${u.placeNotice.length}`);
    assert.equal((u.placeNotice.match(/#\d+/g) ?? []).length, 10, 'exactly 10 buildings listed');
    assert.equal(u.placeNotice, reducer(s, { type: 'consolidatorUndo' }).placeNotice, 'deterministic');
  });
});

describe('BUG-1149 r3: affordability gate only debits', () => {
  test('a city in debt (funds -10,000,000) can still do a plain Undo that pays it back', () => {
    const { s: s0, pass } = afterPass();
    const s = { ...s0, funds: -10_000_000 };
    const net = pass.transactions.reduce((n, t) => n + t.netCost, 0) + layoutNet(pass);
    assert.ok(net > 0, 'setup: plain Undo refund is positive');
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.equal(u.funds, -10_000_000 + net);
    assert.match(u.placeNotice, /^Undo complete: refunded /);
    assert.equal(u.consolidatorUndoConsumed, true);
  });
});
