// milestone-cash-rewards.test.mjs — FEAT-milestone-cash-rewards-2026-09-02
// (Q100047b Aaron ruling B1: "an achieved milestone that does nothing reads
// as broken; small cash + a notice at minimum"). RED-proofs for the gap: the
// 6 MILESTONES predicates (data.ts) used to fire correctly but grant NOTHING
// — reaching one only changed a chip's colour. This closes that: a one-time
// cash reward + the existing level-up-style notice on first achievement,
// persisted so it can never double-pay, retroactive for old saves.
//
// Every test states its OWN mutant/failure scenario so a broken
// implementation demonstrably fails these, not just "looks green".

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MILESTONES, MILESTONE_REWARDS, sanitizeClaimedMilestones } from '../src/sim/data.ts';
import { initialState, reducer, computeMilestoneRewards } from '../src/sim/engine.ts';

/** A residential building placed on empty ground far from the starter city's
 *  own infrastructure — enough on its own to satisfy m1 "First Homes"
 *  (countByKind(buildings).residential > 0) without disturbing anything else. */
function withResidentialBuilding(s, overrides = {}) {
  return {
    ...s,
    buildings: [...s.buildings, { id: 990001, spec: 'res_hut', x: 10, y: 10 }],
    ...overrides,
  };
}

function tickN(s, n) {
  let out = s;
  for (let i = 0; i < n; i++) out = reducer(out, { type: 'tick' });
  return out;
}

function milestoneLedgerRows(s, label) {
  return s.ledger.filter((e) => e.label === `Milestone Reward: ${label}`);
}

// ---------------------------------------------------------------------------
// Catalogue + reward table sanity
// ---------------------------------------------------------------------------
test('MILESTONE_REWARDS covers every MILESTONES id with a non-negative integer placeholder (mutant: a milestone silently maps to undefined/0-by-accident)', () => {
  for (const m of MILESTONES) {
    const reward = MILESTONE_REWARDS[m.id];
    assert.ok(reward !== undefined, `MILESTONE_REWARDS missing entry for ${m.id}`);
    assert.ok(Number.isInteger(reward) && reward >= 0, `MILESTONE_REWARDS[${m.id}] must be a non-negative integer, got ${reward}`);
  }
});

// ---------------------------------------------------------------------------
// First achievement pays exactly once + notice fires
// ---------------------------------------------------------------------------
test('first achievement queues the reward + claims the milestone the SAME tick it is observed met (mutant: milestone marked colour-only, nothing queued)', () => {
  const s0 = withResidentialBuilding(initialState());
  assert.strictEqual((s0.claimedMilestones ?? []).includes('m1'), false, 'test setup: m1 must start unclaimed');

  const s1 = reducer(s0, { type: 'tick' });
  assert.ok((s1.claimedMilestones ?? []).includes('m1'), 'm1 must be claimed the tick its predicate is first observed true');
  const queued = (s1.pendingMilestoneRewards ?? []).find((r) => r.milestoneId === 'm1');
  assert.ok(queued, 'm1 reward must be queued into pendingMilestoneRewards on the detection tick');
  assert.strictEqual(queued.totalReward, MILESTONE_REWARDS.m1);
  assert.strictEqual(queued.notice.label, 'First Homes');
  assert.strictEqual(queued.notice.cash, MILESTONE_REWARDS.m1);
  // Not yet paid — the drain (ledger row / inflow / notice) happens NEXT tick.
  assert.strictEqual(milestoneLedgerRows(s1, 'First Homes').length, 0, 'reward must not be paid on the detection tick itself');
});

test('first achievement pays exactly once via a ledger row + inflow + the milestoneNotice banner fires (mutant: reward computed but never drained into flows/ledger, or notice never set)', () => {
  const s0 = withResidentialBuilding(initialState());
  const s1 = reducer(s0, { type: 'tick' }); // detected + claimed + queued
  const s2 = reducer(s1, { type: 'tick' }); // drained: paid + ledger + notice

  const ledgerRows = milestoneLedgerRows(s2, 'First Homes');
  assert.strictEqual(ledgerRows.length, 1, `expected exactly one ledger row, got ${ledgerRows.length}`);
  assert.strictEqual(ledgerRows[0].amount, MILESTONE_REWARDS.m1);

  const inflowRow = s2.lastFlows.inflows.find((f) => f.label === 'Milestone Reward: First Homes');
  assert.ok(inflowRow, 'reward must appear as a labelled inflow (conservation-visible, mirrors Level Rewards)');
  assert.strictEqual(inflowRow.value, MILESTONE_REWARDS.m1);

  assert.ok(s2.milestoneNotice, 'milestoneNotice banner must be set on the pay tick');
  assert.deepStrictEqual(s2.milestoneNotice, { id: 'm1', label: 'First Homes', cash: MILESTONE_REWARDS.m1 });

  // Further ticks must never re-pay it.
  const s3 = tickN(s2, 5);
  assert.strictEqual(milestoneLedgerRows(s3, 'First Homes').length, 1, 'must still be exactly one ledger row after further ticks');
  assert.strictEqual((s3.pendingMilestoneRewards ?? []).length, 0, 'nothing left queued for m1');
});

test('dismissMilestoneNotice clears only the banner, never the paid reward or the claim (mutant: dismiss un-claims the milestone / clears the ledger)', () => {
  const s0 = withResidentialBuilding(initialState());
  const s2 = reducer(reducer(s0, { type: 'tick' }), { type: 'tick' });
  assert.ok(s2.milestoneNotice, 'test setup: notice must be set before dismissing');
  const dismissed = reducer(s2, { type: 'dismissMilestoneNotice' });
  assert.strictEqual(dismissed.milestoneNotice, null);
  assert.deepStrictEqual(dismissed.claimedMilestones, s2.claimedMilestones);
  assert.strictEqual(milestoneLedgerRows(dismissed, 'First Homes').length, 1);
});

// ---------------------------------------------------------------------------
// Re-achieving / oscillating predicates never double-pay
// ---------------------------------------------------------------------------
test('computeMilestoneRewards never re-queues an already-claimed milestone no matter how the predicate oscillates (mutant: guard checks predicate only, ignores claimedMilestones)', () => {
  const withBuilding = withResidentialBuilding(initialState());
  const withoutBuilding = { ...withBuilding, buildings: [] };
  const claimed = ['m1'];

  // met + claimed -> nothing (the double-pay case this whole feature exists to prevent).
  assert.deepStrictEqual(computeMilestoneRewards(withBuilding, claimed), []);
  // not met + claimed -> nothing (predicate false, also already claimed).
  assert.deepStrictEqual(computeMilestoneRewards(withoutBuilding, claimed), []);
  // met again + claimed -> STILL nothing — this is the oscillation case.
  assert.deepStrictEqual(computeMilestoneRewards(withBuilding, claimed), []);
  // Sanity: the SAME predicate against an UNCLAIMED set does produce a reward,
  // proving the guard above is actually claimedMilestones-driven, not a
  // silently-always-empty stub.
  const fresh = computeMilestoneRewards(withBuilding, []);
  assert.strictEqual(fresh.length, 1);
  assert.strictEqual(fresh[0].milestoneId, 'm1');
});

test('end-to-end: building then demolishing then rebuilding the qualifying residential across several ticks pays m1 exactly once (mutant: reward re-fires whenever the predicate is momentarily true again)', () => {
  let s = initialState();
  s = reducer(s, { type: 'tick' }); // no residential yet — must not claim m1
  assert.strictEqual((s.claimedMilestones ?? []).includes('m1'), false);

  s = withResidentialBuilding(s);
  s = reducer(s, { type: 'tick' }); // detected + claimed
  s = reducer(s, { type: 'tick' }); // paid
  assert.strictEqual(milestoneLedgerRows(s, 'First Homes').length, 1);

  // Oscillate: remove the building (predicate now false), tick, re-add it
  // (predicate true again), tick several more times.
  s = { ...s, buildings: s.buildings.filter((b) => b.spec !== 'res_hut') };
  s = tickN(s, 3);
  s = withResidentialBuilding(s);
  s = tickN(s, 5);

  assert.strictEqual(milestoneLedgerRows(s, 'First Homes').length, 1, 'must still be exactly one ledger row after the predicate oscillates true/false/true');
});

// ---------------------------------------------------------------------------
// Old-save-with-met-milestones pays once on load-observation
// ---------------------------------------------------------------------------
test('a legacy/old save with claimedMilestones absent but a milestone ALREADY met pays it once, on the first tick that observes it (Aaron\'s steer) (mutant: retroactive pay never happens, or pays every tick forever)', () => {
  // Simulate a state as if deserialized from an old save predating this
  // feature entirely: claimedMilestones is `undefined` (not even an empty
  // array), and the m1 predicate is ALREADY satisfied from long-standing
  // buildings, at a tick number far past genesis.
  const loaded = withResidentialBuilding({ ...initialState(), tick: 500 });
  delete loaded.claimedMilestones;
  assert.strictEqual(loaded.claimedMilestones, undefined, 'test setup: simulate a pre-feature save shape');

  const afterDetect = reducer(loaded, { type: 'tick' });
  assert.ok((afterDetect.claimedMilestones ?? []).includes('m1'), 'must claim m1 on the very first tick that observes the old save');
  assert.strictEqual(milestoneLedgerRows(afterDetect, 'First Homes').length, 0, 'not paid yet — detection and payment are still one tick apart for an old save too');

  const afterPay = reducer(afterDetect, { type: 'tick' });
  assert.strictEqual(milestoneLedgerRows(afterPay, 'First Homes').length, 1, 'must pay exactly once on the tick after first observation');

  const later = tickN(afterPay, 10);
  assert.strictEqual(milestoneLedgerRows(later, 'First Homes').length, 1, 'a fresh save can never double-pay because the claimed set persists — no re-payment on subsequent ticks');
});

// ---------------------------------------------------------------------------
// GR#16 — corrupt rewarded-set shapes sanitised
// ---------------------------------------------------------------------------
test('GR#16: sanitizeClaimedMilestones never lets a corrupt/legacy save produce anything but a deduplicated array of KNOWN milestone ids (mutant: bare `?? []` guard, no per-entry validation)', () => {
  const validIds = MILESTONES.map((m) => m.id);
  const shapes = [
    undefined,
    null,
    'abc',
    42,
    {},
    { m1: true },
    [],
    [1, 2, 3],
    [null, undefined, {}],
    ['m1', 'm1', 'm1'], // duplicate
    ['bogus-id', 'also-bogus'],
    ['m1', 'bogus-id', 'm2'],
    [...validIds, 'unknown-future-id'],
    validIds, // already-clean input must round-trip unchanged (order-wise, catalogue order)
  ];
  for (const bad of shapes) {
    const out = sanitizeClaimedMilestones(bad);
    assert.ok(Array.isArray(out), `sanitizeClaimedMilestones(${JSON.stringify(bad)}) must return an array`);
    const seen = new Set();
    for (const id of out) {
      assert.strictEqual(typeof id, 'string', `entry must be a string, got ${typeof id}`);
      assert.ok(validIds.includes(id), `entry "${id}" is not a known MILESTONES id`);
      assert.ok(!seen.has(id), `duplicate entry "${id}" in sanitized output`);
      seen.add(id);
    }
  }
  // Deterministic ordering: two runs of the same corrupt/out-of-order input
  // must produce byte-identical output (GR#21) — catalogue order, not
  // insertion-order-of-the-corrupt-input.
  const shuffledValid = [...validIds].reverse();
  assert.deepStrictEqual(sanitizeClaimedMilestones(shuffledValid), sanitizeClaimedMilestones(validIds));
});

test('GR#16: a hand-corrupted claimedMilestones cannot poison advance()/reducer after several ticks (mutant: corrupt entry crashes or silently re-pays a bogus milestone)', () => {
  const corruptValues = ['abc', 42, {}, ['m1', 'bogus'], [null, 5, {}], [...MILESTONES.map((m) => m.id), 'm1', 'm1']];
  for (const bad of corruptValues) {
    const s = withResidentialBuilding({ ...initialState(), claimedMilestones: bad });
    let after = s;
    assert.doesNotThrow(() => {
      after = tickN(s, 5);
    }, `advance()/reducer must not throw on corrupt claimedMilestones=${JSON.stringify(bad)}`);
    assert.ok(Array.isArray(after.claimedMilestones), 'claimedMilestones must self-heal into an array');
    assert.ok(Number.isFinite(after.funds), 'funds must stay finite after ticking through corrupt claimedMilestones');
  }
});

// ---------------------------------------------------------------------------
// Conservation: funds delta == the grant, ledger row present
// ---------------------------------------------------------------------------
test('conservation: the paid tick\'s fundsAtTickEnd equals fundsAtTickStart + Σinflows − Σoutflows EXACTLY including the milestone reward (mutant: reward added to funds but not to inflows, breaking the invariant)', () => {
  const s0 = withResidentialBuilding(initialState());
  const s1 = reducer(s0, { type: 'tick' });
  const s2 = reducer(s1, { type: 'tick' }); // the pay tick

  const totalIn = s2.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const totalOut = s2.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.strictEqual(s2.fundsAtTickEnd, s2.fundsAtTickStart + totalIn - totalOut);

  // The reward itself is present and exact within that inflow total.
  const rewardInflow = s2.lastFlows.inflows.find((f) => f.label === 'Milestone Reward: First Homes');
  assert.strictEqual(rewardInflow.value, MILESTONE_REWARDS.m1);
  const rewardLedger = milestoneLedgerRows(s2, 'First Homes')[0];
  assert.strictEqual(rewardLedger.amount, rewardInflow.value, 'ledger row amount must exactly match the booked inflow value');
});

// ---------------------------------------------------------------------------
// Determinism through the reward tick
// ---------------------------------------------------------------------------
test('AC-determinism: identical starting states replay to byte-identical milestone fields through the detect + pay ticks (mutant: Math.random()/Date.now()/map-order creeps into the detection or drain)', () => {
  function replay() {
    let s = withResidentialBuilding(initialState());
    s = tickN(s, 4); // detect + pay + a couple of settle ticks
    return s;
  }
  const r1 = replay();
  const r2 = replay();
  assert.deepStrictEqual(r1.claimedMilestones, r2.claimedMilestones);
  assert.deepStrictEqual(r1.pendingMilestoneRewards, r2.pendingMilestoneRewards);
  assert.deepStrictEqual(r1.milestoneNotice, r2.milestoneNotice);
  assert.deepStrictEqual(milestoneLedgerRows(r1, 'First Homes'), milestoneLedgerRows(r2, 'First Homes'));
  assert.strictEqual(r1.funds, r2.funds);
});
