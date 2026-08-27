// ref-ui.test.mjs — FEAT-1972079903: per-building reference-id overlay.
//
// The "Refs" toggle (map controls bar, default OFF) shows each building's `id`
// as its report ref. These tests pin the pure ref-label helper that the map
// overlay and the selected-building panel both render from:
//   - Refs ON  → label includes `#<id>`
//   - Refs OFF → no label
//   - the ref equals buildings[].id (so it matches the debug JSON)
//   - deterministic: no Date/random, same building → same text
//
// RED proof (scratch-copy, NEVER git): temporarily break buildingRef so the
// label drops the id → these go RED; restore the scratch copy.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildingRef, buildingRefLabel } from '../src/sim/refs.ts';

test('refs ON: label includes the building id as #<id>', () => {
  const b = { id: 44, spec: 'res_hut', x: 10, y: 3 };
  const label = buildingRefLabel(b, true);
  assert.equal(label, '#44');
  assert.ok(label.includes(String(b.id)), 'label must contain the raw id');
});

test('refs OFF: no label rendered', () => {
  const b = { id: 44, spec: 'res_hut', x: 10, y: 3 };
  assert.equal(buildingRefLabel(b, false), '');
});

test('the ref equals buildings[].id (matches the debug JSON)', () => {
  for (const id of [0, 1, 7, 44, 100003, 0x40000000 + 5]) {
    const b = { id };
    assert.equal(buildingRef(b), `#${id}`);
    // The token embeds exactly the id the debug capture reports.
    assert.equal(buildingRefLabel(b, true).slice(1), String(id));
  }
});

test('determinism: same building → identical ref text, no clock/random', () => {
  const b = { id: 44 };
  const first = buildingRefLabel(b, true);
  for (let i = 0; i < 50; i++) {
    assert.equal(buildingRefLabel(b, true), first);
  }
});

test('distinct ids produce distinct refs', () => {
  assert.notEqual(buildingRef({ id: 44 }), buildingRef({ id: 45 }));
});
