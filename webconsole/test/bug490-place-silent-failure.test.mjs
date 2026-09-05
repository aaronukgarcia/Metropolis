// BUG-490 — "the workload/construction build queue UI has no observable
// effect" (Aaron, dogfood report, A2Bev001.md Q100007/Q100021/Q100023/Q100037).
//
// ROOT CAUSE (traced UI-event-handler -> reducer -> render, per the lead's
// re-scoping comments on the item): the webconsole has no multi-tick
// construction queue at all (map-click placement is instant) — so "queueing
// a build via the screen has no effect" cannot mean a broken queue mechanic.
// It DOES mean a real click-through-no-effect defect: the reducer's 'place'
// case had FOUR silent `return state` branches (level-locked, insufficient
// funds, occupied, out-of-bounds) that produced ZERO observable change —
// no placeNotice, no NewsFeed entry, nothing in the debug JSON. Insufficient
// funds was already fixed by BUG-396 (see bug396-place-feedback.test.mjs).
// This file proves the two PRACTICALLY-REACHABLE remaining branches now
// surface explicit click-time feedback (Aaron's ruling, Q100037: a toast-style
// notice naming the reason, via the existing placeNotice/NewsFeed funnel):
//   1. Clicking an OCCUPIED tile (the primary suspected repro — the map-click
//      handler in MapView.tsx only gates on affordability before dispatch,
//      never on occupancy, so an occupied-tile click reaches the reducer's
//      `fits()` check completely unguarded).
//   2. Clicking to place a LEVEL-LOCKED spec that reaches the reducer via any
//      non-UI-gated path (defence-in-depth, mirrors the allowance/admin/funds
//      notices already on this exact code path).
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding engine.ts fix (BUG-490) is reverted — deleting either new
// placeNotice assignment reddens its test.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { placementCost, SPECS } from '../src/sim/data.ts';

const countAt = (s, x, y) => s.buildings.filter((b) => b.x === x && b.y === y).length;

// An empty tile clear of the starter city (same convention as bug396's test).
const X = 5;
const Y = 5;

// A cheap, always-affordable, unlocked spec to occupy X,Y with first.
const OCCUPIER = 'road';

test('BUG-490 #1: clicking to place on an ALREADY-OCCUPIED tile sets placeNotice (was silent)', () => {
  const fresh = initialState();
  const occupiedState = reducer(fresh, { type: 'place', spec: OCCUPIER, x: X, y: Y });
  assert.equal(countAt(occupiedState, X, Y), 1, 'precondition: the tile is now occupied');

  const withStaleNoticeCleared = { ...occupiedState, placeNotice: null };
  const before = withStaleNoticeCleared.buildings.length;
  const after = reducer(withStaleNoticeCleared, { type: 'place', spec: OCCUPIER, x: X, y: Y });

  assert.equal(after.buildings.length, before, 'no second building is placed on the occupied tile');
  assert.ok(after.placeNotice, 'placeNotice must be set — the player must learn why nothing happened');
  assert.match(after.placeNotice, /occupied/i, 'notice names the actual reason');
});

test('BUG-490 #2: clicking to place a LEVEL-LOCKED spec sets placeNotice (was silent)', () => {
  // Find a real catalogue spec that is locked at the fresh-game level (unlock > 0)
  // and NOT a placeholder (canEnterSim must pass so this exercises specUnlocked,
  // not the earlier canEnterSim guard).
  const locked = Object.values(SPECS).find((sp) => !sp.placeholder && sp.unlock > 0);
  assert.ok(locked, 'precondition: the catalogue has at least one level-gated spec');

  const fresh = { ...initialState(), placeNotice: null, unlockedAll: false };
  const before = fresh.buildings.length;
  const after = reducer(fresh, { type: 'place', spec: locked.id, x: X, y: Y });

  assert.equal(after.buildings.length, before, 'a locked spec must NOT be placed');
  assert.equal(countAt(after, X, Y), 0, 'no building lands at the target tile');
  assert.ok(after.placeNotice, 'placeNotice must be set — the player must learn why nothing happened');
  assert.match(after.placeNotice, /lock/i, 'notice names the actual reason');
});

test('BUG-490 #3: a successful placement still clears any prior placeNotice (unchanged BUG-396 contract)', () => {
  const withStaleNotice = { ...initialState(), placeNotice: "Can't build here — tile already occupied" };
  const before = withStaleNotice.buildings.length;
  const after = reducer(withStaleNotice, { type: 'place', spec: OCCUPIER, x: X, y: Y });
  assert.equal(after.buildings.length, before + 1, 'the building is placed on an open tile');
  assert.equal(after.placeNotice, null, 'a successful placement clears any prior notice');
});

test('BUG-490 #4: deterministic — identical inputs to the newly-fed branches yield identical outputs', () => {
  const fresh = initialState();
  const occupiedState = reducer(fresh, { type: 'place', spec: OCCUPIER, x: X, y: Y });
  const a = reducer(occupiedState, { type: 'place', spec: OCCUPIER, x: X, y: Y });
  const b = reducer(occupiedState, { type: 'place', spec: OCCUPIER, x: X, y: Y });
  assert.equal(JSON.stringify(a), JSON.stringify(b), 'occupied-tile rejection is deterministic (GR#21)');
});
