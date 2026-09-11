// bug1039-mapview-bend-drag.test.tsx — BUG-1039/BUG-1040 (FEAT-1972079910 inc4
// AC-5, r2 round finding opus-reround-feat910-bends). The ONLY player-facing
// entry point to the bend planner — MapView.tsx's onPointerMove road-tracker
// branch — had no behavioural test: two dataflow mutants (reassign `path` to
// computePath() after the legalisePath block; replace the legalisePath
// result with computePath while leaving the call textually present) survived
// the author suite completely green, because every pre-existing pin is a
// grep over comment-stripped source and a regex cannot see dataflow. A third
// mutant (hardcoding `snapped: false` in the tracker update) also survived —
// the amber snapped-endpoint outline (LEAD RULING: "the preview shows where
// the road will really end") was never rendered-path tested.
//
// This suite mounts the REAL MapView inside the REAL SimProvider (react-dom/
// client + jsdom — the store-dispatch.test.tsx / feat-2326609772-overlay-
// inc3.test.tsx live-mount idiom), hydrates a minimal fixture city (funds
// huge, catalogue fully unlocked, tool pre-selected to a tier-2 road spec),
// and simulates a REAL pointerdown/pointermove/pointerup diagonal drag on the
// canvas element. A DispatchSpy component sits between SimProvider and
// MapView (same SimContext, wraps only `dispatch`) so the exact `Action`
// object MapView dispatches on pointerup can be inspected directly, not just
// inferred from the resulting state.
//
// Assertions:
//  (1)/(2) BUG-1039 — the dispatched `placeRoadPath` action's `tiles` equal
//      roadTracker.ts's own `legalisePath(anchor, cursor, minRun).tiles`
//      (minRun read via data.ts's minBendRadiusTilesForTier — never a
//      literal), and the action carries `bendLegal: true`.
//  (3) BUG-1039 — after the drag, SimState.buildings contains EXACTLY those
//      planned tiles as `rd_avenue` buildings (the reducer's enforcement
//      accepted the real dispatched path) — no more, no fewer.
//  (4) BUG-1040 — for a drag whose cursor needs snapping (0 < |dy| < minRun
//      with dx present), the stubbed Canvas2D context records a strokeRect
//      call with the amber snapped-outline style ('#f2cc60') over the actual
//      (snapped) end tile, and the negative control (an unsnapped diagonal
//      drag) records NONE.
//
// RED/GREEN mutation proof: see docs/tmp/bug1039-mutation-log.md (scratch-
// copy procedure — cp MapView.tsx to a throwaway backup, apply each of the
// three attacker mutants in turn to the LIVE file, re-run this suite RED,
// restore from the backup via `mv` — never `git checkout`/`restore`, GR#24
// — re-run GREEN, md5-verify the restore).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { initialState } from '../src/sim/engine.ts';
import { MAP_W, MAP_H } from '../src/sim/grid.ts';
import { SPECS, roadTierOf, minBendRadiusTilesForTier, type RoadTier } from '../src/sim/data.ts';
import { legalisePath } from '../src/sim/roadTracker.ts';
import type { Building } from '../src/sim/types.ts';

const ROAD_SPEC = 'rd_avenue'; // tier 2 — minBendRadiusTiles.avenue_2_plus_2
// Large enough that geom.s (MapView's tile-to-pixel scale) exceeds the
// component's own `geom.s > 2` gate for the road-tracker ghost/outline draw
// pass (MapView.tsx ~line 973), matching MapView's default View state.
const CANVAS_SIZE = { w: 1872, h: 1104 }; // 3x (MAP_W, MAP_H)
const DEFAULT_VIEW = { zoom: 2.2, cx: 165, cy: 76 }; // MapView's initial useState<View>

function computeGeom(size: { w: number; h: number }, view: typeof DEFAULT_VIEW) {
  const s = Math.min(size.w / MAP_W, size.h / MAP_H) * view.zoom;
  return { s, ox: size.w / 2 - view.cx * s, oy: size.h / 2 - view.cy * s };
}
const GEOM = computeGeom(CANVAS_SIZE, DEFAULT_VIEW);

/** Client (pointer-event) coordinates landing mid-tile for (tileX, tileY), matching
 *  MapView.tsx's tileFrom() inverse (rect.left/top are 0 for an unlaid-out jsdom canvas). */
function clientOf(tileX: number, tileY: number) {
  return { clientX: GEOM.ox + tileX * GEOM.s + GEOM.s / 2, clientY: GEOM.oy + tileY * GEOM.s + GEOM.s / 2 };
}

function fixtureState() {
  const base = initialState();
  return {
    ...base,
    buildings: [] as Building[],
    funds: 1_000_000_000,
    unlockedAll: true,
    tool: { mode: 'build', spec: ROAD_SPEC },
    administrationState: null,
    declineState: null,
    speed: 0, // never race the real 900ms tick driver (BUG-936 idiom)
  };
}

function installJsdom() {
  const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
    url: 'http://localhost/',
    pretendToBeVisual: true,
  });
  const { window } = dom;
  (globalThis as any).window = window;
  (globalThis as any).document = window.document;
  Object.defineProperty(globalThis, 'navigator', { value: window.navigator, configurable: true, writable: true });
  (globalThis as any).HTMLElement = window.HTMLElement;
  (globalThis as any).requestAnimationFrame = window.requestAnimationFrame.bind(window);
  (globalThis as any).cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  // jsdom has no pointer-capture implementation; MapView's onPointerDown
  // calls e.currentTarget.setPointerCapture() unconditionally.
  (window.HTMLElement.prototype as any).setPointerCapture = function () {};
  (window.HTMLElement.prototype as any).releasePointerCapture = function () {};
  return dom;
}

interface RecordedStroke {
  strokeStyle: string;
  lineWidth: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

function installStubCanvas(dom: JSDOM, strokes: RecordedStroke[]) {
  const stubCtx: any = {
    setTransform() {},
    clearRect() {},
    beginPath() {},
    moveTo() {},
    lineTo() {},
    stroke() {},
    fillRect() {},
    fillText() {},
    fill() {},
    arc() {},
    rect() {},
    clip() {},
    setLineDash() {},
    measureText: (s: string) => ({ width: s.length * 6 }),
    save() {},
    restore() {},
    translate() {},
    scale() {},
    rotate() {},
    closePath() {},
    strokeRect(x: number, y: number, w: number, h: number) {
      strokes.push({ strokeStyle: stubCtx.strokeStyle, lineWidth: stubCtx.lineWidth, x, y, w, h });
    },
    strokeStyle: '',
    fillStyle: '',
    lineWidth: 1,
    globalAlpha: 1,
    font: '',
    textAlign: 'start',
    textBaseline: 'alphabetic',
  };
  (dom.window as any).HTMLCanvasElement.prototype.getContext = function () {
    return stubCtx;
  };

  class StubResizeObserver {
    cb: (entries: unknown[]) => void;
    constructor(cb: (entries: unknown[]) => void) {
      this.cb = cb;
    }
    observe(el: unknown) {
      this.cb([{ target: el, contentRect: { width: CANVAS_SIZE.w, height: CANVAS_SIZE.h } }]);
    }
    unobserve() {}
    disconnect() {}
  }
  (globalThis as any).ResizeObserver = StubResizeObserver;
  (dom.window as any).ResizeObserver = StubResizeObserver;
}

async function mountHarness(recordedDispatches: unknown[]) {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BusyProvider } = await import('../src/components/Busy.tsx');
  const { OverlayManagerProvider } = await import('../src/components/overlayManager.tsx');
  const { MapView } = await import('../src/components/MapView.tsx');

  let latestDispatch: ((a: unknown) => void) | null = null;
  let latestState: any = null;
  function Probe() {
    const { state, dispatch } = useSim();
    latestDispatch = dispatch as (a: unknown) => void;
    latestState = state;
    return null;
  }

  // DispatchSpy: consumes the REAL SimContext value and re-provides it with
  // `dispatch` wrapped to record every action MapView (its only child here)
  // dispatches, before forwarding unchanged to the real reducer dispatch —
  // production behaviour is completely untouched, only observed.
  function DispatchSpy({ children }: { children?: unknown }) {
    const ctx = useSim();
    const spied = React.default.useMemo(
      () => ({
        ...ctx,
        dispatch: (a: unknown) => {
          recordedDispatches.push(a);
          return (ctx.dispatch as (a: unknown) => void)(a);
        },
      }),
      [ctx]
    );
    return React.default.createElement(SimContext.Provider, { value: spied }, children as any);
  }

  const container = (globalThis as any).document.getElementById('root')!;
  const root = createRoot(container);

  await act(async () => {
    root.render(
      React.default.createElement(
        OverlayManagerProvider,
        null,
        React.default.createElement(
          BusyProvider,
          null,
          React.default.createElement(SimProvider, {
            children: React.default.createElement(React.default.Fragment, null, [
              React.default.createElement(Probe, { key: 'probe' }),
              React.default.createElement(
                DispatchSpy,
                { key: 'spy' },
                React.default.createElement(MapView, { key: 'map' })
              ),
            ]),
          })
        )
      )
    );
  });

  return {
    act,
    root,
    hydrate: async (state: unknown) => {
      await act(async () => {
        latestDispatch!({ type: 'hydrate', state });
      });
    },
    getState: () => latestState,
    unmount: async () => {
      await act(async () => {
        root.unmount();
      });
    },
  };
}

function getCanvas(dom: JSDOM): HTMLCanvasElement {
  const cv = dom.window.document.querySelector('canvas');
  assert.ok(cv, 'MapView must render a <canvas> element');
  return cv as HTMLCanvasElement;
}

async function dragOnCanvas(
  dom: JSDOM,
  act: any,
  cv: HTMLCanvasElement,
  anchor: { x: number; y: number },
  cursor: { x: number; y: number }
) {
  const a = clientOf(anchor.x, anchor.y);
  const c = clientOf(cursor.x, cursor.y);
  await act(async () => {
    cv.dispatchEvent(
      new (dom.window as any).PointerEvent('pointerdown', {
        clientX: a.clientX,
        clientY: a.clientY,
        button: 0,
        bubbles: true,
        pointerId: 1,
      })
    );
  });
  await act(async () => {
    cv.dispatchEvent(
      new (dom.window as any).PointerEvent('pointermove', {
        clientX: c.clientX,
        clientY: c.clientY,
        button: 0,
        bubbles: true,
        pointerId: 1,
      })
    );
  });
  await act(async () => {
    cv.dispatchEvent(
      new (dom.window as any).PointerEvent('pointerup', {
        clientX: c.clientX,
        clientY: c.clientY,
        button: 0,
        bubbles: true,
        pointerId: 1,
      })
    );
  });
}

function sortTiles(tiles: { x: number; y: number }[]) {
  return [...tiles].sort((p, q) => p.x - q.x || p.y - q.y);
}

// ---------------------------------------------------------------------------
// BUG-1039 — diagonal drag, no snapping needed: both mutants under attack
// (path reassigned to computePath after the block; legalisePath's result
// swapped for computePath while the call stays present) produce a raw
// Bresenham zigzag whose bend radii are all 1 — illegal at minRun 2 — so the
// reducer refuses the WHOLE path (MET-V961) and buildings stays empty,
// failing assertion (3). The real, unmutated code plans a bend-legal
// staircase that the reducer accepts tile-for-tile.
// ---------------------------------------------------------------------------

test('BUG-1039: diagonal tier-2 road drag dispatches placeRoadPath with legalisePath tiles + bendLegal:true, and the reducer places exactly those tiles', async () => {
  const dom = installJsdom();
  try {
    const strokes: RecordedStroke[] = [];
    const dispatches: any[] = [];
    installStubCanvas(dom, strokes);
    const h = await mountHarness(dispatches);
    await h.hydrate(fixtureState());

    const sp = SPECS[ROAD_SPEC];
    const tier = roadTierOf(sp) as RoadTier;
    const minRun = minBendRadiusTilesForTier(tier);
    assert.equal(minRun, 2, 'precondition: avenue (tier 2) minBendRadiusTiles must be 2 per data/roads.json');

    const anchor = { x: 200, y: 100 };
    const cursor = { x: 206, y: 106 }; // dx=dy=6, both >= minRun: no snap needed
    const expected = legalisePath(anchor.x, anchor.y, cursor.x, cursor.y, minRun);
    assert.equal(expected.snapped, false, 'precondition: this drag must not require snapping');
    assert.ok(
      expected.tiles.length > 3 && expected.tiles.length < 13 + 1,
      `precondition: expected planned path must actually bend (got ${expected.tiles.length} tiles)`
    );

    const cv = getCanvas(dom);
    await dragOnCanvas(dom, h.act, cv, anchor, cursor);

    const placeActions = dispatches.filter((a) => a && a.type === 'placeRoadPath');
    assert.equal(placeActions.length, 1, 'exactly one placeRoadPath action must be dispatched on pointerup');
    const action = placeActions[0];

    // (1) dispatched tiles === legalisePath(...).tiles
    assert.deepEqual(
      action.tiles,
      expected.tiles,
      'the dispatched placeRoadPath tiles must equal legalisePath(anchor, cursor, minRun).tiles exactly'
    );
    // (2) the action carries bendLegal: true
    assert.equal(action.bendLegal, true, 'the dispatched action must carry bendLegal: true');
    assert.equal(action.spec, ROAD_SPEC);

    // (3) reducer accepted state contains EXACTLY those tiles as rd_avenue buildings
    const finalState = h.getState();
    const roadTiles = finalState.buildings.filter((b: any) => b.spec === ROAD_SPEC).map((b: any) => ({ x: b.x, y: b.y }));
    assert.deepEqual(
      sortTiles(roadTiles),
      sortTiles(expected.tiles),
      'SimState.buildings must contain exactly the legalisePath-planned tiles as rd_avenue — no more, no fewer ' +
        '(a bend-illegal zigzag from either computePath mutant would be refused whole-path by MET-V961, leaving buildings empty)'
    );

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// BUG-1040 — snapped endpoint amber outline. A drag whose dy is nonzero but
// below minRun forces legalisePath to snap: the tie rule (dropChange <=
// extendChange) drops the short axis to 0, so the plan lands short of the
// raw cursor. MapView must render the amber ('#f2cc60') outline over the
// ACTUAL (snapped) end tile. Hardcoding `snapped: false` in the tracker
// update (the attacker's mutant) makes `roadTracker.snapped` permanently
// falsy, so this outline can never draw — the render-path `if
// (roadTracker.snapped && ...)` guard never executes its body.
// ---------------------------------------------------------------------------

test('BUG-1040: a snapped drag paints the amber snapped-endpoint outline over the real (snapped) end tile; an unsnapped drag paints none', async () => {
  const dom = installJsdom();
  try {
    const strokes: RecordedStroke[] = [];
    const dispatches: any[] = [];
    installStubCanvas(dom, strokes);
    const h = await mountHarness(dispatches);
    await h.hydrate(fixtureState());

    const sp = SPECS[ROAD_SPEC];
    const tier = roadTierOf(sp) as RoadTier;
    const minRun = minBendRadiusTilesForTier(tier);

    const anchor = { x: 200, y: 100 };
    const cursor = { x: 205, y: 101 }; // dx=5 (>= minRun), dy=1 (0 < |dy| < minRun) -> snap
    const expected = legalisePath(anchor.x, anchor.y, cursor.x, cursor.y, minRun);
    assert.equal(expected.snapped, true, 'precondition: this drag must require snapping (0 < |dy| < minRun)');
    assert.notDeepEqual(
      { x: expected.endX, y: expected.endY },
      cursor,
      'precondition: the planned endpoint must differ from the raw cursor — that is what "snapped" means'
    );

    const cv = getCanvas(dom);
    // pointerdown + pointermove only — the amber preview must render DURING
    // the drag, before pointerup commits/clears the tracker.
    const a = clientOf(anchor.x, anchor.y);
    const c = clientOf(cursor.x, cursor.y);
    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointerdown', { clientX: a.clientX, clientY: a.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });
    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointermove', { clientX: c.clientX, clientY: c.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });

    const endTile = expected.tiles[expected.tiles.length - 1];
    const expectedPx = GEOM.ox + endTile.x * GEOM.s;
    const expectedPy = GEOM.oy + endTile.y * GEOM.s;
    const amberStrokes = strokes.filter((s) => s.strokeStyle === '#f2cc60');
    assert.ok(amberStrokes.length >= 1, 'a snapped drag must paint at least one amber (#f2cc60) strokeRect outline');
    const overEndTile = amberStrokes.some((s) => Math.abs(s.x - (expectedPx - 1)) < 0.01 && Math.abs(s.y - (expectedPy - 1)) < 0.01);
    assert.ok(
      overEndTile,
      `the amber outline must sit over the PLANNED (snapped) end tile (${endTile.x},${endTile.y}), not the raw mouse cursor (${cursor.x},${cursor.y})`
    );

    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointerup', { clientX: c.clientX, clientY: c.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });
    await h.unmount();
  } finally {
    dom.window.close();
  }
});

test('BUG-1040 negative control: an unsnapped diagonal drag paints NO amber snapped-endpoint outline', async () => {
  const dom = installJsdom();
  try {
    const strokes: RecordedStroke[] = [];
    const dispatches: any[] = [];
    installStubCanvas(dom, strokes);
    const h = await mountHarness(dispatches);
    await h.hydrate(fixtureState());

    const cv = getCanvas(dom);
    const anchor = { x: 200, y: 100 };
    const cursor = { x: 206, y: 106 }; // no snap (see the BUG-1039 test above)
    const a = clientOf(anchor.x, anchor.y);
    const c = clientOf(cursor.x, cursor.y);
    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointerdown', { clientX: a.clientX, clientY: a.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });
    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointermove', { clientX: c.clientX, clientY: c.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });

    const amberStrokes = strokes.filter((s) => s.strokeStyle === '#f2cc60');
    assert.equal(amberStrokes.length, 0, 'an unsnapped drag must never paint the amber snapped-endpoint outline');

    await h.act(async () => {
      cv.dispatchEvent(
        new (dom.window as any).PointerEvent('pointerup', { clientX: c.clientX, clientY: c.clientY, button: 0, bubbles: true, pointerId: 1 })
      );
    });
    await h.unmount();
  } finally {
    dom.window.close();
  }
});
