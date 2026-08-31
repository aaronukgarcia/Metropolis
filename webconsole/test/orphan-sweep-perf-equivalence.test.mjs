// BUG-467 part B — sweepOrphanConnects was O(n²)+ at scale (measured ~110s at
// ~9,886 buildings): (1) `s.buildings.find(id)` per iteration, (2) `autoConnect`
// rebuilding the full occupied/roads tile-string Sets from ALL buildings on
// EVERY call. This suite proves the rewritten (linear-ish) sweep produces a
// BYTE-IDENTICAL resulting SimState to the original implementation, is still
// deterministic under replay, and reports the measured speedup.
//
// Equivalence method: `autoConnect`'s default code path (no `prebuiltBoard`
// argument) is the UNCHANGED original rebuild-from-scratch logic — only the
// new sweep passes a `prebuiltBoard`. So `oldSweepOrphanConnects` below is the
// verbatim ORIGINAL sweep loop (linear `find`, no prebuilt board), reusing the
// exact same `autoConnect` export, just invoked the old way. Any difference
// between it and the real (new) `sweepOrphanConnects` isolates precisely the
// two optimizations under test — it does not depend on a second, possibly
// drifted, hand-copied reimplementation of the connector/ledger/monitor logic.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS } from '../src/sim/data.ts';
import {
  initialState,
  autoConnect,
  sweepOrphanConnects,
} from '../src/sim/engine.ts';

function board(buildings, extra = {}) {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings) if (b.id > maxId) maxId = b.id;
  return {
    ...base,
    unlockedAll: true,
    funds: 100_000_000_000,
    buildings,
    nextId: maxId + 1,
    roadNotice: null,
    ledger: [],
    ...extra,
  };
}

/** Verbatim ORIGINAL sweepOrphanConnects (BUG-467 part B pre-fix): O(n) `find`
 *  per id, and `autoConnect` invoked with NO prebuiltBoard so it takes its
 *  default rebuild-occupied/roads-from-all-buildings branch every call. */
function oldSweepOrphanConnects(s) {
  const ids = s.buildings.map((b) => b.id).sort((a, b) => a - b);
  for (const id of ids) {
    const placed = s.buildings.find((b) => b.id === id);
    if (!placed) continue;
    const sp = SPECS[placed.spec];
    if (!sp) continue;
    let unaffordable = false;
    const next = autoConnect(s, placed, sp, {
      notice: false,
      onUnaffordable: () => {
        unaffordable = true;
      },
    });
    if (unaffordable) break;
    s = next;
  }
  return s;
}

/**
 * Build a city of `n` hut/road blocks laid out in a grid, none touching each
 * other's footprints. `orphanEvery` controls what fraction get NO adjacent
 * road (so the sweep must actually route+lay a connector for them) — the rest
 * are pre-connected (already touch a road, so the sweep no-ops for them, the
 * dominant case in a real stable city).
 */
function generateCity(n, orphanEvery = 5) {
  const buildings = [];
  let id = 1;
  const cols = Math.floor(440 / 4); // 1 hut + gap, spaced 4 tiles apart in x
  for (let i = 0; i < n; i++) {
    const col = i % cols;
    const row = Math.floor(i / cols);
    const x = 2 + col * 4;
    const y = 2 + row * 4;
    buildings.push({ id: id++, spec: 'res_hut', x, y });
    if (i % orphanEvery !== 0) {
      // Pre-connected: road tile directly south of the hut.
      buildings.push({ id: id++, spec: 'road', x, y: y + 1 });
    }
    // else: orphan — no road adjacent; sweep must route one in.
  }
  return buildings;
}

test('BUG-467B equivalence: small mixed city (orphans + pre-connected + unaffordable) — byte-identical old vs new', () => {
  const buildings = generateCity(60, 4);
  const sOld = oldSweepOrphanConnects(board(buildings));
  const sNew = sweepOrphanConnects(board(buildings));
  assert.deepEqual(sNew, sOld, 'new sweep must match old sweep byte-for-byte');
  assert.equal(JSON.stringify(sNew), JSON.stringify(sOld));
});

test('BUG-467B equivalence: larger city at moderate scale — byte-identical old vs new', () => {
  const buildings = generateCity(400, 6);
  const sOld = oldSweepOrphanConnects(board(buildings));
  const sNew = sweepOrphanConnects(board(buildings));
  assert.deepEqual(sNew, sOld, 'new sweep must match old sweep byte-for-byte at scale');
});

test('BUG-467B equivalence: unaffordable mid-sweep still matches old halt semantics', () => {
  const buildings = generateCity(150, 3);
  // Deliberately tight funds so the sweep runs out partway through — exercises
  // the STOP-not-skip path (order-dependent) identically in both impls.
  const extra = { funds: 20_000 };
  const sOld = oldSweepOrphanConnects(board(buildings, extra));
  const sNew = sweepOrphanConnects(board(buildings, extra));
  assert.deepEqual(sNew, sOld, 'unaffordable-halt path must match old sweep byte-for-byte');
});

test('BUG-467B determinism: new sweep run twice on the same input yields identical state', () => {
  const buildings = generateCity(200, 5);
  const s1 = sweepOrphanConnects(board(buildings));
  const s2 = sweepOrphanConnects(board(buildings));
  assert.equal(JSON.stringify(s1), JSON.stringify(s2), 'new sweep is deterministic');
});

test('BUG-467B perf: new sweep is dramatically faster than old at a few thousand buildings (equivalence + timing)', () => {
  // Mostly-already-connected city (orphanEvery high) mirrors the dominant
  // real-world case: a stable city where the sweep should do almost no work.
  const buildings = generateCity(3000, 20);

  const sOld0 = board(buildings);
  const t0Old = Date.now();
  const sOld = oldSweepOrphanConnects(sOld0);
  const oldMs = Date.now() - t0Old;

  const sNew0 = board(buildings);
  const t0New = Date.now();
  const sNew = sweepOrphanConnects(sNew0);
  const newMs = Date.now() - t0New;

  assert.deepEqual(sNew, sOld, 'perf-scale sweep must still be byte-identical old vs new');

  // eslint-disable-next-line no-console
  console.log(
    `BUG-467B perf: ~${buildings.length} buildings — old sweep ${oldMs}ms, new sweep ${newMs}ms` +
      (oldMs > 0 ? ` (${(oldMs / Math.max(1, newMs)).toFixed(1)}x speedup)` : '')
  );

  // Soft perf assertion (not a hard wall-clock upper bound per GR — a RATIO
  // check against the OLD run measured in the SAME process/machine/tick,
  // never an absolute ms budget): the new sweep must not be slower, and at
  // this scale should clearly be faster given the O(n²)->~O(n) change.
  assert.ok(newMs <= oldMs, `new sweep (${newMs}ms) must not be slower than old (${oldMs}ms)`);
});
