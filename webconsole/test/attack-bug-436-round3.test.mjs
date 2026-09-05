// attack-bug-436-round3.test.mjs — INDEPENDENT DESTRUCTIVE ROUND 3 on the
// BUG-436 estate (attacker: opus-round3-bug436; NOT the author, NOT round 1's
// attacker, NOT the re-round's attacker).
//
// Round 1 REJECTED (R1 mutation coverage, F1 healthy-rebuild-refused, F2 wrong
// remedy message, F3 failed-persist-still-mirrored, F5 stale report state).
// Round 2 (re-round) REJECTED for RR-1: the force branch wrote ONE slot, so on a
// legacy install the OTHER occupied slots kept their no-saveSeq/high-tick shape
// and every post-rebuild autosave was refused forever.
//
// The ROUND-3 rework's headline claim is the RE-STAMP WALK inside
// persistSavepointWithReason's `force` branch: every OTHER occupied same-lineage
// slot whose saveSeq is missing or >= the minted ceiling is re-stamped to a value
// strictly below it, plus a saveSeqRef re-seed in store.tsx's onRebuild. THAT WALK
// is this round's primary target.
//
// The store-side models below mirror store.tsx onRebuild / the autosave timer /
// saveSeqRef AS WRITTEN post-rework, because the defect class all three rounds
// have found lives in the seam between the store's ambient bookkeeping and the
// replay.ts gate.

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

function memStorage(opts = {}) {
  const m = new Map();
  const api = {
    writes: [],
    /** when set to N, the Nth SAVEPOINT write from now on throws QuotaExceeded */
    failSavepointWrite: 0,
    _spWrites: 0,
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => {
      api.writes.push(k);
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
    _keys: () => Array.from(m.keys()).sort(),
    _snapshotAll: () => Object.fromEntries(Array.from(m.entries()).sort()),
  };
  return api;
}

/** store.tsx's saveSeqRef: useRef(boot.bootSavepointMeta?.saveSeq ?? 0), nextSaveSeq = ++ref */
function seqRef(boot) {
  let cur = Number.isFinite(boot) ? boot : 0;
  return { next: () => ++cur, get: () => cur, set: (v) => (cur = v) };
}

/**
 * store.tsx onRebuild completion branch, AS WRITTEN post-round-4-rework: the
 * rebuild boundary now forces UNCONDITIONALLY (fix contract item 1) rather
 * than trying the plain gate first and falling back to force only on a
 * 'stale-overwrite' refusal — that fallback never fired on a legacy install
 * with any free/torn slot (F-R3-1/F-R3-1b), because the plain gate skips its
 * staleness check entirely for an empty target and succeeds outright.
 */
function onRebuildCompletion(storage, rebuiltState, ref, now) {
  const save = createSavepoint(rebuiltState, [], now, 'v-new', null, ref.next());
  const r = persistSavepointForced(storage, save, now);
  if (r.ok && Number.isFinite(save.saveSeq) && save.saveSeq > ref.get()) ref.set(save.saveSeq);
  return { ok: r.ok, reason: r.reason, save };
}

/** store.tsx autosave timer, AS WRITTEN (plain persistSavepoint, NO force). */
function autosave(storage, state, ref, now) {
  const sp = createSavepoint(state, [], now, 'v-new', null, ref.next());
  return { ok: persistSavepoint(storage, sp, now), sp };
}

/** Pre-round-3 legacy save shape: NO saveSeq at all, at high ticks. */
function seedLegacyInstall(storage, ticks, lineageId) {
  ticks.forEach((tick, i) => {
    const st = { ...initialState(), tick, ...(lineageId ? { lineageId } : {}) };
    const sp = createSavepoint(st, [], ms(i), 'v-old', null, undefined);
    assert.equal(sp.saveSeq, undefined, 'fixture must carry NO saveSeq (legacy shape)');
    assert.ok(persistSavepoint(storage, sp, ms(i)), `legacy seed ${i} must land`);
  });
}

/** Run N autosaves after a rebuild, return how many landed. */
function autosaveRun(storage, ref, startTick, n, lineageId) {
  let landed = 0;
  for (let i = 0; i < n; i++) {
    const st = { ...initialState(), tick: startTick + i, ...(lineageId ? { lineageId } : {}) };
    if (autosave(storage, st, ref, ms(50 + i)).ok) landed++;
  }
  return landed;
}

// ---------------------------------------------------------------------------
describe('BUG-436 ROUND 3 — the re-stamp walk under hostile slot states', () => {
  // -------------------------------------------------------------------------
  // R3-CONTROL: the shape the rework was designed for — ALL SAVEPOINT_CAP slots
  // occupied by legacy (no-saveSeq, high-tick) saves. The re-round's RE-3/RE-3b
  // contract. This MUST be green post-rework.
  // -------------------------------------------------------------------------
  test('R3-CONTROL: legacy install, ALL slots occupied -> forced rebuild + re-stamp -> every autosave lands', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok, 'forced rebuild must land');
    const landed = autosaveRun(st, ref, 121, 6);
    assert.equal(landed, 6, 'ALL post-rebuild autosaves must land (RE-3 contract)');
  });

  // -------------------------------------------------------------------------
  // F-R3-1 (P1 KILL SHOT): the re-stamp walk lives INSIDE `if (target.sp)`.
  // If ANY slot is free, `emptySlot` is chosen, `target.sp` is null, the whole
  // gate — staleness check AND re-stamp — is skipped, the first (unforced)
  // persist SUCCEEDS, so store.tsx never even calls persistSavepointForced.
  // The surviving legacy slots keep their no-saveSeq/high-tick shape and every
  // subsequent autosave is refused FOREVER — RR-1 verbatim, unfixed.
  //
  // A free slot is not exotic: a fresh-ish install that has autosaved fewer than
  // SAVEPOINT_CAP times, an install where BUG-469 retention purged a slot, or a
  // slot whose JSON is torn (readSlot degrades to null == "empty"), all land here.
  // -------------------------------------------------------------------------
  test('F-R3-1: legacy install with ONE FREE SLOT -> no force, no re-stamp -> post-rebuild autosaves refused forever', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000]); // 2 of 3 slots — one free
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok, 'rebuild lands in the free slot (unforced)');
    assert.equal(r.reason, undefined);
    const landed = autosaveRun(st, ref, 121, 6);
    assert.equal(landed, 6, `F-R3-1: expected all 6 post-rebuild autosaves to land, only ${landed} did — the re-stamp walk never ran because a free slot short-circuited the force branch (RR-1 unfixed for this install shape)`);
  });

  test('F-R3-1b: legacy install with a TORN/CORRUPT slot -> same permanent refusal', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    // Torn write: partial JSON. readSlot() degrades to null => slot reads EMPTY.
    st.setItem(`${SAVEPOINT_KEY_PREFIX}.1`, '{"savedAt":"2026-09-01T00:01:00.000Z","snapshotT');
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok, 'rebuild lands in the torn (== empty) slot');
    const landed = autosaveRun(st, ref, 121, 6);
    assert.equal(landed, 6, `F-R3-1b: only ${landed}/6 autosaves landed — a torn slot reads as empty, short-circuiting the force+re-stamp path`);
  });

  // -------------------------------------------------------------------------
  // R3-2: duplicate saveSeqs across the other slots must not break the walk's
  // strict-below invariant.
  // -------------------------------------------------------------------------
  test('R3-2: duplicate saveSeqs in other slots -> all end strictly below the minted ceiling, autosaves land', () => {
    const st = memStorage();
    // 3 occupied, all carrying the SAME saveSeq (impossible-but-storable state).
    // Written UNCOMPRESSED directly to the slot keys — decode() is a no-op on a
    // value with no LZv1: prefix, so this is a valid legacy-shaped slot.
    [5000, 6000, 7000].forEach((tick, i) => {
      const sp = createSavepoint({ ...initialState(), tick }, [], ms(i), 'v-old', null, 42);
      st.setItem(`${SAVEPOINT_KEY_PREFIX}.${i}`, JSON.stringify(sp));
    });
    const ref = seqRef(42);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok);
    const ceiling = r.save.saveSeq;
    const all = readAllSavepoints(st, ms(11));
    const others = all.filter((sp) => sp.snapshotTick !== 120);
    for (const o of others) {
      assert.ok(Number.isFinite(o.saveSeq), 'every other slot must carry a finite saveSeq after the walk');
      assert.ok(o.saveSeq < ceiling, `other slot seq ${o.saveSeq} must be strictly below ceiling ${ceiling}`);
    }
    // NOTE (attacker): duplicates BELOW the ceiling are harmless — the gate only
    // ever compares an incoming save against ONE slot, and every future seq is
    // strictly greater. Adjudicated CLEAN, not a defect.
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'all autosaves land after a duplicate-seq re-stamp');
  });

  test('R3-2b: duplicate saveSeqs ABOVE the minted ceiling (force actually fires) -> walk still lands every autosave', () => {
    const st = memStorage();
    [5000, 6000, 7000].forEach((tick, i) => {
      const sp = createSavepoint({ ...initialState(), tick }, [], ms(i), 'v-old', null, 999);
      st.setItem(`${SAVEPOINT_KEY_PREFIX}.${i}`, JSON.stringify(sp));
    });
    const ref = seqRef(undefined); // boots at 0 — the mixed-install shape
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok, 'forced rebuild must land');
    assert.ok(r.save.saveSeq > 999, 'minted seq must supersede the duplicated 999s');
    const all = readAllSavepoints(st, ms(11));
    for (const o of all.filter((sp) => sp.snapshotTick !== 120)) {
      assert.ok(o.saveSeq < r.save.saveSeq, `other slot ${o.snapshotTick} seq ${o.saveSeq} must be below ceiling ${r.save.saveSeq}`);
    }
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'all autosaves land');
  });

  // -------------------------------------------------------------------------
  // R3-3: CROSS-LINEAGE CONTAMINATION. A different lineage's slots share the
  // storage namespace only by key prefix; they must be untouched BYTE-FOR-BYTE.
  // -------------------------------------------------------------------------
  test('R3-3: a DIFFERENT lineage\'s slots are untouched byte-for-byte by the forced re-stamp', () => {
    const st = memStorage();
    const LIVE = 'LliveCity';
    const OTHER = 'LotherCity';
    st.setItem(CURRENT_LINEAGE_KEY, LIVE);
    seedLegacyInstall(st, [5000, 6000, 7000], LIVE);
    seedLegacyInstall(st, [9000, 9100, 9200], OTHER);
    // ALSO the unnamespaced legacy keyspace, which shares the prefix. Written by
    // hand: persistSavepointWithReason would stamp the ambient LIVE lineage onto
    // an unstamped savepoint, which is exactly what we do NOT want here.
    [8000, 8100, 8200].forEach((tick, i) => {
      const sp = createSavepoint({ ...initialState(), tick }, [], ms(i), 'v-old', null, undefined);
      st.setItem(`${SAVEPOINT_KEY_PREFIX}.${i}`, JSON.stringify(sp));
    });

    const before = st._snapshotAll();
    const foreignKeys = Object.keys(before).filter(
      (k) => k.startsWith(SAVEPOINT_KEY_PREFIX) && !k.startsWith(`${SAVEPOINT_KEY_PREFIX}.${LIVE}.`)
    );
    assert.ok(foreignKeys.length >= 6, 'fixture must have foreign + legacy slots');

    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120, lineageId: LIVE }, ref, ms(10));
    assert.ok(r.ok, 'forced rebuild must land');

    const after = st._snapshotAll();
    for (const k of foreignKeys) {
      assert.equal(after[k], before[k], `cross-lineage contamination: ${k} was modified by the re-stamp walk`);
    }
    assert.equal(autosaveRun(st, ref, 121, 6, LIVE), 6, 'live lineage autosaves all land');
  });

  // -------------------------------------------------------------------------
  // R3-4: savedAt TIES across the other slots — the walk sorts by savedAt only.
  // -------------------------------------------------------------------------
  test('R3-4: savedAt ties across slots -> walk still terminates with strictly-decreasing seqs', () => {
    const st = memStorage();
    // Three legacy saves with the IDENTICAL savedAt.
    [5000, 6000, 7000].forEach((tick) => {
      const sp = createSavepoint({ ...initialState(), tick }, [], ms(3), 'v-old', null, undefined);
      persistSavepoint(st, sp, ms(3));
    });
    const occupied = readAllSavepoints(st, ms(4));
    assert.equal(occupied.length, SAVEPOINT_CAP, 'all slots occupied with tied savedAt');
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok);
    const all = readAllSavepoints(st, ms(11));
    const others = all.filter((sp) => sp.snapshotTick !== 120);
    for (const o of others) assert.ok(Number.isFinite(o.saveSeq) && o.saveSeq < r.save.saveSeq, 'strictly below ceiling');
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'autosaves land despite savedAt ties');
  });

  // -------------------------------------------------------------------------
  // R3-5: MORE occupied slots than SAVEPOINT_CAP (a cap reduction leaves slots
  // 3..7 behind). persistSavepointWithReason removes them; prove the walk is not
  // confused and no over-cap slot survives to re-poison the gate.
  // -------------------------------------------------------------------------
  test('R3-5: over-cap leftover slots are purged and never re-poison the gate', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    for (let s = SAVEPOINT_CAP; s < 8; s++) {
      const sp = createSavepoint({ ...initialState(), tick: 9000 + s }, [], ms(s), 'v-old', null, undefined);
      st.setItem(`${SAVEPOINT_KEY_PREFIX}.${s}`, JSON.stringify(sp));
    }
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok);
    for (let s = SAVEPOINT_CAP; s < 8; s++) {
      assert.equal(st._raw(`${SAVEPOINT_KEY_PREFIX}.${s}`), null, `over-cap slot ${s} must be purged`);
    }
    assert.equal(autosaveRun(st, ref, 121, 6), 6);
  });

  // -------------------------------------------------------------------------
  // R3-6: the re-stamp is a WRITE to slots the player never asked to modify.
  // Quota exhaustion MID-WALK: slot A re-stamped, slot B's write throws.
  // Question: is the resulting install WORSE than round 2's (permanently-refused)
  // state, or merely no better?
  // -------------------------------------------------------------------------
  test('R3-6: quota exhaustion MID re-stamp walk -> the forced write still lands and no slot is corrupted', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    // Arm: from here on, the 2nd savepoint write throws QuotaExceeded — i.e. the
    // walk re-stamps one slot then hits the wall mid-way.
    st._spWrites = 0;
    st.failSavepointWrite = 2;
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    // Whatever happens, no slot may be left unparsable and the outcome must be
    // honest: either the rebuild landed, or it reported storage-error.
    st.failSavepointWrite = 0;
    const readable = readAllSavepoints(st, ms(11));
    assert.ok(readable.length >= 1, 'at least one slot must remain readable after a mid-walk quota failure');
    assert.ok(r.ok || r.reason === 'storage-error', 'outcome must be honest');
    // Diagnostic recorded in the round report: does the install still autosave?
    const landed = r.ok ? autosaveRun(st, ref, 121, 6) : -1;
    // eslint-disable-next-line no-console
    console.log(`R3-6 DIAGNOSTIC: forced write ok=${r.ok} reason=${r.reason ?? '-'} post-rebuild autosaves landed=${landed}/6`);
  });

  // -------------------------------------------------------------------------
  // F-R3-2 (P1): the re-stamp walk is BEST-EFFORT and SILENT. Its per-slot write
  // failure is swallowed by a bare `catch {}` / `if (res.ok)`, nothing is
  // recorded, and `persistSavepointForced` still returns ok:true. So on a
  // QUOTA-EXHAUSTED install — which is precisely BUG-436's ORIGINAL trigger
  // condition (the 08-28 dump's live 'preWipeArchive exceeded the quota' error)
  // — the rebuild reports SUCCESS while the walk did nothing, and every
  // post-rebuild autosave is refused forever. RR-1 verbatim, silently.
  // -------------------------------------------------------------------------
  test('F-R3-2: a re-stamp write that fails on quota is swallowed -> rebuild reports success, autosaves refused forever, no signal', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    st._spWrites = 0;
    st.failSavepointWrite = 2; // the walk's second slot hits the wall
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    st.failSavepointWrite = 0;
    assert.ok(r.ok, 'the forced write itself lands (quota freed by then)');
    const landed = autosaveRun(st, ref, 121, 6);
    assert.equal(
      landed,
      6,
      `F-R3-2: rebuild reported ok=true yet only ${landed}/6 post-rebuild autosaves landed — a swallowed re-stamp write leaves the install in the exact RR-1 permanently-refused state with NO signal to the player or the caller`
    );
  });

  // -------------------------------------------------------------------------
  // R3-7: content-untouched claim — the re-stamped history slots must still LOAD
  // as manual restores with their snapshot/tick/journalTail intact; ONLY saveSeq
  // may differ from the pre-rebuild bytes.
  // -------------------------------------------------------------------------
  test('R3-7: re-stamped history slots keep their content (only saveSeq differs) and still load', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    const before = readAllSavepoints(st, ms(4)).map((sp) => JSON.stringify({ ...sp, saveSeq: undefined }));
    const ref = seqRef(undefined);
    const r = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    assert.ok(r.ok);
    const after = readAllSavepoints(st, ms(11));
    const rebuilt = after.find((sp) => sp.snapshotTick === 120);
    assert.ok(rebuilt, 'rebuilt city present');
    const survivors = after.filter((sp) => sp.snapshotTick !== 120);
    for (const s of survivors) {
      const norm = JSON.stringify({ ...s, saveSeq: undefined });
      assert.ok(before.includes(norm), `history slot tick ${s.snapshotTick} content changed beyond saveSeq`);
      assert.ok(s.snapshot && typeof s.snapshot.tick === 'number', 'survivor still loadable as a manual restore');
      assert.equal(s.snapshot.tick, s.snapshotTick, 'snapshot/tick still coherent');
    }
  });

  // -------------------------------------------------------------------------
  // R3-8: end-to-end ordering — after the forced rebuild + 6 autosaves, a reload
  // must boot the LATEST autosave, not a re-stamped history slot.
  // -------------------------------------------------------------------------
  test('R3-8: reload after forced rebuild + autosaves boots the NEWEST autosave', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    const ref = seqRef(undefined);
    assert.ok(onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10)).ok);
    assert.equal(autosaveRun(st, ref, 121, 6), 6);
    const boot = mostRecentSavepoint(readAllSavepoints(st, ms(60)));
    assert.ok(boot, 'boot savepoint exists');
    assert.equal(boot.snapshotTick, 126, 'boot must be the LAST autosave (tick 121+5)');
    const maxSeq = readAllSavepoints(st, ms(60)).reduce((m, sp) => Math.max(m, sp.saveSeq ?? -1), -1);
    assert.equal(boot.saveSeq, maxSeq, 'the newest by savedAt is also the highest saveSeq (gates agree)');
  });

  // -------------------------------------------------------------------------
  // R3-9: saveSeqRef re-seed — ONLY UPWARD. A modern install (no force needed)
  // must see no ref change beyond the ordinary ++.
  // -------------------------------------------------------------------------
  test('R3-9: ref re-seed never LOWERS the counter on a modern (unforced) install', () => {
    const st = memStorage();
    // modern install: saves carry saveSeq, low ticks, so a rebuild is fresher.
    [10, 20, 30].forEach((tick, i) => {
      const sp = createSavepoint({ ...initialState(), tick }, [], ms(i), 'v-old', null, i + 1);
      assert.ok(persistSavepoint(st, sp, ms(i)));
    });
    const ref = seqRef(99); // booted from a HIGH seq
    const before = ref.get();
    const r = onRebuildCompletion(st, { ...initialState(), tick: 500 }, ref, ms(10));
    assert.ok(r.ok);
    assert.equal(r.reason, undefined, 'no force needed on a modern install');
    assert.equal(r.save.saveSeq, 100, 'ordinary nextSaveSeq()');
    assert.ok(ref.get() >= before, 'counter never goes backwards');
    assert.equal(ref.get(), 100);
  });

  // -------------------------------------------------------------------------
  // R3-10: StrictMode double-invoke of the completion branch — running the whole
  // rebuild completion twice must be harmless (no seq inversion, autosaves land).
  // -------------------------------------------------------------------------
  test('R3-10: double-run of the rebuild completion (StrictMode) is harmless', () => {
    const st = memStorage();
    seedLegacyInstall(st, [5000, 6000, 7000]);
    const ref = seqRef(undefined);
    const a = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(10));
    const b = onRebuildCompletion(st, { ...initialState(), tick: 120 }, ref, ms(11));
    assert.ok(a.ok && b.ok, 'both runs must land');
    const all = readAllSavepoints(st, ms(12));
    const seqs = all.map((sp) => sp.saveSeq);
    assert.ok(seqs.every((s) => Number.isFinite(s)), 'every slot carries a finite seq after two walks');
    assert.equal(new Set(seqs).size, seqs.length, 'no duplicate seqs after a double walk');
    assert.equal(autosaveRun(st, ref, 121, 6), 6, 'autosaves still all land after a double walk');
  });
});

// ---------------------------------------------------------------------------
describe('BUG-436 ROUND 3 — wiring pins (mutation targets for the NEW logic)', () => {
  const replaySrc = fs.readFileSync(REPLAY_TS, 'utf8');
  const storeSrc = fs.readFileSync(STORE_TSX, 'utf8');

  test('WIRE-1: the force branch contains the re-stamp walk (anchored to the force block)', () => {
    const i = replaySrc.indexOf('if (opts?.force)');
    assert.ok(i > 0, 'force branch must exist');
    const block = replaySrc.slice(i, i + 3000);
    assert.match(block, /const others = slots/, 're-stamp walk must live inside the force branch');
    assert.match(block, /ceiling - 1/, 'other slots must be re-stamped strictly BELOW the ceiling');
  });

  test('WIRE-2: onRebuild re-seeds saveSeqRef from the (possibly mutated) rebuiltSave.saveSeq', () => {
    const i = storeSrc.indexOf('persistSavepointForced(window.localStorage, rebuiltSave)');
    assert.ok(i > 0, 'forced call site must exist in onRebuild');
    const block = storeSrc.slice(i, i + 4000);
    assert.match(block, /saveSeqRef\.current = rebuiltSave\.saveSeq/, 'ref re-seed must follow the forced write');
  });
});
