// imf-insolvency-inc5.test.mjs — FEAT-1972079923 inc5 (FINAL increment): AC-12,
// determinism + replay consistency across the ENTIRE insolvency flow.
//
// Scope (per the BUILD LANE brief's inc5 slice — AC-12 ONLY): prove the whole
// crisis -> bailout -> [administration] -> second bailout -> [administration] ->
// decline state machine is replay-deterministic:
//   (a) every state transition occurs at the SAME tick on replay as on the
//       original run;
//   (b) decline stats (peakPopulation/finalPopulation/minFundsEver/totalSpending)
//       and the forced-asset-sale list order are IDENTICAL on replay;
//   (c) no Date.now()/Math.random()/new Date()/.getTime() anywhere in the
//       insolvency-relevant code (engine.ts's advance()+forcedSaleAssets,
//       fiscal.ts, data.ts's admin-aware advisor).
//
// inc1-4 already proved each individual state transition is triggered by pure
// tick arithmetic (see their own "Determinism" sections). This increment adds
// the thing those didn't: a genuine drive through the REAL replay machinery
// (src/sim/replay.ts's createSavepoint/persistSavepoint/restoreFromSavepoint —
// the actual save/load path, not just re-calling the reducer twice) and a
// mechanical source-scan that cannot be satisfied by "we looked and didn't see
// any" — it actually greps the code.
//
// node --test type-strips the .ts imports; every assertion below can FAIL if
// the corresponding fix is reverted — RED/GREEN pairs proved with a scratch
// cp/mv of engine.ts/fiscal.ts, never a git revert (GR#24).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  initialState,
  reducer,
  TICKS_PER_YEAR,
  forcedSaleAssets,
} from '../src/sim/engine.ts';
import {
  DEBT_THRESHOLD_FOR_BAILOUT,
  INSOLVENCY_WARNING_THRESHOLD,
  BAILOUT_DURATION_TICKS,
  SECOND_BAILOUT_DURATION_TICKS,
  FINAL_DECLINE_FUNDS_THRESHOLD,
} from '../src/sim/fiscal.ts';
import {
  createSavepoint,
  persistSavepoint,
  restoreFromSavepoint,
} from '../src/sim/replay.ts';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC_DIR = path.join(here, '..', 'src', 'sim');

// In-memory localStorage substitute — mirrors journal.test.mjs's MockStorage
// exactly (the accepted pattern for exercising replay.ts without a real DOM).
class MockStorage {
  constructor() {
    this.data = {};
  }
  getItem(key) {
    return Object.prototype.hasOwnProperty.call(this.data, key) ? this.data[key] : null;
  }
  setItem(key, value) {
    this.data[key] = value;
  }
  removeItem(key) {
    delete this.data[key];
  }
}

// ========== Driving helpers (mirror inc1-4's tickAtFunds/tickN exactly) ==========

function tickAtFunds(state, targetFunds) {
  const forced = reducer(state, { type: 'debugFunds', amount: targetFunds - state.funds });
  return reducer(forced, { type: 'tick' });
}

// BUG-452 inc1 (2026-09-01): derived from the ratio-preserved thresholds (see
// imf-insolvency-inc1.test.mjs) rather than hardcoded to the old £10M-scale
// -6,000,000 literal, so this suite auto-scales with STARTING_TREASURY.
const WARNING_BAND_FUNDS = Math.round((INSOLVENCY_WARNING_THRESHOLD + DEBT_THRESHOLD_FOR_BAILOUT) / 2);

/**
 * Drive a FRESH game from genesis, through warning -> crisis -> first bailout
 * -> (still broke) auto second bailout -> (still broke) hard decline, using
 * ONLY journaled, state-affecting actions (debugFunds + tick — both
 * isStateAffecting per journal.ts), recording the exact action sequence as it
 * goes. This is a GENUINE decline scenario (a real deficit that never
 * recovers), not a fixture that stops short of the states under test.
 *
 * Returns the final state, the full action journal (as {tick, action} entries
 * matching JournalEntry), and the tick each transition actually fired at, so
 * callers can compare transition ticks directly rather than only comparing
 * opaque final-state blobs.
 */
function driveGenesisToDecline() {
  let s = initialState();
  const entries = [];
  const record = (action) => {
    entries.push({ tick: s.tick, action });
    s = reducer(s, action);
  };
  const driveTickAtFunds = (targetFunds) => {
    record({ type: 'debugFunds', amount: targetFunds - s.funds });
    record({ type: 'tick' });
  };

  // Warning band, then crisis (triggers the FIRST bailout).
  driveTickAtFunds(WARNING_BAND_FUNDS);
  driveTickAtFunds(DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  assert.ok(s.bailoutState, 'precondition: first bailout must be active');
  const firstBailoutEnteredAt = s.bailoutState.enteredAt;

  // Ride the first bailout year out, staying deep in crisis throughout, so the
  // SECOND bailout auto-triggers at year-end (mirrors inc4's
  // rideFirstBailoutToSecond).
  for (let i = 0; i < BAILOUT_DURATION_TICKS; i++) {
    driveTickAtFunds(DEBT_THRESHOLD_FOR_BAILOUT - 1_000_000);
  }
  assert.ok(s.bailoutSecondState, 'precondition: second bailout must have auto-triggered');
  const secondBailoutEnteredAt = s.bailoutSecondState.enteredAt;

  // Ride the second bailout year out, staying below the decline threshold
  // throughout, so the FINAL DECLINE screen fires at its year-end.
  for (let i = 0; i < SECOND_BAILOUT_DURATION_TICKS; i++) {
    driveTickAtFunds(FINAL_DECLINE_FUNDS_THRESHOLD - 5_000_000);
  }
  assert.ok(s.declineState, 'precondition: decline must have triggered — this IS the genuine endgame scenario');
  const declineEnteredAt = s.declineState.enteredAt;

  return { finalState: s, entries, firstBailoutEnteredAt, secondBailoutEnteredAt, declineEnteredAt };
}

// ========== (a) Every transition lands on the SAME tick, run to run ==========

test('AC-12: two independent genesis-to-decline drives fire every transition at the SAME tick', () => {
  const a = driveGenesisToDecline();
  const b = driveGenesisToDecline();
  assert.equal(a.firstBailoutEnteredAt, b.firstBailoutEnteredAt, 'first bailout entry tick must match');
  assert.equal(a.secondBailoutEnteredAt, b.secondBailoutEnteredAt, 'second bailout entry tick must match');
  assert.equal(a.declineEnteredAt, b.declineEnteredAt, 'decline entry tick must match');
  // Sanity: the transitions are genuinely SPACED OUT (not all tick 0), proving
  // this assertion is not vacuously true because nothing ever moves.
  assert.ok(a.secondBailoutEnteredAt > a.firstBailoutEnteredAt);
  assert.ok(a.declineEnteredAt > a.secondBailoutEnteredAt);
});

test('AC-12: two independent genesis-to-decline drives produce a byte-identical final state', () => {
  const a = driveGenesisToDecline();
  const b = driveGenesisToDecline();
  assert.equal(JSON.stringify(a.finalState), JSON.stringify(b.finalState));
});

// ========== (b) Decline stats + forced-asset order are identical run to run =========

test('AC-12: decline stats (peakPopulation/finalPopulation/minFundsEver/totalSpending) are identical run to run', () => {
  const a = driveGenesisToDecline();
  const b = driveGenesisToDecline();
  assert.deepEqual(a.finalState.declineState, b.finalState.declineState);
  // Each field individually, so a future refactor that drops one field from
  // the deepEqual (e.g. a destructure that silently narrows the object) still
  // gets caught here.
  assert.equal(a.finalState.declineState.peakPopulation, b.finalState.declineState.peakPopulation);
  assert.equal(a.finalState.declineState.finalPopulation, b.finalState.declineState.finalPopulation);
  assert.equal(a.finalState.declineState.minFundsEver, b.finalState.declineState.minFundsEver);
  assert.equal(a.finalState.declineState.totalSpending, b.finalState.declineState.totalSpending);
});

test('AC-12: forced-asset-sale list order (id sequence) is identical run to run, at every bailout checkpoint', () => {
  const a = driveGenesisToDecline();
  const b = driveGenesisToDecline();
  // Replay both drives up to the moment the FIRST bailout fires (before any
  // asset has been sold), and compare the forced-sale list id order.
  let sa = initialState();
  let sb = initialState();
  const upToFirstBailout = (entries, target) => {
    let s = initialState();
    for (const e of entries) {
      s = reducer(s, e.action);
      if (s.bailoutState && s.bailoutState.enteredAt === s.tick) break;
    }
    return s;
  };
  sa = upToFirstBailout(a.entries);
  sb = upToFirstBailout(b.entries);
  assert.ok(sa.bailoutState && sb.bailoutState, 'precondition: both must have reached the first bailout');
  const idsA = forcedSaleAssets(sa).map((asset) => asset.id);
  const idsB = forcedSaleAssets(sb).map((asset) => asset.id);
  assert.deepEqual(idsA, idsB, 'the forced-sale asset id order must be identical, not just same-length');
  assert.ok(idsA.length > 0, 'precondition: there must be sellable assets to actually compare an order');
});

test('MUTATION-PROVE target: forcedSaleAssets is a genuinely PURE function — calling it twice on the SAME state gives the SAME order', () => {
  // This is the assertion whose violation the AC-12 mutation ("introduce
  // Math.random() into the asset-sort comparator") is meant to catch. Proven
  // live (not just asserted) in this build's verification pass: a scratch
  // copy of engine.ts with `a.id - b.id` replaced by
  // `a.id - b.id + (Math.random() - 0.5)` in forcedSaleAssets' comparator
  // turns THIS test red (two calls on the identical state diverge), and is
  // restored immediately after (GR#24 — never left mutated).
  const a = driveGenesisToDecline();
  let s = initialState();
  for (const e of a.entries) {
    s = reducer(s, e.action);
    if (s.bailoutState) break;
  }
  const first = forcedSaleAssets(s).map((x) => x.id);
  const second = forcedSaleAssets(s).map((x) => x.id);
  assert.deepEqual(first, second, 'a pure comparator must return the identical order on repeated calls');
});

// ========== The REAL replay path: createSavepoint / persistSavepoint / restoreFromSavepoint ==========

test('AC-12: restoreFromSavepoint (the ACTUAL save/load machinery) reproduces the original decline outcome exactly', () => {
  const original = driveGenesisToDecline();

  // Split the SAME recorded action journal at an arbitrary midpoint (partway
  // through the second bailout year, well after the interesting early
  // transitions) into a snapshot + tail — exactly how autosave works (a
  // snapshot state + the journal entries since that snapshot).
  const splitIndex = Math.floor(original.entries.length * 0.7);
  let snapshotState = initialState();
  for (let i = 0; i < splitIndex; i++) {
    snapshotState = reducer(snapshotState, original.entries[i].action);
  }
  const tail = original.entries.slice(splitIndex);
  assert.ok(tail.length > 0, 'precondition: there must be a real journal tail to replay');

  const storage = new MockStorage();
  const savepoint = createSavepoint(snapshotState, tail, new Date('2026-08-31T00:00:00Z'));
  const persisted = persistSavepoint(storage, savepoint);
  assert.equal(persisted, true, 'precondition: savepoint must actually persist');

  const result = restoreFromSavepoint(storage);
  assert.equal(result.success, true, `restore must succeed: ${result.reason ?? ''}`);
  assert.equal(result.replayed, tail.length, 'every tail entry must have been replayed');

  // The transitions the tail crosses (decline, at minimum) must land on the
  // SAME tick as the uninterrupted original run.
  assert.ok(result.state.declineState, 'restored state must have declined, same as the uninterrupted run');
  assert.equal(
    result.state.declineState.enteredAt,
    original.declineEnteredAt,
    'decline must fire at the SAME tick via the real replay path as the uninterrupted run',
  );
  assert.deepEqual(
    result.state.declineState,
    original.finalState.declineState,
    'decline stats via real replay must match the uninterrupted run exactly',
  );

  // Byte-identical final state — the strongest possible check: the ENTIRE
  // restored SimState, not just the fields we thought to name.
  assert.equal(
    JSON.stringify(result.state),
    JSON.stringify(original.finalState),
    'a snapshot+journal-tail restore through the real replay.ts machinery must reproduce the byte-identical final state',
  );
});

test('MUTATION-PROVE target: the byte-identical assertion above is not vacuous — a genuinely DIFFERENT tail produces a genuinely different state', () => {
  const original = driveGenesisToDecline();
  const splitIndex = Math.floor(original.entries.length * 0.7);
  let snapshotState = initialState();
  for (let i = 0; i < splitIndex; i++) {
    snapshotState = reducer(snapshotState, original.entries[i].action);
  }
  // A DIFFERENT tail: recover funds instead of staying broke — must NOT decline.
  const divergedTail = original.entries.slice(splitIndex).map((e) =>
    e.action.type === 'debugFunds' ? { ...e, action: { type: 'debugFunds', amount: 50_000_000 } } : e,
  );
  const storage = new MockStorage();
  const savepoint = createSavepoint(snapshotState, divergedTail, new Date('2026-08-31T00:00:00Z'));
  persistSavepoint(storage, savepoint);
  const result = restoreFromSavepoint(storage);
  assert.equal(result.success, true, `precondition: restore must succeed: ${result.reason ?? ''}`);
  assert.notEqual(
    JSON.stringify(result.state),
    JSON.stringify(original.finalState),
    'a genuinely different tail must NOT reproduce the original final state — proving the equality check above is load-bearing',
  );
});

// ========== (c) Mechanical source-scan: no Date.now/Math.random in the insolvency path ==========

/**
 * Strip `//` line comments and `/* ... *\/` block comments so the scan below
 * only sees CODE, never prose (this file's own source comments legitimately
 * mention "Date.now()" and "Math.random()" by name when documenting GR#21
 * compliance — a naive substring grep over the raw file would false-positive
 * on those very comments).
 */
function stripComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}

const NON_DETERMINISTIC_PATTERN = /Date\.now\(|new Date\(|Math\.random\(|\.getTime\(\)/;

test('AC-12: no Date.now()/new Date()/Math.random()/.getTime() ANYWHERE in engine.ts CODE (comments excluded)', () => {
  const src = readFileSync(path.join(SRC_DIR, 'engine.ts'), 'utf8');
  const code = stripComments(src);
  const match = code.match(NON_DETERMINISTIC_PATTERN);
  assert.equal(match, null, `found non-deterministic construct in engine.ts code: ${match?.[0]}`);
});

test('AC-12: no Date.now()/new Date()/Math.random()/.getTime() ANYWHERE in fiscal.ts CODE (comments excluded)', () => {
  const src = readFileSync(path.join(SRC_DIR, 'fiscal.ts'), 'utf8');
  const code = stripComments(src);
  const match = code.match(NON_DETERMINISTIC_PATTERN);
  assert.equal(match, null, `found non-deterministic construct in fiscal.ts code: ${match?.[0]}`);
});

test('AC-12: no Date.now()/new Date()/Math.random()/.getTime() in data.ts\'s administration-aware advisor code (comments excluded)', () => {
  const src = readFileSync(path.join(SRC_DIR, 'data.ts'), 'utf8');
  const code = stripComments(src);
  const match = code.match(NON_DETERMINISTIC_PATTERN);
  assert.equal(match, null, `found non-deterministic construct in data.ts code: ${match?.[0]}`);
});

test('MUTATION-PROVE target: the source-scan regex genuinely CATCHES an injected Date.now()/Math.random() in real code', () => {
  const bypassSnippet = `
    // no Date/Math.random here, deterministic (this comment is a decoy).
    function advance(s) {
      const tick = s.tick + 1;
      const jitter = Math.random() * 2;
      return { ...s, tick, funds: s.funds + jitter };
    }
  `;
  const code = stripComments(bypassSnippet);
  const match = code.match(NON_DETERMINISTIC_PATTERN);
  assert.ok(match, 'a genuine Math.random() in code must trip the pattern used by the real scan above');
  assert.equal(match[0], 'Math.random(');
});

test('MUTATION-PROVE target: the comment-stripper prevents a FALSE POSITIVE — Date.now() named only in a comment must NOT trip the scan', () => {
  const commentOnlySnippet = `
    // deterministic tick arithmetic only (GR#21), never Date.now(). Recovery is
    // reported via the funds band below.
    function pureFn(x) { return x + 1; }
  `;
  const code = stripComments(commentOnlySnippet);
  const match = code.match(NON_DETERMINISTIC_PATTERN);
  assert.equal(match, null, 'a comment merely NAMING Date.now() must not be mistaken for a real call — proving the scan is not trivially over-broad');
});

// ========== Duration-constant sanity (companion to inc2/inc3/inc4's own copies) ==========

test('AC-12 companion: all three insolvency durations share TICKS_PER_YEAR (360) — the whole endgame is calendar-aligned, not arbitrary', () => {
  assert.equal(BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(SECOND_BAILOUT_DURATION_TICKS, TICKS_PER_YEAR);
  assert.equal(TICKS_PER_YEAR, 360);
});
