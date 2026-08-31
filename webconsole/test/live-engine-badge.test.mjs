// live-engine-badge.test.mjs — FEAT-1972079852 increment 1: the feature
// flag defaults OFF (mock sim stays the default UI everywhere), and the
// flag reader is a pure function testable without a real browser
// localStorage.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  isLiveEngineEnabled,
  LIVE_ENGINE_FLAG_KEY,
} from '../src/sim/liveEngineFlag.ts';

function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, v),
  };
}

describe('isLiveEngineEnabled (feature flag, default OFF)', () => {
  test('disabled when no storage is available', () => {
    assert.equal(isLiveEngineEnabled(undefined), false);
  });

  test('disabled when the flag key is absent', () => {
    assert.equal(isLiveEngineEnabled(fakeStorage()), false);
  });

  test('disabled for any value other than the literal "1"', () => {
    assert.equal(isLiveEngineEnabled(fakeStorage({ [LIVE_ENGINE_FLAG_KEY]: 'true' })), false);
    assert.equal(isLiveEngineEnabled(fakeStorage({ [LIVE_ENGINE_FLAG_KEY]: '0' })), false);
  });

  test('enabled only when set to exactly "1"', () => {
    assert.equal(isLiveEngineEnabled(fakeStorage({ [LIVE_ENGINE_FLAG_KEY]: '1' })), true);
  });

  test('a storage.getItem that throws is treated as disabled, not a crash', () => {
    const throwing = {
      getItem() {
        throw new Error('private mode');
      },
    };
    assert.equal(isLiveEngineEnabled(throwing), false);
  });
});
