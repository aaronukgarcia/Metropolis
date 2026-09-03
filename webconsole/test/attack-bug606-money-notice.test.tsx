// attack-bug606-money-notice.test.tsx — PROMOTED from webconsole/attack/
// atk-money-notice.test.mts (independent round r2, Aaron 2026-09-03: "the
// attacker's regressions... promote into test/ ... so CI carries them").
// Extension changed .mts -> .tsx (not .mjs) because this file imports
// demandFixMessage from src/components/demandFixUi.ts, which itself has
// extensionless internal imports (`from '../sim/data'`) that plain `node
// --test` cannot resolve without the tsx loader — tools/test/scoped.mjs
// only routes .tsx/.ts through tsx, so .mts here would misroute into the
// plain node group and fail with ERR_MODULE_NOT_FOUND (same class of issue
// this session's own bug606-demand-fix-message.test.tsx hit and fixed the
// same way). Content below is otherwise UNCHANGED from the original attack
// file — these are the independent round's own regressions, kept verbatim.

// ATTACK — Fix All money truth, notice truth, message/plan agreement.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, specUnlocked } from '../src/sim/engine.ts';
import { demandFixPlan, orderedDemandFixPlan, SPECS, placementCost, canEnterSim } from '../src/sim/data.ts';
import { demandFixMessage } from '../src/components/demandFixUi.ts';

const ticks = (n: number) => Array.from({ length: n }, () => ({ type: 'tick' }) as never);

function grow(funds: number, extra: unknown[] = []) {
  let s = initialState();
  for (const a of [
    { type: 'debugFunds', amount: 5_000_000 },
    { type: 'unlockAll' },
    { type: 'place', spec: 'res_hut', x: 5, y: 5 },
    { type: 'place', spec: 'res_hut', x: 7, y: 5 },
    { type: 'place', spec: 'res_hut', x: 9, y: 5 },
    ...ticks(60),
    { type: 'debugFunds', amount: -5_000_000 },
    { type: 'debugFunds', amount: funds },
    ...extra,
  ] as never[]) s = reducer(s, a);
  return s;
}

function specCounts(s: { buildings: { spec: string }[] }) {
  const m = new Map<string, number>();
  for (const b of s.buildings) m.set(b.spec, (m.get(b.spec) ?? 0) + 1);
  return m;
}

test('ATTACK/PLANCOST: planCost must equal count * placementCost (what the player is ACTUALLY charged)', () => {
  const s = grow(1e12);
  const plan = demandFixPlan(s as never);
  assert.ok(plan.length > 0);
  const bad: string[] = [];
  for (const p of plan) {
    const sp = SPECS[p.specId];
    const real = p.count * placementCost(sp);
    if (p.planCost !== real) {
      bad.push(`${p.serviceKey}: planCost=${p.planCost} but count*placementCost=${real} (spec ${p.specId}, category ${sp.category})`);
    }
    if (p.alternative) {
      const asp = SPECS[p.alternative.specId];
      const areal = p.alternative.count * placementCost(asp);
      if (p.alternative.planCost !== areal) {
        bad.push(`${p.serviceKey} ALT: planCost=${p.alternative.planCost} but count*placementCost=${areal} (spec ${p.alternative.specId}, category ${asp.category})`);
      }
    }
  }
  assert.deepEqual(bad, [], `demandFixMessage would show a £ figure the player is never charged:\n${bad.join('\n')}`);
});

test('ATTACK/ALT-UNLOCK: alternative may never name a LOCKED or non-enterable spec', () => {
  const bad: string[] = [];
  for (const funds of [0, 50_000, 5_000_000, 1e12]) {
    for (const unlockAll of [false, true]) {
      const s: any = grow(funds, unlockAll ? [] : []);
      const st = { ...s, unlockedAll: unlockAll };
      for (const p of demandFixPlan(st)) {
        for (const id of [p.specId, p.alternative?.specId].filter(Boolean) as string[]) {
          const sp = SPECS[id];
          if (!sp || !canEnterSim(sp) || !specUnlocked(st, sp)) bad.push(`funds=${funds} unlockAll=${unlockAll} ${p.serviceKey} -> ${id}`);
        }
      }
    }
  }
  assert.deepEqual(bad, []);
});

test('ATTACK/ALT-DISTINCT: alternative is never the chosen spec, and message is agreement-by-construction', () => {
  const s = grow(250_000);
  for (const p of demandFixPlan(s as never)) {
    if (p.alternative) assert.notEqual(p.alternative.specId, p.specId);
    const msg = demandFixMessage(p);
    assert.ok(msg.includes(String(p.count)), `message must carry the executed count: ${msg}`);
    assert.ok(msg.includes(SPECS[p.specId].name), `message must name the executed spec: ${msg}`);
    if (p.alternative) assert.ok(msg.includes(SPECS[p.alternative.specId].name), msg);
  }
});

test('ATTACK/MONEY: Fix All never goes negative and spends EXACTLY sum(placed * placementCost)', () => {
  const bad: string[] = [];
  for (const funds of [0, 1, 999, 25_000, 250_000, 2_500_000, 40_000_000, 1e12]) {
    const s: any = grow(funds);
    const after: any = reducer(s, { type: 'resolveDemandAll' } as never);
    if (after.funds < 0) bad.push(`funds=${funds}: went NEGATIVE (${after.funds})`);
    const before = specCounts(s);
    const post = specCounts(after);
    let spent = 0;
    for (const [id, n] of post) {
      const delta = n - (before.get(id) ?? 0);
      if (delta > 0) spent += delta * placementCost(SPECS[id]);
    }
    const actual = s.funds - after.funds;
    if (actual !== spent) bad.push(`funds=${funds}: treasury moved ${actual} but placed buildings cost ${spent}`);
  }
  assert.deepEqual(bad, [], bad.join('\n'));
});

test('ATTACK/NOTICE-TRUTH: every "built N x Name" in the Fix All notice matches real building deltas', () => {
  const bad: string[] = [];
  for (const funds of [0, 25_000, 250_000, 2_500_000, 40_000_000, 1e12]) {
    const s: any = grow(funds);
    const after: any = reducer(s, { type: 'resolveDemandAll' } as never);
    const notice: string = after.placeNotice ?? '';
    if (!notice.startsWith('Fix All')) { bad.push(`funds=${funds}: no Fix All notice (${notice})`); continue; }
    const before = specCounts(s);
    const post = specCounts(after);
    const body = notice.replace(/^Fix All: (built )?/, '').split(' — ')[0];
    if (body.startsWith('nothing built')) {
      for (const [id, n] of post) {
        const d = n - (before.get(id) ?? 0);
        if (d > 0 && placementCost(SPECS[id]) > 0) bad.push(`funds=${funds}: notice says "nothing built" but placed ${d} x ${id}`);
      }
      continue;
    }
    for (const chunk of body.split(', ')) {
      const m = /^(\d+) x (.+)$/.exec(chunk) ?? /^(a|an|the)?\s*(.+)$/.exec(chunk);
      if (!m) continue;
      const n = Number(m[1]);
      const name = m[2];
      if (!Number.isFinite(n)) continue;
      const sp = Object.values(SPECS).find((x: any) => x.name === name) as any;
      if (!sp) { bad.push(`funds=${funds}: notice names unknown spec "${name}"`); continue; }
      const delta = (post.get(sp.id) ?? 0) - (before.get(sp.id) ?? 0);
      if (delta !== n) bad.push(`funds=${funds}: notice claims ${n} x ${name} but ${delta} were placed`);
    }
  }
  assert.deepEqual(bad, [], bad.join('\n'));
});

test('ATTACK/NOTICE-CAUSE: "funds ran out" must not be claimed when funds were ample', () => {
  const s: any = grow(1e12);
  const after: any = reducer(s, { type: 'resolveDemandAll' } as never);
  const notice: string = after.placeNotice ?? '';
  if (/funds ran out/.test(notice)) {
    assert.ok(after.funds < 1000, `notice blames funds but £${after.funds} remains: ${notice}`);
  }
});

test('ATTACK/HEALTH-FIRST: with a budget covering only Health, Health is fully built and nothing else is', () => {
  const s: any = grow(1e12);
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length >= 3);
  assert.ok(order[0].serviceKey === 'gp' || order[0].serviceKey === 'hosp', `Health must lead, got ${order[0].serviceKey}`);
  const healthCost = order.filter((p) => p.serviceKey === 'gp' || p.serviceKey === 'hosp')
    .reduce((a, p) => a + p.count * placementCost(SPECS[p.specId]), 0);
  const capped = { ...s, funds: healthCost };
  const after: any = reducer(capped as never, { type: 'resolveDemandAll' } as never);
  assert.ok(after.funds >= 0);
  const before = specCounts(s);
  const post = specCounts(after);
  for (const p of order.filter((x) => x.serviceKey === 'gp' || x.serviceKey === 'hosp')) {
    const d = (post.get(p.specId) ?? 0) - (before.get(p.specId) ?? 0);
    assert.equal(d, p.count, `${p.serviceKey} must be FULLY funded first (got ${d}/${p.count})`);
  }
});

test('ATTACK/ADMIN: Fix All under administration must match N sequential resolveDemand dispatches', () => {
  const s: any = { ...grow(1e12), administrationState: { monthsRemaining: 6 } };
  const order = orderedDemandFixPlan(s);
  assert.ok(order.length > 0, 'precondition: a plan exists under administration');
  const all: any = reducer(s as never, { type: 'resolveDemandAll' } as never);
  let seq: any = s;
  for (const item of order) seq = reducer(seq, { type: 'resolveDemand', serviceKey: item.serviceKey } as never);
  const a = specCounts(all);
  const b = specCounts(seq);
  const keys = new Set([...a.keys(), ...b.keys()]);
  const bad: string[] = [];
  for (const k of keys) if ((a.get(k) ?? 0) !== (b.get(k) ?? 0)) bad.push(`${k}: fixAll=${a.get(k) ?? 0} sequential=${b.get(k) ?? 0}`);
  assert.deepEqual(bad, [], `Fix All diverges from sequential Fix under administration:\n${bad.join('\n')}`);
});

test('ATTACK/EQUIVALENCE: ample funds, Fix All == N sequential resolveDemand in priority order', () => {
  const s: any = grow(1e12);
  const order = orderedDemandFixPlan(s);
  const all: any = reducer(s as never, { type: 'resolveDemandAll' } as never);
  let seq: any = s;
  for (const item of order) seq = reducer(seq, { type: 'resolveDemand', serviceKey: item.serviceKey } as never);
  assert.equal(all.funds, seq.funds, 'treasury must agree');
  const a = specCounts(all), b = specCounts(seq);
  const keys = new Set([...a.keys(), ...b.keys()]);
  for (const k of keys) assert.equal(a.get(k) ?? 0, b.get(k) ?? 0, `spec ${k} count differs`);
});
