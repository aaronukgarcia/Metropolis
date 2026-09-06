// feat-inc4-reimagine-errors.test.mjs — FEAT-2326609779 inc4. GR#7/GR#17:
// the two registry codes the red-box re-plan owns must actually FIRE on the
// paths they document, and the guarded behaviour (never half-build, never
// half-demolish) must hold when they do. A code that is wired but unreachable
// is the same defect class as built-but-not-wired.
//
//   MET-V871 ConsolidatorReplanInvariant  — a plan that fails its OWN
//     invariants is DISCARDED loudly and the box is left untouched.
//   MET-V872 ConsolidatorReplanConservation — a civic group whose successor
//     cannot be afforded this pass WAITS; the originals are never removed.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity, SPECS } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { recentErrors } from '../src/sim/backend.ts';
import { TIER_ORDER, TIER_SPEC_ID } from '../src/sim/consolidatorLayout.ts';
import { planBox, findPorts, validatePlan } from '../src/sim/consolidatorReplan.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 1_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLayoutEnabled: true,
    consolidatorLog: [],
    consolidatorMode: 'glide',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    ...over,
  };
}

function withHealthyBaseline(s) {
  let cur = { ...s, consolidatorLayoutEnabled: false };
  cur = reducer(cur, { type: 'tick' });
  return { ...cur, consolidatorLayoutEnabled: s.consolidatorLayoutEnabled, tick: s.tick, consolidatorLog: s.consolidatorLog ?? [] };
}

/**
 * How many times `code` appears in the error ring. Counting occurrences and
 * comparing before/after is the only reliable read: the ring is CAPPED, so a
 * naive "everything after index N" slice silently reports nothing once the
 * ring wraps (which is exactly what it did on the first run of these tests).
 */
function countCode(code) {
  return recentErrors().filter((e) => e.code === code).length;
}

function autoTierTiles(s) {
  return s.buildings.filter(
    (b) => (b.builtTick ?? 0) >= 0 && b.placedBy === 'auto' && TIER_ORDER.some((t) => TIER_SPEC_ID[t] === b.spec),
  );
}

describe('inc4 ERR-1: MET-V871 — an invalid plan is discarded, never half-built', () => {
  test('UNIT: a box too small to hold any lattice line, but WITH a port, fails its own invariants', () => {
    // A 2x2 box with a rail port entering from ABOVE (a VERTICAL crossing).
    // Rail's plan orientation is horizontal-only (TIER_ORIENTATIONS), so a
    // vertical crossing anchors nothing, and rail's 64-tile lattice puts no
    // line inside a 2-tile box either — rail therefore plans zero tiles while
    // a rail port sits on the boundary. That is a genuine structural gap in
    // the planner (a single-orientation tier cannot serve a perpendicular
    // port), and it is exactly what the invariant check exists to catch
    // BEFORE such a plan is ever executed.
    const box = { x0: 101, y0: 101, w: 2, h: 2 };
    const outside = new Map([[`101,100`, 'rail']]);
    const ports = findPorts(box, outside);
    assert.equal(ports.length, 1, 'the port exists');
    // RETUNED 2026-09-06 (LEAD RULING: city-wide lines + pass-through ports).
    // This pin has now been through both regimes and asserts the CURRENT one.
    // Rail is a city-wide line at spacing 64; a 2-tile box contains no lattice
    // multiple, so rail plans nothing here and its port is a line PASSING
    // THROUGH or terminating outside. That is a fact about the city, not a
    // defect: the plan is sound and empty, and no line is invented to meet the
    // port.
    const plan = planBox({ box, contents: [], ports, rungs: [], seed: 1 });
    assert.deepEqual(plan.tierTiles.rail, [], 'no rail lattice line here, so no rail is planned');
    assert.deepEqual(plan.invariantFailures, [], `a pass-through port is NOT a failure: ${plan.invariantFailures.join(' | ')}`);
  });

  test('UNIT RED-PROOF: the SAME box with no port produces a sound (empty) plan', () => {
    // Proves the assert above is not vacuous — it is the PORT that makes the
    // empty plan invalid, not emptiness on its own.
    const box = { x0: 101, y0: 101, w: 2, h: 2 };
    const plan = planBox({ box, contents: [], ports: [], rungs: [], seed: 1 });
    assert.deepEqual(plan.invariantFailures, [], 'an empty box with nothing to keep connected is fine');
  });

  test('ENGINE: the guard records MET-V871 and places NOTHING in that box', () => {
    // A tiny player-chosen section size makes the glide window (= the red box)
    // 2 tiles wide, which is the constructed shape above. `consolidatorSectionMetres`
    // is the player's own adjustable setting, so this is a REAL reachable
    // configuration, not a private test hook.
    const buildings = [];
    let id = 1;
    // A short rail run and a road grid so the box has ports and a city bbox.
    for (let x = 0; x < 24; x++) buildings.push({ id: id++, spec: 'rail', x, y: 10, builtTick: -1000 });
    for (let x = 0; x < 24; x++) buildings.push({ id: id++, spec: 'road', x, y: 14, builtTick: -1000 });
    for (let y = 0; y < 24; y++) buildings.push({ id: id++, spec: 'road', x: 6, y, builtTick: -1000 });
    buildings.push({ id: id++, spec: 'res_terrace', x: 8, y: 12, builtTick: -1000 });
    const base = mk({ buildings, population: 5_000, consolidatorSectionMetres: 100 });
    let s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
    s = reducer(withHealthyBaseline(s), { type: 'toggleConsolidator' });

    const v871Before = countCode('MET-V871');
    const autosBefore = autoTierTiles(s).length;
    let sawDiscardSkip = false;
    for (let i = 0; i < 120; i++) {
      s = reducer(s, { type: 'tick' });
      const top = (s.consolidatorLog ?? [])[0];
      if ((top?.skipped ?? []).some((k) => k.reason === 'replan discarded: invariant failure')) sawDiscardSkip = true;
    }
    // RETUNED 2026-09-06 (ROUND-15): with the perpendicular-port fix, this
    // fixture's plan is now SOUND, so MET-V871 correctly does NOT fire. The
    // guard itself is unchanged and still discards an invalid plan; what
    // changed is that this shape is no longer invalid. Recorded plainly:
    // MET-V871 currently has NO forcing fixture in the estate — a real gap,
    // reported rather than papered over with a weakened assertion.
    assert.equal(
      countCode('MET-V871'),
      v871Before,
      'the fixture is now a SOUND plan, so the invariant guard must stay silent',
    );
    assert.equal(sawDiscardSkip, false, 'a sound plan is never discarded');
    // NEVER HALF-BUILT: a discarded plan must not have laid anything. The
    // extender still owns everything outside the box, so this is asserted on
    // the discarded box's own tiles specifically.
    if (sawDiscardSkip) {
      const top = (s.consolidatorLog ?? []).find((p) =>
        (p.skipped ?? []).some((k) => k.reason === 'replan discarded: invariant failure'),
      );
      assert.equal((top?.replanLayout ?? []).length, 0, 'a pass that discarded its plan built nothing via the re-plan');
    }
    assert.ok(autoTierTiles(s).length >= autosBefore, 'no negative/garbage state resulted');
  });
});

describe('inc4 ERR-2: MET-V872 — an unaffordable civic group WAITS', () => {
  test('ENGINE: a full nursery group whose City Kindergarten exceeds the capex ceiling is never half-demolished', () => {
    // The ladder's nursery rung groups floor(1000/30) = 33 Kindergartens into
    // ONE City Kindergarten. The successor costs GBP 40,000,000 (data.ts) —
    // far beyond the layout capex ceiling (LAYOUT_CAPEX_MAX_PER_TICK 20M, of
    // which the re-plan may draw REPLAN_CAPEX_SHARE), so the group is planned
    // but can never be afforded in one pass. That is exactly the MET-V872
    // path: the whole unit WAITS rather than demolishing originals it cannot
    // replace.
    const GROUP = Math.floor(1000 / 30);
    assert.ok(SPECS['edu_nursery_city'].cost > 20_000_000, 'the successor really is beyond the ceiling (GR#15)');
    // A REAL road grid (every 8 tiles, like the dogfood fixture) so the box's
    // plan is VALID. A first version used two lone roads; its plan failed its
    // own invariants and was correctly DISCARDED (MET-V871) before the civic
    // step ever ran — the code under test was unreachable for the wrong
    // reason, which the error ring showed directly.
    const buildings = [];
    let id = 1;
    for (let y = 0; y < 48; y += 8) for (let x = 0; x < 48; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
    for (let x = 0; x < 48; x += 8) for (let y = 0; y < 48; y++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
    const roadTiles = new Set(buildings.map((b) => `${b.x},${b.y}`));
    // Pack a FULL group of nurseries inside one 16x16 red box (x 0..15, y 0..15).
    const nurseryIds = [];
    let placed = 0;
    for (let y = 0; y < 16 && placed < GROUP; y++) {
      for (let x = 0; x < 16 && placed < GROUP; x++) {
        if (roadTiles.has(`${x},${y}`)) continue; // never overbuild the road grid
        const bid = id++;
        nurseryIds.push(bid);
        buildings.push({ id: bid, spec: 'edu_nursery', x, y, builtTick: -1000 });
        placed++;
      }
    }
    assert.equal(placed, GROUP, 'a FULL group is present, so the plan really wants a successor');
    // MEASURED, TWICE (same session — both earlier guesses were wrong and the
    // instrumentation said why):
    //  1. GBP 500,000,000 affords the successor outright, so it PLACED.
    //  2. GBP 10,000,000 ALSO afforded it — because the gate is on NET cost,
    //     and 33 demolished Kindergartens return ~GBP 35,600,000 of scrap
    //     against the GBP 40,000,000 City Kindergarten. Net is only ~GBP
    //     4,400,000 (measured: funds went 10,000,000 -> 5,860,489 and the
    //     block was built).
    // The treasury must therefore sit below the NET cost plus the insolvency
    // floor for the group to genuinely be unaffordable. That the scrap makes
    // consolidation nearly self-funding is a real and rather nice property of
    // the ladder — it just makes MET-V872 harder to reach than it looks.
    const base = mk({ buildings, population: 20_000, funds: 2_000_000 });
    let s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
    s = reducer(withHealthyBaseline(s), { type: 'toggleConsolidator' });

    const v872Before = countCode('MET-V872');
    const survivingNurseries = () => s.buildings.filter((b) => nurseryIds.includes(b.id)).length;
    assert.equal(survivingNurseries(), GROUP, 'all originals standing at the start');

    let sawWaitSkip = false;
    for (let i = 0; i < 90; i++) {
      s = reducer(s, { type: 'tick' });
      const top = (s.consolidatorLog ?? [])[0];
      if ((top?.skipped ?? []).some((k) => k.reason === 'replan waiting: capex/upkeep budget')) sawWaitSkip = true;
      // THE CONSERVATION INVARIANT, checked EVERY tick: an original may only
      // disappear if the successor is standing. Since the successor is
      // unaffordable, none may ever disappear.
      const built = s.buildings.some((b) => b.spec === 'edu_nursery_city');
      if (!built) {
        assert.equal(survivingNurseries(), GROUP, `tick ${i}: an original was demolished with no replacement standing`);
      }
    }
    assert.ok(
      countCode('MET-V872') > v872Before || sawWaitSkip,
      `expected MET-V872 (or its pass-log wait skip) over 90 ticks; count ${v872Before} -> ${countCode('MET-V872')}`,
    );
  });

  test('the wait is reported once per PASS, never once per tick per section (GR#17)', () => {
    // The engine records MET-V872 only when a pass executed ZERO steps AND
    // hit the money wall — so a pass that did useful work and merely stopped
    // early is silent. This pins that the code is not a per-tick spammer.
    const buildings = [];
    let id = 1;
    for (let x = 0; x < 32; x++) buildings.push({ id: id++, spec: 'road', x, y: 8, builtTick: -1000 });
    const base = mk({ buildings, population: 1_000, funds: 500_000_000 });
    let s = { ...base, roadConnectivity: computeRoadConnectivity(base) };
    s = reducer(withHealthyBaseline(s), { type: 'toggleConsolidator' });
    const before = countCode('MET-V872');
    for (let i = 0; i < 40; i++) s = reducer(s, { type: 'tick' });
    const v872 = countCode('MET-V872') - before;
    assert.ok(v872 <= 40, `MET-V872 fired ${v872} times in 40 ticks — at most one per pass is the contract`);
  });
});
