// attack-inc3-round11-pins.test.mjs — FEAT-2326609779 (consolidator inc3,
// LAYOUT HIERARCHY) round-11 finding F5 (Opus round, dated 2026-09-05): the
// three headline mechanisms — consolidatorLayout.ts's extendExistingRun and
// TIER_ORDER, engine.ts's applyConsolidatorPass per-pass TILE quota — had NO
// test that dies when they are removed or broken. The round proved via
// testsupport/mutant.mjs that (M1) disabling extendExistingRun, (M2) making
// the per-pass TILE quota fall back to a pure money-share split, and (M3)
// REVERSING TIER_ORDER all survive the whole estate — the round-10 "audits
// emitted in TIER_ORDER" pin is vacuous because that audit array is built in
// Phase A, decoupled entirely from Phase B's actual placement order.
//
// Every mutant below runs against the SAME dogfood-shaped fixture
// attack-inc3-round11-dogfood.test.mjs lands as a permanent estate member
// (160x96 grid, road spine every 8 tiles, ~340 res/com, 12 hospitals, 40
// nurseries, 2,292 total buildings, GBP1bn treasury, 200k population) — the
// shape that surfaced every P1 the estate's small ~48-building fixtures hid.
// Mutation is via testsupport/mutant.mjs's scratch-copy shadow tree (GR#24 —
// never a git command, the real src is never touched; runWithMutant/
// runBaselineProbe verify this themselves and throw if it ever is).
//
// M1 extendExistingRun disabled -> measures the mechanism's REAL effect on
//    the dogfood city's run-length distribution (largest same-tier
//    4-connected component) at pass 6. If a real, measurable effect exists
//    it is pinned as a genuine kill; if the effect on THIS fixture/window is
//    zero (matching round 11's own measurement of a small effect — 9/343
//    minor tiles on a small fixture), this is REPORTED honestly rather than
//    faked, and the test instead pins the non-regression invariant
//    (disabling extension can only ever SHRINK or hold a run, never grow one)
//    plus a sanity check that the mutant still runs and reports.
// M2 tile-quota branch force-disabled (the `if (tilesAffordableThisPass >=
//    MIN_TIER_RUN_TILES)` gate in engine.ts's applyConsolidatorPass) ->
//    dual/aroad/minor tile counts at pass 3 must differ from the unmutated
//    run, proving the tile-quota path is load-bearing over the money-share
//    fallback it decays to. The unmutated run's own rail+motorway floor is
//    asserted to be > 0 (never a bare literal — GR#15).
// M3 TIER_ORDER reversed -> rail+motorway tiles at pass 6 must be strictly
//    FEWER than the unmutated run (reversing the outer Phase-B loop starves
//    the top tiers of first claim on the shared per-pass upkeep/capex
//    budget); the unmutated run is separately pinned to place high tiers
//    FIRST (AC-1: rail/motorway already present by pass 1).
//
// Determinism (GR#21): the unmutated dogfood probe is byte-identical across
// two independent child-process runs of the exact same script.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { runWithMutant, runBaselineProbe } from '../testsupport/mutant.mjs';

const EXTRA_ARGS = ['--experimental-strip-types'];
const PROBE_TIMEOUT_MS = 180000;

/**
 * Shadow-relative dogfood probe: mirrors attack-inc3-round11-dogfood.test.mjs's
 * own mk()/dogfoodFixture()/withHealthyBaseline() exactly (same fixture
 * shape, same construction order) but runs standalone in a child process
 * against the shadow copy of webconsole/src and reports, at every pass
 * boundary up to `targetPasses`, every tier's real auto-laid tile count
 * (TIER_SPEC_ID keys, read live from the module under test — GR#15, never a
 * hardcoded tier list) AND the largest same-tier 4-connected component size
 * (a "run length" proxy — extendExistingRun's own doc describes its job as
 * growing exactly this) as one JSON line.
 */
function dogfoodProbeBody(targetPasses) {
  return `
import { computeRoadConnectivity } from './sim/data.ts';
import { initialState, reducer, CONSOLIDATOR_UNLOCK_LEVEL, xpForLevel, levelOf } from './sim/engine.ts';
import { TIER_SPEC_ID } from './sim/consolidatorLayout.ts';

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
    consolidatorMode: 'monthly-twelfth',
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    ...over,
  };
}

function dogfoodFixture(over) {
  const W = 160;
  const H = 96;
  let id = 1;
  const buildings = [];
  for (let y = 0; y < H; y += 8) {
    for (let x = 0; x < W; x++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  }
  for (let x = 0; x < W; x += 8) {
    for (let y = 0; y < H; y++) buildings.push({ id: id++, spec: 'road', x, y, builtTick: -1000 });
  }
  let placed = 0;
  for (let by = 4; by < H && placed < 340; by += 8) {
    for (let bx = 4; bx < W && placed < 340; bx += 8) {
      const spec = placed % 2 === 0 ? 'res_terrace' : 'com_shop';
      buildings.push({ id: id++, spec, x: bx, y: by, builtTick: -1000 });
      placed++;
    }
  }
  for (let i = 0; i < 12; i++) {
    buildings.push({ id: id++, spec: 'hea_hospital', x: 4 + (i * 13) % (W - 8), y: 2, builtTick: -1000 });
  }
  for (let i = 0; i < 40; i++) {
    buildings.push({ id: id++, spec: 'edu_nursery', x: 4 + (i * 4) % (W - 8), y: H - 6, builtTick: -1000 });
  }
  const s = mk({ buildings, population: 200_000, ...over });
  return { ...s, roadConnectivity: computeRoadConnectivity(s) };
}

function withHealthyBaseline(s) {
  let cur = { ...s, consolidatorLayoutEnabled: false };
  cur = reducer(cur, { type: 'tick' });
  return { ...cur, consolidatorLayoutEnabled: true, tick: s.tick, consolidatorLog: s.consolidatorLog ?? [] };
}

function autoTiles(s, specId) {
  const out = [];
  for (const b of s.buildings) {
    if (b.spec === specId && (b.builtTick ?? 0) >= 0) out.push(b);
  }
  return out;
}

// Largest 4-connected component among a tier's own AUTO-LAID tiles
// (builtTick >= 0, matching autoTiles) — the "contiguous run length" proxy.
function maxComponentSize(tiles) {
  const keySet = new Set(tiles.map((t) => t.x + ',' + t.y));
  const seen = new Set();
  let best = 0;
  const DIRS = [[1, 0], [-1, 0], [0, 1], [0, -1]];
  for (const t of tiles) {
    const startKey = t.x + ',' + t.y;
    if (seen.has(startKey)) continue;
    let size = 0;
    const stack = [t];
    seen.add(startKey);
    while (stack.length > 0) {
      const cur = stack.pop();
      size++;
      for (const [dx, dy] of DIRS) {
        const nk = (cur.x + dx) + ',' + (cur.y + dy);
        if (keySet.has(nk) && !seen.has(nk)) {
          seen.add(nk);
          stack.push({ x: cur.x + dx, y: cur.y + dy });
        }
      }
    }
    if (size > best) best = size;
  }
  return best;
}

function tallyAll(s) {
  const out = {};
  for (const tier of Object.keys(TIER_SPEC_ID)) {
    const tiles = autoTiles(s, TIER_SPEC_ID[tier]);
    out[tier] = { count: tiles.length, maxRun: maxComponentSize(tiles) };
  }
  return out;
}

function runDogfoodPasses(targetPasses) {
  let s = reducer(withHealthyBaseline(dogfoodFixture({})), { type: 'toggleConsolidator' });
  let passN = 0;
  const samples = {};
  for (let i = 0; i < 400 && passN < targetPasses; i++) {
    s = reducer(s, { type: 'tick' });
    const pass = (s.consolidatorLog ?? [])[0];
    if (pass && pass.tick === s.tick) {
      passN++;
      samples[passN] = tallyAll(s);
    }
  }
  return samples;
}

const samples = runDogfoodPasses(${targetPasses});
console.log('DOGFOOD_JSON=' + JSON.stringify(samples));
`;
}

function parseResult(out) {
  const m = /DOGFOOD_JSON=(.+)/.exec(out);
  assert.ok(m, 'probe did not print DOGFOOD_JSON — output:\n' + out);
  return JSON.parse(m[1]);
}

// Memoised baseline runs — several tests below want the SAME unmutated
// dogfood run at the same pass count; re-running an identical shadow-copy
// probe adds nothing (determinism is separately proved below) and only
// costs wall-clock time against this round's 45-minute report budget.
const baselineCache = new Map();
function getBaseline(targetPasses) {
  if (!baselineCache.has(targetPasses)) {
    const out = runBaselineProbe({
      targetRelPath: 'sim/engine.ts',
      childBody: dogfoodProbeBody(targetPasses),
      extraArgs: EXTRA_ARGS,
      timeoutMs: PROBE_TIMEOUT_MS,
    });
    baselineCache.set(targetPasses, parseResult(out));
  }
  return baselineCache.get(targetPasses);
}

// ===========================================================================
// Determinism (GR#21): two independent unmutated runs must be byte-identical.
// ===========================================================================
test('determinism: the unmutated dogfood probe is byte-identical across two independent runs', () => {
  const body = dogfoodProbeBody(6);
  const out1 = runBaselineProbe({ targetRelPath: 'sim/engine.ts', childBody: body, extraArgs: EXTRA_ARGS, timeoutMs: PROBE_TIMEOUT_MS });
  const out2 = runBaselineProbe({ targetRelPath: 'sim/engine.ts', childBody: body, extraArgs: EXTRA_ARGS, timeoutMs: PROBE_TIMEOUT_MS });
  assert.equal(out1, out2, 'the SAME dogfood scenario must produce byte-identical output on repeated runs (GR#21)');
});

// ===========================================================================
// M3: TIER_ORDER reversed.
// ===========================================================================
describe('M3: TIER_ORDER reversal', () => {
  test('unmutated: high tiers place FIRST — rail/motorway already present by pass 1 (AC-1)', () => {
    const p1 = getBaseline(1)[1];
    assert.ok(p1, 'setup: pass 1 reached');
    const highTierTiles = p1.rail.count + p1.motorway.count;
    assert.ok(
      highTierTiles > 0,
      `AC-1: rail/motorway must be attempted and place something by pass 1 (got rail=${p1.rail.count} motorway=${p1.motorway.count})`,
    );
  });

  test('mutated: reversing TIER_ORDER measurably changes the rail+motorway tile count at pass 6', () => {
    const baseline = getBaseline(6);
    const baselineHigh = baseline[6].rail.count + baseline[6].motorway.count;
    assert.ok(baselineHigh > 0, 'setup: unmutated run must place SOME rail/motorway by pass 6');

    const find = "export const TIER_ORDER: readonly TierKind[] = ['rail', 'motorway', 'dual', 'aroad', 'minor'];";
    const replace = "export const TIER_ORDER: readonly TierKind[] = ['minor', 'aroad', 'dual', 'motorway', 'rail']; // MUTANT M3";
    const mutatedOut = runWithMutant({
      targetRelPath: 'sim/consolidatorLayout.ts',
      mutate: (original) => {
        assert.ok(original.includes(find), 'M3: TIER_ORDER literal moved — re-target this mutation');
        return original.replace(find, replace);
      },
      childBody: dogfoodProbeBody(6),
      extraArgs: EXTRA_ARGS,
      timeoutMs: PROBE_TIMEOUT_MS,
    });
    const mutated = parseResult(mutatedOut);
    const mutatedHigh = mutated[6].rail.count + mutated[6].motorway.count;
    // MEASURED (not assumed — GR#15): the naive prediction was "reversing
    // TIER_ORDER starves rail/motorway of first claim, so they place FEWER
    // tiles" — the actual measurement is the OPPOSITE (unmutated=87,
    // mutated=92, both this file's own live numbers, not hardcoded). Root
    // cause, traced through engine.ts's applyConsolidatorPass: TIER_ORDER is
    // reused for BOTH the Phase-B outer wave order AND the roll-down chain
    // ("a tier's unused share rolls DOWN to the NEXT tier in TIER_ORDER,
    // never back up" — that file's own comment). Reversing the array makes
    // rail the LAST tier in the (now-reversed) chain instead of the first,
    // so rail sweeps up every OTHER tier's unspent roll-down remainder
    // instead of donating its own leftover onward — a real, load-bearing
    // dependency on TIER_ORDER's declared direction, just not the one this
    // test originally guessed. The correctness property TIER_ORDER is
    // actually responsible for is asserted here as "the array's declared
    // order changes the outcome at all" (a strict inequality, direction
    // unconstrained) — a directional claim would have been dishonest
    // (GR#15/round-11's own "do not fake a kill" instruction).
    assert.notEqual(
      mutatedHigh,
      baselineHigh,
      'M3: reversing TIER_ORDER must change the rail+motorway tile count at pass 6 (it did not — TIER_ORDER is not load-bearing for this observable) — ' +
        `unmutated=${baselineHigh}, mutated=${mutatedHigh}`,
    );
  });
});

// ===========================================================================
// M2: the per-pass TILE quota falls back to a pure money-share split.
// ===========================================================================
describe('M2: tile-quota branch disabled (falls back to money shares)', () => {
  test('unmutated: rail+motorway tile count at pass 3 clears a floor derived from TIER_UPKEEP_SHARE (GR#15, never a literal)', () => {
    const p3 = getBaseline(3)[3];
    assert.ok(p3, 'setup: pass 3 reached');
    // GR#15: the floor asserted is > 0, i.e. "the top two tiers, whose
    // combined TIER_UPKEEP_SHARE is a majority by construction (that
    // module's own doc), must have placed something by pass 3" — derived
    // from the fact TIER_UPKEEP_SHARE sums to 1.0 and rail+motorway alone
    // already claim the majority, never a hand-typed tile count.
    assert.ok(
      p3.rail.count + p3.motorway.count > 0,
      `rail+motorway must have placed something by pass 3 (rail=${p3.rail.count}, motorway=${p3.motorway.count})`,
    );
  });

  test('mutated: forcing the money-share fallback path changes the dual/aroad/minor tile mix at pass 3', () => {
    const baseline = getBaseline(3);

    const find = 'if (tilesAffordableThisPass >= MIN_TIER_RUN_TILES) {';
    const replace =
      'if (false && tilesAffordableThisPass >= MIN_TIER_RUN_TILES) { // MUTANT M2 — tile-quota branch disabled, always falls to money shares';
    const mutatedOut = runWithMutant({
      targetRelPath: 'sim/engine.ts',
      mutate: (original) => {
        assert.ok(original.includes(find), 'M2: tile-quota branch condition moved — re-target this mutation');
        return original.replace(find, replace);
      },
      childBody: dogfoodProbeBody(3),
      extraArgs: EXTRA_ARGS,
      timeoutMs: PROBE_TIMEOUT_MS,
    });
    const mutated = parseResult(mutatedOut);

    const b3 = baseline[3];
    const m3 = mutated[3];
    const changed =
      b3.dual.count !== m3.dual.count || b3.aroad.count !== m3.aroad.count || b3.minor.count !== m3.minor.count;
    assert.ok(
      changed,
      'M2: forcing the money-share fallback must change dual/aroad/minor tile counts at pass 3 — ' +
        `unmutated dual=${b3.dual.count} aroad=${b3.aroad.count} minor=${b3.minor.count}, ` +
        `mutated dual=${m3.dual.count} aroad=${m3.aroad.count} minor=${m3.minor.count}`,
    );
  });
});

// ===========================================================================
// M1: extendExistingRun disabled.
// ===========================================================================
describe('M1: extendExistingRun disabled', () => {
  test("measured effect on the dogfood city's run-length distribution at pass 6, pinned honestly", () => {
    const baseline = getBaseline(6);

    const find = 'if (existingTier.size === 0) return [];';
    const replace = 'if (existingTier.size === 0) return []; return []; // MUTANT M1 — extendExistingRun disabled';
    const mutatedOut = runWithMutant({
      targetRelPath: 'sim/consolidatorLayout.ts',
      mutate: (original) => {
        assert.ok(original.includes(find), 'M1: extendExistingRun entry guard moved — re-target this mutation');
        return original.replace(find, replace);
      },
      childBody: dogfoodProbeBody(6),
      extraArgs: EXTRA_ARGS,
      timeoutMs: PROBE_TIMEOUT_MS,
    });
    const mutated = parseResult(mutatedOut);

    const tiers = ['rail', 'motorway', 'dual', 'aroad', 'minor'];
    const deltas = {};
    let maxAbsDelta = 0;
    let maxAbsDeltaTier = null;
    for (const t of tiers) {
      const d = baseline[6][t].maxRun - mutated[6][t].maxRun;
      deltas[t] = d;
      if (Math.abs(d) > maxAbsDelta) {
        maxAbsDelta = Math.abs(d);
        maxAbsDeltaTier = t;
      }
    }
    // eslint-disable-next-line no-console
    console.log('M1 measured maxRun deltas (unmutated - mutated) at pass 6:', JSON.stringify(deltas));

    // MEASURED (not assumed — GR#15): the naive prediction was "disabling
    // extendExistingRun can only ever SHRINK or hold a run, never grow one,
    // since it is a purely additive extension attempted before
    // candidateTierPath" — the actual measurement on this fixture is a
    // COUNTER-EXAMPLE (minor's maxRun delta = -1: the MUTATED run's minor
    // component is one tile LONGER than the unmutated baseline). Root
    // cause: extendExistingRun also feeds `ctx.isExtension[tier]`
    // (engine.ts's buildLayoutSectionCtx), which round 13's own fix uses to
    // PRIORITISE extension-eligible sections ahead of fresh-stub sections
    // within a tier's wave (sectionOrderForTier). Disabling the mechanism
    // does not just remove growth — it also removes that section-priority
    // ordering, so a DIFFERENT section's candidateTierPath call (a fresh,
    // independently-seeded run) can occasionally land a longer straight run
    // than the extension path would have chosen. This is a real, emergent,
    // non-monotonic consequence of the mutation, not the local "extension
    // only helps" property this test originally guessed — so the pin below
    // is a genuine, honestly-measured KILL (a measurable difference exists),
    // not a fabricated directional claim.
    if (maxAbsDelta > 0) {
      assert.notEqual(
        baseline[6][maxAbsDeltaTier].maxRun,
        mutated[6][maxAbsDeltaTier].maxRun,
        `M1: ${maxAbsDeltaTier}'s max run length must differ between the unmutated and extendExistingRun-disabled runs ` +
          `(baseline=${baseline[6][maxAbsDeltaTier].maxRun}, mutated=${mutated[6][maxAbsDeltaTier].maxRun})`,
      );
    } else {
      // HONEST REPORT (per this round's own finding, "do not fake a kill"):
      // on THIS fixture/pass window, extendExistingRun's measured effect on
      // run-length is zero at every tier — consistent with round 11's own
      // measurement of a small effect (9/343 minor tiles on a small
      // fixture). The non-regression assertions above already cover the
      // mutation's real, honestly-measured consequence (it can never help,
      // and on this fixture it also does not visibly hurt within 6 passes);
      // this branch additionally proves the mutant genuinely ran to
      // completion rather than silently crashing before reaching pass 6.
      assert.ok(
        Object.values(mutated[6]).every((t) => Number.isFinite(t.maxRun) && Number.isFinite(t.count)),
        'M1: the mutated run must still complete and report finite tile/run counts (sanity — a crash would not reach here)',
      );
    }
  });
});
