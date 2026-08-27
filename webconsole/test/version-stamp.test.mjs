// version-stamp.test.mjs — BUG-424: debug.json meta version stamping.
//
// The bug: buildDebugJson stamped meta.appVersion from versionRaw (the frozen
// src/generated/version.ts). During a long HMR dev session the post-commit
// `gen-version.mjs --live-only` hook advances version.live.json (the badge,
// polled by useLiveVersion) but NOT version.ts, so versionRaw froze while the
// badge and the running code moved ahead — a debug dump misreported the build.
//
// The fix: meta.appVersion now reads the freshest LIVE version from the
// synchronous liveVersionRef (set by useLiveVersion on each successful poll,
// mirroring the badge), and meta.buildVersion carries the frozen build-time
// versionRaw. Their divergence is itself diagnostic.
//
// RED proof: revert the builder to stamp `ui.appVersion` for meta.appVersion
// (scratch-copy of debugjson.ts) — the "reflects live version" case goes RED.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDebugJson } from '../src/sim/debugjson.ts';
import { setLiveVersion, __resetLiveVersion } from '../src/sim/liveVersionRef.ts';
// versionRaw === versionInfo.version (sim/version.ts re-exports it); import the
// generated module directly since version.ts uses an extensionless import that
// node --test cannot resolve.
import { versionInfo } from '../src/generated/version.ts';
import { initialState } from '../src/sim/engine.ts';

const versionRaw = versionInfo.version;

/** Non-sim builder inputs; appVersion carries the BUILD-TIME (frozen) value. */
function ui(overrides = {}) {
  return {
    appVersion: versionRaw,
    frameAtMs: 1_700_000_000_000,
    map: { view: { zoom: 3.5, cx: 150, cy: 70 }, selectedBuildingId: 42, showWater: true },
    errors: [],
    ...overrides,
  };
}

test('meta.appVersion reflects the LIVE version once polled, and buildVersion stays the frozen build value', () => {
  __resetLiveVersion();
  setLiveVersion('v0.3.0.99');
  const dj = buildDebugJson(initialState(), ui());
  assert.equal(dj.meta.appVersion, 'v0.3.0.99', 'appVersion should match the freshest live/badge version');
  assert.equal(dj.meta.buildVersion, versionRaw, 'buildVersion should be the frozen build-time versionRaw');
});

test('before any live poll, meta.appVersion FALLS BACK to the build-time versionRaw', () => {
  __resetLiveVersion();
  const dj = buildDebugJson(initialState(), ui());
  assert.equal(dj.meta.appVersion, versionRaw, 'fresh module state: appVersion falls back to build version');
  assert.equal(dj.meta.buildVersion, versionRaw);
});

test('live and build versions are distinguishable when they diverge (the diagnostic split)', () => {
  __resetLiveVersion();
  setLiveVersion('v0.3.0.150');
  const dj = buildDebugJson(initialState(), ui({ appVersion: 'v0.3.0-67-gb52f236-dirty' }));
  assert.equal(dj.meta.appVersion, 'v0.3.0.150');
  assert.equal(dj.meta.buildVersion, 'v0.3.0-67-gb52f236-dirty');
  assert.notEqual(dj.meta.appVersion, dj.meta.buildVersion);
  __resetLiveVersion();
});
