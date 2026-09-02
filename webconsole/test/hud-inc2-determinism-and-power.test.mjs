// hud-inc2-determinism-and-power.test.mjs — FEAT-2326609720 inc2, AC-9/AC-14.
//
// AC-14: the two NEW derived surfaces (Employment, Coverage Map/domain
// tabs) must be pure, order-independent folds over state.buildings — same
// bar serviceCoverageOf/totalJobs already meet.
//
// AC-9: a covered power shortfall (Grid Import ON) must never register as a
// brownout — proven against isBrownoutActive, the toggle-aware SSOT.
//
// D3 FIX (independent round REJECT, 2026-09-02): the original buildCity()
// fixture here was INERT — `unlockedAll: true` was set but funds stayed at
// STARTING_TREASURY (£1.5M) while edu_primary costs £9.36M and pol_station
// £4.68M, so BOTH `place` actions silently failed on affordability
// (buildings count 1855 -> 1855, population stayed 0). With population 0,
// serviceCoverageOf's `need <= 0 ? 1` branch makes EVERY row read coverage
// 1 regardless of any real bug — a gate that could not fail. The fixture now
// (a) funds the city so both placements land, (b) asserts that landing as an
// explicit PRECONDITION (buildings count strictly increases) so a future
// regression in the fixture itself fails loudly instead of silently
// degenerating back into the same vacuous all-1.0 case, (c) forces
// construction complete (builtTick far in the past, mirroring the
// activation-inc1.test.mjs roadAt() convention) since a freshly-placed
// building is still under construction and reads offline/cap-0 otherwise,
// and (d) gives the city a real population so `need > 0` for every row.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { serviceCoverageOf, totalJobs, unemploymentOf, isBrownoutActive } from '../src/sim/data.ts';

function buildCity() {
  let s = initialState();
  s = { ...s, unlockedAll: true, funds: 100_000_000 };
  const before = s.buildings.length;
  // Both tiles are adjacent to the starter city's x=150 road column
  // (engine.ts starterCity(), y 57..63) so they land online once
  // construction is forced complete below.
  s = reducer(s, { type: 'place', spec: 'edu_primary', x: 151, y: 60 });
  s = reducer(s, { type: 'place', spec: 'pol_station', x: 151, y: 62 });
  const landedIds = s.buildings.slice(before).map((b) => b.id);
  assert.equal(landedIds.length, 2, 'PRECONDITION: both placements must actually land (funds/road-adjacency) — a fixture that cannot fail its own setup is exactly the D3 defect');
  // Force construction complete (mirrors activation-inc1.test.mjs's
  // roadAt() builtTick: -1000 convention) — otherwise the buildings are
  // still under construction and read offline/cap-0.
  s = {
    ...s,
    buildings: s.buildings.map((b) => (landedIds.includes(b.id) ? { ...b, builtTick: -100000 } : b)),
    // 2500 (not a rounder number) puts primary's coverage exactly at the
    // 0.8+ GREEN band (need = 2500*0.12 = 300 = cap) while nursery/college
    // stay at 0 capacity (RED) — deliberately MIXED colours across the
    // Education domain, not uniform, so a rendered-colour comparison test
    // can actually distinguish a real bug from "everything is red".
    population: 2500,
  };
  return s;
}

test('PRECONDITION: buildCity() fixture actually lands buildings and produces non-zero coverage (a fixture that cannot fail its own setup is the D3 defect class)', () => {
  const s = buildCity();
  const rows = serviceCoverageOf(s);
  assert.ok(s.population > 0, 'fixture must give the city a real population so need > 0');
  const primary = rows.find((r) => r.id === 'primary');
  const police = rows.find((r) => r.id === 'police');
  assert.ok(primary.need > 0 && primary.cap > 0, 'primary row must have real need AND real capacity from the landed edu_primary');
  assert.ok(police.need > 0 && police.cap > 0, 'police row must have real need AND real capacity from the landed pol_station');
  // Not every row can be the SAME coverage value — nursery/college/gp/hosp/
  // fire/cleanwater/waste/power all still have cap 0 while primary/police
  // have real capacity, so the coverage set must have more than one value.
  const coverages = new Set(rows.map((r) => Math.round(r.coverage * 1000)));
  assert.ok(coverages.size > 1, 'fixture must produce VARIED coverage across rows, not a uniform constant (the all-1.0 D3 failure mode)');
});

test('AC-14: serviceCoverageOf/totalJobs/unemploymentOf are order-independent over state.buildings', () => {
  const a = buildCity();
  const b = { ...a, buildings: [...a.buildings].reverse() };

  const rowsA = serviceCoverageOf(a);
  const rowsB = serviceCoverageOf(b);
  assert.deepEqual(
    rowsA.map((r) => ({ id: r.id, need: r.need, cap: r.cap, coverage: r.coverage })),
    rowsB.map((r) => ({ id: r.id, need: r.need, cap: r.cap, coverage: r.coverage })),
    'serviceCoverageOf must be identical regardless of buildings array order'
  );
  assert.equal(totalJobs(a), totalJobs(b), 'totalJobs must be identical regardless of buildings array order');
  assert.equal(unemploymentOf(a), unemploymentOf(b), 'unemploymentOf must be identical regardless of buildings array order');
});

test('AC-14: two identical states (same construction) produce identical Education/Health/Safety row sets', () => {
  const a = buildCity();
  const b = buildCity();
  const domainRows = (s, ids) => serviceCoverageOf(s).filter((r) => ids.includes(r.id));
  assert.deepEqual(domainRows(a, ['nursery', 'primary', 'college']), domainRows(b, ['nursery', 'primary', 'college']));
  assert.deepEqual(domainRows(a, ['gp', 'hosp']), domainRows(b, ['gp', 'hosp']));
  assert.deepEqual(domainRows(a, ['fire', 'police']), domainRows(b, ['fire', 'police']));
});

test('AC-9: a COVERED power shortfall (Grid Import ON) is never a brownout — isBrownoutActive stays false even with a real deficit', () => {
  // Force a state with population but zero power plants -> a real deficit
  // (need > cap), with Grid Import left at its default (ON).
  let s = initialState();
  s = { ...s, unlockedAll: true, population: 100000, gridImportEnabled: true };
  const power = serviceCoverageOf(s).find((r) => r.id === 'power');
  assert.ok(power.need > power.cap, 'test setup must produce a real physical deficit');
  assert.equal(isBrownoutActive(s), false, 'Grid Import ON must suppress isBrownoutActive despite the real deficit');

  // Same deficit, Grid Import OFF -> genuinely uncovered -> brownout active.
  const s2 = { ...s, gridImportEnabled: false };
  assert.equal(isBrownoutActive(s2), true, 'Grid Import OFF must leave the deficit as a real uncovered brownout');
});

// NOTE: the live-component PowerTab mount smoke test lives in
// hud-inc2-power-mount.test.tsx (a .tsx file run under `tsx --test`) because
// this .mjs file runs under plain `node --test`, which cannot load a .tsx
// module (store.tsx) — ERR_UNKNOWN_FILE_EXTENSION. The rendered-colour
// determinism proof (a live React render, not just the underlying
// selectors) lives in hud-inc2-rendered-determinism.test.tsx for the same
// reason.
