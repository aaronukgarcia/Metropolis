// bug617-tail-replay-scale.test.mjs — BUG-617 (P1, 2026-09-03), REAL-WORLD
// shape confirmed by the lead: Aaron's autosave silently failed for hours
// (an 8MB debugQueue blew the localStorage quota — a BUG-607-class silent
// failure). `persistSavepoint` kept rejecting every attempt, so
// `lastSaveIndex` (store.tsx) never advanced and the savepoint's persisted
// `journalTail` grew to thousands of actions (place/tick) needed to grow a
// STALE SMALL snapshot back up to the real ~13,000-building city.
//
// `restoreFromSavepoint`'s tail-replay loop (replay.ts) has NO chunking and
// runs SYNCHRONOUSLY inside store.tsx's `useState` boot initializer — this
// is a SECOND, independent BUG-617 mechanism from the genesis-replay chunk
// sizing fixed in genesisReplay.ts (see bug617-chunked-replay-scale.test.mjs).
//
// This suite proves the fix: `prepareRestoreForChunkedTail` (fast, sync,
// returns the PRE-tail state + the tail un-replayed) + `replayTailChunked`
// (a chunked, bounded, STRICT — non-defensive — replay of that tail).
//
// NOTE ON 'place' COST: unlike a 'tick', 'place' triggers autoConnect(), which
// (by design, for the single-placement path — see engine.ts's
// sweepOrphanConnects doc) rebuilds its occupied/road board Sets from ALL
// buildings on EVERY call — O(current building count) per placement, so a
// tail of N sequential placements growing a city is O(N^2) OVERALL. Chunking
// cannot fix that per-action cost (it only bounds how much work happens
// between yields) — it turns a FROZEN tab into a RESPONSIVE, progress-visible
// one, which is the fix this bug calls for. The O(n^2) autoConnect-during-
// bulk-replay cost itself is flagged as a separate follow-up finding, not
// fixed here (a fix would need a prebuiltBoard threaded through 'place'
// during replay, mirroring the historical BUG-467 sweepOrphanConnects fix,
// and deserves its own independently-attacked change).

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { initialState, reducer } from '../src/sim/engine.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import {
  createSavepoint,
  persistSavepoint,
  prepareRestoreForChunkedTail,
  replayTailChunked,
  LARGE_TAIL_REPLAY_THRESHOLD,
} from '../src/sim/replay.ts';

/** Minimal in-memory StorageLike, mirroring the other replay tests. */
function makeStorage() {
  const map = new Map();
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => {
      map.set(k, v);
    },
    removeItem: (k) => {
      map.delete(k);
    },
  };
}

/**
 * Build a tail of real 'place' actions (a road + a hut, side by side, so each
 * hut is immediately road-adjacent — autoConnect's fast `plan.connected` path
 * applies) interleaved with periodic 'tick's — the authentic shape of "a
 * small city grown by replaying a long tail of real play actions".
 */
function buildGrowthTail(pairs) {
  const actions = [];
  const cols = 100;
  for (let i = 0; i < pairs; i++) {
    const col = i % cols;
    const row = Math.floor(i / cols);
    const x = 2 + col * 3;
    const y = 2 + row * 3;
    actions.push({ type: 'place', spec: 'road', x, y: y + 1 });
    actions.push({ type: 'place', spec: 'res_hut', x, y });
    if (i % 20 === 0) actions.push({ type: 'tick' });
  }
  return actions;
}

/** A small starter state with room to grow (unlocked + well-funded), exactly
 * the "stale small snapshot" shape from the real-world repro. */
function smallStartState() {
  return { ...initialState(), unlockedAll: true, funds: 5_000_000_000 };
}

describe('BUG-617: chunked savepoint-tail replay (large-tail boot path)', () => {
  test('prepareRestoreForChunkedTail returns the pre-tail state fast, tail un-replayed', () => {
    const storage = makeStorage();
    const start = smallStartState();
    const tailActions = buildGrowthTail(60); // 120 place + a few ticks
    let journal = emptyJournal();
    for (const a of tailActions) journal = recordAction(journal, start.tick, a);

    const savepoint = createSavepoint(start, journal.entries, new Date(), 'v0.0.0.1', null);
    persistSavepoint(storage, savepoint);

    const t0 = performance.now();
    const prepared = prepareRestoreForChunkedTail(storage);
    const prepMs = performance.now() - t0;

    assert.equal(prepared.success, true, prepared.reason);
    assert.equal(prepared.state.buildings.length, start.buildings.length, 'pre-tail state must NOT have replayed the tail');
    assert.equal(prepared.tail.length, journal.entries.length);
    assert.ok(prepMs < 50, `prepare must stay fast (no tail loop): ${prepMs.toFixed(1)}ms`);
  });

  test('replayTailChunked is byte-identical to the plain unchunked reducer loop', () => {
    const start = smallStartState();
    const tailActions = buildGrowthTail(80);

    // Baseline: exactly what restoreFromSavepoint's existing loop does.
    let unchunked = start;
    for (const a of tailActions) unchunked = reducer(unchunked, a);

    // Chunked: drive the generator to completion.
    const tail = tailActions.map((a) => ({ tick: start.tick, action: a }));
    const gen = replayTailChunked(start, tail);
    let next;
    do {
      next = gen.next();
    } while (!next.done);
    const chunked = next.value.state;

    assert.deepEqual(chunked, unchunked, 'chunked tail replay must be byte-identical to the unchunked loop');
    assert.equal(next.value.replayed, tailActions.length);
  });

  test('a large growth tail replays in BOUNDED chunks (responsive, not frozen)', () => {
    // A tail well past LARGE_TAIL_REPLAY_THRESHOLD, growing the city by
    // several hundred buildings — big enough to show real per-action cost
    // growth, small enough to keep this suite's runtime sane.
    const pairs = 300; // 600 place actions + periodic ticks
    const tailActions = buildGrowthTail(pairs);
    assert.ok(tailActions.length > LARGE_TAIL_REPLAY_THRESHOLD, 'tail must exceed the large-tail threshold to be representative');

    const start = smallStartState();
    const tail = tailActions.map((a) => ({ tick: start.tick, action: a }));

    const gen = replayTailChunked(start, tail);
    const chunkDurationsMs = [];
    let next;
    do {
      const t0 = performance.now();
      next = gen.next();
      chunkDurationsMs.push(performance.now() - t0);
    } while (!next.done);

    assert.ok(chunkDurationsMs.length > 1, `expected multiple chunk yields, got ${chunkDurationsMs.length}`);
    assert.equal(next.value.state.buildings.length, start.buildings.length + pairs * 2);
    // MEDIAN, not max (P1 timing-gate fix, independent round r2, 2026-09-03):
    // a MAX-based assertion is exactly the wrong statistic under CI's shared,
    // contention-heavy hardware — the attacker measured 2-of-3 red at 20
    // cores from ordinary parallel-test GC/scheduler jitter alone, on a
    // 2-core CI runner that is strictly worse. The house rule (this file's
    // sibling scale-gate.test.mjs already documents it) is the robust MEDIAN
    // — a single slow outlier chunk (a GC pause, a scheduler preemption)
    // must never redden the gate, but a SYSTEMIC regression (every chunk
    // slower) still trivially fails a median bound. Sabotage sensitivity is
    // preserved: an unbounded single-chunk sabotage that processes the WHOLE
    // tail in one `gen.next()` call collapses this array to (effectively) one
    // giant sample, and the median of a mostly-one-sample-dominates array is
    // still that giant value — the attacker's own 15,898ms unbounded-chunk
    // sabotage is caught by this same 500ms bound with enormous margin (~32x
    // over), and a bound this generous (see bug617-chunked-replay-scale.test.mjs's
    // MAX_CHUNK_MS note on GC jitter) still proves BOUNDED and RESPONSIVE,
    // not frozen for 20+ minutes with zero yields — the pre-fix behaviour.
    const sorted = [...chunkDurationsMs].sort((a, b) => a - b);
    const medianChunkMs = sorted[Math.floor(sorted.length / 2)];
    assert.ok(
      medianChunkMs < 500,
      `median chunk time ${medianChunkMs.toFixed(1)}ms (of ${chunkDurationsMs.length} chunks) must stay bounded even as the city grows mid-replay`
    );
  });

  test('a throwing action propagates out of the generator (strict, non-defensive — matches restoreFromSavepoint)', () => {
    // The real reducer is fail-closed-as-no-op for bad input (never throws —
    // grep confirms zero `throw` statements in engine.ts), so this test
    // crafts a malicious action object whose `type` getter itself throws —
    // proving the CONTRACT (an exception from inside the reduce call
    // propagates out of the generator uncaught, exactly like
    // restoreFromSavepoint's plain `for` loop) without depending on a real
    // engine code path that happens to throw today.
    const start = smallStartState();
    const poisoned = {};
    Object.defineProperty(poisoned, 'type', {
      get() {
        throw new Error('BUG-617 test: poisoned action');
      },
    });
    const badTail = [{ tick: 0, action: poisoned }];
    const gen = replayTailChunked(start, badTail);
    assert.throws(() => {
      let next;
      do {
        next = gen.next();
      } while (!next.done);
    }, /poisoned action/);
  });
});
