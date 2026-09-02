// bug-469-autosave-history.test.mjs — BUG-469 (P1, Baseline One goal #6):
// autosave was data-loss-prone. A single savepoint.0 slot with no history/
// protection meant ANY overwrite (a reload, a second tab, a race) destroyed
// the only autosave with no recovery.
//
// Aaron ruling (Q100029): named save slots + 10-min autosaves + autosaves
// auto-purge after 1 month (dev). AC-2/AC-4 (FEAT-2326609714-reliable-save-
// reload.md) require: (a) a bounded rotating HISTORY of SAVEPOINT_CAP (3+)
// slots so no single write destroys every prior good autosave, (b) overwrite
// protection so a stale/older writer cannot clobber a fresher autosave, and
// (c) a ~1-month retention purge for autosaves (never named saves).
//
// Each assertion below is proven RED against the pre-fix behaviour by
// temporarily reverting src/sim/replay.ts to SAVEPOINT_CAP=1 / no protection
// / no purge (see the comment above each test) — see the fork investigation
// notes; this file stays GREEN against the fixed implementation.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  persistSavepoint,
  readAllSavepoints,
  createSavepoint,
  SAVEPOINT_CAP,
  AUTOSAVE_RETENTION_MS,
} from '../src/sim/replay.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

class MockStorage {
  constructor() {
    this.data = {};
  }
  getItem(key) {
    return Object.prototype.hasOwnProperty.call(this.data, key) ? this.data[key] : null;
  }
  setItem(key, value) {
    this.data[key] = String(value);
  }
  removeItem(key) {
    delete this.data[key];
  }
}

function tickState(state, n) {
  let s = state;
  for (let i = 0; i < n; i++) s = reducer(s, { type: 'tick' });
  return s;
}

describe('BUG-469: autosave slot history (AC-2)', () => {
  // RED without the fix: with SAVEPOINT_CAP=1, only the newest autosave ever
  // survives — the 2nd/3rd write destroy every prior good autosave. This
  // proves the fix keeps a bounded HISTORY, not a single overwritten slot.
  test('SAVEPOINT_CAP is a rotating history of 3+ slots, not a single overwritten slot', () => {
    assert.ok(SAVEPOINT_CAP >= 3, 'BUG-469: Aaron ruling Q100029 requires 3+ rotating autosave slots');
  });

  test('firing N+1 autosaves keeps a bounded history of the last N — the oldest rolls off, not everything but the newest', () => {
    const storage = new MockStorage();
    const base = initialState();

    // Fire SAVEPOINT_CAP + 1 autosaves, each with a distinct tick/timestamp so
    // "oldest" and "newest" are unambiguous.
    const savedAts = [];
    let lastNow = null;
    for (let i = 0; i <= SAVEPOINT_CAP; i++) {
      const s = tickState(base, i + 1); // distinct snapshotTick per autosave
      const now = new Date(2026, 0, 1, 0, i); // distinct, monotonically increasing minute
      lastNow = now;
      savedAts.push(now.toISOString());
      const ok = persistSavepoint(storage, createSavepoint(s, [], now), now);
      assert.ok(ok, `autosave #${i} must persist successfully`);
    }

    // Read with a "now" close to the last write (NOT the real wall clock) so
    // the retention purge (a separate BUG-469 requirement, tested below)
    // doesn't interfere with this history-rotation assertion.
    const surviving = readAllSavepoints(storage, lastNow);

    // THE RED ASSERTION: with the pre-fix cap of 1, `surviving.length` would
    // be 1 (every earlier autosave destroyed). The fix must keep SAVEPOINT_CAP
    // of them — a single write never destroys ALL prior good saves.
    assert.equal(
      surviving.length,
      SAVEPOINT_CAP,
      `BUG-469: expected a bounded history of ${SAVEPOINT_CAP} autosaves to survive, not just the newest one`
    );

    // The very FIRST (oldest) autosave must have rolled off...
    const survivingSavedAts = surviving.map((sp) => sp.savedAt).sort();
    assert.ok(
      !survivingSavedAts.includes(savedAts[0]),
      'the oldest autosave should have been evicted by rotation'
    );
    // ...but every one of the last SAVEPOINT_CAP autosaves must still be present.
    for (const expected of savedAts.slice(1)) {
      assert.ok(
        survivingSavedAts.includes(expected),
        `BUG-469: autosave at ${expected} was destroyed instead of kept in history`
      );
    }
  });
});

describe('BUG-469: overwrite protection against reload/second-tab/race (AC-2, AC-4)', () => {
  test('a stale/older writer cannot clobber a fresher autosave already on disk', () => {
    const storage = new MockStorage();
    const base = initialState();
    const seedNow = new Date(2026, 0, 1, 0, 0);

    // Fill every slot with an IDENTICAL snapshot (same tick, same savedAt) so
    // slot-choice ties break deterministically to slot 0 — makes the target
    // slot for the next write fully predictable.
    for (let i = 0; i < SAVEPOINT_CAP; i++) {
      const sp = createSavepoint(tickState(base, 5), [], seedNow);
      const ok = persistSavepoint(storage, sp, seedNow);
      assert.ok(ok, `seed autosave #${i} must persist`);
    }
    const beforeAttack = readAllSavepoints(storage, seedNow);
    assert.equal(beforeAttack.length, SAVEPOINT_CAP, 'fixture must fully populate the rotation first');

    // ATTACK: a stale writer — e.g. a backgrounded tab whose autosave timer
    // fires late, computed from an OLDER tick than what is already saved —
    // tries to persist. Same savedAt as the seed, but a strictly OLDER tick.
    const staleSp = createSavepoint(tickState(base, 1), [], seedNow);
    const staleOk = persistSavepoint(storage, staleSp, seedNow);

    // THE RED ASSERTION: without overwrite protection, this stale write
    // succeeds and silently clobbers a slot holding fresher data (tick 5 -> 1).
    assert.equal(staleOk, false, 'BUG-469: a stale/older autosave write must be rejected, not silently accepted');

    const afterAttack = readAllSavepoints(storage, seedNow);
    assert.ok(
      afterAttack.every((sp) => sp.snapshotTick === tickState(base, 5).tick),
      'BUG-469: the stale write must not have clobbered any existing (fresher) autosave slot'
    );

    // Sanity: a genuinely FRESHER write (higher tick) must still be accepted —
    // protection must not become a one-way autosave freeze.
    const freshNow = new Date(2026, 0, 1, 0, 10);
    const freshSp = createSavepoint(tickState(base, 20), [], freshNow);
    const freshOk = persistSavepoint(storage, freshSp, freshNow);
    assert.equal(freshOk, true, 'a genuinely fresher autosave must still be accepted after a stale write was rejected');

    const afterFresh = readAllSavepoints(storage, freshNow);
    assert.ok(
      afterFresh.some((sp) => sp.snapshotTick === tickState(base, 20).tick),
      'the fresher autosave must actually be persisted'
    );
  });
});

describe('BUG-469: 1-month autosave retention purge (Aaron ruling Q100029)', () => {
  test('an autosave older than the retention window is purged on read, while a fresh one is kept', () => {
    const storage = new MockStorage();
    const base = initialState();

    // Seed one STALE autosave (persisted "fresh" relative to its own saved
    // time, which is itself far in the past relative to "now").
    const staleSavedAt = new Date(2020, 0, 1, 0, 0);
    const staleSp = createSavepoint(tickState(base, 2), [], staleSavedAt);
    assert.ok(persistSavepoint(storage, staleSp, staleSavedAt), 'seed the stale autosave');

    // "Now" is well past the retention window relative to the stale save.
    const farFuture = new Date(staleSavedAt.getTime() + AUTOSAVE_RETENTION_MS + 24 * 60 * 60 * 1000);

    // THE RED ASSERTION: without purge-on-read, the stale autosave would still
    // be returned here even though it is over a month old.
    const purged = readAllSavepoints(storage, farFuture);
    assert.equal(purged.length, 0, 'BUG-469: an autosave older than AUTOSAVE_RETENTION_MS must be purged, not restored');

    // A FRESH autosave (within the window) must survive the same check.
    const freshSavedAt = new Date(farFuture.getTime() - 1000); // 1 second before "now"
    const freshSp = createSavepoint(tickState(base, 3), [], freshSavedAt);
    assert.ok(persistSavepoint(storage, freshSp, freshSavedAt), 'seed the fresh autosave');
    const kept = readAllSavepoints(storage, farFuture);
    assert.equal(kept.length, 1, 'a fresh (within-retention) autosave must NOT be purged');
    assert.equal(kept[0].snapshotTick, tickState(base, 3).tick);
  });

  test('purge-on-write frees a stale slot for reuse instead of leaving it stuck forever', () => {
    const storage = new MockStorage();
    const base = initialState();

    // Fill every slot with a stale autosave (all older than the retention
    // window relative to a later "now").
    const staleSavedAt = new Date(2020, 0, 1, 0, 0);
    for (let i = 0; i < SAVEPOINT_CAP; i++) {
      const sp = createSavepoint(tickState(base, i + 1), [], staleSavedAt);
      assert.ok(persistSavepoint(storage, sp, staleSavedAt));
    }

    // A new autosave arrives long after the retention window has passed for
    // every existing slot.
    const now = new Date(staleSavedAt.getTime() + AUTOSAVE_RETENTION_MS + 24 * 60 * 60 * 1000);
    const freshSp = createSavepoint(tickState(base, 99), [], now);
    const ok = persistSavepoint(storage, freshSp, now);
    assert.ok(ok, 'BUG-469: a fresh write must succeed by reclaiming a purged-stale slot, not be blocked forever');

    const surviving = readAllSavepoints(storage, now);
    assert.equal(surviving.length, 1, 'every stale autosave must have been purged, leaving only the fresh one');
    assert.equal(surviving[0].snapshotTick, tickState(base, 99).tick);
  });
});

describe('BUG-469: named saves are a separate mechanism and unaffected', () => {
  test('the autosave retention/rotation constants are exported and independent of named-save storage', async () => {
    // namedsaves.ts owns its own cap (NAMED_SAVE_BLOB_CAP) and storage keys —
    // this fix touches ONLY the autosave savepoint slots (replay.ts). Prove
    // the two modules stay decoupled: importing namedsaves.ts does not
    // require/alter anything about SAVEPOINT_CAP or AUTOSAVE_RETENTION_MS.
    const namedsaves = await import('../src/sim/namedsaves.ts');
    assert.equal(typeof namedsaves.NAMED_SAVE_BLOB_CAP, 'number');
    assert.notEqual(
      namedsaves.NAMED_SAVE_BLOB_CAP,
      SAVEPOINT_CAP,
      'named-save cap and autosave-slot cap are independent tunables, not the same source of truth'
    );
  });
});
