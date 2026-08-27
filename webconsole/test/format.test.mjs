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
import { fmtNum, fmtMoney, fmtMoneyEach, fmtSigned, gameDate } from '../src/sim/utils.ts';

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

test('gameDate maps ticks onto 360-day years and 30-day months 1..12', () => {
  assert.equal(gameDate(0), 'Y1 D1·M1');
  assert.equal(gameDate(29), 'Y1 D30·M1');
  assert.equal(gameDate(30), 'Y1 D1·M2');
  assert.equal(gameDate(359), 'Y1 D30·M12');
  assert.equal(gameDate(360), 'Y2 D1·M1');
  assert.equal(gameDate(53138), 'Y148 D9·M8');
});

test('fmtMoneyEach shows two decimals for sub-pound amounts', () => {
  assert.equal(fmtMoneyEach(0.12), '£0.12');
  assert.equal(fmtMoneyEach(0), '£0');
  const tourismEach = fmtMoneyEach(188551 / 314252);
  assert.notEqual(tourismEach, '£0');
  assert.notEqual(tourismEach, '£1');
  assert.equal(fmtMoneyEach(12), '£12');
});

test('fmtMoney stays integer for sub-pound funds', () => {
  assert.equal(fmtMoney(0.12), '£0');
});
