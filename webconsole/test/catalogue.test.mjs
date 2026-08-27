// catalogue.test.mjs — FEAT-1972079877: placeholder object catalogue integrity.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript data module, so these assertions exercise the exact shipped
// catalogue — no copy, no drift.
//
// The BUG-385 class (a palette id with no SPECS entry silently renders a
// broken/skipped tile) stays closed here: every check below can go RED by
// mutating the data (add 'ghost_id' to a PALETTE family; duplicate an id into
// two families; set a spec's w to 0 or unlock to 21; delete a level's only
// unlock).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  PALETTE,
  PALETTE_FLAT,
  FAMILIES,
  countByKind,
  unlockedAtLevel,
} from '../src/sim/data.ts';

const HEX = /^#[0-9a-fA-F]{6}$/;
const MAX_LEVEL = 20;
const SEED_UNLOCK = 99; // sentinel: pre-placed seed infrastructure, never in the palette

test('every PALETTE id resolves to a SPECS entry (BUG-385 dangling-id class)', () => {
  for (const fam of PALETTE) {
    for (const id of fam.items) {
      assert.ok(SPECS[id], `palette family "${fam.title}" references missing spec "${id}"`);
    }
  }
});

test('every unlockable spec appears in EXACTLY ONE palette family; seed (99) specs in none', () => {
  const where = new Map(); // spec id -> [family titles]
  for (const fam of PALETTE) {
    for (const id of fam.items) {
      where.set(id, [...(where.get(id) ?? []), fam.title]);
    }
  }
  for (const [id, sp] of Object.entries(SPECS)) {
    const fams = where.get(id) ?? [];
    if (sp.unlock === SEED_UNLOCK) {
      assert.equal(fams.length, 0, `seed spec "${id}" must not be placeable via the palette (found in ${fams})`);
    } else {
      assert.equal(fams.length, 1, `spec "${id}" must appear in exactly one family, found in [${fams}]`);
    }
  }
  // and no palette id points at nothing / no duplicates hide in the flat list
  assert.equal(new Set(PALETTE_FLAT).size, PALETTE_FLAT.length, 'duplicate id across families');
});

test('spec integrity: footprint, cost, upkeep, colour, unlock, naming', () => {
  for (const [id, sp] of Object.entries(SPECS)) {
    assert.equal(sp.id, id, `spec key "${id}" disagrees with its id field "${sp.id}"`);
    assert.ok(Number.isInteger(sp.w) && sp.w >= 1, `${id}: width must be a positive integer`);
    assert.ok(Number.isInteger(sp.h) && sp.h >= 1, `${id}: height must be a positive integer`);
    assert.ok(Number.isFinite(sp.cost) && sp.cost >= 0, `${id}: cost must be finite and >= 0`);
    assert.ok(Number.isFinite(sp.upkeep) && sp.upkeep >= 0, `${id}: upkeep must be finite and >= 0`);
    assert.match(sp.color, HEX, `${id}: colour must be a 6-digit hex`);
    assert.ok(sp.name.trim().length > 0, `${id}: name must be non-empty`);
    assert.ok(
      (sp.unlock >= 1 && sp.unlock <= MAX_LEVEL && Number.isInteger(sp.unlock)) || sp.unlock === SEED_UNLOCK,
      `${id}: unlock ${sp.unlock} outside 1..${MAX_LEVEL} and not the ${SEED_UNLOCK} seed sentinel`
    );
    assert.ok(['network', 'zones', 'services'].includes(sp.category), `${id}: unknown category ${sp.category}`);
    // A blurb is what the palette shows under the name — only bare network
    // strips (roads/rails placed as lines) may omit it.
    if (sp.category !== 'network') {
      assert.ok(sp.blurb.trim().length > 0, `${id}: non-network spec needs a blurb`);
    }
  }
});

test('unlock ladder: every city level 1..20 unlocks at least one structure', () => {
  for (let lv = 1; lv <= MAX_LEVEL; lv++) {
    const names = unlockedAtLevel(lv);
    assert.ok(names.length >= 1, `level ${lv} unlocks nothing — the level ladder has a dead rung`);
  }
});

test('countByKind covers every kind in the catalogue (no NaN counts for new kinds)', () => {
  // One building per spec: the per-kind sum must equal the number of specs.
  const buildings = Object.keys(SPECS).map((spec, i) => ({ id: i + 1, spec, x: 0, y: 0 }));
  const c = countByKind(buildings);
  let sum = 0;
  for (const [kind, n] of Object.entries(c)) {
    assert.ok(Number.isFinite(n), `count for kind "${kind}" is not finite — kind missing from ZERO_COUNTS`);
    sum += n;
  }
  assert.equal(sum, buildings.length, 'per-kind counts must sum to the number of placed buildings');
});

test('every placeable kind has a FAMILIES legend row (status table / map legend)', () => {
  const legend = new Set(FAMILIES.map((f) => f.kind));
  for (const sp of Object.values(SPECS)) {
    if (sp.category === 'network') continue; // rails/motorway seeds render as strips
    assert.ok(legend.has(sp.kind), `kind "${sp.kind}" (spec ${sp.id}) missing from FAMILIES legend`);
  }
});

test('edu_city blurb matches School (5–15) demand meter, no 16 overlap with college', () => {
  assert.ok(SPECS.edu_city.blurb.includes('5–15'), `edu_city blurb must include 5–15, got "${SPECS.edu_city.blurb}"`);
  assert.ok(!SPECS.edu_city.blurb.includes('5–16'), `edu_city blurb must not include 5–16, got "${SPECS.edu_city.blurb}"`);
  assert.ok(SPECS.edu_primary.blurb.includes('5–11'), `edu_primary blurb must stay 5–11, got "${SPECS.edu_primary.blurb}"`);
  assert.ok(SPECS.edu_nursery.blurb.includes('0–4'), `edu_nursery blurb must stay 0–4, got "${SPECS.edu_nursery.blurb}"`);
  assert.ok(SPECS.col_sixth.blurb.includes('16–19'), `col_sixth blurb must stay 16–19, got "${SPECS.col_sixth.blurb}"`);
});

test('palette families look POPULATED: the big families carry realistic counts', () => {
  const byTitle = new Map(PALETTE.map((f) => [f.title, f.items.length]));
  // Representative floor counts — go red if a family is gutted back to a stub.
  for (const [title, min] of [
    ['Transport', 8],
    ['Housing', 6],
    ['Industry & Farms', 8],
    ['Leisure', 5],
    ['Power', 8],
    ['Landmarks', 6],
  ]) {
    assert.ok(
      (byTitle.get(title) ?? 0) >= min,
      `family "${title}" has ${byTitle.get(title) ?? 0} entries, expected at least ${min}`
    );
  }
});
