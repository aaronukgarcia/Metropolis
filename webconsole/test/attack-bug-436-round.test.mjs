// attack-bug-436-round.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23) against
// the BUG-436 fail-closed rebuild-persist estate (store.tsx onRebuild's
// chunk.done branch + bug-436-rebuild-persist-fail-closed.test.mjs).
//
// The fix under attack:
//   const rebuildPersisted = persistSavepoint(window.localStorage, rebuiltSave);
//   mirrorAfterPersist(rebuildPersisted, rebuiltSave);
//   if (!rebuildPersisted) { recordError('...quota...'); setRebuildDecision(null);
//                            setRebuildPhase('prompt'); return; }
//
// These tests are written to FAIL against the estate as it stands where the
// estate is wrong. Each one names its finding.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  persistSavepoint,
  persistSavepointWithReason,
  persistSavepointForced,
  createSavepoint,
  readAllSavepoints,
  mostRecentSavepoint,
} from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';
import { mirrorSavepointDirect } from '../src/sim/saveStore.ts';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const STORE_SRC = fs.readFileSync(path.join(HERE, '..', 'src', 'sim', 'store.tsx'), 'utf8');

function memStorage(seed = {}) {
  const m = new Map(Object.entries(seed));
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
    _map: m,
  };
}

function quotaWedged(base) {
  return {
    getItem: base.getItem,
    setItem: () => {
      const e = new Error('The quota has been exceeded.');
      e.name = 'QuotaExceededError';
      throw e;
    },
    removeItem: base.removeItem,
    _keys: base._keys,
  };
}

/** A minimal in-memory SaveStore matching saveStore.ts's SaveStore surface. */
function memSaveStore() {
  const m = new Map();
  return {
    getItem: async (k) => (m.has(k) ? m.get(k) : null),
    setItem: async (k, v) => {
      m.set(k, String(v));
      return { ok: true, quota: false, degraded: false };
    },
    removeItem: async (k) => void m.delete(k),
    _map: m,
  };
}

/** The exact freshness rule store.tsx's IDB boot-swap uses
 * (`isStrictlyFresherSavepointMeta`) — re-derived here because it is not
 * exported. Kept byte-faithful to the source; the source itself is pinned by
 * the wiring assertion in ATTACK-2 so this cannot silently drift. */
function isStrictlyFresher(candidate, booted) {
  if (!booted) return true;
  const cSeq = Number.isFinite(candidate.saveSeq);
  const bSeq = Number.isFinite(booted.saveSeq);
  if (cSeq && bSeq) {
    if (candidate.saveSeq !== booted.saveSeq) return candidate.saveSeq > booted.saveSeq;
  }
  return (
    candidate.snapshotTick > booted.snapshotTick ||
    (candidate.snapshotTick === booted.snapshotTick && candidate.savedAt > booted.savedAt)
  );
}

// ---------------------------------------------------------------------------
// ATTACK 1 — the mandated highest-value attack: can the lineage-scoped
// (saveSeq, tick) persist gate legitimately REFUSE a HEALTHY rebuild, firing
// the new fail-closed path on a city that is not broken at all?
// ---------------------------------------------------------------------------

describe('ATTACK 1: does the persist gate refuse a HEALTHY rebuild?', () => {
  test('F1 (P1): on a pre-saveSeq (legacy) install, a healthy genesis rebuild of a journal-truncated city is REFUSED as stale-overwrite — the new fail-closed path fires on a city that is perfectly fine', () => {
    // Reachability of the whole shape, all from this repo's own source:
    //  * journal.ts JOURNAL_CAP = 50000, appended with .slice(-JOURNAL_CAP) —
    //    a ring buffer. 'tick' actions ARE journalled (genesisReplay's own
    //    phase-label map lists 'tick'), so once a city exceeds 50k actions the
    //    OLDEST ticks are evicted.
    //  * genesisReplay replays from engine.init() (tick 0) forward over
    //    whatever survives. So on any long-lived city the rebuilt state's tick
    //    is STRICTLY LOWER than the live city's — by exactly the evicted ticks.
    //    That is the NORMAL, HEALTHY outcome of a rebuild, not a defect.
    //  * store.tsx `saveSeqRef = useRef(boot.bootSavepointMeta?.saveSeq ?? 0)`.
    //    A savepoint written before the FEAT-2326609780 round-3 landing (i.e.
    //    every savepoint on any install that existed before today) has NO
    //    saveSeq, so saveSeqRef starts at 0 and nextSaveSeq() returns 1.
    //  * replay.ts isIncomingSavepointNewerOrEqual uses saveSeq as primary
    //    ONLY when BOTH sides carry one. Legacy slots do not — so the
    //    comparison falls back to TICK.
    // => rebuilt tick (lower) vs occupied slot tick (higher) => REFUSED.
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);

    // Three occupied slots of the SAME (legacy) lineage, written before
    // saveSeq existed — note the explicit `undefined` seq.
    for (let i = 0; i < 3; i++) {
      const sp = createSavepoint(
        { ...initialState(), tick: 61000 + i },
        [],
        new Date(now.getTime() - (3 - i) * 60_000),
        'v-old',
        null,
        undefined, // pre-round-3: no saveSeq
      );
      assert.equal(sp.saveSeq, undefined, 'fixture sanity: the legacy slots carry no saveSeq');
      assert.ok(persistSavepoint(storage, sp, now), `legacy slot ${i} must seed`);
    }

    // The rebuild runs to completion, cleanly. No crash, no quota problem,
    // nothing wrong with the city. Its tick is lower purely because 50k+
    // journal actions (including ticks) rolled off the ring.
    const rebuiltState = { ...initialState(), tick: 11000, buildings: [] };
    const rebuiltSave = createSavepoint(rebuiltState, [], now, 'v-new', null, /* nextSaveSeq() */ 1);

    const result = persistSavepointWithReason(storage, rebuiltSave, now);

    assert.equal(result.ok, false, 'the gate refuses the rebuilt city');
    assert.equal(result.reason, 'stale-overwrite', 'and it refuses for stale-overwrite, NOT storage-error');

    // Therefore the new fail-closed branch fires — on a healthy rebuild.
    // Prove it is not transient: repeating the rebuild is deterministic, so the
    // player can never escape it by pressing Rebuild again.
    for (let attempt = 2; attempt <= 5; attempt++) {
      const retry = persistSavepointWithReason(
        storage,
        createSavepoint(rebuiltState, [], now, 'v-new', null, attempt),
        now,
      );
      assert.equal(retry.ok, false, `retry ${attempt} is refused identically — the rebuild can never be completed`);
      assert.equal(retry.reason, 'stale-overwrite');
    }

    // The disk still only has the old city, and no code path in the estate
    // can ever get the rebuilt one there.
    const onDisk = mostRecentSavepoint(readAllSavepoints(storage, now, undefined));
    assert.equal(onDisk.snapshotTick, 61002, 'the old city still owns every slot, permanently');
  });

  test('FIXED (was F2, lead ruling; round-4 superseded the F1 try-then-fallback shape): onRebuild forces UNCONDITIONALLY and never fires the wrong "free up storage" message for a stale-overwrite', () => {
    // Lead ruling on F1+F2, carried forward by the round-4 fix contract: a
    // rebuild is a deliberate replace-the-city boundary. Round 3 landed this
    // as "try the plain gate, force only on a stale-overwrite refusal" —
    // round 4 (F-R3-1/F-R3-1b) proved that fallback never even FIRES on a
    // legacy install with any free/torn slot (the plain gate's staleness
    // check is skipped entirely for an empty target, so it never refuses).
    // onRebuild now calls `persistSavepointForced` UNCONDITIONALLY instead —
    // the boolean-only call site and the two-step fallback are both gone.
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];

    assert.doesNotMatch(
      onRebuild,
      /persistSavepoint\(window\.localStorage, rebuiltSave\)/,
      'the boolean-only call site is gone — replaced by the forced reason-carrying gate',
    );
    assert.match(
      onRebuild,
      /const rebuildResult = persistSavepointForced\(window\.localStorage, rebuiltSave\);/,
      'onRebuild forces the rebuild write unconditionally (round-4) rather than trying the plain gate first',
    );

    // The residual failure message (storage-error only, now that
    // stale-overwrite is handled by forcing) still leads with quota/free-up
    // guidance — which IS actionable for that reason.
    const failMsg = onRebuild.slice(onRebuild.indexOf('if (!rebuildPersisted)'));
    assert.match(failMsg, /storage quota/i, 'the storage-error message leads with quota');
    assert.match(failMsg, /Free up storage space/i, 'and instructs the player to free storage space');
    // Round-4: `persistSavepointForced` skips the staleness check entirely
    // by construction, so there is no reachable 'stale-overwrite' branch any
    // more — the message interpolates whatever reason DOES come back
    // (currently only ever 'storage-error') rather than hardcoding one.
    assert.match(failMsg, /rebuildResult\.reason \?\? 'storage-error'/, 'the message stays reason-aware rather than hardcoding one refusal reason');

    // Data-layer proof: F1's exact repro (legacy install, lower-tick rebuild)
    // is refused unforced, but persistSavepointForced (the retry onRebuild
    // now performs) completes it — closing the "no remedy exists" finding.
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    for (let i = 0; i < 3; i++) {
      persistSavepoint(
        storage,
        createSavepoint({ ...initialState(), tick: 61000 + i }, [], new Date(now.getTime() - (3 - i) * 60_000), 'v-old', null, undefined),
        now,
      );
    }
    const rebuilt = createSavepoint({ ...initialState(), tick: 11000 }, [], now, 'v-new', null, 1);
    assert.equal(persistSavepointWithReason(storage, rebuilt, now).reason, 'stale-overwrite', 'sanity: the plain gate still refuses this shape');

    const forced = persistSavepointForced(storage, rebuilt, now);
    assert.equal(forced.ok, true, 'FIXED: the forced retry onRebuild now performs completes the rebuild instead of leaving it permanently refused');
  });

  test('control: on a MODERN (saveSeq-carrying) install the same lower-tick rebuild is accepted — F1 is scoped to the legacy/mixed case, not universal', () => {
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    for (let i = 0; i < 3; i++) {
      assert.ok(
        persistSavepoint(
          storage,
          createSavepoint({ ...initialState(), tick: 61000 + i }, [], new Date(now.getTime() - (3 - i) * 60_000), 'v-old', null, 40 + i),
          now,
        ),
      );
    }
    const rebuilt = createSavepoint({ ...initialState(), tick: 11000 }, [], now, 'v-new', null, 43);
    const r = persistSavepointWithReason(storage, rebuilt, now);
    assert.equal(r.ok, true, 'saveSeq-primary lets the rebuilt city through despite the lower tick');
  });
});

// ---------------------------------------------------------------------------
// ATTACK 2 — the mirrorAfterPersist addition vs the IDB-PRIMARY boot
// (FEAT-2326609780). FIXED (F3, lead ruling): the failure branch must never
// mirror a failed primary write into IndexedDB — the PRIMARY boot store —
// while its error message asserts the opposite. `mirrorAfterPersist` now
// only ever runs on the confirmed-successful tail.
// ---------------------------------------------------------------------------

describe('ATTACK 2: mirrorAfterPersist on the FAILURE path vs IDB-primary boot', () => {
  test('FIXED (was F3): the fail-closed branch no longer mirrors into IndexedDB — the error message and the data layer now agree', async () => {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    const failStart = onRebuild.indexOf('if (!rebuildPersisted) {');
    let i = onRebuild.indexOf('{', failStart);
    let depth = 0;
    let failEnd = -1;
    for (let j = i; j < onRebuild.length; j++) {
      if (onRebuild[j] === '{') depth++;
      else if (onRebuild[j] === '}') {
        depth--;
        if (depth === 0) {
          failEnd = j + 1;
          break;
        }
      }
    }
    const failBranch = onRebuild.slice(failStart, failEnd);

    // The claim the estate makes to the player.
    assert.match(failBranch, /The rebuilt city was NOT written to disk/, 'precondition: the message claims the rebuilt city did not land');
    assert.match(failBranch, /resuming would restore the OLD city/, 'precondition: the message promises a resume restores the OLD city');
    // FIXED: mirrorAfterPersist no longer runs anywhere inside the failure branch.
    assert.doesNotMatch(
      failBranch,
      /mirrorAfterPersist\(/,
      'FIXED F3: the failure branch must not call mirrorAfterPersist at all',
    );
    // It DOES run, but only after the guard, on the success tail, fed a
    // literal `true` (the outcome is known-successful by construction there).
    const successTail = onRebuild.slice(failEnd);
    assert.match(successTail, /mirrorAfterPersist\(true, rebuiltSave\)/, 'the mirror now runs only on the success tail');

    // Now the data-layer consequence: since the failure branch never calls
    // mirrorSavepointDirect, a quota-wedged rebuild attempt must leave the
    // durable IDB overflow slot untouched.
    const store = memSaveStore();
    const now = new Date(2026, 8, 4, 12);
    const lineage = 'L-city-13';

    const rebuiltState = { ...initialState(), tick: 11000, lineageId: lineage };
    const rebuiltSave = createSavepoint(rebuiltState, [], now, 'v-new', null, 43);
    rebuiltSave.lineageId = lineage;
    const wedged = quotaWedged(memStorage());
    const persisted = persistSavepoint(wedged, rebuiltSave, now);
    assert.equal(persisted, false, 'localStorage write fails, as in the dogfood wedge');

    // FIXED: the failure branch performs NO mirror call at all — the overflow
    // slot for this lineage is never populated by this code path.
    const idbKey = `metropolis.savepoint.${lineage}.idbOnly`;
    assert.equal(store._map.has(idbKey), false, 'FIXED F3: the rebuilt city is NOT sitting in the IDB overflow slot after a failed persist');

    // The error message's claim and the data layer now agree: the rebuilt
    // city genuinely reaches nowhere on a failed write.
  });

  test('F4 (P2, FIXED by BUG-704): on the stale-overwrite refusal, the IDB mirror no longer bypasses the gate that just refused the write — a savepoint rejected as too old in localStorage must ALSO be refused by the durable store when the caller feeds in localStorage\'s current rotation-slot bytes as baselines', async () => {
    // Pre-BUG-704, guardedSavepointSetItem only ever compared against the
    // OVERFLOW key's own prior contents and never saw the three
    // localStorage/IDB SLOTS that caused the stale-overwrite refusal, so an
    // empty overflow slot accepted anything. mirrorSavepointDirect now takes
    // an optional 4th `extraExistingRaw` argument — the caller (store.tsx's
    // `localSavepointBaselines`) feeds in exactly those slot bytes.
    const store = memSaveStore();
    const now = new Date(2026, 8, 4, 12);
    const storage = memStorage();
    for (let i = 0; i < 3; i++) {
      persistSavepoint(
        storage,
        createSavepoint({ ...initialState(), tick: 61000 + i }, [], new Date(now.getTime() - (3 - i) * 60_000), 'v-old', null, undefined),
        now,
      );
    }
    const rebuilt = createSavepoint({ ...initialState(), tick: 11000 }, [], now, 'v-new', null, 1);
    const r = persistSavepointWithReason(storage, rebuilt, now);
    assert.equal(r.reason, 'stale-overwrite', 'refused by the (saveSeq,tick) gate');

    // The exact bytes localStorage currently holds for this (legacy) lineage's
    // three rotation slots — what the fixed call site now threads through.
    const baselines = storage._keys().map((k) => storage.getItem(k));

    const mirroredWithoutBaselines = await mirrorSavepointDirect(store, JSON.stringify(rebuilt), undefined);
    assert.equal(
      mirroredWithoutBaselines.ok,
      true,
      'omitting the baselines degrades to the pre-fix single-key comparison (documented, not a regression) — the overflow slot was empty so the write still lands',
    );

    // Reset the store so the second call starts from the same empty overflow
    // slot the first one did — the point being tested is the GATE, not
    // whatever the previous call happened to leave behind.
    store._map.clear();
    const mirroredWithBaselines = await mirrorSavepointDirect(store, JSON.stringify(rebuilt), undefined, baselines);
    assert.equal(
      mirroredWithBaselines.ok,
      false,
      'FIXED F4/BUG-704: fed the same localStorage bytes that caused the stale-overwrite refusal, the durable mirror now refuses the write too',
    );
    assert.equal(mirroredWithBaselines.reason, 'stale', 'BUG-704 re-round 2 (P3 item 2): a freshness refusal must be reported as \'stale\', not a generic failure');
    assert.equal(store._map.has('metropolis.savepoint.idbOnly'), false, 'the stale savepoint must not land in the durable overflow slot');
  });
});

// ---------------------------------------------------------------------------
// ATTACK 3 — the failure fallback UX. Does phase 'prompt' + decision null
// leave the player anywhere sane? Does it loop? Does it collide with GR#27?
// ---------------------------------------------------------------------------

describe('ATTACK 3: the failure fallback UX', () => {
  test('NOT a defect (attack refuted): setRebuildDecision(null) + setRebuildPhase(\'prompt\') is the established dismiss idiom — the modal unmounts, there is no infinite retry loop', () => {
    // store.tsx renders `{rebuildDecision && isRebuildPromptTop && (<RebuildPrompt ...`
    assert.match(
      STORE_SRC,
      /\{rebuildDecision && isRebuildPromptTop && \(/,
      'the prompt modal is gated on a NON-NULL rebuildDecision',
    );
    // So nulling the decision closes the modal rather than re-arming it. Three
    // other branches already do exactly this pair.
    const pairs = STORE_SRC.match(/setRebuildDecision\(null\);\s*\n\s*setRebuildPhase\('prompt'\)/g) ?? [];
    assert.ok(
      pairs.length >= 3,
      `the dismiss idiom is pre-existing and used in ${pairs.length} places (crashed branch, catch branch, finishLoadOverlay) — the fix mirrors it faithfully`,
    );
    // Consequence (reported as an observation, not a red): after the failure
    // the player has NO in-app control to retry the rebuild — the decision
    // that produced it came from boot's `pendingRebuild`. A page reload
    // re-derives it. Acceptable, and identical to the crashed branch.
  });

  test('NOT a defect (attack refuted): the fail-closed path performs NO wipe, so GR#27 capture-before-wipe is not engaged and cannot collide', () => {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    const failBranch = onRebuild.slice(
      onRebuild.indexOf('if (!rebuildPersisted)'),
      onRebuild.indexOf('persistStashedCamera'),
    );
    assert.doesNotMatch(failBranch, /attemptWipe|captureBeforeWipe|dispatch\(\{ *type: 'reset'/, 'no wipe on the failure path');
    // And the early `return` fires BEFORE the journal flush, so the journal is
    // left exactly as the old (still-live) city expects it.
    assert.ok(
      onRebuild.indexOf('if (!rebuildPersisted)') < onRebuild.indexOf('journalPersisterRef.current?.flush'),
      'the failure return precedes the journal flush — correct: the rebuild boundary is not committed',
    );
  });

  test('FIXED (was F5, P3, ordering): the failure path now clears `rebuildReportState`, mirroring the `crashed` branch above it', () => {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    const reportAt = onRebuild.indexOf('setRebuildReportState(report)');
    const failAt = onRebuild.indexOf('if (!rebuildPersisted)');
    assert.ok(reportAt !== -1 && failAt !== -1);
    assert.ok(
      reportAt < failAt,
      'precondition: the report state is set BEFORE the persist attempt',
    );
    const failBranch = onRebuild.slice(failAt, onRebuild.indexOf('persistStashedCamera'));
    assert.match(
      failBranch,
      /setRebuildReportState\(null\)/,
      'FIXED F5: the abandoned rebuild\'s report is now cleared, matching the `crashed` branch (which returns before it is ever set) rather than leaking a stale report behind the reopened prompt.',
    );
  });
});

// ---------------------------------------------------------------------------
// ATTACK 4 — double-invoke / re-entrancy on the chunk.done branch.
// ---------------------------------------------------------------------------

describe('ATTACK 4: double-invoke on the completion branch', () => {
  test('NOT a defect (attack refuted): the completion branch is reachable only from a generation-guarded rAF chain, and nextSaveSeq() makes a genuine double-persist idempotent-by-ordering rather than destructive', () => {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    // onRebuild is an event callback, not an effect — React StrictMode
    // double-invokes render + effects, never event handlers.
    assert.match(onRebuild, /if \(isStaleRebuildChain\(myGen, rebuildGenRef\.current\)\)/, 'stale-chain guard present');
    assert.match(onRebuild, /rebuildGenRef\.current \+= 1;\s*\n\s*const myGen = rebuildGenRef\.current;/, 'each invocation claims a fresh generation, so an older chain self-aborts before any persist');
    // And a duplicate persist of the SAME rebuilt state would carry a HIGHER
    // saveSeq, so the gate accepts it and the second write is a harmless
    // rewrite of identical data into a rotating slot.
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    const st = { ...initialState(), tick: 500 };
    assert.ok(persistSavepoint(storage, createSavepoint(st, [], now, 'v', null, 1), now));
    assert.ok(persistSavepoint(storage, createSavepoint(st, [], now, 'v', null, 2), now), 'a duplicate persist is accepted, not corrupting');
  });
});

// ---------------------------------------------------------------------------
// ATTACK 5 — MUTATION COVERAGE (R1 fix). ORIGINALLY the four tests the estate
// shipped caught ONE of the four obvious mutations of the fix, because the
// wiring proof's regexes were unanchored `[\s\S]*?` spans that happily leaked
// out of the failure branch into one of onRebuild's THREE OTHER
// `setRebuildPhase('prompt')` sites or FOUR OTHER `recordError(` sites:
//
//   M1  discard the boolean again ....................... CAUGHT (wiring proof)
//   M2  delete `mirrorAfterPersist(rebuildPersisted, …)`  SURVIVED — 4/4 green
//   M3  delete the failure-path `recordError(…)` ........ SURVIVED — 4/4 green
//   M4  failure path -> setRebuildPhase('report') ....... SURVIVED — 4/4 green (the fatal one — the LITERAL BUG-436 defect, restored, with a green suite)
//
// R1 FIX: every assertion below is now brace-matched to the EXACT failure
// branch (`failureBranch()`), so a mutation anywhere in that scoped chunk
// reddens the corresponding test. Re-run against each mutation to confirm:
// M1-M4 are all now CAUGHT (see the fix report for the per-mutation log).
// ---------------------------------------------------------------------------

describe('ATTACK 5: mutation coverage of the shipped BUG-436 tests', () => {
  /** The failure branch ONLY — from `if (!rebuildPersisted) {` to its closing
   * brace — so no assertion below can leak into a sibling branch. */
  function failureBranch() {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    const start = onRebuild.indexOf('if (!rebuildPersisted) {');
    assert.notEqual(start, -1, 'the fail-closed guard must exist');
    // Brace-match from the guard's `{` so the slice is exactly the branch body.
    let i = onRebuild.indexOf('{', start);
    let depth = 0;
    for (let j = i; j < onRebuild.length; j++) {
      if (onRebuild[j] === '{') depth++;
      else if (onRebuild[j] === '}') {
        depth--;
        if (depth === 0) return onRebuild.slice(start, j + 1);
      }
    }
    throw new Error('unbalanced braces in onRebuild failure branch');
  }

  test('KILLS M4: the failure branch itself must set phase \'prompt\' and must NOT set \'report\' (the shipped wiring proof matches a sibling branch and lets the literal BUG-436 defect back in)', () => {
    const branch = failureBranch();
    assert.match(branch, /setRebuildPhase\('prompt'\)/, "the failure branch must fall back to 'prompt'");
    assert.doesNotMatch(branch, /setRebuildPhase\('report'\)/, 'a failed persist must NEVER reach the report phase — this is BUG-436 itself');
    assert.match(branch, /\breturn;/, 'and it must return, never fall through to the success tail');
  });

  test('KILLS M3: the failure branch itself must recordError (the shipped proof matches the crashed branch\'s recordError instead)', () => {
    const branch = failureBranch();
    assert.match(branch, /recordError\(/, 'a failed rebuild persist must surface loudly from THIS branch, not silently');
  });

  test('KILLS M2 (updated for F3): the failure branch must NEVER mirror, and the success tail must mirror exactly once', () => {
    const onRebuild = STORE_SRC.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/)[0];
    const branch = failureBranch();
    // F3 fix: deleting mirrorAfterPersist from the failure branch is now the
    // CORRECT shape (there is nothing to delete — it must not be there), so
    // the mutation this kills is the opposite: ADDING mirrorAfterPersist back
    // into the failure branch, which would resurrect F3 (mirroring a write
    // that was just declared to have failed into the PRIMARY IDB boot store).
    assert.doesNotMatch(
      branch,
      /mirrorAfterPersist\(/,
      'the failure branch must never call mirrorAfterPersist — mirroring a declared-failed write would resurrect FINDING F3',
    );
    // The mirror must still be wired, but ONLY once, on the success tail
    // after the failure branch, fed the literal `true` outcome.
    const successTail = onRebuild.slice(onRebuild.indexOf(branch) + branch.length);
    assert.match(
      successTail,
      /mirrorAfterPersist\(true, rebuiltSave\)/,
      'the IDB mirror must be wired on the confirmed-success tail (FEAT-2326609780 inc2 single-call-site pattern)',
    );
    const mirrorCalls = onRebuild.match(/mirrorAfterPersist\(/g) ?? [];
    assert.equal(mirrorCalls.length, 1, 'exactly one mirrorAfterPersist call in the whole completion branch — on success only');
  });
});
