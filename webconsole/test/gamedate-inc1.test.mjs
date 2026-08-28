// gamedate-inc1.test.mjs — BUG-401: gameDate off-by-one at month boundaries.
//
// Run with `npm test` (node --test). Node's native type-stripping lets this
// .mjs import the real TypeScript helper directly, so the test exercises the
// exact code the UI ships — no reimplementation, no drift.
//
// The bug: the old arithmetic used a 1-based day-of-year
//   const day = (tick % 360) + 1;            // 1..360
//   const month = Math.floor(day / 30) + 1;  // day 30 -> M2, day 360 -> M13 (impossible)
// which rolled the month a day early and could emit an impossible M13.
// The fix uses a zero-based day-of-year (utils.gameDate, the SSOT):
//   const dayOfYear = tick % 360;                   // 0..359
//   const month = Math.floor(dayOfYear / 30) + 1;   // 1..12
//   const day = (dayOfYear % 30) + 1;               // 1..30
//
// These assertions FAIL against the OLD formula: e.g. it produced 'Y1 D30·M2'
// at tick 29 and 'Y1 D30·M13' at tick 359. Determinism (GR#21): pure function
// of tick, no Date.now/Math.random.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { gameDate } from '../src/sim/utils.ts';

test('BUG-401: boundary ticks map to the correct D·M·Y (no early month roll)', () => {
  assert.equal(gameDate(0), 'Y1 D1·M1');    // first day of year 1
  assert.equal(gameDate(29), 'Y1 D30·M1');  // last day of M1 (old bug said M2)
  assert.equal(gameDate(30), 'Y1 D1·M2');   // first day of M2
  assert.equal(gameDate(359), 'Y1 D30·M12'); // last day of year (old bug said M13)
  assert.equal(gameDate(360), 'Y2 D1·M1');  // rolls into year 2
});

test('BUG-401: no impossible month/day over three full years (0..1080)', () => {
  const re = /^Y(\d+) D(\d+)·M(\d+)$/;
  for (let tick = 0; tick <= 1080; tick++) {
    const label = gameDate(tick);
    const m = re.exec(label);
    assert.ok(m, `tick ${tick} produced unparseable label ${label}`);
    const year = Number(m[1]);
    const day = Number(m[2]);
    const month = Number(m[3]);
    assert.ok(month >= 1 && month <= 12, `tick ${tick}: month ${month} out of 1..12 (${label})`);
    assert.ok(day >= 1 && day <= 30, `tick ${tick}: day ${day} out of 1..30 (${label})`);
    assert.ok(year >= 1, `tick ${tick}: year ${year} < 1 (${label})`);
  }
});

test('BUG-401: gameDate is deterministic (same tick → same string)', () => {
  for (const tick of [0, 29, 30, 359, 360, 1080, 53138]) {
    assert.equal(gameDate(tick), gameDate(tick), `tick ${tick} not stable`);
  }
});
