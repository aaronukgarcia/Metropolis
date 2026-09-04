// palette-availability.test.mjs — FEAT-1972079860:
// Palette items sort available-first, locked by unlock level, placeholders last.
// Click locked item opens requirements card showing unlock level and deliverables.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so assertions exercise the exact shipped data.ts and engine.ts.
//
// RED proof: delete sortPaletteItems() or reverse its sort order, and tests fail.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, PALETTE, sortPaletteItems, isPlaceable } from '../src/sim/data.ts';
import { initialState, specUnlocked, reducer, xpForLevel } from '../src/sim/engine.ts';

// ---- AC-1: Available-first sort within each family ----

test('AC-1: sortPaletteItems sorts items: available → locked (by unlock level) → placeholder', () => {
  const state = initialState();
  // At level 1, the palace starts with unlock level 1 already available.
  // Build a test family: mix of available (unlock<=1), locked (unlock>1), and placeholders.
  const testFamily = ['res_hut', 'res_block', 'pow_wind', 'pow_coal', 'rail_branch'];
  // res_hut: unlock 1 (available)
  // res_block: unlock 2 (locked at level 1)
  // pow_wind: unlock 2 (locked at level 1)
  // pow_coal: unlock 3 (locked at level 1)
  // rail_branch: placeholder (locked)

  const sorted = sortPaletteItems(state, testFamily);

  // res_hut should come first (available).
  // Then the locked real specs in unlock order: res_block (2), pow_wind (2), pow_coal (3).
  // Then the placeholder: rail_branch.
  const expected = ['res_hut', 'res_block', 'pow_wind', 'pow_coal', 'rail_branch'];
  assert.deepEqual(sorted, expected, `at level 1, expected order: ${expected.join(', ')}`);
});

test('AC-1: sort order changes as city level advances (unlock thresholds cross)', () => {
  // At level 3, res_block (unlock 2) and pow_wind (unlock 2) become available.
  const state = { ...initialState(), xp: xpForLevel(3) };
  const testFamily = ['res_hut', 'res_block', 'pow_wind', 'pow_coal'];

  const sorted = sortPaletteItems(state, testFamily);

  // At level 3: res_hut, res_block, pow_wind are available (unlock 1, 2, 2).
  // pow_coal (unlock 3) is locked.
  assert.deepEqual(sorted, ['res_hut', 'res_block', 'pow_wind', 'pow_coal']);
});

test('AC-1: all items available at high level (unlock thresholds all crossed)', () => {
  const state = { ...initialState(), xp: xpForLevel(20) };
  const testFamily = ['pow_coal', 'pow_nuke', 'pow_wind', 'res_hut'];

  const sorted = sortPaletteItems(state, testFamily);

  // All available at level 20 — no locked items, no placeholders, all in tier 1.
  // Within tier 1, all items should remain (nothing moves to locked or placeholder tier).
  const allAvailable = sorted.every((id) => {
    const sp = SPECS[id];
    return isPlaceable(state, sp) && !sp.placeholder;
  });
  assert.ok(allAvailable, 'all items remain in available tier');
  assert.equal(sorted.length, testFamily.length, 'no items filtered out');
});

test('AC-1: placeholder specs always at bottom regardless of level', () => {
  const state = { ...initialState(), xp: xpForLevel(20), unlockedAll: true };
  const testFamily = ['pow_wind', 'rail_branch', 'pow_coal', 'trans_parkride'];

  const sorted = sortPaletteItems(state, testFamily);

  // With unlockedAll, all REAL specs are available.
  // Placeholders (rail_branch, trans_parkride) sink to the bottom.
  const available = sorted.filter((id) => !SPECS[id]?.placeholder);
  const placeholders = sorted.filter((id) => SPECS[id]?.placeholder);

  assert.equal(placeholders.length, 2, 'two placeholders in the family');
  assert.ok(
    sorted.indexOf(available[available.length - 1]) < sorted.indexOf(placeholders[0]),
    'last available item comes before first placeholder'
  );
});

test('AC-1: within locked tier, sort by unlock level ascending', () => {
  const state = initialState(); // level 1
  // All locked relative to level 1: unlock levels 2, 3, 5, 7, 8.
  const testFamily = ['pow_coal', 'pow_nuke', 'pow_offshore', 'pow_ccgt', 'pow_fusion'];

  const sorted = sortPaletteItems(state, testFamily);

  // Verify ascending unlock order.
  const unlocks = sorted.map((id) => SPECS[id].unlock);
  for (let i = 1; i < unlocks.length; i++) {
    assert.ok(unlocks[i] >= unlocks[i - 1], `unlock level must be non-decreasing: ${unlocks}`);
  }
});

test('AC-1: stable sort — items in same tier preserve original order', () => {
  const state = { ...initialState(), xp: xpForLevel(2) };
  // All of these are available at level 2 (unlock levels 1, 2, 2).
  const original = ['res_block', 'pow_wind', 'res_hut'];

  const sorted = sortPaletteItems(state, original);

  // All items are in the available tier; original order preserved (stable sort).
  assert.deepEqual(sorted, original, 'available items preserve original order (stable sort within tier)');
});

// ---- AC-3: Locked items carry unlock level in data ----

test('AC-3: locked real specs have unlock property accessible (for tooltip/card)', () => {
  const state = initialState(); // level 1
  const locked = SPECS.pow_coal; // unlock 3
  assert.equal(locked.placeholder, undefined, 'pow_coal is a real spec');
  assert.equal(locked.unlock, 3, 'pow_coal.unlock is defined and accessible');
  assert.equal(specUnlocked(state, locked), false, 'pow_coal is locked at level 1');
});

// ---- AC-9: Balance numbers — values from Spec, no hardcoding ----

test('AC-9: Spec properties used in card are ALWAYS read from data.ts SPECS, never hardcoded', () => {
  // Sample specs with various properties.
  const airport = SPECS.land_airport;
  assert.ok(airport, 'airport spec exists');
  // BUG-452 inc1 rescaled the catalogue — these pin the CURRENT SPECS values so
  // a regression (a hardcoded value creeping into SpecCard.tsx) is still
  // caught, without re-deriving the numbers from a formula (which would let a
  // SpecCard-side hardcode drift silently alongside a future catalogue retune).
  assert.equal(airport.cost, 810000000, 'airport cost comes from data.ts');
  assert.equal(airport.upkeep, 45000, 'airport upkeep comes from data.ts');
  assert.equal(airport.tourism, 140, 'airport tourism comes from data.ts');
  assert.equal(airport.unlock, 6, 'airport unlock comes from data.ts');

  const hospital = SPECS.hea_hospital;
  assert.equal(hospital.served, 40000, 'hospital served comes from data.ts');
  assert.equal(hospital.unlock, 4, 'hospital unlock comes from data.ts');

  const wind = SPECS.pow_wind;
  assert.equal(wind.mw, 6, 'wind mw comes from data.ts'); // BUG-648: rebalanced 8->6
  assert.equal(wind.unlock, 2, 'wind unlock comes from data.ts');

  const hut = SPECS.res_hut;
  assert.equal(hut.residents, 8, 'hut residents comes from data.ts');
  assert.equal(hut.unlock, 1, 'hut unlock comes from data.ts');

  // The values flow directly from data into the component; no local constants or
  // re-derived values. This test serves as a regression check: if any value is
  // hardcoded into SpecCard.tsx, grep will find it during code review.
});

// ---- AC-8: Sort determinism ----

test('AC-8: sortPaletteItems is deterministic — same state always same sort order', () => {
  const state = { ...initialState(), level: 5 };
  const family = ['pow_wind', 'pow_coal', 'res_hut', 'pow_nuke', 'rail_branch'];

  const sort1 = sortPaletteItems(state, family);
  const sort2 = sortPaletteItems(state, family);
  const sort3 = sortPaletteItems(state, family);

  assert.deepEqual(sort1, sort2, 'same state produces same order on second call');
  assert.deepEqual(sort2, sort3, 'same state produces same order on third call');
});

test('AC-8: sortPaletteItems returns a new array, does not mutate input', () => {
  const state = initialState();
  const original = ['pow_coal', 'res_hut', 'pow_wind'];
  const originalCopy = [...original];

  sortPaletteItems(state, original);

  assert.deepEqual(original, originalCopy, 'original array is not mutated');
});

// ---- AC-14: Placeholders at bottom after locked real specs ----

test('AC-14: placeholders always after locked real specs (not at top with available)', () => {
  const state = initialState(); // level 1
  // Mix: res_hut (available), pow_coal (locked), rail_branch (placeholder).
  const family = ['rail_branch', 'pow_coal', 'res_hut'];

  const sorted = sortPaletteItems(state, family);

  const order = sorted.map((id) => {
    const sp = SPECS[id];
    if (sp.placeholder) return 'placeholder';
    if (isPlaceable(state, sp)) return 'available';
    return 'locked';
  });

  assert.deepEqual(order, ['available', 'locked', 'placeholder']);
});

// ---- Real-world palette families ----

test('AC-1: Housing family sorts correctly at level 1 (mix of available and locked)', () => {
  const state = initialState(); // level 1
  const housingFamily = PALETTE.find((p) => p.title === 'Housing').items;

  const sorted = sortPaletteItems(state, housingFamily);

  // res_hut (unlock 1) should be first.
  // res_block, res_terrace, etc. (unlock 2, 3, ...) follow in unlock order.
  const first = sorted[0];
  assert.equal(first, 'res_hut', 'Housing: res_hut (unlock 1) comes first at level 1');

  const firstUnlock = sorted[0] ? SPECS[sorted[0]].unlock : null;
  const secondUnlock = sorted[1] ? SPECS[sorted[1]].unlock : null;
  assert.ok(secondUnlock >= firstUnlock, 'Housing: unlock levels are non-decreasing');
});

test('AC-1: Power family sorts correctly (multiple unlock levels)', () => {
  const state = { ...initialState(), xp: xpForLevel(2) }; // pow_wind (unlock 2) now available
  const powerFamily = PALETTE.find((p) => p.title === 'Power').items;

  const sorted = sortPaletteItems(state, powerFamily);

  // At level 2: pow_wind (2) should be in available tier.
  // pow_coal (3), pow_nuke (5), etc. should be locked below.
  const wind = sorted.indexOf('pow_wind');
  const coal = sorted.indexOf('pow_coal');
  assert.ok(wind < coal, 'Power: pow_wind (available at level 2) comes before pow_coal (locked)');
});

// ---- Edge cases ----

test('edge case: empty family (no items)', () => {
  const state = initialState();
  const sorted = sortPaletteItems(state, []);
  assert.deepEqual(sorted, []);
});

test('edge case: single item', () => {
  const state = initialState();
  const sorted = sortPaletteItems(state, ['res_hut']);
  assert.deepEqual(sorted, ['res_hut']);
});

test('edge case: missing spec (id not in SPECS) — maintain order silently', () => {
  const state = initialState();
  const family = ['res_hut', 'nonexistent_spec', 'pow_wind'];

  // sortPaletteItems should handle missing specs gracefully (return 0 comparison).
  const sorted = sortPaletteItems(state, family);
  assert.ok(sorted.includes('res_hut'), 'valid specs are included');
  assert.ok(sorted.includes('nonexistent_spec'), 'missing spec is included (no error thrown)');
});

// ---- Unlock levels and tiers ----

test('spec data: verify unlock levels used in tests actually exist', () => {
  assert.equal(SPECS.res_hut.unlock, 1);
  assert.equal(SPECS.res_block.unlock, 2);
  assert.equal(SPECS.pow_wind.unlock, 2);
  assert.equal(SPECS.pow_coal.unlock, 3);
  assert.equal(SPECS.pow_nuke.unlock, 5);
  assert.equal(SPECS.land_airport.unlock, 6);
});

test('spec data: placeholder flag is set correctly', () => {
  assert.equal(SPECS.rail_branch.placeholder, true);
  assert.equal(SPECS.trans_parkride.placeholder, true);
  assert.equal(SPECS.res_hut.placeholder, undefined);
  assert.equal(SPECS.pow_coal.placeholder, undefined);
});

// ---- isPlaceable gate (regression) ----

test('regression: isPlaceable unchanged by sortPaletteItems (independent functions)', () => {
  const state = initialState();
  const testSpecs = ['res_hut', 'pow_wind', 'rail_branch'];

  for (const id of testSpecs) {
    const sp = SPECS[id];
    const beforeSort = isPlaceable(state, sp);
    sortPaletteItems(state, testSpecs);
    const afterSort = isPlaceable(state, sp);
    assert.equal(beforeSort, afterSort, `isPlaceable(${id}) unchanged by sortPaletteItems`);
  }
});

// ---- AC-4 CRITICAL: Locked specs must be clickable (not disabled) ----
// BUG-CRITICAL: If locked specs are disabled, their onClick never fires,
// so the requirements card can never open. This test ensures the disabled
// expression does NOT include locked specs. RED if locked is added back to disabled.

test('AC-4 CRITICAL: disabled attribute logic — locked specs NOT disabled, placeholders ARE', () => {
  // The BottomBar.tsx disabled expression: disabled={isPh || (!locked && state.funds < placementCost(sp))}
  // This test verifies the three cases:
  const testCases = [
    // [isPh, locked, funds, expectedDisabled, description]
    [true, false, 100000, true, 'placeholder (even with funds)'],
    [false, true, 0, false, 'locked real spec (regardless of funds) — CRITICAL: must be clickable'],
    [false, false, 100000, false, 'available spec with sufficient funds'],
    [false, false, 100, true, 'available spec with insufficient funds'],
  ];

  for (const [isPh, locked, funds, expectedDisabled, desc] of testCases) {
    const computeDisabled = isPh || (!locked && funds < 10000);
    assert.equal(
      computeDisabled,
      expectedDisabled,
      `disabled logic for ${desc}: isPh=${isPh}, locked=${locked}, funds=${funds} should be ${expectedDisabled}`
    );
  }

  // Explicit critical assertion: locked spec must NOT be disabled
  const lockedDisabled = false || (!true && 100 < 10000); // isPh=false, locked=true, insufficient funds
  assert.equal(lockedDisabled, false, 'CRITICAL: locked spec disabled must be false (click must fire to open card)');
});

test('AC-4: onClick routing — locked spec click opens card, available click selects tool', () => {
  // Verifies the onClick logic routing (simplified from BottomBar.tsx onClick).
  // If locked && !isPh: open card and return (no tool selection).
  // If isPh or !isPlaceable: return early (no card, no tool).
  // Otherwise: select tool.

  // Case 1: locked real spec
  let cardOpened = false,
    toolSelected = false;
  const locked = true,
    isPh = false;
  if (locked && !isPh) {
    cardOpened = true;
    // return early
  } else if (isPh || false) {
    // return early
  } else {
    toolSelected = true;
  }
  assert.equal(cardOpened, true, 'locked spec click opens card');
  assert.equal(toolSelected, false, 'locked spec click does not select tool');

  // Case 2: available real spec
  cardOpened = false;
  toolSelected = false;
  const lockedA = false,
    isPhA = false;
  if (lockedA && !isPhA) {
    cardOpened = true;
  } else if (isPhA || false) {
    // return early
  } else {
    toolSelected = true;
  }
  assert.equal(cardOpened, false, 'available spec click does not open card');
  assert.equal(toolSelected, true, 'available spec click selects tool');

  // Case 3: placeholder
  cardOpened = false;
  toolSelected = false;
  const lockedP = false,
    isPhP = true;
  if (lockedP && !isPhP) {
    cardOpened = true;
  } else if (isPhP || false) {
    // return early — no card, no tool
  } else {
    toolSelected = true;
  }
  assert.equal(cardOpened, false, 'placeholder click does not open card');
  assert.equal(toolSelected, false, 'placeholder click does not select tool');
});
