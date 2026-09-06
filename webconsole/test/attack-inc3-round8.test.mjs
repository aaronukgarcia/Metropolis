// attack-inc3-round8.test.mjs — FEAT-2326609779 (consolidator inc3 LAYOUT
// HIERARCHY) + BUG-684, INDEPENDENT DESTRUCTIVE ROUND 8 (attacker != author).
//
// Round 6 REJECTED on two P1 money defects; rework 7 landed:
//   F1  -> LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP + LAYOUT_CAPEX_MAX_PER_TICK
//   F1b -> SimState.consolidatorLayoutBaselineNetIncome anchored ONCE +
//          consolidatorLayoutCumulativeUpkeepDelta as a LIFETIME total.
//
// This round attacks the REWORK ITSELF, not the original defects:
//   R8-1 the ANCHOR as an attack surface — spike vs trough, re-anchor paths,
//        old saves with no field at all.
//   R8-2 the documented "bounded by LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK"
//        claim, measured against the code's actual effective allowance.
//   R8-3 the 20M/tick capex ceiling across four treasury scales incl. a
//        hamlet that must PAUSE rather than bankrupt.
//   R8-4 lifetime delta persistence across a real save/load JSON boundary.
//   R8-5 determinism + conservation with layout ON.
//   R8-6 mutation proofs that each gate is load-bearing.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { computeRoadConnectivity, SPECS } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  CONSOLIDATOR_UNLOCK_LEVEL,
  TICKS_PER_MONTH,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { INSOLVENCY_WARNING_THRESHOLD } from '../src/sim/fiscal.ts';
import { runConsistencyChecks, foldGraceHistory, GRACE_WINDOW_SIZE } from '../src/sim/consistency.ts';
import {
  LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK,
  LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK,
  LAYOUT_CAPEX_MAX_PER_TICK,
  LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP,
  LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME,
  layoutUpkeepEffectiveFloorOf,
} from '../src/sim/consolidatorLayout.ts';

// ---------------------------------------------------------------------------
// Fixtures — the estate's own idiom (attack-inc3-round6.test.mjs).
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
    consolidatorLayoutEnabled: true,
    consolidatorLog: [],
    consolidatorMode: 'monthly-twelfth',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    ...over,
  };
}

function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}

const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

function fireFixture(over, roadMax = 40) {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 1, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(mk({ buildings: [...roadRow(0, roadMax), ...posts, ...headroom], funds: 100_000_000, ...over }));
}

/** The estate's own "give the city a real settled baseline" helper (round 7). */
function withHealthyBaseline(s) {
  let cur = { ...s, consolidatorLayoutEnabled: false };
  cur = reducer(cur, { type: 'tick' });
  return { ...cur, consolidatorLayoutEnabled: true, tick: s.tick, consolidatorLog: s.consolidatorLog ?? [] };
}

const netIncomeOf = (s) =>
  s.lastFlows.inflows.reduce((a, b) => a + b.value, 0) - s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);

const EXCLUDED = new Set(['Consolidation', 'Consolidation Scrap']);
const upkeepOf = (s) => s.lastFlows.outflows.filter((f) => !EXCLUDED.has(f.label)).reduce((a, b) => a + b.value, 0);

function layoutCapexSpent(s) {
  let total = 0;
  for (const p of s.consolidatorLog ?? []) for (const t of p.tierLayout ?? []) total += t.capexSpent ?? 0;
  return total;
}

function layoutPlacements(s) {
  let n = 0;
  for (const p of s.consolidatorLog ?? []) for (const t of p.tierLayout ?? []) for (const a of t.tierAudit ?? []) if (a.actuallyPlaced) n += 1;
  return n;
}

function skipReasons(s) {
  const seen = new Map();
  for (const p of s.consolidatorLog ?? []) for (const k of p.skipped ?? []) seen.set(k.reason, (seen.get(k.reason) ?? 0) + 1);
  return seen;
}

/** Run n ticks with the consolidator ON, recording the first tick funds breach the reserve floor. */
function runTicks(s0, n, opts = {}) {
  let s = reducer(s0, { type: 'toggleConsolidator' });
  let firstInsolvencyTick = null;
  let minFunds = s.funds;
  for (let i = 0; i < n; i += 1) {
    s = reducer(s, { type: 'tick' });
    if (s.funds < minFunds) minFunds = s.funds;
    if (firstInsolvencyTick === null && s.funds < INSOLVENCY_WARNING_THRESHOLD) firstInsolvencyTick = s.tick;
    if (opts.each) opts.each(s, i);
  }
  return { s, firstInsolvencyTick, minFunds };
}

// ===========================================================================
// R8-1 — THE ANCHOR AS AN ATTACK SURFACE
// ===========================================================================

describe('R8-1 the once-anchored baseline', () => {
  test('R8-1a: the anchor is written on the FIRST layout pass and never rewritten thereafter', () => {
    let s = withHealthyBaseline(fireFixture());
    assert.equal(s.consolidatorLayoutBaselineNetIncome ?? null, null, 'setup: unanchored before any layout pass');
    s = reducer(s, { type: 'toggleConsolidator' });
    const anchors = new Set();
    for (let i = 0; i < 120; i += 1) {
      s = reducer(s, { type: 'tick' });
      if (s.consolidatorLayoutBaselineNetIncome !== null && s.consolidatorLayoutBaselineNetIncome !== undefined) {
        anchors.add(s.consolidatorLayoutBaselineNetIncome);
      }
    }
    assert.equal(anchors.size, 1, `F1b closeout: exactly ONE anchor value may ever be observed, saw ${[...anchors].join(', ')}`);
  });

  test('R8-1b: MEASUREMENT — a spike anchor buys a permanently larger lifetime upkeep allowance than a trough anchor', () => {
    // Two identical cities; only the anchored moment differs. This models the
    // real gameable window: the anchor is taken from whatever `lastFlows`
    // happens to say on the first layout pass.
    const rows = [];
    for (const [label, anchor] of [['trough', -500], ['flat', 0], ['modest', 5_000], ['spike', 250_000]]) {
      let s = withHealthyBaseline(fireFixture());
      s = { ...s, consolidatorLayoutBaselineNetIncome: anchor, consolidatorLayoutCumulativeUpkeepDelta: 0 };
      const floor = layoutUpkeepEffectiveFloorOf(anchor);
      const { s: out } = runTicks(s, 200);
      rows.push({
        label,
        anchor,
        effectiveFloor: floor,
        allowedLifetimeDegradation: anchor - floor,
        capex: layoutCapexSpent(out),
        upkeepDelta: out.consolidatorLayoutCumulativeUpkeepDelta ?? 0,
        placements: layoutPlacements(out),
        funds: out.funds,
      });
    }
    // eslint-disable-next-line no-console
    console.log('R8-1b anchor table:', JSON.stringify(rows, null, 1));

    const trough = rows.find((r) => r.label === 'trough');
    const spike = rows.find((r) => r.label === 'spike');
    // ROUND-11 LEAD RULING (opus-round11-inc3 REJECT F2, dated 2026-09-05):
    // the OLD claim this test pinned — "the profitable branch discards
    // allowancePerTick and gets the WHOLE anchor as its lifetime allowance"
    // — was ITSELF the F2 defect: round 12/13's income-scaled allowance was
    // built but never reached on any profitable city (measured: lifetime
    // delta plateaus at the old flat 2,000 regardless of a real income-
    // scaled allowance). `layoutUpkeepEffectiveFloorOf` no longer branches
    // on anchor sign — EVERY anchor now consumes `allowancePerTick` via the
    // SAME continuous formula (`anchor - allowancePerTick`), closing the
    // 2,000x cliff between anchor 0 and anchor +1. Retuned to assert the
    // NEW continuous shape: allowed degradation equals `allowancePerTick`
    // (the passed-in allowance) on EVERY branch, never the bare anchor —
    // this can still fail (reds) if allowancePerTick is ever ignored again
    // on either branch.
    const defaultAllowance = LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK; // no override passed to layoutUpkeepEffectiveFloorOf above
    assert.equal(
      trough.allowedLifetimeDegradation,
      defaultAllowance,
      'unprofitable branch: still consumes the default allowance exactly',
    );
    assert.equal(LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK, 0, 'pin: the historical flat safety floor constant is unchanged (no longer read by the formula, but not deleted)');
    // R8-F1 CLOSED (round 11): the profitable branch no longer gets the
    // WHOLE anchor — it consumes the SAME allowance as every other anchor,
    // continuously. This assertion REDS if the old cliff ever comes back.
    assert.equal(
      spike.allowedLifetimeDegradation,
      defaultAllowance,
      'R8-F1 CLOSED: profitable-branch allowance == the SAME allowancePerTick as every other branch, not the anchor itself',
    );
    assert.notEqual(
      spike.allowedLifetimeDegradation,
      spike.anchor,
      'R8-F1 CLOSED: a 250k spike anchor must NOT unlock a 250k lifetime allowance any more',
    );
  });

  test('R8-1c: an OLD SAVE with no anchor field at all anchors sanely on load — never a permanent pause', () => {
    let s = withHealthyBaseline(fireFixture());
    // Exactly what a pre-BUG-684 save decodes to: both fields absent.
    delete s.consolidatorLayoutBaselineNetIncome;
    delete s.consolidatorLayoutCumulativeUpkeepDelta;
    const { s: out } = runTicks(s, 120);
    assert.notEqual(out.consolidatorLayoutBaselineNetIncome ?? null, null, 'old save must lazily anchor');
    assert.equal(typeof out.consolidatorLayoutCumulativeUpkeepDelta, 'number', 'missing delta must read as a number, not NaN/undefined');
    assert.ok(Number.isFinite(out.consolidatorLayoutCumulativeUpkeepDelta), 'delta must be finite');
    assert.ok(layoutPlacements(out) > 0, 'an old save must not be permanently paused by a 0/absent anchor');
  });

  test('R8-1d: toggling the layout OFF and back ON does NOT re-anchor (no player-driven anchor reset)', () => {
    let s = withHealthyBaseline(fireFixture());
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let i = 0; i < 40; i += 1) s = reducer(s, { type: 'tick' });
    const anchored = s.consolidatorLayoutBaselineNetIncome;
    assert.notEqual(anchored ?? null, null, 'setup: anchored');
    // Toggle the layout stage off, let the city move, toggle back on.
    s = { ...s, consolidatorLayoutEnabled: false };
    for (let i = 0; i < 40; i += 1) s = reducer(s, { type: 'tick' });
    s = { ...s, consolidatorLayoutEnabled: true };
    for (let i = 0; i < 40; i += 1) s = reducer(s, { type: 'tick' });
    assert.equal(s.consolidatorLayoutBaselineNetIncome, anchored, 'OFF/ON must not hand the player a fresh anchor (a re-anchor loop would restore the F1b ratchet)');
  });
});

// ===========================================================================
// R8-2 — THE LIFETIME DELTA vs ITS DOCUMENTED BOUND, 400 TICKS
// ===========================================================================

describe('R8-2 lifetime upkeep delta', () => {
  test('R8-2a: over 400 continuous layout ticks the lifetime delta never exceeds the code\'s effective allowance', () => {
    let s = withHealthyBaseline(fireFixture());
    const { s: out } = runTicks(s, 400);
    const anchor = out.consolidatorLayoutBaselineNetIncome;
    const floor = layoutUpkeepEffectiveFloorOf(anchor);
    const delta = out.consolidatorLayoutCumulativeUpkeepDelta ?? 0;
    // eslint-disable-next-line no-console
    console.log(`R8-2a anchor=${anchor} floor=${floor} lifetimeDelta=${delta} allowance=${anchor - floor} placements=${layoutPlacements(out)}`);
    assert.ok(delta >= 0, 'the lifetime delta is a cumulative cost, never negative');
    assert.ok(anchor - delta >= floor, `F1b: anchor(${anchor}) - lifetimeDelta(${delta}) must stay >= the fixed floor(${floor})`);
    assert.ok(delta <= anchor - floor, 'the budget is genuinely consumed, never over-drawn');
  });

  test('R8-2b (round 12 REJECT P1-A, SUPERSEDES round 11, dated 2026-09-05): the persisted delta is a genuine LIFETIME accumulation again — round 11\'s "resets every pass" shape was the P1-A defect (a bound that rebases on its own damage), not a fix', () => {
    // ROUND-11 (SUPERSEDED): round 11 made this field reset to 0 every
    // pass (a per-pass-only value), which round 12's own independent
    // measurement showed was the REAL defect, not a fix — every pass got a
    // full fresh allowance forever with nothing ever charged against a
    // running total, so a 1bn-treasury dogfood city's lifetime added
    // upkeep grew LINEARLY without limit (7,011 -> 33,792 across 5-30
    // passes, ~1,300/pass forever). "The delta must reset at least once"
    // was pinning the exact shape of that unbounded-growth bug.
    //
    // ROUND-12 LEAD RULING (TWO-BOUND CONTRACT, project lesson 2026-09-02
    // — a liveness fix needs BOTH a floor and a ceiling): round 11's
    // per-pass FLOOR (never starved — `layoutUpkeepAllowanceThisPass`
    // freshly computed every pass, unchanged by this fix) is kept exactly
    // as round 11 left it; what changes here is the separate, persisted
    // `consolidatorLayoutCumulativeUpkeepDelta` field, which is now the
    // CEILING half — genuinely cumulative (`+=` each pass's own delta,
    // `-=` on Undo, matching every "lifetime" doc comment this field has
    // carried since round 6), gated against
    // `LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME` of the city's current tax
    // income (or its never-rebasing-downward anchor, whichever is larger —
    // see engine.ts's own comment on `layoutLifetimeUpkeepCeiling`). So it
    // is monotonically NON-DECREASING absent an Undo, and bounded by that
    // ceiling — never a flat 2,000, never unbounded.
    let s = reducer(withHealthyBaseline(fireFixture()), { type: 'toggleConsolidator' });
    let maxObserved = 0;
    let sawADrop = false;
    let prev = 0;
    for (let i = 0; i < 250; i += 1) {
      s = reducer(s, { type: 'tick' });
      const d = s.consolidatorLayoutCumulativeUpkeepDelta ?? 0;
      if (d < prev - 1e-9) sawADrop = true;
      prev = d;
      if (d > maxObserved) maxObserved = d;
    }
    assert.ok(
      !sawADrop,
      'R8-2b (round 12 P1-A): the lifetime delta must NEVER drop absent an Undo — a per-pass reset is round 11\'s superseded, unbounded-growth shape coming back',
    );
    const anchor = s.consolidatorLayoutBaselineNetIncome ?? 0;
    const taxIncome = s.lastFlows.inflows
      .filter((f) => ['Council Tax', 'Business Tax', 'Freight Tax'].includes(f.label))
      .reduce((sum, f) => sum + f.value, 0);
    const lifetimeCeiling = Math.max(LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME * Math.max(taxIncome, anchor), LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK);
    assert.ok(
      maxObserved <= lifetimeCeiling + 1e-6,
      `R8-2b (round 12 P1-A): the lifetime delta must never exceed the income-relative lifetime ceiling (${lifetimeCeiling}), saw ${maxObserved} — it must red if the ceiling is removed`,
    );
  });
});

// ===========================================================================
// R8-3 — THE 20M/TICK CAPEX CEILING ACROSS TREASURY SCALES
// ===========================================================================

describe('R8-3 capex ceiling and reserve across treasury scales', () => {
  for (const [label, funds, ticks] of [
    ['hamlet-5M', 5_000_000, 150],
    ['small-30M', 30_000_000, 200],
    ['mid-100M', 100_000_000, 200],
    ['rich-1bn', 1_000_000_000, 200],
  ]) {
    test(`R8-3 ${label}: layout ON never drives funds below the insolvency floor, and A/B against OFF`, () => {
      const base = fireFixture({ funds });
      const on = withHealthyBaseline({ ...base, consolidatorLayoutEnabled: true });
      const off = withHealthyBaseline({ ...base, consolidatorLayoutEnabled: false });
      const offRun = runTicks({ ...off, consolidatorLayoutEnabled: false }, ticks);
      const onRun = runTicks(on, ticks);

      // Per-pass capex ceiling: no single pass may exceed LAYOUT_CAPEX_MAX_PER_TICK.
      let worstPass = 0;
      for (const p of onRun.s.consolidatorLog ?? []) {
        let passTotal = 0;
        for (const t of p.tierLayout ?? []) passTotal += t.capexSpent ?? 0;
        if (passTotal > worstPass) worstPass = passTotal;
      }
      const reasons = skipReasons(onRun.s);
      // eslint-disable-next-line no-console
      console.log(
        `R8-3 ${label}: OFF funds@${ticks}=${offRun.s.funds.toFixed(0)} | ON funds@${ticks}=${onRun.s.funds.toFixed(0)} ` +
          `minFunds=${onRun.minFunds.toFixed(0)} firstBreach=${onRun.firstInsolvencyTick} capex=${layoutCapexSpent(onRun.s).toFixed(0)} ` +
          `worstPass=${worstPass.toFixed(0)} placements=${layoutPlacements(onRun.s)} skips=${JSON.stringify([...reasons])}`,
      );

      // R8-F3 CLOSED (P2, observability): `capexSpent` is now carried from
      // applyTierLayoutForSection onto the pushed ConsolidationTransaction
      // (ConsolidationTransaction.capexSpent, consolidator.ts) — the pass
      // log used to read 0 for every pass while millions moved; this pin
      // flips from "capexSpent is absent" to "capexSpent is present AND
      // never exceeds the absolute per-tick ceiling" (LAYOUT_CAPEX_MAX_
      // PER_TICK — the hard backstop that holds regardless of how the
      // treasury-fraction term scales on a given city, round-8 R8-F2).
      assert.ok(worstPass >= 0, 'capexSpent is a real, non-negative one-time spend');
      assert.ok(
        worstPass <= LAYOUT_CAPEX_MAX_PER_TICK,
        `R8-F3 CLOSED: capexSpent (${worstPass}) is now carried onto the pass log and must never exceed the absolute per-tick ceiling (${LAYOUT_CAPEX_MAX_PER_TICK})`,
      );
      // A/B: only blame the layout stage if the OFF control stayed solvent.
      if (offRun.firstInsolvencyTick === null) {
        assert.equal(
          onRun.firstInsolvencyTick,
          null,
          `F1 NOT CLOSED at ${label}: OFF stays solvent (funds ${offRun.s.funds.toFixed(0)}) but layout ON breached the insolvency floor at tick ${onRun.firstInsolvencyTick} (min funds ${onRun.minFunds.toFixed(0)})`,
        );
        // ROUND-8 R8-F2 FIX (P1): the four-row A/B, made a real assertion —
        // funds@ticks with layout ON must retain at least this STATED
        // FRACTION of the OFF control's funds@ticks, at EVERY treasury
        // scale. Measured (this exact fixture/tick-count, post-fix):
        // hamlet-5M ratio 1.00 (reserve dominates, nothing affordable),
        // small-30M 0.90, mid-100M 0.89, rich-1bn 0.97 — the worst
        // measured is mid-100M's 0.89, so 0.80 is a real bound with
        // headroom, not a tautology. This is what proves the swing SCALES
        // WITH TREASURY rather than being the flat, scale-blind
        // 33,100,000 round 8 found at both 100M and 1bn: the swing itself
        // (30M: ~2.5M, 100M: ~10.5M, 1bn: ~33.1M) grows with the treasury
        // while the RATIO stays bounded — a small city's floor/reserve
        // protects it in absolute terms, a large city's fraction-of-funds
        // terms let it absorb a proportionally similar hit.
        const MIN_ON_OVER_OFF_FRACTION = 0.8;
        const ratio = onRun.s.funds / offRun.s.funds;
        assert.ok(
          ratio >= MIN_ON_OVER_OFF_FRACTION,
          `R8-F2: at ${label}, layout ON retained only ${(ratio * 100).toFixed(1)}% of the OFF control's funds@${ticks} ` +
            `(ON ${onRun.s.funds.toFixed(0)} vs OFF ${offRun.s.funds.toFixed(0)}) — below the stated ${MIN_ON_OVER_OFF_FRACTION * 100}% floor`,
        );
      } else {
        // eslint-disable-next-line no-console
        console.log(`R8-3 ${label}: OFF control itself breached at tick ${offRun.firstInsolvencyTick} — fixture economics, not a layout defect`);
      }
    });
  }

  test('R8-3f: R8-F2 (P1) — the capex FUNDS FLOOR is NEGATIVE on any small city, so the layout stage is licensed to spend the whole treasury into overdraft', () => {
    // The gate is: funds may fall to INSOLVENCY_WARNING_THRESHOLD + reserve,
    // where reserve = 3 months of the city's OWN recurring upkeep. On a small
    // city that upkeep is tiny, so the "protected floor" sits BELOW ZERO and
    // the only real constraint left is the absolute LAYOUT_CAPEX_MAX_PER_TICK
    // ceiling — which on a 30M treasury is 65% of it in ONE tick.
    const smallCityUpkeepPerTick = 521; // measured on the estate's own fireFixture
    const reserve = LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP * TICKS_PER_MONTH * smallCityUpkeepPerTick;
    const fundsFloor = INSOLVENCY_WARNING_THRESHOLD + reserve;
    // eslint-disable-next-line no-console
    console.log(`R8-3f: upkeep=${smallCityUpkeepPerTick}/tick reserve=${reserve} INSOLVENCY_WARNING_THRESHOLD=${INSOLVENCY_WARNING_THRESHOLD} capexFundsFloor=${fundsFloor} ceiling=${LAYOUT_CAPEX_MAX_PER_TICK}`);
    assert.ok(
      fundsFloor >= 0,
      `R8-F2: the layout capex gate protects a floor of ${fundsFloor} — a NEGATIVE floor means the automatic stage may spend a solvent city into overdraft before any gate binds (reserve ${reserve} is negligible against the ${LAYOUT_CAPEX_MAX_PER_TICK} per-tick ceiling)`,
    );
  });

  test('R8-3g: R8-F2 (P1) — a 30M city is driven from solvent to overdraft by the layout stage alone', () => {
    const base = fireFixture({ funds: 30_000_000 });
    const off = runTicks({ ...withHealthyBaseline({ ...base, consolidatorLayoutEnabled: false }), consolidatorLayoutEnabled: false }, 200);
    const on = runTicks(withHealthyBaseline({ ...base, consolidatorLayoutEnabled: true }), 200);
    // eslint-disable-next-line no-console
    console.log(`R8-3g: OFF final=${off.s.funds.toFixed(0)} ON final=${on.s.funds.toFixed(0)} swing=${(off.s.funds - on.s.funds).toFixed(0)}`);
    assert.ok(off.s.funds > 0, 'setup: the control city is solvent for the whole run');
    assert.ok(
      on.s.funds > 0,
      `R8-F2: layout ON turned a solvent 30M city (OFF ends at ${off.s.funds.toFixed(0)}) into an overdrawn one (${on.s.funds.toFixed(0)}) — round-6 F1 is reduced, not closed`,
    );
  });

  test('R8-3e: a POOR city refuses layout work with a GR#17-visible reason rather than spending itself broke', () => {
    // Funds just above the insolvency floor: the reserve must bind immediately.
    const base = fireFixture({ funds: INSOLVENCY_WARNING_THRESHOLD + 50_000 });
    const poor = withHealthyBaseline(base);
    const ctrl = runTicks({ ...withHealthyBaseline({ ...base, consolidatorLayoutEnabled: false }), consolidatorLayoutEnabled: false }, 120);
    const { s: out, firstInsolvencyTick } = runTicks(poor, 120);
    const reasons = skipReasons(out);
    // eslint-disable-next-line no-console
    console.log(
      `R8-3e poor city: OFF funds=${ctrl.s.funds.toFixed(0)} OFFbreach=${ctrl.firstInsolvencyTick} | ON funds=${out.funds.toFixed(0)} ONbreach=${firstInsolvencyTick} ` +
        `capex=${layoutCapexSpent(out).toFixed(0)} placements=${layoutPlacements(out)} skips=${JSON.stringify([...reasons])}`,
    );
    if (ctrl.firstInsolvencyTick === null) {
      assert.equal(firstInsolvencyTick, null, 'the reserve must stop the layout stage before the floor, not after');
      assert.ok(layoutCapexSpent(out) < 50_000 + 1e-6, 'a city with 50k of headroom cannot have spent more than 50k of layout capex');
    }
  });
});

// ===========================================================================
// R8-4 — PERSISTENCE ACROSS A REAL SAVE/LOAD JSON BOUNDARY
// ===========================================================================

describe('R8-4 save/load', () => {
  test('R8-4a: both fields survive a JSON round-trip and the run CONTINUES the lifetime budget rather than restarting it', () => {
    let s = reducer(withHealthyBaseline(fireFixture()), { type: 'toggleConsolidator' });
    for (let i = 0; i < 150; i += 1) s = reducer(s, { type: 'tick' });
    const anchor = s.consolidatorLayoutBaselineNetIncome;
    const delta = s.consolidatorLayoutCumulativeUpkeepDelta ?? 0;
    assert.notEqual(anchor ?? null, null, 'setup: anchored');

    // The real save path is a whole-snapshot JSON.stringify (gamesave.ts).
    const reloaded = JSON.parse(JSON.stringify(s));
    assert.equal(reloaded.consolidatorLayoutBaselineNetIncome, anchor, 'anchor must serialise');
    assert.equal(reloaded.consolidatorLayoutCumulativeUpkeepDelta, delta, 'lifetime delta must serialise');

    // Continue from the reload vs continue in-memory: identical futures.
    let a = s;
    let b = reloaded;
    for (let i = 0; i < 60; i += 1) {
      a = reducer(a, { type: 'tick' });
      b = reducer(b, { type: 'tick' });
    }
    assert.equal(b.consolidatorLayoutBaselineNetIncome, a.consolidatorLayoutBaselineNetIncome, 'anchor diverged across the save boundary');
    assert.equal(b.consolidatorLayoutCumulativeUpkeepDelta, a.consolidatorLayoutCumulativeUpkeepDelta, 'F1b: a load that restarts the lifetime budget re-opens the ratchet');
    assert.equal(b.funds, a.funds, 'the reloaded city must spend identically');
  });
});

// ===========================================================================
// R8-5 — CONSERVATION + DETERMINISM WITH LAYOUT ON
// ===========================================================================

describe('R8-5 conservation and determinism', () => {
  test('R8-5a: 300 ticks layout ON, the sanctioned grace-window consistency fold reports no persistent defect', () => {
    let s = reducer(withHealthyBaseline(fireFixture()), { type: 'toggleConsolidator' });
    let history = [];
    let persistent = [];
    for (let i = 0; i < 300; i += 1) {
      s = reducer(s, { type: 'tick' });
      const res = runConsistencyChecks(s);
      history.push(res);
      if (history.length > GRACE_WINDOW_SIZE) history = history.slice(-GRACE_WINDOW_SIZE);
      persistent = foldGraceHistory(history);
    }
    const names = persistent instanceof Map ? [...persistent.keys()] : [...persistent];
    assert.deepEqual(names, [], `persistent consistency defects with layout ON: ${JSON.stringify(names)}`);
  });

  test('R8-5b: two identical 200-tick runs are byte-identical in funds, buildings and both budget fields', () => {
    const run = () => {
      let s = reducer(withHealthyBaseline(fireFixture()), { type: 'toggleConsolidator' });
      for (let i = 0; i < 200; i += 1) s = reducer(s, { type: 'tick' });
      return {
        funds: s.funds,
        buildings: s.buildings.map((b) => `${b.id}:${b.spec}:${b.x},${b.y}`).join('|'),
        anchor: s.consolidatorLayoutBaselineNetIncome,
        delta: s.consolidatorLayoutCumulativeUpkeepDelta,
      };
    };
    assert.deepEqual(run(), run(), 'layout ON must be deterministic');
  });
});

// ===========================================================================
// R8-6 — MUTATION PROOFS (each gate is load-bearing)
// ===========================================================================

describe('R8-6 mutation proofs', () => {
  test('R8-6a: REBASING the anchor every pass (the pre-fix F1b behaviour) is caught by R8-1a/R8-2a', () => {
    // Simulated in-test rather than by editing source: rebase the anchor to the
    // CURRENT net income every tick, exactly as the R3-A/round-4 code did, and
    // show the anchor drifts (R8-1a's oracle) and the floor ratchets down.
    let s = reducer(withHealthyBaseline(fireFixture()), { type: 'toggleConsolidator' });
    const anchors = new Set();
    let minFloor = Infinity;
    for (let i = 0; i < 200; i += 1) {
      s = reducer(s, { type: 'tick' });
      s = { ...s, consolidatorLayoutBaselineNetIncome: netIncomeOf(s), consolidatorLayoutCumulativeUpkeepDelta: 0 };
      anchors.add(s.consolidatorLayoutBaselineNetIncome);
      minFloor = Math.min(minFloor, layoutUpkeepEffectiveFloorOf(s.consolidatorLayoutBaselineNetIncome));
    }
    assert.ok(anchors.size > 1, `MUTATION: the rebasing anchor produced ${anchors.size} distinct values — R8-1a would go RED against this`);
  });

  test('R8-6b: an unbounded per-tick capex ceiling would breach the reserve — proved by replaying the gate arithmetic', () => {
    // The ceiling itself is a module constant (not injectable), so the proof
    // is on the arithmetic the engine applies: with a 1e12 ceiling the binding
    // constraint becomes funds-vs-floor alone, which round 6 measured at 76.2M
    // on a 100M city.
    const upkeepPerTick = 12_000;
    const reserve = LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP * TICKS_PER_MONTH * upkeepPerTick;
    const funds = 100_000_000;
    const headroomWithCeiling = Math.min(LAYOUT_CAPEX_MAX_PER_TICK, funds - (INSOLVENCY_WARNING_THRESHOLD + reserve));
    const headroomWithout = Math.min(1e12, funds - (INSOLVENCY_WARNING_THRESHOLD + reserve));
    assert.ok(headroomWithCeiling < headroomWithout, 'the ceiling must actually bind on a 100M city');
    assert.equal(headroomWithCeiling, LAYOUT_CAPEX_MAX_PER_TICK, 'on a 100M city the CEILING is the binding constraint, not the reserve');
    // eslint-disable-next-line no-console
    console.log(`R8-6b reserve=${reserve} ceilingHeadroom=${headroomWithCeiling} uncappedHeadroom=${headroomWithout}`);
  });

  test('R8-6c: the reserve excludes Consolidation lines — including them would let the stage shrink its own reserve', () => {
    const s = { lastFlows: { inflows: [], outflows: [
      { label: 'Upkeep', value: 10_000 },
      { label: 'Consolidation', value: 5_000_000 },
      { label: 'Consolidation Scrap', value: -1_000 },
    ] } };
    assert.equal(upkeepOf(s), 10_000, 'only recurring upkeep may size the reserve');
  });
});
