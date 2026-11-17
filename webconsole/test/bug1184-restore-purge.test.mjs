// BUG-1184 r2: the fallback restoreFromSavepoint must enforce maxPerCity through the SAME purge as the
// 'hydrate' reducer case (one enforcement: 50% scrap credit, ledger entry, notice after the tail replay).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { persistSavepoint, createSavepoint, restoreFromSavepoint, prepareRestoreForChunkedTail } from '../src/sim/replay.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

const CAPPED = 'tour_greatwall';
function memStorage() { const m = new Map(); return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => void m.set(k, String(v)), removeItem: (k) => void m.delete(k) }; }
const wall = (id, builtTick, x) => ({ id, spec: CAPPED, x, y: 20, builtTick });
function overCap(extra = {}) { const base = initialState(); return { ...base, tick: 5000, buildings: [...base.buildings, wall(700100, 100, 10), wall(700900, 300, 14)], nextId: 2_000_000, ...extra }; }
function store(state, tail = []) { const s = memStorage(); const when = new Date(Date.UTC(2026, 10, 1, 12, 0)); assert.equal(persistSavepoint(s, createSavepoint(state, tail, when, 'v0.test', null), when), true); return s; }
const walls = (st) => st.buildings.filter((b) => b.spec === CAPPED).map((b) => b.id);

function findPlace(st) {
  for (const spec of Object.keys(SPECS)) {
    if (SPECS[spec].maxPerCity != null || SPECS[spec].placeholder) continue;
    for (let x = 30; x < 60; x += 3) {
      const action = { type: 'place', spec, x, y: 40 };
      if (reducer(st, action).buildings.length > st.buildings.length) return action;
    }
  }
  assert.fail('no placeable spec found');
}

test('A1: scrap credit is paid exactly once on BOTH the chunked (hydrate) path and the fallback path, and they agree', () => {
  const S = overCap();
  const live = reducer(initialState(), { type: 'hydrate', state: S });
  const liveCredit = live.funds - S.funds;
  assert.ok(liveCredit > 0, 'live hydrate purge credits scrap');
  assert.deepEqual(walls(live), [700100]);

  const p = prepareRestoreForChunkedTail(store(S));
  assert.equal(p.success, true, p.reason);
  assert.equal(p.state.funds, S.funds, 'prepare itself must not drop or refund');
  const chunked = reducer(initialState(), { type: 'hydrate', state: p.state });
  assert.equal(chunked.funds - S.funds, liveCredit);

  const r = restoreFromSavepoint(store(S));
  assert.equal(r.success, true, r.reason);
  assert.deepEqual(walls(r.state), [700100], 'oldest kept');
  assert.equal(r.state.funds - S.funds, liveCredit, 'fallback pays the same credit as hydrate');
  assert.equal(r.state.fundsAtTickEnd - S.fundsAtTickEnd, liveCredit);
  assert.ok(r.state.lastFlows.inflows.some((f) => /Surplus .* decommission scrap/.test(f.label) && f.value === liveCredit), 'inflow entry');
  assert.ok(r.state.ledger.length > S.ledger.length, 'ledger entry');
  assert.deepEqual(r.state.placeNotice, live.placeNotice, 'identical notice to hydrate');

  // Idempotent: a later hydrate of the already-purged fallback state pays nothing more.
  const again = reducer(initialState(), { type: 'hydrate', state: r.state });
  assert.equal(again.funds, r.state.funds, 'credit paid exactly once');
  assert.deepEqual(walls(again), [700100]);
});

test('A3: the purge notice survives a tail place action on the fallback path (set after replay)', () => {
  const S = { ...overCap(), unlockedAll: true };
  const act = findPlace(S);
  const tail = [{ tick: S.tick, action: act }];
  const r = restoreFromSavepoint(store(S, tail));
  assert.equal(r.success, true, r.reason);
  assert.equal(r.replayed, 1);
  assert.equal(r.state.buildings.some((b) => b.spec === act.spec && b.x === act.x), true, 'tail place applied');
  assert.match(r.state.placeNotice ?? '', /Removed 1 surplus .* scrap credited/);

  const p = prepareRestoreForChunkedTail(store(S, tail));
  assert.equal(p.success, true, p.reason);
  let st = p.state;
  for (const e of p.tail) st = reducer(st, e.action);
  const hyd = reducer(initialState(), { type: 'hydrate', state: st });
  assert.match(hyd.placeNotice ?? '', /Removed 1 surplus .* scrap credited/);
  assert.equal(hyd.funds - st.funds, r.state.funds - (S.funds - 0 + (reducer(S, act).funds - S.funds)), 'same credit after the same tail');
});

test('fallback path: a legal save is untouched (same reference semantics, no credit, no notice)', () => {
  const base = initialState();
  const S = { ...base, tick: 5000, buildings: [...base.buildings, wall(700100, 100, 10)], nextId: 2_000_000 };
  const r = restoreFromSavepoint(store(S));
  assert.equal(r.success, true, r.reason);
  assert.equal(r.state.funds, S.funds);
  assert.equal(r.state.placeNotice ?? null, S.placeNotice ?? null);
  assert.deepEqual(walls(r.state), [700100]);
});
