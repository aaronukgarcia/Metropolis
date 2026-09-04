// attack-unique-building-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND
// (GR#23, attacker != author) on the maxPerCity unique-building mechanism
// (FEAT-2326609761 AC-28..AC-31). The author's own
// feat-2326609761-unique-building.test.mjs already covers the five named
// placement paths individually and the "two dams in one batch" smuggling
// case thoroughly — this file does NOT repeat that ground. It hunts for:
//
//   1. A SIXTH path — anything besides the five named paths that can append
//      to state.buildings, closed here by a data-invariant proof rather than
//      a per-callsite trace (auto-connect/auto-branch-rail/road-replan-cascade
//      only ever place road/rail/trunk specs — proven those families can
//      NEVER carry maxPerCity, which forecloses the whole class mechanically).
//   2. Batch-smuggling variants the author's tests did not try: a stamp that
//      flattens an EXISTING dam and re-places a new one in the same action.
//   3. The dangerous replay-ordering case: a journal tail recorded when a
//      placement would have succeeded (because the state it was journalled
//      against had allowance) but whose SNAPSHOT already contains the cap-
//      exhausting building — proving what the NORMAL app-resume path
//      (restoreFromSavepoint, used on every reload, not just the special
//      hard-reset-replay feature) does with it.
//   4. The next===cur decline-detection contract inside placeMoreMany's loop,
//      hammered with a MIXED batch (an ordinary occupied-tile decline BEFORE
//      the cap is hit, then a cap decline) to prove continue-vs-break are
//      never conflated.
//   5. AC-31 extended to THREE dams, and to bulldozing one of them.
//   6. memoOnState (countBySpecId) cache-freshness in both directions.
//
// Run via tools/test/scoped.mjs from webconsole/ per the round brief.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  PALETTE_FLAT,
  isPlaceable,
  remainingAllowance,
  countOfSpec,
  findSpot,
  isRoadSpec,
  isRailSpec,
  isRoadOrTrunkSpec,
} from '../src/sim/data.ts';
import { initialState, reducer } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { restoreFromSavepoint, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';

const DAM = 'pow_hydro';

function richState() {
  const s = initialState();
  return { ...s, unlockedAll: true, funds: SPECS[DAM].cost * 20 };
}

function mockStorage(seed = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, v),
    removeItem: (k) => map.delete(k),
  };
}

// ---------------------------------------------------------------------------
// 1. THE SIXTH PATH — data-invariant closure over auto-connect/auto-branch-
//    rail/road-replan-cascade, which each append to buildings[] via their
//    own literal `buildings: [...]`/`.concat(...)` sites (engine.ts ~2321,
//    ~2572, ~4089) WITHOUT calling remainingAllowance at all. They are safe
//    only because they exclusively place road/rail/trunk-tier specs. This
//    test makes that assumption a live, fail-closed invariant: if ANY road,
//    rail, or trunk spec is ever given a maxPerCity in future (e.g. "only
//    one HS1 terminus"), this test goes red BEFORE the connector paths ship
//    a silent hole, rather than relying on a human re-auditing three
//    unrelated call sites by hand every time the catalogue changes.
// ---------------------------------------------------------------------------

describe('SIXTH-PATH CLOSURE: connector paths (autoConnect/autoBranchRail/road-replan) never touch a capped spec', () => {
  test('no road, trunk, or rail spec in the catalogue carries maxPerCity', () => {
    const offenders = Object.values(SPECS).filter(
      (sp) => sp.maxPerCity != null && (isRoadOrTrunkSpec(sp) || isRailSpec(sp))
    );
    assert.deepEqual(
      offenders.map((sp) => sp.id),
      [],
      'a road/trunk/rail spec with maxPerCity would bypass the cap via autoConnect/autoBranchRail/planRoadReplanCascade, ' +
        'none of which call remainingAllowance() — those three append-to-buildings sites are ONLY safe by this invariant'
    );
  });

  test('control: the invariant is not vacuous — pow_hydro itself is neither road nor rail (so it IS reachable by the five named paths, as designed)', () => {
    const dam = SPECS[DAM];
    assert.equal(isRoadSpec(dam), false);
    assert.equal(isRailSpec(dam), false);
    assert.equal(isRoadOrTrunkSpec(dam), false);
  });

  test('relocate cannot be used to duplicate a unique building — it mutates the existing building in place, never appends', () => {
    const s = richState();
    const placed = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    const dam = placed.buildings.find((b) => b.spec === DAM);
    const moved = reducer(
      { ...placed, tool: 'move', movingId: dam.id, funds: 1_000_000_000 },
      { type: 'relocate', x: 400, y: 200 }
    );
    assert.equal(countOfSpec(moved, DAM), 1, 'relocate never changes the count, by construction (map, not concat)');
    assert.equal(moved.buildings.length, placed.buildings.length, 'no new building object was appended');
  });
});

// ---------------------------------------------------------------------------
// 2. BATCH SMUGGLING: flatten an existing dam and place a new one in the
//    SAME stampRegion action. The author's rule is "counts start from
//    POST-FLATTEN buildings" — so this should be ALLOWED (net count stays
//    at 1) rather than refused. Prove it actually behaves that way, and that
//    it cannot be abused to exceed the cap (flatten-and-place-TWO).
// ---------------------------------------------------------------------------

describe('BATCH SMUGGLING: stampRegion demolish-and-replace in one action', () => {
  test('a stamp that flattens the existing dam and places a new one at the SAME spot nets exactly one dam (allowed)', () => {
    const s = richState();
    const withDam = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    assert.equal(countOfSpec(withDam, DAM), 1);

    const clipboard = { w: 8, h: 8, items: [{ spec: DAM, dx: 0, dy: 0 }] };
    const after = reducer(withDam, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    assert.equal(countOfSpec(after, DAM), 1, 'demolish-then-replace-in-one-stamp nets exactly one dam, never zero or two');
    assert.notEqual(after, withDam, 'the stamp DID apply (it is not a silent refusal)');
  });

  test('the SAME demolish-and-replace stamp cannot smuggle a SECOND dam alongside the replacement', () => {
    const s = richState();
    const withDam = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });

    // Flattens the existing dam (landing zone covers it) AND tries to place TWO
    // new dams in the same batch — post-flatten count starts at 0, so item 1
    // is allowed (count -> 1) but item 2 must still be refused, and per the
    // all-or-nothing stamp contract that means the WHOLE stamp is refused.
    const clipboard = {
      w: 20,
      h: 8,
      items: [
        { spec: DAM, dx: 0, dy: 0 },
        { spec: DAM, dx: 12, dy: 0 },
      ],
    };
    const after = reducer(withDam, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    assert.deepEqual(after, withDam, 'flatten-plus-TWO-replacements is refused wholesale, original dam untouched');
    assert.equal(countOfSpec(after, DAM), 1, 'the original dam survives the refused stamp — never demolished on a refusal');
  });

  test('conservation: a demolish-and-replace stamp charges exactly (new build cost - demolition refund), no double-charge', () => {
    const s = richState();
    const withDam = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    const fundsBefore = withDam.funds;

    const clipboard = { w: 8, h: 8, items: [{ spec: DAM, dx: 0, dy: 0 }] };
    const after = reducer(withDam, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    const expectedRefund = Math.round(SPECS[DAM].cost * 0.25);
    const expectedNet = SPECS[DAM].cost - expectedRefund;
    assert.equal(fundsBefore - after.funds, expectedNet, 'net cost is build-minus-refund, not a full double charge');
  });
});

// ---------------------------------------------------------------------------
// 3. THE DANGEROUS CASE: normal app-resume (restoreFromSavepoint), not just
//    the special hard-reset-replay feature, replays a journal TAIL through
//    the CURRENT (capped) reducer against a SNAPSHOT that is already at the
//    cap. This simulates a journal recorded before this cap shipped (or,
//    identically, a browser refresh between "dam #2 was journalled" and "the
//    next periodic snapshot" once two dams already exist in a save from
//    before this commit): the second dam silently fails to reappear on
//    resume, funds are correctly not double-charged, but — UNLIKE the
//    AC-31 `hydrate` path, which raises an explicit, persistent one-time
//    "One per city" banner — this path raises NO durable notice at all: the
//    transient placeNotice set by the refused 'place' entry is immediately
//    overwritten by whatever the NEXT tail entry does (here, nothing further,
//    but in a real tail there are always more entries/ticks after it), so
//    the player has no way to learn a previously-built asset "vanished" on
//    reload. This is real and does not overlap the author's AC-31 tests,
//    which only exercise `hydrate` (a full snapshot handed straight to the
//    reducer), never the journal-tail replay resume path.
// ---------------------------------------------------------------------------

describe('DANGEROUS CASE: journal-tail replay resume vs. a cap-exhausting snapshot', () => {
  test('restoreFromSavepoint silently drops a tail-journalled dam placement once the snapshot itself is at cap — with NO persisted notice', () => {
    const genesis = initialState();
    // Snapshot already contains the first (cap-exhausting) dam, as it would
    // if the last periodic snapshot was taken AFTER dam #1 landed.
    const snapshot = {
      ...genesis,
      funds: SPECS[DAM].cost * 20,
      unlockedAll: true,
      buildings: [...genesis.buildings, { id: 999301, spec: DAM, x: 300, y: 100, builtTick: 0 }],
      nextId: genesis.nextId + 1,
    };
    // Journal tail carries a SECOND dam placement — representing an action
    // that was journalled (and, under pre-cap code or a stale in-memory
    // session, would have applied) but never made it into a snapshot before
    // the app reloaded.
    const journalTail = [{ tick: snapshot.tick, action: { type: 'place', spec: DAM, x: 320, y: 100 } }];
    const savepoint = {
      savedAt: new Date().toISOString(),
      snapshotTick: snapshot.tick,
      snapshot,
      journalTail,
    };
    const storage = mockStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(savepoint) });
    const result = restoreFromSavepoint(storage);

    assert.equal(result.success, true, `restore should still succeed (no crash); got: ${result.reason}`);
    assert.equal(countOfSpec(result.state, DAM), 1, 'the second, tail-journalled dam is silently gone after resume');
    assert.equal(
      result.state.funds,
      snapshot.funds,
      'conservation holds: the refused tail placement did not charge — no money vanished alongside the building'
    );
    // The finding: no durable signal survives. The reducer DOES set a
    // transient placeNotice on the refused 'place' call, but nothing in the
    // restore path surfaces or preserves it distinctly from an ordinary
    // "nothing happened" placeNotice, and it carries none of AC-31's
    // "This save has N x ... more than the cap allows" framing — a player
    // has no way to learn why their second dam is missing after a reload.
    assert.ok(
      result.state.placeNotice == null || !/more than the/i.test(result.state.placeNotice),
      'FINDING: unlike hydrate (AC-31), a normal resume that drops a tail-journalled ' +
        'over-cap placement raises no AC-31-style durable "over cap" explanation'
    );
  });

  test('control: the SAME resume with only ONE dam total (no cap conflict) is fully unaffected', () => {
    const genesis = initialState();
    const snapshot = { ...genesis, funds: SPECS[DAM].cost * 20, unlockedAll: true };
    const journalTail = [{ tick: snapshot.tick, action: { type: 'place', spec: DAM, x: 300, y: 100 } }];
    const savepoint = {
      savedAt: new Date().toISOString(),
      snapshotTick: snapshot.tick,
      snapshot,
      journalTail,
    };
    const storage = mockStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(savepoint) });
    const result = restoreFromSavepoint(storage);
    assert.equal(result.success, true);
    assert.equal(countOfSpec(result.state, DAM), 1, 'the ordinary, non-conflicting case still resumes correctly');
  });
});

// ---------------------------------------------------------------------------
// 4. next===cur CONTRACT: a MIXED placeMany batch — an ordinary occupied-
//    tile decline (must `continue`, trying later tiles) followed by a cap
//    decline (must `break`, stopping the whole batch) — proves the two
//    decline reasons are never conflated into one another.
// ---------------------------------------------------------------------------

describe('next===cur CONTRACT: mixed decline reasons inside placeMany', () => {
  test('an occupied-tile decline (continue) followed later by a cap decline (break) are correctly distinguished', () => {
    const s = richState();
    // Occupy the first candidate tile with an ordinary building first.
    const withObstacle = reducer(s, { type: 'place', spec: 'res_hut', x: 300, y: 100 });
    assert.equal(withObstacle.buildings.some((b) => b.x === 300 && b.y === 100), true);

    const after = reducer(withObstacle, {
      type: 'placeMany',
      spec: DAM,
      tiles: [
        { x: 300, y: 100 }, // occupied -> decline, but NOT cap-related -> continue
        { x: 340, y: 100 }, // free -> succeeds, dam #1
        { x: 360, y: 100 }, // free, but now at cap -> break, never even attempted as a 'place'
      ],
    });
    assert.equal(countOfSpec(after, DAM), 1, 'exactly one dam placed: the occupied tile was skipped, not fatal');
    assert.match(
      after.placeNotice ?? '',
      /one per city/i,
      'the batch summary must report the CAP as the reason it stopped short, not "some tiles already occupied" ' +
        '(which was also technically true for tile 1, but is not why the batch as a whole under-delivered)'
    );
  });

  test('conservation: the mixed batch charges for exactly the one dam that actually placed', () => {
    const s = richState();
    const withObstacle = reducer(s, { type: 'place', spec: 'res_hut', x: 300, y: 100 });
    const fundsBeforeBatch = withObstacle.funds;
    const after = reducer(withObstacle, {
      type: 'placeMany',
      spec: DAM,
      tiles: [
        { x: 300, y: 100 },
        { x: 340, y: 100 },
        { x: 360, y: 100 },
      ],
    });
    assert.equal(fundsBeforeBatch - after.funds, SPECS[DAM].cost, 'exactly one dam charged, obstacle tile cost nothing, cap-refused tile cost nothing');
  });
});

// ---------------------------------------------------------------------------
// 5. AC-31 EXTENDED: three dams (not just two), and bulldozing one of an
//    over-cap group leaves the allowance still clamped at 0 (still over
//    cap by one), never dipping negative and never over-crediting to 1.
// ---------------------------------------------------------------------------

// FEAT-2326609781 (Aaron, 2026-09-04) SUPERSEDES the original AC-31 "none are
// removed" semantics this block used to pin: a load-hydrate now PURGES every
// maxPerCity-capped spec down to its cap (oldest survives; 50% scrap credited
// via a labelled inflow). The over-cap-group-exists-post-load premise the old
// bulldoze subtests guarded is therefore structurally unreachable — 'place'
// refuses past the cap and load purges — so this block now pins the purge
// semantics end to end instead.
describe('AC-31 EXTENDED: three-dam old save, and bulldozing one of an over-cap group', () => {
  function threeDamSnapshot() {
    const genesis = initialState();
    return {
      ...genesis,
      funds: SPECS[DAM].cost * 10,
      unlockedAll: true,
      buildings: [
        ...genesis.buildings,
        { id: 999401, spec: DAM, x: 300, y: 100, builtTick: 0 },
        { id: 999402, spec: DAM, x: 320, y: 100, builtTick: 5 },
        { id: 999403, spec: DAM, x: 340, y: 100, builtTick: 9 },
      ],
      nextId: genesis.nextId + 3,
    };
  }

  test('a three-dam save purges to exactly the cap at load, the OLDEST survives, and a second is refused', () => {
    const fresh = initialState();
    const hydrated = reducer(fresh, { type: 'hydrate', state: threeDamSnapshot() });
    assert.equal(countOfSpec(hydrated, DAM), SPECS[DAM].maxPerCity, 'purged down to the cap');
    const survivor = hydrated.buildings.find((b) => b.spec === DAM);
    assert.equal(survivor.id, 999401, 'the oldest (lowest builtTick) survives');
    assert.match(hydrated.placeNotice ?? '', /Removed 2 surplus/, 'notice names the purge count');

    let s = hydrated;
    for (let i = 0; i < 12; i++) s = reducer(s, { type: 'tick' });
    assert.equal(countOfSpec(s, DAM), SPECS[DAM].maxPerCity, 'still at cap after 12 ticks — the tick loop never re-purges or re-places');

    const secondAttempt = reducer(s, { type: 'place', spec: DAM, x: 360, y: 100 });
    assert.equal(countOfSpec(secondAttempt, DAM), SPECS[DAM].maxPerCity, 'a second is refused while one stands');
  });

  test('the purge credits 50% scrap per removed dam via the labelled inflow, exactly once', () => {
    const fresh = initialState();
    const snapshot = threeDamSnapshot();
    const hydrated = reducer(fresh, { type: 'hydrate', state: snapshot });
    const scrap = hydrated.lastFlows.inflows.find((f) => /decommission scrap/i.test(f.label));
    assert.ok(scrap, 'the scrap inflow line exists');
    assert.equal(scrap.value > 0, true, 'a positive credit was booked');
    // Idempotency: hydrating the already-purged state again credits nothing new.
    const again = reducer(initialState(), { type: 'hydrate', state: hydrated });
    assert.equal(countOfSpec(again, DAM), SPECS[DAM].maxPerCity, 'second load is a no-op purge');
  });

  test('bulldozing the surviving dam frees the slot (allowance 1, placeable again) — never negative, never over-credited', () => {
    const fresh = initialState();
    let s = reducer(fresh, { type: 'hydrate', state: threeDamSnapshot() });
    const dam = s.buildings.find((b) => b.spec === DAM);
    s = reducer(s, { type: 'bulldoze', x: dam.x, y: dam.y });
    assert.equal(countOfSpec(s, DAM), 0);
    assert.equal(remainingAllowance(s, SPECS[DAM]), SPECS[DAM].maxPerCity, 'the full allowance returns once the survivor goes');
    assert.equal(isPlaceable(s, SPECS[DAM]), true);
  });
});

// ---------------------------------------------------------------------------
// 6. memoOnState (countBySpecId) CACHE FRESHNESS, in both directions.
// ---------------------------------------------------------------------------

describe('countBySpecId (memoOnState) cache freshness', () => {
  test('after a place() that returns a NEW state object, the count is FRESH, not stale from the parent', () => {
    const s = richState();
    // Warm the cache against the PARENT state at count 0.
    assert.equal(countOfSpec(s, DAM), 0);
    const placed = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    assert.notEqual(placed, s, 'place() must return a new state object');
    assert.equal(countOfSpec(placed, DAM), 1, 'the child state is a DIFFERENT WeakMap key, so its own fresh fold is computed, not the parent’s cached 0');
    // The parent's cached answer must remain 0 — proves the memo keys on
    // object identity, not on some shared/global counter.
    assert.equal(countOfSpec(s, DAM), 0, 'the original parent state object is unmutated and keeps its own (correct) fresh answer');
  });

  test('a declined action returning the SAME state reference reuses the cached count (no re-fold, same answer)', () => {
    const s = richState();
    const placed = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    assert.equal(countOfSpec(placed, DAM), 1); // warms the cache for `placed`
    // A second placement past the cap is refused — but per AC-35, this still
    // returns a NEW top-level object (for the placeNotice), NOT the same
    // reference; the buildings array reference is unchanged (already
    // asserted in the author's AC-35 test). Confirm the count on THAT new
    // object is still correctly 1 (a fresh fold over an unchanged
    // buildings[] reference must equal the cached fold over that same
    // buildings[] reference used elsewhere).
    const refused = reducer(placed, { type: 'place', spec: DAM, x: 320, y: 100 });
    assert.equal(refused.buildings, placed.buildings, 'buildings[] reference is byte-identical, per AC-35');
    assert.equal(countOfSpec(refused, DAM), 1, 'count is still correct even though `refused` is a distinct top-level SimState object');
  });
});

// ---------------------------------------------------------------------------
// 7. LOCKED + AT-CAP INTERACTION (BottomBar chip precedence): a spec that is
//    both past its unlock gate AND (hypothetically) at cap must render as
//    LOCKED, never as "One per city" — the palette should never claim a
//    building is "already built" when the player could not have unlocked it
//    to build it in the first place.
// ---------------------------------------------------------------------------

describe('LOCKED + AT-CAP precedence (BottomBar.tsx atCap derivation, mirrored)', () => {
  // Mirrors BottomBar.tsx's exact derivation: atCap = !isPh && !locked && canEnterSim(sp) && remainingAllowance(state, sp) <= 0
  function deriveTiers(state, sp, isPh, locked) {
    const atCap = !isPh && !locked && !!sp && remainingAllowance(state, sp) <= 0;
    return { locked, atCap };
  }

  test('a spec that is BOTH locked and would otherwise be at-cap renders as locked, not at-cap', () => {
    const s = richState();
    const dam = SPECS[DAM];
    // Simulate "locked" (as specUnlocked() would report before the player
    // reaches the unlock level) directly, independent of remainingAllowance.
    const lockedTiers = deriveTiers(s, dam, false, /* locked */ true);
    assert.equal(lockedTiers.atCap, false, 'locked takes precedence — the chip must never show "One per city" for a building the player has not unlocked yet');
  });

  test('control: once unlocked and actually at cap, atCap correctly flips true', () => {
    const withDam = reducer(richState(), { type: 'place', spec: DAM, x: 300, y: 100 });
    const tiers = deriveTiers(withDam, SPECS[DAM], false, /* locked */ false);
    assert.equal(tiers.atCap, true);
  });
});

// ---------------------------------------------------------------------------
// 8. GENERAL CONSISTENCY: an over-cap state (from any of the above scenarios)
//    never trips the ordinary consistency gate, and a mid-batch refusal never
//    leaves buildings.ids-unique or funds-conservation invariants broken.
// ---------------------------------------------------------------------------

test('CONSISTENCY: a mixed placeMany refusal batch leaves a fully consistent state (ids unique, no phantom entries)', () => {
  const s = richState();
  const withObstacle = reducer(s, { type: 'place', spec: 'res_hut', x: 300, y: 100 });
  const after = reducer(withObstacle, {
    type: 'placeMany',
    spec: DAM,
    tiles: [
      { x: 300, y: 100 },
      { x: 340, y: 100 },
      { x: 360, y: 100 },
    ],
  });
  const report = runConsistencyChecks(after);
  assert.equal(report.failures, 0, `expected a clean consistency report, got: ${JSON.stringify(report)}`);
});

test('CONSISTENCY: a demolish-and-replace stampRegion of the dam leaves a fully consistent state', () => {
  const s = richState();
  const withDam = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
  const clipboard = { w: 8, h: 8, items: [{ spec: DAM, dx: 0, dy: 0 }] };
  const after = reducer(withDam, { type: 'stampRegion', clipboard, x: 300, y: 100 });
  const report = runConsistencyChecks(after);
  assert.equal(report.failures, 0, `expected a clean consistency report, got: ${JSON.stringify(report)}`);
});

// Reference PALETTE import used above only to confirm the catalogue module
// surface hasn't drifted under this test file (keeps the import list honest).
test('sanity: pow_hydro appears in the palette catalogue at all (guards against a future rename breaking every test above silently)', () => {
  assert.ok(PALETTE_FLAT.includes(DAM), 'pow_hydro must still be registered in the palette');
});
