// fast-build-inc1.test.mjs — FEAT-159: DEBUG-ONLY per-class fast-build override.
//
// Run with `npm test` / `node --test`; node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped code.
//
// Prove-can-fail:
//   • OFF-path exactness goes RED if scaleConstructionTicks ever altered ticks
//     while disabled (e.g. dropped the early return) — determinism guarantee.
//   • ON-path goes RED if a factor changed or the round/floor maths drifted.
//   • floor-at-1 goes RED if Math.max(1, …) were removed (would assert 0).
//   • the constructionTicks() integration goes RED if the SSOT seam were
//     unwired (base returned instead of the scaled value).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  isFastBuildEnabled,
  scaleConstructionTicks,
  FAST_BUILD_FLAG_KEY,
  FAST_BUILD_CLASS_FACTORS,
  DEFAULT_FAST_BUILD_FACTOR,
} from '../src/sim/debugBuildSpeed.ts';
import { constructionTicks } from '../src/sim/data.ts';

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, v),
  };
}

describe('isFastBuildEnabled (debug flag, default OFF)', () => {
  test('disabled when no storage is available', () => {
    assert.equal(isFastBuildEnabled(undefined), false);
  });
  test('disabled when the flag key is absent', () => {
    assert.equal(isFastBuildEnabled(fakeStorage()), false);
  });
  test('disabled for any value other than the literal "1"', () => {
    assert.equal(isFastBuildEnabled(fakeStorage({ [FAST_BUILD_FLAG_KEY]: 'true' })), false);
    assert.equal(isFastBuildEnabled(fakeStorage({ [FAST_BUILD_FLAG_KEY]: '0' })), false);
  });
  test('enabled only for the literal "1"', () => {
    assert.equal(isFastBuildEnabled(fakeStorage({ [FAST_BUILD_FLAG_KEY]: '1' })), true);
  });
  test('never throws when storage.getItem throws (private mode)', () => {
    const hostile = { getItem() { throw new Error('blocked'); } };
    assert.equal(isFastBuildEnabled(hostile), false);
  });
});

describe('scaleConstructionTicks — OFF is byte-for-byte unchanged', () => {
  test('every base value passes through untouched when disabled', () => {
    for (const base of [1, 3, 7, 100, 137, 4001]) {
      assert.equal(scaleConstructionTicks(base, 'residential', { enabled: false }), base);
      assert.equal(scaleConstructionTicks(base, 'power', { enabled: false }), base);
    }
  });
  test('disabled by default (no flag) leaves ticks unchanged', () => {
    assert.equal(scaleConstructionTicks(250, 'residential', { storage: fakeStorage() }), 250);
  });
});

describe('scaleConstructionTicks — ON scales per class', () => {
  test('a dwelling (residential ×0.1) is scaled down', () => {
    // 100 × 0.1 = 10
    assert.equal(scaleConstructionTicks(100, 'residential', { enabled: true }), 10);
    assert.equal(FAST_BUILD_CLASS_FACTORS.residential, 0.1);
  });
  test('a mega/utility facility (power ×0.05) is drastically reduced', () => {
    // 400 × 0.05 = 20  (weeks not years)
    assert.equal(scaleConstructionTicks(400, 'power', { enabled: true }), 20);
    assert.equal(FAST_BUILD_CLASS_FACTORS.power, 0.05);
  });
  test('an unlisted class falls through to the default factor', () => {
    // 'park' is not in the map → DEFAULT_FAST_BUILD_FACTOR (0.1): 100 → 10
    assert.equal(FAST_BUILD_CLASS_FACTORS.park, undefined);
    assert.equal(scaleConstructionTicks(100, 'park', { enabled: true }),
      Math.max(1, Math.round(100 * DEFAULT_FAST_BUILD_FACTOR)));
  });
  test('floor-at-1: never returns zero or negative ticks', () => {
    // 5 × 0.05 = 0.25 → round → 0 → floored to 1
    assert.equal(scaleConstructionTicks(5, 'power', { enabled: true }), 1);
    // 1 × 0.05 → 0 → floored to 1
    assert.equal(scaleConstructionTicks(1, 'health', { enabled: true }), 1);
  });
});

describe('constructionTicks() SSOT seam — real shipped function', () => {
  // constructionTicks reads only sp.cost + sp.kind. base = max(3, round(cost/1500)).
  const dwelling = { cost: 150000, kind: 'residential' }; // base = 100
  const mega = { cost: 900000, kind: 'power' }; // base = 600

  test('OFF (no localStorage): unchanged base is returned', () => {
    // No global localStorage in node → flag reads disabled → base unchanged.
    assert.equal(constructionTicks(dwelling), 100);
    assert.equal(constructionTicks(mega), 600);
  });

  test('ON (global localStorage flag): scaled per class', () => {
    const prior = globalThis.localStorage;
    globalThis.localStorage = fakeStorage({ [FAST_BUILD_FLAG_KEY]: '1' });
    try {
      assert.equal(constructionTicks(dwelling), 10); // 100 × 0.1
      assert.equal(constructionTicks(mega), 30); // 600 × 0.05
    } finally {
      if (prior === undefined) delete globalThis.localStorage;
      else globalThis.localStorage = prior;
    }
    // and after restore, back to unchanged
    assert.equal(constructionTicks(dwelling), 100);
  });
});
