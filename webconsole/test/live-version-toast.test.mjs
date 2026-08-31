// live-version-toast.test.mjs — BAR-1 (BUG-435 defect 3, round r1 REJECT).
//
// liveVersion.tsx:166 used to show ONE toast message — "running hot, your
// city kept playing" — from BOTH the normal hot-swap path (true: the sim kept
// ticking) and the queued-post-rebuild drain path (false: ticks were
// suppressed for the whole rebuild). This proves toastMessageFor() produces
// two DIFFERENT, correct messages for the two sources, and that the drain
// message never claims the city kept playing.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { toastMessageFor } from '../src/sim/upgradeToast.ts';

describe('toastMessageFor: truthful copy per upgrade source', () => {
  test('hot path may claim the city kept playing', () => {
    const msg = toastMessageFor('v0.3.0.66', 'hot');
    assert.ok(msg.includes('v0.3.0.66'), 'must include the new version label');
    assert.ok(msg.includes('kept playing'), 'hot path is truthfully allowed to say the city kept playing');
  });

  test('drain path must NOT claim the city kept playing', () => {
    const msg = toastMessageFor('v0.3.0.66', 'drain');
    assert.ok(msg.includes('v0.3.0.66'), 'must include the new version label');
    assert.ok(!msg.includes('kept playing'), 'BUG-435 defect 3: drain path must not claim ticks continued');
    assert.ok(msg.includes('rebuilt'), 'drain path must say the swap applied after the rebuild');
  });

  test('the two sources produce genuinely different copy', () => {
    const hot = toastMessageFor('v1.0.0.1', 'hot');
    const drain = toastMessageFor('v1.0.0.1', 'drain');
    assert.notEqual(hot, drain, 'hot and drain paths must render different, correct copy');
  });
});
