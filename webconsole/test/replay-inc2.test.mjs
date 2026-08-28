// replay-inc2.test.mjs — FEAT-1972079897 inc2: cross-build rebuild path.
//
// Design brief: docs/planning/hard-reset-replay-brief.md (§4.3-4.5).
//
// Proves the PURE inc2 helpers, which is where all the decision/report/defensive
// logic lives (the store + modal wiring is presentational glue over these):
//   1. needsRebuild — same version → false, different → true, missing → false.
//   2. replayFromGenesisDefensive — an action invalid under the (simulated) new
//      rules is skipped-and-logged, replay does NOT crash, and the rest applies.
//   3. rebuildReport — before/after metrics + skipped list; NEVER asserts identity
//      (a rules-change build yielding a different city is expected, not a bug).
//   4. camera round-trips through the sync ref and the reload-surviving storage.
//
// RED proof (performed out-of-band, cp/mv NEVER git): break needsRebuild to always
// return false → the "different version" case goes RED; restore. See the report.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import {
  needsRebuild,
  replayFromGenesisDefensive,
  rebuildReport,
  metricsOf,
} from '../src/sim/genesisReplay.ts';
import {
  stashCamera,
  consumeStashedCamera,
  persistStashedCamera,
  consumePersistedCamera,
  __resetCameraStash,
} from '../src/sim/cameraStash.ts';

/** Minimal injectable in-memory storage mirroring the Web Storage subset. */
function memStorage() {
  const m = new Map();
  return {
    getItem: (k) => (m.has(k) ? m.get(k) : null),
    setItem: (k, v) => void m.set(k, String(v)),
    removeItem: (k) => void m.delete(k),
  };
}

describe('needsRebuild: version-diff decision', () => {
  test('same version → false (no rebuild offered)', () => {
    assert.equal(needsRebuild('v0.3.0.71', 'v0.3.0.71'), false);
  });

  test('different version → true (offer a rebuild)', () => {
    assert.equal(needsRebuild('v0.3.0.71', 'v0.3.0.99'), true);
  });

  test('missing either side → false (cannot prove a rules change; do not nag)', () => {
    assert.equal(needsRebuild(null, 'v0.3.0.99'), false);
    assert.equal(needsRebuild(undefined, 'v0.3.0.99'), false);
    assert.equal(needsRebuild('v0.3.0.71', null), false);
    assert.equal(needsRebuild('', 'v0.3.0.99'), false);
  });
});

describe('replayFromGenesisDefensive: skip-and-log invalid actions', () => {
  // A journal where the middle action is INVALID under the (simulated) new rules.
  // We inject an engine whose reduce() throws on a marked action, standing in for
  // "the new engine rejects this" without needing a second real build.
  const throwingEngine = {
    init: initialState,
    reduce: (state, action) => {
      if (action && action.__invalidUnderNewRules) {
        throw new Error('spec removed under new rules');
      }
      return reducer(state, action);
    },
  };

  const journal = {
    entries: [
      { tick: 1, action: { type: 'place', spec: 'res_hut', x: 5, y: 5 } },
      { tick: 1, action: { type: 'place', spec: 'ghost_spec', x: 9, y: 9, __invalidUnderNewRules: true } },
      { tick: 1, action: { type: 'tick' } },
      { tick: 2, action: { type: 'tick' } },
    ],
  };

  test('a throwing action is skipped-and-logged, replay does not crash', () => {
    let result;
    assert.doesNotThrow(() => {
      result = replayFromGenesisDefensive(journal, throwingEngine);
    });
    assert.equal(result.crashed, false, 'a single bad action must not crash the rebuild');
    assert.equal(result.skipped.length, 1, 'exactly the one invalid action is skipped');
    assert.equal(result.skipped[0].index, 1, 'skipped record points at the offending entry');
    assert.equal(result.skipped[0].type, 'place');
    assert.equal(result.skipped[0].tick, 1);
    assert.match(result.skipped[0].error, /removed under new rules/);
  });

  test('the surviving actions still apply (tick advanced past the skip)', () => {
    const result = replayFromGenesisDefensive(journal, throwingEngine);
    // 2 tick actions were applied on top of genesis (which sits at tick 1).
    assert.equal(result.state.tick, initialState().tick + 2, 'valid ticks after the skip still ran');
    // The valid res_hut placement is present; the ghost_spec one is not.
    assert.ok(
      result.state.buildings.some((b) => b.spec === 'res_hut' && b.x === 5 && b.y === 5),
      'the valid placement survived'
    );
    assert.ok(
      !result.state.buildings.some((b) => b.spec === 'ghost_spec'),
      'the invalid placement was skipped, not applied'
    );
  });

  test('a crash constructing genesis is caught and flagged (crashed=true)', () => {
    const brokenInit = {
      init: () => {
        throw new Error('genesis unavailable');
      },
      reduce: reducer,
    };
    let result;
    assert.doesNotThrow(() => {
      result = replayFromGenesisDefensive({ entries: [] }, brokenInit);
    });
    assert.equal(result.crashed, true);
    assert.match(result.crashError, /genesis unavailable/);
  });

  test('the real engine replays a clean journal with zero skips', () => {
    const clean = {
      entries: [
        { tick: 1, action: { type: 'place', spec: 'res_hut', x: 3, y: 3 } },
        { tick: 1, action: { type: 'tick' } },
      ],
    };
    const result = replayFromGenesisDefensive(clean);
    assert.equal(result.crashed, false);
    assert.equal(result.skipped.length, 0, 'a valid journal produces no skips on the real engine');
  });
});

describe('rebuildReport: before/after metrics, never claims identity', () => {
  const oldState = { tick: 100, population: 425000, funds: 50000, buildings: [{}, {}, {}] };
  const newState = { tick: 100, population: 431000, funds: 48000, buildings: [{}, {}, {}, {}] };

  test('reports before, after, and signed deltas', () => {
    const report = rebuildReport(oldState, newState, []);
    assert.deepEqual(report.before, { tick: 100, population: 425000, funds: 50000, buildings: 3 });
    assert.deepEqual(report.after, { tick: 100, population: 431000, funds: 48000, buildings: 4 });
    assert.equal(report.deltas.population, 6000, 'population diverged upward under new rules');
    assert.equal(report.deltas.funds, -2000);
    assert.equal(report.deltas.buildings, 1);
  });

  test('divergence is reported, NOT treated as failure (identical=false, no throw)', () => {
    const report = rebuildReport(oldState, newState, []);
    assert.equal(report.identical, false, 'a rules-change city differs — that is expected');
    // The contract: the report NEVER asserts identity. It simply carries the numbers.
  });

  test('identical metrics set the informational flag but are not a success gate', () => {
    const same = rebuildReport(oldState, oldState, []);
    assert.equal(same.identical, true, 'all deltas zero → informational identical flag');
    assert.deepEqual(same.deltas, { tick: 0, population: 0, funds: 0, buildings: 0 });
  });

  test('carries the skipped-actions list through to the report', () => {
    const skipped = [{ index: 2, tick: 7, type: 'place', error: 'gone' }];
    const report = rebuildReport(oldState, newState, skipped);
    assert.deepEqual(report.skipped, skipped);
  });

  test('metricsOf tolerates a null/partial state', () => {
    assert.deepEqual(metricsOf(null), { tick: 0, population: 0, funds: 0, buildings: 0 });
  });
});

describe('camera round-trip (restore across the rebuild reload)', () => {
  const cam = { zoom: 8, cx: 120, cy: 55 };

  test('synchronous stash round-trips and is read-once', () => {
    __resetCameraStash();
    stashCamera(cam);
    assert.deepEqual(consumeStashedCamera(), cam, 'stash returns the exact camera');
    assert.equal(consumeStashedCamera(), null, 'read-once: a second consume is empty');
  });

  test('persisted camera survives a (simulated) reload and is read-once', () => {
    const storage = memStorage();
    assert.equal(persistStashedCamera(storage, cam), true);
    assert.deepEqual(consumePersistedCamera(storage), cam, 'reload reads the exact camera back');
    assert.equal(consumePersistedCamera(storage), null, 'read-once: cleared after consume');
  });

  test('invalid cameras are ignored, never persisted', () => {
    __resetCameraStash();
    stashCamera(null);
    stashCamera({ zoom: 'x', cx: 1, cy: 2 });
    assert.equal(consumeStashedCamera(), null, 'garbage never becomes a stashed camera');
    const storage = memStorage();
    assert.equal(persistStashedCamera(storage, { cx: 1 }), false, 'a partial camera is rejected');
  });
});
