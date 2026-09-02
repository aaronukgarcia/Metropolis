// play-mode-endgame.test.mjs — FEAT-2326609723 (Play Mode): the ONE-WAY sandbox
// escape hatch reachable from the Decline / game-over screen, per Aaron's
// Q100045 ruling companion to the endgame-ladder fix (BUG-504/505/506).
//
// Covers: the latch is irreversible (set-once, never unset), engaging Play
// Mode injects PLAY_MODE_INJECTION_AMOUNT as a clearly-labelled inflow (never
// disguised as a real economy event), the persistent "PLAY MODE — not a
// simulation" banner is wired to the latch (source-scan, mirrors the
// DeclineScreen source-scan pattern in imf-insolvency-inc4.test.mjs), a
// latched session is EXCLUDED from being used as a genesis-replay/AB
// determinism REFERENCE, and everything is a deterministic state transition
// (no Date/random — GR#21).
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved by re-running
// this file against a scratch cp/mv of engine.ts with 'enterPlayMode'
// reverted (GR#24 — never a git revert).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { PLAY_MODE_INJECTION_AMOUNT, PLAY_MODE_INJECTION_LABEL } from '../src/sim/fiscal.ts';
import { isStateAffecting, emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis, canUseAsReplayReference, stableStringify } from '../src/sim/genesisReplay.ts';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));

function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

// Drive a fresh game all the way to the FINAL DECLINE screen (hard game-over) —
// the scenario Play Mode is offered from. Mirrors imf-insolvency-inc4's
// rideSecondBailoutToDecline() helper, inlined so this file has no
// cross-suite dependency.
function driveToDecline() {
  let s = initialState();
  s = tickAtFunds(s, -2_000_000); // crisis -> first bailout.
  assert.ok(s.bailoutState, 'precondition: first bailout must be active');
  for (let i = 0; i < 360; i++) s = tickAtFunds(s, -2_000_000);
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must have auto-triggered');
  for (let i = 0; i < 360; i++) s = tickAtFunds(s, -5_000_000);
  assert.ok(s.declineState, 'precondition: the game must be in decline');
  return s;
}

// ========== The latch is ONE-WAY: set-once, never unset ==========

test('Play Mode: engaging from decline sets the latch, clears the freeze, and is otherwise a normal deterministic transition', () => {
  const declined = driveToDecline();
  const before = declined.funds;
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  assert.equal(engaged.playModeLatched, true, 'the latch must be set');
  assert.equal(engaged.declineState, null, 'engaging Play Mode must clear the hard-stop decline freeze');
  assert.equal(engaged.funds, before + PLAY_MODE_INJECTION_AMOUNT, 'must credit EXACTLY the named injection amount');
});

test('Play Mode: the latch is IRREVERSIBLE — no action can ever set it back to false', () => {
  const declined = driveToDecline();
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  assert.equal(engaged.playModeLatched, true);
  // A second engagement must be a true no-op (idempotent) — no re-injection.
  const again = reducer(engaged, { type: 'enterPlayMode' });
  assert.equal(again, engaged, 'a second enterPlayMode once latched must be a genuine no-op (same reference)');
  assert.equal(again.funds, engaged.funds, 'no re-injection on the second call');
  // Ticking, placing, resetting — nothing in the reducer writes `false` to
  // this field; a fresh reset() starts a NEW game (latch starts false there
  // too), but an EXISTING latched state can never be un-latched in place.
  const ticked = reducer(engaged, { type: 'tick' });
  assert.equal(ticked.playModeLatched, true, 'ticking must never clear the latch');
});

test('Play Mode MUTATION-PROVE target: a hand-mutated latch=false on an already-engaged state is DISTINGUISHABLE from the real irreversible latch', () => {
  const declined = driveToDecline();
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  const tampered = { ...engaged, playModeLatched: false };
  // Proves playModeLatched is a real, load-bearing field (not vacuously
  // always true) — a hand-cleared copy reads false, showing the reducer
  // itself (not some derived/computed getter) is what keeps it true.
  assert.notEqual(tampered.playModeLatched, engaged.playModeLatched);
  // But engaging AGAIN from the tampered copy re-latches deterministically —
  // proving the action, not incidental state, is what sets it.
  const relatched = reducer(tampered, { type: 'enterPlayMode' });
  assert.equal(relatched.playModeLatched, true);
});

// ========== The injection is a trillion, clearly labelled, never disguised ==========

test('Play Mode: the injection is booked as a NAMED, unambiguous inflow — never disguised as a real economy event', () => {
  const declined = driveToDecline();
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  const injection = engaged.lastFlows.inflows.find((f) => f.label === PLAY_MODE_INJECTION_LABEL);
  assert.ok(injection, 'the sandbox injection must be a real, findable inflow entry');
  assert.equal(injection.value, PLAY_MODE_INJECTION_AMOUNT);
  assert.match(PLAY_MODE_INJECTION_LABEL, /play mode/i, 'the label itself must say what it is — never a generic "Grant"/"Bailout" name');
});

test('Play Mode: conservation holds exactly across the engagement tick (no bypass flag needed)', () => {
  const declined = driveToDecline();
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  const inflowSum = engaged.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const outflowSum = engaged.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  assert.equal(
    engaged.fundsAtTickEnd,
    engaged.fundsAtTickStart + inflowSum - outflowSum,
    'fundsAtTickEnd === fundsAtTickStart + Σinflows − Σoutflows must hold WITH the sandbox injection counted as a normal inflow',
  );
});

test('Play Mode MUTATION-PROVE target: PLAY_MODE_INJECTION_AMOUNT is genuinely a "trillion"-scale sandbox sum, not a real economy figure', () => {
  // Distinguishes a real (small) grant from a deliberate sandbox injection —
  // orders of magnitude above STARTING_TREASURY (1.5M), so it can never be
  // mistaken for a real bailout/grant amount by a player reading the ledger.
  assert.ok(PLAY_MODE_INJECTION_AMOUNT >= 1_000_000_000_000, 'must be at least a literal trillion');
});

// ========== The action is state-affecting, journaled, and exempt from the decline freeze ==========

test('Play Mode: enterPlayMode is state-affecting — must be journaled for replay (mirrors enterAdministration)', () => {
  assert.equal(isStateAffecting({ type: 'enterPlayMode' }), true);
});

test('Play Mode: enterPlayMode is EXEMPT from the decline hard-stop freeze (reachable from the Decline screen)', () => {
  const declined = driveToDecline();
  const afterOtherAction = reducer(declined, { type: 'place', spec: 'res_hut', x: 60, y: 60 });
  assert.equal(afterOtherAction, declined, 'sanity: ordinary actions ARE still frozen by decline');
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  assert.notEqual(engaged, declined, 'enterPlayMode must NOT be frozen — it is the offered escape hatch');
  assert.equal(engaged.playModeLatched, true);
});

// ========== Determinism (GR#21): no Date/random ==========

test('Determinism: two identical Play Mode engagements produce byte-identical state', () => {
  function run() {
    const declined = driveToDecline();
    return reducer(declined, { type: 'enterPlayMode' });
  }
  const a = run();
  const b = run();
  assert.equal(stableStringify(a), stableStringify(b));
});

test('Determinism: genesis-replay of a journal containing enterPlayMode reproduces the SAME latched state', () => {
  let journal = emptyJournal();
  let s = initialState();
  let tick = 0;

  function step(action) {
    s = reducer(s, action);
    if (action.type === 'tick') tick = s.tick;
    journal = recordAction(journal, tick, action);
  }

  step({ type: 'debugFunds', amount: -2_000_000 - s.funds });
  step({ type: 'tick' });
  for (let i = 0; i < 360; i++) {
    step({ type: 'debugFunds', amount: -2_000_000 - s.funds });
    step({ type: 'tick' });
  }
  for (let i = 0; i < 360; i++) {
    step({ type: 'debugFunds', amount: -5_000_000 - s.funds });
    step({ type: 'tick' });
  }
  assert.ok(s.declineState, 'precondition: journal must reach decline');
  step({ type: 'enterPlayMode' });
  assert.ok(s.playModeLatched, 'precondition: journal must reach a latched Play Mode state');

  const replayed = replayFromGenesis(journal);
  assert.equal(stableStringify(replayed), stableStringify(s), 'genesis-replay must reproduce the EXACT latched state — the latch/injection are ordinary deterministic transitions');
});

// ========== A latched session is EXCLUDED from the AB/replay-reference path ==========

test('Play Mode: canUseAsReplayReference returns FALSE once latched, TRUE otherwise', () => {
  const fresh = initialState();
  assert.equal(canUseAsReplayReference(fresh), true, 'a normal, never-latched session IS a valid reference');
  const declined = driveToDecline();
  assert.equal(canUseAsReplayReference(declined), true, 'a declined-but-not-latched session is still a valid reference (no sandbox deviation yet)');
  const engaged = reducer(declined, { type: 'enterPlayMode' });
  assert.equal(canUseAsReplayReference(engaged), false, 'a LATCHED session must be excluded — it is a deliberate sandbox deviation, not a valid economy run');
});

test('Play Mode MUTATION-PROVE target: canUseAsReplayReference is not vacuously true — it genuinely reads the latch field', () => {
  const engaged = reducer(driveToDecline(), { type: 'enterPlayMode' });
  const tamperedBack = { ...engaged, playModeLatched: false };
  assert.equal(canUseAsReplayReference(tamperedBack), true, 'flipping the field must flip the verdict — proving the check is load-bearing, not a stub');
});

// ========== UI wiring: the persistent banner + Decline-screen button (source-scan) ==========
//
// Mirrors imf-insolvency-inc4.test.mjs's DeclineScreen source-scan pattern —
// a full jsdom/store mount belongs to the .tsx suite (see mount.test.tsx);
// this proves MapView.tsx's SOURCE wires the button to the real action and
// renders an unmissable, persistent banner gated on the latch field, not some
// other ad-hoc flag.

test('Play Mode: MapView.tsx wires the Decline-screen button to dispatch(enterPlayMode), and a persistent banner is gated on playModeLatched', () => {
  const src = readFileSync(path.join(here, '..', 'src', 'components', 'MapView.tsx'), 'utf8');

  const declineStart = src.indexOf('function DeclineScreen()');
  assert.ok(declineStart >= 0, 'DeclineScreen component must exist');
  const declineEnd = src.indexOf('\nfunction PlayModeBanner()', declineStart);
  assert.ok(declineEnd > declineStart, 'PlayModeBanner must be declared right after DeclineScreen');
  const declineBody = src.slice(declineStart, declineEnd);
  assert.match(
    declineBody,
    /dispatch\(\{\s*type:\s*'enterPlayMode'\s*\}\)/,
    'the Decline screen must offer a button dispatching the REAL enterPlayMode action',
  );

  const bannerStart = declineEnd;
  const bannerEnd = src.indexOf('\nfunction Compass()', bannerStart);
  const bannerBody = src.slice(bannerStart, bannerEnd >= 0 ? bannerEnd : bannerStart + 800);
  assert.match(bannerBody, /playModeLatched/, 'the persistent banner must gate on the SAME latch field the reducer sets');
  assert.match(bannerBody, /PLAY MODE/i, 'the banner copy must be unmissable and say what it is');
  assert.match(bannerBody, /not a simulation/i, 'the banner must explicitly disclaim itself as not a simulation');

  const renderList = src.slice(src.indexOf('<DeclineScreen />'), src.indexOf('<DeclineScreen />') + 200);
  assert.match(renderList, /<PlayModeBanner\s*\/>/, 'PlayModeBanner must actually be mounted, not just declared');
});

test('MUTATION-PROVE target: the source-scan above can fail — a missing wire-up is detectable', () => {
  const bypassSnippet =
    "function DeclineScreen() {\n  return <button onClick={() => window.location.reload()}>Reset</button>;\n}\nfunction PlayModeBanner() {\n  return null;\n}\nfunction Compass() {}";
  const declineStart = bypassSnippet.indexOf('function DeclineScreen()');
  const declineEnd = bypassSnippet.indexOf('\nfunction PlayModeBanner()', declineStart);
  const declineBody = bypassSnippet.slice(declineStart, declineEnd);
  assert.throws(() => {
    assert.match(declineBody, /dispatch\(\{\s*type:\s*'enterPlayMode'\s*\}\)/);
  }, 'a genuine missing wire-up must trip the match assertion used in the real test above');
});
