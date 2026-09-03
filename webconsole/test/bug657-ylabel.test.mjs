// BUG-657 — yLabel()'s row-band clamp was a no-op: a misplaced parenthesis made
// `Math.min(band)` a single-argument identity and `Math.max(0, band, 25)` a
// FLOOR of 25, so every row in the map gutter rendered 'Z' and every
// coordLabel() the player ever saw ("grid Z,374") carried the same dead letter.
//
// These assertions fail against the pre-fix expression — verified by scratch-copy
// mutation (cp data.ts data.ts.bak; restore the `Math.min(...)` , 25)` misplacement;
// mv back), not by inspection: with the bug restored, EVERY case below except the
// final saturating one reports 'Z'.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { yLabel, coordLabel, ROW_BAND, MAP_H } from '../src/sim/data.ts';

test('BUG-657: row labels advance A, B, C... one letter per ROW_BAND rows', () => {
  assert.equal(yLabel(0), 'A');
  assert.equal(yLabel(ROW_BAND - 1), 'A', 'the last row of band 0 is still A');
  assert.equal(yLabel(ROW_BAND), 'B', 'the first row of band 1 is B');
  assert.equal(yLabel(2 * ROW_BAND), 'C');
  assert.equal(yLabel(5 * ROW_BAND), 'F');
});

test('BUG-657: distinct bands get DISTINCT letters (the defect made them all Z)', () => {
  const seen = new Set();
  for (let band = 0; band * ROW_BAND < MAP_H; band++) seen.add(yLabel(band * ROW_BAND));
  assert.ok(
    seen.size > 1,
    `every band collapsed to a single letter (${[...seen]}) — this is the BUG-657 defect`
  );
  // MAP_H 260 / ROW_BAND 10 = 26 bands, exactly A..Z with no collisions.
  assert.equal(seen.size, Math.ceil(MAP_H / ROW_BAND), 'every band has its own letter');
});

test('BUG-657: the clamp saturates at Z rather than running past the alphabet', () => {
  assert.equal(yLabel(25 * ROW_BAND), 'Z');
  assert.equal(yLabel(26 * ROW_BAND), 'Z', 'a band beyond 25 clamps to Z, never "["');
  assert.equal(yLabel(10_000), 'Z');
});

test('BUG-657: negative/degenerate y clamps to A, never below the alphabet', () => {
  assert.equal(yLabel(-1), 'A');
  assert.equal(yLabel(-10_000), 'A');
});

test('BUG-657: coordLabel carries the corrected letter, not a uniform Z', () => {
  assert.equal(coordLabel(373, 0), 'A,374');
  assert.equal(coordLabel(373, 5 * ROW_BAND), 'F,374');
  assert.notEqual(
    coordLabel(373, 0),
    coordLabel(373, 5 * ROW_BAND),
    'two different rows must not share a coordinate label'
  );
});
