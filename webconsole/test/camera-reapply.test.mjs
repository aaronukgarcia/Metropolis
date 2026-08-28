// camera-reapply.test.mjs — FEAT-1972079897 inc2 RE-APPLY (the deferred wiring).
//
// inc2 captured the UI camera into a reload-surviving stash (cameraStash.ts) but
// the MapView re-apply was deferred to the owning lane. This proves the PURE seam
// of that re-apply — applyStashedCameraToView — which is where the decision lives
// (MapView's mount effect is a one-line call over it: consume then setView).
//
//   1. a consumed camera produces a view homed on its zoom + focus/pan.
//   2. a null/undefined stash (fresh session, no camera carried) → view unchanged.
//   3. a malformed camera (non-finite / wrong type) → rejected, view unchanged.
//
// RED proof (performed out-of-band, cp/mv NEVER git): break applyStashedCameraToView
// to ignore the camera (always `return view`) → case 1 goes RED; restore. See report.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { applyStashedCameraToView } from '../src/sim/cameraApply.ts';

const DEFAULT_VIEW = { zoom: 2.2, cx: 165, cy: 76 };

describe('applyStashedCameraToView: re-home the view from a stashed camera', () => {
  test('a stashed camera produces a view with its zoom + focus/pan', () => {
    const cam = { zoom: 8, cx: 120, cy: 55 };
    const next = applyStashedCameraToView(DEFAULT_VIEW, cam);
    assert.deepEqual(next, { zoom: 8, cx: 120, cy: 55 }, 'view re-homes onto the stashed camera');
    // Distinct from the default — proves the apply actually moved the camera.
    assert.notDeepEqual(next, DEFAULT_VIEW);
  });

  test('null stash → the current (default) view is returned unchanged', () => {
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, null), DEFAULT_VIEW, 'no camera → no jump');
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, undefined), DEFAULT_VIEW);
  });

  test('malformed cameras are rejected, view unchanged', () => {
    // Non-finite and wrong-type fields must never overwrite the live view.
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, { zoom: NaN, cx: 1, cy: 2 }), DEFAULT_VIEW);
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, { zoom: Infinity, cx: 1, cy: 2 }), DEFAULT_VIEW);
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, { zoom: 4, cx: '10', cy: 2 }), DEFAULT_VIEW);
    assert.equal(applyStashedCameraToView(DEFAULT_VIEW, { zoom: 4, cx: 10 }), DEFAULT_VIEW, 'missing cy rejected');
  });

  test('a valid camera at the origin (all-finite zeros) is applied, not mistaken for empty', () => {
    // Guards against a truthiness bug: 0 is a legitimate coordinate.
    const next = applyStashedCameraToView(DEFAULT_VIEW, { zoom: 1, cx: 0, cy: 0 });
    assert.deepEqual(next, { zoom: 1, cx: 0, cy: 0 });
  });
});
