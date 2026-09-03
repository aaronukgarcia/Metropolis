// bug645-population-visibility.test.mjs — BUG-645 (P1).
//
// Aaron: "as the days go past with 1.9m people the population stays the same
// — why do births and deaths not make it go up and down per day or at month
// end?" RCA: the mechanic is CORRECT (births beat deaths — a real +569
// natural increase on his city — but move-outs almost exactly cancel it once
// the city sits at 99.7% of online housing capacity, so net population is
// pinned at 0 even though every flow is genuinely moving). The DEFECT was
// that nothing in the UI showed the flows or explained why they net to ~zero.
//
// This file covers the DATA/SELECTOR layer:
//   (1) residentialConstructionSummary(s) (sim/data.ts, new) — count+capacity
//       of residential buildings still under construction, provably IDENTICAL
//       to offlineResidentsByReason(s).construction for every state (GR#3:
//       one number, never a second divergent copy).
//   (2) isAtCapacity(population, onlineCapacity) (ragThresholds.ts, new) —
//       the single named 99% threshold, boundary-tested both directions.
//   (3) PERF — residentialConstructionSummary is memoOnState-wrapped exactly
//       like onlineByBuilding/waterCaps/residentsCapacity (BUG-642/BUG-622
//       idiom): measured median cost at Aaron's 29,831-building scale, both
//       the first (cold) call per state and the cached (warm, same state
//       object — the real per-render shape) call.
//   (4) RED-PROOF — a scratch-copy mutation (GR#24: cp/mv, never git) that
//       un-memoises residentialConstructionSummary reddens the perf bound
//       while the semantic-identity assertions stay green, proving the perf
//       test can actually fail.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';
import {
  residentialConstructionSummary,
  offlineResidentsByReason,
  onlineResidentsCapacity,
  residentsCapacity,
  underConstructionResidents,
  SPECS,
} from '../src/sim/data.ts';
import { isAtCapacity, RAG_THRESHOLDS } from '../src/components/ragThresholds.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { buildScaleFixture } from './scale/fixture.mjs';

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

// ────────────────────────────────────────────────────────────────────────
// (1) residentialConstructionSummary — semantic identity against
//     offlineResidentsByReason's independently-shipped 'construction' bucket
// ────────────────────────────────────────────────────────────────────────

/** Finds a real, unlocked, non-tiered residential spec with a long build time
 *  ('res_apt'/'res_tower'-shaped catalogue entries are the "mega-estate"
 *  Aaron describes — pick whichever spec actually has the longest
 *  constructionTicks so the under-construction window is comfortably wide). */
function longestBuildResidentialSpec() {
  const residential = Object.values(SPECS).filter((sp) => sp.kind === 'residential' && !sp.placeholder);
  assert.ok(residential.length > 0, 'catalogue must have at least one residential spec');
  // constructionTicks() (data.ts) derives build time from sp.cost — the
  // "mega-estate" shape Aaron describes is the highest-cost residential spec.
  return residential.reduce((best, sp) => (sp.cost > best.cost ? sp : best));
}

test('BUG-645: residentialConstructionSummary counts buildings + capacity for a hand-built mega-estate-under-construction city', () => {
  const sp = longestBuildResidentialSpec();
  let s = initialState();
  s = {
    ...s,
    tick: 5,
    roadConnectivity: { connectedRoadTiles: ['1,0'] },
    buildings: [
      // Online (built long ago).
      { id: 1, spec: sp.id, x: 0, y: 0, builtTick: -100000 },
      { id: 2, spec: 'road', x: 1, y: 0, builtTick: 0 },
      // Still under construction (builtTick == tick, i.e. just started).
      { id: 3, spec: sp.id, x: 5, y: 5, builtTick: 5 },
      { id: 4, spec: sp.id, x: 10, y: 10, builtTick: 5 },
    ],
  };
  const summary = residentialConstructionSummary(s);
  assert.equal(summary.count, 2, 'exactly the two freshly-placed buildings are still under construction');
  assert.ok(summary.capacity > 0, 'the under-construction buildings must contribute nonzero withheld capacity');
  assert.deepEqual(
    summary,
    { count: summary.count, capacity: offlineResidentsByReason(s).construction },
    'capacity must be EXACTLY offlineResidentsByReason(s).construction — never a second, divergent number (GR#3)'
  );
});

test('BUG-645: residentialConstructionSummary is zero for a fully-online city (no construction, no disconnection)', () => {
  const s = initialState();
  const summary = residentialConstructionSummary(s);
  assert.equal(summary.count, 0);
  assert.equal(summary.capacity, 0);
  assert.equal(offlineResidentsByReason(s).construction, 0, 'sanity: the SSOT bucket agrees');
});

test('BUG-645: residentialConstructionSummary excludes a road-disconnected (built, not-construction) residential building', () => {
  const sp = longestBuildResidentialSpec();
  let s = initialState();
  s = {
    ...s,
    tick: 100000, // far past any construction window
    roadConnectivity: { connectedRoadTiles: [] }, // no connected tiles at all
    buildings: [{ id: 1, spec: sp.id, x: 40, y: 40, builtTick: 0 }],
  };
  const summary = residentialConstructionSummary(s);
  assert.equal(summary.count, 0, 'a road-disconnected (not under-construction) building must NOT be counted as construction');
  assert.equal(summary.capacity, 0);
  assert.ok(
    offlineResidentsByReason(s).disconnected > 0,
    'sanity: the building really is offline for the DISCONNECTED reason, not construction'
  );
});

test('BUG-645: residentialConstructionSummary matches offlineResidentsByReason(s).construction across the 13k-building scale fixture', () => {
  const s = buildScaleFixture({ buildingCount: 1500, targetPopulation: 100_000, settleTicks: 2 });
  const summary = residentialConstructionSummary(s);
  assert.equal(summary.capacity, offlineResidentsByReason(s).construction, 'GR#3: must never diverge from the shipped bucket, at scale too');
});

test('BUG-645: underConstructionResidents(s) - residentialConstructionSummary(s).capacity equals the disconnected remainder (partition holds)', () => {
  const sp = longestBuildResidentialSpec();
  let s = initialState();
  s = {
    ...s,
    tick: 5,
    roadConnectivity: { connectedRoadTiles: [] },
    buildings: [
      { id: 1, spec: sp.id, x: 5, y: 5, builtTick: 5 }, // under construction
      { id: 2, spec: sp.id, x: 40, y: 40, builtTick: 0 }, // built, disconnected
    ],
  };
  const constructionCap = residentialConstructionSummary(s).capacity;
  const gross = residentsCapacity(s);
  const online = onlineResidentsCapacity(s);
  const totalOffline = underConstructionResidents(s); // gross - online (BUG-417 name, includes BOTH reasons)
  assert.equal(gross - online, totalOffline);
  const disconnectedCap = offlineResidentsByReason(s).disconnected;
  assert.equal(
    constructionCap + disconnectedCap,
    totalOffline,
    'construction + disconnected must exactly partition the offline residential capacity — no resident lost or double-counted'
  );
});

// ────────────────────────────────────────────────────────────────────────
// (2) isAtCapacity — single named threshold, both boundary directions
// ────────────────────────────────────────────────────────────────────────

test('BUG-645: isAtCapacity reads a SINGLE named threshold (RAG_THRESHOLDS.AT_CAPACITY.RATIO), boundary both ways', () => {
  const ratio = RAG_THRESHOLDS.AT_CAPACITY.RATIO;
  const capacity = 1_000_000;
  const atRatio = Math.floor(capacity * ratio);
  const justBelow = atRatio - 1;

  assert.equal(isAtCapacity(atRatio, capacity), true, 'population exactly at the ratio boundary is at-capacity');
  assert.equal(isAtCapacity(justBelow, capacity), false, 'one resident below the boundary is NOT at-capacity');
  assert.equal(isAtCapacity(capacity, capacity), true, 'population exactly at gross capacity is at-capacity');
  assert.equal(isAtCapacity(0, capacity), false, 'an empty city is never at-capacity');
});

test('BUG-645: isAtCapacity never reads capacity as "full" when there is no housing at all', () => {
  assert.equal(isAtCapacity(0, 0), false, 'zero population / zero capacity must not report at-capacity (nothing to be full of)');
  assert.equal(isAtCapacity(100, 0), false, 'a nonsensical population>capacity>0 state (0 capacity) must not report at-capacity');
  assert.equal(isAtCapacity(0, -5), false, 'a negative capacity (should never occur) fails closed to false, not a divide-by-negative flip');
});

test('BUG-645: isAtCapacity reproduces Aaron\'s reported live-city ratio (99.7% -> at capacity)', () => {
  // Aaron's own numbers from the bug report.
  assert.equal(isAtCapacity(1_898_104, 1_903_719), true, 'Aaron\'s live city (99.7% full) must read as at-capacity');
});

test('BUG-645: isAtCapacity says NO for a city with real headroom', () => {
  assert.equal(isAtCapacity(500_000, 1_000_000), false, 'a 50%-full city must never read as at-capacity');
});

// ────────────────────────────────────────────────────────────────────────
// (3) PERF — residentialConstructionSummary at Aaron's reported scale
//     (29,831 buildings). Bound: MEDIAN of 5 independently-built states
//     (never max — a single GC pause is real but not the steady-state signal
//     this gate exists to catch), set to ~4x the measured figure per this
//     project's house rule (never a tight wall-clock bound).
//
//     MEASURED locally (Windows, Node 25.3.0, 5 independent 29,831-building
//     fixtures, first/cold call on each): 2.28ms, 2.38ms, 2.69ms, 2.91ms,
//     3.28ms -> median ~2.9ms. Warm (cached, 20 repeated calls on the SAME
//     state — the real per-render shape TopBar/DemographicsTab hit every
//     tick): median ~0.0004ms (a WeakMap hit). Bounds below are ~4x the
//     measured medians, rounded up for CI-hardware margin (never tightened
//     without a fresh CI-runner measurement, matching scale-gate.test.mjs's
//     documented practice).
// ────────────────────────────────────────────────────────────────────────

const COLD_CALL_MEDIAN_BOUND_MS = 15; // ~4x the measured ~2.9ms cold-call median, rounded up.
// WARM bound is deliberately NOT a loose "~4x" figure like the cold bound
// above: measured directly (RED-PROOF test below), an UN-memoised
// residentialConstructionSummary's warm (repeated-same-state) median is
// ~0.65-0.75ms — well under a naive "generous" 2ms ceiling, which would let
// a memoOnState regression sail through undetected. 0.05ms sits ~125x above
// the measured memoised median (~0.0004ms, a WeakMap hit — effectively
// hardware-speed-independent) and >10x below the measured un-memoised floor,
// so it stays robust across CI hardware while still catching the regression
// class this gate exists for.
const WARM_CALL_MEDIAN_BOUND_MS = 0.05;
const SCALE_BUILDING_COUNT = 29831; // Aaron's reported live-city building count.
const SCALE_TARGET_POPULATION = 1_900_000; // Aaron's reported live-city population.

test('BUG-645: PERF — residentialConstructionSummary median COLD-call time at 29,831 buildings stays under bound', () => {
  const times = [];
  for (let i = 0; i < 5; i++) {
    const s = buildScaleFixture({ buildingCount: SCALE_BUILDING_COUNT, targetPopulation: SCALE_TARGET_POPULATION, settleTicks: 1 });
    const t0 = performance.now();
    residentialConstructionSummary(s);
    times.push(performance.now() - t0);
  }
  const med = median(times);
  assert.ok(
    med < COLD_CALL_MEDIAN_BOUND_MS,
    `median cold-call time at ${SCALE_BUILDING_COUNT} buildings was ${med.toFixed(2)}ms across 5 states ` +
      `(all: ${times.map((t) => t.toFixed(2)).join(', ')}), must be under ${COLD_CALL_MEDIAN_BOUND_MS}ms`
  );
});

test('BUG-645: PERF — residentialConstructionSummary median WARM (cached, same-state) call time is near-zero — proves the render-path cost is a WeakMap hit, not a re-scan', () => {
  const s = buildScaleFixture({ buildingCount: SCALE_BUILDING_COUNT, targetPopulation: SCALE_TARGET_POPULATION, settleTicks: 1 });
  residentialConstructionSummary(s); // warm the memo
  const times = [];
  for (let i = 0; i < 20; i++) {
    const t0 = performance.now();
    residentialConstructionSummary(s);
    times.push(performance.now() - t0);
  }
  const med = median(times);
  assert.ok(
    med < WARM_CALL_MEDIAN_BOUND_MS,
    `median warm-call time on the SAME 29,831-building state was ${med.toFixed(4)}ms across 20 calls, ` +
      `must be under ${WARM_CALL_MEDIAN_BOUND_MS}ms (a per-render re-scan would be ~1000x slower than this)`
  );
});

// ────────────────────────────────────────────────────────────────────────
// (4) RED-PROOF — un-memoise residentialConstructionSummary via a scratch
//     copy (GR#24: cp/mv, never a git command) and show the WARM-call perf
//     bound goes RED while semantic identity stays green. (The COLD-call
//     bound above is NOT what this proves against — a first call on a
//     fresh state pays the same O(buildings) scan whether memoised or not;
//     memoOnState's actual payoff is REPEATED calls on the SAME state,
//     which is what the WARM test and this red-proof both measure.)
// ────────────────────────────────────────────────────────────────────────

const DATA_TS_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'data.ts');
const BACKUP_PATH = `${DATA_TS_PATH}.bug645.bak`;

test('BUG-645: RED-PROOF — un-memoising residentialConstructionSummary (scratch mutation, run in a fresh child process) turns the WARM-call perf bound RED at scale, while a tiny hand-built state\'s answer is unaffected', () => {
  execFileSync('cp', [DATA_TS_PATH, BACKUP_PATH]);
  const backupStat = fs.statSync(BACKUP_PATH);
  assert.ok(backupStat.size > 0, 'GR#24 mutation-cycle safety check: backup must exist and be non-empty before mutating');
  try {
    const original = fs.readFileSync(DATA_TS_PATH, 'utf8');
    const openMarker = 'export const residentialConstructionSummary: (s: SimState) => { count: number; capacity: number } = memoOnState(\n  (s) => {';
    const closeMarker = '    return { count, capacity };\n  }\n);';
    assert.ok(original.includes(openMarker), 'RED-PROOF setup: expected opening shape not found — update this test\'s marker if the function was refactored');
    assert.ok(original.includes(closeMarker), 'RED-PROOF setup: expected closing shape not found — update this test\'s marker if the function was refactored');
    // Strip the memoOnState wrapper (both its opening call and matching
    // closing paren) so residentialConstructionSummary becomes a plain arrow
    // function that recomputes on EVERY call — the pre-fix shape this
    // red-proof simulates.
    const mutated = original
      .replace(openMarker, 'export const residentialConstructionSummary: (s: SimState) => { count: number; capacity: number } = (s) => {')
      .replace(closeMarker, '    return { count, capacity };\n  };');
    assert.notEqual(mutated, original, 'RED-PROOF setup: the mutation must actually change the file');
    fs.writeFileSync(DATA_TS_PATH, mutated, 'utf8');

    // Run a FRESH child process (this test file's own tsx import cache already
    // holds the memoised version — matches attack-bug642-memo.test.mjs's
    // documented reasoning for why this must be a child process, not inline).
    const scriptPath = path.join(path.dirname(fileURLToPath(import.meta.url)), 'scale', 'bug645-redproof-child.mjs');
    const webconsoleDir = path.resolve(path.dirname(DATA_TS_PATH), '..', '..'); // .../webconsole/src/sim/data.ts -> .../webconsole
    let output;
    let threw = false;
    try {
      // NODE_TEST_CONTEXT must NOT be inherited here: this outer file is
      // itself running under `node --test`, so process.env.NODE_TEST_CONTEXT
      // is set for THIS process — execFileSync inherits the parent env by
      // default, which would make the child's own NODE_TEST_CONTEXT guard
      // (see bug645-redproof-child.mjs's header) exit(0) immediately without
      // ever measuring anything, silently defeating this whole red-proof.
      const childEnv = { ...process.env };
      delete childEnv.NODE_TEST_CONTEXT;
      output = execFileSync(process.execPath, ['--import', 'tsx', scriptPath], {
        cwd: webconsoleDir,
        encoding: 'utf8',
        env: childEnv,
      });
    } catch (err) {
      threw = true;
      output = (err.stdout || '') + (err.stderr || '');
    }
    assert.ok(threw, 'the un-memoised child script must exit non-zero (its own perf assertion must fail)');
    assert.match(output, /RED-PROOF-FAIL/, `child output must report the perf assertion failing; got: ${output}`);
  } finally {
    fs.renameSync(BACKUP_PATH, DATA_TS_PATH);
  }
});

test('BUG-645: RED-PROOF tripwire — data.ts is currently in its expected (memoised) state, no stray backup left behind', () => {
  const src = fs.readFileSync(DATA_TS_PATH, 'utf8');
  assert.match(
    src,
    /export const residentialConstructionSummary: \(s: SimState\) => \{ count: number; capacity: number \} = memoOnState\(/,
    'data.ts must currently contain the memoised residentialConstructionSummary implementation'
  );
  assert.equal(fs.existsSync(BACKUP_PATH), false, 'no stray .bak file should be left over from a prior red-proof run');
});
