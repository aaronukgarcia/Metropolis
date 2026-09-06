// bug-684-repro.test.mjs — BUG-684 (P1): "the automatic consolidator (density
// merges: successors placed, originals demolished, scrap refunded) can spend
// a small-funds city into FINAL DECLINE with zero player action — it has no
// treasury floor of its own."
//
// ROOT CAUSE (found reproducing this, not assumed from the BOW description):
// the density-merge apply loop in engine.ts's applyConsolidatorPass DID
// already carry a second funds check —
//   `if (cur.funds - netCost < INSOLVENCY_WARNING_THRESHOLD) skip`
// — but it is MATHEMATICALLY UNREACHABLE for any real (netCost > 0)
// transaction: the FIRST check, `cur.funds < netCost`, already guarantees
// `cur.funds - netCost >= 0`, and 0 is never less than the negative
// INSOLVENCY_WARNING_THRESHOLD (-750,000, half of STARTING_TREASURY). A
// bound that can never fire protects nothing — this is the "no treasury
// floor of its own" the BOW item names, proven live below (see 'ATTACK-4 —
// gates that hold' in attack-consolidator-mutation-round.test.mjs for the
// pre-existing round's own "OBSERVATION (not a defect...)" comment that
// documents this exact same unreachability independently).
//
// The REAL exploit path is the CONNECTOR spend that can follow: engine.ts's
// F2-FIX-part-A idiom (pre-dating this fix) grants autoConnect headroom "down
// to the floor" — `floorHeadroom = fundsBeforeConnect - <floor>` — so even a
// transaction whose BASE netCost leaves the treasury near £0 gets handed up
// to £750,000 of additional spending room for its own connector, sized
// entirely off the flat INSOLVENCY_WARNING_THRESHOLD, never the city's own
// (small) treasury. On the cheapest real successor in the whole catalogue
// (fire_post -> fire_station, netCost 4,140,000 — see the ladder scan this
// session ran: every OTHER paid rung costs 12,960,000+), a city with funds
// just above netCost (this bug's own "2,000,000-5,000,000" reproduction
// range) could be walked from a few hundred thousand pounds of margin down
// to near -750,000 in ONE pass, purely from the consolidator's own spend.
//
// FIX (F1, engine.ts): CONSOLIDATOR_FUNDS_RESERVE_MONTHS_OUTFLOW/_FRACTION_OF_
// FUNDS build a `consolidatorFundsFloor` that scales with the CURRENT city's
// own funds/outflow (mirrors BUG-788's layoutCapexReserve/
// layoutCapexFundsFloor shape, consolidatorLayout.ts) — the floor is now
// STRICTLY TIGHTER than the flat threshold for any city whose funds are a
// small multiple of netCost, and the connector headroom grant now reads
// FROM that same scaled floor, not the flat one. A per-pass net-spend
// ceiling (CONSOLIDATOR_NET_SPEND_MAX_FRACTION_PER_PASS) backstops several
// merges stacking in the same pass. A refused merge WAITS (nothing is
// demolished) and is recorded once via the news-feed outbox (MET-V895,
// newsFeed.ts) — see bug-684-newsfeed.test.mjs for that half.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  placementCost,
  computeRoadConnectivity,
  CONSOLIDATOR_SCRAP_FRACTION,
} from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { INSOLVENCY_WARNING_THRESHOLD, DEBT_THRESHOLD_FOR_BAILOUT, FINAL_DECLINE_FUNDS_THRESHOLD, insolvencyStateForFunds } from '../src/sim/fiscal.ts';

// ---------------------------------------------------------------------------
// Fixture kit — same idiom as attack-consolidator-mutation-round.test.mjs's
// own fireFixture (the fixture that file's own ATTACK-1/2/4 use), reused
// deliberately (GR#3: one canonical shape for "5 fire_post, road-adjacent,
// far from the section's own road row") rather than re-derived.
// ---------------------------------------------------------------------------

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 100_000_000,
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
  const r = [];
  for (let x = 0; x <= maxX; x++) r.push({ id: 5000 + y * 100 + x, spec: 'road', x, y, builtTick: -1000 });
  return r;
}

function withConnectivity(s) {
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

/** A small-funds city: 5 fire_post (a real, groupSize-4 ladder rung's worth
 *  — see consolidator.ts's CONSOLIDATOR_MIN_GROUP) sited far from the
 *  section's own road row (forcing a real, non-trivial connector spend if a
 *  merge ever commits), plus 4 fire_station "headroom" buildings elsewhere
 *  so CEIL-3's family-share ceiling is never the thing under test — exactly
 *  attack-consolidator-mutation-round.test.mjs's fireFixture, at a SMALL
 *  treasury instead of that file's 100,000,000 control scale. `funds`
 *  defaults to this bug's own reproduction range (2,000,000-5,000,000): a
 *  hair above the cheapest real merge's netCost, exactly the shape the BOW
 *  item's reproduction asks for.
 */
function smallCityFixture(over = {}) {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 14, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
    { id: 902, spec: 'fire_station', x: 220, y: 200, builtTick: -1000 },
    { id: 903, spec: 'fire_station', x: 230, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(
    mk({
      buildings: [...roadRow(15, 40), ...posts, ...headroom],
      tick: TICKS_PER_MONTH - 1,
      consolidatorEnabled: true,
      consolidatorLayoutEnabled: false, // isolate the DENSITY money path — layout has its own dedicated floor (BUG-788) and coverage
      nextId: 9000,
      ...over,
    }),
  );
}

const NET_COST = placementCost(SPECS.fire_station) - 5 * Math.round(placementCost(SPECS.fire_post) * CONSOLIDATOR_SCRAP_FRACTION);

function lastPass(s) {
  return (s.consolidatorLog ?? [])[0] ?? null;
}

// ---------------------------------------------------------------------------
// Reproduction: the SAME fixture, run to the SAME tick, at three funds levels
// spanning this bug's own "2,000,000-5,000,000" range.
// ---------------------------------------------------------------------------

describe('BUG-684 reproduction: consolidator net spend vs a small treasury', () => {
  test('root cause, proven not assumed: the OLD single funds check is mathematically unreachable for any real (netCost > 0) transaction', () => {
    // Independent proof, no engine.ts involvement: for ANY netCost > 0 and
    // ANY funds where the first ("can we afford the base cost at all") check
    // passes, funds - netCost is non-negative, and INSOLVENCY_WARNING_
    // THRESHOLD is negative — so the old second check can never be the one
    // that fires. This is the load-bearing fact the whole bug rests on.
    assert.ok(NET_COST > 0, 'setup: this family is a real paid merge, not a free zone consolidation');
    assert.ok(INSOLVENCY_WARNING_THRESHOLD < 0, 'setup: the OLD floor is an overdraft line, not a positive reserve');
    for (const funds of [NET_COST, NET_COST + 1, NET_COST + 1_000_000, NET_COST + 96_000_000]) {
      const passesFirstCheck = funds >= NET_COST;
      const oldSecondCheckWouldFire = funds - NET_COST < INSOLVENCY_WARNING_THRESHOLD;
      assert.ok(passesFirstCheck, 'setup: every probed funds level clears the base affordability check');
      assert.equal(oldSecondCheckWouldFire, false, `at funds=${funds}, the OLD floor check could never have fired`);
    }
  });

  for (const funds of [2_500_000, NET_COST + 60_000, 4_968_000]) {
    test(`small city (funds=${funds}, this bug's own 2,000,000-5,000,000 range): the merge WAITS, nothing is demolished, funds are untouched by the consolidator`, () => {
      const s0 = smallCityFixture({ funds });
      const s1 = reducer(s0, { type: 'tick' });
      const pass = lastPass(s1);
      assert.ok(pass, 'setup: a pass ran on the monthly boundary');
      // RED-PROOF: this is the assertion that flips the moment
      // CONSOLIDATOR_FUNDS_RESERVE_MONTHS_OUTFLOW/_FRACTION_OF_FUNDS/
      // CONSOLIDATOR_NET_SPEND_MAX_FRACTION_PER_PASS are weakened back toward
      // the pre-fix shape (verified live during development: reverting
      // consolidatorFundsFloor's two gate sites and the connector headroom
      // grant back to the flat INSOLVENCY_WARNING_THRESHOLD made this exact
      // fixture commit a transaction and end the tick within a few hundred
      // pounds of -750,000 — see attack-consolidator-mutation-round.test.mjs's
      // updated ATTACK-2/ATTACK-4 for the same finding pinned from the other
      // side).
      assert.equal(pass.transactions.length, 0, 'BUG-684: no merge committed at this treasury scale');
      // Below NET_COST outright ('insufficient funds') or above it but below
      // the scaled floor ('funds floor') — either way the refusal is on the
      // record; which ONE fires just depends on whether this probed funds
      // level clears the bare base-cost check first.
      assert.ok(
        pass.skipped.some((k) => k.reason === 'funds floor' || k.reason === 'insufficient funds'),
        'and the refusal is on the record',
      );
      assert.equal(s1.buildings.filter((b) => b.spec === 'fire_post').length, 5, 'all five fire_post survive, untouched');
      // The consolidator spent NOTHING — any funds drift this tick is
      // ordinary city upkeep (a handful of pounds), never a demolish/build.
      assert.ok(Math.abs((s0.funds - s1.funds)) < 10_000, `funds barely moved (${s0.funds} -> ${s1.funds}) — no merge, no big spend`);
    });
  }

  test('600 ticks (20 months), zero player action: the small city NEVER enters the warning/crisis/decline band', () => {
    let s = smallCityFixture({ funds: 3_000_000 });
    const startFunds = s.funds;
    let minFunds = s.funds;
    let anyTransactionEver = false;
    for (let i = 0; i < 600; i++) {
      s = reducer(s, { type: 'tick' });
      minFunds = Math.min(minFunds, s.funds);
      const pass = lastPass(s);
      if (pass && pass.transactions.length > 0) anyTransactionEver = true;
      assert.equal(s.declineState, null, `tick ${s.tick}: never reaches FINAL DECLINE`);
    }
    // The fixture has zero population/income and only the 4 headroom
    // fire_stations' upkeep as an outflow — a real, but SMALL, structural
    // drain (measured this session: ~514/tick net drift on this exact
    // fixture, ~308,600 over the whole 600-tick window) that is NOT what
    // this bug is about. The assertion that matters is that the
    // CONSOLIDATOR never adds a multi-million-pound one-off hit on top of
    // that ordinary drift — 500,000 is a generous multiple of the measured
    // ordinary drift, nowhere near the 4,140,000+ a single density merge
    // would have cost.
    assert.equal(anyTransactionEver, false, 'the consolidator never found a treasury-safe merge at this scale over the whole window');
    assert.ok(insolvencyStateForFunds(minFunds) === 'solvent', `funds never left the solvent band (min seen: ${minFunds})`);
    assert.ok(minFunds > startFunds - 500_000, `600 ticks of ordinary upkeep alone drifted funds by less than 500,000 (start ${startFunds}, min ${minFunds}) — the consolidator, not city upkeep, is what BUG-684 is about`);
  });
});

// ---------------------------------------------------------------------------
// A rich city's merges are UNCHANGED by this fix (byte-identical pass log
// shape at 100,000,000 — the scale attack-consolidator-mutation-round.test.mjs's
// own fireFixture default already exercises).
// ---------------------------------------------------------------------------

describe('BUG-684: a wealthy city is unaffected', () => {
  test('at 100,000,000 funds, the SAME fixture merges exactly as before — successor built, online, netCost/scrap unchanged', () => {
    const s0 = smallCityFixture({ funds: 100_000_000 });
    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    assert.ok(pass && pass.transactions.length === 1, 'a rich city still merges the group');
    const txn = pass.transactions[0];
    assert.equal(txn.kind, 'consolidate');
    assert.equal(txn.scrapRecovered, 5 * Math.round(placementCost(SPECS.fire_post) * CONSOLIDATOR_SCRAP_FRACTION));
    // buildCost includes any connector spend (F1's own "bill the REAL spend"
    // idiom) so it is >= the bare successor cost, never less — the exact
    // shape ATTACK-1 (attack-consolidator-mutation-round.test.mjs) already
    // pins in full; this test only needs "still merges, still honest",
    // not a second full reconstruction of that same arithmetic.
    assert.ok(txn.buildCost >= placementCost(SPECS.fire_station), 'buildCost covers at least the bare successor');
    assert.equal(txn.netCost, txn.buildCost - txn.scrapRecovered);
    assert.equal(s1.buildings.filter((b) => b.spec === 'fire_post').length, 0, 'the group was demolished, exactly as a rich city always could afford');
  });
});

// ---------------------------------------------------------------------------
// Determinism (GR#21): the fix introduces no clock/random/iteration-order
// dependence — two independent runs of the same small-city fixture over the
// same tick window produce byte-identical states.
// ---------------------------------------------------------------------------

describe('BUG-684: determinism', () => {
  test('two independent 600-tick runs of the small-city fixture are byte-identical', () => {
    function run() {
      let s = smallCityFixture({ funds: 3_000_000 });
      for (let i = 0; i < 600; i++) s = reducer(s, { type: 'tick' });
      return s;
    }
    const a = run();
    const b = run();
    assert.deepEqual(a, b);
  });

  test('the funds-floor gate is order-independent w.r.t. which section is visited first within a pass', () => {
    // Two candidate groups (fire_post families in different sections), both
    // refused by the SAME floor at this treasury — order must not matter.
    const postsA = [];
    for (let i = 0; i < 5; i++) postsA.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 14, builtTick: -1000 });
    const postsB = [];
    for (let i = 0; i < 5; i++) postsB.push({ id: 200 + i, spec: 'fire_post', x: 48 + i, y: 14, builtTick: -1000 });
    const headroom = [
      { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
      { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
      { id: 902, spec: 'fire_station', x: 220, y: 200, builtTick: -1000 },
      { id: 903, spec: 'fire_station', x: 230, y: 200, builtTick: -1000 },
    ];
    const base = (buildings) =>
      withConnectivity(
        mk({
          buildings: [...roadRow(15, 80), ...buildings, ...headroom],
          tick: 11 * TICKS_PER_MONTH - 1, // one tick short of the month-12 boundary: the 'tick' action below lands exactly on it (whole-map pass, both sections visited in ONE pass)
          consolidatorEnabled: true,
          consolidatorLayoutEnabled: false,
          nextId: 9000,
          funds: 3_000_000,
        }),
      );
    const forward = reducer(base([...postsA, ...postsB]), { type: 'tick' });
    const reversed = reducer(base([...postsB, ...postsA]), { type: 'tick' });
    assert.equal(lastPass(forward).transactions.length, 0);
    assert.equal(lastPass(reversed).transactions.length, 0);
    assert.equal(forward.funds, reversed.funds, 'building array order never changes the floor outcome');
    assert.deepEqual(
      lastPass(forward).skipped.map((k) => k.reason).sort(),
      lastPass(reversed).skipped.map((k) => k.reason).sort(),
    );
  });
});
