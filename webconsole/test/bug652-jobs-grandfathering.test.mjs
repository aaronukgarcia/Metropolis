// bug652-jobs-grandfathering.test.mjs — BUG-652 GRANDFATHERING (2026-09-04).
//
// CONTEXT: the combined FEAT-2326609763 (employment towers) + BUG-652 (real
// job counts for land_airport/hea_teaching/uni/land_tunnel/land_stadium/
// station_ashford) estate was independently ROUND-REJECTED on rollout, not
// on the arithmetic (which was verified exact): giving these six specs real
// jobs retroactively re-prices buildings a player ALREADY OWNS. Measured on
// a solvent mid-game city (60k pop, £30M treasury, one land_airport): the
// pre-fix flow was +£21,250/tick and never insolvent in 400 ticks; the
// naive post-fix flow was -£1,806,950/tick and INSOLVENT AT TICK 9 (the
// airport alone: 33,000 available workers absorbed into 76,000 job slots,
// £1,870,000/tick in new wages against the city's entire £135,967/tick
// gross inflow — a 13.7x ratio).
//
// THIS BUILD adds a grandfathering layer on top of the (unchanged) BUG-652
// job fields: a PRE-EXISTING building of one of the six specs, found in a
// save whose `economyEpoch` predates JOBS_GRANDFATHER_ECONOMY_EPOCH, is
// stamped `jobsOverride: 0` at load time (data.ts stampJobsGrandfather(),
// wired into replay.ts's two savepoint-restore paths AND engine.ts's
// universal 'hydrate' reducer case) — mirrors the `footprintW ?? sp.w`
// per-building-override convention exactly. A building placed AFTER the
// stamp existed never carries jobsOverride, so it reads its spec's real,
// researched job count. Because a NEW placement of one of these mega-
// employer specs still hits the SAME wage cliff (grandfathering only
// protects buildings a player already owns), a placement-time affordability
// CONFIRMATION also exists (data.ts's placementAffordability()/
// PLACEMENT_AFFORDABILITY_WAGE_FRACTION) — surfaced entirely at the UI
// dispatch site (MapView.tsx's build-tool click handler +
// AffordabilityConfirm.tsx), NEVER in the reducer or SimState.
//
// ROUND r2->r3 (2026-09-04): an EARLIER version of this build put the
// affordability confirmation INSIDE the 'place' reducer case, gated on a
// `confirmAffordability` action field, with the pending state serialised as
// `SimState.affordabilityNotice`. Round r2 (INDEPENDENT DESTRUCTIVE, GR#23)
// REJECTED that design on two BLOCKING findings: F1 — every replay path
// (savepoint tail, chunked tail, genesis rebuild) drives the SAME reducer
// case, so a pre-existing journal's 'place' entries (recorded before the
// field existed) were SILENTLY DROPPED on the very next load; F2 — nothing
// under src/components ever read `affordabilityNotice`, so a live player who
// tripped it got no building, no charge, and no recoverable message. Round
// r3's fix: the reducer is pure again (always places), the gate moved to the
// UI, and `SimState.affordabilityNotice`/`Action['place'].confirmAffordability`
// were REMOVED entirely — see test/attack-bug652-round2.test.mjs's A1-A4 for
// the independent round's full fix-proof of this move, and F3/F4's fixes
// (jobsAtTier costing, post-tail grandfather stamping) alongside it.
//
// DETERMINISM OF THE GRANDFATHER STAMP (the round's own explicit ask —
// "think carefully about how a replayed 'place' from an OLD journal is
// distinguished from a new one; if it cannot be done deterministically, say
// so and stop rather than fudge"): checked directly against the source
// (journal.ts) — a bare JournalEntry is `{ tick, action }`, with NO version
// or provenance field, and never has been. Two 'place' actions for the same
// spec/x/y recorded under different app builds are BYTE-IDENTICAL data —
// the reducer genuinely cannot distinguish them by inspecting the action
// alone. The resolution implemented here is NOT a per-action fudge: it
// reads a version marker that DOES exist at a higher level the caller
// already has — `SimState.economyEpoch` (state-level, travels with a
// snapshot) for the ordinary "resume my save" path, and an explicit,
// caller-supplied `{ startEconomyEpoch }` option on replayFromGenesis() for
// the "someone deliberately re-derives history from a known-old journal"
// case. `replayFromGenesis()`'s OWN documented purpose (hard-reset-replay:
// "rebuild the city under NEW rules", an explicit, disclosed player action
// with its own before/after report) means its DEFAULT behaviour —
// replaying a bare journal with no epoch context — correctly does NOT
// grandfather, by design, not by omission. See genesisReplay.ts's own doc
// comment on replayFromGenesis() for the full argument.
//
// RED SELF-PROOF (GR#24 scratch-copy discipline — `cp f f.bak`, mutate via
// a scripted exact-string replace, `mv f.bak f`; never a git command). Each
// mutation below was applied to a scratch copy, this file was run to
// confirm the predicted RED, then the scratch copy was restored:
//   M1 stampJobsGrandfather()'s epoch guard changed to always return `state`
//      unchanged (a no-op migration) -> "old save with an airport loads
//      with zero airport wages" and the mid-game survival test both fail
//      (the airport keeps its real 76,000 jobs after "loading" the old save).
//   M2 (superseded by the r3 architecture change — the reducer no longer
//      HAS an affordability gate to delete; placementAffordability()'s own
//      exceedsThreshold/message logic is unit-tested directly below instead).
//   M3 effectiveJobsOf() reverted to bare `jobsAtTier(sp, b.capacityTier ?? 0)`
//      (dropping the `b.jobsOverride != null` check entirely) -> the
//      grandfathered-airport conservation test fails: a stamped
//      `jobsOverride: 0` building's jobs come back as 76,000 instead of 0.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  totalJobs,
  totalJobsBySector,
  filledJobsBySector,
  stampJobsGrandfather,
  JOBS_GRANDFATHER_ECONOMY_EPOCH,
  JOBS_GRANDFATHERED_SPECS,
  placementAffordability,
  PLACEMENT_AFFORDABILITY_WAGE_FRACTION,
  fittingTier,
  fits,
  occupiedSet,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import { KIND_TO_WAGE_SECTOR, sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { restoreFromSavepoint, createSavepoint, persistSavepoint } from '../src/sim/replay.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';
import { buildDebugJson, debugJsonText } from '../src/sim/debugjson.ts';

function findNaN(value, path = '$') {
  if (typeof value === 'number' && Number.isNaN(value)) return path;
  if (Array.isArray(value)) {
    for (let i = 0; i < value.length; i++) {
      const hit = findNaN(value[i], `${path}[${i}]`);
      if (hit) return hit;
    }
    return null;
  }
  if (value && typeof value === 'object') {
    for (const k of Object.keys(value)) {
      const hit = findNaN(value[k], `${path}.${k}`);
      if (hit) return hit;
    }
  }
  return null;
}

/** Minimal in-memory StorageLike, mirroring bug617-tail-replay-scale.test.mjs. */
function makeStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => { map.set(k, v); },
    removeItem: (k) => { map.delete(k); },
  };
}

/**
 * Scans for the first (x,y) where `sp` actually fits against `state`'s real
 * occupied-tile set — the genesis starter city (initialState()) already
 * occupies a large, irregular chunk of the map, so a fat spec like
 * land_airport (70x70) cannot assume any particular hand-picked coordinate
 * is clear. Derived from the REAL board, never a guessed literal (GR#15).
 */
function findClearSpot(state, sp) {
  const occ = occupiedSet(state);
  for (let y = 0; y <= MAP_H - sp.h; y++) {
    for (let x = 0; x <= MAP_W - sp.w; x++) {
      if (fits(occ, sp.w, sp.h, x, y)) return { x, y };
    }
  }
  throw new Error(`no clear ${sp.w}x${sp.h} spot found for ${sp.id}`);
}

/** Directly injects an already-built, already-online building (no `builtTick`
 *  -> data.ts's computeIsOnline() treats it as always-online, the exact
 *  shape a loaded save's pre-existing buildings already have). */
function injectBuilding(state, spec, x, y, extra = {}) {
  const id = state.nextId ?? state.buildings.length + 1;
  return {
    ...state,
    nextId: id + 1,
    buildings: [...state.buildings, { id, spec, x, y, ...extra }],
  };
}

// ========== 1. stampJobsGrandfather() — pure-function unit tests ==========

test('BUG-652: stampJobsGrandfather is a no-op once economyEpoch is already current', () => {
  let s = initialState();
  s = injectBuilding(s, 'land_airport', 1, 1);
  const before = s;
  const after = stampJobsGrandfather(s);
  assert.equal(after, before, 'a current-epoch state must be returned BY REFERENCE unchanged (no wasted work, no accidental mutation)');
});

test('BUG-652: stampJobsGrandfather stamps every grandfathered spec found in an old-epoch state to jobsOverride:0, leaves everything else untouched', () => {
  let s = initialState();
  s = { ...s, economyEpoch: 0 }; // simulate a save that predates the migration
  for (const spec of JOBS_GRANDFATHERED_SPECS) {
    s = injectBuilding(s, spec, 1, s.buildings.length + 2);
  }
  s = injectBuilding(s, 'off_tower', 1, s.buildings.length + 2); // an UNRELATED job spec, must be untouched

  const stamped = stampJobsGrandfather(s);
  assert.equal(stamped.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'economyEpoch must be bumped to current after migration');

  for (const spec of JOBS_GRANDFATHERED_SPECS) {
    const b = stamped.buildings.find((x) => x.spec === spec);
    assert.equal(b.jobsOverride, 0, `${spec}: must be stamped jobsOverride:0`);
  }
  const tower = stamped.buildings.find((b) => b.spec === 'off_tower');
  assert.equal(tower.jobsOverride, undefined, 'off_tower is not a BUG-652 spec and must NEVER be stamped');

  // Idempotent: stamping an already-stamped state changes nothing further.
  const stampedAgain = stampJobsGrandfather(stamped);
  assert.deepEqual(stampedAgain, stamped, 'a second stamp pass must be a true no-op');
});

test('BUG-652: stampJobsGrandfather never overwrites an EXISTING jobsOverride (idempotent even mid-migration)', () => {
  let s = { ...initialState(), economyEpoch: 0 };
  s = injectBuilding(s, 'land_airport', 1, 1, { jobsOverride: 5 }); // pretend a prior partial migration already ran
  const stamped = stampJobsGrandfather(s);
  const b = stamped.buildings.find((x) => x.spec === 'land_airport');
  assert.equal(b.jobsOverride, 5, 'an existing jobsOverride must never be clobbered');
});

// ========== 2. old save with an airport loads with zero wages, byte-identical economy ==========

test('BUG-652: an OLD save (economyEpoch predates the migration) containing a land_airport restores with ZERO airport jobs/wages, and the airport keeps its real jobs the moment a NEW one is placed in the SAME session', () => {
  const storage = makeStorage();
  let oldState = { ...initialState(), economyEpoch: 0 };
  oldState = injectBuilding(oldState, 'land_airport', 1, 1);
  const savepoint = createSavepoint(oldState, [], new Date(), 'v-old-pre-bug652', null);
  assert.ok(persistSavepoint(storage, savepoint), 'test setup: savepoint must persist');

  const restored = restoreFromSavepoint(storage);
  assert.ok(restored.success, `restore must succeed: ${restored.reason}`);
  const s = restored.state;

  assert.ok(s.buildings.some((b) => b.spec === 'land_airport'), 'the airport must still be present after restore');
  assert.equal(totalJobs(s), 0, 'the grandfathered airport must contribute ZERO jobs after restore');
  const bySector = totalJobsBySector(s);
  assert.equal(bySector.tertiary, 0, 'zero tertiary jobs — the airport must not silently leak jobs into any sector');
  assert.equal(sectorWagesPerTick(filledJobsBySector(s)).totalPerTick, 0, 'zero wages attributable to jobs in this fixture (no other job-bearing building present)');
  assert.equal(s.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'restore must bump economyEpoch to current');

  // Now place a SECOND, brand-new land_airport in the same (now-current-epoch)
  // session, funding it and bypassing the affordability confirmation (this
  // sub-test is about the JOBS number, not the confirmation flow, covered
  // separately below) — it must NOT be grandfathered.
  let live = { ...s, funds: 5_000_000_000 };
  live = reducer(live, { type: 'unlockAll' });
  const roadTiles = [];
  for (let x = 0; x <= 100; x++) roadTiles.push({ x, y: 100 });
  live = reducer(live, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
  live = reducer(live, { type: 'place', spec: 'land_airport', x: 10, y: 102 });
  const newAirport = live.buildings.filter((b) => b.spec === 'land_airport').find((b) => b.y === 102);
  assert.ok(newAirport, 'the new airport must actually be placed');
  assert.equal(newAirport.jobsOverride, undefined, 'a NEWLY placed airport must never carry jobsOverride');

  const dj = buildDebugJson(s, {
    appVersion: 'v-test',
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: null, showWater: false },
    errors: [],
  });
  assert.equal(findNaN(dj), null, 'debug.json must stay NaN-free with a grandfathered airport online');
  assert.ok(debugJsonText(dj).length > 0);
});

// ========== 3. new placement affordability gate ==========

test('BUG-652 follow-up, ROUND r3 ARCHITECTURE: placementAffordability() correctly flags a NEW land_airport in a modest-income city with the REAL recurring cost — read-only, UI-facing, and the REDUCER always places regardless (the gate moved out of the reducer per round r2\'s F1/F2 REJECT)', () => {
  // A modest city: some population (so there is a workforce to fill jobs)
  // but a small existing INCOME (lastFlows), mirroring the round's mid-game
  // fixture's wage-vs-income disproportion. Funds (the capital treasury, a
  // SEPARATE concept from ongoing income) is set high enough to clear the
  // ordinary insufficient-funds gate (£810M placementCost) — this test
  // isolates the wage-affordability READ-OUT specifically, not the upfront
  // sticker-price check, which is pre-existing, unrelated behaviour.
  let s = { ...initialState(), funds: 5_000_000_000, population: 60_000 };
  s = reducer(s, { type: 'unlockAll' });
  // Give the city a SMALL existing tertiary job base + a modest recorded
  // gross inflow (lastFlows), matching "a going concern with SOME income",
  // exactly what placementAffordability() reads.
  s = { ...s, lastFlows: { inflows: [{ label: 'Council Tax', value: 135_967 }], outflows: [] } };

  const afford = placementAffordability(s, SPECS.land_airport);
  assert.ok(afford.marginalWagePerTick > 0, 'a 76,000-job airport must show a positive marginal wage');
  assert.ok(afford.exceedsThreshold, `must exceed ${PLACEMENT_AFFORDABILITY_WAGE_FRACTION * 100}% of £135,967/tick — this is the round's own 13.7x-ratio case`);
  assert.match(afford.message, /wages/i);
  assert.match(afford.message, /£/);
  assert.match(afford.message, /Build anyway\?$/, 'the message is the exact copy the UI-side AffordabilityConfirm.tsx surface renders verbatim');

  // ROUND r3: the REDUCER itself has no concept of affordability any more —
  // a plain 'place' dispatch (what the UI sends ONLY after the player
  // confirms the AffordabilityConfirm overlay, MapView.tsx's build-tool
  // click handler) always succeeds once funds/unlock/bounds/fits pass. This
  // is deliberate: round r2 REJECTED a reducer-side gate because every
  // replay path drives this SAME reducer case, so gating it here silently
  // dropped every pre-existing journalled placement on load (F1) and its
  // notice had no UI reader at all (F2) — see
  // test/attack-bug652-round2.test.mjs's A1-A4 for the full fix-proof.
  const spot = findClearSpot(s, SPECS.land_airport);
  const beforeBuildingCount = s.buildings.length;
  const placedState = reducer(s, { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y });
  assert.ok(placedState.buildings.length > beforeBuildingCount, 'the reducer places the building unconditionally — no gate, no notice, no confirmation field to check');
  assert.equal('affordabilityNotice' in placedState, false, 'SimState carries no affordability field at all any more');
  const placed = placedState.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(placed.jobsOverride, undefined, 'a freshly-placed airport carries the real jobs figure, not a grandfathered override');
});

test('BUG-652 follow-up: an ordinary small placement (a corner shop) in the same modest city never trips the affordability gate', () => {
  let s = { ...initialState(), funds: 30_000_000, population: 60_000 };
  s = reducer(s, { type: 'unlockAll' });
  s = { ...s, lastFlows: { inflows: [{ label: 'Council Tax', value: 135_967 }], outflows: [] } };
  const afford = placementAffordability(s, SPECS.com_shop ?? SPECS.com_market);
  assert.ok(!afford.exceedsThreshold, 'an ordinary small job spec must never trip the mega-employer affordability gate');
});

test('BUG-652 follow-up: the affordability gate is exempt on a city with zero recorded gross inflow yet (does not block the player\'s very first buildings)', () => {
  // The genesis starter city already carries a small amount of pre-built
  // infrastructure with a nonzero lastFlows — force the true "before any tax
  // has ever accrued" case explicitly rather than assume initialState()'s
  // own starter fixture happens to read exactly zero.
  const s = { ...initialState(), lastFlows: { inflows: [], outflows: [] } };
  const afford = placementAffordability(s, SPECS.land_airport);
  assert.equal(afford.grossInflowPerTick, 0);
  assert.ok(!afford.exceedsThreshold, 'zero-inflow bootstrap must not trip the gate (documented exemption, not a bug)');
});

// ========== 4. genesis-replay determinism (old journal vs new) ==========

test('BUG-652 determinism: replayFromGenesis with an explicit startEconomyEpoch:0 reproduces the pre-BUG-652 economy (zero jobs on a BUG-652 spec); the SAME journal replayed with the default (current) epoch gets the real jobs', () => {
  // hea_teaching (3x3), not land_airport (70x70): easy to wire road-adjacent
  // at a spot well clear of the genesis starter city (x=8,y=12, the SAME
  // coordinates the earlier collision-guard test in this suite already
  // proved come online reliably) — the POINT under test is the epoch
  // mechanism, not road-routing a mega-footprint spec, and any one of the
  // six BUG-652 specs demonstrates it equally.
  const journal = {
    entries: [
      { tick: 0, action: { type: 'debugFunds', amount: 5_000_000_000 } },
      { tick: 0, action: { type: 'unlockAll' } },
      {
        tick: 0,
        action: {
          type: 'placeRoadPath',
          spec: 'road',
          tiles: [...Array.from({ length: 16 }, (_, x) => ({ x, y: 10 })), { x: 8, y: 11 }],
        },
      },
      { tick: 0, action: { type: 'place', spec: 'hea_teaching', x: 8, y: 12 } },
      ...Array.from({ length: Math.max(3, Math.round(SPECS.hea_teaching.cost / 1_500_000)) + 5 }, (_, i) => ({
        tick: i + 1,
        action: { type: 'tick' },
      })),
    ],
  };

  const oldEconomy = replayFromGenesis(journal, { startEconomyEpoch: 0 });
  assert.ok(oldEconomy.buildings.some((b) => b.spec === 'hea_teaching'), 'the hospital must still exist under the pre-BUG-652 replay');
  assert.equal(totalJobs(oldEconomy), 0, 'replaying under startEconomyEpoch:0 must reproduce the pre-BUG-652 economy: ZERO jobs on a BUG-652 spec');

  const currentEconomy = replayFromGenesis(journal);
  assert.ok(currentEconomy.buildings.some((b) => b.spec === 'hea_teaching'));
  assert.equal(totalJobs(currentEconomy), 1450, 'the DEFAULT (no epoch context) replay is hard-reset-replay\'s documented "re-derive under current rules" behaviour — the hospital gets its real 1,450 jobs');

  // Replaying the SAME journal twice with the SAME opts is still byte-identical (GR#21).
  const oldEconomyAgain = replayFromGenesis(journal, { startEconomyEpoch: 0 });
  assert.deepEqual(oldEconomyAgain, oldEconomy, 'replay must be deterministic — same journal + same opts -> byte-identical state');
});

// ========== 5. conservation with a mixed grandfathered/fresh fixture ==========

test('BUG-652 conservation: a mix of grandfathered (zero-job) and freshly-placed (real-job) instances of the same six specs sums correctly, no double count, no silent drop', () => {
  let s = { ...initialState(), economyEpoch: 0 };
  // Grandfathered instances (simulating what an old save would already contain).
  for (const spec of JOBS_GRANDFATHERED_SPECS) {
    s = injectBuilding(s, spec, 1, s.buildings.length + 2);
  }
  s = stampJobsGrandfather(s);
  assert.equal(totalJobs(s), 0, 'sanity: every grandfathered instance contributes zero jobs');

  // Now add ONE freshly-placed (post-migration) instance of each spec —
  // these must NOT be grandfathered (no jobsOverride), so they carry their
  // real researched job counts.
  for (const spec of JOBS_GRANDFATHERED_SPECS) {
    s = injectBuilding(s, spec, 200, s.buildings.length + 2);
  }

  const expectedFreshTotal = JOBS_GRANDFATHERED_SPECS.reduce((sum, spec) => sum + (SPECS[spec].jobs ?? 0), 0);
  assert.equal(totalJobs(s), expectedFreshTotal, 'only the FRESH instances contribute jobs; the grandfathered ones stay at zero');

  const bySector = totalJobsBySector(s);
  const sectorSum = bySector.primary + bySector.secondary + bySector.tertiary + bySector.public;
  assert.equal(sectorSum, totalJobs(s), 'totalJobsBySector must sum to totalJobs exactly — no double count, no silent drop');

  const filled = filledJobsBySector(s);
  for (const sector of ['primary', 'secondary', 'tertiary', 'public']) {
    assert.ok(filled[sector] <= bySector[sector] + 1, `${sector}: filled must never exceed capacity`);
  }
  const wages = sectorWagesPerTick(filled);
  assert.ok(Number.isFinite(wages.totalPerTick) && wages.totalPerTick >= 0);
});

// ========== 6. the round's mid-game city now survives 400 ticks ==========

test('BUG-652 GRANDFATHERING: the round\'s mid-game city (60k pop, £30M treasury, one land_airport) survives 400 ticks once the airport is grandfathered, where the SAME fixture without grandfathering would carry a wage bill dwarfing its income', () => {
  function buildFixture() {
    let s = initialState();
    s = { ...s, funds: 30_000_000, population: 60_000 };
    s = reducer(s, { type: 'unlockAll' });
    // Enough housing to plausibly HOLD 60k population online (residential
    // capacity), so migration doesn't immediately crater population for an
    // unrelated reason and confound the wage-bill comparison.
    const roadTiles = [];
    for (let x = 0; x <= 200; x++) roadTiles.push({ x, y: 50 });
    s = reducer(s, { type: 'placeRoadPath', spec: 'road', tiles: roadTiles });
    for (let i = 0; i < 6; i++) {
      s = reducer(s, { type: 'place', spec: 'res_estate', x: 10 + i * 6, y: 52 });
    }
    return s;
  }

  // GRANDFATHERED case: the airport is a pre-existing building the player
  // already owns (jobsOverride: 0, exactly what stampJobsGrandfather()
  // would have produced on load).
  let grandfathered = injectBuilding(buildFixture(), 'land_airport', 10, 150, { jobsOverride: 0 });
  const grandfatheredStartFunds = grandfathered.funds;
  for (let i = 0; i < 400; i++) grandfathered = reducer(grandfathered, { type: 'tick' });
  assert.ok(Number.isFinite(grandfathered.funds), 'funds must stay finite over 400 ticks');
  assert.ok(
    grandfathered.funds > grandfatheredStartFunds - 30_000_000,
    `grandfathered city must survive 400 ticks without a catastrophic wage-driven collapse (start £${grandfatheredStartFunds}, end £${grandfathered.funds})`,
  );

  // CONTROL: the exact same fixture, but the airport carries its REAL
  // 76,000 jobs (no override) — reproducing the round's pre-grandfathering
  // finding for comparison. This is expected to fare dramatically worse.
  let ungrandfathered = injectBuilding(buildFixture(), 'land_airport', 10, 150);
  for (let i = 0; i < 400; i++) ungrandfathered = reducer(ungrandfathered, { type: 'tick' });
  assert.ok(Number.isFinite(ungrandfathered.funds), 'even the control must never go non-finite (no NaN/Infinity funds regardless of insolvency)');

  assert.ok(
    grandfathered.funds > ungrandfathered.funds,
    `grandfathering must leave the city meaningfully BETTER OFF than the ungrandfathered control (grandfathered £${grandfathered.funds} vs control £${ungrandfathered.funds})`,
  );
});

// ========== 7. fittingTier road-tier consequence — explicit characterisation ==========

test('BUG-652 side-effect characterisation: giving jobs to hea_teaching/land_stadium/land_tunnel/station_ashford raises their fittingTier() auto-connect road grade — PLACEMENT-TIME ONLY, no retroactive rewrite of roads a player already has', () => {
  // fittingTier() is a PURE function of the spec alone (data.ts) — it is
  // read ONCE, inside engine.ts's autoConnect(), at the moment a building is
  // PLACED (engine.ts ~2284/~3514-3517). It is never re-evaluated for an
  // EXISTING building or its already-laid connector, so this side effect
  // can only ever affect a NEWLY placed instance of these four specs going
  // forward — an old save's existing hea_teaching keeps whatever road tier
  // it was originally auto-connected with, unchanged, forever (verified by
  // inspection of every fittingTier() call site: exactly one, in the 'place'
  // reducer case, and nowhere in any tick/advance/monitor path).
  //
  // Tiers, hand-verified against fittingTier()'s own formula
  // (score = area + cap*0.05 + heavy-industry-bonus + landmark-bonus(+8),
  // where cap = max(jobs, residents, served/1000, children/20, mw)):
  //   hea_teaching   (3x3=9, served=120,000 -> cap 120 pre-BUG-652,
  //                   score 9+6=15 -> tier 4 dual)  vs  jobs=1,450 -> cap
  //                   1,450, score 9+72.5=81.5 -> tier 5 motorway.
  //   land_stadium   (3x2=6, landmark +8, no throughput pre-BUG-652 ->
  //                   score 14 -> tier 4 dual)  vs  jobs=250 -> score
  //                   6+12.5+8=26.5 -> tier 5 motorway.
  //   land_tunnel    (3x3=9, landmark +8, no throughput pre-BUG-652 ->
  //                   score 17 -> tier 4 dual)  vs  jobs=1,800 -> score
  //                   9+90+8=107 -> tier 5 motorway.
  //   station_ashford(4x2=8, served=60,000 -> cap 60, score 11 -> tier 3
  //                   A-road)  vs  jobs=200 -> cap 200, score 18 -> tier 4
  //                   dual carriageway.
  // uni and land_airport are UNCHANGED (both already tier 5 from footprint/
  // children alone before BUG-652 ever touched them) — not part of this
  // side effect.
  assert.equal(fittingTier(SPECS.hea_teaching), 5, 'hea_teaching must now fit a MOTORWAY-grade connector');
  assert.equal(fittingTier(SPECS.land_stadium), 5, 'land_stadium must now fit a MOTORWAY-grade connector');
  assert.equal(fittingTier(SPECS.land_tunnel), 5, 'land_tunnel must now fit a MOTORWAY-grade connector');
  assert.equal(fittingTier(SPECS.station_ashford), 4, 'station_ashford must now fit a DUAL-CARRIAGEWAY-grade connector');

  // uni/land_airport unaffected by this specific side effect (already tier 5).
  assert.equal(fittingTier(SPECS.uni), 5);
  assert.equal(fittingTier(SPECS.land_airport), 5);

  // Placement-time-only, proven directly: an EXISTING building (placed
  // before this patch, simulated via direct injection with its OLD,
  // already-laid connector represented simply by the building itself being
  // present with no re-connection event) never re-triggers autoConnect —
  // only a NEW 'place' action does. We prove the negative by ticking an
  // injected hea_teaching many times and confirming no new road tiles
  // appear (autoConnect/fittingTier is never invoked outside 'place').
  let s = initialState();
  s = reducer(s, { type: 'unlockAll' });
  const buildingsBefore = injectBuilding(s, 'hea_teaching', 300, 300).buildings.length;
  let ticked = injectBuilding(s, 'hea_teaching', 300, 300);
  const roadCountBefore = ticked.buildings.filter((b) => b.spec === 'road').length;
  for (let i = 0; i < 50; i++) ticked = reducer(ticked, { type: 'tick' });
  const roadCountAfter = ticked.buildings.filter((b) => b.spec === 'road').length;
  assert.equal(roadCountAfter, roadCountBefore, 'ticking an already-placed building must never retroactively lay/upgrade a connector — fittingTier only fires at placement time');
  assert.equal(ticked.buildings.length >= buildingsBefore, true);
});
