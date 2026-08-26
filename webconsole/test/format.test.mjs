// format.test.mjs — FEAT-1972079879: the ONE shared number/money formatter.
//
// Run with `npm test` (node --test). Node's native type-stripping lets this
// .mjs import the real TypeScript helper directly, so the test exercises the
// exact code the UI ships — no reimplementation, no drift.
//
// These assertions FAIL if the helper stops adding thousands separators, drops
// the £ prefix, or mishandles the sign/zero/non-finite cases — i.e. they can go
// red (proven by mutating fmtNum to `String(n)` or fmtMoney's '£' to '¤').

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fmtNum, fmtMoney, fmtSigned } from '../src/sim/utils.ts';

test('fmtNum inserts thousands separators', () => {
  assert.equal(fmtNum(33000000), '33,000,000');
  assert.equal(fmtNum(1000), '1,000');
  assert.equal(fmtNum(999), '999'); // below 1,000 → no separator
  assert.equal(fmtNum(0), '0');
});

test('fmtNum handles negatives and rounds', () => {
  assert.equal(fmtNum(-1234567), '-1,234,567');
  assert.equal(fmtNum(1234.6), '1,235');
});

test('fmtNum degrades non-finite input to "0" (no leaked NaN/Infinity)', () => {
  assert.equal(fmtNum(NaN), '0');
  assert.equal(fmtNum(Infinity), '0');
});

test('fmtMoney prefixes £ and uses commas', () => {
  assert.equal(fmtMoney(33000000), '£33,000,000');
  assert.equal(fmtMoney(0), '£0');
  assert.equal(fmtMoney(2), '£2');
  // sign sits before the symbol, magnitude keeps separators
  assert.equal(fmtMoney(-4200), '-£4,200');
});

test('fmtMoney never emits the old ¤ placeholder glyph', () => {
  assert.ok(!fmtMoney(1000).includes('¤'));
  assert.ok(fmtMoney(1000).startsWith('£'));
});

test('fmtSigned always shows an explicit +£ / -£', () => {
  assert.equal(fmtSigned(1500), '+£1,500');
  assert.equal(fmtSigned(-340), '-£340');
  assert.equal(fmtSigned(0), '+£0');
});
