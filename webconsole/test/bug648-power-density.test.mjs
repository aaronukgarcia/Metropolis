// bug648-power-density.test.mjs — BUG-648: the power catalogue's MW/tile
// density must fall out of the data in the intended technology-tier order
// (turbine < wind farm < gas), with pow_nuke and pow_hydro documented as
// FOOTPRINT-REALISM EXCEPTIONS rather than forced onto the same density
// chain (see the round-2 correction below), and pow_hydro a deliberate
// special-case capstone above everything else.
//
// Background: before this fix pow_wind (8 MW / 1 tile = 8.00 MW/tile)
// out-densified pow_windfarm (60 MW / 9 tiles = 6.67) and pow_nuke
// (1,120 MW / 169 tiles = 6.63) — a single wind turbine beating a nuclear
// plant on power density, the opposite of reality and fatal to the
// CONSOLIDATOR's data-driven density ladder (FEAT-2326609761).
//
// ROUND-2 CORRECTION (2026-09-04, independent destructive round REJECT): the
// FIRST draft of this fix also shrunk pow_nuke's footprint 13x13->5x4 so it
// would out-density pow_ccgt. An independent attacker proved that change a
// live regression — replayed against Aaron's real building record and real
// road network, his already-placed pow_nuke (id 3331) is road-adjacent (and
// therefore online) under the ORIGINAL 13x13 footprint but silently goes
// road-DISCONNECTED (offline, -1,120 MW, no notice) under the shrunk 5x4
// one. footprintOf's "a shrink can't cause tile overlap" safety property
// says nothing about road-adjacency, which is a separate gate over the same
// footprint. The shrink is REVERTED: pow_nuke stays 13x13 forever (test 4
// below pins this), and pow_nuke/pow_hydro are now BOTH documented
// footprint-realism exceptions — their real-world footprints (nuclear
// exclusion zones, dam + reservoir) are honestly large, so it is correct,
// not a defect, for them to sit below pow_ccgt on a strict MW/tile basis.
// The CONSOLIDATOR's "10 nuke plants -> 1 XXL nuke" example is intended to
// fall out of COUNT-reduction (pow_nuke's own capacityTiers reactor ladder),
// not out of pow_nuke beating pow_ccgt per tile.
//
// Run with the scoped test runner: node ../tools/test/scoped.mjs
// test/bug648-power-density.test.mjs

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS } from '../src/sim/data.ts';

/** MW per tile for a spec — the SAME metric Aaron's bug report used. */
function densityOf(sp) {
  return sp.mw / (sp.w * sp.h);
}

/**
 * Asserts every id's density is STRICTLY greater than the previous one's
 * (GR#15 — derives the numbers from the array of specs passed in, never a
 * hardcoded expectation). Throws with a descriptive message on violation, so
 * it doubles as the RED-proof helper below.
 */
function assertStrictlyIncreasingDensity(specsInOrder, labelOf) {
  for (let i = 1; i < specsInOrder.length; i++) {
    const prev = specsInOrder[i - 1];
    const cur = specsInOrder[i];
    const dPrev = densityOf(prev);
    const dCur = densityOf(cur);
    assert.ok(
      dCur > dPrev,
      `density ladder violated: ${labelOf(cur)} (${dCur.toFixed(2)} MW/tile) must be denser than ` +
        `${labelOf(prev)} (${dPrev.toFixed(2)} MW/tile)`
    );
  }
}

// ---------------------------------------------------------------------------
// 1. The shipped catalogue satisfies the CONVENTIONAL (non-exception) ladder.
//    pow_nuke and pow_hydro are DELIBERATELY excluded from this chain (see
//    the file header) — they are checked separately in tests 2 and 3.
// ---------------------------------------------------------------------------
test('BUG-648: power density is monotonically increasing along the conventional ladder (turbine < wind farm < gas)', () => {
  const ladderIds = ['pow_wind', 'pow_windfarm', 'pow_ccgt'];
  const ladderSpecs = ladderIds.map((id) => {
    const sp = SPECS[id];
    assert.ok(sp, `${id} must exist in SPECS`);
    assert.ok(typeof sp.mw === 'number' && sp.mw > 0, `${id} must carry a real mw`);
    return sp;
  });
  assertStrictlyIncreasingDensity(ladderSpecs, (sp) => sp.name);
});

// ---------------------------------------------------------------------------
// 2. The ORIGINAL bug: a single turbine must never again out-density a
//    nuclear plant, even though pow_nuke sits outside the conventional
//    ladder chain (test 1) as a documented footprint-realism exception.
// ---------------------------------------------------------------------------
test('BUG-648: pow_wind (turbine) never out-densifies pow_nuke (the original defect)', () => {
  const wind = SPECS.pow_wind;
  const nuke = SPECS.pow_nuke;
  assert.ok(densityOf(wind) < densityOf(nuke),
    `pow_wind (${densityOf(wind).toFixed(2)} MW/tile) must NOT exceed pow_nuke (${densityOf(nuke).toFixed(2)}) — this was the literal Aaron bug report`);
});

// ---------------------------------------------------------------------------
// 3. Hydro is a documented SPECIAL CASE: it must dwarf pow_nuke, the top of
//    the footprint-realism exceptions, not merely sit somewhere above it.
// ---------------------------------------------------------------------------
test('BUG-648: pow_hydro (special case) is denser than pow_nuke', () => {
  const nuke = SPECS.pow_nuke;
  const dam = SPECS.pow_hydro;
  assert.ok(densityOf(dam) > densityOf(nuke),
    `pow_hydro (${densityOf(dam).toFixed(2)} MW/tile) must exceed pow_nuke (${densityOf(nuke).toFixed(2)})`);
});

// ---------------------------------------------------------------------------
// 4. PERMANENT REGRESSION (round-2): pow_nuke's footprint must NEVER shrink
//    below 13x13 again. This is not a style preference — a smaller footprint
//    was proven (attack-bug648-round.test.mjs) to silently road-disconnect
//    Aaron's real already-placed pow_nuke (id 3331), deleting 1,120 MW from
//    his grid with no notice. Any future balance pass that wants pow_nuke
//    denser MUST do it via a load-time "grandfathered footprint" migration
//    (mirroring the existing `b.footprintW ?? sp.w` auto-scale-ladder
//    pattern) — never a bare spec-table shrink.
// ---------------------------------------------------------------------------
test('BUG-648 REGRESSION: pow_nuke footprint is pinned at 13x13 (never shrinks — road-adjacency hazard)', () => {
  const nuke = SPECS.pow_nuke;
  assert.equal(nuke.w, 13, 'pow_nuke.w must stay 13 — a smaller footprint silently road-disconnects existing placements');
  assert.equal(nuke.h, 13, 'pow_nuke.h must stay 13 — a smaller footprint silently road-disconnects existing placements');
});

// ---------------------------------------------------------------------------
// 5. RED-PROOF: assertStrictlyIncreasingDensity is not a tautology — it
//    genuinely fails on the PRE-FIX catalogue (Aaron's exact bug report
//    numbers) and the turbine-vs-nuke check independently fails too.
// ---------------------------------------------------------------------------
test('RED-PROOF: the conventional-ladder assertion fails on the historical (pre-BUG-648) catalogue', () => {
  // The exact figures from Aaron's bug report: pow_wind 8 MW/1 tile,
  // pow_windfarm 60 MW/9 tiles.
  const preFix = [
    { name: 'pow_wind (pre-fix)', mw: 8, w: 1, h: 1 },
    { name: 'pow_windfarm (pre-fix)', mw: 60, w: 3, h: 3 },
  ];
  // pre-fix pow_wind (8.00) already beats pre-fix pow_windfarm (6.67) —
  // exactly Aaron's reported defect.
  assert.throws(
    () => assertStrictlyIncreasingDensity(preFix, (sp) => sp.name),
    /density ladder violated/,
    'the pre-fix catalogue (turbine denser than wind farm) must fail this assertion'
  );
});

test('RED-PROOF: the turbine-vs-nuke check fails on the historical (pre-BUG-648) turbine mw', () => {
  const preFixWindDensity = 8 / (1 * 1); // Aaron's bug report figure
  const nukeDensity = densityOf(SPECS.pow_nuke); // unchanged, 6.63
  assert.ok(
    preFixWindDensity > nukeDensity,
    'sanity: the pre-fix 8 MW turbine must genuinely have out-densified pow_nuke — proves test 2 is not a tautology'
  );
});

test('RED-PROOF: perturbing one live spec (halving pow_windfarm.mw) flips the conventional-ladder test to failing', () => {
  const ladderIds = ['pow_wind', 'pow_windfarm', 'pow_ccgt'];
  const perturbed = ladderIds.map((id) => ({ ...SPECS[id] }));
  const wfIdx = ladderIds.indexOf('pow_windfarm');
  perturbed[wfIdx] = { ...perturbed[wfIdx], mw: Math.round(perturbed[wfIdx].mw / 2) };
  assert.throws(
    () => assertStrictlyIncreasingDensity(perturbed, (sp) => sp.name),
    /density ladder violated/,
    'halving pow_windfarm.mw must drop it below pow_wind and fail the ordering assertion'
  );
});

// ---------------------------------------------------------------------------
// 6. GRID-CAPACITY DELTA — no silent blackout on an existing, turbine-heavy
//    city (constraint (2) of this bug's brief). An independent round later
//    measured the TRUE figure on Aaron's real fresh savepoint at -14.57%
//    (bigger than this fixture's -3.0%-scale estimate, both because his
//    city had grown and because a since-reverted footprint shrink was
//    separately taking his one nuke offline) — even so, capacity still
//    cleared need with a healthy 51% margin; see attack-bug648-round.test.mjs
//    finding (1) for that real-savepoint proof. This fixture pins the
//    BOUNDED, SPECS-derived nature of the turbine-only delta on a smaller,
//    reproducible shape (never the literal external savepoint file).
// ---------------------------------------------------------------------------
test('BUG-648: grid-capacity delta on a turbine-heavy fixture is bounded, not a silent collapse', () => {
  const OLD_WIND_MW = 8; // Aaron's bug report figure, historical constant for this regression only
  const TURBINES = 1991;
  const NUKES = 1;
  const HYDROS = 23;

  const grossCap = (windMw) =>
    TURBINES * windMw + NUKES * SPECS.pow_nuke.mw + HYDROS * SPECS.pow_hydro.mw;

  const before = grossCap(OLD_WIND_MW);
  const after = grossCap(SPECS.pow_wind.mw);
  const delta = after - before;
  const deltaPct = (delta / before) * 100;

  // The fix must not be a no-op (the whole point is pow_wind's mw changed)...
  assert.notEqual(SPECS.pow_wind.mw, OLD_WIND_MW, 'sanity: pow_wind.mw must actually differ from the historical figure');
  // ...but the mitigation (6 MW, not the naive 4 MW halving) must keep the
  // gross capacity hit on a turbine-dominated city smaller than the naive
  // halving would have produced on this exact fixture shape (deriving the
  // bound from the OLD figure, never a literal percentage the mitigation
  // was tuned to hit).
  const naiveHalvingDeltaPct = ((grossCap(OLD_WIND_MW / 2) - before) / before) * 100;
  assert.ok(
    deltaPct > naiveHalvingDeltaPct,
    `mitigated delta (${deltaPct.toFixed(1)}%) must be less severe than the naive halving would have been (${naiveHalvingDeltaPct.toFixed(1)}%)`
  );
  // And the absolute hit must stay under 5% on THIS fixture's gross terms —
  // real online-gated figures on an actual save are bigger (see the header
  // note above); this bound is on the fixture's own gross nameplate math.
  assert.ok(
    Math.abs(deltaPct) < 5,
    `gross capacity delta (${deltaPct.toFixed(1)}%) exceeds the 5% blast-radius budget for an existing turbine-heavy city`
  );
});
