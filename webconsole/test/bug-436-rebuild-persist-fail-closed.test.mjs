// bug-436-rebuild-persist-fail-closed.test.mjs — BUG-436: "rebuild completes but
// the rebuilt city FAILS".
//
// ROOT CAUSE (store.tsx onRebuild's chunk-done completion branch, ~line 2612):
// every OTHER `persistSavepoint` call site in this file follows the single
// call-site pattern documented at `mirrorAfterPersist`'s definition — check the
// boolean, recordError + do NOT proceed on failure, and mirror to IndexedDB
// either way. onRebuild's completion branch was the one holdout: it called
// `persistSavepoint(window.localStorage, rebuiltSave)`, threw the boolean away,
// and *unconditionally* flipped `rebuildPhase` to `'report'`.
//
// The rebuilt city is NEVER swapped into React state directly by design — the
// report modal's Resume action reloads from the disk savepoint written right
// here. So when the write silently failed (storage quota, or BUG-469's
// stale-overwrite guard rejecting a write that lost a race), the report modal
// still told the player the rebuild succeeded, but Resume/reload restored the
// OLD pre-rebuild savepoint — exactly BUG-436's dogfood symptom (rebuild
// "completes", the rebuilt city silently never exists on disk, the next boot
// resurrects the old one).
//
// This file proves the divergence at the data layer (the same OLD-vs-NEW
// savepoint the store.tsx logic reads/writes) and then pins the actual fixed
// source wiring, mirroring BUG-439's wiring-proof idiom: onRebuild is an
// unexported closure inside the SimProvider component, so a black-box test of
// replay.ts alone cannot see whether store.tsx actually checks the boolean.

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
import { initialState, reducer } from '../src/sim/engine.ts';

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
  };
}

/** A storage whose writes always throw QuotaExceededError-shaped errors, like a
 * genuinely full localStorage — exercises the SAME failure `safeSetItem` (and
 * therefore `persistSavepoint`) already handles by returning `false` rather
 * than throwing. */
function quotaWedgedStorage(seedFrom) {
  const base = seedFrom ?? memStorage();
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

/** Reproduces store.tsx onRebuild's completion branch AS WRITTEN (both the
 * pre-fix and post-fix shape are exercised via `failClosed`), against the
 * SAME real `persistSavepoint`/`createSavepoint` functions the live code
 * calls — so this proves the actual data-layer consequence, not a re-typed
 * summary of it.
 *
 * BUG-436 round-4 fix (fix contract item 1, superseding the round F1/F2/F3
 * "try plain, force on stale-overwrite" shape): the rebuild boundary now
 * forces UNCONDITIONALLY via `persistSavepointForced` — round 4's
 * F-R3-1/F-R3-1b attack proved the old fallback never even fired on a
 * legacy install with any free/torn slot (the plain gate's staleness check
 * is skipped entirely for an empty target, so it never refuses). Any
 * genuine storage error still fails closed, and the failure path never
 * mirrors to the durable store (F3) — modelled here as `mirrored: false` vs
 * `mirrored: true`. */
function simulateOnRebuildCompletion({ storage, rebuiltState, running, failClosed }) {
  const rebuiltSave = createSavepoint(rebuiltState, [], new Date(2026, 8, 4, 11), running, null, 6);
  if (!failClosed) {
    // THE BUG: the boolean is discarded — always report success regardless of
    // whether `rebuiltSave` actually reached disk.
    persistSavepoint(storage, rebuiltSave);
    return { phase: 'report', error: false, mirrored: true };
  }
  // THE FIX: only claim success (and only let Resume rely on the disk
  // savepoint) when the write actually landed — forcing unconditionally
  // since a rebuild is a deliberate replace-the-city boundary, never
  // bypassing a genuine storage error.
  const result = persistSavepointForced(storage, rebuiltSave);
  if (!result.ok) {
    return { phase: 'prompt', error: true, mirrored: false, reason: result.reason };
  }
  return { phase: 'report', error: false, mirrored: true };
}

describe('BUG-436: rebuild completes but the rebuilt city fails — onRebuild must check persistSavepoint, not discard it', () => {
  test('RED proof: discarding the persist boolean (the pre-fix shape) claims success even when the rebuilt city never reached disk, so Resume resurrects the OLD city', () => {
    const storage = memStorage();

    // The OLD (pre-rebuild) city, already on disk — this is what Resume must
    // NOT silently fall back to.
    const oldCity = { ...initialState(), tick: 100, funds: 1000 };
    assert.ok(persistSavepoint(storage, createSavepoint(oldCity, [], new Date(2026, 8, 4, 10), 'v-old', null, 5)));

    // The rebuild runs, producing a genuinely different (rebuilt) city...
    const rebuiltState = { ...initialState(), tick: 9999, funds: 42 };

    // ...but the disk write for it hits a wedged quota, exactly like Aaron's
    // dogfood localStorage that was already over quota per BUG-436's own
    // evidence dump (errors[0] type=reset-abort, "metropolis.preWipeArchive
    // exceeded the quota").
    const wedged = quotaWedgedStorage(storage);
    const result = simulateOnRebuildCompletion({
      storage: wedged,
      rebuiltState,
      running: 'v-new',
      failClosed: false, // the bug: boolean discarded
    });

    // BUG PRESENT: phase claims success...
    assert.equal(result.phase, 'report', 'bug-present shape: completion branch always reports success');

    // ...but the disk still only has the OLD city. Resume/reload would read
    // THIS and silently resurrect it — the rebuild "completed" for nothing.
    const onDisk = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), undefined));
    assert.equal(onDisk.buildVersion, 'v-old', 'BUG-436: the rebuilt city never reached disk — Resume would restore the pre-rebuild city');
    assert.equal(onDisk.snapshotTick, 100, 'the tick on disk is still the OLD city, not the rebuilt one (tick 9999)');
  });

  test('GREEN proof: checking the persist boolean (the fixed shape) refuses to report success when the write failed, and never claims the rebuilt city landed', () => {
    const storage = memStorage();
    const oldCity = { ...initialState(), tick: 100, funds: 1000 };
    assert.ok(persistSavepoint(storage, createSavepoint(oldCity, [], new Date(2026, 8, 4, 10), 'v-old', null, 5)));

    const rebuiltState = { ...initialState(), tick: 9999, funds: 42 };
    const wedged = quotaWedgedStorage(storage);
    const result = simulateOnRebuildCompletion({
      storage: wedged,
      rebuiltState,
      running: 'v-new',
      failClosed: true, // the fix
    });

    assert.equal(result.phase, 'prompt', 'FIX: a failed persist must NOT proceed to the report/resume flow');
    assert.equal(result.error, true, 'FIX: a failed persist must surface loudly (recordError), never silently');

    // Still only the OLD city on disk — but now that is what the phase HONESTLY
    // reflects (no rebuild-succeeded claim was made).
    const onDisk = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), undefined));
    assert.equal(onDisk.snapshotTick, 100, 'sanity: nothing on disk changed — the old city is intact, as the fix promises');
  });

  test('control: a HEALTHY persist (no quota wedge) still reports success under the fixed shape — the fix does not regress the happy path', () => {
    const storage = memStorage();
    const rebuiltState = { ...initialState(), tick: 9999, funds: 42 };
    const result = simulateOnRebuildCompletion({
      storage, // healthy, unwedged
      rebuiltState,
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(result.phase, 'report', 'a successful rebuild persist must still reach the report phase');
    assert.equal(result.error, false);

    const onDisk = mostRecentSavepoint(readAllSavepoints(storage, new Date(2026, 8, 4, 12), undefined));
    assert.equal(onDisk.snapshotTick, 9999, 'the rebuilt city is genuinely on disk when the write actually succeeds');
  });

  // ---------------------------------------------------------------------
  // R1 fix (round REJECT finding): the unanchored `[\s\S]*?` wiring-proof
  // regexes below used to span clean OUT of the failure branch into one of
  // onRebuild's THREE OTHER `setRebuildPhase('prompt')` sites or FOUR OTHER
  // `recordError(` sites, so all four of the round's mutations (discard the
  // boolean, delete mirrorAfterPersist, delete recordError, report-on-
  // failure) left every assertion green. `failureBranch()` brace-matches
  // the EXACT `if (!rebuildPersisted) { ... }` chunk so no assertion below
  // can leak into a sibling branch — mirrors attack-bug-436-round.test.mjs's
  // ATTACK 5 idiom, duplicated here so the shipped estate's own proof is
  // self-sufficient (not dependent on the round's separate attack file).
  // ---------------------------------------------------------------------
  function onRebuildBodySrc() {
    const storePath = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'sim', 'store.tsx');
    const src = fs.readFileSync(storePath, 'utf8');
    const onRebuildBody = src.match(/const onRebuild = \(\) => \{[\s\S]*?\n  \};\n/);
    assert.ok(onRebuildBody, 'onRebuild must exist in store.tsx');
    return onRebuildBody[0];
  }

  function failureBranchSrc(body) {
    const start = body.indexOf('if (!rebuildPersisted) {');
    assert.notEqual(start, -1, 'the fail-closed guard must exist');
    let i = body.indexOf('{', start);
    let depth = 0;
    for (let j = i; j < body.length; j++) {
      if (body[j] === '{') depth++;
      else if (body[j] === '}') {
        depth--;
        if (depth === 0) return body.slice(start, j + 1);
      }
    }
    throw new Error('unbalanced braces in onRebuild failure branch');
  }

  test('wiring proof: onRebuild forces UNCONDITIONALLY (round-4), never on a plain boolean discard', () => {
    // Round-4 superseded the F1/F2 "try plain, force only on stale-overwrite"
    // shape: F-R3-1/F-R3-1b proved that fallback never fires on a legacy
    // install with any free/torn slot (the plain gate's staleness check is
    // skipped entirely for an empty target, so it never refuses, so force —
    // and its re-stamp walk — never ran). Rebuild is a deliberate
    // replace-the-city boundary, so it now forces unconditionally.
    const body = onRebuildBodySrc();
    assert.doesNotMatch(
      body,
      /persistSavepoint\(window\.localStorage,\s*rebuiltSave\)/,
      'BUG-436: the boolean-only persistSavepoint call site must be gone',
    );
    assert.match(
      body,
      /const\s+rebuildResult\s*=\s*persistSavepointForced\(window\.localStorage,\s*rebuiltSave\)/,
      'BUG-436 round-4: onRebuild must force the rebuild write unconditionally, not try the plain gate first',
    );
    assert.match(
      body,
      /const\s+rebuildPersisted\s*=\s*rebuildResult\.ok/,
      'the final outcome boolean must come from the forced result, not be discarded',
    );
  });

  test('KILLS M4 (scoped, behavioral): the failure branch itself sets phase \'prompt\', never \'report\'', () => {
    const branch = failureBranchSrc(onRebuildBodySrc());
    assert.match(branch, /setRebuildPhase\('prompt'\)/, "the failure branch must fall back to 'prompt'");
    assert.doesNotMatch(branch, /setRebuildPhase\('report'\)/, 'a failed persist must NEVER reach the report phase — this is BUG-436 itself');
    assert.match(branch, /\breturn;/, 'and it must return, never fall through to the success tail');

    // Behavioral pin, not just source-regex: the SAME real functions the
    // failure branch calls must actually land on 'prompt' for a genuine
    // storage-error, exercised end to end via simulateOnRebuildCompletion.
    const storage = memStorage();
    const wedged = quotaWedgedStorage(storage);
    const result = simulateOnRebuildCompletion({
      storage: wedged,
      rebuiltState: { ...initialState(), tick: 9999 },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(result.phase, 'prompt', 'a real failed persist must behaviorally land on prompt, not report');
  });

  test('KILLS M3 (scoped, behavioral): the failure branch itself calls recordError, not a sibling branch\'s call', () => {
    const branch = failureBranchSrc(onRebuildBodySrc());
    assert.match(branch, /recordError\(/, 'a failed rebuild persist must surface loudly from THIS branch, not silently');

    const storage = memStorage();
    const wedged = quotaWedgedStorage(storage);
    const result = simulateOnRebuildCompletion({
      storage: wedged,
      rebuiltState: { ...initialState(), tick: 9999 },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(result.error, true, 'the real failure path must surface an error, behaviorally, not just per-regex');
  });

  test('KILLS M2 (scoped, behavioral): mirrorAfterPersist is wired on the SUCCESS tail and must NOT fire on the failure branch (F3)', () => {
    const body = onRebuildBodySrc();
    const branch = failureBranchSrc(body);
    assert.doesNotMatch(
      branch,
      /mirrorAfterPersist\(/,
      'F3 fix: the failure branch must never mirror a failed primary write into the IDB PRIMARY boot store',
    );
    assert.match(
      body.slice(body.indexOf(branch) + branch.length),
      /mirrorAfterPersist\(true, rebuiltSave\)/,
      'the mirror must still run, but only on the success tail, fed a literal `true` (mirrored state IS successfully persisted here)',
    );

    // Behavioral: a real failed persist must not leave the rebuilt city
    // reachable as "mirrored" in this test's own model of the branch.
    const storage = memStorage();
    const wedged = quotaWedgedStorage(storage);
    const failResult = simulateOnRebuildCompletion({
      storage: wedged,
      rebuiltState: { ...initialState(), tick: 9999 },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(failResult.mirrored, false, 'F3: a failed persist must not be mirrored anywhere');

    const healthy = memStorage();
    const okResult = simulateOnRebuildCompletion({
      storage: healthy,
      rebuiltState: { ...initialState(), tick: 9999 },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(okResult.mirrored, true, 'control: a real successful persist IS mirrored');
  });

  test('F1 regression (round repro shape): a legacy install with ring-capped-journal-shaped lower tick now COMPLETES its rebuild via the forced write', () => {
    // Exactly the round's ATTACK 1 F1 shape: three occupied slots of the
    // SAME (legacy) lineage written before saveSeq existed (explicit
    // `undefined`), at a high tick — and a healthy, lower-tick rebuild
    // (the normal, healthy consequence of JOURNAL_CAP eviction on a
    // long-lived city, not a defect).
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    for (let i = 0; i < 3; i++) {
      const sp = createSavepoint(
        { ...initialState(), tick: 61000 + i },
        [],
        new Date(now.getTime() - (3 - i) * 60_000),
        'v-old',
        null,
        undefined, // pre-round-3: no saveSeq
      );
      assert.ok(persistSavepoint(storage, sp, now), `legacy slot ${i} must seed`);
    }

    const rebuiltState = { ...initialState(), tick: 11000, buildings: [] };
    const result = simulateOnRebuildCompletion({
      storage,
      rebuiltState,
      running: 'v-new',
      failClosed: true,
    });

    assert.equal(result.phase, 'report', 'F1 fix: the healthy rebuild now COMPLETES instead of being refused as stale-overwrite');
    assert.equal(result.error, false);
    assert.equal(result.mirrored, true);

    // NOTE: `mostRecentSavepoint` itself is savedAt-only (a naive wall-clock
    // pick, not saveSeq-aware — this simulate helper hardcodes an early
    // `savedAt` for the rebuilt save purely as a fixture artifact, unlike the
    // real store.tsx call site which stamps `new Date()`), so the disk-level
    // proof here checks directly for the rebuilt slot's PRESENCE and its
    // minted saveSeq rather than relying on that comparator.
    const onDiskAll = readAllSavepoints(storage, now, undefined);
    const rebuiltOnDisk = onDiskAll.find((sp) => sp.snapshotTick === 11000);
    assert.ok(rebuiltOnDisk, 'the REBUILT (lower-tick) city landed on disk — the forced write superseded one of the old high-tick slots');
    assert.ok(
      Number.isFinite(rebuiltOnDisk.saveSeq) && rebuiltOnDisk.saveSeq > 0,
      'the forced write minted a coherent saveSeq rather than leaving it stale/absent',
    );
    // And it genuinely replaced (not merely added alongside) one of the
    // three legacy occupied slots — the rotation cap is still respected.
    assert.equal(onDiskAll.length, 3, 'the SAVEPOINT_CAP rotation is respected — the rebuilt save took an existing slot, not a fourth one');

    // And it is durably repeatable — pressing Rebuild again on the (now
    // rebuilt, still-legacy-lineage) city keeps completing, never wedging.
    const again = simulateOnRebuildCompletion({
      storage,
      rebuiltState: { ...initialState(), tick: 11500, buildings: [] },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(again.phase, 'report', 'a second rebuild also completes — the fix is not a one-shot escape hatch');
  });

  // ---------------------------------------------------------------------
  // F-R3-3 (round 4): absorb the round-3 attacker's re-seed and
  // below-ceiling coverage into the ESTATE's OWN suite, not only the
  // attack file — a regression in the unconditional-force fix or the
  // re-stamp walk must redden HERE too.
  // ---------------------------------------------------------------------

  test('F-R3-1 regression, absorbed into the estate: a legacy install with ONE FREE SLOT still gets every occupied slot re-stamped, so post-rebuild autosaves keep landing', () => {
    // Round-3 found that a free target slot let the unforced write succeed
    // outright, skipping the re-stamp walk entirely — every subsequent
    // autosave was then refused forever against a surviving legacy slot.
    // Round 4 fixed this by forcing UNCONDITIONALLY at the rebuild
    // boundary; pin the fix here too, not only in the attack file.
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    // Only 2 of the 3 SAVEPOINT_CAP slots occupied — one slot is free.
    for (let i = 0; i < 2; i++) {
      const sp = createSavepoint(
        { ...initialState(), tick: 61000 + i },
        [],
        new Date(now.getTime() - (2 - i) * 60_000),
        'v-old',
        null,
        undefined, // legacy shape: no saveSeq
      );
      assert.ok(persistSavepoint(storage, sp, now), `legacy slot ${i} must seed`);
    }

    const result = simulateOnRebuildCompletion({
      storage,
      rebuiltState: { ...initialState(), tick: 120, buildings: [] },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(result.phase, 'report', 'the rebuild lands in the free slot');

    // Every OTHER occupied (legacy) slot must now carry a finite saveSeq —
    // proof the re-stamp walk ran even though the target slot was free.
    const onDisk = readAllSavepoints(storage, now, undefined);
    const others = onDisk.filter((sp) => sp.snapshotTick !== 120);
    assert.ok(others.length >= 1, 'fixture must still have surviving legacy slots to check');
    for (const o of others) {
      assert.ok(Number.isFinite(o.saveSeq), `legacy slot at tick ${o.snapshotTick} must carry a finite saveSeq after the walk`);
    }

    // And a subsequent ordinary (unforced) autosave at a low post-rebuild
    // tick must actually LAND — the RR-1/F-R3-1 symptom was a permanent
    // refusal here.
    const autosave = createSavepoint({ ...initialState(), tick: 121 }, [], new Date(now.getTime() + 60_000), 'v-new', null, 7);
    assert.ok(
      persistSavepoint(storage, autosave, new Date(now.getTime() + 60_000)),
      'F-R3-1: the first post-rebuild autosave must land, not be refused against a stale legacy slot',
    );
  });

  test('R3-2 regression, absorbed into the estate: duplicate/at-ceiling saveSeqs in the surviving slots end up strictly below the minted ceiling', () => {
    // Round-3's R3-2/R3-2b coverage: the surviving slots' saveSeq must never
    // be left AT or ABOVE the newly minted lineage-authority value, however
    // it got there (a duplicate seq, or a seq that happens to equal the
    // rebuild's own). Pinned here so a regression in the walk's
    // strictly-below invariant reddens the estate suite too.
    const storage = memStorage();
    const now = new Date(2026, 8, 4, 12);
    for (let i = 0; i < 3; i++) {
      const sp = createSavepoint({ ...initialState(), tick: 61000 + i }, [], new Date(now.getTime() - (3 - i) * 60_000), 'v-old', null, 5);
      assert.ok(persistSavepoint(storage, sp, now), `duplicate-seq slot ${i} must seed`);
    }

    const result = simulateOnRebuildCompletion({
      storage,
      rebuiltState: { ...initialState(), tick: 200, buildings: [] },
      running: 'v-new',
      failClosed: true,
    });
    assert.equal(result.phase, 'report', 'forced rebuild over duplicate-seq slots must still complete');

    const onDisk = readAllSavepoints(storage, now, undefined);
    const rebuilt = onDisk.find((sp) => sp.snapshotTick === 200);
    assert.ok(rebuilt, 'rebuilt city present on disk');
    const ceiling = rebuilt.saveSeq;
    for (const o of onDisk.filter((sp) => sp.snapshotTick !== 200)) {
      assert.ok(Number.isFinite(o.saveSeq), 'every surviving slot must carry a finite saveSeq');
      assert.ok(o.saveSeq < ceiling, `surviving slot seq ${o.saveSeq} must end strictly below the minted ceiling ${ceiling}`);
    }
  });
});
