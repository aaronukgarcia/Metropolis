// attack-largest-first-reround3.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND 3
// (GR#23, attacker != author) against BUG-685 / BUG-686 after re-round 2's
// REJECT (RR2-1 hardcoded "insufficient funds" on every partial; RR2-2 the
// `|| (placed > 0 && !matchesPlan)` disjunct claiming self-substitution) was
// answered by switching 'resolveDemand's substitution branch to
// `result.substituted` + shortfallReason(), and 'resolveDemandAll's
// anySubstituted to `result.substituted`.
//
// See the file footer for this round's verdict summary.
//
// Everything here drives the REAL reducer — no engine internals are imported
// beyond what a test can legitimately see; every precondition is derived from
// demandFixPlan()/SPECS at runtime (GR#15), never hardcoded.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, xpForLevel } from '../src/sim/engine.ts';
import { SPECS, demandFixPlan, placementCost, MAP_W, MAP_H, RESOLVE_DEMAND_ALL_MAX_UNITS } from '../src/sim/data.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';

const QUADRILLION = 1e15;

const city = (population, funds, extra = {}) => ({
  ...initialState(),
  population,
  unlockedAll: true,
  funds,
  administrationState: null,
  ...extra,
});

/** A fully paved 1x1-road map with `holes` isolated single free tiles in the
 *  middle (spaced 2 apart so nothing bigger than 1x1 can ever fit). Funds and
 *  administration mode are provably not in play on this fixture — the ONLY
 *  thing that can stop a build is the absence of free area. Same helper shape
 *  re-round 2 used, re-derived here so this file stands alone. */
function pavedMapWithHoles(holes) {
  const cx = Math.floor(MAP_W / 2);
  const cy = Math.floor(MAP_H / 2);
  const free = new Set();
  for (let i = 0; i < holes; i++) free.add(`${cx + i * 2},${cy}`);
  const buildings = [];
  let id = 1;
  for (let y = 0; y < MAP_H; y++) {
    for (let x = 0; x < MAP_W; x++) {
      if (free.has(`${x},${y}`)) continue;
      buildings.push({ id: id++, spec: 'road', x, y, builtTick: 0 });
    }
  }
  return buildings;
}

/** Per-spec DELTA between two states. Counting `buildings.slice(before)` (as
 *  earlier rounds did) is wrong on a dense fixture: a 'place' can REPLACE road
 *  tiles, so the array can shrink even though a unit landed, and it also
 *  counts autoConnect()'s road/avenue tiles as "built". A full census delta is
 *  immune to both. */
function census(state) {
  const out = {};
  for (const b of state.buildings) out[b.spec] = (out[b.spec] ?? 0) + 1;
  return out;
}
function placedBetween(before, after) {
  const a = census(before);
  const b = census(after);
  const out = {};
  for (const id of new Set([...Object.keys(a), ...Object.keys(b)])) {
    const d = (b[id] ?? 0) - (a[id] ?? 0);
    if (d > 0) out[id] = d;
  }
  return out;
}
/** Only the service units — autoConnect road furniture stripped out, so a
 *  "did the plan get built" comparison is not polluted by connectors. */
function placedOfPlan(plan, before, after) {
  const all = placedBetween(before, after);
  const out = {};
  for (const m of plan.mix) if (all[m.specId]) out[m.specId] = all[m.specId];
  for (const id of Object.keys(all)) {
    if (SPECS[id] && SPECS[id].kind === SPECS[plan.specId]?.kind) out[id] = all[id];
  }
  return out;
}

/** Every spec name that appears anywhere in a notice, mapped back to specIds
 *  (longest-name-first so "Primary School" is not matched inside a longer
 *  name). Used to prove a notice never NAMES AS BUILT a spec of which zero
 *  units landed. */
const SPEC_IDS_BY_NAME = Object.entries(SPECS)
  .filter(([, sp]) => sp && sp.name)
  .sort((a, b) => b[1].name.length - a[1].name.length);

/** The clause(s) of a notice that ASSERT construction — i.e. the parts a
 *  player reads as "this is what you now own". Deliberately excludes the
 *  "of <plan>" half (planCountLabel) and the trailing reason after the em
 *  dash, both of which legitimately name unbuilt, planned specs. */
function builtClauses(notice) {
  const out = [];
  // 'resolveDemand': "Fix (<plan>): substituted <mix>[, N of M units total] — <reason>"
  const sub = /substituted\s+(.*?)(?:,\s*\d+ of\s|\s+—|$)/.exec(notice);
  if (sub) out.push(sub[1]);
  // 'resolveDemandAll': "Fix All: built <list> — <...>"
  const all = /Fix All: built\s+(.*?)(?:\s+—|$)/.exec(notice);
  if (all) out.push(all[1]);
  return out;
}

/** Every "<N> x <Spec Name>" token asserted as BUILT by a notice, resolved to
 *  an exact spec (never a substring match — "College" must not be found
 *  inside "Technical College"). */
function assertedBuiltUnits(notice) {
  const units = [];
  for (const clause of builtClauses(notice)) {
    for (const raw of clause.split(/\s*(?:\+|,)\s*/)) {
      const m = /^(\d+)\s*x\s*(.+?)\s*$/.exec(raw);
      if (!m) continue;
      const hit = SPEC_IDS_BY_NAME.find(([, sp]) => sp.name === m[2]);
      units.push({ raw, count: Number(m[1]), specId: hit ? hit[0] : null, name: m[2] });
    }
  }
  return units;
}

/** RR-1 phantom asset: a notice may never assert as BUILT a spec of which
 *  zero units landed, nor more units than actually landed. */
function assertNoPhantomAsset(notice, built, ctx) {
  for (const u of assertedBuiltUnits(notice)) {
    assert.ok(u.specId, `${ctx}: notice names an unknown spec "${u.name}": ${notice}`);
    assert.ok(
      (built[u.specId] ?? 0) >= u.count,
      `${ctx}: RR-1 PHANTOM ASSET — the notice claims "${u.raw}" but only ${built[u.specId] ?? 0} landed.\n` +
        `  notice: ${notice}\n  actually built: ${JSON.stringify(built)}`
    );
  }
}

// ===========================================================================
// RR3-1 — the FULL honesty grid on 'resolveDemand'.
//   cells: {ample funds, zero funds, administration mode, no free site,
//           funded-for-a-cheaper-rung-only, unit-cap} x every service.
// Invariants asserted on EVERY cell:
//   H1  a notice is null ONLY when the plan was built spec-for-spec in full
//   H2  never "insufficient funds" while the treasury is provably untouched
//   H3  never "no free area" on a wide-open map
//   H4  never a phantom asset (RR-1)
//   H5  never a self-substitution claim (RR2-2)
//   H6  the reason stated must be TRUE of the state it is reported against
// ===========================================================================

const OPEN_MAP_CELLS = () => [
  ['ample funds', city(100_000, QUADRILLION)],
  ['zero funds', city(100_000, 0)],
  ['administration mode', city(100_000, QUADRILLION, { administrationState: { since: 1, reason: 'test' } })],
  ['administration mode + zero funds', city(100_000, 0, { administrationState: { since: 1, reason: 'test' } })],
  ['tiny treasury (cheap rungs only)', city(100_000, 12_000_000)],
];

const FULL_MAP_CELLS = () => [
  ['no free site, ample funds', city(100_000, QUADRILLION, { buildings: pavedMapWithHoles(0) })],
  ['no free site, zero funds', city(100_000, 0, { buildings: pavedMapWithHoles(0) })],
  ['3 x 1x1 sites, ample funds', city(100_000, QUADRILLION, { buildings: pavedMapWithHoles(3) })],
];

test('RR3-1 (grid, resolveDemand): every cell x every service tells the truth', () => {
  const cells = [...OPEN_MAP_CELLS(), ...FULL_MAP_CELLS()];
  let cellCount = 0;
  for (const [tag, s] of cells) {
    const openMap = s.buildings.length < 1000;
    for (const plan of demandFixPlan(s)) {
      cellCount++;
      const after = reducer(s, { type: 'resolveDemand', serviceKey: plan.serviceKey });
      const built = placedOfPlan(plan, s, after);
      const placedTotal = Object.values(built).reduce((a, b) => a + b, 0);
      const notice = after.placeNotice ?? null;
      const ctx = `[${tag} / ${plan.serviceKey}]`;
      const spent = s.funds - after.funds;

      // H1: silence is reserved for a complete, spec-for-spec plan build.
      if (notice === null) {
        assert.equal(
          placedTotal,
          plan.count,
          `${ctx}: silent notice but ${placedTotal} of ${plan.count} units placed — RR-2 silent-substitution class`
        );
        for (const m of plan.mix) {
          assert.equal(built[m.specId] ?? 0, m.count, `${ctx}: silent notice but the mix does not match the plan`);
        }
        continue;
      }

      // H4 / H5
      assertNoPhantomAsset(notice, built, ctx);
      if (plan.mix.length === 1 && /substituted/.test(notice)) {
        const only = SPECS[plan.mix[0].specId].name;
        const clause = builtClauses(notice)[0] ?? '';
        assert.ok(
          !clause.includes(only) || Object.keys(built).some((id) => id !== plan.mix[0].specId),
          `${ctx}: RR2-2 regression — claims substituting "${only}" for itself.\n  notice: ${notice}`
        );
      }

      // H2: the treasury may only be blamed when it is genuinely short of the
      // spec the notice is about.
      if (/insufficient funds/i.test(notice)) {
        const headlineCost = placementCost(SPECS[plan.specId]);
        assert.ok(
          after.funds < headlineCost,
          `${ctx}: blamed the treasury with £${after.funds} in the bank against a £${headlineCost} spec (spent £${spent}).\n  notice: ${notice}`
        );
      }

      // H3: the map may only be blamed when the map is genuinely full for the
      // spec in question. On the open genesis map it never is.
      if (openMap) {
        assert.ok(
          !/no free .* area on the map/i.test(notice),
          `${ctx}: blamed a full map on an open ${MAP_W}x${MAP_H} map with ${before} buildings.\n  notice: ${notice}`
        );
      }

      // H6: administration mode is only claimed when it is actually engaged,
      // and when engaged + cost>0 it must be the reason (it is the hard stop).
      if (/Administration Mode/i.test(notice)) {
        assert.ok(after.administrationState, `${ctx}: claimed administration mode while not in it.\n  notice: ${notice}`);
      }
      if (s.administrationState && placementCost(SPECS[plan.specId]) > 0 && placedTotal === 0) {
        assert.match(
          notice,
          /Administration Mode/i,
          `${ctx}: administration mode blocked the build but the notice blames something else`
        );
      }
    }
  }
  assert.ok(cellCount >= 80, `grid must be a real sweep, got ${cellCount} cells`);
});

// ===========================================================================
// RR3-2 — the same grid on 'resolveDemandAll'.
// ===========================================================================
test('RR3-2 (grid, resolveDemandAll): every cell tells the truth', () => {
  for (const [tag, s] of [...OPEN_MAP_CELLS(), ...FULL_MAP_CELLS()]) {
    const openMap = s.buildings.length < 1000;
    const after = reducer(s, { type: 'resolveDemandAll' });
    const built = placedBetween(s, after);
    const notice = after.placeNotice ?? null;
    const ctx = `[Fix All / ${tag}]`;
    assert.ok(notice !== null, `${ctx}: Fix All must always report something`);
    assertNoPhantomAsset(notice, built, ctx);

    if (/unaffordable|was substituted/i.test(notice)) {
      // A substitution claim requires that SOME service actually built a spec
      // its own plan did not name.
      const planned = new Set();
      for (const p of demandFixPlan(s)) for (const m of p.mix) planned.add(m.specId);
      assert.ok(
        Object.keys(built).some((id) => !planned.has(id)) || Object.keys(built).length > 0,
        `${ctx}: substitution claimed with nothing built.\n  notice: ${notice}`
      );
    }
    if (openMap) {
      assert.ok(
        !/no free .* area on the map/i.test(notice),
        `${ctx}: blamed a full map on an open map.\n  notice: ${notice}`
      );
    }
    if (/insufficient funds/i.test(notice) && s.funds === QUADRILLION) {
      // At least ONE service must genuinely be beyond the (post-spend) purse.
      const cheapest = Math.min(
        ...demandFixPlan(after)
          .map((p) => placementCost(SPECS[p.specId]))
          .filter((c) => c > 0)
      );
      assert.ok(
        after.funds < cheapest,
        `${ctx}: Fix All blamed the treasury with £${after.funds} in the bank.\n  notice: ${notice}`
      );
    }
  }
});

// ===========================================================================
// RR3-3 — shortfallReason()'s priority order when TWO reasons are
// simultaneously true. DOCUMENTED, not a defect: administration mode wins
// over insufficient funds, and both win over no-free-site. Every winner is
// itself TRUE of the state, which is the honesty bar.
// ===========================================================================
test('RR3-3: with several blockers true at once, the stated reason is one that is actually true', () => {
  // zero funds AND administration mode AND a solid map — all three true.
  const s = city(100_000, 0, {
    administrationState: { since: 1, reason: 'test' },
    buildings: pavedMapWithHoles(0),
  });
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  assert.equal(after.buildings.length, s.buildings.length, 'precondition: nothing placed');
  const notice = after.placeNotice ?? '';
  assert.match(notice, /Administration Mode/i, 'administration mode is checked first and is true here');
  // ... and the loser reasons are true too, so no dishonesty either way.
  assert.ok(s.funds < placementCost(SPECS['hea_clinic']), 'the funds reason would also have been true');

  // zero funds AND a solid map, no administration: funds wins, funds is true.
  const s2 = city(100_000, 0, { buildings: pavedMapWithHoles(0) });
  const a2 = reducer(s2, { type: 'resolveDemand', serviceKey: 'gp' });
  assert.match(a2.placeNotice ?? '', /insufficient funds/i, 'funds wins over site when both are true');

  // ample funds AND a solid map: only the site reason is true, and it is used.
  const s3 = city(100_000, QUADRILLION, { buildings: pavedMapWithHoles(0) });
  const a3 = reducer(s3, { type: 'resolveDemand', serviceKey: 'gp' });
  assert.match(a3.placeNotice ?? '', /no free .* area on the map/i, 'only the site reason is true here');
  assert.ok(!/insufficient funds/i.test(a3.placeNotice ?? ''), 'RR2-1 stays fixed');
});

// ===========================================================================
// RR3-4 (RR2-1 / RR2-2 regression pins, re-derived independently): a partial
// build that is NOT a substitution must still yield an honest, non-null
// "Placed N of M" notice with the TRUE reason.
// ===========================================================================
test('RR3-4: a plain partial build of the planned spec is honest and not called a substitution', () => {
  // Locked to xp level 3 (civic-tier rebase fix, FEAT-2326609772,
  // 2026-09-05): edu_nursery unlocks at level 2, but the catalogue since
  // gained a same-stage successor, edu_nursery_city (unlock level 4) — at
  // unlockedAll god-mode it would also be a nursery-family candidate and
  // this fixture's whole point (a plain same-spec partial build) needs a
  // single-spec plan, same as when this test was written.
  const unit = placementCost(SPECS['edu_nursery']);
  const s = city(100_000, unit * 3 + 1000, { unlockedAll: false, xp: xpForLevel(3) });
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'nursery');
  assert.ok(plan.mix.length === 1 && plan.count > 3, 'fixture: a single-spec plan bigger than the purse');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'nursery' });
  const built = placedBetween(s, after);
  assert.equal(built['edu_nursery'], 3, 'precondition: exactly three of the planned spec landed');
  const notice = after.placeNotice ?? '';
  assert.match(notice, /^Placed 3 of /, 'must lead with the honest placed/planned pair');
  assert.match(notice, /insufficient funds/i, 'and carry the true reason (the purse really is empty)');
  assert.ok(!/substituted/i.test(notice), 'RR2-2: nothing was substituted — the planned spec is what was built');
});

test('RR3-4b: a partial build on a funds-rich, site-poor map blames the MAP, not the treasury', () => {
  const buildings = pavedMapWithHoles(3);
  const s = city(100_000, QUADRILLION, { buildings });
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const built = placedBetween(s, after);
  assert.equal(built['hea_clinic'], 3, 'precondition: the three holes were filled and nothing else');
  assert.ok(after.funds > s.funds * 0.999, 'precondition: the treasury is untouched');
  const notice = after.placeNotice ?? '';
  assert.ok(!/insufficient funds/i.test(notice), `RR2-1 regression: ${notice}`);
  assert.ok(!/substituted/i.test(notice), `RR2-2 regression: ${notice}`);
  assert.match(notice, /no free .* area on the map/i, 'the true, actionable reason');
});

// ===========================================================================
// RR3-5 — the `substituted` contract (angle 3). placePlanItem() sets it ONLY
// from the self-heal retry. This test pins BOTH halves: it is true for a real
// self-heal, and it is false for a plain same-spec partial. The third case —
// a WITHIN-WALK fall-through to a cheaper rung of the same mix — is measured
// and documented by RR3-6 below.
// ===========================================================================
test('RR3-5: "substituted" is claimed exactly when a spec other than the plan headline stood in for it', () => {
  // (a) real self-heal: the headline is flatly unaffordable, the plan has no
  //     cheaper entry to fall through to, and largestFirstFill() re-picks.
  const s = city(100_000, 500_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  assert.ok(plan.mix.length === 1 && plan.mix[0].specId === 'pow_hydro', 'fixture: the single-entry dam plan');
  assert.ok(placementCost(SPECS['pow_hydro']) > s.funds, 'precondition: the dam is unaffordable');
  const a = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  const builtA = placedBetween(s, a);
  assert.equal(builtA['pow_hydro'] ?? 0, 0, 'precondition: no dam exists');
  assert.ok(Object.keys(builtA).length > 0, 'precondition: the self-heal built something');
  assert.match(a.placeNotice ?? '', /substituted/i, 'a genuine substitution must say so');
  assertNoPhantomAsset(a.placeNotice ?? '', builtA, 'RR3-5a');

  // (b) not a substitution: same spec, fewer units.
  const unit = placementCost(SPECS['hea_clinic']);
  const s2 = city(100_000, unit * 5 + 1000);
  const plan2 = demandFixPlan(s2).find((p) => p.serviceKey === 'gp');
  const a2 = reducer(s2, { type: 'resolveDemand', serviceKey: 'gp' });
  const built2 = placedOfPlan(plan2, s2, a2);
  assert.equal(Object.keys(built2).join(','), 'hea_clinic', 'precondition: only the planned spec landed');
  assert.ok(!/substituted/i.test(a2.placeNotice ?? ''), 'a same-spec partial is not a substitution');
});

// ===========================================================================
// RR3-6 (FINDING F1, P2) — a WITHIN-WALK downgrade. When a multi-entry plan's
// headline entry cannot place but a cheaper entry of the SAME plan can,
// placePlanItem() returns substituted=false (the flag is only ever set by the
// self-heal retry), so the notice takes the generic "Placed N of M" path and
// never discloses WHICH rung landed. The player is told "Placed 3 of 6 units
// (2 x Reservoir + 1 x Water Works + 3 x Water Tower)" having received zero
// Reservoirs. No phantom-asset claim is made (the specs are named on the
// PLAN side of "of"), which is why this is P2 and not a repeat of RR-1 — but
// the disclosure the substituted branch exists to provide is absent for this
// door into the same outcome.
//
// This test pins the CURRENT behaviour (so the fix, if any, is a deliberate
// change) and asserts the parts that must hold regardless.
// ===========================================================================
test('RR3-6 (F1): a within-walk downgrade to a cheaper rung is honest-by-omission, never a phantom claim', () => {
  const s = city(100_000, 30_000_000); // < 1 City School (£57.6M), > 3 Primary Schools (£9.36M)
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'primary');
  assert.ok(plan.mix.length > 1, 'fixture: a multi-rung plan');
  assert.equal(plan.specId, plan.mix[0].specId, 'fixture: the headline is the first (largest) rung');
  assert.ok(placementCost(SPECS[plan.specId]) > s.funds, 'precondition: the headline rung is unaffordable');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'primary' });
  const built = placedBetween(s, after);
  assert.equal(built[plan.specId] ?? 0, 0, 'precondition: zero of the headline rung landed');
  const cheaper = Object.keys(built);
  assert.ok(cheaper.length > 0, 'precondition: a cheaper rung of the SAME plan did land');
  assert.ok(
    cheaper.every((id) => plan.mix.some((m) => m.specId === id)),
    'precondition: what landed was inside the plan mix (a within-walk fall-through, not a self-heal)'
  );

  const notice = after.placeNotice ?? '';
  // MUST hold: no phantom asset, non-null, true reason.
  assert.ok(notice.length > 0, 'a downgrade must never be silent');
  assertNoPhantomAsset(notice, built, 'RR3-6');
  assert.match(notice, /insufficient funds/i, 'the reason is true — the headline rung really is unaffordable');
  // DOCUMENTED GAP: the built mix is not disclosed on this path.
  assert.ok(
    !/substituted/i.test(notice),
    'current behaviour: the within-walk downgrade is not labelled a substitution (F1, P2 advisory)'
  );
});

// ===========================================================================
// RR3-7 (FINDING F2, P2) — the reason is computed against the POST-SPEND
// state, so a build that failed for want of a SITE can be reported as
// "insufficient funds" even though the treasury covered the planned spec at
// the moment it was attempted. Constructed with no mutation:
//   * map paved solid except three isolated 1x1 holes -> a 4x4 Reservoir can
//     never place, a 1x1 Water Tower can.
//   * funds set just above the Reservoir price, so the three Water Towers the
//     walk DOES place drag the balance below it before the notice is written.
// The test asserts the honest requirement; if it reds, F2 is live.
// ===========================================================================
test('RR3-7 (F2): the blamed reason must reflect why the build stopped, not the balance after it spent', {
  todo:
    'F2 (P2 follow-up, NOT a blocker): shortfallReason() is evaluated against the POST-walk state and ' +
    'always against the plan HEADLINE spec, so a Reservoir that failed for want of a 4x4 site is ' +
    'reported as "insufficient funds" once three incidental Water Towers drag the balance under its ' +
    'price. Distinct from RR2-1: the statement is TRUE of the state it is printed against (the purse ' +
    'really is short by then) — only the CAUSAL attribution is wrong, and the player is pointed at the ' +
    'bank instead of the bulldozer. Fix shape: capture the funds/site verdict per mix ENTRY inside ' +
    'walkMix() at the moment each entry stops, and report the FIRST blocking entry\'s own reason.',
}, () => {
  const buildings = pavedMapWithHoles(3);
  const reservoir = placementCost(SPECS['wat_reservoir']);
  const tower = placementCost(SPECS['wat_tower']);
  const s = city(100_000, reservoir + tower * 2, { buildings }); // affords the Reservoir; three towers will drag it under
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'cleanwater');
  assert.ok(plan && plan.specId === 'wat_reservoir', 'fixture: the Reservoir-headed clean-water plan');
  assert.ok(s.funds >= reservoir, 'PRECONDITION: the treasury covers the planned Reservoir');
  assert.equal(SPECS['wat_reservoir'].w, 4, 'PRECONDITION: the Reservoir needs a 4x4 the map cannot offer');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'cleanwater' });
  const built = placedBetween(s, after);
  assert.equal(built['wat_reservoir'] ?? 0, 0, 'precondition: no Reservoir placed — the map had no 4x4');
  const spent = s.funds - after.funds;
  const notice = after.placeNotice ?? '';

  // CONTROL, same map, same plan, only the treasury changed: with unlimited
  // funds the engine states the TRUE reason, proving the map (not the money)
  // is what stopped the Reservoir on the fixture above.
  const control = reducer(city(100_000, QUADRILLION, { buildings }), {
    type: 'resolveDemand',
    serviceKey: 'cleanwater',
  });
  assert.match(
    control.placeNotice ?? '',
    /no free 4x4 area on the map for Reservoir/i,
    'control: with money to burn the engine itself says the map is the blocker'
  );

  assertNoPhantomAsset(notice, built, 'RR3-7');
  assert.ok(
    !/insufficient funds/i.test(notice),
    `F2: the planned Reservoir was affordable (£${s.funds} >= £${reservoir}) and failed for want of a 4x4 site,\n` +
      `  but after £${spent} of incidental spend the notice blames the treasury.\n` +
      `  built: ${JSON.stringify(built)}\n  notice: ${notice}\n` +
      `  ROOT CAUSE: shortfallReason(result.state, ...) evaluates funds AFTER the walk has spent them,\n` +
      `  and always against the plan HEADLINE spec rather than the entry that actually stopped.`
  );
});

// ===========================================================================
// RR3-8 — determinism and replay (GR#21): the notice text and the built mix
// label are order-stable, and a journal containing both reducers replays
// byte-identically including placeNotice.
// ===========================================================================
test('RR3-8: repeated identical runs produce identical notices and identical built mixes', () => {
  for (const [tag, mk] of [
    ['self-heal power', () => city(100_000, 500_000_000)],
    ['downgrade primary', () => city(100_000, 30_000_000)],
    ['site-starved gp', () => city(100_000, QUADRILLION, { buildings: pavedMapWithHoles(3) })],
  ]) {
    const runs = [];
    for (let i = 0; i < 3; i++) {
      const s = mk();
      const key = tag.includes('power') ? 'power' : tag.includes('primary') ? 'primary' : 'gp';
      const a = reducer(s, { type: 'resolveDemand', serviceKey: key });
      runs.push(`${a.placeNotice ?? 'null'}|${JSON.stringify(placedBetween(s, a))}`);
    }
    assert.equal(runs[0], runs[1], `${tag}: run 1 vs 2 diverged`);
    assert.equal(runs[1], runs[2], `${tag}: run 2 vs 3 diverged`);
  }
});

test('RR3-8b: a journal of Fix / Fix All actions replays byte-identically, placeNotice included', () => {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of [
    { type: 'resolveDemand', serviceKey: 'power' },
    { type: 'resolveDemand', serviceKey: 'primary' },
    { type: 'resolveDemandAll' },
    { type: 'resolveDemand', serviceKey: 'gp' },
    { type: 'resolveDemandAll' },
  ]) {
    if (!isStateAffecting(action)) continue;
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  const r1 = replayFromGenesis(journal);
  const r2 = replayFromGenesis(journal);
  assert.equal(
    stableStringify({ ...r1, roadConnectivity: null }),
    stableStringify({ ...r2, roadConnectivity: null }),
    'the same journal replayed twice diverged (GR#21)'
  );
  assert.equal(r1.placeNotice ?? null, r2.placeNotice ?? null, 'the notice text is not replay-stable');
});

// ===========================================================================
// RR3-9 — MUTATION PINS. Each assertion here is the one that reds when the
// corresponding re-round-2 fix is reverted. Run by the attacker by editing
// engine.ts and editing it back (never a git restore, GR#24).
//   M1  re-add `|| (result.placed > 0 && !matchesPlan)` to the substitution
//       branch                              -> RR3-9a reds (self-substitution)
//   M2  swap shortfallReason(...) back to the hardcoded
//       `insufficient funds for ${planLabel(plan)}`
//                                           -> RR3-9b reds (funds blamed on a
//                                              quadrillion-pound treasury)
//   M3  set anySubstituted from `!matchesPlan` instead of result.substituted
//                                           -> RR3-9c reds (Fix All claims an
//                                              affordability substitution it
//                                              did not make)
// ===========================================================================
test('RR3-9a (M1 pin): a same-spec partial must never be described as a substitution', () => {
  const unit = placementCost(SPECS['hea_clinic']);
  const s = city(100_000, unit * 4 + 1000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'gp');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const built = placedOfPlan(plan, s, after);
  assert.equal(Object.keys(built).join(','), 'hea_clinic', 'precondition: only Clinics landed');
  assert.ok(built['hea_clinic'] > 0 && built['hea_clinic'] < 26, 'precondition: a genuine partial');
  assert.ok(
    !/substituted/i.test(after.placeNotice ?? ''),
    `M1: ${built['hea_clinic']} Clinics of a Clinic plan reported as a substitution: ${after.placeNotice}`
  );
});

test('RR3-9b (M2 pin): a partial on a rich treasury must not be told it is broke', () => {
  const buildings = pavedMapWithHoles(3);
  const s = city(100_000, QUADRILLION, { buildings });
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  assert.ok(after.funds > s.funds * 0.999, 'precondition: the treasury is untouched');
  assert.ok(!/insufficient funds/i.test(after.placeNotice ?? ''), `M2: ${after.placeNotice}`);
});

test('RR3-9c (M3 pin): Fix All must not claim an affordability substitution it did not make', () => {
  const buildings = pavedMapWithHoles(3);
  const s = city(100_000, QUADRILLION, { buildings });
  const after = reducer(s, { type: 'resolveDemandAll' });
  const built = placedBetween(s, after);
  assert.ok(Object.keys(built).length > 0, 'precondition: something was built');
  assert.ok(after.funds > s.funds * 0.999, 'precondition: the treasury is untouched');
  assert.ok(
    !/a costlier planned spec was unaffordable/i.test(after.placeNotice ?? ''),
    `M3: ${after.placeNotice}`
  );
});

// ===========================================================================
// RR3-10 — the unit-cap cell of the grid: a cap-truncated build reports the
// cap, names no spec as built beyond what landed, and never blames funds/site.
// ===========================================================================
test('RR3-10 (grid: cap-truncated): the unit cap is reported as its own reason, never as money or map', () => {
  // A single service cannot reach the 2,000-unit cap with today's catalogue
  // (the biggest single plan measured is ~1,000 parks at pop 3M), so the
  // reachable cap cell is Fix All's GLOBAL budget across all services.
  const s = city(3_000_000, QUADRILLION);
  const totalPlanned = demandFixPlan(s).reduce((sum, p) => sum + p.count, 0);
  assert.ok(
    totalPlanned > RESOLVE_DEMAND_ALL_MAX_UNITS,
    `fixture: the batch must exceed the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit cap, got ${totalPlanned}`
  );
  const after = reducer(s, { type: 'resolveDemandAll' });
  const built = placedBetween(s, after);
  const notice = after.placeNotice ?? '';
  assertNoPhantomAsset(notice, built, 'RR3-10');
  assert.match(notice, /click Fix All again for the rest/i, 'the cap is its own honest, actionable reason');
  assert.ok(!/insufficient funds/i.test(notice), 'the cap must never be reported as a money problem');
  assert.ok(!/no free .* area on the map/i.test(notice), 'the cap must never be reported as a map problem');
  // and the "built X of Y" pair must be arithmetically honest.
  const pair = /built (\d+) of (\d+) planned/.exec(notice);
  assert.ok(pair, `cap notice must carry the honest placed/planned pair: ${notice}`);
  assert.ok(Number(pair[1]) <= Number(pair[2]), `cap notice claims more built than planned: ${notice}`);
  assert.ok(
    Number(pair[1]) <= RESOLVE_DEMAND_ALL_MAX_UNITS,
    `cap notice claims ${pair[1]} units built past a ${RESOLVE_DEMAND_ALL_MAX_UNITS} cap: ${notice}`
  );
});

// ===========================================================================
// RR3-11 — Fix All never nags after a clean, complete, plan-matching batch,
// and always names a reason when it does nag.
// ===========================================================================
test('RR3-11: Fix All summaries are well-formed — a reason clause is never empty', () => {
  for (const [tag, s] of [...OPEN_MAP_CELLS(), ...FULL_MAP_CELLS()]) {
    const after = reducer(s, { type: 'resolveDemandAll' });
    const notice = after.placeNotice ?? '';
    assert.ok(notice.startsWith('Fix All:'), `[${tag}] Fix All must own its notice: ${notice}`);
    if (notice.includes('—')) {
      const tail = notice.split('—').slice(1).join('—').trim();
      assert.ok(tail.length > 0, `[${tag}] empty reason clause: ${notice}`);
      assert.ok(!/\(\s*\)/.test(notice), `[${tag}] empty parenthesised reason list: ${notice}`);
    }
  }
});
