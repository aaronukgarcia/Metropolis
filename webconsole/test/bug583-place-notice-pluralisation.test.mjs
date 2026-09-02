// bug583-place-notice-pluralisation.test.mjs — BUG-583.
//
// resolveDemand's/placeMany's affordability-shortfall placeNotice used to build
// its message with a bare `sp.name + (count === 1 ? '' : 's')` naive English
// pluraliser. That breaks the instant a catalogue name already ends in 's':
// SPECS.wat_clean.name === 'Water Works' (data.ts) rendered as
// "Placed 2 of 4 Water Workss — insufficient funds" for any count != 1.
//
// The fix (engine.ts formatPlacedCount) sidesteps English pluralisation
// entirely: "N x <Name>" reads correctly for every name in SPECS regardless
// of its own grammatical number. Exercised directly via 'placeMany' (rather
// than the demand-fix planner, whose spec choice is budget-sensitive and
// would make "which spec gets short-funded" non-deterministic to control) so
// the "Water Works" name is guaranteed to be the one in the notice.
//
// RED-PROOF: reverting formatPlacedCount's body to the old
// `${specName}${count === 1 ? '' : 's'}` (dropping the 'N x ' prefix) makes
// this test's /Water Workss/ assertion fail — verified by hand during
// development (see the PR diff / git history of engine.ts for the reverted
// form) and re-confirmed by temporarily restoring the old line and re-running.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, placementCost } from '../src/sim/data.ts';

function baseState(fundsOverride) {
  const base = initialState();
  return { ...base, unlockedAll: true, funds: fundsOverride, administrationState: null };
}

test('BUG-583: placeMany placeNotice never double-pluralises a name already ending in "s" (Water Works)', () => {
  assert.equal(SPECS.wat_clean.name, 'Water Works', 'precondition: catalogue name already ends in "s"');
  const sp = SPECS.wat_clean;
  const unitCost = placementCost(sp);
  assert.ok(unitCost > 0, 'precondition: wat_clean is a paid spec');

  // Four widely-spaced candidate tiles (no adjacency to worry about), funds
  // for barely more than one unit (road-adjacency connector costs can eat
  // into a naive `unitCost * N` budget — see demand-fix.test.mjs's identical
  // caveat — so assert a STRICTLY PARTIAL batch by shape, not an exact count)
  // so the shortfall notice (the only code path that names the spec) fires.
  const tiles = [
    { x: 5, y: 5 },
    { x: 15, y: 5 },
    { x: 25, y: 5 },
    { x: 35, y: 5 },
  ];
  const s = baseState(unitCost * 2);
  const result = reducer(s, { type: 'placeMany', spec: 'wat_clean', tiles });

  assert.ok(result.placeNotice, 'a partial batch placement must set a shortfall notice');
  assert.match(result.placeNotice, /^Placed [0-3] of 4/, `expected a strictly partial "N of 4" notice, got: ${result.placeNotice}`);

  // The bug: naive `+ 's'` on a name already ending in 's'.
  assert.doesNotMatch(result.placeNotice, /Water Workss/, `placeNotice double-pluralised "Water Works": ${result.placeNotice}`);
  // The fix's actual shape: "N x Water Works" (count-then-name, no suffix).
  assert.match(result.placeNotice, /4 x Water Works\b/, `placeNotice must read "N x Water Works", got: ${result.placeNotice}`);
});

test('BUG-583: a single-unit ("of 1") shortfall notice also uses the "N x Name" shape (no bare singular name)', () => {
  const sp = SPECS.wat_tower;
  const unitCost = placementCost(sp);
  assert.ok(unitCost > 0, 'precondition: wat_tower is a paid spec');

  // One candidate tile, zero funds — 0-of-1 placed, still must notice.
  const s = baseState(0);
  const result = reducer(s, { type: 'placeMany', spec: 'wat_tower', tiles: [{ x: 5, y: 5 }] });

  assert.ok(result.placeNotice, 'a fully-declined batch placement must still set a shortfall notice');
  assert.match(result.placeNotice, /Placed 0 of 1/, `expected a "0 of 1" notice, got: ${result.placeNotice}`);
  assert.match(result.placeNotice, /1 x Water Tower\b/, `placeNotice must read "1 x Water Tower" (not a bare/suffixed name), got: ${result.placeNotice}`);
});
