// attack-largest-first-reround2.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND 2
// (GR#23, attacker != author, fresh eyes) against BUG-685/BUG-686 after
// re-round 1's REJECT was answered with the builtMix reporting rework
// (walkMix() returns builtMix, placePlanItem() returns `substituted`,
// builtMixLabel()/builtMixMatchesPlan(), and the new substitution branch in
// 'resolveDemand'/'resolveDemandAll').
//
// VERDICT: REJECT (reporting, again — the placement money mechanism is
// untouched and still sound; re-round 1's RR-4..RR-9 all re-verified green).
//
// WHAT THE REWORK FIXED (pinned green below so it can never regress):
//   * RR-1 phantom asset is GONE. Across a swept grid of populations x
//     treasuries x every service, on BOTH reducers, no notice ever names a
//     spec as BUILT of which zero units were placed (RR2-8).
//   * RR-2 silent substitution is GONE. A self-heal substitution always
//     surfaces a notice naming exactly the substituted mix (RR2-5, RR2-6).
//   * A full build that matches the plan spec-for-spec still returns a null
//     notice; zero-placed still reports the true reason (RR2-3, RR2-4).
//
// LEAD DEFECT (RR2-1, P1, D2/GR#17 class — the SAME class BUG-606's own
// independent round REJECTED for, reintroduced in the new branch):
//   'resolveDemand's new substitution branch hardcodes its reason string:
//       `... — insufficient funds for ${planLabel(plan)}`
//   but the branch is entered for EVERY partial build, not just substitutions
//   (`result.placed > 0 && !matchesPlan` is true of any short walk). So a city
//   holding ONE QUADRILLION POUNDS that placed 3 of 26 Clinics because the map
//   is full is told "insufficient funds for 26 x Clinic". Reproduced with no
//   mutation: full 440x260 map, three 1x1 holes punched in the middle, funds
//   1e15, £9.72M spent, 999,999,990,280,000 left in the bank.
//   The regression is provable by bisecting on `placed` alone with EVERYTHING
//   ELSE held constant on the same fixture:
//       0 holes -> placed 0 -> "Placed 0 of 26 x Clinic — no free 1x1 area on
//                               the map for Clinic"          (honest, old path)
//       3 holes -> placed 3 -> "... substituted 3 x Clinic, 3 of 26 units
//                               total — insufficient funds"  (a lie, new path)
//   i.e. the notice gets LESS honest the moment the build partially succeeds.
//   shortfallReason() — which exists precisely to tell funds from
//   administration from no-site, and whose own doc comment cites this exact
//   defect — is not consulted on this branch at all.
//
// SECOND DEFECT (RR2-2, P2, false claim): the same over-broad branch condition
//   makes the game report a substitution that never happened, in the game's
//   own words, substituting a spec for ITSELF:
//       "Fix (9 x Kindergarten): substituted 3 x Kindergarten, 3 of 9 units
//        total — insufficient funds for 9 x Kindergarten"
//   Nothing was substituted: 9 Kindergartens were planned and 3 Kindergartens
//   were built. `placePlanItem()` already returns an accurate `substituted`
//   boolean (it is only ever true for a genuine self-heal); the branch ORs it
//   with the count-mismatch test and so throws that accuracy away. BUG-686's
//   own headline notice is the worked example, which is why this verdict is
//   recorded against BUG-686 as well as BUG-685.
//   'resolveDemandAll' has the milder form of the same fault: it sets
//   `anySubstituted` from `!matchesPlan`, so a pure partial appends the reason
//   "a costlier planned spec was unaffordable — a cheaper mix was substituted"
//   to a batch that substituted nothing and had 1e15 in the bank (RR2-2b).
//   It is milder only because it ADDS to the true reasons instead of
//   REPLACING them, as 'resolveDemand' does.
//
// ADJUDICATED, NOT A REJECT (documented by RR2-7 / RR2-11):
//   * cap-before-substitution ordering: the cap wins, and the losing
//     substitution fact IS dropped. But (a) the cap message names no spec at
//     all in 'resolveDemandAll' and names only the plan headline in
//     'resolveDemand' — where a capped walk always placed >= 1 of that
//     headline — so there is NO phantom-asset regression either way, and
//     (b) the combination "capped AND substituted" is unreachable with the
//     current catalogue: the self-heal walk must land > 2000 units, which
//     needs a service ladder whose headline costs more than 2000x its
//     cheapest affordable rung; the widest ratio in data.ts is power's
//     pow_hydro £5.0bn / pow_wind £3.6M = 1,389x, and largestFirstFill()
//     spends down the intermediate rungs first, cutting the unit count
//     further. Latent, P3, worth a follow-up, not a blocker. RR2-11 pins the
//     ratio so a future catalogue edit that makes it reachable reds here.
//   * "N of M units total" arithmetic: the brief's worried example
//     ("3 of 1 units total") does NOT occur — the suffix is emitted only when
//     `placed < plan.count`, so the count pair is always small-of-large and
//     honest. A substitution that OVERSHOOTS the plan's unit count prints no
//     count at all ("substituted 1 x Onshore Wind Farm + 2 x Wind Turbine —
//     insufficient funds for 1 x Three Gorges Dam"). Honest per unit, but it
//     never says the CAPACITY gap is still ~94% open; that is a known,
//     separately-sanctioned gap, not this round's finding.
//
// MUTATIONS: none needed for the two REJECTs — both reproduce on stock source.
// Re-round 1's own mutation battery (M1 largestFirstFill budget gate, M2
// placePlanItem self-heal, RR-9 bare-count comparison) was re-run from this
// worktree and all three still red; RR2-12 re-asserts the money mechanism
// invariants those mutations attack, so this file reds under them too.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, xpForLevel } from '../src/sim/engine.ts';
import { SPECS, demandFixPlan, placementCost, MAP_W, MAP_H, RESOLVE_DEMAND_ALL_MAX_UNITS } from '../src/sim/data.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, stableStringify } from '../src/sim/genesisReplay.ts';

const city = (population, funds, extra = {}) => ({
  ...initialState(),
  population,
  unlockedAll: true,
  funds,
  administrationState: null,
  ...extra,
});

/** A 440x260 map paved solid with 1x1 road tiles except `holes` single free
 *  tiles punched in the middle (the edges are excluded from findSpot's scan
 *  window, so a hole at the border is not a buildable site). Gives a fixture
 *  where the ONLY thing that can stop a build is the absence of free area —
 *  funds and administration mode are both provably not in play. */
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

function placedSince(state, before) {
  const out = {};
  for (const b of state.buildings.slice(before)) out[b.spec] = (out[b.spec] ?? 0) + 1;
  return out;
}

const QUADRILLION = 1e15;

// ===========================================================================
// RR2-1 (LEAD DEFECT, REJECT): the partial-build notice blames a treasury that
// is provably untouched.
// ===========================================================================
test(
  'RR2-1 REJECT: a partial build must report its TRUE reason, never a hardcoded "insufficient funds"',
  () => {
    const buildings = pavedMapWithHoles(3);
    const s = city(100_000, QUADRILLION, { buildings });
    const plan = demandFixPlan(s).find((p) => p.serviceKey === 'gp');
    assert.ok(plan && plan.count > 3, 'fixture must plan more clinics than there are holes');

    const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
    const built = placedSince(after, buildings.length);
    const spent = s.funds - after.funds;

    // The attack preconditions, all derived (GR#15): a genuine PARTIAL, with
    // an absurd treasury barely dented.
    assert.equal(built[plan.specId], 3, 'precondition: exactly the three holes were filled');
    assert.ok(after.funds > s.funds * 0.999, `precondition: the treasury is untouched (spent £${spent} of £${s.funds})`);

    const notice = after.placeNotice ?? '';
    assert.ok(
      !/insufficient funds/i.test(notice),
      `the game blamed the treasury for a map problem.\n` +
        `  funds before: £${s.funds}, after: £${after.funds} (spent £${spent})\n` +
        `  notice: ${notice}\n` +
        `  ROOT CAUSE: 'resolveDemand's substitution branch hardcodes ` +
        `"insufficient funds for <planLabel>" instead of calling shortfallReason(), ` +
        `and the branch fires for EVERY partial (placed > 0 && !matchesPlan), not just substitutions.`
    );
    // The honest reason the SAME fixture produces one hole earlier:
    assert.match(
      notice,
      /no free .* area on the map/i,
      'a build stopped by a full map must say so — that is exactly what shortfallReason() returns'
    );
  }
);

// The bisect that proves RR2-1 is a REGRESSION of the notice, not a property
// of the fixture: identical map, identical treasury, only `placed` differs.
test('RR2-1b: the zero-placed path on the SAME fixture is honest — the lie appears only once a build partially succeeds', () => {
  const solid = pavedMapWithHoles(0);
  const s = city(100_000, QUADRILLION, { buildings: solid });
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  assert.equal(after.buildings.length, solid.length, 'precondition: nothing could be placed');
  assert.match(
    after.placeNotice ?? '',
    /no free .* area on the map/i,
    'the pre-existing zero-placed path already reports the true, actionable reason'
  );
  assert.ok(!/insufficient funds/i.test(after.placeNotice ?? ''), 'and does not blame funds');
});

// ===========================================================================
// RR2-2 (REJECT): the game claims a substitution that did not happen —
// substituting a spec for itself.
// ===========================================================================
test(
  'RR2-2 REJECT: a partial build of the PLANNED spec must not be reported as a substitution',
  () => {
    // BUG-686's own service, its own headline notice. Fund exactly three
    // Kindergartens of a nine-Kindergarten plan. Locked to xp level 3
    // (civic-tier rebase fix, FEAT-2326609772, 2026-09-05): edu_nursery
    // unlocks at level 2, but the catalogue since gained a same-stage
    // successor, edu_nursery_city (unlock level 4) — at unlockedAll god-mode
    // it would also be a nursery-family candidate and this fixture's whole
    // point (a plain same-spec partial build, nothing to substitute) needs a
    // single-spec plan, same as when this test was written.
    const unit = placementCost(SPECS['edu_nursery']);
    const s = city(100_000, unit * 3 + 1000, { unlockedAll: false, xp: xpForLevel(3) });
    const plan = demandFixPlan(s).find((p) => p.serviceKey === 'nursery');
    assert.ok(plan && plan.mix.length === 1 && plan.mix[0].specId === 'edu_nursery', 'fixture: a single-spec Kindergarten plan');
    assert.ok(plan.count > 3, 'fixture: the plan wants more than the treasury funds');

    const after = reducer(s, { type: 'resolveDemand', serviceKey: 'nursery' });
    const built = placedSince(after, s.buildings.length);
    assert.equal(built['edu_nursery'], 3, 'precondition: three of the PLANNED spec were built, nothing else');
    assert.equal(
      Object.keys(built).filter((id) => SPECS[id]?.kind === SPECS['edu_nursery'].kind).length,
      1,
      'precondition: no other education spec was touched — there is nothing a substitution could be'
    );

    assert.ok(
      !/substituted/i.test(after.placeNotice ?? ''),
      `the game reported substituting a spec for itself:\n` +
        `  notice: ${after.placeNotice}\n` +
        `  built:  ${JSON.stringify(built)} against a plan of ${plan.count} x ${SPECS[plan.specId].name}\n` +
        `  ROOT CAUSE: the branch ORs placePlanItem()'s accurate \`substituted\` flag with a bare ` +
        `count-mismatch test, discarding the accuracy the flag was added to provide.`
    );
  }
);

test(
  'RR2-2b REJECT: Fix All must not append a "cheaper mix was substituted" reason to a batch that substituted nothing',
  () => {
    const buildings = pavedMapWithHoles(3);
    const s = city(100_000, QUADRILLION, { buildings });
    const after = reducer(s, { type: 'resolveDemandAll' });
    const built = placedSince(after, buildings.length);
    assert.ok(Object.keys(built).length > 0, 'precondition: something was built');
    assert.ok(after.funds > s.funds * 0.999, 'precondition: the treasury is untouched');
    assert.ok(
      !/unaffordable|substituted/i.test(after.placeNotice ?? ''),
      `Fix All claimed an affordability substitution with £${after.funds} in the bank:\n  ${after.placeNotice}`
    );
  }
);

// ===========================================================================
// RR2-3..RR2-6: the notice-honesty grid on 'resolveDemand'. These are the
// cells the rework GOT RIGHT — pinned so they cannot regress.
// ===========================================================================
test('RR2-3 (grid: full build matching plan): a spec-for-spec complete build stays silent', () => {
  const s = city(100_000, QUADRILLION);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  assert.equal(
    after.buildings.filter((b) => b.spec === plan.specId).length,
    plan.count,
    'precondition: the whole plan was built exactly as planned'
  );
  assert.equal(after.placeNotice ?? null, null, 'a plan built exactly as promised must not nag the player');
});

test('RR2-4 (grid: zero placed): reports the true reason, never silence', () => {
  for (const [tag, s, key, expect] of [
    ['no funds', city(100_000, 0), 'nursery', /insufficient funds/i],
    ['administration mode', city(100_000, QUADRILLION, { administrationState: { since: 1, reason: 'test' } }), 'gp', /Administration Mode/i],
    ['no map room', city(100_000, QUADRILLION, { buildings: pavedMapWithHoles(0) }), 'gp', /no free .* area on the map/i],
  ]) {
    const before = s.buildings.length;
    const after = reducer(s, { type: 'resolveDemand', serviceKey: key });
    assert.equal(after.buildings.length, before, `${tag}: precondition, nothing placed`);
    assert.match(after.placeNotice ?? '', /^Placed 0 of /, `${tag}: must lead with the honest count`);
    assert.match(after.placeNotice ?? '', expect, `${tag}: must carry the true cause`);
  }
});

test('RR2-5 (grid: substitution covering the full unit count): names the substitutes, never the headline as built', () => {
  const s = city(100_000, 500_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  const headline = SPECS[plan.specId];
  assert.ok(placementCost(headline) > s.funds, 'precondition: the headline spec is flatly unaffordable');

  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  const built = placedSince(after, s.buildings.length);
  assert.equal(built[plan.specId] ?? 0, 0, 'precondition: zero of the headline spec exist');
  const placedPower = Object.entries(built).filter(([id]) => SPECS[id]?.kind === 'power');
  assert.ok(placedPower.length > 0, 'precondition: the self-heal substituted real units');

  const notice = after.placeNotice ?? '';
  assert.ok(notice.length > 0, 'RR-2 regression: a substitution must never be silent');
  // The "substituted <list>" clause must name every placed spec and ONLY
  // placed specs — the headline may appear only in the trailing reason.
  const clause = /substituted ([^—]*?)(?:,\s*\d+ of|\s+—)/.exec(notice);
  assert.ok(clause, `notice must carry a "substituted <mix>" clause: ${notice}`);
  for (const [id, n] of placedPower) {
    assert.ok(clause[1].includes(`${n} x ${SPECS[id].name}`), `${SPECS[id].name} x${n} was built but is missing from: ${clause[1]}`);
  }
  assert.ok(!clause[1].includes(headline.name), `RR-1 regression: the unbuilt headline "${headline.name}" is named as built in: ${clause[1]}`);
});

test('RR2-6 (grid: substitution covering only part of the shortfall): still names only what landed', () => {
  const s = city(100_000, 50_000_000);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
  const built = placedSince(after, s.buildings.length);
  const delivered = Object.entries(built).reduce((sum, [id, n]) => sum + (SPECS[id]?.mw ?? 0) * n, 0);
  assert.ok(delivered > 0, 'precondition: something was built');
  assert.ok(delivered * 4 < plan.need - plan.have, 'precondition: the substitution barely dents the capacity gap');
  const notice = after.placeNotice ?? '';
  assert.ok(notice.length > 0, 'a token substitution against a huge gap must not read as success');
  assert.equal(after.buildings.filter((b) => b.spec === plan.specId).length, 0, 'the headline was not built');
  assert.ok(
    !new RegExp(`substituted[^—]*${SPECS[plan.specId].name}`).test(notice),
    `the unbuilt headline appears inside the "substituted" clause: ${notice}`
  );
});

test('RR2-7 (grid: cap-truncated plan): the cap is its own reason and names only specs that were placed', () => {
  const s = city(20_000_000, QUADRILLION);
  const plan = demandFixPlan(s).find((p) => p.serviceKey === 'gp');
  assert.ok(plan.count > RESOLVE_DEMAND_ALL_MAX_UNITS, 'fixture: the plan must exceed the per-click cap');
  const after = reducer(s, { type: 'resolveDemand', serviceKey: 'gp' });
  const built = placedSince(after, s.buildings.length);
  assert.equal(built[plan.specId], RESOLVE_DEMAND_ALL_MAX_UNITS, 'precondition: the cap, not funds/site, stopped this');
  const notice = after.placeNotice ?? '';
  assert.match(notice, /per-click build limit/, 'the cap must be reported as itself');
  assert.ok(!/insufficient funds/i.test(notice), 'the cap must never be reported as a funds problem');
  assert.ok(!/substituted/i.test(notice), 'a cap truncation is not a substitution — the ordering fix holds');
});

// ===========================================================================
// RR2-8: the RR-1 phantom-asset invariant, generalised. This is the assertion
// that would have caught the original defect wherever it appeared, swept over
// the whole reachable space rather than one fixture.
// ===========================================================================
test('RR2-8: across every service x population x treasury, no notice names a spec as BUILT that was not placed', () => {
  const POPS = [50_000, 100_000, 200_000, 1_000_000];
  const FUNDS = [0, 1_000_000, 50_000_000, 500_000_000, 4_900_000_000, QUADRILLION];
  /** Every spec name the notice claims was BUILT — i.e. the "built <list>"
   *  segment of a Fix All summary and the "substituted <list>" clause of a
   *  Fix notice. Deliberately NOT the trailing reason, which legitimately
   *  names the unaffordable plan. */
  function claimedBuilt(notice) {
    if (!notice) return [];
    const seg = /^Fix All: built (.*?)(?: — |$)/.exec(notice) ?? /substituted (.*?)(?:, \d+ of |$| — )/.exec(notice);
    if (!seg) return [];
    return seg[1].split(' + ').flatMap((p) => p.split(', ')).map((p) => p.replace(/^\d+ x /, '').trim()).filter(Boolean);
  }
  const nameToSpec = new Map(Object.values(SPECS).map((sp) => [sp.name, sp.id]));

  let checked = 0;
  for (const pop of POPS) {
    for (const funds of FUNDS) {
      const base = city(pop, funds);
      const services = demandFixPlan(base).map((p) => p.serviceKey);
      for (const action of [{ type: 'resolveDemandAll' }, ...services.map((k) => ({ type: 'resolveDemand', serviceKey: k }))]) {
        const after = reducer(base, action);
        const built = placedSince(after, base.buildings.length);
        for (const name of claimedBuilt(after.placeNotice)) {
          const id = nameToSpec.get(name);
          assert.ok(id, `notice named an unknown spec "${name}" in: ${after.placeNotice}`);
          assert.ok(
            (built[id] ?? 0) > 0,
            `PHANTOM ASSET (RR-1 class): pop=${pop} funds=£${funds} ${JSON.stringify(action)}\n` +
              `  claimed built: ${name} (${id})\n  actually placed: ${JSON.stringify(built)}\n  notice: ${after.placeNotice}`
          );
          checked++;
        }
      }
    }
  }
  assert.ok(checked > 20, `the sweep must actually exercise notices (only ${checked} claims seen)`);
});

// ===========================================================================
// RR2-9: determinism of the new label (GR#21) — builtMix order and therefore
// the notice text must not depend on incidental state ordering, and must
// survive a real genesis replay byte-identically (placeNotice is part of the
// replayed state, so a wobbly label breaks replay identity outright).
// ===========================================================================
test('RR2-9: the builtMix label is order-stable and replays byte-identically', () => {
  // (a) shuffling the buildings array (same buildings, different order) must
  //     not move a single character of the notice.
  const seed = city(100_000, 50_000_000);
  const primed = reducer(seed, { type: 'resolveDemand', serviceKey: 'gp' });
  const shuffled = { ...primed, buildings: [...primed.buildings].reverse() };
  const a = reducer(primed, { type: 'resolveDemand', serviceKey: 'power' });
  const b = reducer(shuffled, { type: 'resolveDemand', serviceKey: 'power' });
  assert.equal(a.placeNotice, b.placeNotice, 'the notice moved when only the buildings array ORDER changed');

  // (b) the notice must be a pure function of the state it is computed from —
  //     recomputing from an independently-rebuilt copy of the same state
  //     (structurally identical, every array/object freshly allocated) must
  //     produce the identical string.
  const rebuilt = JSON.parse(JSON.stringify(primed));
  assert.equal(
    reducer(rebuilt, { type: 'resolveDemand', serviceKey: 'power' }).placeNotice,
    a.placeNotice,
    'the notice depends on object identity, not on state VALUE — it cannot survive a save/load boundary'
  );

  // (c) a real journal replay: placeNotice is part of the replayed state, so a
  //     non-deterministic label would break replay identity outright.
  let state = initialState();
  let journal = emptyJournal();
  for (const action of [
    { type: 'debugFunds', amount: 50_000_000 },
    { type: 'resolveDemandAll' },
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
    'the same journal replayed twice diverged (GR#21) — placeNotice included'
  );
  assert.equal(r1.placeNotice ?? null, r2.placeNotice ?? null, 'the notice text is not replay-stable');
});

// ===========================================================================
// RR2-10 (BUG-686): the Kindergarten notice specifically — it rides the same
// branch and is the worked example in the re-round-1 verdict.
// ===========================================================================
test('RR2-10 (BUG-686): the Kindergarten notice names only Kindergartens that exist', () => {
  const unit = placementCost(SPECS['edu_nursery']);
  for (const funds of [0, unit + 1000, unit * 3 + 1000, QUADRILLION]) {
    const s = city(100_000, funds);
    const plan = demandFixPlan(s).find((p) => p.serviceKey === 'nursery');
    const after = reducer(s, { type: 'resolveDemand', serviceKey: 'nursery' });
    const n = after.buildings.filter((b) => b.spec === 'edu_nursery').length;
    const notice = after.placeNotice ?? '';
    if (notice.includes('x Kindergarten') && /substituted|built/.test(notice)) {
      const m = /(\d+) x Kindergarten/.exec(/substituted ([^—]*)/.exec(notice)?.[1] ?? '');
      if (m) assert.equal(Number(m[1]), n, `notice claims ${m[1]} Kindergartens, ${n} exist — ${notice}`);
    }
    // BUG-686's own ladder invariant: a plan of thousands of toy Kindergartens
    // is exactly what the fix removed — the plan must stay sane.
    assert.ok(plan.count < 100, `BUG-686 regression: a 100k city planned ${plan.count} Kindergartens`);
  }
});

// ===========================================================================
// RR2-11: the cap-vs-substitution adjudication, pinned. The combination is
// currently UNREACHABLE; this test pins the catalogue property that makes it
// so, so that a future ladder edit which makes it reachable reds here and the
// dropped-substitution-fact gap gets addressed before a player can see it.
// ===========================================================================
test('RR2-11: "capped AND substituted" never occurs — a capped notice always names a headline that WAS placed', () => {
  // Behavioural half: sweep the space where a self-heal is possible at all
  // (headline unaffordable) at populations large enough for the cap to bite,
  // and assert the cap branch never fires on a build containing zero of the
  // headline it names. This is the assertion that would catch the dropped
  // substitution fact the moment the catalogue makes it reachable.
  let capSeen = 0;
  for (const pop of [1_000_000, 5_000_000, 20_000_000]) {
    for (const funds of [4_900_000_000, 1_500_000_000, 300_000_000, 40_000_000, 4_000_000]) {
      const s = city(pop, funds);
      for (const plan of demandFixPlan(s)) {
        const after = reducer(s, { type: 'resolveDemand', serviceKey: plan.serviceKey });
        const notice = after.placeNotice ?? '';
        if (!notice.includes('per-click build limit')) continue;
        capSeen++;
        const headlineBuilt = after.buildings.filter((b) => b.spec === plan.specId).length;
        assert.ok(
          headlineBuilt > 0,
          `capped notice names "${SPECS[plan.specId].name}" but zero were placed ` +
            `(pop ${pop}, £${funds}): ${notice}\n` +
            `  The cap branch is checked BEFORE the substitution branch and reports planCountLabel(plan), ` +
            `so a capped SELF-HEAL resurrects the RR-1 phantom asset and drops the substitution fact (GR#17). ` +
            `Report both facts.`
        );
      }
    }
  }
  assert.ok(capSeen > 0, 'the sweep must actually reach the cap branch');

  // Catalogue half: the property that makes the combination unreachable today
  // — a self-heal must land > RESOLVE_DEMAND_ALL_MAX_UNITS units, which needs
  // a headline costing more than that many of the cheapest CAPACITY-providing
  // rung (pow_substation and friends carry no capacity and are never picked).
  const capacityPower = Object.values(SPECS).filter((sp) => sp.kind === 'power' && (sp.mw ?? 0) > 0);
  const costs = capacityPower.map(placementCost).filter((c) => c > 0);
  const ratio = Math.max(...costs) / Math.min(...costs);
  assert.ok(
    ratio < RESOLVE_DEMAND_ALL_MAX_UNITS,
    `the power ladder now spans ${ratio.toFixed(0)}x (>= the ${RESOLVE_DEMAND_ALL_MAX_UNITS}-unit cap): ` +
      `a self-heal can exceed the cap in one call, making "capped AND substituted" reachable. ` +
      `Fix the cap/substitution reporting order before shipping this ladder.`
  );
});

// ===========================================================================
// RR2-12: the money mechanism, re-verified independently of re-round 1's file.
// These are the assertions re-round 1's M1/M2 mutations red; the reporting
// rework must not have disturbed any of them.
// ===========================================================================
test('RR2-12: placement invariants unchanged by the reporting rework', () => {
  for (const [pop, funds] of [
    [100_000, 50_000_000],
    [200_000, 5_000_000_000],
    [1_000_000, 500_000_000],
  ]) {
    const s = city(pop, funds);
    const plan = demandFixPlan(s).find((p) => p.serviceKey === 'power');
    // fall-through / self-heal: an unaffordable headline still builds something
    const one = reducer(s, { type: 'resolveDemand', serviceKey: 'power' });
    if (placementCost(SPECS[plan.specId]) > s.funds) {
      assert.ok(
        one.buildings.filter((b) => SPECS[b.spec]?.kind === 'power').length > 0,
        `pop ${pop}/£${funds}: the affordability fall-through placed nothing`
      );
    }
    assert.ok(one.funds >= 0, 'never overspend');
    // conservation: every pound leaves a building behind
    const all = reducer(s, { type: 'resolveDemandAll' });
    assert.ok(all.funds >= 0, 'Fix All never goes negative');
    // Accounting note (re-round 1's RR-6): auto-laid road connectors are
    // billed through a path whose per-tile charge is not placementCost(), so
    // the invariant is spent >= catalogue bill and spent <= treasury — what
    // must never happen is the self-heal billing its abandoned first walk.
    const added = all.buildings.slice(s.buildings.length);
    const bill = added.reduce((t, b) => t + placementCost(SPECS[b.spec] ?? {}), 0);
    const spent = s.funds - all.funds;
    assert.ok(spent >= bill, `pop ${pop}/£${funds}: spent ${spent} < catalogue bill ${bill}`);
    assert.ok(spent <= funds, `pop ${pop}/£${funds}: spent more than the treasury held`);
    assert.ok(added.length > 0 || spent === 0, `pop ${pop}/£${funds}: money left with nothing to show for it`);
  }
});
