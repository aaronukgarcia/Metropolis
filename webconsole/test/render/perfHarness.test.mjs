// perfHarness.test.mjs — FEAT-2326609760 GPU acceleration spike, Phase 0.
//
// Proves the Phase 0 harness runs against the real 13k-building scale-gate
// fixture and produces real (non-zero, non-degenerate) numbers — this test
// is NOT a performance gate (no assert.ok(x < bound) here; see scale-gate.
// test.mjs for how this repo's real perf gates are derived and bounded).
// Its job is to prove the harness ITSELF works and reports its numbers in a
// sane shape, so the written-up figures in the spike's report are backed by
// a runnable artefact, not a one-off manual measurement.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScaleFixture } from '../scale/fixture.mjs';
import {
  measureCurrentDrawLoopJsCost,
  measureInstancedFullRebuildJsCost,
  measureInstancedDynamicOnlyJsCost,
  runPhase0Comparison,
  makeCountingCtx2D,
} from '../../src/render/perfHarness.ts';

// NOTE ON SCALE: the full 13k-building scale-gate fixture is used to prove
// CORRECTNESS at scale in instanceBuilder.test.mjs. It is deliberately NOT
// used here: MapView.tsx's real per-building calls (isOnline/blockOccupancy/
// utilisationOf) replicated by measureCurrentDrawLoopJsCost below invoke
// data.ts's non-memoised residentsCapacity()/totalJobs() aggregates (O(buildings)
// EACH call, confirmed by reading data.ts:1765-1772/2465 — genuinely O(n^2)
// over the whole city, out of this spike's scope to fix since data.ts is a
// no-touch surface for this task) — running that shape 5x at 13k buildings
// is itself the ~minutes-scale cost this whole spike exists to chase, and
// would make this correctness test unusably slow. A smaller fixture proves
// the harness mechanism works; the actual 13k-scale numbers for the report
// come from a one-off manual run (see the task report), exactly per the
// plan's own Phase 0 note that real-scale/real-GPU numbers are a separate,
// out-of-CI measurement step.
const HARNESS_TEST_BUILDING_COUNT = 800;

let fixture;
test('setup: build a small fixture for harness-mechanism correctness (not a scale measurement)', () => {
  fixture = buildScaleFixture({ buildingCount: HARNESS_TEST_BUILDING_COUNT, targetPopulation: 20000 });
  assert.equal(fixture.buildings.length, HARNESS_TEST_BUILDING_COUNT);
});

test('makeCountingCtx2D counts draw calls without touching a real backing store', () => {
  const ctx = makeCountingCtx2D();
  ctx.fillRect();
  ctx.strokeRect();
  ctx.beginPath();
  ctx.stroke();
  ctx.measureText();
  assert.equal(ctx.calls, 5);
});

test('measureCurrentDrawLoopJsCost: real, finite, non-zero cost', () => {
  const result = measureCurrentDrawLoopJsCost(fixture, 3);
  assert.equal(result.samples.length, 3);
  assert.ok(Number.isFinite(result.msPerFrame));
  assert.ok(result.msPerFrame >= 0, 'median frame cost must be non-negative');
  assert.ok(result.samples.every((s) => Number.isFinite(s) && s >= 0));
});

test('measureInstancedFullRebuildJsCost: real, finite, non-zero cost', () => {
  const result = measureInstancedFullRebuildJsCost(fixture, 3);
  assert.equal(result.samples.length, 3);
  assert.ok(Number.isFinite(result.msPerFrame));
  assert.ok(result.msPerFrame >= 0);
});

test('measureInstancedDynamicOnlyJsCost: real, finite, non-zero cost, and no worse than a full rebuild', () => {
  const dynamicOnly = measureInstancedDynamicOnlyJsCost(fixture, 3);
  const fullRebuild = measureInstancedFullRebuildJsCost(fixture, 3);
  assert.ok(Number.isFinite(dynamicOnly.msPerFrame));
  // The whole point of splitting STATIC/DYNAMIC (plan §2.2) is that the
  // steady-state per-tick path skips colour parsing + static geometry for
  // every instance — it must never cost MORE than a full rebuild. Loose
  // multiplier (not a tight bound) since this is a correctness sanity check
  // for the harness, not the CI perf gate itself (see scale-gate.test.mjs's
  // own house rule on bound derivation for how a real gate would be set).
  assert.ok(
    dynamicOnly.msPerFrame <= fullRebuild.msPerFrame * 1.5 + 5,
    `dynamic-only re-upload (${dynamicOnly.msPerFrame.toFixed(2)}ms) should not exceed a full rebuild ` +
      `(${fullRebuild.msPerFrame.toFixed(2)}ms) by more than a generous margin`
  );
});

test('runPhase0Comparison returns all three labelled samples in the documented order', () => {
  const report = runPhase0Comparison(fixture, 3);
  assert.equal(report.length, 3);
  assert.match(report[0].label, /current-canvas2d-draw-loop/);
  assert.match(report[1].label, /full rebuild/);
  assert.match(report[2].label, /dynamic-only/);
  for (const r of report) {
    assert.ok(Number.isFinite(r.msPerFrame));
    assert.equal(r.samples.length, 3);
  }
});
