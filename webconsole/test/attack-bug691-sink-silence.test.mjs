/**
 * INDEPENDENT DESTRUCTIVE ROUND — BUG-691 (commitDebug silent-failure fix).
 *
 * Attacker: opus-round-bug691 (NOT the author).
 *
 * REWORK PASS (2026-09-05): the round's findings below have now been FIXED
 * in backend.ts/commitqueue.ts. Per the round protocol, this file's
 * assertions are updated in place to pin the CORRECTED contract (not to
 * delete the history of what was found) — each test still documents the
 * original defect in its title/comments for the record, and now asserts the
 * fixed behavior instead of the defect.
 *
 * Findings pinned here (now FIXED, contract asserted):
 *   F1 (P1, FIXED) — the DBG- commit id used to be minted from Date.now() at
 *             1ms resolution: two commits in the same millisecond got the
 *             SAME id, and the MET-V857 message (which embedded the id) was
 *             therefore byte-identical, so recordError's dedupeKey collapsed
 *             the second failure into the first (one row, count=2) — masking
 *             a real second failure as a mere repeat. Fixed on BOTH halves:
 *             a monotonic per-module counter suffix makes the id
 *             collision-free regardless of clock resolution, AND the id was
 *             moved OUT of the dedupe-bearing message into `action` (outside
 *             dedupeKey) — so even if a future id ever collided, the message
 *             aggregating via `count` is now the correct/intended dedup
 *             behavior (F2 guarantees no data is lost underneath it either
 *             way).
 *   F2 (P1, FIXED) — drainQueue's removal step used to be
 *             `filter(e => e.id !== id)`, which deletes EVERY entry sharing
 *             that id — a real risk while ids could collide (F1). Removal is
 *             now index-based (`findIndex` + `splice`, at most one entry per
 *             confirmed POST), so even a legacy/forced id collision on the
 *             queue can never double-delete.
 *   F3 (P2, FIXED) — commitDebug used to clear sinkLastUnreachableAt BEFORE
 *             awaiting drainQueue, and drainQueue recorded nothing on its own
 *             postToSink failures — a sink dying mid-drain was completely
 *             silent AND the indicator was affirmatively (wrongly) cleared.
 *             drainQueue now records its own MET-V857 row and re-stamps the
 *             timestamp on every failed retry, and commitDebug only clears
 *             the indicator once the drain reports itself fully clean.
 *   F4 (P2, FIXED) — debugSinkStatus() was module state only: across a page
 *             reload (fresh module) it reported "reachable" even with a
 *             non-empty persisted queue. Module init now seeds the indicator
 *             from `pendingCommits() > 0` — a non-empty leftover queue is a
 *             reliable "the last thing that happened was an unreached sink"
 *             signal even with no recoverable historical timestamp.
 *   F5 (P3, FIXED) — `sinceMs` used to return the raw absolute epoch
 *             timestamp; a consumer rendering "down for {sinceMs}ms" would
 *             have printed ~1.7e12. `debugSinkStatus()` now computes and
 *             returns genuinely ELAPSED milliseconds on every call.
 *   F6 (P2, FIXED, inherited from BUG-607 but newly load-bearing here) — an
 *             unserializable payload used to reach postToSink's own
 *             JSON.stringify throw (swallowed as "unreachable" -- blaming
 *             the NETWORK), then enqueueCommit's un-guarded JSON.stringify
 *             threw straight out of a function whose doc comment promises
 *             "never throws". commitDebug now screens the payload up front
 *             with its own MET-V864 code, never touches the sink or the
 *             indicator, and resolves (never rejects); enqueueCommit also
 *             guards its own JSON.stringify calls as defense in depth.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { QUEUE_KEY } from '../src/sim/commitqueue.ts';
import { commitDebug, debugSinkStatus, recentErrors } from '../src/sim/backend.ts';

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

/** Freeze Date.now to a fixed value so "same millisecond" is deterministic,
 * not a race we hope to win. This is exactly the real-world shape when two
 * commits are fired back to back on a fast machine. */
function withFrozenClock(ms, fn) {
  return async (...args) => {
    const realNow = Date.now;
    Date.now = () => ms;
    try {
      await fn(...args);
    } finally {
      Date.now = realNow;
    }
  };
}

// ---------------------------------------------------------------- F1 + F2 ---
// NOTE: this file's tests share ONE backend.ts module instance (errorLog is
// module-level), so a direct-failure message that already exists from an
// earlier test in this run does not create a fresh array entry — it bumps
// that row's `count`. Every assertion below tracks a SPECIFIC row's `count`
// delta rather than assuming the array grows by exactly N, so these tests
// hold regardless of run order.
test(
  'FIX BUG-691 F1: two sink-down commits in the SAME millisecond mint DISTINCT ids (no more collision), and identical-cause failures aggregate safely via count',
  withFakeLocalStorage(
    withFrozenClock(1_757_000_000_000, async (mem) => {
      const f = installFetch(async () => ({ ok: false, status: 503 }));
      const isDirectFailureRow = (e) => e.code === 'MET-V857' && !/drain retry failed/.test(e.msg);
      try {
        const countBefore = recentErrors()
          .filter(isDirectFailureRow)
          .reduce((n, e) => n + e.count, 0);
        const a = await commitDebug({ first: 1 });
        const b = await commitDebug({ second: 2 });

        // FIXED: a monotonic counter suffix makes the id collision-free even
        // when Date.now() is frozen to the exact same millisecond.
        assert.notEqual(a.id, b.id, 'FIX F1: ids no longer collide within the same millisecond');

        // The MET-V857 message no longer embeds the id (moved to `action`,
        // outside dedupeKey) — two genuinely distinct failures of the SAME
        // kind now aggregate under one row via `count`, which is the correct
        // recordError dedup contract for a repeated identical-cause failure.
        // This is safe now that F2 (below) proves the underlying queue
        // entries are never conflated or dropped because of it.
        const rows = recentErrors().filter(isDirectFailureRow);
        assert.equal(rows.length, 1, 'both failures collapse into the SAME direct-failure row');
        const countAfter = rows.reduce((n, e) => n + e.count, 0);
        assert.equal(countAfter, countBefore + 2, 'both failed attempts are accounted for via count, not lost');
        assert.doesNotMatch(
          rows[0].msg,
          new RegExp(a.id),
          'FIX F1: the commit id is no longer embedded in the dedupe-bearing message'
        );
        assert.match(rows[0].action, /^commit DBG-/, 'the id is captured in action instead, on whichever occurrence first created the row');
      } finally {
        f.restore();
      }
    })
  )
);

test(
  'FIX BUG-691 F2: even a forced/legacy id collision on the persisted queue can never double-delete — drain removal is index-based, at most one entry per confirmed POST',
  withFakeLocalStorage(async (mem) => {
    // F1's fix means commitDebug itself can no longer produce a colliding
    // id, so simulate the shape directly — e.g. a queue written by a
    // pre-fix build, or any future regression that reintroduces a collision.
    const seeded = [
      { id: 'DBG-COLLIDE', at: '2026-09-01T00:00:00.000Z', payload: { first: 'FIRST-PAYLOAD' } },
      { id: 'DBG-COLLIDE', at: '2026-09-01T00:00:00.001Z', payload: { second: 'SECOND-PAYLOAD' } },
    ];
    mem.set(QUEUE_KEY, JSON.stringify(seeded));

    // The live commit succeeds (POST #1), then the drain POSTs the two
    // colliding entries in order: the first succeeds (POST #2), the second
    // is refused (POST #3, 503).
    let n = 0;
    const posted = [];
    const f = installFetch(async (url, init) => {
      n += 1;
      posted.push(JSON.parse(init.body));
      return n <= 2 ? { ok: true, status: 200 } : { ok: false, status: 503 };
    });
    try {
      await commitDebug({ live: true });
    } finally {
      f.restore();
    }

    const lastPost = posted[posted.length - 1];
    assert.match(
      JSON.stringify(lastPost.payload),
      /SECOND-PAYLOAD/,
      'precondition: the second colliding entry was the POST that got refused (503)'
    );
    const remaining = mem.has(QUEUE_KEY) ? JSON.parse(mem.get(QUEUE_KEY)) : [];
    assert.equal(
      remaining.length,
      1,
      'FIX F2: exactly ONE entry remains — the refused one, never zero (double-delete) and never two (nothing removed)'
    );
    assert.equal(
      JSON.stringify(remaining).includes('SECOND-PAYLOAD'),
      true,
      'FIX F2: the surviving entry is the one that was actually refused by the sink, not the one that was sunk'
    );
  })
);

// ---------------------------------------------------------------------- F3 ---
test(
  'FIX BUG-691 F3: a sink that dies MID-DRAIN records its own error AND re-raises the indicator, even though the live commit itself succeeded',
  withFakeLocalStorage(async (mem) => {
    // Track the DRAIN-specific row by count, not overall array length —
    // this exact message is shared by every drain failure across this
    // whole test file (dedup), so the array does not necessarily grow.
    const isDrainFailureRow = (e) => e.code === 'MET-V857' && /drain retry failed/.test(e.msg);
    const drainCountBefore = recentErrors()
      .filter(isDrainFailureRow)
      .reduce((n, e) => n + e.count, 0);

    // Seed one queued entry with the sink down (distinct ms from the live commit).
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    try {
      await commitDebug({ stranded: true });
    } finally {
      down.restore();
    }
    assert.equal(debugSinkStatus().unreachable, true, 'precondition: indicator is up-down');
    assert.equal(JSON.parse(mem.get(QUEUE_KEY)).length, 1, 'precondition: one entry stranded on the queue');

    // Now: the NEXT live commit succeeds (sink briefly alive), but the sink
    // dies again for the drain of the stranded entry.
    let n = 0;
    const flaky = installFetch(async () => {
      n += 1;
      return n === 1 ? { ok: true, status: 200 } : { ok: false, status: 503 };
    });
    try {
      const r = await commitDebug({ live: true });
      assert.equal(r.ok, true, 'the LIVE commit itself still succeeded');
      assert.equal(r.queued, false);
    } finally {
      flaky.restore();
    }

    const drainRows = recentErrors().filter(isDrainFailureRow);
    assert.equal(drainRows.length, 1, 'exactly one drain-failure row exists (message is stable across calls)');
    const drainCountAfter = drainRows.reduce((n, e) => n + e.count, 0);
    assert.equal(
      drainCountAfter,
      drainCountBefore + 1,
      'FIX F3: drainQueue records its own MET-V857 row for the failed retry — never silent'
    );
    assert.equal(
      debugSinkStatus().unreachable,
      true,
      'FIX F3: the indicator is RE-STAMPED down because the drain itself failed, even though the live commit that triggered it succeeded'
    );
    assert.equal(
      JSON.parse(mem.get(QUEUE_KEY)).length,
      1,
      'the stranded entry is still queued, and now visibly so'
    );
  })
);

// ---------------------------------------------------------------------- F4 ---
test(
  'ATTACK BUG-691 F4: debugSinkStatus() is module state — it cannot survive a reload, so a returning player sees a green indicator over a full queue',
  withFakeLocalStorage(async (mem) => {
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    try {
      await commitDebug({ before: 'reload' });
    } finally {
      down.restore();
    }
    assert.equal(debugSinkStatus().unreachable, true);
    const persisted = mem.get(QUEUE_KEY);
    assert.ok(persisted && JSON.parse(persisted).length >= 1, 'the queue IS persisted across a reload');

    // A page reload = a fresh module instance. Re-import with a cache-busting
    // query so we observe genuine module-init state, not this process's.
    const fresh = await import('../src/sim/backend.ts?bug691reload=1');
    assert.equal(
      fresh.debugSinkStatus().unreachable,
      true,
      'FIX F4: module init seeds the indicator from pendingCommits() > 0 -- a non-empty leftover queue means the sink was NOT reached, even with no historical timestamp to recover'
    );
    assert.equal(
      typeof fresh.debugSinkStatus().sinceMs,
      'number',
      'FIX F4: since the ORIGINAL outage time cannot be recovered across a real reload, the fresh module stamps "noticed now" rather than lying null'
    );
  })
);

// ---------------------------------------------------------------------- F5 ---
test(
  'FIX BUG-691 F5: sinceMs is an ELAPSED duration (ms since the outage was noticed), not an absolute epoch timestamp',
  withFakeLocalStorage(async () => {
    const down = installFetch(async () => ({ ok: false, status: 503 }));
    try {
      await commitDebug({ x: 1 });
    } finally {
      down.restore();
    }
    const { sinceMs } = debugSinkStatus();
    assert.equal(typeof sinceMs, 'number');
    assert.ok(
      sinceMs >= 0 && sinceMs < 5000,
      `FIX F5: sinceMs must be a small ELAPSED value close to zero right after the outage was noticed, not an epoch (~1.7e12) -- got ${sinceMs}`
    );
    // A real elapsed duration only grows the longer the outage persists.
    await new Promise((r) => setTimeout(r, 5));
    assert.ok(
      debugSinkStatus().sinceMs >= sinceMs,
      'sinceMs increases with real wall-clock time -- proof it is computed live, not a frozen absolute stamp'
    );
  })
);

// ------------------------------------------------------- honest-contract ---
test(
  'ATTACK BUG-691: module-init state before ANY commit attempt is unreachable:false / sinceMs:null (no false alarm on a cold tab)',
  async () => {
    const fresh = await import('../src/sim/backend.ts?bug691cold=1');
    const s = fresh.debugSinkStatus();
    assert.equal(s.unreachable, false);
    assert.equal(s.sinceMs, null);
  }
);

test(
  'ATTACK BUG-691: an honest flap (down -> up -> down) re-arms the outage-start stamp -- elapsed sinceMs resets to ~0 for the NEW outage rather than keeping accumulating from the first one',
  withFakeLocalStorage(async () => {
    const ctl = { down: true };
    const f = installFetch(async () => (ctl.down ? { ok: false, status: 503 } : { ok: true, status: 200 }));
    const realNow = Date.now;
    let clock = 1_757_100_000_000;
    Date.now = () => clock;
    try {
      await commitDebug({ p: 1 });
      assert.equal(debugSinkStatus().unreachable, true);
      assert.equal(debugSinkStatus().sinceMs, 0, 'no elapsed time yet at the instant the outage was first noticed');

      clock += 10_000; // 10s pass with the SAME outage still ongoing
      assert.equal(debugSinkStatus().sinceMs, 10_000, 'elapsed grows while the same outage persists');

      ctl.down = false;
      await commitDebug({ p: 2 });
      assert.equal(debugSinkStatus().unreachable, false, 'recovery clears the indicator');
      assert.equal(debugSinkStatus().sinceMs, null, 'recovery clears the timestamp too — no stale value survives');

      ctl.down = true;
      clock += 5; // guarantee a distinct instant for the second outage
      await commitDebug({ p: 3 });
      assert.equal(debugSinkStatus().unreachable, true, 'the second outage re-raises the indicator');
      assert.equal(
        debugSinkStatus().sinceMs,
        0,
        'sinceMs is re-stamped for the NEW outage (elapsed resets to 0) -- an un-re-armed stamp would instead read ~10,005ms, still ticking from the first outage'
      );
    } finally {
      Date.now = realNow;
      f.restore();
    }
  })
);

test(
  'ATTACK BUG-691: a 2xx with a garbage/empty body is treated as a SUCCESS — the sink ack is never validated (inherited gap, pinned)',
  withFakeLocalStorage(async (mem) => {
    const f = installFetch(async () => ({ ok: true, status: 200, text: async () => 'not json at all' }));
    const before = recentErrors().length;
    try {
      const r = await commitDebug({ x: 1 });
      assert.equal(r.ok, true);
      assert.equal(r.queued, false, 'a 200 from ANY listener on 127.0.0.1:8642 counts as sunk');
    } finally {
      f.restore();
    }
    assert.equal(recentErrors().length, before, 'no error is recorded, and the indicator stays green');
    assert.equal(debugSinkStatus().unreachable, false);
    assert.equal(mem.has(QUEUE_KEY), false, 'nothing is kept locally — if that 200 was not the real sink, the frame is gone');
  })
);

/**
 * F6 (P2, FIXED — inherited from the BUG-607 estate but newly load-bearing):
 * an unserializable payload used to reach postToSink's own JSON.stringify
 * throw (swallowed as "unreachable" — blaming the NETWORK for what is
 * actually a payload defect), then enqueueCommit's un-guarded
 * `JSON.stringify(entry)` threw straight out of a function whose doc comment
 * promises "never throws". commitDebug now screens the payload up front,
 * before touching the network or the sink-health indicator at all.
 */
test(
  'FIX BUG-691 F6: an UNSERIALIZABLE payload is caught up front as a payload defect — commitDebug resolves (never rejects), the sink is never contacted, and a DISTINCT MET-V864 row is recorded',
  withFakeLocalStorage(async () => {
    const f = installFetch(async () => ({ ok: true, status: 200 }));
    const circular = { name: 'loop' };
    circular.self = circular;
    const before = recentErrors().length;
    try {
      const r = await commitDebug(circular);
      assert.equal(r.ok, false, 'a payload defect is a real failure — nothing was sent or queued');
      assert.equal(r.queued, false, 'FIX F6: not queued either — there is nothing serializable to queue');
      assert.equal(f.calls.length, 0, 'FIX F6: the sink is never even contacted for an unserializable payload');

      const rows = recentErrors();
      assert.equal(rows.length, before + 1, 'exactly one new row');
      assert.equal(rows[0].code, 'MET-V864', 'FIX F6: a DISTINCT payload-defect code, not MET-V857');
      assert.doesNotMatch(
        rows[0].msg,
        /Debug sink unreachable/,
        'FIX F6: a serialization defect in the payload is no longer reported as a network/sink outage'
      );
      assert.equal(
        debugSinkStatus().unreachable,
        false,
        'FIX F6: a perfectly healthy sink is NOT marked down by a payload defect'
      );
    } finally {
      f.restore();
    }
  })
);
