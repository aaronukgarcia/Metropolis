// bug-684-round3.test.mjs — BUG-684 RE-ROUND REJECT (opus-reround-bug684,
// 2026-09-06): "the floor is right, its INPUT is not."
//
//   Finding 1: consolidatorNetOutflowPerTick read `cur.lastFlows` once —
//   which is COLD (advance() computes/writes the real snapshot AFTER
//   applyConsolidatorPass has already run this same tick) on the very first
//   pass a city ever runs (genesis tick 0, a fresh load, or the tick the
//   consolidator is first toggled on). Measured on the round's own
//   9,000,000 fire-section fixture: cold read 91/tick, real structural
//   upkeep 7,635/tick — an under-read that let an unsafe merge through the
//   (correctly-shaped) runway floor on exactly that fixture/tick
//   (monthly-twelfth, pass at tick 0).
//
//   FIX: the outflow baseline's two DOMINANT, cheaply-structural components
//   — per-building upkeep and wages — are now computed DIRECTLY from
//   `cur.buildings`/`cur.population` every pass (never from `cur.lastFlows`,
//   which can lag by a full tick); everything else computeFlows charges
//   (transit subsidy, overdraft interest, bailout costs, grid import, etc.)
//   stays lastFlows-sourced, and a genuinely cold `cur.lastFlows` (both
//   arrays empty) is an explicit REFUSAL — never a permissive "treat as
//   zero" — for that residual term.
//
//   Finding 2 (BUG-796): the runway term now uses the POST-merge outflow —
//   the successor's own upkeep added, the absorbed originals' subtracted —
//   so a merge whose successor costs MORE to run than what it replaces is
//   correctly judged against the CITY THE MERGE CREATES, not the one that
//   exists right now.
//
//   Finding 3: a cash-positive city's runway term legitimately collapses to
//   zero (Math.max(0, outflow - inflow) inside consolidatorNetOutflowPerTick
//   already does this) — ONLY the netCost-sized floor term protects it in
//   that case. Documented and asserted explicitly below, not just implied.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
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
import { DEBT_THRESHOLD_FOR_BAILOUT } from '../src/sim/fiscal.ts';
import { runMutantSelfReinvoke } from '../testsupport/mutant.mjs';

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
function lastPass(s) {
  return (s.consolidatorLog ?? [])[0] ?? null;
}
const NET_COST = placementCost(SPECS.fire_station) - 5 * Math.round(placementCost(SPECS.fire_post) * CONSOLIDATOR_SCRAP_FRACTION);

// ---------------------------------------------------------------------------
// Finding 1: the attacker's EXACT cold-start shape — genesis tick -1 (the
// 'tick' action lands the pass at tick 0, monthly-twelfth), consolidator ON
// from the very first tick, `lastFlows` untouched (whatever initialState()
// itself produces — never hand-warmed).
// ---------------------------------------------------------------------------

/** 8 separate 5-post fire_post groups across 8 sections — mirrors round 2's
 *  own fire-section city, sited so section 0 (twelfth 0's own scope) is
 *  covered on the VERY FIRST possible boundary tick. */
function fireSectionCityColdStart(over = {}) {
  const buildings = [];
  let id = 100;
  const groups = 8;
  for (let g = 0; g < groups; g++) {
    const sx = (g % 4) * 16;
    const sy = Math.floor(g / 4) * 16;
    for (let x = 0; x <= 40; x++) buildings.push({ id: id++, spec: 'road', x: sx + x, y: sy + 15, builtTick: -1000 });
    for (let i = 0; i < 5; i++) buildings.push({ id: id++, spec: 'fire_post', x: sx + i, y: sy + 14, builtTick: -1000 });
  }
  const s = mk({
    buildings,
    tick: -1, // the 'tick' action below lands the FIRST pass at tick 0 (0 % TICKS_PER_MONTH === 0)
    consolidatorEnabled: true,
    consolidatorLayoutEnabled: false,
    nextId: 9000,
    ...over,
  });
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function runColdStart(funds, on, ticks) {
  let s = fireSectionCityColdStart({ funds, consolidatorEnabled: on });
  let minFunds = s.funds;
  let crossedBailoutAt = null;
  let firstPassTransactions = null;
  for (let i = 0; i < ticks; i++) {
    s = reducer(s, { type: 'tick' });
    if (i === 0) firstPassTransactions = lastPass(s)?.transactions?.length ?? 0;
    minFunds = Math.min(minFunds, s.funds);
    if (crossedBailoutAt === null && s.funds <= DEBT_THRESHOLD_FOR_BAILOUT) crossedBailoutAt = s.tick;
  }
  return { minFunds, crossedBailoutAt, finalFunds: s.funds, declineState: s.declineState, firstPassTransactions };
}

describe("BUG-684 RE-ROUND finding 1: the attacker's exact cold-start (tick 0, monthly-twelfth) table", () => {
  for (const funds of [9_000_000, 12_000_000, 20_000_000]) {
    test(`funds=${funds}: cold-start ON never goes negative over 900 ticks, never crosses bailout`, () => {
      const off = runColdStart(funds, false, 900);
      const on = runColdStart(funds, true, 900);
      // eslint-disable-next-line no-console
      console.log(
        `BUG-684 cold-start table: funds=${funds}  OFF min=${off.minFunds} final=${off.finalFunds}  ` +
          `ON min=${on.minFunds} final=${on.finalFunds} crossedBailout=${on.crossedBailoutAt} ` +
          `firstPassTxns=${on.firstPassTransactions}`,
      );
      assert.ok(off.minFunds >= 0, `setup: OFF arm never goes negative either (min ${off.minFunds})`);
      // RED-PROOF: this is the assertion the re-round's own reproduction
      // flips if consolidatorNetOutflowPerTick goes back to reading purely
      // from cur.lastFlows on this exact cold-start shape.
      assert.ok(on.minFunds >= 0, `ON never goes negative over 900 ticks at funds=${funds} (min ${on.minFunds})`);
      assert.equal(on.crossedBailoutAt, null, `ON never crosses DEBT_THRESHOLD_FOR_BAILOUT at funds=${funds}`);
      assert.equal(on.declineState, null, `ON never reaches FINAL DECLINE at funds=${funds}`);
    });
  }
});

// ---------------------------------------------------------------------------
// Finding 1 (warm vs cold): the SAME small-city fixture (BUG-684's own
// original 2,000,000-5,000,000 reproduction range) must reach the SAME
// decision whether lastFlows is warm (a real prior tick's snapshot) or
// genuinely cold (hand-forced empty) — the structural upkeep term is
// identical either way, and at THIS treasury scale the netCost-sized floor
// term dominates regardless, so cold's explicit refusal and warm's normal
// floor evaluation land on the same outcome for the same underlying reason.
// ---------------------------------------------------------------------------

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
      consolidatorLayoutEnabled: false,
      nextId: 9000,
      ...over,
    }),
  );
}

describe('BUG-684 RE-ROUND finding 1: warm vs cold produce the same decision (small-city scale)', () => {
  test(`funds=${NET_COST + 60_000}: warm (real lastFlows) and cold (hand-forced empty) both refuse, same reason`, () => {
    const funds = NET_COST + 60_000;
    const warm = reducer(smallCityFixture({ funds }), { type: 'tick' });
    const cold = reducer(smallCityFixture({ funds, lastFlows: { inflows: [], outflows: [] } }), { type: 'tick' });
    assert.equal(lastPass(warm).transactions.length, 0, 'setup: warm refuses (netCost-sized floor dominates at this scale regardless of outflow)');
    assert.equal(lastPass(cold).transactions.length, 0, 'cold ALSO refuses');
    assert.deepEqual(
      lastPass(warm).skipped.map((k) => k.reason),
      lastPass(cold).skipped.map((k) => k.reason),
      'warm and cold reach the identical decision (same skip reason) at this treasury scale',
    );
  });

  test('documented asymmetry (NOT a regression): a cold refusal is never MORE permissive than warm, but may be MORE strict — a rich city that warm would merge, cold correctly still refuses until flows warm up', () => {
    const warmRich = reducer(smallCityFixture({ funds: 100_000_000 }), { type: 'tick' });
    const coldRich = reducer(smallCityFixture({ funds: 100_000_000, lastFlows: { inflows: [], outflows: [] } }), { type: 'tick' });
    assert.equal(lastPass(warmRich).transactions.length, 1, 'setup: warm, wealthy, merges normally');
    assert.equal(lastPass(coldRich).transactions.length, 0, 'cold refuses even here — the round\'s explicit "never permissive" mandate, not a bug');
    assert.ok(lastPass(coldRich).skipped.some((k) => k.reason === 'funds floor'));
  });
});

// ---------------------------------------------------------------------------
// Finding 2 (BUG-796): post-merge outflow — a successor whose upkeep EXCEEDS
// the absorbed originals' is refused where the PRE-merge figure would have
// allowed it.
// ---------------------------------------------------------------------------

function bizParkFixture(funds) {
  const bp = [];
  for (let i = 0; i < 8; i++) bp.push({ id: 100 + i, spec: 'off_businesspark', x: 16 + i, y: 14, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'off_tower_canary', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'off_tower_canary', x: 210, y: 200, builtTick: -1000 },
    { id: 902, spec: 'off_tower_canary', x: 220, y: 200, builtTick: -1000 },
    { id: 903, spec: 'off_tower_canary', x: 230, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(
    mk({
      buildings: [...roadRow(15, 40), ...bp, ...headroom],
      tick: TICKS_PER_MONTH - 1,
      consolidatorEnabled: true,
      consolidatorLayoutEnabled: false,
      nextId: 9000,
      funds,
    }),
  );
}

describe('BUG-684 RE-ROUND finding 2 (BUG-796): successor-upkeep-worse is refused where pre-merge would allow', () => {
  test('off_businesspark (8x, upkeep 420 each = 3,360/tick removed) -> off_tower_canary (upkeep 3,500/tick added): funds=2,300,000 is refused', () => {
    // MEASURED (not hand-derived): with the FIXED post-merge outflow, the
    // floor first clears between 2,400,000 and 2,450,000 funds on this
    // fixture; with the pre-merge-ONLY outflow (the shape this fix
    // replaces), it already clears between 2,200,000 and 2,300,000 — so
    // 2,300,000 is refused by the fix and would NOT have been refused by
    // the superseded formula. Both specs are 'zones' (placementCost 0), so
    // netCost is 0 here — this isolates the outflow/runway term entirely,
    // with zero interference from the netCost-sized floor term.
    assert.equal(placementCost(SPECS.off_businesspark), 0, 'setup: zone spec, netCost term is 0');
    assert.equal(placementCost(SPECS.off_tower_canary), 0, 'setup: zone spec, netCost term is 0');
    assert.ok(SPECS.off_tower_canary.upkeep > 8 * SPECS.off_businesspark.upkeep, 'setup: the successor genuinely costs more to run than the group it replaces (BUG-796 shape)');

    const s1 = reducer(bizParkFixture(2_300_000), { type: 'tick' });
    const pass = lastPass(s1);
    // RED-PROOF: this is the assertion that flips if the runway term goes
    // back to reading the PRE-merge consolidatorNetOutflowPerTick instead of
    // postMergeNetOutflowPerTick — verified live during development
    // (reverting that one substitution let this exact fixture clear the
    // floor and fall through to an unrelated later gate instead).
    assert.equal(pass.transactions.length, 0, 'BUG-796: refused because the successor makes the city more expensive to run');
    assert.ok(pass.skipped.some((k) => k.reason === 'funds floor'), 'and the refusal is on the record');
  });

  test('RED-PROOF (source revert, private shadow copy): using the pre-merge outflow instead of the post-merge one reproduces the BUG-796 gap', () => {
    const { failed, output, crashed } = runMutantSelfReinvoke({
      targetRelPath: path.join('sim', 'engine.ts'),
      mutate: (original) => {
        const fixedLine = 'const consolidatorFundsFloor = consolidatorFundsFloorFor(netCost, postMergeNetOutflowPerTick);';
        assert.ok(original.includes(fixedLine), 'precondition: the fixed post-merge floor computation line is present verbatim');
        const buggyLine = 'const consolidatorFundsFloor = consolidatorFundsFloorFor(netCost, consolidatorNetOutflowPerTick);';
        return original.replace(fixedLine, buggyLine);
      },
      testFileAbsPath: fileURLToPath(import.meta.url),
      testNamePattern: 'off_businesspark \\(8x, upkeep 420 each = 3,360/tick removed\\) -> off_tower_canary \\(upkeep 3,500/tick added\\): funds=2,300,000 is refused',
    });
    assert.ok(!crashed, `the re-invoked test must actually RUN against the mutant; output:\n${output}`);
    assert.ok(failed, 'the successor-worse test must FAIL against the pre-merge-only (reverted) outflow term');
    // Under the mutant, transactions.length is STILL 0 (the pre-merge floor
    // clears, but the pass falls through to an unrelated later gate — see
    // the manual verification in this fix's own doc) — the assertion that
    // actually flips is the SKIP REASON no longer being 'funds floor'.
    assert.match(output, /and the refusal is on the record/, `child output must report the SPECIFIC reason-mismatch assertion failing; got:\n${output}`);
  });
});

// ---------------------------------------------------------------------------
// Finding 3: a cash-positive city's runway term collapses to zero — only the
// netCost-sized floor term protects it. Documented explicitly, not just
// implied by Math.max(0, ...).
// ---------------------------------------------------------------------------

describe('BUG-684 RE-ROUND finding 3: cash-positive cities — the runway term collapses to zero by design', () => {
  test('a huge structural inflow clamps consolidatorNetOutflowPerTick to 0 — the merge boundary is IDENTICAL regardless of how large the surplus is', () => {
    // DOCUMENTED, INTENDED (not a gap): consolidatorNetOutflowPerTick is
    // `Math.max(0, outflow - inflow)` — a cash-positive city (inflow >
    // outflow) reads 0 here regardless of the surplus's size, so
    // consolidatorFundsFloorFor's `max(spend, 0 * CONSOLIDATOR_MIN_RUNWAY_
    // TICKS)` collapses to `spend` alone. On THIS fixture (fire_post ->
    // fire_station, netCost 4,140,000) the per-pass net-spend CEILING
    // (CONSOLIDATOR_NET_SPEND_MAX_FRACTION_PER_PASS, 0.5) is the term that
    // actually binds tightest once outflow is neutralised — MEASURED
    // (8,280,000 exactly, = netCost / 0.5), not hand-derived from the floor
    // formula alone — but the POINT is unaffected: this boundary is
    // ENTIRELY a function of netCost/funds, never of outflow, so it must be
    // IDENTICAL whether the hand-set inflow is 10,000,000/tick or
    // 100,000,000/tick, proving the runway term contributes nothing once
    // outflow is dominated.
    const boundaryFunds = 8_280_000;
    for (const inflow of [10_000_000, 100_000_000]) {
      const justBelow = reducer(
        smallCityFixture({ funds: boundaryFunds - 1, lastFlows: { inflows: [{ label: 'Test Inflow', value: inflow }], outflows: [] } }),
        { type: 'tick' },
      );
      const atBoundary = reducer(
        smallCityFixture({ funds: boundaryFunds, lastFlows: { inflows: [{ label: 'Test Inflow', value: inflow }], outflows: [] } }),
        { type: 'tick' },
      );
      assert.equal(lastPass(justBelow).transactions.length, 0, `inflow=${inflow}: one pound short of the boundary still refuses`);
      assert.equal(lastPass(atBoundary).transactions.length, 1, `inflow=${inflow}: exactly at the boundary, the merge commits — the runway term added nothing, regardless of surplus size`);
    }
  });
});
