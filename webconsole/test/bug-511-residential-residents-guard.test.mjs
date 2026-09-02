// bug-511-residential-residents-guard.test.mjs — BUG-511 (P3): capacityAtTier's
// no-capacityTiers fallback is `sp.residents ?? sp.jobs ?? 0`. Every one of
// today's 10 residential specs defines `residents` (BUG-509's round proved
// capacityAtTier(sp,0) === sp.residents for all of them), so the fallback is
// harmless TODAY -- but a FUTURE residential spec added without `residents`
// would silently contribute 0 to the population ceiling with no error at all.
//
// THE FIX: assertResidentialSpecsHaveResidents(specs) (src/sim/data.ts) is run
// at catalogue-load time against the real SPECS table and throws the
// registry-sourced error MET-V852 the moment a 'residential' spec omits
// `residents`. This test proves:
//   1) the guard FIRES (throws MET-V852) against a synthetic spec missing
//      `residents` -- the RED proof: without the fix (guard absent/no-op)
//      this assertion fails, proving the test can fail;
//   2) it does NOT fire for a synthetic residential spec that legitimately
//      has `residents` (no false positive);
//   3) every CURRENT residential spec in the real SPECS table passes the
//      guard unchanged, and capacityAtTier(sp, 0) === sp.residents still
//      holds for all ten -- proving no current-city behaviour changed
//      (BUG-509's fix is undisturbed).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, capacityAtTier, assertResidentialSpecsHaveResidents } from '../src/sim/data.ts';

const RESIDENTIAL_IDS = [
  'res_hut',
  'res_block',
  'res_terrace',
  'res_lowrise',
  'res_midrise',
  'res_highrise',
  'res_penthouse',
  'res_estate_compact',
  'res_estate',
  'res_estate_sprawl',
];

test('BUG-511: RED proof -- assertResidentialSpecsHaveResidents THROWS MET-V852 for a residential spec missing `residents`', () => {
  const synthetic = {
    // A hypothetical future spec: residential, no capacityTiers, no residents.
    // This is EXACTLY the silent-zero trap BUG-511 describes -- capacityAtTier
    // would return `undefined ?? undefined ?? 0` = 0 for it.
    ghost_res: {
      id: 'ghost_res',
      kind: 'residential',
      name: 'Ghost Housing',
      blurb: '',
      w: 1,
      h: 1,
      cost: 0,
      upkeep: 0,
      color: '#000000',
      category: 'zones',
      unlock: 1,
      // residents deliberately omitted
    },
  };

  // Precondition: capacityAtTier really would silently resolve to 0 for this
  // spec if nothing guarded against it -- proves the trap this bug describes
  // is real, not hypothetical.
  assert.equal(capacityAtTier(synthetic.ghost_res, 0), 0, 'precondition: the silent-zero fallback is real for a residents-less spec');

  let thrown = null;
  try {
    assertResidentialSpecsHaveResidents(synthetic);
  } catch (err) {
    thrown = err;
  }

  assert.ok(thrown, 'the guard must THROW for a residential spec missing `residents` -- a silent pass here is the bug');
  assert.equal(thrown.code, 'MET-V852', 'the thrown error carries the registry-sourced MET-V852 code (GR#7)');
  assert.match(thrown.message, /ghost_res/, 'the error message names the offending spec id');
});

test('BUG-511: the guard does NOT fire for a synthetic residential spec that legitimately declares `residents` (no false positive)', () => {
  const synthetic = {
    fine_res: {
      id: 'fine_res',
      kind: 'residential',
      name: 'Fine Housing',
      blurb: '',
      w: 1,
      h: 1,
      cost: 0,
      upkeep: 0,
      color: '#000000',
      category: 'zones',
      unlock: 1,
      residents: 42,
    },
  };

  assert.doesNotThrow(
    () => assertResidentialSpecsHaveResidents(synthetic),
    'a residential spec that declares `residents` must pass the guard cleanly'
  );
});

test('BUG-511: the guard does NOT fire for non-residential specs missing `residents` (commercial/services legitimately have none)', () => {
  const synthetic = {
    com_no_residents: {
      id: 'com_no_residents',
      kind: 'commercial',
      name: 'Shop',
      blurb: '',
      w: 1,
      h: 1,
      cost: 0,
      upkeep: 0,
      color: '#000000',
      category: 'zones',
      unlock: 1,
      // no residents -- fine, this is not a residential spec
    },
  };

  assert.doesNotThrow(
    () => assertResidentialSpecsHaveResidents(synthetic),
    'a non-residential spec must never be required to declare `residents`'
  );
});

test('BUG-511: every CURRENT residential spec passes the guard unchanged (no current-city behaviour change)', () => {
  // Re-running the guard against the real, already-loaded SPECS table must be
  // a no-op: this module already ran it once at import time (module-load-time
  // enforcement), so a second run proves the table is still clean and that
  // nothing about calling the guard mutates or disturbs SPECS.
  assert.doesNotThrow(() => assertResidentialSpecsHaveResidents(SPECS), 'the real SPECS catalogue passes the guard cleanly today');

  assert.equal(RESIDENTIAL_IDS.length, 10, 'precondition: this is the full current residential roster (BUG-509 proof set)');
  for (const id of RESIDENTIAL_IDS) {
    const sp = SPECS[id];
    assert.ok(sp, `precondition: ${id} exists in SPECS`);
    assert.equal(sp.kind, 'residential', `precondition: ${id} is kind 'residential'`);
    assert.ok(sp.residents != null, `${id} must declare 'residents' (this is what the guard enforces)`);
    assert.equal(
      capacityAtTier(sp, 0),
      sp.residents,
      `capacityAtTier(${id}, 0) must still equal sp.residents -- BUG-509's tier-0-equals-flat-base invariant is undisturbed`
    );
  }
});
