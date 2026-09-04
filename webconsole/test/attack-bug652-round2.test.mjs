// attack-bug652-round2.test.mjs — BUG-652 r2 INDEPENDENT DESTRUCTIVE ROUND
// (GR#23: attacker != author). 2026-09-04.
//
// SCOPE: the r2 estate — jobsOverride/economyEpoch grandfathering,
// stampJobsGrandfather()'s three load paths, effectiveJobsOf(), and the
// placement-time affordability gate (placementAffordability() +
// PLACEMENT_AFFORDABILITY_WAGE_FRACTION + the 'place' reducer case's
// confirmAffordability branch).
//
// WHAT THIS FILE IS: characterisation + attack evidence. The tests here PASS
// against the estate as shipped — several of them pass precisely BECAUSE they
// pin defective behaviour this round found. Each such test says so on its own
// assertion message, so a later fix will RED them deliberately and loudly.
//
// FINDINGS (see the verdict note on BUG-652 for the full write-up):
//   F1 (BLOCKING) — an OLD journal tail's 'place' action is SILENTLY DROPPED
//      on restore. Every journal recorded before this build has no
//      `confirmAffordability` field on its 'place' entries, so the new gate
//      in the 'place' reducer case fires DURING TAIL REPLAY and returns a
//      state with a notice and NO BUILDING. A player's already-built,
//      already-paid-for airport does not come back. restoreFromSavepoint
//      still reports success:true with no error. This is a save-migration
//      regression shipped by a save-migration fix.
//   F2 (BLOCKING) — the affordability gate has NO UI. Nothing under
//      src/components reads `state.affordabilityNotice`, and nothing anywhere
//      dispatches `confirmAffordability: true`. In the live app the gate
//      turns a placement into a permanent, silent, feedback-free no-op: the
//      player clicks, nothing appears, no message, no charge, and there is no
//      "build anyway" affordance to recover with. land_airport, hea_teaching,
//      off_tower_canary and off_tower_marina become UNBUILDABLE on a normal
//      mid-game city.
//   F3 — placementAffordability() reads `capacityAtTier(sp, 0)` for the new
//      spec's job count instead of the estate's OWN jobsAtTier() SSOT, so a
//      spec carrying `jobs` alongside a `served`-sized capacityTiers ladder
//      (hea_teaching) is costed at its SERVED tier value (120,000) rather
//      than its job count (1,450) — an 82.8x overstatement of the number the
//      player is quoted, and exactly the misread jobsAtTier() was written to
//      prevent (GR#3).
//   F4 — the tail-journal seam is real: an airport that lives in the TAIL
//      (not the snapshot) comes back UNGRANDFATHERED at its full 76,000 jobs.
//      Combined with F1 this is currently masked (the placement is dropped
//      instead), but the moment F1 is fixed by threading confirmAffordability
//      through, F4 becomes live.
//   F5 — jobsOverride:0 is honoured by the two wage/employment consumers only.
//      A grandfathered building is still a full-size WORKPLACE for
//      feederTrafficWeight()/lineUsageOf() (road+rail traffic read-outs),
//      fittingTier(), densityTier() and the building profile panel.
//
// VERIFIED SOUND (no defect): the grandfather stamp itself, its idempotence,
// its epoch fence across all three load paths, the r1 60k/£30m/airport city's
// solvency under the new build, and fittingTier's placement-time-only pin.
//
// GR#24: no git command was used at any point. The RED-proofs below were run
// by cp/scripted-edit/mv on scratch copies and are documented, not automated.

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
  capacityAtTier,
  fittingTier,
  fits,
  occupiedSet,
  MAP_W,
  MAP_H,
} from '../src/sim/data.ts';
import { sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { initialState, reducer, feederTrafficWeight } from '../src/sim/engine.ts';
import {
  restoreFromSavepoint,
  prepareRestoreForChunkedTail,
  createSavepoint,
  persistSavepoint,
} from '../src/sim/replay.ts';
import { replayFromGenesis } from '../src/sim/genesisReplay.ts';

// ─────────────────────────── helpers ────────────────────────────────────────

function makeStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => { m.set(k, v); },
    removeItem: (k) => { m.delete(k); },
  };
}

/** Injects an ALREADY-BUILT, always-online building (no builtTick) — the exact
 *  shape a loaded save's pre-existing buildings have. */
function inject(s, spec, x, y, extra = {}) {
  const id = s.nextId ?? s.buildings.length + 1;
  return { ...s, nextId: id + 1, buildings: [...s.buildings, { id, spec, x, y, ...extra }] };
}

function findClearSpot(state, sp) {
  const occ = occupiedSet(state);
  for (let y = 0; y <= MAP_H - sp.h; y++) {
    for (let x = 0; x <= MAP_W - sp.w; x++) {
      if (fits(occ, sp.w, sp.h, x, y)) return { x, y };
    }
  }
  throw new Error(`no clear ${sp.w}x${sp.h} spot for ${sp.id}`);
}

/** The r1 round's own city: 200 housing estates, 60,000 people, £30m, and one
 *  already-owned land_airport. Solvent PRE-BUG-652, bankrupt at tick 9 under
 *  the unphased BUG-652 build. `economyEpoch: 0` == a save written before this
 *  migration existed. */
function r1City(funds = 30_000_000, economyEpoch = 0) {
  let s = { ...initialState(), economyEpoch, funds, population: 60_000, unlockedAll: true };
  for (let i = 0; i < 200; i++) s = inject(s, 'res_estate', 100 + (i % 60) * 5, 5 + Math.floor(i / 60) * 4);
  s = inject(s, 'land_airport', 300, 5);
  return s;
}

const grossInflowOf = (s) => s.lastFlows.inflows.reduce((a, f) => a + f.value, 0);

// ══════════════════════════════════════════════════════════════════════════
// A1 — THE EPOCH FENCE. Stamp exactly once, never twice, never on a new save.
// ══════════════════════════════════════════════════════════════════════════

test('A1a: the epoch fence holds through a full old->stamped->resaved->reloaded lifecycle on the PLAIN restore path — the old airport stays at 0 jobs and is never re-stamped', () => {
  const storage = makeStorage();
  const old = r1City();
  assert.equal(old.economyEpoch, 0, 'setup: this save predates the migration');
  assert.ok(persistSavepoint(storage, createSavepoint(old, [], new Date(), 'v-old', null)));

  const r1 = restoreFromSavepoint(storage);
  assert.ok(r1.success, 'old save must restore');
  const airport = r1.state.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airport.jobsOverride, 0, 'the pre-existing airport must be pinned to 0 jobs');
  assert.equal(r1.state.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'epoch must be bumped exactly to current');
  assert.equal(totalJobs(r1.state), 0, 'a grandfathered airport contributes no jobs');

  // Re-save the migrated state and reload: must be a pure no-op, byte-stable.
  const storage2 = makeStorage();
  assert.ok(persistSavepoint(storage2, createSavepoint(r1.state, [], new Date(), 'v-new', null)));
  const r2 = restoreFromSavepoint(storage2);
  assert.ok(r2.success);
  const airport2 = r2.state.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airport2.jobsOverride, 0, 'a second load must not change the pin');
  assert.equal(r2.state.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'the epoch must never advance past current');
  assert.equal(totalJobs(r2.state), 0, 'still zero after a second round-trip');
});

test('A1b: a NEW save is never stamped — a city created under this build keeps its airports at FULL jobs across a save/load round-trip (no accidental blanket zeroing)', () => {
  const storage = makeStorage();
  // A brand-new city (initialState stamps the current epoch) with a FRESH
  // airport injected as an already-built building — i.e. the player built it
  // under this build, so it must keep its real job count forever.
  let fresh = { ...initialState(), population: 60_000, unlockedAll: true, buildings: [] };
  assert.equal(fresh.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'initialState must stamp the current epoch');
  fresh = inject(fresh, 'land_airport', 300, 5);
  assert.ok(persistSavepoint(storage, createSavepoint(fresh, [], new Date(), 'v-new', null)));
  const r = restoreFromSavepoint(storage);
  assert.ok(r.success);
  const airport = r.state.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airport.jobsOverride, undefined, 'a NEW-epoch save must NEVER be stamped');
  assert.equal(totalJobs(r.state), 76_000, 'a legitimately-new airport keeps its real job count');
});

test('A1c: the CHUNKED-tail load path stamps exactly once, with the same result as the plain path (three distinct paths, one behaviour)', () => {
  const storage = makeStorage();
  const old = r1City();
  assert.ok(persistSavepoint(storage, createSavepoint(old, [], new Date(), 'v-old', null)));

  const prepared = prepareRestoreForChunkedTail(storage);
  assert.ok(prepared.success, `chunked prepare must succeed: ${prepared.reason ?? ''}`);
  const airport = prepared.state.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airport.jobsOverride, 0, 'chunked path must grandfather identically to the plain path');
  assert.equal(prepared.state.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH);

  // And the hydrate reducer case (the third path) on the prepared state is a
  // true no-op — it must not double-stamp or re-derive anything.
  const hydrated = reducer(prepared.state, { type: 'hydrate', state: prepared.state });
  const airportH = hydrated.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airportH.jobsOverride, 0, 'hydrate must not disturb an already-stamped state');
  assert.equal(hydrated.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH);
  assert.equal(totalJobs(hydrated), totalJobs(prepared.state), 'hydrate must be economically identical');
});

test('A1d: the HYDRATE path is a genuine third entry point — hydrating a RAW old-epoch state (never through replay.ts) grandfathers it', () => {
  const old = r1City();
  const hydrated = reducer(initialState(), { type: 'hydrate', state: old });
  const airport = hydrated.buildings.find((b) => b.spec === 'land_airport');
  assert.equal(airport.jobsOverride, 0, 'hydrate must be able to stamp on its own');
  assert.equal(hydrated.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH);
});

test('A1e: the fence is per-BUILDING, not per-save — grandfather an old save, then place a FRESH airport in the same session: the old one stays 0, the new one keeps 76,000, and that survives a save/reload', () => {
  const storage = makeStorage();
  const old = r1City(5_000_000_000);
  assert.ok(persistSavepoint(storage, createSavepoint(old, [], new Date(), 'v-old', null)));
  let live = restoreFromSavepoint(storage).state;
  assert.equal(live.buildings.find((b) => b.spec === 'land_airport').jobsOverride, 0);

  const spot = findClearSpot(live, SPECS.land_airport);
  live = reducer(live, { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y, confirmAffordability: true });
  const airports = live.buildings.filter((b) => b.spec === 'land_airport');
  assert.equal(airports.length, 2, 'the fresh airport must actually be placed');
  const fresh = airports.find((b) => b.builtTick !== undefined);
  assert.equal(fresh.jobsOverride, undefined, 'a freshly-placed airport must NEVER be stamped');

  // Save + reload: the asymmetry must persist exactly.
  const storage2 = makeStorage();
  assert.ok(persistSavepoint(storage2, createSavepoint(live, [], new Date(), 'v-new', null)));
  const reloaded = restoreFromSavepoint(storage2).state;
  const olds = reloaded.buildings.filter((b) => b.spec === 'land_airport' && b.jobsOverride === 0);
  const news = reloaded.buildings.filter((b) => b.spec === 'land_airport' && b.jobsOverride === undefined);
  assert.equal(olds.length, 1, 'exactly one grandfathered airport survives the round-trip');
  assert.equal(news.length, 1, 'exactly one full-jobs airport survives the round-trip');
});

test('A1f: every one of the six grandfathered specs is stamped, and nothing else in the catalogue ever is', () => {
  let s = { ...initialState(), economyEpoch: 0, buildings: [] };
  const others = ['off_tower', 'off_towers_downtown', 'off_tower_canary', 'off_tower_marina', 'grand_terminus', 'ind_estate', 'com_mall'];
  [...JOBS_GRANDFATHERED_SPECS, ...others].forEach((spec, i) => { s = inject(s, spec, 1, i * 2); });
  const stamped = stampJobsGrandfather(s);
  for (const spec of JOBS_GRANDFATHERED_SPECS) {
    assert.equal(stamped.buildings.find((b) => b.spec === spec).jobsOverride, 0, `${spec} must be pinned`);
  }
  for (const spec of others) {
    assert.equal(stamped.buildings.find((b) => b.spec === spec).jobsOverride, undefined, `${spec} must NEVER be pinned`);
  }
  // The two FEAT-2326609763 towers are deliberately NOT grandfathered — they
  // are brand-new specs, so no save can contain a pre-existing one.
  assert.ok(!JOBS_GRANDFATHERED_SPECS.includes('off_tower_canary'));
  assert.ok(!JOBS_GRANDFATHERED_SPECS.includes('off_tower_marina'));
});

// ══════════════════════════════════════════════════════════════════════════
// A2 — F1 (BLOCKING): the affordability gate DESTROYS old journal tails.
// ══════════════════════════════════════════════════════════════════════════

test('A2a (F1 FIX-PROOF, was BLOCKING): an OLD journal tail placing a land_airport now comes BACK on restore — the gate no longer lives in the reducer, so replay can never drop a journalled action', () => {
  const storage = makeStorage();
  // The SAME real pre-this-build journal entry as before the fix: a bare
  // `{type:'place',spec,x,y}` with NO `confirmAffordability` field (that
  // field no longer exists on the Action type at all, ROUND r3 — see
  // engine.ts's Action union doc comment).
  let snap = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) snap = reducer(snap, { type: 'tick' });
  assert.ok(grossInflowOf(snap) > 0, 'setup: the city must have real income');
  const spot = findClearSpot(snap, SPECS.land_airport);
  const tail = [{ tick: snap.tick, action: { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y } }];
  assert.ok(persistSavepoint(storage, createSavepoint({ ...snap, economyEpoch: 0 }, tail, new Date(), 'v-old', null)));

  const r = restoreFromSavepoint(storage);
  assert.ok(r.success, 'the restore reports success');
  assert.equal(r.error, undefined);

  const placed = r.state.buildings.filter((b) => b.builtTick !== undefined && b.spec === 'land_airport');
  assert.equal(
    placed.length,
    1,
    'F1 FIXED: a bare, pre-existing journal entry with no confirmAffordability field now replays into a real building — the reducer is pure again and always places'
  );
  assert.equal(
    'affordabilityNotice' in r.state,
    false,
    'F2 FIXED: SimState carries no affordabilityNotice field at all any more — the gate is dispatch-side only, nothing to serialise'
  );
  // F4 crossover: since this tail-created airport predates the build (the
  // snapshot's own economyEpoch was 0), the post-tail catch-up stamp
  // (replay.ts's restoreFromSavepoint) grandfathers it too — see A3 below.
  const tailAirport = placed[0];
  assert.equal(tailAirport.jobsOverride, 0, 'the tail-created airport is grandfathered exactly like a snapshot one (F4 fix)');
});

test('A2b (F1 FIX-PROOF): tail-journalled placements are NEVER dropped regardless of whether they would trip the affordability gate — including specs that genuinely DO still trip it post-F3-fix', () => {
  let city = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) city = reducer(city, { type: 'tick' });
  const gross = grossInflowOf(city);
  const tripped = Object.entries(SPECS)
    .filter(([, sp]) => placementAffordability(city, sp).exceedsThreshold)
    .map(([id]) => id);
  assert.ok(tripped.length > 0, 'setup sanity: the gate must still trip on something genuinely disproportionate');
  // F3 FIX-PROOF: hea_teaching no longer trips the gate on this city, now
  // that its job count is read via jobsAtTier() (1,450) instead of
  // capacityAtTier(sp,0) (its unrelated 120,000-served ladder value) — see
  // A5 below for the direct costing proof. land_airport and the two new
  // employment towers (genuinely enormous single-building payrolls) still do.
  assert.ok(!tripped.includes('hea_teaching'), 'F3 FIXED: a Teaching Hospital no longer falsely trips the gate');
  assert.ok(tripped.includes('land_airport'), 'sanity: a genuinely disproportionate employer still trips it');
  for (const id of tripped) {
    const a = placementAffordability(city, SPECS[id]);
    assert.ok(
      a.marginalWagePerTick > gross * PLACEMENT_AFFORDABILITY_WAGE_FRACTION,
      `${id}: characterisation of the threshold arithmetic`
    );
    // The point of F1's fix: even for a spec that DOES trip the gate, a
    // bare pre-existing journal entry for it still replays successfully —
    // the gate is UI-side ADVICE at dispatch time, never a replay-time block.
    const spot = findClearSpot(city, SPECS[id]);
    const before = city.buildings.length;
    const after = reducer(city, { type: 'place', spec: id, x: spot.x, y: spot.y });
    assert.ok(after.buildings.length > before, `${id}: the reducer places it even though it would have tripped the old gate`);
  }
});

test('A2c (F1): a tail entry carrying confirmAffordability:true replays fine — proving the drop is caused ONLY by the missing field on pre-existing journals', () => {
  const storage = makeStorage();
  let snap = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) snap = reducer(snap, { type: 'tick' });
  const spot = findClearSpot(snap, SPECS.land_airport);
  const tail = [{ tick: snap.tick, action: { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y, confirmAffordability: true } }];
  assert.ok(persistSavepoint(storage, createSavepoint({ ...snap, economyEpoch: 0 }, tail, new Date(), 'v-old', null)));
  const r = restoreFromSavepoint(storage);
  assert.ok(r.success);
  const placed = r.state.buildings.filter((b) => b.builtTick !== undefined && b.spec === 'land_airport');
  assert.equal(placed.length, 1, 'the SAME action with the new field replays correctly — the field, not the geometry, is the difference');
});

// ══════════════════════════════════════════════════════════════════════════
// A3 — F4: the tail-journal seam. A tail-placed airport is UNGRANDFATHERED.
// ══════════════════════════════════════════════════════════════════════════

test('A3 (F4 FIX-PROOF): an airport that lives in the TAIL rather than the snapshot is now ALSO grandfathered — stampJobsGrandfather runs AFTER the tail replays (restoreFromSavepoint\'s post-tail catch-up pass), so it can reach a tail-created building too', () => {
  const storage = makeStorage();
  let snap = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) snap = reducer(snap, { type: 'tick' });
  const spot = findClearSpot(snap, SPECS.land_airport);
  // No confirmAffordability field needed any more (F1 fix removed it from
  // the Action type entirely) — a bare, historically-accurate journal entry.
  const tail = [{ tick: snap.tick, action: { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y } }];
  assert.ok(persistSavepoint(storage, createSavepoint({ ...snap, economyEpoch: 0 }, tail, new Date(), 'v-old', null)));
  const r = restoreFromSavepoint(storage);
  const tailAirport = r.state.buildings.find((b) => b.spec === 'land_airport' && b.builtTick !== undefined);
  const snapAirport = r.state.buildings.find((b) => b.spec === 'land_airport' && b.builtTick === undefined);
  assert.ok(tailAirport, 'the tail-created airport must actually exist (F1 fixed)');
  assert.equal(snapAirport.jobsOverride, 0, 'the SNAPSHOT airport is grandfathered');
  assert.equal(
    tailAirport.jobsOverride,
    0,
    'F4 FIXED: the TAIL airport is ALSO grandfathered — the entire snapshot+tail predates this build (economyEpoch:0 on the snapshot), so a POST-tail stamp correctly reaches a building the tail itself created'
  );
  // It never charges a wage bill the player never paid for.
  const online = { ...r.state, buildings: r.state.buildings.map((b) => (b.id === tailAirport.id ? { ...b, builtTick: undefined } : b)) };
  assert.equal(totalJobs(online), 0, 'the tail airport contributes zero jobs — no never-paid-for wage bill');
});

// ══════════════════════════════════════════════════════════════════════════
// A4 — F2 (BLOCKING): the gate has no UI. It is a silent, unrecoverable no-op.
// ══════════════════════════════════════════════════════════════════════════

test('A4a (F2 FIX-PROOF, was BLOCKING): the REDUCER always places, charges, and never touches placeNotice for affordability reasons — the gate moved entirely to the UI dispatch site, so it cannot silently swallow a placement any more', () => {
  let city = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) city = reducer(city, { type: 'tick' });
  const before = city.buildings.length;
  const beforeFunds = city.funds;
  const spot = findClearSpot(city, SPECS.land_airport);
  // This is exactly the placement that USED to trip the reducer-side gate —
  // the reducer now has no concept of affordability at all.
  const after = reducer(city, { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y });

  assert.ok(after.buildings.length > before, 'F2 FIXED: the reducer places the building unconditionally');
  assert.ok(after.funds < beforeFunds, 'F2 FIXED: the reducer charges for it unconditionally');
  assert.equal(
    'affordabilityNotice' in after,
    false,
    'F2 FIXED: there is no affordabilityNotice field on SimState at all any more — nothing for a missing UI reader to fail to render'
  );

  // The UI-side confirmation surface (data.ts placementAffordability(),
  // called from MapView.tsx BEFORE dispatch — never from the reducer) still
  // produces the same real-cost message, just at the correct layer.
  const afford = placementAffordability(city, SPECS.land_airport);
  assert.ok(afford.exceedsThreshold, 'setup: this placement genuinely is disproportionate');
  assert.match(afford.message, /Build anyway\?$/, 'the UI-facing message names the real recurring cost, same copy as before, now surfaced at the dispatch site (AffordabilityConfirm.tsx)');
});

test('A4b (F2 FIX-PROOF): there is nothing left to go stale — SimState carries no affordability field to survive ticks/tool-changes or serialise into a save; placementAffordability() itself stays a pure, re-computed-on-demand read-out', () => {
  let city = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) city = reducer(city, { type: 'tick' });
  const spot = findClearSpot(city, SPECS.land_airport);
  let s = reducer(city, { type: 'place', spec: 'land_airport', x: spot.x, y: spot.y });
  assert.equal('affordabilityNotice' in s, false, 'no notice field exists on the post-place state');

  s = reducer(s, { type: 'tool', tool: 'select' });
  s = reducer(s, { type: 'tick' });
  assert.equal('affordabilityNotice' in s, false, 'still no such field after further actions — nothing COULD go stale');

  // It never persists into a save file either.
  const storage = makeStorage();
  assert.ok(persistSavepoint(storage, createSavepoint(s, [], new Date(), 'v', null)));
  const r = restoreFromSavepoint(storage);
  assert.equal('affordabilityNotice' in r.state, false, 'F2 FIXED: nothing serialises — the save file carries no stale confirmation state');
});

// ══════════════════════════════════════════════════════════════════════════
// A5 — F3: placementAffordability bypasses the estate's own jobsAtTier SSOT.
// ══════════════════════════════════════════════════════════════════════════

test('A5 (F3 FIX-PROOF): placementAffordability now costs hea_teaching at its REAL job count (1,450) via jobsAtTier(), not its unrelated SERVED tier value (120,000) — the quote matches the estate\'s own wage SSOT exactly', () => {
  const sp = SPECS.hea_teaching;
  assert.equal(sp.jobs, 1450, 'catalogue: the teaching hospital employs 1,450');
  assert.equal(capacityAtTier(sp, 0), 120_000, 'sanity: its capacityTiers ladder really is sized for `served`, not jobs — the trap is still there for a naive reader');

  // An empty city with a big workforce isolates the arithmetic.
  const s = { ...initialState(), buildings: [], population: 60_000, lastFlows: { inflows: [{ label: 'x', value: 100_000 }], outflows: [] } };
  const a = placementAffordability(s, sp);

  // What the estate's own SSOT says the building is worth:
  const asCoded = totalJobsBySector({ ...s, buildings: [{ id: 1, spec: 'hea_teaching', x: 1, y: 1 }] });
  const truthful = sectorWagesPerTick(filledJobsBySector({ ...s, buildings: [{ id: 1, spec: 'hea_teaching', x: 1, y: 1 }] })).totalPerTick;

  assert.equal(asCoded.public, 1450, 'totalJobsBySector — the real wage SSOT — correctly says 1,450');
  assert.equal(
    a.marginalWagePerTick,
    truthful,
    `F3 FIXED: the gate now quotes exactly £${truthful}/tick, matching totalJobsBySector/sectorWagesPerTick — no more 82.8x overstatement`
  );
});

test('A5b (F3 FIX-PROOF): the gate\'s DECISION, not just its message, now matches the truth — a false block is no longer reachable at this income', () => {
  const sp = SPECS.hea_teaching;
  const s = { ...initialState(), buildings: [], population: 60_000, lastFlows: { inflows: [{ label: 'x', value: 700_000 }], outflows: [] } };
  const a = placementAffordability(s, sp);
  const truthful = sectorWagesPerTick(filledJobsBySector({ ...s, buildings: [{ id: 1, spec: 'hea_teaching', x: 1, y: 1 }] })).totalPerTick;
  const threshold = 700_000 * PLACEMENT_AFFORDABILITY_WAGE_FRACTION;
  assert.ok(truthful < threshold, 'the TRUE marginal wage is comfortably affordable at this income');
  assert.equal(a.marginalWagePerTick, truthful, 'F3 FIXED: the quoted figure IS the truthful figure, not an inflated one');
  assert.equal(a.exceedsThreshold, false, 'F3 FIXED: no false block — the gate correctly does not trip here');
});

// ══════════════════════════════════════════════════════════════════════════
// A6 — jobsOverride:0 coherence sweep (GR#3).
// ══════════════════════════════════════════════════════════════════════════

test('A6a: jobsOverride:0 is honoured by BOTH wage/employment consumers and by nothing else — a grandfathered airport is jobless for money but full-size for traffic', () => {
  let s = { ...initialState(), buildings: [], population: 60_000, economyEpoch: 0 };
  s = inject(s, 'land_airport', 300, 5);
  const g = stampJobsGrandfather(s);

  // Money/employment side: correctly zero.
  assert.equal(totalJobs(g), 0);
  assert.equal(totalJobsBySector(g).tertiary, 0);
  assert.equal(sectorWagesPerTick(filledJobsBySector(g)).totalPerTick, 0);

  // Traffic side: the SPEC is read, not the building — the grandfathered
  // airport still generates 76,000 jobs' worth of road/rail traffic.
  assert.equal(
    feederTrafficWeight(SPECS.land_airport),
    76_000,
    'FINDING F5: feederTrafficWeight() is spec-level and cannot see jobsOverride — a building that employs nobody still loads the road network as a 76,000-job employer'
  );
});

test('A6b: fittingTier is spec-level and PLACEMENT-TIME only — loading an old save never retroactively rewrites a road (the pin the r1 round asked for)', () => {
  // The jobs fields DID move fittingTier for four specs (r1 finding, pinned in
  // the author's own attack file). What matters here is that nothing on the
  // LOAD path re-runs it.
  assert.equal(fittingTier(SPECS.land_stadium), 5, 'characterisation: stadium now fits a motorway');
  assert.equal(fittingTier(SPECS.land_tunnel), 5);
  assert.equal(fittingTier(SPECS.hea_teaching), 5);
  assert.equal(fittingTier(SPECS.station_ashford), 4);

  // Load an old save containing all four and confirm no road spec changed.
  const storage = makeStorage();
  let old = { ...initialState(), economyEpoch: 0, population: 60_000 };
  const roadsBefore = old.buildings.filter((b) => SPECS[b.spec]?.roadTier).map((b) => `${b.id}:${b.spec}`).sort();
  for (const spec of ['land_stadium', 'land_tunnel', 'hea_teaching', 'station_ashford']) old = inject(old, spec, 300, 5);
  assert.ok(persistSavepoint(storage, createSavepoint(old, [], new Date(), 'v-old', null)));
  const r = restoreFromSavepoint(storage);
  const roadsAfter = r.state.buildings.filter((b) => SPECS[b.spec]?.roadTier).map((b) => `${b.id}:${b.spec}`).sort();
  assert.deepEqual(roadsAfter, roadsBefore, 'no road tile is re-specced by the load path — fittingTier stays placement-time-only');
});

// ══════════════════════════════════════════════════════════════════════════
// A7 — CONSERVATION + the r1 city, re-run independently.
// ══════════════════════════════════════════════════════════════════════════

test('A7a: the r1 city (60k pop, £30m, one already-owned airport) is SOLVENT for 400 ticks under this build — the r1 defect is genuinely fixed', () => {
  const loaded = stampJobsGrandfather(r1City());
  assert.equal(totalJobs(loaded), 0, 'the grandfathered airport employs nobody');
  let run = loaded;
  let insolventAt = null;
  for (let t = 0; t < 400; t++) {
    run = reducer(run, { type: 'tick' });
    if (run.funds <= 0 && insolventAt === null) insolventAt = t + 1;
  }
  assert.equal(insolventAt, null, 'the r1 city must never go insolvent (it was insolvent at tick 9 pre-grandfathering)');
  assert.ok(run.funds > 30_000_000, `funds must GROW from £30m (measured £${run.funds})`);
});

test('A7b: a NEW city that places an airport WITH confirmation gets the honest, disclosed bankruptcy — the confirmation is the only thing standing between the player and the r1 outcome', () => {
  let s = { ...initialState(), buildings: [], population: 60_000, funds: 5_000_000_000, unlockedAll: true };
  for (let i = 0; i < 200; i++) s = inject(s, 'res_estate', 100 + (i % 60) * 5, 5 + Math.floor(i / 60) * 4);
  s = inject(s, 'land_airport', 300, 5); // "confirmed and built" — full jobs
  assert.equal(totalJobs(s), 76_000);
  let run = { ...s, funds: 30_000_000 };
  let insolventAt = null;
  for (let t = 0; t < 60 && insolventAt === null; t++) {
    run = reducer(run, { type: 'tick' });
    if (run.funds <= 0) insolventAt = t + 1;
  }
  assert.ok(insolventAt !== null && insolventAt <= 30, `a confirmed airport still bankrupts a £30m city fast (tick ${insolventAt}) — disclosed, but the disclosure has no UI (F2)`);
});

test('A7c: conservation holds across the grandfathered/ungrandfathered mix — sum(totalJobsBySector) === totalJobs for every combination', () => {
  for (const population of [0, 1, 60_000, 1_000_000]) {
    let s = { ...initialState(), buildings: [], population, economyEpoch: 0 };
    JOBS_GRANDFATHERED_SPECS.forEach((spec, i) => { s = inject(s, spec, 1, i * 2); });
    s = stampJobsGrandfather(s);
    // add a fresh (ungrandfathered) one of each on top
    JOBS_GRANDFATHERED_SPECS.forEach((spec, i) => { s = inject(s, spec, 200, i * 2); });

    const total = totalJobs(s);
    const bySector = totalJobsBySector(s);
    const sum = ['primary', 'secondary', 'tertiary', 'public'].reduce((a, k) => a + bySector[k], 0);
    assert.equal(sum, total, `pop=${population}: sector sum must equal totalJobs exactly`);

    const filled = filledJobsBySector(s);
    for (const sector of ['primary', 'secondary', 'tertiary', 'public']) {
      assert.ok(filled[sector] <= bySector[sector], `pop=${population}/${sector}: filled can never exceed capacity`);
    }
    // Exactly HALF the grandfathered specs' jobs are counted (the fresh copies).
    const expected = JOBS_GRANDFATHERED_SPECS.reduce((a, id) => a + SPECS[id].jobs, 0);
    assert.equal(total, expected, `pop=${population}: only the ungrandfathered half contributes`);
  }
});

test('A7d: determinism — stampJobsGrandfather is a pure function of its input, byte-identical across repeated and interleaved application', () => {
  const s = r1City();
  const a = stampJobsGrandfather(s);
  const b = stampJobsGrandfather(s);
  assert.deepEqual(a, b, 'same input, same output');
  assert.deepEqual(stampJobsGrandfather(a), a, 'idempotent');
  assert.equal(stampJobsGrandfather(a), a, 'and returns BY REFERENCE once current (no wasted allocation)');
});

// ══════════════════════════════════════════════════════════════════════════
// A8 — genesis replay / hard-reset-replay: the DEFAULT no-grandfather claim.
// ══════════════════════════════════════════════════════════════════════════

test('A8a: replayFromGenesis DEFAULT does not grandfather (rebuild-under-new-rules), and startEconomyEpoch:0 does — both as documented', () => {
  const journal = { entries: [] };
  const dflt = replayFromGenesis(journal);
  assert.equal(dflt.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'default replay lands on the current epoch');

  const old = replayFromGenesis(journal, { startEconomyEpoch: 0 });
  assert.equal(old.economyEpoch, JOBS_GRANDFATHER_ECONOMY_EPOCH, 'an explicit old-epoch replay is stamped up to current at the end');
});

test('A8b: replayFromGenesis({startEconomyEpoch:0}) actually grandfathers the buildings the journal placed — the escape hatch is real, not decorative', () => {
  // A journal that places a stadium (small enough not to trip the gate at
  // genesis-time income, so the F1 mechanism does not confound this test).
  const spot = findClearSpot(initialState(), SPECS.land_stadium);
  const journal = {
    entries: [
      { tick: 0, action: { type: 'unlockAll' } },
      { tick: 0, action: { type: 'debugFunds', amount: 5_000_000_000 } },
      { tick: 0, action: { type: 'place', spec: 'land_stadium', x: spot.x, y: spot.y } },
    ],
  };
  const rebuilt = replayFromGenesis(journal);
  const asNew = rebuilt.buildings.find((b) => b.spec === 'land_stadium');
  const grandfathered = replayFromGenesis(journal, { startEconomyEpoch: 0 });
  const asOld = grandfathered.buildings.find((b) => b.spec === 'land_stadium');
  if (asNew && asOld) {
    assert.equal(asNew.jobsOverride, undefined, 'default: full jobs (rebuild under new rules)');
    assert.equal(asOld.jobsOverride, 0, 'startEconomyEpoch:0: grandfathered');
  } else {
    assert.fail('setup: the stadium placement must survive the replay (if it did not, F1 has swallowed it — see A2a)');
  }
});

test('A8c (F1 crossover, FIX-PROOF): the reducer places even a spec whose marginal wage would trip the affordability gate — on ANY replay path (savepoint tail, chunked tail, or genesis rebuild), because the reducer itself no longer knows the gate exists', () => {
  // Build a city whose own income makes a later placement trip the gate.
  let s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000, population: 60_000 };
  for (let i = 0; i < 200; i++) s = inject(s, 'res_estate', 100 + (i % 60) * 5, 5 + Math.floor(i / 60) * 4);
  for (let i = 0; i < 40; i++) s = reducer(s, { type: 'tick' });
  const gross = grossInflowOf(s);
  const trips = Object.entries(SPECS).filter(([, sp]) => placementAffordability(s, sp).exceedsThreshold);
  assert.ok(trips.length > 0, `setup: on a city with £${gross}/tick of income, something must still be genuinely disproportionate`);
  // Direct proof on the reducer, which is the exact function replayFromGenesis
  // and replayTailChunked both drive:
  const [tripId] = trips[0];
  const spot = findClearSpot(s, SPECS[tripId]);
  const placed = reducer(s, { type: 'place', spec: tripId, x: spot.x, y: spot.y });
  assert.ok(
    placed.buildings.length > s.buildings.length,
    `F1 FIXED: replaying a bare 'place ${tripId}' entry produces a real building on ANY replay path — the reducer has no affordability concept left to gate on`
  );
});
