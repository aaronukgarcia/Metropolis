/**
 * INDEPENDENT DESTRUCTIVE RE-ROUND — BUG-691 (commitDebug silent-failure fix).
 *
 * Attacker: opus-reround-bug691 (NOT the author, NOT the round-1 attacker).
 *
 * Round 1 REJECTed on F1 (ms-resolution DBG- id collision) and F2 (drainQueue
 * filter() double-delete). This file attacks the REWORK, on the axes the
 * re-round brief names:
 *
 *   R1 — mintCommitId()'s monotonic counter is PER MODULE INSTANCE and resets
 *        on every module (re)load. Two coexisting instances (main thread +
 *        worker, or a cache-busted re-import) minting in the same millisecond
 *        still produce IDENTICAL ids. The collision is therefore NOT gone,
 *        only made unreachable from a single instance — so F2's splice fix is
 *        the load-bearing half. R1 proves both halves together: the collision
 *        is reproducible, AND no data is lost when it happens.
 *   R2 — dedup honesty post-fix: two DISTINCT outage commits collapse into ONE
 *        MET-V857 row. Adjudicated ACCEPTABLE only because (a) `count` grows
 *        so the second is never silently dropped, and (b) BOTH commits are
 *        individually recoverable from the persisted queue with distinct ids.
 *        Pinned here so a future change that breaks either leg reds.
 *   R3 — the clean-drain flag across a FULL lifecycle in one test: outage ->
 *        partial drain (one sinks, one 503s) -> indicator stays down, sunk
 *        entry gone, refused entry retained -> next fully-clean drain clears.
 *   R4 — F6 mid-BATCH: a garbage payload arriving while GOOD entries are
 *        already queued must not disturb them, must not touch sink state, and
 *        must not block their later drain.
 *   R5 — commitqueue.enqueueCommit's "never throws" contract under a directly
 *        unserializable payload (defense in depth, called without backend's
 *        up-front screen).
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { QUEUE_KEY, enqueueCommit } from '../src/sim/commitqueue.ts';

/** Install a fake localStorage for the duration of `fn`. Returns the backing
 * Map so a test can inspect/seed the persisted queue directly. */
function withFakeLocalStorage(fn) {
  return async (...args) => {
    const originalDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage');
    const mem = new Map();
    Object.defineProperty(globalThis, 'localStorage', {
      value: {
        getItem: (k) => (mem.has(k) ? mem.get(k) : null),
        setItem: (k, v) => void mem.set(k, String(v)),
      },
      configurable: true,
    });
    try {
      await fn(mem, ...args);
    } finally {
      if (originalDescriptor) Object.defineProperty(globalThis, 'localStorage', originalDescriptor);
      else delete globalThis.localStorage;
    }
  };
}

function installFetch(impl) {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'fetch');
  const calls = [];
  Object.defineProperty(globalThis, 'fetch', {
    value: async (url, init) => {
      calls.push({ url, init });
      return impl(url, init);
    },
    configurable: true,
    writable: true,
  });
  return {
    calls,
    restore() {
      if (original) Object.defineProperty(globalThis, 'fetch', original);
      else delete globalThis.fetch;
    },
  };
}

function readQueue(mem) {
  const raw = mem.get(QUEUE_KEY);
  return raw ? JSON.parse(raw) : [];
}

/**
 * BUG-703: a genuine sink success now requires a verifiable ack, not just a
 * 2xx status — see backend.ts's postToSink. Every fake "the real sink is up"
 * response in this file uses this helper so it echoes the exact id the
 * client sent (mirroring tools/debugsink/server.js's real ack shape:
 * `{ ok: true, sink: 'metropolis-debugsink', id }`).
 */
function sinkAck(init) {
  const body = JSON.parse(init.body);
  return { ok: true, status: 200, json: async () => ({ sink: 'metropolis-debugsink', id: body.id }) };
}

let freshCounter = 0;
/** A "page reload" / second execution context: a genuinely fresh module
 * instance with its own commitIdCounter, errorLog and sink-state module vars. */
function freshBackend() {
  freshCounter += 1;
  return import(`../src/sim/backend.ts?reround=${freshCounter}`);
}

// --------------------------------------------------------------------- R1 ---
test(
  'RE-ROUND R1: mintCommitId is per-INSTANCE monotonic only — two module instances minting in the SAME millisecond still collide, and the splice fix (F2) is what keeps that from losing data',
  withFakeLocalStorage(async (mem) => {
    const realNow = Date.now;
    Date.now = () => 1_757_500_000_000; // frozen: "same millisecond" is deterministic
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    let a;
    let b;
    try {
      // Two coexisting module instances (main thread + a worker, or a reload
      // that left the persisted queue behind). Each starts commitIdCounter at 0.
      const A = await freshBackend();
      const B = await freshBackend();
      a = await A.commitDebug({ tag: 'FIRST-PAYLOAD' });
      b = await B.commitDebug({ tag: 'SECOND-PAYLOAD' });
    } finally {
      down.restore();
      Date.now = realNow;
    }

    // The finding, pinned as fact rather than asserted away: the counter does
    // NOT make the id globally unique, only instance-unique.
    assert.equal(
      a.id,
      b.id,
      'R1 (finding): a per-module counter reset by a reload/worker DOES still collide within one millisecond'
    );

    const queued = readQueue(mem);
    assert.equal(queued.length, 2, 'precondition: both commits are on the shared persisted queue, colliding ids and all');

    // Now the pairing that matters: a drain over a colliding queue must remove
    // AT MOST the one entry it actually confirmed sunk (F2's splice), so the
    // collision degrades to a cosmetic id clash instead of silent data loss.
    let n = 0;
    const bodies = [];
    const flaky = installFetch(async (url, init) => {
      n += 1;
      bodies.push(JSON.parse(init.body));
      return n <= 2 ? sinkAck(init) : { ok: false, status: 503 };
    });
    try {
      const C = await freshBackend();
      const r = await C.commitDebug({ live: true });
      assert.equal(r.ok, true, 'the live commit sinks (POST #1), triggering the drain');
    } finally {
      flaky.restore();
    }

    assert.match(
      JSON.stringify(bodies[bodies.length - 1].payload),
      /SECOND-PAYLOAD/,
      'precondition: the refused (503) POST was the SECOND colliding entry'
    );
    const remaining = readQueue(mem);
    assert.equal(
      remaining.length,
      1,
      'R1/F2: exactly ONE entry survives — the pre-fix filter() would have deleted BOTH on the first 200 (silent loss)'
    );
    assert.match(
      JSON.stringify(remaining[0].payload),
      /SECOND-PAYLOAD/,
      'R1/F2: the survivor is the entry the sink actually refused, not the one it accepted'
    );
  })
);

// --------------------------------------------------------------------- R2 ---
test(
  'RE-ROUND R2: two DISTINCT outage commits produce ONE MET-V857 row with count=2 — the second is never dropped (count) and stays individually recoverable on the queue with its own id',
  withFakeLocalStorage(async (mem) => {
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    let A;
    let a;
    let b;
    try {
      A = await freshBackend(); // fresh instance => empty errorLog, clean accounting
      a = await A.commitDebug({ distinct: 'ONE' });
      b = await A.commitDebug({ distinct: 'TWO' });
    } finally {
      down.restore();
    }

    assert.notEqual(a.id, b.id, 'within ONE instance the ids are genuinely distinct (F1 fix holds)');

    const direct = A.recentErrors().filter(
      (e) => e.code === 'MET-V857' && !/drain retry failed/.test(e.msg)
    );
    assert.equal(direct.length, 1, 'both outages collapse into one row (message no longer carries the id)');
    assert.equal(
      direct[0].count,
      2,
      'R2 leg (a): the SECOND failure is accounted for by count — it is aggregated, not hidden'
    );

    // Leg (b): honest aggregation is only acceptable because the individual
    // failures remain recoverable somewhere. They do — on the queue.
    const queued = readQueue(mem);
    assert.equal(queued.length, 2, 'R2 leg (b): both commits are on the queue as separate entries');
    assert.deepEqual(
      queued.map((e) => e.id).sort(),
      [a.id, b.id].sort(),
      'R2 leg (b): each queued entry carries its own distinct commit id, so neither outage is unrecoverable'
    );

    // Documented limitation (P3, NOT a blocker): recordError's dedup bumps
    // count/lastAt but never updates `action`, so the row names only the
    // FIRST occurrence's id. Pinned so nobody reads `action` as a full index.
    assert.equal(
      direct[0].action,
      `commit ${a.id}`,
      'known limitation: `action` names the first occurrence only; the queue, not the error row, is the per-commit index'
    );
  })
);

// --------------------------------------------------------------------- R3 ---
test(
  'RE-ROUND R3: full clean-drain lifecycle in ONE test — outage -> partial drain keeps the indicator down and retains only the refused entry -> a fully clean drain finally clears it',
  withFakeLocalStorage(async (mem) => {
    const A = await freshBackend();

    // Phase 1: sink down, two commits strand on the queue.
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    let first;
    let second;
    try {
      first = await A.commitDebug({ n: 'ONE' });
      second = await A.commitDebug({ n: 'TWO' });
    } finally {
      down.restore();
    }
    assert.equal(A.debugSinkStatus().unreachable, true, 'phase 1: indicator down');
    assert.equal(readQueue(mem).length, 2, 'phase 1: two entries stranded');

    // Phase 2: PARTIAL drain. Live commit sinks (POST #1), first queued entry
    // sinks (POST #2), second queued entry is refused (POST #3).
    let n = 0;
    const partial = installFetch(async (url, init) => {
      n += 1;
      return n <= 2 ? sinkAck(init) : { ok: false, status: 503 };
    });
    try {
      const r = await A.commitDebug({ live: 1 });
      assert.equal(r.ok, true, 'phase 2: the live commit itself succeeded');
    } finally {
      partial.restore();
    }
    assert.equal(
      A.debugSinkStatus().unreachable,
      true,
      'phase 2: an UNCLEAN drain must leave/put the indicator DOWN — clearing it here was round-1 F3'
    );
    const afterPartial = readQueue(mem);
    assert.equal(afterPartial.length, 1, 'phase 2: the sunk entry is gone, the refused entry is retained');
    assert.equal(afterPartial[0].id, second.id, 'phase 2: the retained entry is the one the sink refused');
    assert.notEqual(afterPartial[0].id, first.id, 'phase 2: the entry that got a 2xx really was removed');
    const drainRows = A.recentErrors().filter(
      (e) => e.code === 'MET-V857' && /drain retry failed/.test(e.msg)
    );
    assert.equal(drainRows.length, 1, 'phase 2: the drain failure recorded its own registry row — never silent');
    assert.equal(drainRows[0].count, 1);

    // Phase 3: the sink is genuinely back. Live commit + a fully clean drain.
    const up = installFetch(async (url, init) => sinkAck(init));
    try {
      const r = await A.commitDebug({ live: 2 });
      assert.equal(r.ok, true);
    } finally {
      up.restore();
    }
    assert.equal(readQueue(mem).length, 0, 'phase 3: the queue is fully drained');
    assert.equal(
      A.debugSinkStatus().unreachable,
      false,
      'phase 3: only a fully CLEAN drain is allowed to clear the indicator'
    );
    assert.equal(A.debugSinkStatus().sinceMs, null, 'phase 3: the outage stamp is cleared too');
  })
);

// --------------------------------------------------------------------- R4 ---
test(
  'RE-ROUND R4 (F6 mid-BATCH): a garbage payload arriving while GOOD entries are already queued leaves those entries untouched, leaves sink state untouched, and does not block their later drain',
  withFakeLocalStorage(async (mem) => {
    const A = await freshBackend();

    // Two good entries already queued from an earlier outage.
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    try {
      await A.commitDebug({ good: 'ONE' });
      await A.commitDebug({ good: 'TWO' });
    } finally {
      down.restore();
    }
    const beforeBad = readQueue(mem);
    assert.equal(beforeBad.length, 2, 'precondition: two good entries queued');
    const snapshot = JSON.stringify(beforeBad);
    const sinkStateBefore = A.debugSinkStatus().unreachable;

    // Now a commit whose payload cannot be serialized at all.
    const circular = { tag: 'BAD' };
    circular.self = circular;
    const up = installFetch(async (url, init) => sinkAck(init));
    let bad;
    try {
      bad = await A.commitDebug(circular);
      assert.equal(bad.ok, false, 'the bad commit fails');
      assert.equal(bad.queued, false, 'the bad commit is not queued');
      assert.equal(up.calls.length, 0, 'R4: the sink is never contacted for an unserializable payload');
    } finally {
      up.restore();
    }

    assert.equal(
      JSON.stringify(readQueue(mem)),
      snapshot,
      'R4: the previously queued GOOD entries are byte-identical after the bad commit — a payload defect never mutates the queue'
    );
    assert.equal(
      A.debugSinkStatus().unreachable,
      sinkStateBefore,
      'R4: sink reachability state is untouched by a payload defect (neither raised nor cleared)'
    );
    const v864 = A.recentErrors().filter((e) => e.code === 'MET-V864');
    assert.equal(v864.length, 1, 'R4: exactly one MET-V864 row for the one bad payload');
    assert.doesNotMatch(v864[0].msg, /unreachable/, 'R4: a payload defect is never reported as a network outage');

    // And the good entries still drain normally on the next healthy commit.
    const up2 = installFetch(async (url, init) => sinkAck(init));
    try {
      await A.commitDebug({ live: true });
    } finally {
      up2.restore();
    }
    assert.equal(readQueue(mem).length, 0, 'R4: the good entries drained on the next healthy commit — never orphaned by the bad one');
    assert.equal(A.debugSinkStatus().unreachable, false, 'R4: a clean drain clears the indicator as normal');
  })
);

// --------------------------------------------------------------------- R5 ---
test(
  'RE-ROUND R5: enqueueCommit honors its "never throws" contract when handed a directly unserializable payload (defense in depth, with backend\'s up-front screen bypassed)',
  () => {
    const mem = new Map();
    const storage = {
      getItem: (k) => (mem.has(k) ? mem.get(k) : null),
      setItem: (k, v) => void mem.set(k, String(v)),
    };
    const circular = { tag: 'BAD' };
    circular.self = circular;

    let outcome;
    assert.doesNotThrow(() => {
      outcome = enqueueCommit(storage, 'DBG-DIRECT', circular, '2026-09-05T00:00:00.000Z');
    }, 'R5: enqueueCommit must never throw — its own doc comment promises it');
    assert.equal(outcome.ok, false, 'R5: it reports failure instead');
    assert.equal(outcome.unserializable, true, 'R5: and names the reason distinctly, so the caller cannot blame storage quota');
    assert.equal(mem.has(QUEUE_KEY), false, 'R5: nothing half-written to storage');
  }
);

// A payload with a throwing toJSON is the other real shape of "unserializable"
// (getters/toJSON are where this bites in practice, not just cycles).
test(
  'RE-ROUND R5b: a payload whose toJSON() throws is handled on the same path (MET-V864, sink untouched, promise resolves)',
  withFakeLocalStorage(async (mem) => {
    const A = await freshBackend();
    const up = installFetch(async (url, init) => sinkAck(init));
    try {
      const nasty = {
        toJSON() {
          throw new Error('boom from toJSON');
        },
      };
      const r = await A.commitDebug(nasty);
      assert.equal(r.ok, false);
      assert.equal(r.queued, false);
      assert.equal(up.calls.length, 0, 'the sink is never contacted');
      const v864 = A.recentErrors().filter((e) => e.code === 'MET-V864');
      assert.equal(v864.length, 1);
      assert.match(v864[0].msg, /boom from toJSON/, 'the real reason is surfaced, not swallowed');
    } finally {
      up.restore();
    }
    assert.equal(readQueue(mem).length, 0, 'nothing queued');
    assert.equal(A.debugSinkStatus().unreachable, false, 'sink state untouched');
  })
);
