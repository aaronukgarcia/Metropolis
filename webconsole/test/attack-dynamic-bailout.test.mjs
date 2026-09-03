// attack-dynamic-bailout.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23)
// against FEAT-2326609745 (dynamic auto-scaled bailout). Attacker != author.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, placementCost } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  sanitizeTreasury,
  sweepOrphanConnects,
  TICKS_PER_MONTH,
} from '../src/sim/engine.ts';
import { buildGameSave, parseGameSave, gameSaveText } from '../src/sim/gamesave.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_FLOOR,
  DYNAMIC_BAILOUT_INJECTION_LABEL,
  BAILOUT_SECOND_INJECTION_LABEL,
  netOpexBleedPerTick,
  computeDynamicBailoutOffer,
} from '../src/sim/fiscal.ts';

const PERIOD = 2 * TICKS_PER_MONTH;
const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);
const CRISIS_FUNDS = DEBT_THRESHOLD_FOR_BAILOUT * 2;

function board(buildings, extra = {}) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return { ...base, unlockedAll: true, funds: 10_000_000, buildings, nextId: maxId + 1, roadNotice: null, ledger: [], ...extra };
}

function sum(a) {
  return a.reduce((x, y) => x + y.value, 0);
}

function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

function freshCrisisEntry(state) {
  return tickAtFunds(tickAtFunds(state, WARNING_BAND_FUNDS), CRISIS_FUNDS);
}

// ══════════════ A1: CAPEX TRACKER — the ninth site (double-count) ══════════════

test('A1: auto-connect sweep capex is counted ONCE, not twice', () => {
  // An orphan house 4 tiles from a road: the bi-monthly sweep lays connectors.
  const s0 = board(
    [
      { id: 1, spec: 'res_hut', x: 10, y: 10 },
      { id: 2, spec: 'road', x: 10, y: 14 },
    ],
    { tick: PERIOD - 1, cumulativeCapexSpent: 0, capexBackfilled: true },
  );
  // Ground truth: what the sweep ACTUALLY spends, measured off funds.
  const swept = sweepOrphanConnects({ ...s0, tick: PERIOD });
  const realSpend = s0.funds - swept.funds;
  assert.ok(realSpend > 0, 'precondition: the sweep must actually spend money');

  const s1 = reducer(s0, { type: 'tick' });
  assert.equal(s1.tick, PERIOD, 'precondition: the sweep tick fired');
  const connectOutflow = (s1.lastFlows.outflows.find((f) => f.label === 'Road Auto-Connect') ?? { value: 0 }).value;
  assert.equal(connectOutflow, realSpend, 'precondition: outflow equals the measured sweep spend');

  assert.equal(
    s1.cumulativeCapexSpent,
    realSpend,
    `auto-connect capex double-counted: cumulativeCapexSpent=${s1.cumulativeCapexSpent} for a real spend of ${realSpend}`,
  );
});

// ══════════════ A2: CONSERVATION through the whole insolvency arc ══════════════

test('A2: funds == before + inflows - outflows on EVERY tick of the insolvency arc, grant tick included', () => {
  let s = initialState();
  s = reducer(s, { type: 'debugFunds', amount: WARNING_BAND_FUNDS - s.funds });
  let sawGrant = false;
  for (let i = 0; i < 400; i++) {
    if (i === 3) s = reducer(s, { type: 'debugFunds', amount: CRISIS_FUNDS - s.funds });
    const before = s.funds;
    const next = reducer(s, { type: 'tick' });
    const expected = before + sum(next.lastFlows.inflows) - sum(next.lastFlows.outflows);
    assert.equal(next.funds, expected, `conservation broke at tick ${next.tick}`);
    if (next.lastFlows.inflows.some((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL)) sawGrant = true;
    s = next;
  }
  assert.ok(sawGrant, 'precondition: the arc must actually credit a dynamic grant');
});

test('A2b: hostile same-tick collision — grant + asset sale + level reward at deeply negative funds still conserves', () => {
  let s = initialState();
  s = reducer(s, { type: 'debugFunds', amount: WARNING_BAND_FUNDS - s.funds });
  s = reducer(s, { type: 'tick' });
  // Deeply negative + a queued level reward landing the same tick as the grant.
  s = reducer(s, { type: 'debugFunds', amount: DEBT_THRESHOLD_FOR_BAILOUT * 50 - s.funds });
  s = { ...s, pendingRewards: [{ totalReward: 123_456, notice: null }] };
  const before = s.funds;
  const next = reducer(s, { type: 'tick' });
  assert.ok(
    next.lastFlows.inflows.some((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL),
    'precondition: grant must fire this tick',
  );
  assert.equal(next.funds, before + sum(next.lastFlows.inflows) - sum(next.lastFlows.outflows), 'collision tick conservation');
});

// ══════════════ A3: ONCE-ONLY LATCH across save/load (BUG-504 re-arm class) ══════════════

function roundTrip(state) {
  const save = buildGameSave({ state, journal: emptyJournal(), journalTail: [], name: 'a', buildVersion: 'test' });
  const parsed = parseGameSave(gameSaveText(save));
  assert.equal(parsed.ok, true);
  return parsed.save.savepoint.snapshot;
}

test('A3: grant → save → load → second insolvency must NOT re-grant (PASSES — branch B boundary holds)', () => {
  let s = freshCrisisEntry(initialState());
  assert.ok(s.lastFlows.inflows.some((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL), 'grant fired');
  assert.equal(s.dynamicBailoutUsed, true, 'latch set');
  // Recover fully and run the bailout year out so bailoutState clears cleanly.
  s = reducer(s, { type: 'debugFunds', amount: 50_000_000 - s.funds });
  for (let i = 0; i < 400; i++) s = reducer(s, { type: 'tick' });
  assert.equal(s.bailoutState, null, 'precondition: recovered, no bailout in flight');
  assert.equal(s.bailoutSecondState, null);
  assert.equal(s.declineState, null);

  const loaded = roundTrip(s);
  assert.equal(loaded.dynamicBailoutUsed, true, 'LATCH LOST ACROSS SAVE/LOAD — second grant is now possible');

  const again = freshCrisisEntry(loaded);
  assert.ok(
    !again.lastFlows.inflows.some((f) => f.label === DYNAMIC_BAILOUT_INJECTION_LABEL),
    'a SECOND dynamic grant was minted after a save/load cycle',
  );
  assert.ok(
    again.lastFlows.inflows.some((f) => f.label === BAILOUT_SECOND_INJECTION_LABEL),
    'the second insolvency must escalate to the UNCHANGED worse-terms path (Aaron branch B)',
  );
});

test('A3b: reset (new game) clears the latch with the wipe', () => {
  const fresh = initialState();
  assert.equal(fresh.dynamicBailoutUsed, false);
  assert.equal(fresh.cumulativeCapexSpent, 0);
  assert.equal(fresh.capexBackfilled, true);
});

// ══════════════ A4: BACKFILL — once-only, corrupt inputs ══════════════

function legacy(extra = {}) {
  const s = board([{ id: 1, spec: 'res_hut', x: 10, y: 10 }]);
  delete s.cumulativeCapexSpent;
  delete s.capexBackfilled;
  delete s.dynamicBailoutUsed;
  return { ...s, ...extra };
}

test('A4: legacy save backfills once from the standing asset base, and never re-runs', () => {
  const l = legacy();
  const a = sanitizeTreasury(l);
  const expected = placementCost(SPECS.res_hut);
  assert.equal(a.cumulativeCapexSpent, expected);
  assert.equal(a.capexBackfilled, true);
  assert.equal(a.dynamicBailoutUsed, false);
  // Spend more, then re-sanitize: the backfill must not re-run and clobber it.
  const b = sanitizeTreasury({ ...a, cumulativeCapexSpent: a.cumulativeCapexSpent + 999 });
  assert.equal(b.cumulativeCapexSpent, expected + 999, 'backfill re-ran and clobbered real spend');
  // load → save → load byte-stability.
  const t1 = gameSaveText(buildGameSave({ state: a, journal: emptyJournal(), journalTail: [], name: 'n', buildVersion: 'v', now: new Date(0) }));
  const r1 = parseGameSave(t1).save.savepoint.snapshot;
  const t2 = gameSaveText(buildGameSave({ state: r1, journal: emptyJournal(), journalTail: [], name: 'n', buildVersion: 'v', now: new Date(0) }));
  assert.equal(t1, t2, 'save/load/save drifted');
});

test('A4b: no-double-dip migration — a save mid-bailout is latched true', () => {
  assert.equal(sanitizeTreasury(legacy({ bailoutState: { enteredAt: 5 } })).dynamicBailoutUsed, true);
  assert.equal(sanitizeTreasury(legacy({ firstBailoutCount: 1 })).dynamicBailoutUsed, true);
  assert.equal(sanitizeTreasury(legacy({ declineState: { enteredAt: 1 } })).dynamicBailoutUsed, true);
});

test('A4c: computeDynamicBailoutOffer NEVER returns below BAILOUT_FLOOR (its own documented clamp)', () => {
  for (const bad of [NaN, -1e12, 1e308, Infinity, '5000', null, {}, 2e17, Number.MAX_SAFE_INTEGER * 100]) {
    const r = computeDynamicBailoutOffer(bad, 0);
    assert.ok(Number.isFinite(r.offer), `non-finite offer from cumulativeCapexSpent=${String(bad)}`);
    assert.ok(
      r.offer >= BAILOUT_FLOOR,
      `offer ${r.offer} (branch "${r.branch}") is BELOW the documented floor ${BAILOUT_FLOOR} for capex=${String(bad)} — sanitizeFunds() returns 0 for a non-safe-integer, silently voiding the grant AFTER the clamp`,
    );
  }
});

test('A4d: a corrupt (string/NaN) cumulativeCapexSpent is COERCED by sanitizeTreasury, not carried into state', () => {
  const s = sanitizeTreasury(board([{ id: 1, spec: 'res_hut', x: 1, y: 1 }], { cumulativeCapexSpent: 'not a number', capexBackfilled: true, dynamicBailoutUsed: false }));
  assert.equal(typeof s.cumulativeCapexSpent, 'number', 'GR#16: a stored non-number must be coerced at the storage boundary');
  assert.ok(Number.isFinite(s.cumulativeCapexSpent));
  // The real damage: every charge site does `(s.cumulativeCapexSpent ?? 0) + cost`,
  // which on a string is CONCATENATION, not addition — the tracker turns into
  // ever-growing garbage and the whole CAPEX-proportional feature silently
  // degrades to the floor for the rest of the playthrough (GR#1/#17: no error).
  const p1 = reducer({ ...s, unlockedAll: true, funds: 10_000_000 }, { type: 'place', spec: 'res_hut', x: 20, y: 20 });
  assert.equal(typeof p1.cumulativeCapexSpent, 'number', 'capex tracker string-concatenated instead of adding');
});

test('A4e: a corrupt dynamicBailoutUsed (non-boolean truthy/falsy) must not silently re-arm the once-only latch', () => {
  // A hand-edited / corrupted save carrying `0` or `""` is FALSY but DEFINED —
  // the migration only fires on `=== undefined`, so it passes straight through.
  const s = sanitizeTreasury(board([{ id: 1, spec: 'res_hut', x: 1, y: 1 }], {
    cumulativeCapexSpent: 0,
    capexBackfilled: true,
    dynamicBailoutUsed: 0,
    bailoutSecondState: { enteredAt: 3 },
  }));
  assert.equal(s.dynamicBailoutUsed, true, 'a falsy-but-defined latch on an already-escalated save re-arms the grant');
});

// ══════════════ A5: BLEED SSOT excludes the grant's own injection ══════════════

test('A5: netOpexBleedPerTick is identical before and after appending the grant inflow', () => {
  const flows = { inflows: [{ label: 'Council Tax', value: 100 }], outflows: [{ label: 'Upkeep', value: 5000 }] };
  const before = netOpexBleedPerTick(flows);
  const after = netOpexBleedPerTick({ inflows: [...flows.inflows, { label: DYNAMIC_BAILOUT_INJECTION_LABEL, value: 9_999_999 }], outflows: flows.outflows });
  assert.equal(after, before, 'the grant injection distorted the bleed reading');
  const after2 = netOpexBleedPerTick({ inflows: [...flows.inflows, { label: BAILOUT_SECOND_INJECTION_LABEL, value: 9_999_999 }], outflows: flows.outflows });
  assert.equal(after2, before);
});

// ══════════════ A6: DETERMINISM ══════════════

test('A6: two identical runs through the arc produce identical offers and capex totals', () => {
  const run = () => {
    let s = freshCrisisEntry(initialState());
    return [s.funds, s.cumulativeCapexSpent, sum(s.lastFlows.inflows)];
  };
  assert.deepEqual(run(), run());
});
