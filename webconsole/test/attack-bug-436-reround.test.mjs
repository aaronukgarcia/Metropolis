// attack-bug-436-reround.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND on the
// BUG-436 estate (attacker: opus-reround-bug436; NOT the author, NOT round 1's
// attacker).
//
// Round 1 REJECTED for R1/F1/F2/F3/F5. The rework's headline claim is a NEW
// entry point, `persistSavepointForced` (replay.ts), reached from exactly one
// call site (store.tsx onRebuild's completion branch) when the ordinary
// fail-closed gate refuses SPECIFICALLY for 'stale-overwrite'. That new
// forced-write path is the surface this re-round attacks.
//
// The attacks below model the store's OWN ambient `saveSeqRef` bookkeeping
// (store.tsx ~line 1070: `useRef(boot.bootSavepointMeta?.saveSeq ?? 0)` and
// `nextSaveSeq()` = `++ref`) against the REAL replay.ts persist gate, because
// the defect class round 1 found lives precisely in the seam between the two.

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
} from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const STORE_TSX = path.join(__dirname, '..', 'src', 'sim', 'store.tsx');
const REPLAY_TS = path.join(__dirname, '..', 'src', 'sim', 'replay.ts');

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
  };
}

/**
 * Model of the store's ambient saveSeq counter. `boot` is the saveSeq of the
 * savepoint the app booted from (undefined on a legacy install -> 0), exactly
 * as store.tsx initialises `saveSeqRef`.
 */
function seqRef(boot) {
  let cur = Number.isFinite(boot) ? boot : 0;
  return { next: () => ++cur, get: () => cur };
}

/** store.tsx onRebuild completion branch, AS WRITTEN post-rework. */
function onRebuildCompletion(storage, rebuiltState, ref, now) {
  const save = createSavepoint(rebuiltState, [], now, 'v-new', null, ref.next());
  let r = persistSavepointWithReason(storage, save, now);
  if (!r.ok && r.reason === 'stale-overwrite') r = persistSavepointForced(storage, save, now);
  return { ok: r.ok, reason: r.reason, save };
}

/** store.tsx autosave timer, AS WRITTEN (plain persistSavepoint, NO force). */
function autosave(storage, state, ref, now) {
  const sp = createSavepoint(state, [], now, 'v-new', null, ref.next());
  return { ok: persistSavepoint(storage, sp, now), sp };
}

/** The exact legacy install shape round 1's F1 reproduced: pre-round-3 saves
 * carrying NO saveSeq at all, at ticks the ring-capped journal can no longer
 * replay back up to. */
function seedLegacyInstall(storage, ticks, baseMs) {
  ticks.forEach((tick, i) => {
    const sp = createSavepoint({ ...initialState(), tick }, [], new Date(baseMs + i * 60_000), 'v-old', null, undefined);
    assert.equal(sp.saveSeq, undefined, 'fixture must carry NO saveSeq (legacy shape)');
    assert.ok(persistSavepoint(storage, sp, new Date(baseMs + i * 60_000)), `legacy seed ${i} must land`);
  });
}

describe('BUG-436 RE-ROUND — the forced-write path is the new attack surface', () => {
  // ------------------------------------------------------------------
  test('RE-1 (containment): persistSavepointForced / the `force` option are reachable from the REBUILD call site ONLY', () => {
    const srcDir = path.join(__dirname, '..', 'src');
    const offenders = [];
    const walk = (dir) => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, e.name);
        if (e.isDirectory()) walk(p);
        else if (/\.(ts|tsx)$/.test(e.name)) {
          const txt = fs.readFileSync(p, 'utf8');
          // strip block comments + line comments so doc-comment mentions don't count
          const code = txt.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
          if (p === REPLAY_TS) continue; // the definition site itself
          const lines = code.split('\n');
          lines.forEach((l, i) => {
            if (/persistSavepointForced\s*\(/.test(l) || /force\s*:\s*true/.test(l)) {
              offenders.push(`${path.relative(srcDir, p)}:${i + 1}: ${l.trim()}`);
            }
          });
        }
      }
    };
    walk(srcDir);
    assert.equal(
      offenders.length,
      1,
      `the forced write must have exactly ONE production call site (the rebuild boundary); found:\n${offenders.join('\n')}`
    );
    assert.match(offenders[0], /^sim[\\/]store\.tsx:/, 'the sole forced call site must be store.tsx');
  });

  // ------------------------------------------------------------------
  test('RE-2 (F1 repro re-run): a legacy install with ring-capped-journal-shaped LOWER tick COMPLETES its rebuild, and the reloaded city is the REBUILT one', () => {
    const storage = memStorage();
    const base = Date.UTC(2026, 8, 1, 10);
    seedLegacyInstall(storage, [2000, 2020, 2044], base);

    // boot savepoint carries no saveSeq -> ambient ref starts at 0 (store.tsx)
    const booted = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 3 * 60_000)));
    assert.equal(booted.snapshotTick, 2044);
    const ref = seqRef(booted.saveSeq);

    // genesis replay over the ring-capped journal lands LOWER than the live city
    const rebuilt = { ...initialState(), tick: 812, funds: 5_000_000 };
    const now = new Date(base + 10 * 60_000);

    // unforced first: must legitimately refuse (this is F1's mechanism)
    const unforced = persistSavepointWithReason(storage, createSavepoint(rebuilt, [], now, 'v-new', null, 1), now);
    assert.equal(unforced.ok, false);
    assert.equal(unforced.reason, 'stale-overwrite', 'F1 mechanism must still be real, else this test proves nothing');

    const res = onRebuildCompletion(storage, rebuilt, ref, now);
    assert.equal(res.ok, true, 'F1: a HEALTHY rebuild must no longer be refused forever on a legacy install');

    const reloaded = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 11 * 60_000)));
    assert.equal(reloaded.snapshotTick, 812, 'the reloaded city must be the REBUILT one, not the old high-tick city');
    assert.equal(reloaded.snapshot.funds, 5_000_000);
  });

  // ------------------------------------------------------------------
  // THE KILL SHOT the re-round brief names explicitly.
  // ------------------------------------------------------------------
  test('RE-3 (KILL SHOT): after a forced rebuild, the NEXT ORDINARY AUTOSAVE must land and must win the following reload', () => {
    const storage = memStorage();
    const base = Date.UTC(2026, 8, 1, 10);
    seedLegacyInstall(storage, [2000, 2020, 2044], base);

    const booted = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 3 * 60_000)));
    const ref = seqRef(booted.saveSeq);

    const rebuilt = { ...initialState(), tick: 812, funds: 5_000_000 };
    const t0 = new Date(base + 10 * 60_000);
    assert.equal(onRebuildCompletion(storage, rebuilt, ref, t0).ok, true, 'precondition: forced rebuild lands');

    // The player resumes; the app reloads from the rebuilt savepoint, so the
    // ambient ref is re-seeded from it (store.tsx boot), then plays on and the
    // autosave timer fires.
    const afterBoot = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 11 * 60_000)));
    assert.equal(afterBoot.snapshotTick, 812, 'precondition: boot picks the rebuilt city');
    const ref2 = seqRef(afterBoot.saveSeq);

    const played = { ...initialState(), tick: 900, funds: 4_900_000 };
    const t1 = new Date(base + 20 * 60_000);
    const a = autosave(storage, played, ref2, t1);
    assert.equal(a.ok, true, 'the first post-rebuild autosave must be accepted — a refused autosave is BUG-436 reborn one layer down');

    const reloaded = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 21 * 60_000)));
    assert.equal(reloaded.snapshotTick, 900, 'the post-rebuild autosave must WIN the reload');
  });

  test('RE-3b (KILL SHOT, sustained): every autosave over a whole post-rebuild session must land, not just the first', () => {
    const storage = memStorage();
    const base = Date.UTC(2026, 8, 1, 10);
    seedLegacyInstall(storage, [2000, 2020, 2044], base);
    const booted = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 3 * 60_000)));
    const ref = seqRef(booted.saveSeq);
    const rebuilt = { ...initialState(), tick: 812 };
    assert.equal(onRebuildCompletion(storage, rebuilt, ref, new Date(base + 10 * 60_000)).ok, true);

    const afterBoot = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 11 * 60_000)));
    const ref2 = seqRef(afterBoot.saveSeq);
    const refused = [];
    for (let i = 1; i <= SAVEPOINT_CAP + 3; i++) {
      const when = new Date(base + (20 + i) * 60_000);
      const r = autosave(storage, { ...initialState(), tick: 812 + i * 10 }, ref2, when);
      if (!r.ok) refused.push(i);
    }
    assert.deepEqual(refused, [], `post-rebuild autosaves refused at iterations ${refused.join(',')} — the rebuilt city stops saving`);
    const final = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 40 * 60_000)));
    assert.equal(final.snapshotTick, 812 + (SAVEPOINT_CAP + 3) * 10, 'the newest autosave must be what a reload boots');
  });

  // ------------------------------------------------------------------
  test('RE-4 (race): an ordinary autosave landing between replay completion and the forced persist must NOT be destroyed by the force', () => {
    const storage = memStorage();
    const base = Date.UTC(2026, 8, 1, 10);
    seedLegacyInstall(storage, [2000, 2020, 2044], base);
    const booted = mostRecentSavepoint(readAllSavepoints(storage, new Date(base + 3 * 60_000)));
    const ref = seqRef(booted.saveSeq);

    // the rebuild's savepoint is minted (nextSaveSeq consumed) ...
    const rebuilt = { ...initialState(), tick: 812, funds: 1 };
    const rebuiltSave = createSavepoint(rebuilt, [], new Date(base + 10 * 60_000), 'v-new', null, ref.next());

    // ... and BEFORE the persist runs, the autosave timer fires for the LIVE
    // (old, still-running) city and lands legitimately.
    const live = { ...initialState(), tick: 2100, funds: 777 };
    const raceAt = new Date(base + 10 * 60_000 + 1000);
    assert.equal(autosave(storage, live, ref, raceAt).ok, true, 'precondition: the racing autosave lands');

    // now the rebuild persists (forced on a stale-overwrite refusal)
    const now = new Date(base + 10 * 60_000 + 2000);
    let r = persistSavepointWithReason(storage, rebuiltSave, now);
    if (!r.ok && r.reason === 'stale-overwrite') r = persistSavepointForced(storage, rebuiltSave, now);
    assert.equal(r.ok, true);

    const all = readAllSavepoints(storage, new Date(base + 12 * 60_000));
    assert.ok(
      all.some((s) => s.snapshotTick === 2100 && s.snapshot.funds === 777),
      'the racing autosave must still be present in the rotation — the force must not clobber a NEWER legitimate save'
    );
  });

  // ------------------------------------------------------------------
  test('RE-5 (mutation): neuter the forced retry — a stale-overwrite must then surface HONESTLY, never report success', () => {
    // Mutant: the store keeps the fail-closed gate but drops the forced retry.
    const mutant = (storage, rebuiltState, ref, now) => {
      const save = createSavepoint(rebuiltState, [], now, 'v-new', null, ref.next());
      const r = persistSavepointWithReason(storage, save, now);
      return { ok: r.ok, reason: r.reason };
    };
    const storage = memStorage();
    const base = Date.UTC(2026, 8, 1, 10);
    seedLegacyInstall(storage, [2000, 2020, 2044], base);
    const ref = seqRef(undefined);
    const out = mutant(storage, { ...initialState(), tick: 812 }, ref, new Date(base + 10 * 60_000));
    assert.equal(out.ok, false, 'the mutant must NOT silently succeed');
    assert.equal(out.reason, 'stale-overwrite');
    // and the shipped code must NOT be the mutant:
    const src = fs.readFileSync(STORE_TSX, 'utf8');
    assert.match(src, /persistSavepointForced\(window\.localStorage, rebuiltSave\)/);
  });

  test('RE-6 (mutation): force must NOT bypass a genuine storage error', () => {
    const base = memStorage();
    const storage = {
      getItem: base.getItem,
      setItem: () => {
        const e = new Error('quota');
        e.name = 'QuotaExceededError';
        throw e;
      },
      removeItem: base.removeItem,
    };
    const r = persistSavepointForced(storage, createSavepoint({ ...initialState(), tick: 5 }, [], new Date(), 'v', null, 9));
    assert.equal(r.ok, false);
    assert.equal(r.reason, 'storage-error', 'forcing must skip the STALENESS check only, never the write itself');
  });

  test('RE-7 (mutation): the minted saveSeq must strictly exceed EVERY occupied slot, not just the target slot', () => {
    const storage = memStorage();
    const now = new Date(Date.UTC(2026, 8, 1, 12));
    // slots with mixed, non-monotonic saveSeq; the OLDEST-savedAt slot has the LOWEST seq
    [
      { tick: 100, seq: 3, at: 0 },
      { tick: 200, seq: 42, at: 60_000 },
      { tick: 300, seq: 7, at: 120_000 },
    ].forEach((s) => {
      const when = new Date(now.getTime() + s.at);
      assert.ok(persistSavepoint(storage, createSavepoint({ ...initialState(), tick: s.tick }, [], when, 'v', null, s.seq), when));
    });
    const rebuilt = createSavepoint({ ...initialState(), tick: 9 }, [], new Date(now.getTime() + 300_000), 'v', null, 1);
    const r = persistSavepointForced(storage, rebuilt, new Date(now.getTime() + 300_000));
    assert.equal(r.ok, true);
    const others = readAllSavepoints(storage, new Date(now.getTime() + 301_000))
      .filter((s) => s.snapshotTick !== 9)
      .map((s) => s.saveSeq);
    assert.ok(
      others.every((s) => rebuilt.saveSeq > s),
      `minted saveSeq ${rebuilt.saveSeq} must exceed every surviving slot (${others.join(',')})`
    );
  });
});
