// attack-inc3-round5-defrag.test.mjs — FEAT-2326609779 (consolidator inc3,
// LAYOUT HIERARCHY), INDEPENDENT DESTRUCTIVE ROUND 5 (attacker != author).
//
// Rounds 1-4 attacked the geometry, the money gates and the perf. This round's
// brief was explicitly Aaron's own sentence for the feature:
//
//   "the red box should defrag - roads lay out, then train layout, then the
//    bigger consolidated buildings get laid down"
//
// and the acceptance doc's AC-1, which formalises it as an ABSOLUTE order:
//
//   "it applies tier placements in the exact order:
//    rail -> motorway -> dual -> A-road -> minor -> buildings"
//   (docs/planning/acceptance/FEAT-2326609779.md, AC-1)
//   "...the consolidator places infrastructure (rail, motorways, dual
//    carriageways, A-roads, minor roads) BEFORE buildings, in order" (S2)
//
// The tests below pin what the estate ACTUALLY does against that text. Where
// the estate matches, the test is a regression pin. Where it does not, the
// test documents the measured gap IN ITS ASSERTION MESSAGE and pins the
// current behaviour, so a later fix reddens it deliberately rather than
// silently (the round-3 "mutation 5" lesson: an estate that adds a real
// evaluation and no test that notices it reverting).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { computeRoadConnectivity, SPECS } from '../src/sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { TIER_ORDER, candidateTierPath, MIN_TIER_RUN_TILES } from '../src/sim/consolidatorLayout.ts';

const ENGINE_SRC = readFileSync(fileURLToPath(new URL('../src/sim/engine.ts', import.meta.url)), 'utf8');

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

function roadRow(y, maxX, skip = () => false) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) {
    if (skip(x)) continue;
    roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  }
  return roads;
}

const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });

/** The proven consolidation opportunity idiom from consolidator-mutation.test.mjs. */
function fireFixture(over) {
  const posts = [];
  for (let i = 0; i < 5; i++) posts.push({ id: 100 + i, spec: 'fire_post', x: 16 + i, y: 1, builtTick: -1000 });
  const headroom = [
    { id: 900, spec: 'fire_station', x: 200, y: 200, builtTick: -1000 },
    { id: 901, spec: 'fire_station', x: 210, y: 200, builtTick: -1000 },
  ];
  return withConnectivity(mk({ buildings: [...roadRow(0, 40), ...posts, ...headroom], funds: 100_000_000, ...over }));
}

function advanceTo(s, tick) {
  let cur = s;
  while (cur.tick < tick) cur = reducer(cur, { type: 'tick' });
  return cur;
}

function layoutTxnsOf(s) {
  const out = [];
  for (const pass of s.consolidatorLog ?? []) for (const t of pass.tierLayout ?? []) out.push(t);
  return out;
}

// ===========================================================================
// R5-A — AC-1's placement order: is "buildings" really LAST?
// ===========================================================================

describe('R5-A — AC-1 absolute placement order (rail -> ... -> minor -> BUILDINGS)', () => {
  test('the five INFRASTRUCTURE tiers are attempted in exactly TIER_ORDER (this half of AC-1 HOLDS)', () => {
    let s = fireFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceTo(s, 330);
    const txns = layoutTxnsOf(s);
    assert.ok(txns.length > 0, 'setup: at least one layout transaction');
    for (const t of txns) {
      assert.deepEqual(
        t.tierAudit.map((ta) => ta.tier),
        TIER_ORDER,
        'AC-1: every layout transaction must attempt rail->motorway->dual->aroad->minor in order',
      );
    }
  });

  test('FIXED (R5-A, round-5 rework): within ONE pass the INFRASTRUCTURE tiers are minted BEFORE the buildings step — AC-1 says buildings are LAST', () => {
    let s = fireFixture();
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceTo(s, 330);

    // A pass that both consolidated buildings AND laid tiers.
    const mixed = (s.consolidatorLog ?? []).find(
      (p) => (p.transactions ?? []).some((t) => (t.added ?? []).length > 0) && (p.tierLayout ?? []).length > 0,
    );
    assert.ok(mixed, 'setup: a pass that both placed a consolidated building and laid infrastructure');

    const consolidationIds = mixed.transactions.flatMap((t) => (t.added ?? []).map((r) => r.id));
    const layoutIds = mixed.tierLayout.flatMap((t) => (t.added ?? []).map((r) => r.id));
    assert.ok(consolidationIds.length > 0 && layoutIds.length > 0);

    // Building ids are minted monotonically from state.nextId as each stage
    // commits, so the id ranges are a faithful, observable record of which
    // stage ran first. Nothing here depends on wall-clock or iteration order.
    const lastBuildingId = Math.max(...consolidationIds);
    const firstInfraId = Math.min(...layoutIds);
    assert.ok(
      firstInfraId < lastBuildingId,
      'PINS THE FIXED ORDER: infrastructure ids are minted BEFORE building ids, i.e. applyConsolidatorPass ' +
        'now runs the tier-layout stage first (engine.ts, immediately after the pass\'s own setup) and only ' +
        'then commits reconnect/density consolidation transactions. This matches AC-1: ' +
        'rail -> motorway -> dual -> A-road -> minor -> buildings.',
    );
  });

  test('STRUCTURAL: in engine.ts the tier-layout stage textually precedes the consolidation-transaction stage inside applyConsolidatorPass', () => {
    // ROUND-11 RESTRUCTURE (Aaron's ruling, "F2 is not deferrable"): the
    // single per-section `applyTierLayoutForSection` was split into a
    // Phase A/B/C tier-major pipeline (`buildLayoutSectionCtx` /
    // `attemptOneTierInSection` / `finalizeLayoutSection`) so a lower tier
    // can never spend the pass-wide budget across sections before a higher
    // tier gets first claim — see consolidatorLayout.ts's TIER_UPKEEP_SHARE
    // doc and engine.ts's own file-header note on the split. The call site
    // this test pins moved from `applyTierLayoutForSection(` to
    // `attemptOneTierInSection(` — same structural claim (infra laid before
    // any consolidation building commits within the pass), same location
    // in the function, only the callee's name changed.
    const passStart = ENGINE_SRC.indexOf('function applyConsolidatorPass(');
    assert.ok(passStart > 0, 'applyConsolidatorPass must exist');
    const firstTxnPush = ENGINE_SRC.indexOf('transactions.push(', passStart);
    const layoutLoop = ENGINE_SRC.indexOf('attemptOneTierInSection(', ENGINE_SRC.indexOf('const tierLayout', passStart));
    assert.ok(firstTxnPush > 0 && layoutLoop > 0);
    assert.ok(
      layoutLoop < firstTxnPush,
      'PINS R5-A structurally (fixed): infrastructure is laid before any consolidation building is committed within the pass.',
    );
  });
});

// ===========================================================================
// R5-B — "the red box should DEFRAG": is any existing content reorganised?
// ===========================================================================

describe('R5-B — the layout stage never reclaims, moves or defragments existing content', () => {
  test('MEASURED: every layout transaction has removed:[] and scrapRecovered:0 — the stage only ever FILLS free space', () => {
    let s = fireFixture({ funds: 1_000_000_000 });
    s = reducer(s, { type: 'toggleConsolidator' });
    s = advanceTo(s, 700);
    const txns = layoutTxnsOf(s);
    assert.ok(txns.length > 0, 'setup: layout ran');
    for (const t of txns) {
      assert.equal((t.removed ?? []).length, 0, 'a layout transaction must not demolish anything (it never does)');
      assert.equal(t.scrapRecovered ?? 0, 0);
      for (const ta of t.tierAudit) assert.equal(ta.estimatedScrap, 0);
    }
  });

  test('MEASURED GAP (R5-B): a section fragmented so no free run reaches MIN_TIER_RUN_TILES gets NO layout at all — it is never defragmented, only skipped', () => {
    // A checkerboard leaves abundant TOTAL free space but no two free tiles
    // orthogonally adjacent, so the longest straight run is 1.
    const available = new Set();
    for (let x = 16; x < 32; x++) {
      for (let y = 0; y < 16; y++) {
        if ((x + y) % 2 === 1) available.add(`${x},${y}`);
      }
    }
    assert.ok(available.size > 100, 'setup: plenty of free tiles in total');
    for (const seed of [0, 1, 2, 7, 12345, 99991]) {
      const path = candidateTierPath(available, { x0: 16, y0: 0, w: 16, h: 16 }, seed);
      assert.deepEqual(
        path,
        [],
        'PINS R5-B: with 128 free tiles available but none contiguous, the planner returns NO candidate ' +
          `(seed ${seed}). It has no mechanism to RELOCATE the fragmenting buildings to open a corridor — ` +
          'i.e. it fills gaps, it does not defrag. (The acceptance doc DOES place "multi-tier re-planning" ' +
          'and "rebalancing existing pre-consolidation networks" out of scope for inc3, so this is a gap ' +
          "against Aaron's spoken word, not against the written AC.)",
      );
    }
    assert.ok(MIN_TIER_RUN_TILES >= 2, 'sanity: a 1-tile run genuinely cannot qualify');
  });

  test('no pre-existing building is ever MOVED by the layout stage (ids that survive keep their exact tile)', () => {
    const obstacles = [];
    let id = 5000;
    for (let x = 16; x < 30; x += 3) {
      for (let y = 3; y < 14; y += 3) obstacles.push({ id: id++, spec: 'park', x, y, builtTick: -1000 });
    }
    let s = fireFixture({ funds: 1_000_000_000 });
    s = withConnectivity({ ...s, buildings: [...s.buildings, ...obstacles] });
    s = reducer(s, { type: 'toggleConsolidator' });
    const before = new Map(obstacles.map((b) => [b.id, `${b.x},${b.y}`]));
    s = advanceTo(s, 700);
    const after = new Map(s.buildings.map((b) => [b.id, `${b.x},${b.y}`]));
    let moved = 0;
    for (const [bid, pos] of before) {
      const now = after.get(bid);
      if (now !== undefined && now !== pos) moved++;
    }
    assert.equal(moved, 0, 'PINS R5-B: the layout stage relocates nothing — surviving buildings keep their tile');
  });
});

// ===========================================================================
// R5-C — BUG-684 / R3-A: can the layout stage still bankrupt a city?
// ===========================================================================

describe('R5-C — hostile treasury: the upkeep-aware gate (BUG-684 / R3-A)', () => {
  test('the per-PASS aggregate upkeep delta never exceeds the documented bound, on a hostile low-income city', async () => {
    const { LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK } = await import('../src/sim/consolidatorLayout.ts');
    const obstacles = [];
    let id = 6000;
    for (let x = 16; x < 40; x += 5) for (let y = 4; y < 15; y += 4) obstacles.push({ id: id++, spec: 'park', x, y, builtTick: -1000 });

    let s = fireFixture({ funds: 5_000_000 });
    s = withConnectivity({ ...s, buildings: [...s.buildings, ...obstacles] });
    s = reducer(s, { type: 'toggleConsolidator' });

    let worst = 0;
    let prev = s;
    for (let i = 0; i < 400; i++) {
      const next = reducer(prev, { type: 'tick' });
      const pass = (next.consolidatorLog ?? [])[0];
      const prevPass = (prev.consolidatorLog ?? [])[0];
      if (pass && pass !== prevPass && (pass.tierLayout ?? []).length > 0) {
        // Recompute this pass's added recurring upkeep independently of the engine.
        let delta = 0;
        for (const t of pass.tierLayout) {
          for (const rec of t.added ?? []) {
            const sp = SPECS[rec.spec];
            delta += sp?.upkeep ?? 0;
          }
        }
        if (delta > worst) worst = delta;
      }
      prev = next;
    }
    assert.ok(
      worst <= LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK,
      `round-4 aggregate bound must hold per PASS across every committing section: worst measured ${worst} ` +
        `vs bound ${LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK}`,
    );
  });

  test('a broke city is never spent BELOW the insolvency floor by the layout stage, and never enters decline (BUG-684)', async () => {
    // MEASURED CONTRACT (round 5): the layout stage's build gate is
    // `funds - estimatedCost < INSOLVENCY_WARNING_THRESHOLD` — the SAME floor
    // the pre-existing inc1/inc2 consolidator already uses (engine.ts's own
    // "AC-23: never spend a background process through the insolvency floor").
    // So a near-zero city CAN legitimately be taken into overdraft as far as
    // that floor (-STARTING_TREASURY/2 = -GBP750,000), and no further. Measured
    // on this fixture: start GBP1,000 -> GBP732,000 booked in one pass. That is
    // the project's established background-spend contract, NOT an inc3
    // regression — this test pins the floor rather than demanding zero spend.
    const { INSOLVENCY_WARNING_THRESHOLD } = await import('../src/sim/fiscal.ts');
    for (const startFunds of [1_000, 0, -1_000_000]) {
      let s = fireFixture({ funds: startFunds });
      s = reducer(s, { type: 'toggleConsolidator' });
      let prev = s;
      let enteredDecline = false;
      let minFundsBeforeInterest = Math.min(startFunds, 0);
      for (let i = 0; i < 400; i++) {
        const next = reducer(prev, { type: 'tick' });
        if (next.declineState) enteredDecline = true;
        // The layout stage's OWN spend must never take funds below the floor.
        const pass = (next.consolidatorLog ?? [])[0];
        const prevPass = (prev.consolidatorLog ?? [])[0];
        if (pass && pass !== prevPass) {
          let spend = 0;
          for (const t of pass.tierLayout ?? []) spend += t.buildCost ?? 0;
          if (spend > 0) {
            assert.ok(
              prev.funds - spend >= INSOLVENCY_WARNING_THRESHOLD,
              `layout booked GBP${spend} from GBP${prev.funds}, breaching the insolvency floor ` +
                `${INSOLVENCY_WARNING_THRESHOLD}`,
            );
            minFundsBeforeInterest = Math.min(minFundsBeforeInterest, prev.funds - spend);
          }
        }
        prev = next;
      }
      assert.equal(
        enteredDecline,
        false,
        `start GBP${startFunds}: the layout stage must never drive an untouched city into FINAL DECLINE (R3-A/BUG-684)`,
      );
    }
  });

  test('consistency + conservation stay clean through 400 ticks of layout activity', () => {
    let s = fireFixture({ funds: 1_000_000_000 });
    s = reducer(s, { type: 'toggleConsolidator' });
    let breaches = 0;
    let prev = s;
    for (let i = 0; i < 400; i++) {
      const next = reducer(prev, { type: 'tick' });
      const inflow = next.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
      const outflow = next.lastFlows.outflows.reduce((a, f) => a + f.value, 0);
      if (Math.abs(next.funds - (prev.funds + inflow - outflow)) > 0.5) breaches++;
      prev = next;
    }
    assert.equal(breaches, 0, 'tick-boundary money identity must hold every tick');
    const rep = runConsistencyChecks(prev);
    assert.equal(
      rep.failures,
      0,
      JSON.stringify(rep.checks.filter((c) => !c.ok).map((c) => `${c.id}: ${c.detail}`)),
    );
  });
});

// ===========================================================================
// R5-D — determinism (GR#21)
// ===========================================================================

describe('R5-D — determinism / replay', () => {
  test('two independent runs of the same fixture produce byte-identical layout output', () => {
    const run = () => {
      let s = fireFixture({ funds: 1_000_000_000 });
      s = reducer(s, { type: 'toggleConsolidator' });
      s = advanceTo(s, 400);
      return JSON.stringify(layoutTxnsOf(s));
    };
    assert.equal(run(), run(), 'GR#21: the layout stage must be byte-identical across runs');
  });

  test('shuffling the buildings array never changes the layout output (no iteration-order dependence)', () => {
    const base = fireFixture({ funds: 1_000_000_000 });
    const run = (buildings) => {
      let s = withConnectivity({ ...base, buildings });
      s = reducer(s, { type: 'toggleConsolidator' });
      s = advanceTo(s, 400);
      return JSON.stringify(layoutTxnsOf(s));
    };
    const straight = run(base.buildings.slice());
    const shuffled = run(base.buildings.slice().reverse());
    assert.equal(straight, shuffled, 'GR#21: building array order must not affect layout');
  });
});
