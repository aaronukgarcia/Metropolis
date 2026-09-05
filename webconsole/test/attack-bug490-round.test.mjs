// attack-bug490-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) against
// the BUG-490 estate ("silent place-click failures now surface via
// placeNotice": engine.ts reduceCore's 'place' case gained placeNotice on the
// locked-spec, out-of-bounds and occupied-tile branches).
//
// The attacker is NOT the author. Every test below was written to try to BREAK
// the fix, not to confirm it. Where a test documents a PRE-EXISTING (inherited)
// behaviour rather than a defect introduced by this estate, it says so in its
// own name/comment and asserts the inherited shape explicitly so a future
// change to it reddens here.
//
// Attack surfaces covered (round brief):
//   1. REPLAY/DETERMINISM — a 'place' is journaled; does the new
//      placeNotice-bearing state diverge live-vs-replay? (A1/A2/A3)
//   2. Rejection PURITY — a rejected place must not charge, not build, not
//      award XP, not log a ledger entry, not mutate its input state. (B1..B4)
//   3. THE REPORTED REPRO end-to-end — occupied → valid → occupied sequences
//      never leave a stale or wrong notice. (C1..C4)
//   4. OUT-OF-BOUNDS — the third new branch, which the author's own test file
//      does NOT cover at all. (D1/D2)
//   5. NEWSFEED absorption + StrictMode double-observe dedup for all three new
//      notice strings, and their severity classification. (E1..E4)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction, isStateAffecting } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic, stableStringify } from '../src/sim/genesisReplay.ts';
import { SPECS, MAP_W, MAP_H } from '../src/sim/data.ts';
import {
  observeNews,
  createNewsFeedTracker,
  createNewsFeedSeq,
  placeNoticeSeverity,
} from '../src/sim/newsFeed.ts';

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------
function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    if (isStateAffecting(action)) journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

// roadConnectivity is the sanctioned live-vs-replay exclusion (see
// attack-bug606-replay.test.mjs's own use of the same idiom).
const canon = (s) => stableStringify({ ...s, roadConnectivity: null });

const OPEN_X = 5;
const OPEN_Y = 5;
const OCC = 'road'; // cheap, unlocked, always affordable, 1x1

const lockedSpec = () => Object.values(SPECS).find((sp) => !sp.placeholder && sp.unlock > 0);

// ---------------------------------------------------------------------------
// A. REPLAY / DETERMINISM
// ---------------------------------------------------------------------------

test('A1: a journal containing REJECTED places (all three new branches) replays byte-identically to live', () => {
  const locked = lockedSpec();
  const { journal, liveState } = driveAndRecord([
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }, // succeeds → occupies
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }, // REJECT: occupied
    { type: 'place', spec: locked.id, x: 20, y: 20 }, // REJECT: locked
    { type: 'place', spec: OCC, x: -3, y: 20 }, // REJECT: out of bounds
    { type: 'tick' },
    { type: 'place', spec: OCC, x: 30, y: 30 }, // succeeds → clears notice
    { type: 'tick' },
  ]);
  // Every rejected place must still be JOURNALED (it is state-affecting by
  // type, and the notice IS a state change now) — otherwise replay could not
  // reproduce the notice at all.
  assert.equal(
    journal.entries.filter((e) => e.action.type === 'place').length,
    5,
    'all five place actions — successes AND rejections — are journaled'
  );
  const replayed = replayFromGenesis(journal);
  assert.equal(canon(replayed), canon(liveState), 'live vs genesis-replay must be byte-identical');
  assert.ok(replayIsDeterministic(journal), 'the same journal replayed twice must be byte-identical');
});

test('A2: a journal ENDING on a rejection (notice still set) replays byte-identically, notice included', () => {
  const { journal, liveState } = driveAndRecord([
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y },
    { type: 'tick' },
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }, // REJECT: occupied — LAST action
  ]);
  assert.match(liveState.placeNotice ?? '', /occupied/i, 'precondition: live ends with the notice set');
  const replayed = replayFromGenesis(journal);
  assert.equal(replayed.placeNotice, liveState.placeNotice, 'the notice itself replays identically');
  assert.equal(canon(replayed), canon(liveState));
});

test('A3 (INHERITED, pre-dates this estate): dismissPlaceNotice is NOT journaled, so dismissing a notice DIVERGES live-vs-replay — this estate WIDENS an existing hole, it does not open one', () => {
  // The non-journaled dismiss is the only way placeNotice can differ between
  // live and replay. Prove it BOTH with a pre-existing notice source
  // (insufficient funds, BUG-396 — shipped long before this estate) and with a
  // new one (occupied), so the classification "inherited, not caused here" is
  // evidence-backed rather than asserted.
  // (i) PRE-EXISTING source: insufficient funds. Strip the treasury to zero
  // first (road costs > 0, so the funds gate must bite).
  const broke = driveAndRecord([
    { type: 'debugFunds', amount: -initialState().funds },
    { type: 'place', spec: OCC, x: 40, y: 40 }, // REJECT: insufficient funds (pre-existing notice)
  ]);
  assert.match(broke.liveState.placeNotice ?? '', /insufficient/i, 'precondition: pre-existing funds notice fired');
  const brokeDismissed = reducer(broke.liveState, { type: 'dismissPlaceNotice' });
  assert.equal(brokeDismissed.placeNotice, null);
  assert.notEqual(
    canon(replayFromGenesis(broke.journal)),
    canon(brokeDismissed),
    'INHERITED: dismissing the pre-existing funds notice already diverged live-vs-replay before BUG-490'
  );

  // (ii) NEW source: occupied tile — identical mechanism, identical outcome.
  const occ = driveAndRecord([
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y },
    { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y },
  ]);
  const occDismissed = reducer(occ.liveState, { type: 'dismissPlaceNotice' });
  assert.notEqual(canon(replayFromGenesis(occ.journal)), canon(occDismissed));

  // The divergence is CONFINED to placeNotice — nothing else drifts. That is
  // what keeps this a cosmetic/UI divergence rather than a simulation one.
  const replayed = replayFromGenesis(occ.journal);
  assert.equal(
    canon({ ...replayed, placeNotice: null }),
    canon({ ...occDismissed, placeNotice: null }),
    'with placeNotice normalised away, live and replay are byte-identical — the divergence is UI-only'
  );
});

test('A4: the rejection branches are pure functions of their input — repeated application is identical and the input state is never mutated', () => {
  const base = reducer(initialState(), { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  const snapshot = stableStringify(base);
  const a = reducer(base, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  const b = reducer(base, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  assert.equal(stableStringify(a), stableStringify(b));
  assert.equal(stableStringify(base), snapshot, 'GR#21: the input state object must not be mutated in place');
  assert.notEqual(a, base, 'a rejection returns a NEW object (the notice IS a state change)');
});

// ---------------------------------------------------------------------------
// B. REJECTION PURITY — a rejection must change NOTHING but the notice
// ---------------------------------------------------------------------------

const purityCases = [
  {
    name: 'occupied tile',
    setup: () => reducer(initialState(), { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }),
    action: { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y },
    expect: /occupied/i,
  },
  {
    name: 'level-locked spec',
    setup: () => ({ ...initialState(), unlockedAll: false }),
    action: () => ({ type: 'place', spec: lockedSpec().id, x: 20, y: 20 }),
    expect: /lock/i,
  },
  {
    name: 'out of bounds (negative x)',
    setup: () => initialState(),
    action: { type: 'place', spec: OCC, x: -1, y: 20 },
    expect: /bounds/i,
  },
  {
    name: 'out of bounds (right edge overflow)',
    setup: () => initialState(),
    action: { type: 'place', spec: OCC, x: MAP_W, y: 20 },
    expect: /bounds/i,
  },
  {
    name: 'out of bounds (bottom edge overflow)',
    setup: () => initialState(),
    action: { type: 'place', spec: OCC, x: 20, y: MAP_H },
    expect: /bounds/i,
  },
];

for (const c of purityCases) {
  test(`B: rejection "${c.name}" sets a notice and changes NOTHING else (no charge, no build, no XP, no ledger entry)`, () => {
    const before = { ...c.setup(), placeNotice: null };
    const act = typeof c.action === 'function' ? c.action() : c.action;
    const after = reducer(before, act);

    assert.ok(after.placeNotice, 'the whole point of BUG-490: the failure must be observable');
    assert.match(after.placeNotice, c.expect, 'the notice names the actual reason');

    assert.equal(after.funds, before.funds, 'a rejected placement must never charge the player');
    assert.equal(after.buildings.length, before.buildings.length, 'nothing is built');
    assert.equal(after.ledger.length, before.ledger.length, 'no ledger entry is logged for a rejection');
    assert.equal(after.nextLedgerId, before.nextLedgerId, 'the ledger id cursor does not advance');
    assert.equal(after.xp ?? null, before.xp ?? null, 'no XP is awarded');
    assert.equal(after.level ?? null, before.level ?? null, 'no level change');
    assert.equal(after.tick, before.tick, 'a rejection never advances the clock');

    // Nothing at all changed except placeNotice.
    assert.equal(
      canon({ ...after, placeNotice: null }),
      canon({ ...before, placeNotice: null }),
      'with the notice normalised away the state is byte-identical to before the rejected click'
    );
  });
}

// ---------------------------------------------------------------------------
// C. THE REPORTED REPRO, END TO END (the MapView single-click dispatch shape)
// ---------------------------------------------------------------------------

test('C1: rapid occupied → valid → occupied click sequence never leaves a stale or wrong notice', () => {
  let s = initialState();
  s = reducer(s, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }); // occupy
  assert.equal(s.placeNotice, null, 'a successful place leaves no notice');

  s = reducer(s, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y }); // occupied → notice
  assert.match(s.placeNotice, /occupied/i);

  s = reducer(s, { type: 'place', spec: OCC, x: 60, y: 60 }); // valid → clears
  assert.equal(s.placeNotice, null, 'a successful placement CLEARS the previous rejection notice');

  s = reducer(s, { type: 'place', spec: OCC, x: 60, y: 60 }); // occupied again
  assert.match(s.placeNotice, /occupied/i, 'the notice comes back for a genuinely new rejection');

  s = reducer(s, { type: 'place', spec: OCC, x: -5, y: 60 }); // out of bounds → different reason
  assert.match(s.placeNotice, /bounds/i, 'the notice tracks the LATEST reason, never a stale one');

  s = reducer(s, { type: 'dismissPlaceNotice' });
  assert.equal(s.placeNotice, null, 'dismissing sticks');
});

test('C2: a rejection notice SURVIVES a tick (it is not silently eaten by advance())', () => {
  let s = reducer(initialState(), { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  s = reducer(s, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  const notice = s.placeNotice;
  assert.ok(notice);
  s = reducer(s, { type: 'tick' });
  assert.equal(s.placeNotice, notice, 'the player has at least a full tick to read it');
});

test('C3: dismissing a rejection notice STICKS across subsequent ticks (BUG-677 class — no re-stamping)', () => {
  let s = reducer(initialState(), { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  s = reducer(s, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  s = reducer(s, { type: 'dismissPlaceNotice' });
  for (let i = 0; i < 10; i++) s = reducer(s, { type: 'tick' });
  assert.equal(s.placeNotice, null, 'a dismissed rejection must never resurrect on a tick');
});

test('C4 (BUG-677 class): a worker-tick hydrate does not re-fire or resurrect a dismissed rejection notice', () => {
  let s = reducer(initialState(), { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  s = reducer(s, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  const dismissed = reducer(s, { type: 'dismissPlaceNotice' });
  // store.tsx delivers each worker-advanced tick as hydrate(source:'tick').
  const advanced = reducer(dismissed, { type: 'tick' });
  const hydrated = reducer(dismissed, { type: 'hydrate', state: advanced, source: 'tick' });
  assert.equal(hydrated.placeNotice, null, 'a tick-sourced hydrate must not re-stamp a place notice');
  // And a hydrate carrying a notice does not duplicate/append it either.
  const withNotice = reducer(hydrated, { type: 'place', spec: OCC, x: OPEN_X, y: OPEN_Y });
  const rehydrated = reducer(hydrated, { type: 'hydrate', state: withNotice, source: 'tick' });
  assert.equal(rehydrated.placeNotice, withNotice.placeNotice, 'exactly one notice, not a concatenation');
});

test('C5: a MULTI-TILE spec overlapping an existing building at an OFFSET (not the same origin tile) is also reported, not silent', () => {
  const multi = Object.values(SPECS).find(
    (sp) => !sp.placeholder && sp.unlock === 0 && (sp.w > 1 || sp.h > 1) && sp.maxPerCity == null
  );
  if (!multi) return; // catalogue has no unlocked multi-tile spec — nothing to attack
  let s = { ...initialState(), funds: 1_000_000_000 };
  s = reducer(s, { type: 'place', spec: OCC, x: 100, y: 100 }); // a 1x1 road at 100,100
  assert.equal(s.placeNotice, null);
  // Place the multi-tile spec so its FOOTPRINT covers 100,100 but its origin does not.
  const ox = 100 - (multi.w - 1);
  const oy = 100 - (multi.h - 1);
  const after = reducer({ ...s, placeNotice: null }, { type: 'place', spec: multi.id, x: ox, y: oy });
  if (after.buildings.length === s.buildings.length) {
    assert.ok(after.placeNotice, `an overlapping ${multi.id} placement must not be silent`);
  }
});

// ---------------------------------------------------------------------------
// D. OUT-OF-BOUNDS — the branch the author's own test file never touches
// ---------------------------------------------------------------------------

test('D1: every out-of-bounds corner case produces a notice (author test file has ZERO coverage of this branch)', () => {
  const base = { ...initialState(), funds: 1_000_000_000, placeNotice: null };
  const cases = [
    { x: -1, y: 10 },
    { x: 10, y: -1 },
    { x: MAP_W, y: 10 },
    { x: 10, y: MAP_H },
    { x: MAP_W - 1 + 1, y: MAP_H - 1 + 1 },
    { x: -9999, y: -9999 },
    { x: MAP_W + 500, y: MAP_H + 500 },
  ];
  for (const c of cases) {
    const after = reducer(base, { type: 'place', spec: OCC, x: c.x, y: c.y });
    assert.equal(after.buildings.length, base.buildings.length, `nothing placed at ${c.x},${c.y}`);
    assert.ok(after.placeNotice, `out-of-bounds place at ${c.x},${c.y} must NOT be silent`);
    assert.match(after.placeNotice, /bounds/i);
  }
});

test('D2: the LAST legal tile is still placeable — the bounds notice does not over-fire (off-by-one guard)', () => {
  const sp = SPECS[OCC];
  const base = { ...initialState(), funds: 1_000_000_000, placeNotice: null };
  const after = reducer(base, { type: 'place', spec: OCC, x: MAP_W - sp.w, y: MAP_H - sp.h });
  assert.equal(after.buildings.length, base.buildings.length + 1, 'the last in-bounds tile must still build');
  assert.equal(after.placeNotice, null, 'and must not raise a bounds notice');
});

// ---------------------------------------------------------------------------
// E. NEWSFEED ABSORPTION + STRICTMODE DEDUP + SEVERITY
// ---------------------------------------------------------------------------

const NEW_NOTICES = [
  { label: 'occupied', text: "Can't build here — tile already occupied" },
  { label: 'out of bounds', text: "Can't build here — out of bounds" },
  { label: 'locked', text: 'Locked — Test Building not unlocked yet' },
];

for (const n of NEW_NOTICES) {
  test(`E1 (${n.label}): the notice reaches the news feed exactly once, and a StrictMode DOUBLE render does not duplicate it`, () => {
    const tracker = createNewsFeedTracker();
    const seq = createNewsFeedSeq();
    let ring = [];
    const sources = { tick: 12, notice: null, milestoneNotice: null, placeNotice: n.text };
    ring = observeNews(sources, tracker, ring, seq);
    assert.equal(ring.length, 1, 'first observe appends');
    // React StrictMode invokes the render body twice with the SAME state.
    const ring2 = observeNews(sources, tracker, ring, seq);
    assert.equal(ring2.length, 1, 'the double-invoke must NOT append a second entry');
    assert.equal(ring2, ring, 'and must return the same array reference (identity-stable bailout)');
    assert.equal(ring[0].source, 'placeNotice');
    assert.equal(ring[0].text, n.text);
  });
}

test('E2: clear → re-fire of the SAME rejection text appends a genuinely new entry (a second failed click is not swallowed forever)', () => {
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const text = "Can't build here — tile already occupied";
  let ring = observeNews({ tick: 1, notice: null, milestoneNotice: null, placeNotice: text }, tracker, [], seq);
  assert.equal(ring.length, 1);
  // A successful placement clears the notice...
  ring = observeNews({ tick: 2, notice: null, milestoneNotice: null, placeNotice: null }, tracker, ring, seq);
  assert.equal(ring.length, 1);
  // ...and a NEW failed click re-raises it.
  ring = observeNews({ tick: 3, notice: null, milestoneNotice: null, placeNotice: text }, tracker, ring, seq);
  assert.equal(ring.length, 2, 'the second genuine rejection is its own feed entry');
});

test('E3 (KNOWN GAP, documents current behaviour): two CONSECUTIVE rejections with identical text produce only ONE feed entry', () => {
  // Clicking two DIFFERENT occupied tiles yields the identical notice string,
  // so the by-exact-string dedup suppresses the second. The state field is
  // also unchanged, so the left-panel hint does not flicker either — the
  // player gets no NEW signal for the second failed click. Documented, not
  // asserted as correct; if the estate ever disambiguates the text (e.g. by
  // naming the blocking building or the coordinates) this test must be
  // updated deliberately.
  const tracker = createNewsFeedTracker();
  const seq = createNewsFeedSeq();
  const text = "Can't build here — tile already occupied";
  let ring = observeNews({ tick: 1, notice: null, milestoneNotice: null, placeNotice: text }, tracker, [], seq);
  ring = observeNews({ tick: 2, notice: null, milestoneNotice: null, placeNotice: text }, tracker, ring, seq);
  assert.equal(ring.length, 1);

  // Reducer-level confirmation of the same shape: two different occupied
  // tiles produce byte-identical notice text.
  let s = { ...initialState(), funds: 1_000_000_000 };
  s = reducer(s, { type: 'place', spec: OCC, x: 70, y: 70 });
  s = reducer(s, { type: 'place', spec: OCC, x: 72, y: 70 });
  const a = reducer(s, { type: 'place', spec: OCC, x: 70, y: 70 });
  const b = reducer(a, { type: 'place', spec: OCC, x: 72, y: 70 });
  assert.equal(a.placeNotice, b.placeNotice, 'the two rejections are indistinguishable to the player');
});

test('E4 (FINDING, severity gap): the two new "Can\'t build here" rejections classify as INFO while their sibling rejections classify as WARNING', () => {
  // Sibling rejections already shipped as warnings...
  assert.equal(placeNoticeSeverity('Insufficient funds — £100 needed'), 'warning');
  assert.equal(placeNoticeSeverity('Locked — Wind Farm not unlocked yet'), 'warning'); // matches /locked/i
  assert.equal(placeNoticeSeverity('No road access'), 'warning');
  // ...but the two new ones fall through to the default.
  assert.equal(
    placeNoticeSeverity("Can't build here — tile already occupied"),
    'info',
    'CURRENT behaviour: the PRIMARY reported repro renders as neutral info, not a warning'
  );
  assert.equal(placeNoticeSeverity("Can't build here — out of bounds"), 'info');
});

// ---------------------------------------------------------------------------
// F. INHERITED FINDING — the in-flight worker-tick reply CLOBBERS any state
//    change made between postMessage and the reply landing.
//
//    store.tsx posts `stateRefForDispatch.current` to the worker and applies
//    the reply wholesale via `dispatch({type:'hydrate', state: msg.state,
//    source:'tick'})`. Only a 'reset' calls invalidateInFlightWorkerTick() —
//    a 'place' does not. decideTickReply's only gate is
//    `resultTick > currentLiveTick`, which a non-tick action never moves.
//
//    Consequence for THIS estate: a rejection notice raised by a click that
//    lands inside the worker round-trip window is silently erased before the
//    player can read it — i.e. the very "silent failure" BUG-490 exists to
//    remove, reintroduced by a race.
//
//    Classified INHERITED, not caused here: the identical window swallows the
//    SUCCESSFUL placement too (asserted below), which is a strictly larger,
//    pre-existing defect that predates this estate and is not fixable inside
//    the 'place' reducer case. Documented here so it cannot be re-lost.
// ---------------------------------------------------------------------------
import {
  initialOffloadControllerState,
  beginTickRequest,
  decideTickReply,
} from '../src/sim/simWorkerOffloadController.ts';

test('F1 (INHERITED, pre-existing): an in-flight worker tick reply erases a rejection notice raised mid-flight — and erases a SUCCESSFUL placement the same way', () => {
  // t0: live state, worker request posted with THIS state.
  const live = { ...initialState(), funds: 1_000_000_000 };
  const posted = live; // store.tsx posts stateRefForDispatch.current verbatim
  const begun = beginTickRequest(initialOffloadControllerState(), live.tick);
  assert.ok(begun, 'precondition: a request was issued');

  // t1: the player clicks an OCCUPIED tile while the worker is crunching.
  const occupied = reducer(live, { type: 'place', spec: OCC, x: 150, y: 150 });
  const rejected = reducer(occupied, { type: 'place', spec: OCC, x: 150, y: 150 });
  assert.match(rejected.placeNotice ?? '', /occupied/i, 'the notice IS raised on the live state');

  // t2: the worker's reply lands. It was computed from `posted` (pre-click).
  const workerResult = reducer(posted, { type: 'tick' });
  const { decision } = decideTickReply(
    begun.state,
    { requestId: begun.requestId, resultTick: workerResult.tick },
    rejected.tick // the click did NOT advance the tick
  );
  assert.equal(decision.kind, 'apply', 'nothing invalidates the reply — a place() does not supersede it');

  // t3: store.tsx applies it wholesale.
  const afterHydrate = reducer(rejected, { type: 'hydrate', state: workerResult, source: 'tick' });
  assert.equal(afterHydrate.placeNotice, null, 'FINDING: the rejection notice is silently erased by the tick reply');
  assert.equal(
    afterHydrate.buildings.length,
    live.buildings.length,
    'INHERITED and larger: the SUCCESSFUL placement made in the same window is lost too — this is not a BUG-490 regression'
  );
});
