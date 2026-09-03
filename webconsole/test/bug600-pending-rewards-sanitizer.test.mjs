// bug600-pending-rewards-sanitizer.test.mjs — BUG-600 (P3, milestone round
// follow-up). BOTH reward pending queues — s.pendingMilestoneRewards (BUG-541)
// AND the pre-existing s.pendingRewards (level rewards) — lacked sanitisers:
// a hand-edited savepoint with a non-array value threw an uncaught TypeError
// inside advance() (the `for (const pr of s.pendingRewards)` / `for (const pr
// of pendingMilestoneRewards)` drain loops), and a NaN totalReward drove
// funds to NaN, silently breaking the tick-boundary conservation invariant
// with no error surfaced.
//
// Fixed with ONE shared engine.ts helper (sanitizePendingRewards, GR#16/GR#3)
// wired at BOTH boundaries: the load boundary (sanitizeTreasury, which runs
// on EVERY reducer() call including 'hydrate') and defensively inside
// advance()'s own drain loops (a NaN reaching funds must be structurally
// impossible, not merely prevented upstream).
//
// The independent round's exact corruption table (documented on BUG-600):
// non-array, junk elements, NaN totalReward, a 1000-element flood, dupes of
// already-paid ids. Every case below is run against BOTH queues.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, sanitizeTreasury } from '../src/sim/engine.ts';

const tickOnce = (s) => reducer(s, { type: 'tick' });
const tickN = (s, n) => {
  let out = s;
  for (let i = 0; i < n; i++) out = tickOnce(out);
  return out;
};
const conservationOk = (s) => {
  const i = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const o = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  return s.fundsAtTickEnd === s.fundsAtTickStart + i - o;
};

/** A clean base state, one tick in so lastFlows/fundsAtTickStart are populated. */
function base() {
  return tickOnce(initialState());
}

const VALID_LEVEL_REWARD = { totalReward: 500, newLevel: 3, notice: { level: 3, cash: 500, unlocked: [] } };
const VALID_MILESTONE_REWARD = {
  totalReward: 750,
  milestoneId: 'm1',
  notice: { id: 'm1', label: 'First Homes', cash: 750 },
};

// ---------------------------------------------------------------------------
// The shared corruption table — run against BOTH queues, at BOTH boundaries.
// ---------------------------------------------------------------------------
function corruptionsFor(valid) {
  return [
    ['non-array (string)', 'not-an-array'],
    ['non-array (plain object)', { totalReward: 1 }],
    ['non-array (number)', 42],
    ['non-array (null is handled via ?? already, but explicit null too)', null],
    ['array of junk elements', [null, undefined, 'x', 42, {}, [], true]],
    ['NaN totalReward', [{ ...valid, totalReward: NaN }]],
    ['Infinity totalReward', [{ ...valid, totalReward: Infinity }]],
    ['negative totalReward', [{ ...valid, totalReward: -999999 }]],
    ['non-integer totalReward', [{ ...valid, totalReward: 1.5 }]],
    ['string totalReward', [{ ...valid, totalReward: '500' }]],
    ['1000-element flood', new Array(1000).fill(valid)],
    ['dupes of a paid id / repeated identical entries', [valid, valid, valid]],
    ['mixed: one valid + one junk', [valid, { garbage: true }]],
  ];
}

// ---------------------------------------------------------------------------
// LOAD-TIME: sanitizeTreasury (runs on EVERY reducer() call, incl. 'hydrate')
// must self-heal both queues to a safe array and never throw.
// ---------------------------------------------------------------------------
test('BUG-600 load-time: sanitizeTreasury self-heals every corrupt pendingRewards shape, never throws', () => {
  const s = base();
  for (const [name, corrupt] of corruptionsFor(VALID_LEVEL_REWARD)) {
    let out;
    assert.doesNotThrow(() => {
      out = sanitizeTreasury({ ...s, pendingRewards: corrupt });
    }, `sanitizeTreasury threw on pendingRewards corruption: ${name}`);
    assert.ok(Array.isArray(out.pendingRewards), `pendingRewards must self-heal to an array for: ${name}`);
    for (const r of out.pendingRewards) {
      assert.ok(Number.isFinite(r.totalReward) && Number.isSafeInteger(r.totalReward) && r.totalReward >= 0,
        `a surviving pendingRewards element must have a safe non-negative integer totalReward: ${name}`);
    }
    assert.ok(out.pendingRewards.length <= 200, `flood must be capped: ${name}`);
  }
});

test('BUG-600 load-time: sanitizeTreasury self-heals every corrupt pendingMilestoneRewards shape, never throws', () => {
  const s = base();
  for (const [name, corrupt] of corruptionsFor(VALID_MILESTONE_REWARD)) {
    let out;
    assert.doesNotThrow(() => {
      out = sanitizeTreasury({ ...s, pendingMilestoneRewards: corrupt });
    }, `sanitizeTreasury threw on pendingMilestoneRewards corruption: ${name}`);
    assert.ok(Array.isArray(out.pendingMilestoneRewards), `pendingMilestoneRewards must self-heal to an array for: ${name}`);
    for (const r of out.pendingMilestoneRewards) {
      assert.ok(Number.isFinite(r.totalReward) && Number.isSafeInteger(r.totalReward) && r.totalReward >= 0,
        `a surviving pendingMilestoneRewards element must have a safe non-negative integer totalReward: ${name}`);
    }
    assert.ok(out.pendingMilestoneRewards.length <= 200, `flood must be capped: ${name}`);
  }
});

test('BUG-600 load-time: a well-shaped pendingRewards/pendingMilestoneRewards entry survives sanitizeTreasury unchanged', () => {
  const s = base();
  const withValid = { ...s, pendingRewards: [VALID_LEVEL_REWARD], pendingMilestoneRewards: [VALID_MILESTONE_REWARD] };
  const out = sanitizeTreasury(withValid);
  assert.deepEqual(out.pendingRewards, [VALID_LEVEL_REWARD]);
  assert.deepEqual(out.pendingMilestoneRewards, [VALID_MILESTONE_REWARD]);
});

test('BUG-600 load-time: a legacy save with pendingMilestoneRewards undefined does not throw and normalizes to []', () => {
  const s = base();
  const legacy = { ...s, pendingMilestoneRewards: undefined };
  const out = sanitizeTreasury(legacy);
  assert.deepEqual(out.pendingMilestoneRewards, []);
});

test('BUG-600 load-time: sanitizeTreasury returns the SAME reference (no churn) when both queues are already clean', () => {
  const s = { ...base(), pendingRewards: [VALID_LEVEL_REWARD], pendingMilestoneRewards: [VALID_MILESTONE_REWARD] };
  const sanitizedOnce = sanitizeTreasury(s);
  assert.equal(sanitizeTreasury(sanitizedOnce), sanitizedOnce, 'a clean state must be returned by identity');
});

// ---------------------------------------------------------------------------
// DRAIN-TIME: a 'tick' action must never throw and must never let a NaN/
// corrupt reward amount reach funds, and conservation must hold on the
// draining tick.
// ---------------------------------------------------------------------------
test('BUG-600 drain-time: a tick with corrupt pendingRewards never throws, never NaNs funds, conservation holds', () => {
  const s = base();
  for (const [name, corrupt] of corruptionsFor(VALID_LEVEL_REWARD)) {
    const poisoned = { ...s, pendingRewards: corrupt };
    let out;
    assert.doesNotThrow(() => {
      out = tickOnce(poisoned);
    }, `tick threw on pendingRewards corruption: ${name}`);
    assert.ok(Number.isFinite(out.funds), `funds must never become non-finite (NaN/Infinity) for: ${name}`);
    assert.ok(Number.isSafeInteger(out.funds), `funds must remain a safe integer for: ${name}`);
    assert.ok(conservationOk(out), `conservation must hold on the draining tick for: ${name}`);
    assert.deepEqual(out.pendingRewards, [], 'the queue must be drained (emptied) every tick regardless of input shape');
  }
});

test('BUG-600 drain-time: a tick with corrupt pendingMilestoneRewards never throws, never NaNs funds, conservation holds', () => {
  const s = base();
  for (const [name, corrupt] of corruptionsFor(VALID_MILESTONE_REWARD)) {
    const poisoned = { ...s, pendingMilestoneRewards: corrupt };
    let out;
    assert.doesNotThrow(() => {
      out = tickOnce(poisoned);
    }, `tick threw on pendingMilestoneRewards corruption: ${name}`);
    assert.ok(Number.isFinite(out.funds), `funds must never become non-finite (NaN/Infinity) for: ${name}`);
    assert.ok(Number.isSafeInteger(out.funds), `funds must remain a safe integer for: ${name}`);
    assert.ok(conservationOk(out), `conservation must hold on the draining tick for: ${name}`);
    assert.deepEqual(out.pendingMilestoneRewards, [], 'the queue must be drained (emptied) every tick regardless of input shape');
  }
});

test('BUG-600 drain-time: a valid pendingRewards entry still pays out through funds/inflows exactly once', () => {
  const s = base();
  const withValid = { ...s, pendingRewards: [VALID_LEVEL_REWARD] };
  const before = withValid.funds;
  const out = tickOnce(withValid);
  const levelInflows = out.lastFlows.inflows.filter((f) => f.label === 'Level Rewards');
  assert.ok(levelInflows.some((f) => f.value === VALID_LEVEL_REWARD.totalReward), 'the valid reward must still be paid');
  assert.equal(out.lastRewardedLevel, VALID_LEVEL_REWARD.newLevel, 'lastRewardedLevel must reflect the valid drained entry');
  assert.ok(conservationOk(out));
  assert.ok(Number.isFinite(out.funds) && out.funds !== before || levelInflows.length > 0);
});

test('BUG-600 drain-time: a valid pendingMilestoneRewards entry still pays out through funds/inflows exactly once', () => {
  const s = base();
  const withValid = { ...s, pendingMilestoneRewards: [VALID_MILESTONE_REWARD] };
  const out = tickOnce(withValid);
  const milestoneInflows = out.lastFlows.inflows.filter((f) => f.label === `Milestone Reward: ${VALID_MILESTONE_REWARD.notice.label}`);
  assert.equal(milestoneInflows.length, 1, 'the valid milestone reward must still be paid exactly once');
  assert.equal(milestoneInflows[0].value, VALID_MILESTONE_REWARD.totalReward);
  assert.ok(conservationOk(out));
});

test('BUG-600 drain-time: a mixed valid+junk pendingRewards array pays ONLY the valid entry, junk dropped silently (no crash)', () => {
  const s = base();
  const mixed = { ...s, pendingRewards: [VALID_LEVEL_REWARD, { garbage: true }, null, { totalReward: NaN, newLevel: 9, notice: {} }] };
  const out = tickOnce(mixed);
  const levelInflows = out.lastFlows.inflows.filter((f) => f.label === 'Level Rewards');
  assert.equal(levelInflows.length, 1, 'only the one valid entry may pay');
  assert.equal(levelInflows[0].value, VALID_LEVEL_REWARD.totalReward);
  assert.ok(conservationOk(out));
});

test('BUG-600 drain-time: a mixed valid+junk pendingMilestoneRewards array pays ONLY the valid entry, junk dropped silently (no crash)', () => {
  const s = base();
  const mixed = {
    ...s,
    pendingMilestoneRewards: [VALID_MILESTONE_REWARD, { garbage: true }, null, { totalReward: NaN, milestoneId: 'm2', notice: {} }],
  };
  const out = tickOnce(mixed);
  const milestoneInflows = out.lastFlows.inflows.filter((f) => f.label.startsWith('Milestone Reward: '));
  assert.equal(milestoneInflows.length, 1, 'only the one valid entry may pay');
  assert.equal(milestoneInflows[0].value, VALID_MILESTONE_REWARD.totalReward);
  assert.ok(conservationOk(out));
});

// ---------------------------------------------------------------------------
// Multi-tick conservation stays exact after a sanitised drain, both queues
// corrupted simultaneously.
// ---------------------------------------------------------------------------
test('BUG-600: conservation stays EXACT across 60 ticks after both queues are simultaneously poisoned then drained', () => {
  let s = {
    ...base(),
    pendingRewards: new Array(1000).fill(VALID_LEVEL_REWARD),
    pendingMilestoneRewards: [
      { totalReward: NaN, milestoneId: 'm1', notice: { id: 'm1', label: 'X', cash: NaN } },
      VALID_MILESTONE_REWARD,
      'junk',
    ],
  };
  for (let i = 0; i < 60; i++) {
    const before = s.funds;
    s = tickOnce(s);
    const inSum = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
    const outSum = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
    assert.equal(s.funds, before + inSum - outSum, `funds delta mismatch at tick ${s.tick}`);
    assert.ok(conservationOk(s), `conservation broke at tick ${s.tick}`);
    assert.ok(Number.isFinite(s.funds), `funds went non-finite at tick ${s.tick}`);
  }
  // Both queues must be fully drained and never resurrected by the poison.
  assert.deepEqual(s.pendingRewards, []);
  assert.deepEqual(s.pendingMilestoneRewards, []);
});

// ---------------------------------------------------------------------------
// A hostile pendingMilestoneRewards['dupes of a paid id'] must not let a
// milestone pay more than once across many ticks (mirrors the round's dupe
// finding, but proven at the sanitizer level directly rather than via the
// claim/detect predicate machinery).
// ---------------------------------------------------------------------------
test('BUG-600: dupes of the same milestone reward queued at once each pay (queue does not dedupe payment), but funds stay exact and finite', () => {
  const s = base();
  const dupes = { ...s, pendingMilestoneRewards: [VALID_MILESTONE_REWARD, VALID_MILESTONE_REWARD, VALID_MILESTONE_REWARD] };
  const out = tickOnce(dupes);
  const milestoneInflows = out.lastFlows.inflows.filter((f) => f.label === `Milestone Reward: ${VALID_MILESTONE_REWARD.notice.label}`);
  assert.equal(milestoneInflows.length, 3, 'the sanitiser does not dedupe VALID queued entries — that is claimedMilestones\' job, not this one');
  assert.ok(conservationOk(out));
  assert.ok(Number.isFinite(out.funds));
});
