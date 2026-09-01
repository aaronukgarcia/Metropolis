// BUG-396 — clean, model-independent placement-feedback fixes.
//
// Covers the four unambiguous bugs (NOT the deferred insolvency model):
//   1. A cost-0 (free zone) placement succeeds even when funds are negative.
//   2. A PAID placement with insufficient funds is blocked AND sets placeNotice
//      (silent-failure fix) — the building is NOT placed.
//   3. A PAID placement WITH sufficient funds proceeds, charges the cost, and
//      leaves placeNotice null.
//   4. The advisor (pickAutoSpec) never suggests a build the player cannot afford.
//   5. Determinism (GR#21): identical inputs yield byte-identical outputs.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if the
// corresponding fix is reverted.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { pickAutoSpec, placementCost, SPECS } from '../src/sim/data.ts';

// A free zone (category 'zones' → placementCost 0) and a paid network spec
// (real cost, BUG-452 inc1 rebased — read live via placementCost(), never inlined).
const FREE_ZONE = 'res_hut'; // Small Holding — category 'zones', placementCost 0.
const PAID_SPEC = 'road'; // Road — category 'network', real (non-zero) cost.
const PAID_COST = placementCost(SPECS[PAID_SPEC]);

// An empty tile clear of the starter city (starterCity fills rows ~56-96 near
// x=150, rails at y=84, hs1 at y=205); (5,5) is open — snapshot.test uses it too.
const X = 5;
const Y = 5;

const countAt = (s, x, y) => s.buildings.filter((b) => b.x === x && b.y === y).length;

test('BUG-396 #1: a cost-0 (free zone) placement succeeds even while INSOLVENT (funds < 0)', () => {
  assert.equal(placementCost(SPECS[FREE_ZONE]), 0, 'precondition: the free zone costs 0');
  const insolvent = { ...initialState(), funds: -5000 };
  const before = insolvent.buildings.length;
  const after = reducer(insolvent, { type: 'place', spec: FREE_ZONE, x: X, y: Y });
  assert.equal(after.buildings.length, before + 1, 'free zone must place while insolvent');
  assert.equal(countAt(after, X, Y), 1, 'the free zone landed at the target tile');
  // A free placement charges nothing — funds unchanged (upkeep is a separate path).
  assert.equal(after.funds, -5000, 'a free zone charges £0, so funds are unchanged');
});

test('BUG-396 #2: a PAID placement with insufficient funds is BLOCKED and sets placeNotice', () => {
  assert.ok(PAID_COST > 0, 'precondition: the paid spec actually costs money');
  const broke = { ...initialState(), funds: PAID_COST - 10, placeNotice: null };
  const before = broke.buildings.length;
  const after = reducer(broke, { type: 'place', spec: PAID_SPEC, x: X, y: Y });
  assert.equal(after.buildings.length, before, 'the building must NOT be placed');
  assert.equal(countAt(after, X, Y), 0, 'no building at the target tile');
  assert.ok(after.placeNotice, 'placeNotice must be set so the player learns why nothing happened');
  assert.match(after.placeNotice, /Insufficient funds/, 'notice explains the block');
  assert.match(after.placeNotice, /£/, 'notice names the amount needed');
  assert.equal(after.funds, broke.funds, 'a blocked placement charges nothing');
});

test('BUG-396 #3: a PAID placement WITH sufficient funds proceeds, charges the cost, clears the notice', () => {
  // Seed a stale notice to prove a successful placement clears it. The exact
  // wording/amount in the seeded notice string is irrelevant (it only proves
  // clearing) — funds must comfortably exceed PAID_COST regardless of the
  // catalogue's current scale (BUG-452 inc1 rebased road's real cost, so this
  // is derived, not a hardcoded £1,000/£40 pair that predates the rescale).
  const richFunds = PAID_COST + 1_000_000;
  const rich = { ...initialState(), funds: richFunds, placeNotice: 'Insufficient funds — shortfall noted' };
  const before = rich.buildings.length;
  const after = reducer(rich, { type: 'place', spec: PAID_SPEC, x: X, y: Y });
  assert.equal(after.buildings.length, before + 1, 'the building is placed');
  assert.equal(countAt(after, X, Y), 1, 'the building landed at the target tile');
  assert.equal(after.funds, richFunds - PAID_COST, 'funds charged exactly the placement cost');
  assert.equal(after.placeNotice, null, 'a successful placement clears any prior notice');
});

test('BUG-396 #4: the advisor never suggests a build costing more than current funds', () => {
  // A city with population but no power/water/health/school → real service shortfalls.
  const shortfall = { ...initialState(), population: 5000 };
  const rich = pickAutoSpec({ ...shortfall, funds: 10_000_000 });
  assert.ok(rich, 'precondition: an under-served city with ample funds gets a suggestion');
  const cost = placementCost(SPECS[rich.spec]);
  assert.ok(cost > 0, 'precondition: the suggested service build costs money');

  // With funds just under that cost, the advisor must NOT offer the unaffordable build.
  const poor = pickAutoSpec({ ...shortfall, funds: cost - 1 });
  assert.ok(
    poor === null || placementCost(SPECS[poor.spec]) <= cost - 1,
    'advisor must never suggest a build costing more than current funds'
  );

  // With zero funds every non-free service build is unaffordable → no suggestion.
  const broke = pickAutoSpec({ ...shortfall, funds: 0 });
  assert.ok(
    broke === null || placementCost(SPECS[broke.spec]) <= 0,
    'a broke city is only ever advised free (£0) builds, or nothing'
  );
});

test('BUG-396 #5: deterministic — identical inputs yield identical outputs', () => {
  const broke = { ...initialState(), funds: PAID_COST - 10 };
  const a = reducer(broke, { type: 'place', spec: PAID_SPEC, x: X, y: Y });
  const b = reducer(broke, { type: 'place', spec: PAID_SPEC, x: X, y: Y });
  assert.equal(JSON.stringify(a), JSON.stringify(b), 'blocked-placement reducer is deterministic');

  const shortfall = { ...initialState(), population: 5000, funds: 500 };
  assert.equal(
    JSON.stringify(pickAutoSpec(shortfall)),
    JSON.stringify(pickAutoSpec(shortfall)),
    'pickAutoSpec is deterministic'
  );
});
