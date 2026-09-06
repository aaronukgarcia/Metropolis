// bug-798-save-compress-worker.test.mjs — BUG-798: saveCodec.encode()'s LZ
// compression step blocked the main thread ~2.4s per autosave on Aaron's
// 9.48M-citizen capture (38,251 buildings, ~14MB savepoint). This file pins:
//
//   1. The off-thread encode ACTUALLY uses the worker when one is available
//      (an algorithmic probe, not a wall-clock timing assert — BUG-757
//      precedent: "no absolute wall-clock asserts in CI").
//   2. A stale in-flight AUTOSAVE encode is DISCARDED (never written to
//      storage) once a newer persist of EITHER kind has started.
//   3. The synchronous fallback (no Worker capability) produces BYTE-
//      IDENTICAL output to the worker path.
//   4. PRIORITY (opus-round-bug798 REJECT, finding A): an EXPLICIT save
//      (saveGame/saveGameAs/an applied load) is NEVER superseded by an
//      autosave, only by a LATER explicit save — the original single-
//      counter design let an autosave silently defeat an in-flight manual
//      save, which Aaron's dogfood measured on ~8% of manual saves.
//   5. `precomputedEncoded` is actually wired through — one encode per
//      async persist, never a silent second synchronous recompute (finding
//      B).
//   6. MET-V888/MET-V889 registry-error coverage for a worker that fails to
//      construct / errors at runtime, with the fallback still persisting
//      correctly either way (finding C).
//   7. The reset boundary fence (finding D / BUG-687 shape): an in-flight
//      persist from the OLD city can never land after a reset, regardless
//      of kind.
//
// jsdom (used by this project's .test.tsx files) cannot construct a real
// MODULE Worker either — same structural gap simWorker.ts's own header
// comment already documents for the tick-offload worker — so this file (like
// feat-2326609771-webworker-default-on.test.tsx) uses a minimal FakeWorker
// global instead of attempting a real one. No jsdom is used here at all;
// saveCodecAsync.ts/replay.ts are pure enough to exercise directly under
// plain node --test.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import {
  encodeOffMainThread,
  nextPersistGeneration,
  isCurrentPersistGeneration,
  fencePersistGeneration,
  runSerializedExplicitWrite,
  resetSaveCodecAsyncForTests,
  __saveCodecAsyncProbe,
} from '../src/sim/saveCodecAsync.ts';
import { encode, decode } from '../src/sim/saveCodec.ts';
import { persistSavepointWithReason, persistSavepointWithReasonAsync, createSavepoint } from '../src/sim/replay.ts';
import { initialState } from '../src/sim/engine.ts';
import { recentErrors } from '../src/sim/backend.ts';

function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
    _keys: () => Array.from(m.keys()).sort(),
    _raw: (k) => m.get(k),
  };
}

/** A large-ish repetitive JSON string — same "thousands of near-identical
 *  shapes" pattern saveCodec.ts's own header comment says LZ wins big on,
 *  scaled down from a real 14MB capture to keep the test fast while still
 *  exercising real (not trivially-short) compression work. */
function bigJson(n = 3000) {
  const buildings = [];
  for (let i = 0; i < n; i++) {
    buildings.push({ id: i, kind: 'house', tier: i % 5, occupants: [i, i + 1, i + 2], flags: { built: true, online: true } });
  }
  return JSON.stringify({ buildings });
}

/** Minimal fake Worker: postMessage schedules the SAME encode() saveCodec.ts
 *  exports, via queueMicrotask (never synchronously) — proving a caller
 *  cannot observe the compression on its own call stack, matching a real
 *  worker's inherent asynchrony without needing an actual Worker thread in
 *  node --test. onmessage/onerror are plain settable properties, matching
 *  the DOM Worker interface saveCodecAsync.ts programs against. */
// opus-reround-bug798 finding B: `sentinel: true` makes the fake worker
// return a DISTINGUISHABLE value ('SENTINEL_' + the real encode() output)
// instead of plain encode() output. Without this, the worker path and a
// silently-broken wrapper that drops `precomputedEncoded` and recomputes
// `encode(json)` itself inside `persistSavepointWithReason` produce BYTE-
// IDENTICAL bytes (both call the same `encode`) — so a test that only
// compares stored bytes against `encode(json)` can never catch that
// regression. The sentinel prefix makes the two paths produce visibly
// different output, so the assertion reds the instant the wrapper stops
// forwarding the worker's result verbatim.
function installFakeWorker({ resolveOrder = 'fifo', onConstruct, sentinel = false } = {}) {
  const origWorker = globalThis.Worker;
  const instances = [];
  class FakeWorker {
    constructor(url, opts) {
      onConstruct?.(url, opts);
      this.onmessage = null;
      this.onerror = null;
      this._queue = [];
      instances.push(this);
    }
    postMessage(msg) {
      // Defer via queueMicrotask so postMessage() itself never synchronously
      // invokes onmessage — the real DOM contract, and the thing this file's
      // "no wall-clock" probe test relies on.
      const run = () => {
        const real = encode(msg.json);
        const encoded = sentinel ? `SENTINEL_${real}` : real;
        this.onmessage?.({ data: { requestId: msg.requestId, encoded } });
      };
      if (resolveOrder === 'fifo') {
        queueMicrotask(run);
      } else {
        this._queue.push(run);
      }
    }
    terminate() {}
  }
  globalThis.Worker = FakeWorker;
  return {
    instances,
    restore: () => {
      if (origWorker === undefined) delete globalThis.Worker;
      else globalThis.Worker = origWorker;
    },
  };
}

/** A savepoint carrying a unique tick, so JSON.parse(decode(raw)).snapshotTick
 *  identifies which of several racing persists actually landed. */
function spWithTick(tick, saveSeq, when) {
  return createSavepoint({ ...initialState(), tick }, [], new Date(when), 'v1', null, saveSeq);
}

describe('BUG-798: encodeOffMainThread uses the worker when available (algorithmic probe, not wall-clock)', () => {
  test('a synchronous caller observes NO encode() result on its own call stack — the promise settles only after a microtask', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const json = bigJson();
      const p = encodeOffMainThread(json);
      // Immediately after calling encodeOffMainThread, nothing has resolved
      // yet — the probe counters must both still read zero for THIS call,
      // proving the LZ step has not run synchronously inline.
      assert.equal(__saveCodecAsyncProbe.workerEncodeCount, 0, 'worker must not have replied yet on the calling stack');
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 0, 'no synchronous fallback should have fired either');
      await p;
      assert.equal(__saveCodecAsyncProbe.workerEncodeCount, 1, 'exactly one worker-path encode should have completed');
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 0, 'the worker path must not also fall back synchronously');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('the resolved text round-trips through saveCodec.decode() to the original JSON', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const json = bigJson();
      const encoded = await encodeOffMainThread(json);
      assert.equal(decode(encoded), json);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('the Worker is constructed with a URL naming saveCodecWorker.ts and { type: "module" }', async () => {
    resetSaveCodecAsyncForTests();
    let capturedUrl = null;
    let capturedOpts = null;
    const fake = installFakeWorker({
      onConstruct: (url, opts) => {
        capturedUrl = url;
        capturedOpts = opts;
      },
    });
    try {
      await encodeOffMainThread(bigJson(10));
      assert.ok(capturedUrl, 'Worker must have been constructed');
      assert.match(String(capturedUrl), /saveCodecWorker\.ts$/, 'must construct the dedicated saveCodecWorker.ts entry point');
      assert.deepEqual(capturedOpts, { type: 'module' }, 'must request a MODULE worker, matching simWorker.ts\'s own construction contract');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('BUG-798: synchronous fallback (no Worker capability) is byte-identical to the worker path', () => {
  test('no Worker global at all -> synchronous encode(), same bytes as the worker path would have produced', async () => {
    resetSaveCodecAsyncForTests();
    const origWorker = globalThis.Worker;
    delete globalThis.Worker;
    try {
      const json = bigJson();
      const fallbackEncoded = await encodeOffMainThread(json);
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 1, 'must have taken the synchronous fallback path');
      assert.equal(__saveCodecAsyncProbe.workerEncodeCount, 0);
      assert.equal(fallbackEncoded, encode(json), 'fallback output must be byte-identical to calling saveCodec.encode() directly');
    } finally {
      if (origWorker === undefined) delete globalThis.Worker;
      else globalThis.Worker = origWorker;
      resetSaveCodecAsyncForTests();
    }
  });

  test('worker-path and fallback-path produce IDENTICAL compressed bytes for the same input', async () => {
    resetSaveCodecAsyncForTests();
    const json = bigJson();

    const fake = installFakeWorker();
    let workerEncoded;
    try {
      workerEncoded = await encodeOffMainThread(json);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }

    delete globalThis.Worker;
    const fallbackEncoded = await encodeOffMainThread(json);
    resetSaveCodecAsyncForTests();

    assert.equal(workerEncoded, fallbackEncoded, 'both paths call the exact same saveCodec.encode() — output must match byte for byte');
  });
});

describe('BUG-798 round REJECT finding C: MET-V888/MET-V889 registry-error coverage', () => {
  test('MET-V888: a Worker whose construction throws is recorded exactly once, and the fallback still produces a correct result', async () => {
    resetSaveCodecAsyncForTests();
    const origWorker = globalThis.Worker;
    const uniqueDetail = `boom-construct-${Math.random()}`;
    class ThrowingWorker {
      constructor() {
        throw new Error(uniqueDetail);
      }
    }
    globalThis.Worker = ThrowingWorker;
    try {
      const json = bigJson(50);
      const encoded = await encodeOffMainThread(json);
      assert.equal(encoded, encode(json), 'the fallback must still persist a correct, decodable result');
      assert.equal(decode(encoded), json);
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 1);

      const v888 = recentErrors().filter((e) => e.code === 'MET-V888' && e.msg.includes(uniqueDetail));
      assert.equal(v888.length, 1, 'exactly one MET-V888 record for this failure');
      assert.equal(v888[0].count, 1, 'recorded exactly once, not duplicated');

      // A second call must not retry construction (sticky broken flag) —
      // still falls back cleanly, and must NOT add a second MET-V888 record.
      const encoded2 = await encodeOffMainThread(json);
      assert.equal(encoded2, encode(json));
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 2);
      const v888After = recentErrors().filter((e) => e.code === 'MET-V888' && e.msg.includes(uniqueDetail));
      assert.equal(v888After.length, 1, 'construction is never retried, so no second MET-V888 record is added');
      assert.equal(v888After[0].count, 1, 'the existing record\'s count must not bump either — construction was never attempted again');
    } finally {
      if (origWorker === undefined) delete globalThis.Worker;
      else globalThis.Worker = origWorker;
      resetSaveCodecAsyncForTests();
    }
  });

  test('MET-V889: a Worker whose onerror fires is recorded exactly once, and the pending request still resolves correctly via fallback', async () => {
    resetSaveCodecAsyncForTests();
    const uniqueMsg = `boom-runtime-${Math.random()}`;
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const json = bigJson(50);
      const p = encodeOffMainThread(json);
      const worker = fake.instances[0];
      assert.ok(worker, 'worker must have been constructed');
      // Fire onerror BEFORE draining the queued reply — simulating a runtime
      // crash while a compression job is still outstanding.
      worker.onerror({ message: uniqueMsg });
      const encoded = await p;
      assert.equal(encoded, encode(json), 'the pending request must still resolve, via the synchronous fallback rescue');
      assert.equal(decode(encoded), json);

      const v889 = recentErrors().filter((e) => e.code === 'MET-V889' && e.msg.includes(uniqueMsg));
      assert.equal(v889.length, 1, 'exactly one MET-V889 record for this failure');
      assert.equal(v889[0].count, 1);

      // The worker is now sticky-broken too — a further request must not
      // try to use it again (no second postMessage onto a known-dead
      // worker), it must go straight to the synchronous fallback.
      const before = __saveCodecAsyncProbe.syncFallbackCount;
      const encoded2 = await encodeOffMainThread(json);
      assert.equal(encoded2, encode(json));
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, before + 1);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('BUG-798: stale in-flight AUTOSAVE encode is discarded once a newer persist starts (generation guard)', () => {
  test("nextPersistGeneration/isCurrentPersistGeneration: an older 'autosave' id reads stale once ANY newer call is minted", () => {
    resetSaveCodecAsyncForTests();
    const a = nextPersistGeneration('autosave');
    assert.equal(isCurrentPersistGeneration(a, 'autosave'), true);
    const b = nextPersistGeneration('autosave');
    assert.equal(isCurrentPersistGeneration(a, 'autosave'), false, 'a must now read stale');
    assert.equal(isCurrentPersistGeneration(b, 'autosave'), true, 'b is the latest');
    resetSaveCodecAsyncForTests();
  });

  test('persistSavepointWithReasonAsync: an OLDER autosave whose worker reply resolves AFTER a NEWER autosave has started never writes to storage', async () => {
    resetSaveCodecAsyncForTests();
    // Ordered fake worker: postMessage() queues replies, and this test
    // drains them in a chosen order below rather than FIFO microtask order —
    // simulating a real worker round trip where request A can genuinely
    // resolve AFTER request B, even though A was posted first.
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      const savepointA = spWithTick(10, 1, '2026-01-01T00:00:00Z');
      const savepointB = spWithTick(20, 2, '2026-01-01T00:00:10Z');

      const promiseA = persistSavepointWithReasonAsync(storage, savepointA, 'autosave');
      const promiseB = persistSavepointWithReasonAsync(storage, savepointB, 'autosave');

      const worker = fake.instances[0];
      assert.equal(worker._queue.length, 2, 'both requests must have reached the same worker instance');
      worker._queue[1](); // B's compression finishes first
      await Promise.resolve();
      await Promise.resolve();
      worker._queue[0](); // A's compression finishes second (stale by now)
      await Promise.resolve();
      await Promise.resolve();

      const [resultA, resultB] = await Promise.all([promiseA, promiseB]);

      assert.equal(resultB.ok, true, 'the newer request must succeed normally');
      assert.equal(resultA.ok, false);
      assert.equal(resultA.reason, 'superseded', 'the older request must report superseded, not storage-error or stale-overwrite');

      const keys = storage._keys();
      assert.equal(keys.length, 1, 'exactly one savepoint slot should exist');
      const decoded = JSON.parse(decode(storage._raw(keys[0])));
      assert.equal(decoded.snapshotTick, 20, 'the persisted savepoint must be the NEWER one (B), never the stale A');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('two AUTOSAVE persists with NO overlap (sequential awaits) both succeed normally', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const storage = memStorage();
      const r1 = await persistSavepointWithReasonAsync(storage, spWithTick(0, 1, '2026-01-01T00:00:00Z'), 'autosave');
      assert.equal(r1.ok, true);
      const r2 = await persistSavepointWithReasonAsync(storage, spWithTick(5, 2, '2026-01-01T00:01:00Z'), 'autosave');
      assert.equal(r2.ok, true);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe("BUG-798 round REJECT finding A: EXPLICIT saves have priority over autosave", () => {
  test("an in-flight EXPLICIT save is NEVER superseded by an autosave that starts (and finishes) before it", async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      const explicitSp = spWithTick(100, 1, '2026-01-01T00:00:00Z');
      const autosaveSp = spWithTick(101, 2, '2026-01-01T00:00:05Z');

      // Explicit save (Save As) starts FIRST; the autosave timer fires
      // WHILE the explicit save's encode is still in flight — exactly
      // Aaron's dogfood shape (~8% of manual saves on his city).
      const explicitPromise = persistSavepointWithReasonAsync(storage, explicitSp, 'explicit');
      const autosavePromise = persistSavepointWithReasonAsync(storage, autosaveSp, 'autosave');

      const worker = fake.instances[0];
      assert.equal(worker._queue.length, 2);
      // The AUTOSAVE's compression finishes FIRST (it was a smaller/faster
      // job, or simply won the round trip) — the worst case for the old,
      // single-counter design.
      worker._queue[1]();
      await Promise.resolve();
      await Promise.resolve();
      worker._queue[0](); // explicit's compression finishes second
      await Promise.resolve();
      await Promise.resolve();

      const [explicitResult, autosaveResult] = await Promise.all([explicitPromise, autosavePromise]);

      assert.equal(explicitResult.ok, true, 'the EXPLICIT save must succeed — it must never read as superseded by an autosave');
      assert.notEqual(explicitResult.reason, 'superseded');
      // The autosave, having started after the explicit save and lost the
      // priority contest, is discarded ("an autosave in flight is the one
      // discarded" — the round's own wording).
      assert.equal(autosaveResult.ok, false);
      assert.equal(autosaveResult.reason, 'superseded');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('an in-flight AUTOSAVE is discarded once an EXPLICIT save starts (the round\'s literal example)', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      const autosaveSp = spWithTick(1, 1, '2026-01-01T00:00:00Z');
      const explicitSp = spWithTick(2, 2, '2026-01-01T00:00:05Z');

      const autosavePromise = persistSavepointWithReasonAsync(storage, autosaveSp, 'autosave');
      const explicitPromise = persistSavepointWithReasonAsync(storage, explicitSp, 'explicit');

      const worker = fake.instances[0];
      worker._queue[0](); // autosave resolves first
      await Promise.resolve();
      await Promise.resolve();
      worker._queue[1](); // explicit resolves second
      await Promise.resolve();
      await Promise.resolve();

      const [autosaveResult, explicitResult] = await Promise.all([autosavePromise, explicitPromise]);
      assert.equal(autosaveResult.ok, false);
      assert.equal(autosaveResult.reason, 'superseded');
      assert.equal(explicitResult.ok, true);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('two EXPLICIT saves in a row: the OLDER is superseded (only a later explicit can do that), never silently reported by callers as a no-op success', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      const spSave = spWithTick(1, 1, '2026-01-01T00:00:00Z');
      const spSaveAs = spWithTick(2, 2, '2026-01-01T00:00:01Z');

      const savePromise = persistSavepointWithReasonAsync(storage, spSave, 'explicit');
      const saveAsPromise = persistSavepointWithReasonAsync(storage, spSaveAs, 'explicit');

      const worker = fake.instances[0];
      worker._queue[1](); // Save As (the later explicit) finishes first
      await Promise.resolve();
      await Promise.resolve();
      worker._queue[0](); // Save (the older explicit) finishes second — stale
      await Promise.resolve();
      await Promise.resolve();

      const [saveResult, saveAsResult] = await Promise.all([savePromise, saveAsPromise]);
      assert.equal(saveAsResult.ok, true);
      assert.equal(saveResult.ok, false);
      assert.equal(saveResult.reason, 'superseded', 'the OLDER explicit call must report superseded so the CALLER (store.tsx) knows to retry rather than assume success');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('BUG-798 round REJECT finding B: precomputedEncoded is actually wired through (exactly one encode per async persist)', () => {
  test('persistSavepointWithReason: an explicit precomputedEncoded string is written VERBATIM, never recomputed via encode(json)', () => {
    const storage = memStorage();
    const sp = spWithTick(42, 1, '2026-01-01T00:00:00Z');
    const marker = 'BUG798_PRECOMPUTED_MARKER_NOT_REAL_LZ_OUTPUT';
    const result = persistSavepointWithReason(storage, sp, new Date('2026-01-01T00:00:00Z'), { precomputedEncoded: marker });
    assert.equal(result.ok, true);
    const keys = storage._keys();
    assert.equal(keys.length, 1);
    const raw = storage._raw(keys[0]);
    // If a future change drops `precomputedEncoded` from the opts spread (or
    // stops honouring it) the write would fall back to
    // `encode(JSON.stringify(savepoint))` instead — real LZ-compressed
    // bytes, which can NEVER equal this literal marker string. This assert
    // reds the instant that wiring breaks.
    assert.equal(raw, marker, 'the write must use precomputedEncoded VERBATIM, not recompute encode(json) internally');
  });

  test('persistSavepointWithReasonAsync: exactly one encode happens end-to-end (worker count 1, sync fallback count 0)', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const storage = memStorage();
      const sp = spWithTick(7, 1, '2026-01-01T00:00:00Z');
      const result = await persistSavepointWithReasonAsync(storage, sp, 'explicit');
      assert.equal(result.ok, true);
      assert.equal(__saveCodecAsyncProbe.workerEncodeCount, 1, 'exactly one worker-side encode');
      assert.equal(__saveCodecAsyncProbe.syncFallbackCount, 0, 'no synchronous fallback should also have fired for the same persist');

      // Cross-check against the marker test above: the actual stored bytes
      // must be real LZ output (decodable), not a duplicate encode's
      // different-but-still-valid output either — same string, one call.
      const keys = storage._keys();
      const raw = storage._raw(keys[0]);
      const decoded = JSON.parse(decode(raw));
      assert.equal(decoded.snapshotTick, 7);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('opus-reround-bug798: the stored raw slot value is the WORKER\'S SENTINEL, byte-for-byte — reds the instant the wrapper stops forwarding precomputedEncoded', async () => {
    // Prior version of this test only compared stored bytes against
    // `encode(json)`, which is ALSO what a broken wrapper (one that drops
    // `precomputedEncoded` from the opts spread and lets
    // `persistSavepointWithReason` recompute `encode(json)` itself
    // internally) would produce — byte-identical, so that comparison could
    // never catch the regression. The sentinel-returning worker makes the
    // two paths produce visibly DIFFERENT bytes, so this assertion is
    // load-bearing: it can only pass if the async wrapper's `encoded` value
    // (the worker's sentinel-prefixed reply) is the exact string that lands
    // in storage.
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ sentinel: true });
    try {
      const storage = memStorage();
      const sp = spWithTick(7, 1, '2026-01-01T00:00:00Z');
      const result = await persistSavepointWithReasonAsync(storage, sp, 'explicit');
      assert.equal(result.ok, true);

      const keys = storage._keys();
      assert.equal(keys.length, 1);
      const raw = storage._raw(keys[0]);
      const expectedSentinel = `SENTINEL_${encode(JSON.stringify(sp))}`;
      assert.equal(raw, expectedSentinel, "the stored bytes must be the worker's sentinel reply verbatim, not a locally-recomputed encode(json)");
      assert.notEqual(raw, encode(JSON.stringify(sp)), 'sanity: the sentinel and the plain encode() output must actually differ, or this test proves nothing');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('BUG-798 round REJECT finding D: the reset boundary fences ALL in-flight persists (BUG-687 shape)', () => {
  test('fencePersistGeneration invalidates an in-flight AUTOSAVE generation', () => {
    resetSaveCodecAsyncForTests();
    const gen = nextPersistGeneration('autosave');
    assert.equal(isCurrentPersistGeneration(gen, 'autosave'), true);
    fencePersistGeneration();
    assert.equal(isCurrentPersistGeneration(gen, 'autosave'), false, 'a reset must invalidate an in-flight autosave generation');
    resetSaveCodecAsyncForTests();
  });

  test('fencePersistGeneration invalidates an in-flight EXPLICIT generation too — unlike the autosave-vs-explicit priority rule, the reset fence is unconditional', () => {
    resetSaveCodecAsyncForTests();
    const gen = nextPersistGeneration('explicit');
    assert.equal(isCurrentPersistGeneration(gen, 'explicit'), true);
    fencePersistGeneration();
    assert.equal(isCurrentPersistGeneration(gen, 'explicit'), false, 'a reset must invalidate an in-flight EXPLICIT generation too — no priority survives a city replace');
    resetSaveCodecAsyncForTests();
  });

  test('end-to-end: a savepoint from the OLD city, in flight across a reset, never reaches storage — even though its own generation looks otherwise unsuperseded', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      // A lineage-less savepoint (the exact BUG-687 shape: an old-city
      // savepoint whose lineageId is not yet stamped) starts persisting...
      const oldCitySp = spWithTick(999, 1, '2026-01-01T00:00:00Z');
      delete oldCitySp.lineageId;
      const oldCityPromise = persistSavepointWithReasonAsync(storage, oldCitySp, 'autosave');

      // ...then a reset happens (store.tsx's reset dispatch path calls this
      // FIRST, before the wipe/lineage mint) before that persist's own
      // encode has resolved.
      fencePersistGeneration();

      const worker = fake.instances[0];
      assert.equal(worker._queue.length, 1);
      worker._queue[0]();
      await Promise.resolve();
      await Promise.resolve();

      const oldCityResult = await oldCityPromise;
      assert.equal(oldCityResult.ok, false);
      assert.equal(oldCityResult.reason, 'superseded', 'the pre-reset persist must be fenced off, never reaching storage under the new city\'s lineage');
      assert.equal(storage._keys().length, 0, 'nothing from the old city may land in storage after the reset fence');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('opus-reround-bug798 P2 finding 1: settlePersistGeneration runs even when something throws between mint and await', () => {
  test('a throw during JSON.stringify (between nextPersistGeneration and the await) does not leak explicitInFlightCount — a later autosave still lands', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const storage = memStorage();

      // A savepoint with a property whose getter throws on enumeration —
      // JSON.stringify hits it and throws, BEFORE persistSavepointWithReasonAsync
      // ever reaches its `await`. Without the try/finally fix, the
      // `explicitInFlightCount` bump from `nextPersistGeneration('explicit')`
      // above that throw point is never released, silently dooming every
      // future autosave for the rest of the session (nextPersistGeneration's
      // `explicitInFlightCount > 0` branch marks every subsequent autosave
      // generation as doomed at birth).
      const poisoned = spWithTick(1, 1, '2026-01-01T00:00:00Z');
      Object.defineProperty(poisoned, 'poison', {
        enumerable: true,
        get() {
          throw new Error('boom-stringify');
        },
      });

      await assert.rejects(
        () => persistSavepointWithReasonAsync(storage, poisoned, 'explicit'),
        /boom-stringify/,
        'the poisoned savepoint must still reject with the real error, not be swallowed'
      );

      // If explicitInFlightCount leaked, THIS autosave would be born
      // "doomed" (see nextPersistGeneration's explicitInFlightCount>0
      // branch) and would read as superseded even though nothing else is
      // racing it — the exact silent-forever-broken-autosave shape the
      // round warned about.
      const autosaveSp = spWithTick(2, 1, '2026-01-01T00:00:01Z');
      const result = await persistSavepointWithReasonAsync(storage, autosaveSp, 'autosave');
      assert.equal(result.ok, true, 'a leaked explicitInFlightCount must not doom every future autosave forever');
      assert.notEqual(result.reason, 'superseded');

      // Belt and braces: a SECOND autosave right after must also be fine —
      // proving the leak, if any, is not merely delayed by one cycle.
      const autosaveSp2 = spWithTick(3, 1, '2026-01-01T00:00:02Z');
      const result2 = await persistSavepointWithReasonAsync(storage, autosaveSp2, 'autosave');
      assert.equal(result2.ok, true);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });

  test('a throw during JSON.stringify for an AUTOSAVE itself is also safe (settle still runs, no lingering doomed-generation entry)', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker();
    try {
      const storage = memStorage();
      const poisoned = spWithTick(1, 1, '2026-01-01T00:00:00Z');
      Object.defineProperty(poisoned, 'poison', {
        enumerable: true,
        get() {
          throw new Error('boom-stringify-autosave');
        },
      });
      await assert.rejects(() => persistSavepointWithReasonAsync(storage, poisoned, 'autosave'), /boom-stringify-autosave/);

      // A later, unrelated autosave must land normally.
      const sp2 = spWithTick(2, 1, '2026-01-01T00:00:01Z');
      const result = await persistSavepointWithReasonAsync(storage, sp2, 'autosave');
      assert.equal(result.ok, true);
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});

describe('opus-reround-bug798 P2 finding 2: explicit writes are serialised — a later generation is never overwritten by an earlier one', () => {
  test('runSerializedExplicitWrite: an earlier generation arriving LATE (after a higher generation already wrote) is skipped, not applied', async () => {
    resetSaveCodecAsyncForTests();
    const applied = [];
    // Enqueue gen 2 first — it "wins" and writes.
    const p1 = runSerializedExplicitWrite(2, () => {
      applied.push(2);
      return 'result-2';
    });
    // Enqueue gen 1 SECOND (arriving late relative to gen 2, e.g. a slower
    // encode round trip for an OLDER explicit call) — must be skipped since
    // 1 < the high-water mark (2) by the time it is this write's turn.
    const p2 = runSerializedExplicitWrite(1, () => {
      applied.push(1);
      return 'result-1';
    });
    const [r1, r2] = await Promise.all([p1, p2]);
    assert.equal(r1, 'result-2', 'the higher generation\'s write must actually run and return its result');
    assert.equal(r2, undefined, 'the lower (late-arriving) generation\'s write function must be SKIPPED entirely — never applied');
    assert.deepEqual(applied, [2], 'gen 1\'s write body must never have executed at all, not merely have its result discarded');
    resetSaveCodecAsyncForTests();
  });

  test('runSerializedExplicitWrite: generations arriving in ASCENDING order all apply normally (no false-positive skip)', async () => {
    resetSaveCodecAsyncForTests();
    const applied = [];
    const r1 = await runSerializedExplicitWrite(1, () => {
      applied.push(1);
      return 'a';
    });
    const r2 = await runSerializedExplicitWrite(2, () => {
      applied.push(2);
      return 'b';
    });
    const r3 = await runSerializedExplicitWrite(3, () => {
      applied.push(3);
      return 'c';
    });
    assert.deepEqual(applied, [1, 2, 3]);
    assert.deepEqual([r1, r2, r3], ['a', 'b', 'c']);
    resetSaveCodecAsyncForTests();
  });

  test('runSerializedExplicitWrite: writes execute STRICTLY one at a time, never interleaved', async () => {
    resetSaveCodecAsyncForTests();
    let concurrent = 0;
    let maxConcurrent = 0;
    const order = [];
    const makeWrite = (id) => async () => {
      concurrent += 1;
      maxConcurrent = Math.max(maxConcurrent, concurrent);
      await Promise.resolve(); // yield once, to give a bug a chance to interleave
      order.push(id);
      concurrent -= 1;
      return id;
    };
    // runSerializedExplicitWrite's `write` callback itself is synchronous in
    // production (persistSavepointWithReason never awaits), but this proves
    // the CHAIN itself serialises even if a write were slower than another.
    await Promise.all([
      runSerializedExplicitWrite(1, makeWrite(1)),
      runSerializedExplicitWrite(2, makeWrite(2)),
      runSerializedExplicitWrite(3, makeWrite(3)),
    ]);
    assert.equal(maxConcurrent, 1, 'at most one write body may be "in flight" at any instant');
    resetSaveCodecAsyncForTests();
  });

  test('end-to-end via persistSavepointWithReasonAsync: two explicit persists racing never leave the slot holding the OLDER tick', async () => {
    resetSaveCodecAsyncForTests();
    const fake = installFakeWorker({ resolveOrder: 'manual' });
    try {
      const storage = memStorage();
      // Save (older generation, LOWER tick) starts first; SaveAs (newer
      // generation, HIGHER tick) starts second — but SaveAs's encode
      // resolves and its WRITE is enqueued and completes FIRST (the
      // adversarial ordering the round's report described).
      const olderSp = spWithTick(11, 1, '2026-01-01T00:00:00Z');
      const newerSp = spWithTick(20, 2, '2026-01-01T00:00:05Z');

      const olderPromise = persistSavepointWithReasonAsync(storage, olderSp, 'explicit');
      const newerPromise = persistSavepointWithReasonAsync(storage, newerSp, 'explicit');

      const worker = fake.instances[0];
      assert.equal(worker._queue.length, 2);
      worker._queue[1](); // newer (tick 20) encode finishes first
      await Promise.resolve();
      await Promise.resolve();
      worker._queue[0](); // older (tick 11) encode finishes second
      await Promise.resolve();
      await Promise.resolve();

      const [olderResult, newerResult] = await Promise.all([olderPromise, newerPromise]);
      assert.equal(newerResult.ok, true);
      assert.equal(olderResult.ok, false);
      assert.equal(olderResult.reason, 'superseded', 'the older generation must never be allowed to write after the newer one already has');

      const keys = storage._keys();
      assert.equal(keys.length, 1, 'exactly one slot must hold data');
      const decoded = JSON.parse(decode(storage._raw(keys[0])));
      assert.equal(decoded.snapshotTick, 20, 'the slot must hold the NEWER generation\'s tick, never the older one\'s — regardless of encode completion order');
    } finally {
      fake.restore();
      resetSaveCodecAsyncForTests();
    }
  });
});
