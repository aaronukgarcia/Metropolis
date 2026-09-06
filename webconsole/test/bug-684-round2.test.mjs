// bug-684-round2.test.mjs — BUG-684 ROUND REJECT (opus-round-bug684,
// 2026-09-06) fixes:
//
//   Finding 1: the original F1 floor (6-month-of-outflow OR 0.2-of-funds
//   reserve) was ITSELF too short — measured live on a 9,000,000
//   fire-section city: consolidator ON crossed into bailout at tick 609,
//   OFF never dropped below +2,280,000. FIXED: the floor is now a per-
//   candidate RUNWAY check — a merge is refused unless, after paying it,
//   `(funds - netCost) / max(netOutflowPerTick, 1) >=
//   CONSOLIDATOR_MIN_RUNWAY_TICKS` (900) — combined via `max` with a
//   netCost-sized floor so a low-outflow small city (BUG-684's original
//   2,000,000-5,000,000 reproduction) stays protected too.
//
//   Finding 2 (GR#3): the RECONNECT lane (applyConsolidatorPass, runs
//   BEFORE the density phase) still gated on the bare
//   INSOLVENCY_WARNING_THRESHOLD. FIXED: both the per-building break and the
//   per-section rollback check now route through the SAME
//   consolidatorFundsFloorFor the density lane uses.
//
//   Pin gap: the floor-reserve mutant (reserve zeroed) and the headroom
//   mutant (flat threshold restored) both survived every one of the 34
//   round-1 tests — none of those fixtures could isolate the floor as the
//   SOLE refusing gate (two were vacuous: blocked by the bare affordability
//   check first, with an assertion that accepted either reason). FIXED:
//   dedicated fixtures below construct funds >= netCost with funds - netCost
//   strictly between the affordability/ceiling gates and the runway floor,
//   proving each mutant RED individually via the sanctioned shadow-copy
//   mutation harness (testsupport/mutant.mjs — never touches the real tree).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
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
import { INSOLVENCY_WARNING_THRESHOLD, DEBT_THRESHOLD_FOR_BAILOUT } from '../src/sim/fiscal.ts';
import { runMutantSelfReinvoke, createMutantShadow } from '../testsupport/mutant.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
// LAZY (BUG-739 class): `runMutantSelfReinvoke` re-invokes this WHOLE file
// inside a shadow copy that has no .git directory at all — a module-scope
// `git rev-parse` here would throw on EVERY mutant re-invocation, not just
// the one test that needs it. Computed only inside the '100,000,000' test,
// inside a try/catch, so the shadow re-invocation of the mutation-testing
// sibling tests never touches git at all.
function repoRootOrNull() {
  try {
    return execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: __dirname, encoding: 'utf8' }).trim();
  } catch {
    return null;
  }
}

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
// Finding 1: the 9,000,000/12,000,000/20,000,000 fire-section table.
// ---------------------------------------------------------------------------

/**
 * A "fire-section" city: 8 separate 5-post fire_post groups scattered across
 * 8 different consolidator sections (16-tile grid), each road-adjacent to
 * its own road row but sited far enough inside the section to force a real
 * connector spend on merge (mirrors the estate's own fireFixture shape,
 * scaled to several independent groups so a 900-tick run has more than one
 * merge opportunity to draw from).
 */
function fireSectionCity(over = {}) {
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
    tick: TICKS_PER_MONTH - 1,
    consolidatorEnabled: true,
    consolidatorLayoutEnabled: false,
    nextId: 9000,
    ...over,
  });
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function runFireSection(funds, on, ticks) {
  let s = fireSectionCity({ funds, consolidatorEnabled: on });
  let minFunds = s.funds;
  let crossedBailoutAt = null;
  for (let i = 0; i < ticks; i++) {
    s = reducer(s, { type: 'tick' });
    minFunds = Math.min(minFunds, s.funds);
    if (crossedBailoutAt === null && s.funds <= DEBT_THRESHOLD_FOR_BAILOUT) crossedBailoutAt = s.tick;
  }
  return { minFunds, crossedBailoutAt, finalFunds: s.funds, declineState: s.declineState };
}

describe('BUG-684 ROUND REJECT finding 1: the 9M/12M/20M fire-section table', () => {
  for (const funds of [9_000_000, 12_000_000, 20_000_000]) {
    test(`funds=${funds}: consolidator ON never goes negative over 900 ticks, and never crosses DEBT_THRESHOLD_FOR_BAILOUT`, () => {
      const off = runFireSection(funds, false, 900);
      const on = runFireSection(funds, true, 900);
      // eslint-disable-next-line no-console
      console.log(
        `BUG-684 table: funds=${funds}  OFF min=${off.minFunds} final=${off.finalFunds}  ` +
          `ON min=${on.minFunds} final=${on.finalFunds} crossedBailout=${on.crossedBailoutAt}`,
      );
      assert.ok(off.minFunds >= 0, `setup: OFF arm (ordinary upkeep only) never goes negative either (min ${off.minFunds})`);
      // RED-PROOF: this is the assertion the round's own reproduction flips —
      // verified live during development that reverting consolidatorFundsFloorFor
      // to the pre-round-2 fixed-6-month/0.2-of-funds shape (this exact
      // fixture, this exact funds level) still stayed positive on THIS
      // fixture (the finding's own fire-section city had a different outflow
      // shape); the isolated 'headroom'/'isolate-floor' tests below pin the
      // runway mechanism directly and RED-prove it against the two mutants
      // the round named.
      assert.ok(on.minFunds >= 0, `ON never goes negative over 900 ticks at funds=${funds} (min ${on.minFunds})`);
      assert.equal(on.crossedBailoutAt, null, `ON never crosses DEBT_THRESHOLD_FOR_BAILOUT at funds=${funds}`);
      assert.equal(on.declineState, null, `ON never reaches FINAL DECLINE at funds=${funds}`);
    });
  }

  test('100,000,000: byte-identical to the pre-BUG-684 git HEAD (this fix never binds at that scale)', async (t) => {
    // Compares the LIVE (fixed) engine.ts against a PINNED pre-fix commit
    // (630b104, the parent of BUG-684's a48f68c landing), both run against
    // the IDENTICAL fireSectionCity fixture for 400 ticks — a rigorous
    // version of "unaffected at scale", not just an assertion that the
    // transaction still happens. Uses createMutantShadow's in-process
    // shadow-copy mechanism (testsupport/mutant.mjs) so the real src tree is
    // never touched even transiently.
    //
    // CI FIX (2026-09-06): this originally read `git show HEAD:...` as the
    // pre-fix baseline, which is correct only on a checkout where HEAD has
    // NOT yet landed the fix — once a48f68c merges to the branch CI actually
    // runs against, HEAD *is* the fix, so `headEngineSrc === original` and
    // the test proves nothing (or, worse, silently compares the fix against
    // itself). Pinning to the known pre-fix commit makes the comparison
    // meaningful regardless of which commit HEAD is on. A shallow clone
    // without that commit available skips outright rather than fabricate a
    // baseline.
    const PRE_FIX_COMMIT = '630b104';
    const REPO_ROOT = repoRootOrNull();
    assert.ok(REPO_ROOT, 'setup: this test needs a real .git checkout (never true inside a mutant shadow re-invocation)');
    let headEngineSrc;
    try {
      headEngineSrc = execFileSync('git', ['show', `${PRE_FIX_COMMIT}:webconsole/src/sim/engine.ts`], {
        cwd: REPO_ROOT,
        encoding: 'utf8',
      });
    } catch {
      t.skip(`BUG-684: pre-fix baseline commit ${PRE_FIX_COMMIT} unavailable in this clone`);
      return;
    }
    const shadow = createMutantShadow({
      targetRelPath: 'sim/engine.ts',
      mutate: (original) => {
        assert.notEqual(headEngineSrc, original, `setup: ${PRE_FIX_COMMIT} engine.ts must differ from the live (fixed) file, or this proves nothing`);
        return headEngineSrc;
      },
    });
    try {
      // fireSectionCity/mk close over live-imported helpers (computeRoadConnectivity
      // etc.) — rebuild the equivalent fixture using EACH module's own
      // exports so both trajectories are self-consistent within their own
      // module graph (the shadow's data.ts is a byte-identical copy of the
      // live one unless BUG-684 touched it, which it did not).
      const buildFixture = (engineMod, dataMod, funds) => {
        const base = engineMod.initialState();
        const buildings = [];
        let id = 100;
        for (let g = 0; g < 8; g++) {
          const sx = (g % 4) * 16;
          const sy = Math.floor(g / 4) * 16;
          for (let x = 0; x <= 40; x++) buildings.push({ id: id++, spec: 'road', x: sx + x, y: sy + 15, builtTick: -1000 });
          for (let i = 0; i < 5; i++) buildings.push({ id: id++, spec: 'fire_post', x: sx + i, y: sy + 14, builtTick: -1000 });
        }
        const s0 = {
          ...base,
          unlockedAll: true,
          roadMonitors: [],
          buildingMonitors: [],
          buildings,
          population: 0,
          funds,
          tick: TICKS_PER_MONTH - 1,
          consolidatorEnabled: true,
          consolidatorLayoutEnabled: false,
          consolidatorLog: [],
          nextId: 9000,
          xp: engineMod.xpForLevel(engineMod.CONSOLIDATOR_UNLOCK_LEVEL),
          lastRewardedLevel: engineMod.levelOf(engineMod.xpForLevel(engineMod.CONSOLIDATOR_UNLOCK_LEVEL)),
          consolidatorMode: 'monthly-twelfth',
        };
        return { ...s0, roadConnectivity: dataMod.computeRoadConnectivity(s0) };
      };
      const runTicks = (engineMod, s0, ticks) => {
        let s = s0;
        for (let i = 0; i < ticks; i++) s = engineMod.reducer(s, { type: 'tick' });
        return s;
      };

      const headEngine = await import(shadow.importUrl('sim/engine.ts'));
      const headData = await import(shadow.importUrl('sim/data.ts'));
      const liveEngine = await import('../src/sim/engine.ts');
      const liveData = await import('../src/sim/data.ts');

      const TICKS = 400;
      const headFinal = runTicks(headEngine, buildFixture(headEngine, headData, 100_000_000), TICKS);
      const liveFinal = runTicks(liveEngine, buildFixture(liveEngine, liveData, 100_000_000), TICKS);

      assert.equal(liveFinal.funds, headFinal.funds, 'funds trajectory identical at 100,000,000 (the fix never binds at this scale)');
      assert.equal(liveFinal.buildings.length, headFinal.buildings.length, 'same number of buildings built/demolished');
      assert.deepEqual(
        JSON.parse(JSON.stringify(liveFinal.consolidatorLog ?? [])),
        JSON.parse(JSON.stringify(headFinal.consolidatorLog ?? [])),
        'consolidatorLog (every pass, every transaction, every skip) is byte-identical',
      );
    } finally {
      shadow.cleanup();
    }
  });
});

// ---------------------------------------------------------------------------
// Finding 2: the RECONNECT lane now routes through consolidatorFundsFloorFor.
// ---------------------------------------------------------------------------

function reconnectFixture(funds, outflow) {
  const buildings = [...roadRow(0, 40), { id: 500, spec: 'res_hut', x: 25, y: 10, builtTick: -1000 }];
  return withConnectivity(
    mk({
      buildings,
      tick: TICKS_PER_MONTH - 1,
      consolidatorEnabled: true,
      consolidatorLayoutEnabled: false,
      nextId: 9000,
      funds,
      // BUG-684 RE-ROUND FIX note: a genuinely EMPTY lastFlows (both arrays)
      // is now the explicit "cold" refusal condition (see
      // bug-684-round3.test.mjs) — this fixture's own subject is the
      // OUTFLOW-vs-floor mechanism, not cold-handling, so a placeholder
      // zero-value inflow keeps `outflow == null` meaning "no EXTRA
      // (non-structural) outflow" without accidentally tripping the
      // separate cold gate.
      lastFlows: {
        inflows: [{ label: 'Placeholder', value: 0 }],
        outflows: outflow != null ? [{ label: 'Test Outflow', value: outflow }] : [],
      },
    }),
  );
}

describe('BUG-684 ROUND REJECT finding 2: the reconnect lane routes through consolidatorFundsFloorFor', () => {
  test('a reconnect-heavy fixture: affordable by the flat threshold, refused by the scaled runway floor', () => {
    // res_hut is a free (zone-category) spec — placementCost 0 — so the
    // reconnect spend here is PURELY the connector road tiles autoConnect
    // lays, never the building itself; a real, non-trivial ~100,000+ spend
    // on this fixture's own 9-tile route, comfortably affordable against
    // the flat INSOLVENCY_WARNING_THRESHOLD (-750,000) at 500,000 funds, but
    // NOT against a scaled floor once outflow is high enough that
    // outflow * CONSOLIDATOR_MIN_RUNWAY_TICKS dominates.
    const affordableUnderFlat = reducer(reconnectFixture(500_000, null), { type: 'tick' });
    assert.equal(lastPass(affordableUnderFlat)?.transactions.length, 1, 'setup: with zero extra outflow, the reconnect goes through (the netCost-sized floor term is trivially small here)');

    const refusedByRunway = reducer(reconnectFixture(500_000, 2_000), { type: 'tick' });
    const pass = lastPass(refusedByRunway);
    // RED-PROOF: this is the assertion that flips if EITHER of the two
    // reconnect-lane check sites (engine.ts, the per-building break and the
    // per-section rollback) is reverted to the bare INSOLVENCY_WARNING_
    // THRESHOLD — verified live during development (temporarily reverting
    // both sites made this exact fixture commit the reconnect transaction
    // instead of refusing it).
    assert.equal(pass.transactions.length, 0, 'BUG-684 finding 2: the reconnect is refused once outflow makes the runway floor bind');
    assert.ok(pass.skipped.some((k) => k.reason === 'funds floor'), 'and the refusal is on the record with the SAME reason the density lane uses');
    assert.equal(refusedByRunway.buildings.length, reconnectFixture(500_000, 2_000).buildings.length, 'nothing was laid — the res_hut stays exactly as stranded as before');
  });

  test('RED-PROOF (source revert, private shadow copy): reverting the reconnect lane back to the bare INSOLVENCY_WARNING_THRESHOLD reproduces finding 2', () => {
    const { failed, output, crashed } = runMutantSelfReinvoke({
      targetRelPath: path.join('sim', 'engine.ts'),
      mutate: (original) => {
        const fixedBreak = 'if (attempt.funds < consolidatorFundsFloorFor(preFunds - attempt.funds, consolidatorNetOutflowPerTick)) break;';
        const fixedRollback = 'if (attempt.funds < consolidatorFundsFloorFor(spend, consolidatorNetOutflowPerTick)) {';
        assert.ok(original.includes(fixedBreak), 'precondition: the fixed per-building break check is present');
        assert.ok(original.includes(fixedRollback), 'precondition: the fixed per-section rollback check is present');
        return original
          .replace(fixedBreak, 'if (attempt.funds < INSOLVENCY_WARNING_THRESHOLD) break;')
          .replace(fixedRollback, 'if (attempt.funds < INSOLVENCY_WARNING_THRESHOLD) {');
      },
      testFileAbsPath: fileURLToPath(import.meta.url),
      testNamePattern: 'a reconnect-heavy fixture: affordable by the flat threshold, refused by the scaled runway floor',
    });
    assert.ok(!crashed, `the re-invoked test must actually RUN against the mutant; output:\n${output}`);
    assert.ok(failed, 'the reconnect-floor test must FAIL against the flat-threshold (reverted) reconnect lane');
    assert.match(output, /BUG-684 finding 2: the reconnect is refused/, `child output must report the SPECIFIC finding-2 assertion failing; got:\n${output}`);
  });
});

// ---------------------------------------------------------------------------
// Pin gap: fixtures that isolate the floor as the SOLE refusing gate, each
// proven to RED under its named mutant via the shadow-copy harness.
// ---------------------------------------------------------------------------

function isolateFloorFixture(funds) {
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
      funds,
      // 20,000/tick outflow: outflow * 900 = 18,000,000, which STRICTLY
      // dominates this family's netCost (4,140,000) — chosen specifically so
      // the runway term, not the netCost term, is what binds, and so the
      // per-pass net-spend ceiling (0.5 * funds) is comfortably clear at
      // funds=20,000,000 (10,000,000 >= 4,140,000) — isolating the FLOOR as
      // the one and only gate that can refuse this candidate.
      lastFlows: { inflows: [], outflows: [{ label: 'Test Outflow', value: 20_000 }] },
    }),
  );
}

describe('BUG-684 pin gap: isolate-the-floor fixture (funds >= netCost, ceiling clear, ONLY the runway floor refuses)', () => {
  test('funds=20,000,000: base affordability and the per-pass ceiling both clear; the reason is EXACTLY \'funds floor\'', () => {
    const s0 = isolateFloorFixture(20_000_000);
    assert.ok(s0.funds >= NET_COST, 'setup: funds clears the bare affordability check');
    assert.ok(0.5 * s0.funds >= NET_COST, 'setup: funds clears the per-pass net-spend ceiling too');
    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    assert.equal(pass.transactions.length, 0, 'the merge is refused');
    assert.equal(pass.skipped.length, 1, 'exactly one skip reason recorded, not a pile of overlapping ones');
    assert.equal(pass.skipped[0].reason, 'funds floor', "RED-PROOF: the reason is EXACTLY 'funds floor', not 'insufficient funds' or 'action budget'");
    // Control: one tick above the floor's own boundary (25,000,000 — margin
    // 20,860,000 clears floor 17,250,000) DOES commit, proving this fixture
    // genuinely isolates the floor rather than being permanently stuck.
    const s2 = reducer(isolateFloorFixture(25_000_000), { type: 'tick' });
    assert.equal(lastPass(s2).transactions.length, 1, 'control: a wealthier city (same outflow) the floor genuinely permits DOES merge');
  });

  test('RED-PROOF (source revert, private shadow copy): zeroing the reserve term reproduces the floor-reserve mutant', () => {
    const { failed, output, crashed } = runMutantSelfReinvoke({
      targetRelPath: path.join('sim', 'engine.ts'),
      mutate: (original) => {
        const fixedFn = 'return INSOLVENCY_WARNING_THRESHOLD + Math.max(spend, netOutflowPerTick * CONSOLIDATOR_MIN_RUNWAY_TICKS);';
        assert.ok(original.includes(fixedFn), 'precondition: the fixed consolidatorFundsFloorFor body is present');
        // The named mutant: "the reserve zeroed" — drop the max(...) term
        // entirely, collapsing the floor back to the bare flat threshold.
        return original.replace(fixedFn, 'return INSOLVENCY_WARNING_THRESHOLD;');
      },
      testFileAbsPath: fileURLToPath(import.meta.url),
      testNamePattern: "funds=20,000,000: base affordability and the per-pass ceiling both clear; the reason is EXACTLY 'funds floor'",
    });
    assert.ok(!crashed, `the re-invoked test must actually RUN against the mutant; output:\n${output}`);
    assert.ok(failed, 'the isolate-floor test must FAIL against a zeroed reserve (the merge would wrongly commit)');
    assert.match(output, /the merge is refused/, `child output must report the SPECIFIC 'the merge is refused' assertion failing; got:\n${output}`);
  });
});

function headroomBreachFixture(funds, outflow) {
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
      funds,
      lastFlows: { inflows: [], outflows: [{ label: 'Test Outflow', value: outflow }] },
    }),
  );
}

describe('BUG-684 pin gap: headroom fixture (the connector grant would breach the scaled floor, not the flat one)', () => {
  test('funds=55,000,000, outflow=56,500/tick (hand-set) + structural upkeep: the pre-check clears, but the connector spend must never be allowed to breach the floor', () => {
    const FUNDS = 55_000_000;
    const OUTFLOW = 56_500;
    // BUG-684 RE-ROUND FIX note: the floor's outflow term is no longer JUST
    // the hand-set `lastFlows` figure — it is
    // `consolidatorBuildingUpkeepPerTick (structural, computed from the
    // fixture's own ONLINE buildings) + OUTFLOW`, then adjusted post-merge
    // (successor upkeep 480 added, the group's removed upkeep 500
    // subtracted — see BUG-796's own doc). The four headroom fire_stations
    // in this fixture sit far from any road (OFFLINE, per computeFlows'
    // own isOnline gate), so they do NOT contribute; only the 5
    // road-adjacent fire_post do (5 x 100 = 500/tick). These numbers are
    // MEASURED against the live gate below (not re-derived by hand as the
    // test's own gate) — the setup assertions confirm the SHAPE (small
    // positive headroom), the live pass is what actually proves the point.
    const structuralUpkeep = 500;
    const postMergeOutflow = structuralUpkeep + OUTFLOW + SPECS.fire_station.upkeep - 5 * SPECS.fire_post.upkeep;
    const floor = INSOLVENCY_WARNING_THRESHOLD + Math.max(NET_COST, postMergeOutflow * 900);
    const margin = FUNDS - NET_COST;
    assert.ok(margin >= floor, `setup: the base netCost check clears the floor by design (margin ${margin} >= floor ${floor})`);
    assert.ok(margin - floor < 500_000, 'setup: the headroom above the floor is deliberately small — smaller than a real connector route on this fixture');

    const s0 = headroomBreachFixture(FUNDS, OUTFLOW);
    const s1 = reducer(s0, { type: 'tick' });
    const pass = lastPass(s1);
    // Whatever the pass DID (refuse the merge outright, or refuse the
    // successor for being left offline because the connector could not be
    // fully afforded within the tight headroom) — the one invariant that
    // must NEVER be violated is that a COMMITTED transaction's real spend
    // never leaves funds below the scaled floor computed for its own
    // netCost. This is checked directly, independent of exactly which
    // skip reason fired, so it is robust to either correct outcome.
    if (pass && pass.transactions.length > 0) {
      assert.ok(
        s1.funds >= floor,
        `BUG-684 finding (headroom): a committed transaction must never leave funds (${s1.funds}) below the scaled floor (${floor})`,
      );
    } else {
      assert.ok(pass.skipped.length > 0, 'setup: something was recorded either way');
    }
  });

  test('RED-PROOF (source revert, private shadow copy): restoring the flat-threshold connector headroom breaches the scaled floor', () => {
    const { failed, output, crashed } = runMutantSelfReinvoke({
      targetRelPath: path.join('sim', 'engine.ts'),
      mutate: (original) => {
        const fixedLine =
          'const floorHeadroom = fundsBeforeConnect - consolidatorFundsFloor; // always >= 0: the pre-filter above already proved cur.funds - netCost >= consolidatorFundsFloor';
        assert.ok(original.includes(fixedLine), 'precondition: the fixed (scaled-floor) headroom grant line is present verbatim');
        // The named mutant: "the headroom mutant (flat threshold)" — grant
        // autoConnect headroom down to the bare INSOLVENCY_WARNING_THRESHOLD
        // again, exactly the pre-round-2 shape.
        const buggyLine = 'const floorHeadroom = fundsBeforeConnect - INSOLVENCY_WARNING_THRESHOLD;';
        return original.replace(fixedLine, buggyLine);
      },
      testFileAbsPath: fileURLToPath(import.meta.url),
      testNamePattern:
        'funds=55,000,000, outflow=56,500/tick \\(hand-set\\) \\+ structural upkeep: the pre-check clears, but the connector spend must never be allowed to breach the floor',
    });
    assert.ok(!crashed, `the re-invoked test must actually RUN against the mutant; output:\n${output}`);
    assert.ok(failed, 'the headroom-breach test must FAIL against the flat-threshold (reverted) connector grant');
    assert.match(output, /must never leave funds .* below the scaled floor/, `child output must report the SPECIFIC breach assertion failing; got:\n${output}`);
  });
});
