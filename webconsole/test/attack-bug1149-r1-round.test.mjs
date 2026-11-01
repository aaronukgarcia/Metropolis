// attack-bug1149-r1-round.test.mjs - independent destructive round r1 on the
// BUG-1149 / BUG-1150 consolidator Undo fix (opus-round-r1-bug1149).
// Every scenario drives the REAL reducer (place / bulldoze / consolidatorUndo /
// tick) on the fire fixture (5 fire_post -> fire_station). Money is judged
// against a CONTROL: the same post-pass state, plain Undo, then the best
// legitimate liquidation the player could do by hand (bulldoze at 25%).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, BULLDOZE_REFUND_FRACTION, placementCost, footprintOf } from '../src/sim/data.ts';
import { initialState, reducer, TICKS_PER_MONTH, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';

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
  do cur = reducer(cur, { type: 'tick' });
  while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
function fixture(extra = []) {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 1, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
  ];
  return withConn(mk({ buildings: [...roadRow(0, 40), ...posts, ...headroom, ...extra] }));
}
function afterPass(extra) {
  let s = fixture(extra);
  s = reducer(s, { type: 'toggleConsolidator' });
  s = advanceToNextBoundary(s);
  const pass = s.consolidatorLog[0];
  const txn = pass?.transactions.find((t) => t.kind === 'consolidate');
  assert.ok(txn, 'setup: a consolidate txn applied');
  return { s, pass, txn };
}
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
const bulldozeValue = (spec) => Math.round(placementCost(SPECS[spec]) * BULLDOZE_REFUND_FRACTION);
const succOf = (s, txn) => s.buildings.find((b) => b.id === txn.added.find((a) => a.spec === 'fire_station').id);
function tilesOf(b) {
  const { w, h } = footprintOf(b, SPECS[b.spec]);
  const out = [];
  for (let dx = 0; dx < w; dx++) for (let dy = 0; dy < h; dy++) out.push(`${b.x + dx},${b.y + dy}`);
  return out;
}

describe('R1 money: original exploits vs the PRE-PASS (plain-Undo) baseline', () => {
  test('A1 bulldoze successor then Undo never ends above plain-Undo funds', () => {
    const { s, txn } = afterPass();
    const plain = reducer(s, { type: 'consolidatorUndo' });
    const succ = succOf(s, txn);
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    const u = reducer(b, { type: 'consolidatorUndo' });
    assert.ok(u.funds <= plain.funds, `exploit: ${u.funds} <= plain ${plain.funds}`);
    // And the player holds the SAME map as plain Undo (all posts back, no station).
    assert.deepEqual(u.buildings.map((x) => x.id).sort(), plain.buildings.map((x) => x.id).sort());
    assert.deepEqual(overlaps(u), []);
  });

  test('A2 BUG-1150 via the REAL place action: free zone on a freed tile, Undo, zero overlaps', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const succTiles = new Set(tilesOf(succ));
    const free = txn.removed.filter((r) => !succTiles.has(`${r.x},${r.y}`));
    assert.ok(free.length > 0, 'setup: some freed tiles outside the successor');
    let p = s;
    for (const r of free) p = reducer(p, { type: 'place', spec: 'res_hut', x: r.x, y: r.y });
    assert.equal(p.buildings.length, s.buildings.length + free.length, 'setup: the real place action accepted every freed tile');
    const u = reducer(p, { type: 'consolidatorUndo' });
    assert.deepEqual(overlaps(u), []);
  });
});

describe('R1 money: selective removal of any ONE added building (attack 2)', () => {
  // Pro-rata attribution as a mint surface: removing added building `a` pays
  // its 25% bulldoze refund and forfeits its pro-rata build share. A mint needs
  // share(a) < bulldozeRefund(a). Walk EVERY added record of the real pass
  // (consolidate successor + every inc3 layout tile - mixed specs, unequal shares).
  test('A2b for every added building: bulldoze it, Undo -> funds <= plain Undo', () => {
    const { s, pass } = afterPass();
    const plain = reducer(s, { type: 'consolidatorUndo' });
    const all = [...pass.transactions, ...(pass.tierLayout ?? [])].flatMap((t) => t.added);
    assert.ok(all.length >= 2, `setup: mixed added set (${all.map((a) => a.spec).join(',')})`);
    let tried = 0;
    for (const a of all) {
      const live = s.buildings.find((b) => b.id === a.id);
      if (!live) continue;
      const b = reducer(s, { type: 'bulldoze', x: live.x, y: live.y });
      if (b.buildings.some((q) => q.id === a.id)) continue; // bulldoze refused (e.g. protected)
      tried += 1;
      const u = reducer(b, { type: 'consolidatorUndo' });
      assert.ok(u.funds <= plain.funds, `${a.spec}#${a.id}: ${u.funds} <= ${plain.funds} (diff ${u.funds - plain.funds})`);
      assert.deepEqual(overlaps(u), []);
    }
    assert.ok(tried >= 2, `exercised ${tried}`);
  });
});

describe('R1 layout upkeep budget (ROUND-14 F3 class)', () => {
  // bulldoze never decrements consolidatorLayoutCumulativeUpkeepDelta, so a
  // layout tile the player bulldozed before Undo has had its upkeep reversed
  // ZERO times - scaling the reversal by the surviving share leaves that upkeep
  // charged against the LIFETIME layout ceiling forever.
  test('A6 bulldoze one layout tile then Undo: the lifetime upkeep counter returns to its plain-Undo value', () => {
    const { s: s0, pass: pass0 } = afterPass();
    // On this fixture the pass's lifetime counter is real (16 rd_dual x 5 = 80)
    // but the txn's own upkeepDelta field reads 0; stamp the txn with the
    // counter's real contribution (the shape the field documents) so the
    // reversal path is exercised.
    const tl = pass0.tierLayout ?? [];
    const idx = tl.findIndex((t) => t.added.length > 0);
    assert.ok(idx >= 0, 'setup: a layout txn that added tiles');
    const realUp = tl[idx].added.reduce((n, a) => n + SPECS[a.spec].upkeep, 0);
    const pass = { ...pass0, tierLayout: tl.map((t, i) => (i === idx ? { ...t, upkeepDelta: realUp } : t)) };
    const s = { ...s0, consolidatorLog: [pass, ...s0.consolidatorLog.slice(1)] };
    const layout = [pass.tierLayout[idx]];
    const plain = reducer(s, { type: 'consolidatorUndo' });
    const a = layout[0].added[0];
    const live = s.buildings.find((b) => b.id === a.id);
    const b = reducer(s, { type: 'bulldoze', x: live.x, y: live.y });
    assert.equal(b.buildings.some((q) => q.id === a.id), false, 'setup: tile bulldozed');
    assert.equal(b.consolidatorLayoutCumulativeUpkeepDelta, s.consolidatorLayoutCumulativeUpkeepDelta, 'bulldoze leaves the lifetime counter alone');
    const u = reducer(b, { type: 'consolidatorUndo' });
    assert.equal(u.consolidatorLayoutCumulativeUpkeepDelta, plain.consolidatorLayoutCumulativeUpkeepDelta,
      `residual lifetime upkeep after Undo: ${u.consolidatorLayoutCumulativeUpkeepDelta} vs ${plain.consolidatorLayoutCumulativeUpkeepDelta}`);
  });
});

describe('R1 money: blocked-member scrap retention (attack 3)', () => {
  // The attack: after a pass, drop a FREE zone (res_hut, placementCost 0) on
  // every freed member tile the successor does not cover, then Undo. The
  // blocked members' scrap (50% of their cost) is kept, the successor's full
  // build cost is refunded. Compared with a plain Undo followed by hand-
  // bulldozing the same posts (25%), the player is up 25% of every blocked
  // member's cost - Undo has become a 2x "sell" button.
  test('A3 block every outside member with a free zone, Undo: end funds <= plain Undo + hand-bulldoze of the same members', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const succTiles = new Set(tilesOf(succ));
    const blockable = txn.removed.filter((r) => !succTiles.has(`${r.x},${r.y}`));
    assert.ok(blockable.length >= 1);
    // CONTROL: plain Undo, then the player bulldozes the same posts by hand.
    let ctrl = reducer(s, { type: 'consolidatorUndo' });
    for (const r of blockable) ctrl = reducer(ctrl, { type: 'bulldoze', x: r.x, y: r.y });
    const ctrlExpected = reducer(s, { type: 'consolidatorUndo' }).funds + blockable.reduce((n, r) => n + bulldozeValue(r.spec), 0);
    assert.equal(ctrl.funds, ctrlExpected, 'control arithmetic');
    // ATTACK
    let a = s;
    for (const r of blockable) a = reducer(a, { type: 'place', spec: 'res_hut', x: r.x, y: r.y });
    a = reducer(a, { type: 'consolidatorUndo' });
    for (const r of blockable) a = reducer(a, { type: 'bulldoze', x: r.x, y: r.y }); // clear the free huts too (refund 0)
    // FLIPPED (r1 LEAD RULING, all-or-nothing Undo): the old "same end map" assertion
    // encoded PARTIAL restore (rest of the posts restored). Under the ruling the Undo is
    // REFUSED, so no post is restored and the successor stays; assert that strictly.
    for (const r of txn.removed) assert.equal(a.buildings.some((x) => x.id === r.id), false, `post ${r.id} not restored (Undo refused)`);
    assert.ok(a.buildings.some((x) => x.id === succ.id), 'successor kept (Undo refused)');
    assert.ok(a.funds <= ctrl.funds, `mint: attack ${a.funds} vs control ${ctrl.funds} (diff ${a.funds - ctrl.funds})`);
  });

  test('A3b against the pre-pass state: no free-zone blocking pattern ends with more cash than plain Undo AND a map worth no less', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const succTiles = new Set(tilesOf(succ));
    const blockable = txn.removed.filter((r) => !succTiles.has(`${r.x},${r.y}`));
    const plain = reducer(s, { type: 'consolidatorUndo' });
    let a = s;
    for (const r of blockable) a = reducer(a, { type: 'place', spec: 'res_hut', x: r.x, y: r.y });
    a = reducer(a, { type: 'consolidatorUndo' });
    // Liquidation value the attacker gave up = what bulldozing the lost posts would have paid.
    const gaveUp = blockable.reduce((n, r) => n + bulldozeValue(r.spec), 0);
    assert.ok(a.funds - plain.funds <= gaveUp, `cash gained ${a.funds - plain.funds} vs liquidation value given up ${gaveUp}`);
  });
});

describe('R1 interleavings (attack 4)', () => {
  test('A4a Undo twice: second press is an identity no-op', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    const u1 = reducer(b, { type: 'consolidatorUndo' });
    const u2 = reducer(u1, { type: 'consolidatorUndo' });
    assert.equal(u2, u1);
  });

  test('A4b save/load mid-stream (bulldoze, JSON round-trip, Undo) equals no round-trip', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const b = reducer(s, { type: 'bulldoze', x: succ.x, y: succ.y });
    const rt = JSON.parse(JSON.stringify(b));
    const x = reducer(b, { type: 'consolidatorUndo' });
    const y = reducer(rt, { type: 'consolidatorUndo' });
    assert.equal(y.funds, x.funds);
    assert.deepEqual(y.buildings.map((q) => q.id).sort(), x.buildings.map((q) => q.id).sort());
  });

  test('A4c successor relocated, then Undo: no mint vs plain Undo, zero overlaps', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const plain = reducer(s, { type: 'consolidatorUndo' });
    const r = reducer(reducer(s, { type: 'pickup', id: succ.id }), { type: 'relocate', x: 30, y: 1 });
    const moved = r.buildings.find((b) => b.id === succ.id);
    assert.ok(moved && moved.x === 30, 'setup: the successor really moved');
    const u = reducer(r, { type: 'consolidatorUndo' });
    assert.equal(u.buildings.some((b) => b.id === succ.id), false, 'moved successor still removed by Undo');
    for (const rr of txn.removed) assert.ok(u.buildings.some((b) => b.id === rr.id), `post ${rr.id} restored`);
    assert.ok(u.funds <= plain.funds, `${u.funds} <= ${plain.funds}`);
    assert.deepEqual(overlaps(u), []);
  });

  test('A4d REAL second pass consolidates the successor: Undo reverses only pass 2, no mint, no stack', () => {
    // 5 stations exist after pass 1 (successor + 4 seeded on the road row) -> pass 2 may merge them.
    // fire_station -> fire_hq groups of 4: the successor + 3 seeded stations; fire_hq headroom far away.
    const extra = [{ id: 950, spec: 'fire_hq', x: 220, y: 220, builtTick: -1000 }, { id: 951, spec: 'fire_hq', x: 230, y: 220, builtTick: -1000 }];
    for (let i = 0; i < 3; i++) extra.push({ id: 300 + i, spec: 'fire_station', x: 24 + i * 2, y: 1, builtTick: -1000 });
    let { s } = afterPass(extra);
    const pass1Len = s.consolidatorLog.length;
    let s2 = s;
    for (let m = 0; m < 13 && s2.consolidatorLog.filter((p) => p.transactions.length > 0).length < 2; m++) s2 = advanceToNextBoundary(s2);
    const real = s2.consolidatorLog.filter((p) => p.transactions.length > 0);
    const succ1 = s.consolidatorLog[0].transactions.find((t) => t.kind === 'consolidate').added.find((a) => a.spec === 'fire_station').id;
    assert.ok(real.length >= 2, `a second REAL pass must run (log ${pass1Len} -> ${s2.consolidatorLog.length})`);
    const p2removed = real[0].transactions.flatMap((t) => t.removed.map((r) => r.id));
    assert.ok(p2removed.includes(succ1), `pass 2 really consolidated pass 1's successor ${succ1} (removed ${p2removed})`);
    const before = s2.funds;
    const p2 = s2.consolidatorLog.find((p) => p.transactions.length > 0);
    const expected = p2.transactions.reduce((n, t) => n + t.netCost, 0) + (p2.tierLayout ?? []).reduce((n, t) => n + t.netCost, 0);
    const u = reducer(s2, { type: 'consolidatorUndo' });
    assert.equal(u.funds - before, expected, 'plain Undo of pass 2 refunds exactly its net');
    assert.deepEqual(overlaps(u), []);
    assert.equal(reducer(u, { type: 'consolidatorUndo' }), u, 'pass 1 not reachable');
  });
});

describe('R1 map invariants (attack 5)', () => {
  // NOTE: ConsolidationRecord carries no footprintW/H, so a member is always
  // restored at its spec size - the grown-aware half of the check is the
  // OCCUPANCY side (a non-member neighbour that has grown onto a freed tile).
  test('A5a grown NON-member neighbour covering freed tiles blocks exactly those members', () => {
    const { s: s0, txn } = afterPass([{ id: 7001, spec: 'res_hut', x: 15, y: 1, builtTick: -1000 }]);
    const succ = succOf(s0, txn);
    let s = reducer(s0, { type: 'bulldoze', x: succ.x, y: succ.y });
    // simulate scale-out growth: the hut at 15,1 grows to 4 wide (15..18), over posts 100..102's old tiles.
    s = { ...s, buildings: s.buildings.map((b) => (b.id === 7001 ? { ...b, footprintW: 4 } : b)) };
    s = withConn(s);
    assert.deepEqual(overlaps(s), [], 'setup: no overlap before Undo');
    const u = reducer(s, { type: 'consolidatorUndo' });
    assert.deepEqual(overlaps(u), []);
    // FLIPPED (r1 LEAD RULING, all-or-nothing): was "100..102 blocked, 103/104 restored"
    // (partial restore). Now the whole Undo is refused: NONE of 100..104 is restored,
    // everything but the notice is untouched, and the notice names the blocked tiles.
    for (const id of [100, 101, 102, 103, 104]) assert.equal(u.buildings.some((b) => b.id === id), false, `post ${id} not restored (Undo refused)`);
    assert.deepEqual(u.buildings, s.buildings);
    assert.equal(u.funds, s.funds);
    assert.match(u.placeNotice, /Undo refused/);
  });

  test('A5c edge-of-map member restores in bounds (no out-of-map tiles introduced)', () => {
    const { s, txn } = afterPass();
    const u = reducer(s, { type: 'consolidatorUndo' });
    for (const r of txn.removed) {
      const b = u.buildings.find((q) => q.id === r.id);
      assert.ok(b && b.x >= 0 && b.y >= 0);
    }
  });

  test('A5d partial restore leaves every restored member road-adjacent where it was before', () => {
    const { s, txn } = afterPass();
    const succ = succOf(s, txn);
    const succTiles = new Set(tilesOf(succ));
    let p = s;
    const outside = txn.removed.filter((r) => !succTiles.has(`${r.x},${r.y}`));
    p = reducer(p, { type: 'place', spec: 'res_hut', x: outside[0].x, y: outside[0].y });
    const u = reducer(p, { type: 'consolidatorUndo' });
    assert.deepEqual(overlaps(u), []);
    const conn = computeRoadConnectivity(u);
    for (const r of txn.removed) {
      const b = u.buildings.find((q) => q.id === r.id);
      if (!b) continue;
      // fixture: every post sits on y=1 directly under the y=0 road row.
      assert.ok(u.buildings.some((q) => q.spec === 'road' && q.x === b.x && q.y === b.y - 1), `post ${b.id} still has its road`);
    }
    void conn;
  });

  test('A5b two removed records claiming one tile: only one restored', () => {
    const { s, txn } = afterPass();
    const pass = s.consolidatorLog[0];
    const a = txn.removed[0];
    const dupe = { ...txn.removed[1], x: a.x, y: a.y };
    const patched = {
      ...s,
      consolidatorLog: [{ ...pass, transactions: pass.transactions.map((t) => (t === txn ? { ...t, removed: t.removed.map((q, i) => (i === 1 ? dupe : q)) } : t)) }, ...s.consolidatorLog.slice(1)],
    };
    const u = reducer(patched, { type: 'consolidatorUndo' });
    assert.deepEqual(overlaps(u), []);
  });
});
