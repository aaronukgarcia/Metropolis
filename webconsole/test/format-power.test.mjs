// format-power.test.mjs — FEAT-1972079893: auto-scale power units (MW→GW→TW).
//
// Run with `npm test` (node --test). Node's native type-stripping lets this
// .mjs import the real TypeScript helper directly, so the test exercises the
// exact code the UI ships — no reimplementation, no drift.
//
// These assertions FAIL if the scaling rules break, trailing .0 is not stripped,
// negatives lose their sign, or non-finite values are leaked as "NaN"/"Infinity".

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatPower } from '../src/sim/utils.ts';

test('formatPower: sub-1000 MW stays as MW', () => {
  assert.equal(formatPower(0), '0 MW');
  assert.equal(formatPower(1), '1 MW');
  assert.equal(formatPower(500), '500 MW');
  assert.equal(formatPower(950), '950 MW');
  assert.equal(formatPower(999), '999 MW');
});

test('formatPower: exact 1000 MW → 1 GW (no trailing .0)', () => {
  assert.equal(formatPower(1000), '1 GW');
});

test('formatPower: fractional GW in the 1k–1M range', () => {
  assert.equal(formatPower(1500), '1.5 GW');
  assert.equal(formatPower(2000), '2 GW');
  assert.equal(formatPower(2200), '2.2 GW');
  assert.equal(formatPower(17300), '17.3 GW');
  assert.equal(formatPower(999999), '1000 GW'); // edge case: rounding up
  assert.equal(formatPower(10000), '10 GW');
});

test('formatPower: TW for 1M and above', () => {
  assert.equal(formatPower(1000000), '1 TW');
  assert.equal(formatPower(1500000), '1.5 TW');
  assert.equal(formatPower(2000000), '2 TW');
  assert.equal(formatPower(5500000), '5.5 TW');
});

test('formatPower: negative values keep the sign', () => {
  assert.equal(formatPower(-500), '-500 MW');
  assert.equal(formatPower(-1500), '-1.5 GW');
  assert.equal(formatPower(-1000), '-1 GW');
  assert.equal(formatPower(-2000000), '-2 TW');
});

test('formatPower: trailing .0 is stripped', () => {
  assert.equal(formatPower(1000), '1 GW'); // not "1.0 GW"
  assert.equal(formatPower(2000), '2 GW');
  assert.equal(formatPower(1000000), '1 TW'); // not "1.0 TW"
  assert.equal(formatPower(3000), '3 GW');
});

test('formatPower: one decimal place in GW/TW (not more)', () => {
  // The function rounds to one decimal and strips .0
  assert.equal(formatPower(1234), '1.2 GW');
  assert.equal(formatPower(1567), '1.6 GW');
  assert.equal(formatPower(1234567), '1.2 TW');
});

test('formatPower: non-finite input degrades to "0 MW"', () => {
  assert.equal(formatPower(NaN), '0 MW');
  assert.equal(formatPower(Infinity), '0 MW');
  assert.equal(formatPower(-Infinity), '0 MW');
});

test('formatPower: boundary values', () => {
  // Just below and above the 1k and 1M thresholds
  assert.equal(formatPower(999), '999 MW');
  assert.equal(formatPower(1000), '1 GW');
  assert.equal(formatPower(999999), '1000 GW');
  assert.equal(formatPower(1000000), '1 TW');
});
