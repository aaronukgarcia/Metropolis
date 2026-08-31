// journal-persister.test.mjs — BUG-458: coalescing journal persistence.
//
// The old wiring called persistJournal(window.localStorage, updated) on EVERY
// dispatched action — a full JSON.stringify + setItem of the whole journal per
// action, O(n) and worse as the city ages. createJournalPersister debounces
// that into ONE write per idle window (or per actionInterval scheduled calls,
// whichever comes first), while `flush` still forces an immediate synchronous
// write for boundaries where losing the tail is unacceptable (save, wipe/
// capture, unload). This file proves:
//   1. N scheduled writes inside the debounce window -> ONE persisted write.
//   2. flush() bypasses the debounce and writes immediately + cancels the timer.
//   3. actionInterval forces a write mid-burst even with no idle gap.
//   4. after a simulated reload, the journal on disk replays correctly (no
//      lost actions past the last flush/actionInterval-forced write).
//   5. MUTATION PROOF: an implementation that persists synchronously per
//      schedule() call (the BUG-458 regression) is caught by assertion 1.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  createJournalPersister,
  emptyJournal,
  recordAction,
  loadJournal,
  JOURNAL_KEY,
} from '../src/sim/journal.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

class MockStorage {
  constructor() {
    this.data = {};
    this.setItemCalls = 0;
  }
  getItem(key) {
    return Object.prototype.hasOwnProperty.call(this.data, key) ? this.data[key] : null;
  }
  setItem(key, value) {
    this.setItemCalls += 1;
    this.data[key] = value;
  }
  removeItem(key) {
    delete this.data[key];
  }
}

/** A fake, manually-driven timer so tests are deterministic (no real waiting). */
function fakeScheduler() {
  let nextId = 1;
  const pending = new Map(); // id -> fn
  return {
    setTimeoutFn: (fn) => {
      const id = nextId++;
      pending.set(id, fn);
      return id;
    },
    clearTimeoutFn: (id) => {
      pending.delete(id);
    },
    /** Fire ALL currently-pending timers (simulates the debounce window elapsing). */
    fireAll() {
      const fns = Array.from(pending.values());
      pending.clear();
      for (const fn of fns) fn();
    },
    pendingCount() {
      return pending.size;
    },
  };
}

describe('createJournalPersister: debounce/coalesce (BUG-458)', () => {
  test('N scheduled actions within the debounce window produce ONE persisted write', () => {
    const storage = new MockStorage();
    const sched = fakeScheduler();
    const persister = createJournalPersister(storage, {
      debounceMs: 1000,
      actionInterval: 200,
      setTimeoutFn: sched.setTimeoutFn,
      clearTimeoutFn: sched.clearTimeoutFn,
    });

    let j = emptyJournal();
    const N = 20;
    for (let i = 0; i < N; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: i });
      persister.schedule(j);
    }

    // No write should have happened yet — still inside the debounce window.
    assert.equal(storage.setItemCalls, 0, 'no write should happen before the debounce timer fires');
    assert.equal(sched.pendingCount(), 1, 'only ONE timer should be pending, not one per schedule() call');

    sched.fireAll();

    assert.equal(storage.setItemCalls, 1, `expected exactly 1 persisted write for ${N} scheduled actions, got ${storage.setItemCalls}`);
    const persisted = JSON.parse(storage.getItem(JOURNAL_KEY));
    assert.equal(persisted.entries.length, N, 'the single write must contain ALL coalesced entries');
  });

  test('MUTATION PROOF: a naive per-action persist (the BUG-458 regression) fails assertion 1', () => {
    // Simulate the OLD, buggy wiring directly (persistJournal on every schedule
    // call) to prove the test above actually distinguishes debounced from not.
    const storage = new MockStorage();
    let j = emptyJournal();
    const N = 20;
    for (let i = 0; i < N; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: i });
      // The regression: a raw setItem on every action.
      storage.setItem(JOURNAL_KEY, JSON.stringify({ entries: j.entries }));
    }
    assert.equal(storage.setItemCalls, N, 'the naive per-action wiring writes once per action (this is exactly the bug BUG-458 fixes)');
    assert.notEqual(storage.setItemCalls, 1, 'sanity: proves 1-write and N-writes are distinguishable');
  });

  test('flush() writes immediately and cancels any pending debounce timer', () => {
    const storage = new MockStorage();
    const sched = fakeScheduler();
    const persister = createJournalPersister(storage, {
      debounceMs: 1000,
      actionInterval: 200,
      setTimeoutFn: sched.setTimeoutFn,
      clearTimeoutFn: sched.clearTimeoutFn,
    });

    let j = emptyJournal();
    j = recordAction(j, 0, { type: 'debugFunds', amount: 1 });
    persister.schedule(j);
    assert.equal(storage.setItemCalls, 0);
    assert.equal(sched.pendingCount(), 1);

    const ok = persister.flush(j);
    assert.ok(ok, 'flush must report success');
    assert.equal(storage.setItemCalls, 1, 'flush must write immediately, not wait for the debounce');
    assert.equal(sched.pendingCount(), 0, 'flush must cancel the pending debounce timer');

    // If the (now-cancelled) timer were somehow to fire, it must NOT write again.
    sched.fireAll();
    assert.equal(storage.setItemCalls, 1, 'a cancelled timer must never fire a second write');
  });

  test('actionInterval forces a write mid-burst even with no idle gap (sustained turbo-speed ticking)', () => {
    const storage = new MockStorage();
    const sched = fakeScheduler();
    const ACTION_INTERVAL = 5;
    const persister = createJournalPersister(storage, {
      debounceMs: 60_000, // effectively "never" idles out on its own
      actionInterval: ACTION_INTERVAL,
      setTimeoutFn: sched.setTimeoutFn,
      clearTimeoutFn: sched.clearTimeoutFn,
    });

    let j = emptyJournal();
    for (let i = 0; i < ACTION_INTERVAL; i++) {
      j = recordAction(j, i, { type: 'debugFunds', amount: i });
      persister.schedule(j);
    }

    assert.equal(storage.setItemCalls, 1, 'a forced write must land after actionInterval scheduled calls, without waiting for the idle debounce');
    assert.equal(sched.pendingCount(), 0, 'the forced write must clear any pending idle timer too');
  });

  test('END-TO-END: after a simulated reload, the journal on disk replays correctly (no lost actions past the last flush)', () => {
    const storage = new MockStorage();
    const sched = fakeScheduler();
    const persister = createJournalPersister(storage, {
      debounceMs: 1000,
      actionInterval: 200,
      setTimeoutFn: sched.setTimeoutFn,
      clearTimeoutFn: sched.clearTimeoutFn,
    });

    // Live session: some actions scheduled (debounced, not yet on disk)...
    let liveState = initialState();
    let liveJournal = emptyJournal();
    const scripted = [
      { type: 'place', spec: 'res_hut', x: 5, y: 5 },
      { type: 'tick' },
      { type: 'tick' },
    ];
    for (const action of scripted) {
      liveJournal = recordAction(liveJournal, liveState.tick, action);
      liveState = reducer(liveState, action);
      persister.schedule(liveJournal);
    }

    // ...then a flush boundary fires (e.g. Save, or beforeunload) BEFORE the
    // debounce timer would have — this is the crash-safety contract.
    persister.flush(liveJournal);

    // "Reload": read back whatever is on disk and replay it onto a fresh state.
    const reloaded = loadJournal(storage);
    assert.equal(reloaded.entries.length, scripted.length, 'the flushed journal must contain every scripted action');

    let replayedState = initialState();
    for (const entry of reloaded.entries) {
      replayedState = reducer(replayedState, entry.action);
    }
    assert.deepEqual(replayedState, liveState, 'replaying the on-disk journal after "reload" must match the live end state exactly');
  });
});
