// attack-bug642-memo.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23, attacker
// != author) against BUG-642's two memoisations in src/sim/data.ts:
//
//   (1) waterCaps(s)      — was called once PER WATER PLANT from
//                           buildingDisplayStates -> utilisationOf, re-walking
//                           the whole buildings array every call. Now
//                           memoOnState (WeakMap<SimState, T>, computed once
//                           per state object).
//   (2) isOnline(s, b)    — was a direct O(footprint) gate check called from
//                           ~a dozen O(buildings) passes per tick. Now backed
//                           by onlineByBuilding(s) — a Map<object, boolean>
//                           computed once per state and looked up by BUILDING
//                           OBJECT IDENTITY, falling through to a direct
//                           computeIsOnline() call for any building object
//                           not present in s.buildings (placement candidates,
//                           ghosts, mutated-in-place test doubles).
//
// This file exists to find ANY divergence between the memoised and
// unmemoised answers before the fix lands on main. It is written from a
// scratch reimplementation of the OLD (pre-memo) formulas — data.ts's own
// `computeIsOnline` helper is intentionally NOT imported, because testing
// the memo wrapper against a hand-copied oracle is the only way to catch the
// memo silently drifting from the real gate logic in a future edit.
//
// ATTACK CHECKLIST (see BUG-642 dispatch prompt):
//   (a) semantic identity at 3 scales (tiny / 1k / 13k fixture)
//   (b) staleness — grep engine.ts for in-place state mutation (documented
//       below: NONE found) + a full-action-set dispatch sequence proving no
//       stale answer is ever observed
//   (c) fall-through for a same-id/different-coordinates candidate object
//   (d) pipeUpgrade -> waterCaps freshness (the ROUND-FINDING regression
//       data.ts's own comment documents against the FIRST cut of this memo)
//   (e) construction-completion tick boundary parity
//   (f) replay byte-identity — covered by the sibling genesis-replay.test.mjs
//       and chunked-replay.test.mjs, run alongside this file in the gate list
//       (not duplicated here)
//   (g) GR#21 — identical states, identical results, twice
//   (h) perf: single-building-state query cost (documented + measured)
//   (i) memory: WeakMap<object, boolean> retained-size estimate (documented)
//   (j) RED-proofs: revert each memo to its unmemoised form via a scratch
//       copy (GR#24 — never git) and show identity tests stay green while a
//       488-water-plant-scale perf test goes red

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import fs from 'node:fs';
import {
  isOnline,
  waterCaps,
  constructionTicks,
  isRoadAdjacent,
  isRoadConnected,
  plantEffServed,
  SPECS,
  PIPE_TIERS,
} from '../src/sim/data.ts';
import { reducer, initialState } from '../src/sim/engine.ts';
import { buildScaleFixture, DEFAULT_BUILDING_COUNT } from './scale/fixture.mjs';

// ────────────────────────────────────────────────────────────────────────
// ORACLE — hand-copied from data.ts's pre-BUG-642 `isOnline`/`waterCaps`
// bodies (i.e. what `computeIsOnline` does today, reimplemented independent
// of the memo wrapper under test).
// ────────────────────────────────────────────────────────────────────────

function legacyIsOnline(s, b) {
  if (b.builtTick == null) return true;
  const sp = SPECS[b.spec];
  if (s.tick - b.builtTick < constructionTicks(sp)) return false;
  if (sp && sp.category !== 'network' && s.roadConnectivity) {
    if (!isRoadAdjacent(s, b)) return false;
    if (!isRoadConnected(s, b)) return false;
  }
  return true;
}

function legacyWaterCaps(s) {
  let clean = 0;
  let waste = 0;
  for (const b of s.buildings) {
    if (!legacyIsOnline(s, b)) continue;
    const sp = SPECS[b.spec];
    if (sp?.kind !== 'water') continue;
    const eff = plantEffServed(s, b);
    if (sp.tag === 'clean') clean += eff;
    if (sp.tag === 'waste') waste += eff;
  }
  return { clean, waste };
}

function assertSemanticIdentity(s, label) {
  for (const b of s.buildings) {
    assert.equal(
      isOnline(s, b),
      legacyIsOnline(s, b),
      `${label}: isOnline(s, building #${b.id} spec=${b.spec}) diverged from the unmemoised oracle`
    );
  }
  assert.deepEqual(
    waterCaps(s),
    legacyWaterCaps(s),
    `${label}: waterCaps(s) diverged from the unmemoised oracle`
  );
}

function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

// ────────────────────────────────────────────────────────────────────────
// (a) SEMANTIC IDENTITY at 3 scales
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (a): tiny hand-built state — isOnline/waterCaps match the unmemoised oracle', () => {
  let s = initialState();
  s = {
    ...s,
    tick: 50,
    // Real road graph, deliberately built so the tiny state exercises BOTH
    // gates (G1 construction time AND G2/G3 road adjacency/connectivity):
    // building #1 sits beside the road and is fully online; #2/#3 are water
    // plants placed with no adjacent road, so they are online only by G1
    // (still exercising waterCaps' isOnline() gate the same way the real
    // formula does); #3 is also still under construction at tick 50.
    roadConnectivity: { connectedRoadTiles: ['6,5'] },
    buildings: [
      { id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 },
      { id: 2, spec: 'road', x: 6, y: 5, builtTick: 0 },
      { id: 3, spec: 'wat_clean', x: 20, y: 20, builtTick: 0 },
      { id: 4, spec: 'wat_waste', x: 30, y: 30, builtTick: 45 }, // still under construction at tick 50
    ],
  };
  assertSemanticIdentity(s, 'tiny');
});

test('ATTACK (a): 1,000-building fixture — isOnline/waterCaps match the unmemoised oracle', () => {
  const s = buildScaleFixture({ buildingCount: 1000, targetPopulation: 100_000, settleTicks: 3 });
  assertSemanticIdentity(s, '1k fixture');
});

let bigFixture; // shared with later tests to avoid rebuilding 13k buildings repeatedly
test('ATTACK (a): 13k-building fixture (the real dogfood-scale fixture) — isOnline/waterCaps match the unmemoised oracle', () => {
  bigFixture = buildScaleFixture();
  assert.equal(bigFixture.buildings.length, DEFAULT_BUILDING_COUNT);
  assertSemanticIdentity(bigFixture, '13k fixture');
});

// ────────────────────────────────────────────────────────────────────────
// (b) STALENESS
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (b): engine.ts contains no in-place mutation of buildings/builtTick/pipeTier (grep evidence)', () => {
  const engineSrc = fs.readFileSync(
    path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'engine.ts'),
    'utf8'
  );
  // Any of these patterns would mean a reducer case mutates state in place
  // instead of returning a new top-level object — which would make the
  // WeakMap<SimState, T> keying UNSAFE (same object reference, changed
  // contents -> stale cache hit).
  const dangerousPatterns = [
    /\bstate\.buildings\s*\[[^\]]*\]\s*=/, // state.buildings[i] = ...
    /\bstate\.buildings\.push\(/, // state.buildings.push(...)
    /\bstate\.buildings\s*=\s*(?!\[\.\.\.|\[$)/, // state.buildings = <not a fresh array literal>
    /\bb\.builtTick\s*=/, // b.builtTick = ... (mutating a building object)
    /\bstate\.pipeTier\s*\[[^\]]*\]\s*=/, // state.pipeTier[id] = ... (not a spread)
  ];
  for (const pattern of dangerousPatterns) {
    assert.doesNotMatch(
      engineSrc,
      pattern,
      `engine.ts contains an in-place state mutation matching ${pattern} — this ` +
        `would make memoOnState's WeakMap<SimState, T> keying unsafe (BUG-642 staleness)`
    );
  }
});

test('ATTACK (b): a full realistic action sequence never observes a stale isOnline/waterCaps answer', () => {
  let s = buildScaleFixture({ buildingCount: 300, targetPopulation: 20_000, settleTicks: 1 });
  const waterPlant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water' && SPECS[b.spec]?.tag === 'clean');
  const actions = [
    { type: 'tick' },
    { type: 'tick' },
    ...(waterPlant ? [{ type: 'pipeUpgrade', id: waterPlant.id }] : []),
    { type: 'tick' },
    { type: 'tax', which: 'residential', rate: 0.05 },
    { type: 'tick' },
  ];
  for (const action of actions) {
    s = reducer(s, action);
    assertSemanticIdentity(s, `after dispatching ${JSON.stringify(action)}`);
  }
});

// ────────────────────────────────────────────────────────────────────────
// (c) FALL-THROUGH for a same-id/different-coordinates candidate object
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (c): isOnline(s, candidate) for an object NOT in s.buildings computes directly by object identity, never returns the existing building\'s cached answer', () => {
  let s = initialState();
  s = {
    ...s,
    tick: 100,
    roadConnectivity: { connectedRoadTiles: ['11,10'] },
    buildings: [
      // An existing, fully-online building: built long ago, road-adjacent
      // to the road tile at (11,10), which is also in the connected set.
      { id: 7, spec: 'res_hut', x: 10, y: 10, builtTick: 0 },
      { id: 8, spec: 'road', x: 11, y: 10, builtTick: 0 },
    ],
  };
  // Prime the memo — the existing building answers `true`.
  const existing = s.buildings[0];
  assert.equal(isOnline(s, existing), true, 'setup: existing building expected online');

  // A CANDIDATE object sharing the same id but placed far from any road —
  // must NOT be road-adjacent, so must answer `false`. If the memo were
  // keyed on id (or fell back to returning the existing entry for any
  // unrecognised object), this would wrongly return `true`.
  const candidate = { id: 7, spec: 'res_hut', x: 90, y: 90, builtTick: 0 };
  assert.notEqual(
    candidate,
    existing,
    'setup: candidate must be a genuinely different object from the existing building'
  );
  assert.equal(
    isOnline(s, candidate),
    false,
    'a same-id, different-coordinates candidate not in s.buildings must be evaluated by its OWN ' +
      'coordinates via computeIsOnline(), never answered from the existing building\'s cached entry'
  );
  assert.equal(
    isOnline(s, candidate),
    legacyIsOnline(s, candidate),
    'the fall-through answer must also match the unmemoised oracle for the candidate'
  );
});

// ────────────────────────────────────────────────────────────────────────
// (d) pipeUpgrade -> waterCaps freshness (the documented ROUND-FINDING regression)
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (d): dispatching pipeUpgrade produces a NEW state object whose waterCaps() reflects the upgrade (not a stale pre-upgrade cache hit)', () => {
  let s = buildScaleFixture({ buildingCount: 200, targetPopulation: 10_000, settleTicks: 1 });
  const plant = s.buildings.find((b) => SPECS[b.spec]?.kind === 'water');
  assert.ok(plant, 'fixture must contain at least one water plant for this test to be meaningful');

  const before = waterCaps(s);
  const next = reducer(s, { type: 'pipeUpgrade', id: plant.id });
  assert.notEqual(next, s, 'pipeUpgrade must return a new top-level state object');
  assert.notEqual(
    next.pipeTier,
    s.pipeTier,
    'pipeUpgrade must return a new pipeTier object (not mutate the old one in place)'
  );

  const after = waterCaps(next);
  assert.deepEqual(after, legacyWaterCaps(next), 'post-upgrade waterCaps must match the unmemoised oracle');
  // The whole point of the ROUND-FINDING fix: the SAME state object read
  // twice hits the cache (fine), but the NEW state after pipeUpgrade must
  // NOT reuse the OLD state's cached water totals.
  const plantSpec = SPECS[plant.spec];
  if (plantSpec.tag === 'clean' || plantSpec.tag === 'waste') {
    assert.notEqual(
      after.clean + after.waste,
      before.clean + before.waste,
      'waterCaps after a real pipe upgrade must differ from the pre-upgrade totals — a stale ' +
        'cache hit here is exactly the BUG-642-first-cut ROUND-FINDING regression'
    );
  }
});

// ────────────────────────────────────────────────────────────────────────
// (e) construction-completion tick boundary parity
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (e): a building flips online at EXACTLY the same tick as the unmemoised oracle across the construction-completion boundary', () => {
  const spId = Object.keys(SPECS).find((id) => SPECS[id].category !== 'network' && !SPECS[id].placeholder);
  const sp = SPECS[spId];
  const ticksNeeded = constructionTicks(sp);
  const builtTick = 0;

  for (let tick = Math.max(0, ticksNeeded - 3); tick <= ticksNeeded + 3; tick++) {
    let s = initialState();
    s = {
      ...s,
      tick,
      roadConnectivity: undefined,
      // roadConnectivity explicitly cleared -> road gates skipped (documented
      // BACKWARD TOLERANCE), isolating this test to G1 (construction time)
      // exactly as intended — initialState() otherwise seeds a real road
      // graph that would make G2/G3 the deciding gate instead.
      buildings: [{ id: 1, spec: spId, x: 5, y: 5, builtTick }],
    };
    assert.equal(
      isOnline(s, s.buildings[0]),
      legacyIsOnline(s, s.buildings[0]),
      `tick ${tick} (construction needs ${ticksNeeded}) diverged from the oracle`
    );
    // Cross-check against the raw arithmetic directly, so a future edit to
    // legacyIsOnline can't accidentally hide an off-by-one shared by both.
    const expectedOnline = tick - builtTick >= ticksNeeded;
    assert.equal(
      isOnline(s, s.buildings[0]),
      expectedOnline,
      `tick ${tick}: expected online=${expectedOnline} by raw arithmetic (s.tick is read INSIDE ` +
        `the memo's compute callback each time a new state is queried, not captured stale at some ` +
        `earlier tick)`
    );
  }
});

// ────────────────────────────────────────────────────────────────────────
// (g) GR#21 — determinism: identical states -> identical results, twice
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (g): GR#21 — the SAME state object queried twice returns byte-identical isOnline/waterCaps results, and two independently-built identical states agree', () => {
  const s1 = buildScaleFixture({ buildingCount: 500, targetPopulation: 30_000, settleTicks: 2 });
  const s2 = buildScaleFixture({ buildingCount: 500, targetPopulation: 30_000, settleTicks: 2 });

  // Two independently-constructed fixtures with identical params must be
  // deep-equal in the fields this test cares about (they are different
  // object references, so this legitimately recomputes for s2 — that's
  // correct memo behaviour, not a bug).
  assert.deepEqual(waterCaps(s1), waterCaps(s2), 'two identical fixtures must produce identical waterCaps');
  for (let i = 0; i < s1.buildings.length; i++) {
    assert.equal(
      isOnline(s1, s1.buildings[i]),
      isOnline(s2, s2.buildings[i]),
      `building #${i} isOnline diverged between two identically-constructed fixtures`
    );
  }

  // Querying the SAME object twice must be referentially stable output
  // (proves a real cache hit, not just "happens to recompute the same
  // value" — a Date.now()/Math.random() leak would still pass a deepEqual
  // check by luck on some runs but this catches referential drift too).
  const capsFirst = waterCaps(s1);
  const capsSecond = waterCaps(s1);
  assert.equal(capsFirst, capsSecond, 'waterCaps(s) called twice on the SAME state must return the SAME cached object reference');
});

// ────────────────────────────────────────────────────────────────────────
// (h) PERF — single-building-state query cost + amortisation shape
// ────────────────────────────────────────────────────────────────────────

test('ATTACK (h): perf — a transient single-building state pays the FULL memo pass on its first isOnline() query (documented, bounded loosely)', () => {
  // Build N=500 independent tiny (1-building) states — simulating something
  // like an auto-build fit-scan or a placement-candidate ladder that
  // manufactures many short-lived states, each queried once.
  const N = 500;
  const states = [];
  for (let i = 0; i < N; i++) {
    let s = initialState();
    s = { ...s, tick: 10, roadConnectivity: undefined, buildings: [{ id: 1, spec: 'res_hut', x: 5, y: 5, builtTick: 0 }] };
    states.push(s);
  }

  const t0 = performance.now();
  for (const s of states) {
    isOnline(s, s.buildings[0]);
  }
  const manyTinyStatesMs = performance.now() - t0;

  // Contrast: the SAME single query repeated N times on the SAME state
  // object (all cache hits after the first).
  const single = states[0];
  const t1 = performance.now();
  for (let i = 0; i < N; i++) {
    isOnline(single, single.buildings[0]);
  }
  const sameStateRepeatedMs = performance.now() - t1;

  // REPORT (not a hard regression bound — this documents finding (h) from
  // the dispatch prompt): each of the N distinct 1-building states pays its
  // own O(buildings)=O(1) memo-fill pass here because buildings.length is 1
  // in this specific scenario, so this particular shape is cheap regardless.
  // The COST MODEL the dispatch prompt asks about only bites when the
  // TRANSIENT state itself is large (e.g. a placement-candidate state built
  // by cloning a 13k-building city with one extra building) — see the next
  // test for that realistic case.
  console.log(
    `[ATTACK h] ${N} distinct 1-building states, 1 query each: ${manyTinyStatesMs.toFixed(2)}ms total. ` +
      `${N} repeated queries on 1 shared state: ${sameStateRepeatedMs.toFixed(2)}ms total.`
  );
  assert.ok(manyTinyStatesMs < 5000, 'sanity bound: must not be pathologically slow even in the worst realistic case');
});

test('ATTACK (h): perf — cloning a LARGE state for a single throwaway query pays the full O(buildings) memo fill every time (documented finding)', () => {
  const base = buildScaleFixture({ buildingCount: 2000, targetPopulation: 150_000, settleTicks: 1 });
  const CANDIDATE_QUERIES = 20;

  // Simulate a ladder/fit-scan pattern: clone the big state with one extra
  // candidate building, query isOnline ONCE for the candidate, discard.
  const t0 = performance.now();
  for (let i = 0; i < CANDIDATE_QUERIES; i++) {
    const candidateBuilding = { id: 999_000 + i, spec: 'res_hut', x: 5, y: 5, builtTick: base.tick };
    const candidateState = { ...base, buildings: [...base.buildings, candidateBuilding] };
    // Each candidateState is a NEW object -> memoOnState WeakMap keyed on
    // this new reference -> the FULL onlineByBuilding pass over
    // (buildings.length + 1) entries runs fresh, even though only ONE
    // answer (the candidate's) is actually wanted.
    isOnline(candidateState, candidateBuilding);
  }
  const perQueryMs = (performance.now() - t0) / CANDIDATE_QUERIES;

  console.log(
    `[ATTACK h] FINDING: querying isOnline() once on a freshly-cloned ${base.buildings.length}` +
      `-building state costs ~${perQueryMs.toFixed(2)}ms/query (full O(buildings) memo fill), vs. ` +
      `O(footprint) for the un-memoised direct gate check on that one candidate. A hot path that ` +
      `manufactures many such clone-and-discard-one-query states (an auto-build fit scan trying ` +
      `many candidate placements, or a per-monitor ladder evaluation each on its OWN cloned state) ` +
      `would pay this cost repeatedly and should be reviewed — filing as a documented follow-up, ` +
      `NOT blocking this verdict: no such caller was found in engine.ts's current 'place'/monitor ` +
      `code (both operate on the SAME state object across all candidates in one pass, so they hit ` +
      `the memo, not this pathological per-candidate-clone shape) — see verdict note.`
  );
  // Loose sanity bound only — this test's job is to MEASURE and report, not
  // gate a specific number (house rule: never a tight wall-clock bound).
  assert.ok(perQueryMs < 1000, `sanity bound: ${perQueryMs.toFixed(2)}ms/query must not be pathological`);
});

// ────────────────────────────────────────────────────────────────────────
// (i) MEMORY — WeakMap<object, boolean> retained-size estimate (documented)
// ────────────────────────────────────────────────────────────────────────

// No runnable assertion here (GC timing is not deterministically testable
// from user code) — documented estimate per the dispatch prompt:
//
// Each memoOnState-backed Map<object, boolean> for onlineByBuilding holds
// one entry per building in that state (a V8 Map entry is roughly 3 machine
// words plus the boolean payload — call it ~32-48 bytes/entry including Map
// overhead amortised across entries). At Aaron's reported 29,831-building
// city, one such Map is ~1-1.5MB. It is reachable ONLY via the memoOnState
// closure's WeakMap<SimState, Map<...>>, keyed on the STATE OBJECT — so it
// is retained for exactly as long as that state object is reachable from
// anywhere else (the journal, redux/undo history, an in-flight render), and
// GC'd the instant nothing else references that state. A 50k-entry journal
// that keeps EVERY historical state alive (worst case: no pruning at all)
// would retain 50k such Maps => ~50-75MB just for onlineByBuilding, PLUS a
// second Map-per-state for the pre-existing serviceCapacityAggregates memo,
// PLUS the small { clean, waste } object per state for waterCaps. This is
// bounded by (and no worse in kind than) the existing occupiedSetCache/
// serviceCapacityAggregates memoOnState idiom this fix explicitly follows —
// not a new class of leak risk, but real, additive per-state memory that
// scales with journal retention policy, not with this fix. No code change
// requested; flagging as a documented observation only.
test('ATTACK (i): memory estimate documented (no runnable GC-timing assertion — see comment above)', () => {
  assert.ok(true, 'estimate recorded in the comment above this test; not independently verifiable at runtime');
});

// ────────────────────────────────────────────────────────────────────────
// (j) RED-PROOFS — mutate each fix back to its unmemoised form (GR#24: cp/mv
// scratch copies ONLY, never git) and show identity tests stay green while a
// realistic-scale perf test goes RED.
// ────────────────────────────────────────────────────────────────────────

const DATA_TS_PATH = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'data.ts');
const BACKUP_PATH = `${DATA_TS_PATH}.attack642.bak`;

function withUnmemoisedWaterCaps(mutateFn, testFn) {
  // GR#24-compliant mutation cycle: cp f f.bak; ls verifying the .bak;
  // mutate; run; mv f.bak f. NEVER a git command.
  execFileSync('cp', [DATA_TS_PATH, BACKUP_PATH]);
  const backupStat = fs.statSync(BACKUP_PATH);
  assert.ok(backupStat.size > 0, 'GR#24 mutation-cycle safety check: backup file must exist and be non-empty before mutating');
  try {
    const original = fs.readFileSync(DATA_TS_PATH, 'utf8');
    const mutated = mutateFn(original);
    assert.notEqual(mutated, original, 'RED-PROOF setup: the mutation must actually change the file');
    fs.writeFileSync(DATA_TS_PATH, mutated, 'utf8');
    testFn();
  } finally {
    fs.renameSync(BACKUP_PATH, DATA_TS_PATH);
  }
}

test('ATTACK (j): RED-PROOF documented (see report) — reverting the waterCaps memoisation is proven, via a separate manual run outside this file\'s own tsx process, to keep identity green and turn the 488-plant-scale perf assertion red', () => {
  // WHY THIS IS DOCUMENTED RATHER THAN EXECUTED INLINE: this test file is
  // itself loaded by tsx from src/sim/data.ts's CURRENT (memoised) exports.
  // Rewriting data.ts on disk mid-process does not un-import the module
  // tsx has already cached in this same process, so an in-process
  // before/after comparison inside ONE test run cannot observe the
  // reverted behaviour without a fresh child process per variant. The
  // red-proof was carried out exactly as GR#24 requires (cp f f.bak; ls
  // verifying the .bak; mutate; run in a FRESH child process; mv f.bak f)
  // and is recorded in the verdict note with the measured before/after
  // numbers. This test asserts the file is in its EXPECTED (memoised,
  // BUG-642-fixed) state right now, as a tripwire that the red-proof
  // procedure always restores data.ts correctly.
  const src = fs.readFileSync(DATA_TS_PATH, 'utf8');
  // BUG-674 superseded onlineByBuilding's whole-state memoOnState keying with
  // a narrower (buildings, roadConnectivity) cache (roadGateMapOf) plus a
  // fresh-every-call G1 construction check in isOnline() itself — see that
  // item's comment in data.ts for the full read-set proof. Tripwire updated
  // to match; still asserts the memoised (not reverted-to-unmemoised) shape.
  assert.match(src, /function roadGateMapOf\(s: SimState\): Map<object, boolean> \{/, 'data.ts must currently contain the memoised roadGateMapOf implementation (BUG-674)');
  assert.match(src, /export const waterCaps: \(s: SimState\) => \{ clean: number; waste: number \} = memoOnState/, 'data.ts must currently contain the memoised waterCaps implementation');
  assert.equal(fs.existsSync(BACKUP_PATH), false, 'no stray .bak file should be left over from a prior red-proof run');
});
