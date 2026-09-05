// attack-bug-436-round5.test.mjs — INDEPENDENT DESTRUCTIVE ROUND 5 (VERDICT
// ROUND) on the BUG-436 estate (attacker: opus-round5-bug436; NOT the author,
// NOT round 1's / the re-round's / round 3's attacker).
//
// Rounds 1, 2 (re-round) and 3 all REJECTED. The round-4 rework's headline
// claims are:
//   (a) the mint + re-stamp walk is gated on `opts?.force` ALONE (hoisted out
//       of `if (target.sp)`), so a FREE or TORN target slot no longer skips it
//       — the round-3 F-R3-1/F-R3-1b kill shot;
//   (b) store.tsx onRebuild calls `persistSavepointForced` UNCONDITIONALLY at
//       the rebuild boundary;
//   (c) per-slot walk failures are RECORDED on the result as
//       `restampFailures` (after one internal retry) and SURFACED by
//       onRebuild via recordError instead of being swallowed — the round-3
//       F-R3-2 kill shot's remedy contract item (2).
//
// Round 5's mutation union found (a) and (b) well pinned, but (c) COMPLETELY
// UNCOVERED: deleting the `restampFailures` reporting from replay.ts, deleting
// the `restampFailures.push`, or disabling the store-side surfacing all leave
// the estate 46/46 GREEN. `grep -rn restampFailures test/` returns nothing.
// F-R3-2 is green ONLY because of the one-shot RETRY (its quota wedge is
// one-shot), so the reporting half of the remedy was shipped untested.
//
// This file is the missing contract. Every test below is written against the
// PRODUCTION functions (and a source pin for the store side), and each one was
// verified to RED under the corresponding mutation before being committed to
// the estate.

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
  SAVEPOINT_CAP,
  SAVEPOINT_KEY_PREFIX,
  CURRENT_LINEAGE_KEY,
} from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STORE_TSX = path.join(__dirname, '..', 'src', 'sim', 'store.tsx');
const REPLAY_TS = path.join(__dirname, '..', 'src', 'sim', 'replay.ts');

const BASE = Date.parse('2026-09-01T00:00:00Z');
const ms = (min) => new Date(BASE + min * 60_000);

/**
 * Storage double with two independent wedges:
 *  - `failSavepointWrite: N` — the Nth savepoint write throws (one-shot, the
 *    round-3 shape; a retry SUCCEEDS against this);
 *  - `failKeysForever: Set` — every write to those keys throws, always (the
 *    shape a retry can NOT paper over: a genuinely full origin, which is
 *    BUG-436's own original trigger).
 */
function memStorage() {
  const m = new Map();
  const api = {
    writes: [],
    failSavepointWrite: 0,
    _spWrites: 0,
    failKeysForever: new Set(),
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => {
      api.writes.push(k);
      if (api.failKeysForever.has(k)) {
        const e = new Error('QuotaExceededError');
        e.name = 'QuotaExceededError';
        throw e;
      }
      if (k.startsWith('metropolis.savepoint') && api.failSavepointWrite > 0) {
        api._spWrites++;
        if (api._spWrites === api.failSavepointWrite) {
          const e = new Error('QuotaExceededError');
          e.name = 'QuotaExceededError';
          throw e;
        }
      }
      m.set(k, String(v));
    },
    removeItem: (k) => void m.delete(k),
    _raw: (k) => (m.has(k) ? m.get(k) : null),
    _snapshotAll: () => Object.fromEntries(Array.from(m.entries()).sort()),
  };
  return api;
}

function seqRef(boot) {
  let cur = Number.isFinite(boot) ? boot : 0;
  return { next: () => ++cur, get: () => cur, set: (v) => (cur = v) };
}

/** Legacy install: saves with NO saveSeq, at high ticks. */
function seedLegacyInstall(storage, ticks, lineageId) {
  ticks.forEach((tick, i) => {
    const st = { ...initialState(), tick, ...(lineageId ? { lineageId } : {}) };
    const sp = createSavepoint(st, [], ms(i), 'v-old', null, undefined);
    assert.equal(sp.saveSeq, undefined, 'fixture must carry NO saveSeq (legacy shape)');
    assert.ok(persistSavepoint(storage, sp, ms(i)), `legacy seed ${i} must land`);
  });
}

/** Modern install: saves carrying explicit saveSeqs. */
function seedModernInstall(storage, ticks) {
  ticks.forEach((tick, i) => {
    const sp = createSavepoint({ ...initialState(), tick }, [], ms(i), 'v-old', null, i + 1);
    assert.ok(persistSavepoint(storage, sp, ms(i)), `modern seed ${i} must land`);
  });
}

/**
 * store.tsx onRebuild completion branch, AS WRITTEN post-round-4, INCLUDING
 * the `restampFailures` surfacing that the estate never modelled. `errors` is
 * the recordError sink.
 */
function onRebuildCompletion(storage, rebuiltState, ref, now, errors = []) {
  const save = createSavepoint(rebuiltState, [], now, 'v-new', null, ref.next());
  const r = persistSavepointForced(storage, save, now);
  if (!r.ok) {
    errors.push({ kind: 'rebuild-failed', reason: r.reason });
    return { ok: false, reason: r.reason, save, phase: 'prompt', mirrored: false, errors };
  }
  if (r.restampFailures && r.restampFailures.length > 0) {
    errors.push({ kind: 'restamp-partial', count: r.restampFailures.length, slots: r.restampFailures.map((f) => f.slot) });
  }
  if (Number.isFinite(save.saveSeq) && save.saveSeq > ref.get()) ref.set(save.saveSeq);
  return { ok: true, restampFailures: r.restampFailures, save, phase: 'report', mirrored: true, errors };
}

function autosave(storage, state, ref, now) {
  const sp = createSavepoint(state, [], now, 'v-new', null, ref.next());
  return persistSavepoint(storage, sp, now);
}

function autosaveRun(storage, ref, startTick, n) {
  let landed = 0;
  for (let i = 0; i < n; i++) {
    const st = { ...initialState(), tick: startTick + i };
    if (autosave(storage, st, ref, ms(50 + i))) landed++;
  }
  return landed;
}

// ===========================================================================
describe('BUG-436 ROUND 5 — the restampFailures reporting contract (was UNCOVERED)', () => {
  // -------------------------------------------------------------------------
  // R5-1 (the gap): a PERSISTENT re-stamp write failure — one the one-shot
  // retry cannot paper over, i.e. a genuinely full origin, which is BUG-436's
  // own original trigger — MUST be reported on the result. Deleting the
  // reporting (`return { ok: true }`) or the `restampFailures.push` left the
  // whole estate green before this test existed.
  // -------------------------------------------------------------------------
  test('R5-1: a PERSISTENTLY failing re-stamp write is reported in restampFailures, not swallowed', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    // Slot 2 holds the newest legacy save (savedAt ms(2)); slot 0 is the oldest
    // and therefore the forced write's target. Wedge slot 1 permanently.
    st.failKeysForever.add(`${SAVEPOINT_KEY_PREFIX}.1`);

    const save = createSavepoint({ ...initialState(), tick: 120 }, [], ms(10), 'v-new', null, 1);
    const r = persistSavepointForced(st, save, ms(10));

    assert.equal(r.ok, true, 'the PRIMARY write still lands — a partial walk must not block the rebuild');
    assert.ok(Array.isArray(r.restampFailures), 'restampFailures MUST be present when a walk write fails permanently');
    assert.equal(r.restampFailures.length, 1, 'exactly the one wedged slot is reported');
    assert.equal(r.restampFailures[0].slot, 1, 'the reported slot number identifies the un-restamped slot');
    assert.ok(typeof r.restampFailures[0].reason === 'string' && r.restampFailures[0].reason.length > 0,
      'each failure carries a non-empty reason string');
  });

  // -------------------------------------------------------------------------
  // R5-1b: and the consequence the report exists to warn about is REAL — the
  // un-restamped slot does go on to refuse future autosaves. This is what
  // makes R5-1 a P1 honesty contract rather than cosmetics.
  // -------------------------------------------------------------------------
  test('R5-1b: the reported slot genuinely does refuse later autosaves (the warning is not cosmetic)', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    st.failKeysForever.add(`${SAVEPOINT_KEY_PREFIX}.1`);
    const ref = seqRef(undefined);
    const errors = [];
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10), errors);
    assert.ok(r.ok, 'rebuild lands');
    assert.equal(errors.length, 1, 'onRebuild must recordError EXACTLY the partial-walk warning');
    assert.equal(errors[0].kind, 'restamp-partial');
    assert.deepEqual(errors[0].slots, [1]);
    assert.equal(r.phase, 'report', 'a PARTIAL walk still completes the rebuild — warn, do not block');

    // Slot 1 still carries a no-saveSeq/high-tick legacy save, so once rotation
    // reaches it the tick fallback refuses. Prove the refusal is real.
    const landed = autosaveRun(st, ref, 121, 6);
    assert.ok(landed < 6, `the warning corresponds to a REAL refusal (${landed}/6 landed) — if this ever becomes 6/6 the reporting contract needs re-deriving, not deleting`);
  });

  // -------------------------------------------------------------------------
  // R5-2: the UNFORCED path must NEVER carry restampFailures — the field is a
  // force-path-only signal, and a stray value would make every ordinary
  // autosave look like a partial rebuild to the store's surfacing branch.
  // -------------------------------------------------------------------------
  test('R5-2: the unforced path never sets restampFailures', () => {
    const st = memStorage();
    seedModernInstall(st, [10, 20]);
    const sp = createSavepoint({ ...initialState(), tick: 30 }, [], ms(5), 'v-new', null, 9);
    const r = persistSavepointWithReason(st, sp, ms(5));
    assert.equal(r.ok, true);
    assert.equal(r.restampFailures, undefined, 'unforced results must not carry the force-only field');
  });

  // -------------------------------------------------------------------------
  // R5-3: a fully SUCCESSFUL walk must not report a phantom empty list — the
  // store branches on `length > 0`, but an always-present empty array would
  // make the honest/partial distinction depend on a truthiness accident.
  // -------------------------------------------------------------------------
  test('R5-3: a clean forced walk reports no restampFailures at all', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    const save = createSavepoint({ ...initialState(), tick: 120 }, [], ms(10), 'v-new', null, 1);
    const r = persistSavepointForced(st, save, ms(10));
    assert.equal(r.ok, true);
    assert.equal(r.restampFailures, undefined, 'clean walk => field absent');
  });

  // -------------------------------------------------------------------------
  // R5-4: the store WIRING pin for the surfacing half. Anchored INSIDE the
  // success tail of the forced call site so a sibling branch's recordError
  // cannot satisfy it (round 1's M3/M4 lesson: unanchored regexes span sites).
  // -------------------------------------------------------------------------
  test('R5-4 WIRE: onRebuild surfaces restampFailures via recordError on the SUCCESS tail', () => {
    const src = fs.readFileSync(STORE_TSX, 'utf8');
    const i = src.indexOf('const rebuildResult = persistSavepointForced(window.localStorage, rebuiltSave);');
    assert.ok(i > 0, 'the unconditional forced call site must exist in onRebuild');
    const block = src.slice(i, i + 4000);
    // The surfacing must come AFTER the !ok early-return (i.e. on the success
    // tail) and must actually call recordError with the failure count.
    const failIdx = block.indexOf('rebuildResult.restampFailures');
    assert.ok(failIdx > 0, 'onRebuild must READ rebuildResult.restampFailures — MUT-6 (disabling this branch) left the estate green');
    const guard = block.slice(failIdx - 60, failIdx + 400);
    assert.match(guard, /restampFailures\.length > 0/, 'must branch on a NON-EMPTY failure list');
    assert.match(guard, /recordError\(/, 'a non-empty failure list must reach recordError, not a console log or nothing');
    assert.doesNotMatch(guard, /if \(false/, 'the surfacing branch must not be short-circuited');
  });

  // -------------------------------------------------------------------------
  // R5-5 WIRE: the retry itself. Round 5 measured that F-R3-2 is green because
  // of the retry, so the retry is load-bearing for the one-shot-quota case and
  // must be pinned in its own right (MUT-C reds F-R3-2, so it IS covered — this
  // pin makes the coverage explicit rather than incidental).
  // -------------------------------------------------------------------------
  test('R5-5 WIRE: the walk retries a failed slot write exactly once inside the force block', () => {
    const src = fs.readFileSync(REPLAY_TS, 'utf8');
    const i = src.indexOf('if (opts?.force)');
    assert.ok(i > 0);
    const block = src.slice(i, i + 3500);
    const total = block.split('res = safeSetItem(storage, key, encoded)').length - 1;
    const initial = block.split('let res = safeSetItem(storage, key, encoded)').length - 1;
    assert.equal(initial, 1, 'one initial per-slot write');
    assert.equal(total - initial, 1, 'exactly ONE bare retry re-assignment (a loop here would hammer a full origin)');
    assert.match(block, /restampFailures\.push\(/, 'a still-failing slot must be PUSHED, not swallowed (MUT-E left the estate green)');
  });
});

// ===========================================================================
describe('BUG-436 ROUND 5 — walk-runs-for-the-right-reason instrumentation', () => {
  // -------------------------------------------------------------------------
  // R5-6: checklist item 1 — prove F-R3-1/F-R3-1b are green because the WALK
  // RUNS on a free-slot forced write, not because the autosaves happened to
  // land for some other reason. Instrument via observed storage WRITES: a
  // forced write into a FREE slot must produce writes to the OTHER occupied
  // slot keys too.
  // -------------------------------------------------------------------------
  test('R5-6: forced write into a FREE slot still writes the other occupied slots (walk observed)', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000]); // 2 of 3 slots — slot 2 free
    st.writes.length = 0;
    const save = createSavepoint({ ...initialState(), tick: 120 }, [], ms(10), 'v-new', null, 1);
    const r = persistSavepointForced(st, save, ms(10));
    assert.equal(r.ok, true);
    const spWrites = st.writes.filter((k) => k.startsWith(SAVEPOINT_KEY_PREFIX));
    assert.ok(spWrites.includes(`${SAVEPOINT_KEY_PREFIX}.2`), 'the rebuild lands in the FREE slot 2');
    assert.ok(spWrites.includes(`${SAVEPOINT_KEY_PREFIX}.0`) && spWrites.includes(`${SAVEPOINT_KEY_PREFIX}.1`),
      'the WALK must also re-write slots 0 and 1 — this is the observable proof the walk ran, not just that autosaves happened to land');
    const all = readAllSavepoints(st, ms(11));
    assert.ok(all.every((sp) => Number.isFinite(sp.saveSeq)), 'every surviving slot now carries a finite saveSeq');
    const ceiling = save.saveSeq;
    assert.ok(all.filter((sp) => sp.snapshotTick !== 120).every((sp) => sp.saveSeq < ceiling),
      'every history slot sits strictly below the minted ceiling');
  });

  test('R5-6b: forced write with a TORN slot as target still writes the other occupied slots', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    st.setItem(`${SAVEPOINT_KEY_PREFIX}.1`, '{"savedAt":"2026-09-01T00:01:00.000Z","snapshotT');
    st.writes.length = 0;
    const save = createSavepoint({ ...initialState(), tick: 120 }, [], ms(10), 'v-new', null, 1);
    assert.equal(persistSavepointForced(st, save, ms(10)).ok, true);
    const spWrites = st.writes.filter((k) => k.startsWith(SAVEPOINT_KEY_PREFIX));
    assert.ok(spWrites.includes(`${SAVEPOINT_KEY_PREFIX}.0`) && spWrites.includes(`${SAVEPOINT_KEY_PREFIX}.2`),
      'the walk must re-stamp the two intact slots even though the target was the torn one');
  });
});

// ===========================================================================
describe('BUG-436 ROUND 5 — hostile states on the NEW unconditional-force path', () => {
  test('R5-7: FRESH INSTALL (all slots free) + force — walk over nothing, no crash', () => {
    const st = memStorage();
    const ref = seqRef(undefined);
    const errors = [];
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10), errors);
    assert.equal(r.ok, true, 'a fresh install must rebuild cleanly');
    assert.equal(r.restampFailures, undefined);
    assert.equal(errors.length, 0, 'no spurious error on a fresh install');
    assert.equal(readAllSavepoints(st, ms(11)).length, 1);
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'every post-rebuild autosave lands on a fresh install');
  });

  test('R5-8: ALL slots torn/corrupt + force — no crash, rebuild lands, autosaves land', () => {
    const st = memStorage();
    for (let s = 0; s < SAVEPOINT_CAP; s++) st.setItem(`${SAVEPOINT_KEY_PREFIX}.${s}`, '{"savedAt":"2026-0');
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.equal(r.ok, true, 'all-torn install must still rebuild');
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'and keep autosaving');
  });

  test('R5-9: quota fails on the PRIMARY forced write — fail-closed, honest, prompt fallback reachable', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    // Slot 0 is the oldest => the forced write's target. Wedge it forever.
    st.failKeysForever.add(`${SAVEPOINT_KEY_PREFIX}.0`);
    const ref = seqRef(undefined);
    const errors = [];
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10), errors);
    assert.equal(r.ok, false, 'a failed PRIMARY write must fail closed even on the forced path');
    assert.equal(r.reason, 'storage-error', 'and report the honest reason (never stale-overwrite on the forced path)');
    assert.equal(r.phase, 'prompt', 'the fail-closed prompt fallback is still reachable');
    assert.equal(r.mirrored, false, 'F3: a failed primary write is NEVER mirrored to the IDB primary boot store');
    assert.equal(errors.length, 1, 'the player is told');
    // And the OLD city survives — nothing claimed, nothing lost.
    const survivors = readAllSavepoints(st, ms(11));
    assert.ok(survivors.some((sp) => sp.snapshotTick === 7000), 'the pre-rebuild city is still on disk');
    assert.ok(!survivors.some((sp) => sp.snapshotTick === 120), 'the rebuilt city is NOT on disk — matching what the message says');
  });

  test('R5-10: a DIFFERENT lineage is untouched byte-for-byte by the (now more frequent) forced walk', () => {
    const st = memStorage();
    const OTHER = 'lineage-other-0000';
    seedLegacyInstall(st, [5000, 6000, 7000], OTHER);
    const otherKeys = Object.keys(st._snapshotAll()).filter((k) => k.includes(OTHER));
    assert.ok(otherKeys.length >= 3, 'the other lineage has slots');
    const before = Object.fromEntries(otherKeys.map((k) => [k, st._raw(k)]));

    // Now the LIVE lineage rebuilds (forced), twice, plus autosaves.
    const LIVE = 'lineage-live-00000';
    st.setItem(CURRENT_LINEAGE_KEY, LIVE);
    seedLegacyInstall(st, [8000, 9000], LIVE);
    const ref = seqRef(undefined);
    for (const t of [120, 121]) {
      const sp = createSavepoint({ ...initialState(), tick: t, lineageId: LIVE }, [], ms(10), 'v-new', null, ref.next());
      assert.ok(persistSavepointForced(st, sp, ms(10)).ok);
    }
    for (let i = 0; i < 6; i++) {
      const sp = createSavepoint({ ...initialState(), tick: 200 + i, lineageId: LIVE }, [], ms(50 + i), 'v-new', null, ref.next());
      persistSavepoint(st, sp, ms(50 + i));
    }
    const after = Object.fromEntries(otherKeys.map((k) => [k, st._raw(k)]));
    assert.deepEqual(after, before, 'cross-lineage slots must be byte-for-byte identical after repeated forced walks');
  });
});

// ===========================================================================
describe('BUG-436 ROUND 5 — full lifecycle on all three install shapes', () => {
  const shapes = [
    { name: 'legacy-full (all CAP slots, no saveSeq, high tick)', seed: (st) => seedLegacyInstall(st, [5000, 6000, 7000]) },
    { name: 'legacy-with-free-slot (F-R3-1 shape)', seed: (st) => seedLegacyInstall(st, [5000, 6000]) },
    { name: 'modern (explicit saveSeqs)', seed: (st) => seedModernInstall(st, [10, 20, 30]) },
  ];

  for (const shape of shapes) {
    test(`R5-11 lifecycle [${shape.name}]: rebuild -> 6 autosaves -> reload boots the rebuilt lineage's LATEST`, () => {
      const st = memStorage();
      shape.seed(st);
      const ref = seqRef(undefined);
      const errors = [];
      const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10), errors);
      assert.ok(r.ok, 'rebuild must land');
      assert.equal(errors.length, 0, 'a healthy install produces no rebuild error');
      assert.equal(autosaveRun(st, ref, 121, 6), 6, 'ALL SIX post-rebuild autosaves must land');

      const all = readAllSavepoints(st, ms(60));
      const boot = mostRecentSavepoint(all);
      assert.ok(boot, 'a boot savepoint exists');
      assert.equal(boot.snapshotTick, 126, 'reload boots the LAST autosave (121+5), never a resurrected pre-rebuild city');
      const maxSeq = all.reduce((m, sp) => Math.max(m, sp.saveSeq ?? -1), -1);
      assert.equal(boot.saveSeq, maxSeq, 'savedAt ordering and saveSeq ordering agree at boot');
      assert.ok(!all.some((sp) => sp.snapshotTick >= 5000), 'no pre-rebuild high-tick save survived rotation to resurrect later');
    });

    test(`R5-12 StrictMode [${shape.name}]: double-completion is idempotent and still boots the rebuilt city`, () => {
      const st = memStorage();
      shape.seed(st);
      const ref = seqRef(undefined);
      const a = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
      const b = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
      assert.ok(a.ok && b.ok, 'both StrictMode invocations must land');
      const seqs = readAllSavepoints(st, ms(20)).map((sp) => sp.saveSeq);
      assert.ok(seqs.every((s) => Number.isFinite(s)), 'no slot is left seq-less by a double run');
      assert.equal(new Set(seqs).size, seqs.length, 'no duplicate saveSeqs after a double completion');
      assert.equal(autosaveRun(st, ref, 121, 6), 6, 'autosaves still all land after a double completion');
      assert.equal(mostRecentSavepoint(readAllSavepoints(st, ms(60))).snapshotTick, 126);
    });
  }
});
