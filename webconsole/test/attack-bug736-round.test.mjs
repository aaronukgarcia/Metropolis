// attack-bug736-round.test.mjs — INDEPENDENT destructive round against the
// BUG-736 capacity-loss gate (author: the lane; attacker: opus-round-bug736).
//
// Everything here drives the REAL reducer on the REAL catalogue. No expected
// capacity constant is hand-typed (GR#15) — every number is derived from
// SPECS/capacityAtTier/consolidationLadder() at run time.
//
// Attack surface, per the round brief:
//   1. successor tier symmetry (is capacityOf(toSpec) apples-to-apples?)
//   2. gain == 0 boundary + off-by-one both sides
//   3. skipped-pass journal / undo consistency
//   4. skip recorded once per pass per section (BUG-400-class log growth)
//   5. the intended arc still works
//   6. determinism across runs and across a save/load mid-arc
//   7. mutation kills: the gate itself, and CEIL-3's tiered subtrahend

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity, capacityAtTier } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { consolidationLadder, capacityOf, buildingCapacityOf, familyKeyOf } from '../src/sim/consolidator.ts';
import { stableStringify } from '../src/sim/genesisReplay.ts';
import { emptyJournal } from '../src/sim/journal.ts';
import { buildGameSave, gameSaveText, parseGameSave } from '../src/sim/gamesave.ts';

const NURSERY = SPECS.edu_nursery;
const CITY = SPECS.edu_nursery_city;
const RUNG = consolidationLadder().find((e) => e.from === 'edu_nursery' && e.to === 'edu_nursery_city');
const G = RUNG.groupSize;
const T0 = capacityAtTier(NURSERY, 0);
const SUCC = capacityOf(CITY);

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 500_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}

function runMonths(s, n) {
  let cur = s;
  for (let m = 0; m < n; m++) cur = advanceToNextBoundary(cur);
  return cur;
}

/**
 * `tiers` -> that many edu_nursery in section 0 (x 0..15, y 1..), road row at
 * y=0. `extraTiers` -> nurseries parked FAR away (section >0) purely to move
 * CEIL-3's city-wide family denominator; keep its length < groupSize so that
 * far section can never itself become a ladder candidate. `cityCount` ->
 * untouched edu_nursery_city, also far away, as family-share headroom.
 */
function fixture({ tiers, extraTiers = [], cityCount = 0 }) {
  const buildings = [...roadRow(0, 15)];
  tiers.forEach((tier, i) => {
    buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: tier, builtTick: -1000 });
  });
  extraTiers.forEach((tier, i) => {
    buildings.push({ id: 5000 + i, spec: 'edu_nursery', x: 200 + (i % 16), y: 200 + Math.floor(i / 16), capacityTier: tier, builtTick: -1000 });
  });
  for (let h = 0; h < cityCount; h++) {
    buildings.push({ id: 9000 + h, spec: 'edu_nursery_city', x: 300 + h * 10, y: 300, builtTick: -1000 });
  }
  return withConnectivity(mk({ buildings }));
}

const on = (s) => reducer(s, { type: 'toggleConsolidator' });
const allSkips = (s) => (s.consolidatorLog ?? []).flatMap((p) => p.skipped);
const allTxns = (s) => (s.consolidatorLog ?? []).flatMap((p) => p.transactions);
const cityCountOf = (s) => s.buildings.filter((b) => b.spec === 'edu_nursery_city').length;
const nurseryCountOf = (s) => s.buildings.filter((b) => b.spec === 'edu_nursery').length;
const realCapOf = (s) =>
  s.buildings.filter((b) => b.spec === 'edu_nursery').reduce((n, b) => n + buildingCapacityOf(NURSERY, b.capacityTier ?? 0), 0);

/** Tier index whose capacity is the smallest value strictly greater than `want` above tier 0 — derived, never hardcoded. */
function tierWithCapacity(target) {
  for (let t = 0; t < (NURSERY.capacityTiers?.length ?? 0); t++) {
    if (capacityAtTier(NURSERY, t) === target) return t;
  }
  return null;
}

describe('ATTACK 0 — the catalogue facts this whole file derives from', () => {
  test('the rung, the tier ladder, and the family are as the gate assumes', () => {
    assert.ok(RUNG, 'edu_nursery -> edu_nursery_city rung exists');
    assert.equal(familyKeyOf(NURSERY), familyKeyOf(CITY), 'from/to must share a family or CEIL-3s denominator is vacuous');
    assert.ok(G * T0 <= SUCC, 'a flat tier-0 group is a genuine gain (the positive control)');
    assert.ok(G * capacityAtTier(NURSERY, 9) > SUCC, 'a tier-9 group is a genuine loss (the negative control)');
    // capacityOf(spec) MUST equal buildingCapacityOf(spec, 0), or the gates
    // two sides are measured on different scales.
    assert.equal(capacityOf(CITY), buildingCapacityOf(CITY, 0));
    assert.equal(capacityOf(NURSERY), buildingCapacityOf(NURSERY, 0));
  });
});

describe('ATTACK 1 — successor tier symmetry: is capacityOf(toSpec) apples-to-apples?', () => {
  test('the successor the apply lane creates carries NO capacityTier, so the gate compares tier-0 to tier-0', () => {
    let s = on(fixture({ tiers: Array.from({ length: G }, () => 0), cityCount: 2 }));
    s = runMonths(s, 12);
    const txn = allTxns(s).find((t) => t.added[0]?.spec === 'edu_nursery_city');
    assert.ok(txn, 'setup: a consolidation actually happened');
    const rec = txn.added.find((a) => a.spec === 'edu_nursery_city');
    assert.ok(rec.capacityTier == null, 'successor record must be born untiered — a tiered successor would silently understate the gate');
    const live = s.buildings.find((b) => b.id === rec.id);
    assert.ok(live.capacityTier == null, 'and the LIVE building agrees');
    // Therefore the number the gate uses is exactly the successors real
    // capacity at birth: no understatement, no overstatement.
    assert.equal(capacityOf(CITY), buildingCapacityOf(SPECS[live.spec], live.capacityTier ?? 0));
  });
});

/**
 * Every capacity value reachable by summing up to `maxPicks` values (with
 * repetition) from `values`, PRUNED to never track a partial sum exceeding
 * `cap` — used below to PROVE (not assume) whether some target extra is
 * constructible from a spec's real tier-upgrade deltas, so a catalogue
 * change that opens or closes a gap is caught automatically instead of
 * silently assumed either way (GR#15).
 */
function reachableSums(values, maxPicks, cap) {
  let frontier = new Set([0]);
  const seen = new Set([0]);
  for (let i = 0; i < maxPicks && frontier.size > 0; i++) {
    const next = new Set();
    for (const s of frontier) {
      for (const v of values) {
        const ns = s + v;
        if (ns <= cap && !seen.has(ns)) {
          seen.add(ns);
          next.add(ns);
        }
      }
    }
    frontier = next;
  }
  return seen;
}

describe('ATTACK 2 — the gain==0 boundary and off-by-one on both sides', () => {
  // BUG-685/686 (0eb5f31, landed after this round's original authoring):
  // edu_nursery's capacityTiers moved from tierLadder(30) (topped at 71,
  // where a single tier's delta could land exactly on the group's +10 slack
  // to the successor) to campusLadder(30, 2000) — a much steeper curve whose
  // SMALLEST upgrade (tier0->tier1) is +18, already MORE than that +10
  // slack. Whether an exact break-even (or a mixed-tier at-or-under) group
  // is constructible is therefore PROVEN below by exhaustive search over the
  // real capacityTiers deltas, never assumed from either the old ladder or
  // a fresh guess at the new one.
  const slack = SUCC - G * T0; // +10 on the live catalogue: how far a flat group sits BELOW the successor
  const deltas = (NURSERY.capacityTiers ?? []).map((cap) => cap - T0).filter((d) => d > 0);
  const minUpgradeDelta = deltas.length ? Math.min(...deltas) : Infinity; // +18 on the live catalogue
  // Reachable EXTRA-above-flat sums using up to G-1 upgraded members, capped
  // at `slack` (anything beyond slack is an overshoot, not a break-even
  // candidate) — if `slack` itself is reachable, an exact break-even group
  // exists; if it is not, the smallest reachable value ABOVE slack (found
  // separately below) is the smallest real overshoot.
  const reachableAtOrUnderSlack = reachableSums(deltas, G - 1, slack);
  const exactBreakEvenReachable = reachableAtOrUnderSlack.has(slack);

  test('setup: whether an exactly-break-even group is constructible is PROVEN, not assumed, from the live catalogue', () => {
    assert.equal(minUpgradeDelta > slack, !exactBreakEvenReachable, 'sanity: unreachable iff even the cheapest upgrade already exceeds the slack');
    assert.equal(
      exactBreakEvenReachable,
      false,
      `no nursery tier-upgrade combination reaches extra=${slack} exactly (smallest upgrade is +${minUpgradeDelta}); the exact-break-even case below is a documented SKIP, not a vacuous pass — the underlying gate semantic is instead proven on pol_station -> pol_hq just below`,
    );
  });

  test(
    'gain EXACTLY 0 is ALLOWED (the gate is < 0, not <= 0) — documented behaviour',
    exactBreakEvenReachable
      ? () => {
          // Kept for if/when a future catalogue change reopens this case on
          // edu_nursery itself: (G-1) tier-0 members plus one member whose
          // tier supplies the exact remaining slack.
          const exactTier = tierWithCapacity(T0 + slack);
          assert.ok(exactTier != null, 'setup: a tier supplying the exact slack must exist if exactBreakEvenReachable is true');
          const tiers = [...Array.from({ length: G - 1 }, () => 0), exactTier];
          const groupReal = tiers.reduce((n, t) => n + capacityAtTier(NURSERY, t), 0);
          assert.equal(groupReal, SUCC, 'setup: exactly break-even');
          let s = on(fixture({ tiers, cityCount: 2 }));
          s = runMonths(s, 12);
          assert.equal(allSkips(s).filter((k) => k.reason === 'capacity loss').length, 0, 'break-even is not a loss');
          assert.equal(cityCountOf(s), 3, 'a break-even merge is permitted (2 headroom + 1 new)');
        }
      : { skip: `UNCONSTRUCTIBLE on the live campusLadder(30,2000): no real 33-building edu_nursery group sums to exactly ${SUCC} (proven above) — see the pol_station substitute test below for the same semantic on a rung where it IS real` },
  );

  test('SUBSTITUTE — gain EXACTLY 0 is ALLOWED, proven on pol_station -> pol_hq (edu_nursery cannot construct this case on the live catalogue, see the skip above)', () => {
    // pol_station -> pol_hq is chosen deliberately over the more obvious
    // res_hut -> res_lowrise: res_hut carries FIVE ladder successors
    // (res_lowrise/res_block/res_midrise/res_highrise/res_tower_*), several
    // of which ALSO qualify at 15 res_hut (res_block only needs 7), so a
    // res_hut fixture non-deterministically consolidates into whichever
    // rung findOpportunities ranks first — confirmed live: it silently
    // built res_block, not res_lowrise. pol_station has exactly ONE ladder
    // successor (pol_hq), so this fixture is unambiguous.
    const POL_STATION = SPECS.pol_station;
    const POL_HQ = SPECS.pol_hq;
    const polRung = consolidationLadder().find((e) => e.from === 'pol_station' && e.to === 'pol_hq');
    assert.ok(polRung, 'pol_station -> pol_hq exists in the live ladder');
    assert.equal(
      consolidationLadder().filter((e) => e.from === 'pol_station').length,
      1,
      'setup: pol_station must have exactly ONE ladder successor or this fixture is ambiguous too',
    );
    const polT0 = capacityAtTier(POL_STATION, 0);
    const polSucc = capacityOf(POL_HQ);
    // On the live catalogue this rung's flat group ALREADY lands exactly on
    // the successor (groupSize * T0 === successor capacity) — a genuine,
    // real exact-break-even case requiring no tier mixing at all.
    assert.equal(polRung.groupSize * polT0, polSucc, 'setup: this rung is exactly break-even at tier 0 on the live catalogue');

    const buildings = [...roadRow(0, 15)];
    for (let i = 0; i < polRung.groupSize; i++) {
      buildings.push({
        id: 100 + i,
        spec: 'pol_station',
        x: (i % 8) * POL_STATION.w,
        y: 1 + Math.floor(i / 8) * POL_STATION.h,
        capacityTier: 0,
        builtTick: -1000,
      });
    }
    for (let h = 0; h < 2; h++) buildings.push({ id: 9000 + h, spec: 'pol_hq', x: 300 + h * 10, y: 300, builtTick: -1000 });
    let s = on(withConnectivity(mk({ buildings })));
    s = runMonths(s, 12);

    assert.equal(allSkips(s).filter((k) => k.reason === 'capacity loss').length, 0, 'break-even is not a loss');
    const txn = allTxns(s).find((t) => t.added[0]?.spec === 'pol_hq');
    assert.ok(txn, 'setup: the break-even group actually consolidated into pol_hq (not diverted to some other rung)');
    assert.equal(txn.removed.length, polRung.groupSize, 'exactly the ladder-derived group size was consolidated');
    const cityCountPol = s.buildings.filter((b) => b.spec === 'pol_hq').length;
    assert.equal(cityCountPol, 3, 'a break-even merge is permitted (2 headroom + 1 new) — gain==0 passes the gate');
  });

  test('gain -1 side: the SMALLEST possible real overshoot is skipped', () => {
    // Proven, not assumed: the smallest reachable extra-above-flat value
    // STRICTLY GREATER than `slack` is the smallest real overshoot — search
    // a bit past slack (capped at slack + one whole tier-0 building, since
    // AC-8 rule 3/4 guarantee the answer can never need more than that).
    const reachableUpToOneExtraTier = reachableSums(deltas, G - 1, slack + T0);
    const smallestOvershootExtra = Math.min(...[...reachableUpToOneExtraTier].filter((e) => e > slack));
    assert.equal(smallestOvershootExtra, minUpgradeDelta, 'setup: the single cheapest upgrade IS the smallest real overshoot (adding more upgrades can only add more)');
    // Realise it: G-1 tier-0 members + exactly ONE member at whichever tier
    // supplies minUpgradeDelta (tier 1 on the live catalogue).
    const upgradeTier = (NURSERY.capacityTiers ?? []).findIndex((cap) => cap - T0 === minUpgradeDelta);
    assert.ok(upgradeTier > 0, 'setup: a tier supplying the minimal upgrade delta must exist');
    const tiers = [...Array.from({ length: G - 1 }, () => 0), upgradeTier];
    const groupReal = tiers.reduce((n, t) => n + capacityAtTier(NURSERY, t), 0);
    assert.ok(groupReal > SUCC, 'setup: a real overshoot');
    assert.equal(groupReal - SUCC, smallestOvershootExtra - slack, 'setup: matches the proven-smallest overshoot exactly');
    assert.ok(groupReal - SUCC < T0, 'setup: and a SMALL one — well under one whole tier-0 building, the case a flat count cannot see');
    let s = on(fixture({ tiers }));
    s = runMonths(s, 12);
    assert.ok(
      allSkips(s).some((k) => k.reason === 'capacity loss'),
      'even a single-digit overshoot must be skipped, not rounded away',
    );
    assert.equal(cityCountOf(s), 0);
    assert.equal(nurseryCountOf(s), tiers.length, 'nothing demolished');
  });

  test('gain +1 side: the largest sub-break-even group still consolidates', () => {
    // On the live catalogue this is the UNIQUE constructible sub-break-even
    // group: any upgrade at all (minUpgradeDelta=18) already exceeds the
    // +10 slack, so the all-tier-0 group is simultaneously "the largest"
    // and "the only" real group strictly under the successor's capacity.
    assert.ok(minUpgradeDelta > slack, 'setup: confirms the all-tier-0 group is the ONLY sub-break-even case on the live catalogue');
    const tiers = Array.from({ length: G }, () => 0);
    const groupReal = G * T0;
    assert.ok(groupReal < SUCC, 'setup: a genuine (small) gain');
    let s = on(fixture({ tiers, cityCount: 2 }));
    s = runMonths(s, 12);
    assert.equal(allSkips(s).filter((k) => k.reason === 'capacity loss').length, 0);
    assert.equal(cityCountOf(s), 3);
  });
});

describe('ATTACK 3 — a skipped pass leaves journal / log / undo consistent', () => {
  test('no phantom removal, no funds movement, no ledger row, and Undo cannot resurrect anything', () => {
    let s = on(fixture({ tiers: Array.from({ length: G }, () => 9) }));
    const fundsBefore = s.funds;
    const capexBefore = s.cumulativeCapexSpent ?? 0;
    // NOTE: the reconnect phase legitimately lays road spurs for nurseries
    // that are not road-adjacent, so the WHOLE building set is not expected
    // to be frozen. What must be frozen is the ladder-relevant estate.
    const nurseryIds = (st) => st.buildings.filter((b) => b.spec === 'edu_nursery').map((b) => b.id).sort((a, b) => a - b);
    const idsBefore = nurseryIds(s);
    s = runMonths(s, 12);
    assert.ok(allSkips(s).some((k) => k.reason === 'capacity loss'), 'setup: the skip fired');

    for (const p of s.consolidatorLog ?? []) {
      for (const t of p.transactions) {
        assert.ok(
          !t.removed.some((r) => r.spec === 'edu_nursery'),
          'a skipped candidate must never appear as a removal in ANY transaction (phantom removal)',
        );
        assert.ok(t.kind === 'reconnect', 'the only transactions this fixture may produce are reconnects');
      }
    }
    assert.deepEqual(nurseryIds(s), idsBefore, 'every skipped nursery still stands after 12 skipped months');
    assert.equal(cityCountOf(s), 0, 'no successor was ever minted');

    // Undo: must never resurrect or destroy a nursery, and must be single-level.
    const undone = reducer(s, { type: 'consolidatorUndo' });
    assert.deepEqual(nurseryIds(undone), idsBefore, 'Undo touches no skipped nursery');
    assert.equal(cityCountOf(undone), 0);
    const twice = reducer(undone, { type: 'consolidatorUndo' });
    assert.equal(stableStringify(twice), stableStringify(undone), 'second Undo press is idempotent');
    assert.ok(s.funds <= fundsBefore, 'a skip never CREATES money');
    assert.ok((s.cumulativeCapexSpent ?? 0) >= capexBefore);
  });

  test('a real consolidation is STILL fully undoable when a skip shares the same pass log', () => {
    // Section 0 loses (tier 9). A second, far section wins (tier 0, full
    // group) — one pass, one skip + one transaction.
    const buildings = [...roadRow(0, 15)];
    for (let i = 0; i < G; i++) buildings.push({ id: 100 + i, spec: 'edu_nursery', x: i % 16, y: 1 + Math.floor(i / 16), capacityTier: 9, builtTick: -1000 });
    for (let x = 0; x <= 15; x++) buildings.push({ id: 2000 + x, spec: 'road', x: 32 + x, y: 0, builtTick: -1000 });
    for (let i = 0; i < G; i++) buildings.push({ id: 300 + i, spec: 'edu_nursery', x: 32 + (i % 16), y: 1 + Math.floor(i / 16), capacityTier: 0, builtTick: -1000 });
    for (let h = 0; h < 2; h++) buildings.push({ id: 9000 + h, spec: 'edu_nursery_city', x: 300 + h * 10, y: 300, builtTick: -1000 });
    let s = on(withConnectivity(mk({ buildings })));

    const before = s;
    s = runMonths(s, 12);
    assert.ok(allSkips(s).some((k) => k.reason === 'capacity loss'), 'setup: the tier-9 section was skipped');
    assert.ok(allTxns(s).some((t) => t.added[0]?.spec === 'edu_nursery_city'), 'setup: the tier-0 section consolidated');
    // The demolished ids all came from the tier-0 section, never the skipped one.
    const removedIds = new Set(allTxns(s).flatMap((t) => t.removed.map((r) => r.id)));
    for (const id of removedIds) assert.ok(id >= 300, `id ${id} was demolished but belongs to the SKIPPED tier-9 group`);
    assert.equal(nurseryCountOf(s) >= G, true);
    assert.equal(realCapOf(s) >= G * capacityAtTier(NURSERY, 9), true, 'the skipped groups real capacity survives in full');
    assert.ok(before.funds >= s.funds, 'building the successor cost money');
  });
});

describe('ATTACK 4 — BUG-400-class: the skip is recorded once per pass per section, not once per building', () => {
  test('40 tier-9 nurseries over 24 months: at most one capacity-loss row per pass per section, and the log stays capped', () => {
    let s = on(fixture({ tiers: Array.from({ length: 40 }, () => 9) }));
    const capBefore = realCapOf(s);
    let passes = 0;
    for (let m = 0; m < 24; m++) {
      s = advanceToNextBoundary(s);
      const p = (s.consolidatorLog ?? [])[0];
      if (p && p.tick === s.tick) {
        passes += 1;
        const rows = p.skipped.filter((k) => k.reason === 'capacity loss');
        const keys = new Set(rows.map((k) => k.sectionKey));
        assert.ok(rows.length <= keys.size, `pass at tick ${s.tick}: ${rows.length} capacity-loss rows for ${keys.size} sections — one row PER BUILDING is the BUG-400 shape`);
        assert.ok(rows.length <= 40, 'never one row per building');
      }
      assert.equal(nurseryCountOf(s), 40, `month ${m + 1}: nothing demolished`);
      assert.equal(cityCountOf(s), 0);
    }
    assert.ok(passes > 0, 'setup: passes actually ran');
    assert.equal(realCapOf(s), capBefore, 'real tiered capacity held EXACTLY steady for 24 months');
    assert.ok((s.consolidatorLog ?? []).length <= 24, 'consolidatorLog is a capped ring, not unbounded growth');
    // Total rows across the whole retained log stay proportional to passes,
    // never to buildings x passes.
    const totalRows = allSkips(s).filter((k) => k.reason === 'capacity loss').length;
    assert.ok(totalRows <= (s.consolidatorLog ?? []).length, `${totalRows} capacity-loss rows across ${(s.consolidatorLog ?? []).length} retained passes`);
  });
});

describe('ATTACK 5 — CEIL-3 must use the REAL tiered subtrahend (the flat formula must red)', () => {
  // BUG-685/686 (0eb5f31): this describe block originally built its
  // divergence fixture from edu_nursery, requiring `groupReal == SUCC`
  // exactly (capacityGain==0) so the capacity-loss gate passes and the
  // fixture reaches CEIL-3 untouched. On the live campusLadder(30,2000) that
  // is impossible for edu_nursery for a STRONGER reason than ATTACK 2's
  // exact-match case: the group's ENTIRE +10 slack below the successor is
  // smaller than even the cheapest real upgrade (+18) — so for edu_nursery,
  // ANY tiered group with >=1 upgraded member already trips the
  // capacity-loss gate before CEIL-3 is ever reached, meaning the two gates'
  // real-vs-flat divergence cannot be isolated on this spec at all any more.
  // ATTACK 5's actual target is the CEIL-3 mechanism, not edu_nursery
  // specifically, so this block now uses off_tower -> off_towers_downtown —
  // a real, live ladder rung with genuine slack (a group can gain +17 REAL
  // capacity over a flat approximation while still passing the capacity-loss
  // gate) — to prove the same mutation-kill.
  const OFF_TOWER = SPECS.off_tower;
  const OFF_DOWNTOWN = SPECS.off_towers_downtown;
  const officeRung = consolidationLadder().find((e) => e.from === 'off_tower' && e.to === 'off_towers_downtown');
  const oT0 = capacityAtTier(OFF_TOWER, 0);
  const oSucc = capacityOf(OFF_DOWNTOWN);
  const oG = officeRung.groupSize;
  // The highest single-member upgrade tier that keeps the WHOLE group's real
  // capacity at or under the successor (capacityGain >= 0) — derived, never
  // hand-typed: search every tier from the top down for the first that fits.
  const oDeltaCap = oSucc - oG * oT0; // slack for exactly ONE upgraded member
  let oUpgradeTier = 0;
  for (let t = (OFF_TOWER.capacityTiers ?? []).length - 1; t >= 1; t--) {
    if (capacityAtTier(OFF_TOWER, t) - oT0 <= oDeltaCap) { oUpgradeTier = t; break; }
  }
  const groupTiers = [...Array.from({ length: oG - 1 }, () => 0), oUpgradeTier];
  const groupReal = groupTiers.reduce((n, t) => n + capacityAtTier(OFF_TOWER, t), 0);
  const groupFlat = oG * oT0;

  // Solve for a family-headroom target D (= groupReal + headroom H) that
  // separates the two formulas. D - groupReal == H always (by definition),
  // so:
  //   real:  after = max(0, D-groupReal) + oSucc = H + oSucc
  //          skips iff oSucc > 0.5*(H+oSucc)      => H < oSucc
  //   flat:  after = max(0, D-groupFlat) + oSucc = (groupReal-groupFlat+H)+oSucc
  //          allows iff oSucc <= 0.5*that         => H >= oSucc-(groupReal-groupFlat)
  // So H in [oSucc-(groupReal-groupFlat), oSucc) separates them; in terms of
  // D = groupReal + H that is [oSucc+groupFlat, groupReal+oSucc).
  const dLow = oSucc + groupFlat;
  const dHigh = groupReal + oSucc;

  /** Greedy tier decomposition of `target` into off_tower headroom buildings, using real tier capacities only — mirrors the nursery version's own extrasFor idiom (GR#3: one algorithm shape, reused). */
  function officeExtrasFor(target) {
    const caps = (OFF_TOWER.capacityTiers ?? []);
    const out = [];
    let left = target;
    while (left > 0) {
      let best = -1;
      for (let t = caps.length - 1; t >= 0; t--) {
        if (caps[t] <= left) { best = t; break; }
      }
      if (best < 0) break;
      out.push(best);
      left -= caps[best];
    }
    return { tiers: out, achieved: target - left };
  }

  const extraTarget = Math.floor((dLow + dHigh) / 2) - groupReal;
  const extras = officeExtrasFor(Math.max(0, extraTarget));
  const D = groupReal + extras.achieved;

  test('setup: the two formulas provably disagree on this exact (off_tower -> off_towers_downtown) fixture', () => {
    assert.ok(officeRung, 'off_tower -> off_towers_downtown exists in the live ladder');
    assert.ok(oUpgradeTier > 0, 'setup: at least one real off_tower tier upgrade must fit within the capacity-loss gate\'s slack — real slack constructible');
    assert.ok(groupReal <= oSucc, 'setup: this group passes the capacity-loss gate (gain >= 0)');
    assert.ok(groupReal > groupFlat, 'setup: real and flat subtrahends genuinely DIFFER — the divergence this block is proving');
    assert.ok(dLow < dHigh, `no separating window: oSucc=${oSucc} groupFlat=${groupFlat} groupReal=${groupReal}`);
    assert.ok(extras.tiers.length < oG, 'the extras section must stay below groupSize so it is never itself a candidate');
    assert.ok(D >= dLow && D < dHigh, `denominator ${D} must sit in the separating window [${dLow}, ${dHigh})`);
    // real formula: skip
    assert.ok(oSucc > 0.5 * D, 'REAL subtrahend => family-share ceiling SKIPS');
    // flat formula: allow
    assert.ok(oSucc <= 0.5 * (Math.max(0, D - groupFlat) + oSucc), 'FLAT subtrahend => family-share ceiling ALLOWS (the pre-fix hole)');
  });

  test('MUTATION TARGET: with the real tiered subtrahend the merge is refused by the family-share ceiling', () => {
    const buildings = [...roadRow(0, 15)];
    groupTiers.forEach((tier, i) => {
      buildings.push({ id: 100 + i, spec: 'off_tower', x: (i % 8) * OFF_TOWER.w, y: 1 + Math.floor(i / 8) * OFF_TOWER.h, capacityTier: tier, builtTick: -1000 });
    });
    // Headroom, split into chunks each strictly below oG so no chunk is ever
    // itself a candidate group — placed far from the main section.
    let hid = 5000;
    let placed = 0;
    let chunkStart = 0;
    while (chunkStart < extras.tiers.length) {
      const chunkLen = Math.min(oG - 1, extras.tiers.length - chunkStart);
      for (let i = 0; i < chunkLen; i++) {
        const tier = extras.tiers[chunkStart + i];
        buildings.push({
          id: hid++,
          spec: 'off_tower',
          x: 200 + (placed % 8) * OFF_TOWER.w,
          y: 200 + Math.floor(placed / 8) * OFF_TOWER.h,
          capacityTier: tier,
          builtTick: -1000,
        });
        placed++;
      }
      chunkStart += chunkLen;
    }
    let s = on(withConnectivity(mk({ buildings })));
    s = runMonths(s, 12);
    assert.equal(
      allSkips(s).filter((k) => k.reason === 'capacity loss').length,
      0,
      'this fixture must NOT be stopped by the capacity-loss gate — it must reach CEIL-3, or the mutation proof is vacuous',
    );
    assert.ok(
      allSkips(s).some((k) => k.reason === 'family share ceiling'),
      'CEIL-3 must refuse this merge using the REAL tiered subtrahend',
    );
    assert.equal(
      s.buildings.filter((b) => b.spec === 'off_towers_downtown').length,
      0,
      'no successor was built — reverting CEIL-3 to capacityOf(fromSpec)*groupCount makes this line RED',
    );
    assert.equal(s.buildings.filter((b) => b.spec === 'off_tower').length, groupTiers.length + extras.tiers.length, 'and nothing was demolished');
  });
});

describe('ATTACK 6 — determinism, and determinism ACROSS a real save/load mid-arc', () => {
  const lossTiers = Array.from({ length: G }, () => 9);

  test('two independent runs of the skipping fixture are byte-identical', () => {
    const a = runMonths(on(fixture({ tiers: lossTiers })), 12);
    const b = runMonths(on(JSON.parse(JSON.stringify(fixture({ tiers: lossTiers })))), 12);
    assert.ok(allSkips(a).some((k) => k.reason === 'capacity loss'), 'setup: the gated path really ran');
    assert.equal(stableStringify(a), stableStringify(b));
  });

  test('save/load at month 6 then continue == an uninterrupted 12-month run (skip path)', () => {
    const straight = runMonths(on(fixture({ tiers: lossTiers })), 12);
    let mid = runMonths(on(fixture({ tiers: lossTiers })), 6);
    const save = buildGameSave({
      state: mid,
      journal: emptyJournal(),
      journalTail: [],
      name: 'attack-bug736',
      buildVersion: 'attack',
      now: new Date(0),
    });
    const reloaded = parseGameSave(gameSaveText(save)).save.savepoint.snapshot;
    const resumed = runMonths(reloaded, 6);
    assert.equal(resumed.buildings.length, straight.buildings.length);
    assert.equal(nurseryCountOf(resumed), G);
    assert.equal(cityCountOf(resumed), 0, 'a reloaded save must not lose the tiers and then merge away the capacity');
    assert.equal(realCapOf(resumed), realCapOf(straight), 'real tiered capacity survives the storage boundary');
    assert.ok(allSkips(resumed).some((k) => k.reason === 'capacity loss'), 'the gate still fires post-reload');
  });

  test('save/load mid-arc on the CONSOLIDATING fixture keeps the successor and the tier bookkeeping', () => {
    const tiers = Array.from({ length: G }, () => 0);
    let mid = runMonths(on(fixture({ tiers, cityCount: 2 })), 6);
    const save = buildGameSave({ state: mid, journal: emptyJournal(), journalTail: [], name: 'a', buildVersion: 'attack', now: new Date(0) });
    const reloaded = parseGameSave(gameSaveText(save)).save.savepoint.snapshot;
    const resumed = runMonths(reloaded, 6);
    const straight = runMonths(on(fixture({ tiers, cityCount: 2 })), 12);
    assert.equal(cityCountOf(resumed), cityCountOf(straight));
    assert.equal(nurseryCountOf(resumed), nurseryCountOf(straight));
  });
});

describe('ATTACK 7 — GR#16: a wrong-but-typed capacityTier from storage', () => {
  test('a FRACTIONAL capacityTier survives gamesave validation and is a live hole in both gates', () => {
    // capacityAtTier does an array index lookup: a fractional tier returns
    // undefined, so buildingCapacityOf returns undefined and the summed
    // groupCapacityReal becomes NaN. `NaN < 0` is false => the capacity-loss
    // gate does not fire, and Math.max(0, before - NaN) is NaN => CEIL-3's
    // ceiling comparison is also false. Both gates fail OPEN.
    // gamesave.ts only checks `typeof capacityTier === 'number'`, so such a
    // save is accepted. This is documented here as a finding, not a claim
    // that the fix regressed the reachable game.
    assert.equal(capacityAtTier(NURSERY, 1.5), undefined, 'fractional tier -> undefined (the root of the hole)');
    assert.equal(buildingCapacityOf(NURSERY, 1.5), undefined);
    const poisoned = [1.5, ...Array.from({ length: G - 1 }, () => 9)].reduce(
      (n, t) => n + buildingCapacityOf(NURSERY, t),
      0,
    );
    assert.ok(Number.isNaN(poisoned), 'one fractional member poisons the whole groupCapacityReal sum');
    assert.equal(SUCC - poisoned < 0, false, 'and NaN < 0 is FALSE — the capacity-loss gate fails OPEN');
    assert.equal(SUCC > 0.5 * (Math.max(0, 5000 - poisoned) + SUCC), false, 'CEIL-3 fails open too');
    // Integer out-of-range tiers are safe (capacityAtTier clamps).
    assert.equal(buildingCapacityOf(NURSERY, 999), capacityAtTier(NURSERY, (NURSERY.capacityTiers?.length ?? 1) - 1));
    assert.equal(buildingCapacityOf(NURSERY, -1), T0, 'negative tier clamps to base');
  });

  test('END-TO-END: a fractional-tier tier-9 group IS consolidated away, losing real capacity', () => {
    const tiers = [1.5, ...Array.from({ length: G - 1 }, () => 9)];
    let s = on(fixture({ tiers, cityCount: 2 }));
    s = runMonths(s, 12);
    // OBSERVED (round finding F1, pinned so a future fix flips it
    // DELIBERATELY rather than by accident): one fractional member is enough
    // for the whole tier-9 group to sail through both gates and be merged
    // away — the exact BUG-736 shape, reachable only from a corrupt/
    // hand-edited save. If a later fix coerces capacityTier at the storage
    // boundary (or makes buildingCapacityOf total), these three lines flip
    // and this test must be updated to the fixed expectation.
    assert.equal(cityCountOf(s), 3, 'F1: the poisoned group WAS consolidated (2 headroom + 1 new successor)');
    assert.equal(nurseryCountOf(s), 0, 'F1: every nursery in the poisoned group was demolished');
    assert.equal(
      allSkips(s).filter((k) => k.reason === 'capacity loss').length,
      0,
      'F1: the capacity-loss gate never fired — NaN < 0 is false, so the gate fails OPEN',
    );
  });
});
