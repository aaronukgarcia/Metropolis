// feat-2326609772-overlay-inc3.test.tsx — FEAT-2326609772 inc3 AC-7: the map
// overlay surfacing PER-SEGMENT road/rail saturation (lineSegmentsOf/
// lineSegmentIdByTileOf from data.ts), folded into MapView.tsx's EXISTING
// "Lines" toggle (same button group as Water/Power — Q100162 assumption
// recorded on the BOW item: no standalone toggle).
//
// Mirrors test/attack-bug622-frame-pump.test.tsx's mount idiom: a real
// StrictMode-free React root over SimProvider/MapView with a stubbed
// Canvas2D context that records fillRect calls (fillStyle + the globalAlpha
// in effect at call time), so the assertions exercise the ACTUAL production
// draw path rather than a re-implementation of its colour logic.
//
// Fixture choice: a single long contiguous run of 'm20' (motorway) tiles
// along one row, with NO other buildings (no residential/stations/rail).
// This deliberately avoids every other draw pass that could paint the SAME
// BUG-425 colour tokens ('#3fb950'/'#ff7b72') the segment overlay uses:
//   - utilisationOf() returns null for kind 'motorware' (data.ts's
//     utilisationOf switch defaults 'road'/'motorway' to the null-basis
//     case), so the per-building amber/green/red utilisation bar never
//     fires for these tiles.
//   - no rail tiles/stations exist, so buildRailGeometry/trainPositions
//     (also gated behind the SAME "Lines" toggle) yields zero trains and
//     draws nothing.
//   - the disconnected-road flash pass uses a different colour ('#ffd166'),
//     and the base building fill uses the spec's own catalogue colour
//     ('#1d5fa8' for m20), neither of which collides with OK/HOT.
// So every fillRect call painted with fillStyle '#3fb950' or '#ff7b72' while
// mounted is UNAMBIGUOUSLY attributable to the segment-saturation overlay.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { JSDOM } from 'jsdom';
import { initialState } from '../src/sim/engine.ts';
import { MAP_W, MAP_H, lineSegmentsOf, lineSegmentIdByTileOf, lineUsageOf, SEGMENT_ROAD_CLASSES } from '../src/sim/data.ts';
import { viewportTileRect, visibleBuildingsOf } from '../src/render/viewportCull.ts';
import type { Building } from '../src/sim/types.ts';

const OK = '#3fb950';
const HOT = '#ff7b72';
const CANVAS_SIZE = { w: 400, h: 300 };
// MapView's default initial view (View state at mount, before any pan/zoom).
const DEFAULT_VIEW = { zoom: 2.2, cx: 165, cy: 76 };
const STRIP_Y = 76; // same row as the default camera centre, guarantees overlap.

function computeGeom(size: { w: number; h: number }, view: typeof DEFAULT_VIEW) {
  const s = Math.min(size.w / MAP_W, size.h / MAP_H) * view.zoom;
  return { s, ox: size.w / 2 - view.cx * s, oy: size.h / 2 - view.cy * s };
}

/** One contiguous motorway strip spanning the FULL map width at STRIP_Y —
 *  guaranteed to have both on-screen and off-screen tiles under the default
 *  camera, and forms exactly one lineSegmentsOf() segment (AC-1 contiguous
 *  run). */
function stripState() {
  const base = initialState();
  const buildings: Building[] = [];
  for (let x = 0; x < MAP_W; x++) {
    buildings.push({ id: x + 1, spec: 'm20', x, y: STRIP_Y } as Building);
  }
  // speed: 0 (lead fix after CI run 34543913946, RCA on FEAT-2326609798): these
  // tests assert nothing about ticking, yet initialState() ships speed 1 so the
  // store installs a REAL 900 ms setInterval tick driver on mount. inc5's first
  // cadence snapshot pushed every test here past 900 ms, a tick fired inside
  // act(), MapView drew twice (576 = 2 x 288 fillRects) and SimState mutated
  // (tick 1->2). speed 0 makes the store skip the interval entirely.
  return { ...base, buildings, population: 0, funds: base.funds, administrationState: null, declineState: null, speed: 0 };
}

// One plain 'road' tile (tier 1, NOT in SEGMENT_ROAD_CLASSES — no segment
// resolves for it) on-screen at STRIP_Y, plus N off-screen 'res_hut' feeder
// buildings (residents:8 each, feederTrafficWeight's exact SSOT — see
// engine.ts) that drive lineUsageOf's road-class usage without ever being
// drawn themselves (parked far outside the default camera's visible rect,
// so they can never paint a colliding OK/HOT fillRect of their own).
// population is pinned to ROAD_TRAFFIC_ACTIVITY_REF (500) so
// trafficActivity(s) === 1 exactly — the whole feeder weight lands as usage,
// making the resulting saturation an exact, hand-checkable arithmetic fact
// rather than an emergent one.
const ROAD_X = 150;
const OFFSCREEN_X = 600;
const OFFSCREEN_Y = 350;

function plainRoadState(residentBuildingCount: number) {
  const base = initialState();
  const buildings: Building[] = [{ id: 1, spec: 'road', x: ROAD_X, y: STRIP_Y } as Building];
  for (let i = 0; i < residentBuildingCount; i++) {
    buildings.push({ id: 100 + i, spec: 'res_hut', x: OFFSCREEN_X + i, y: OFFSCREEN_Y } as Building);
  }
  return {
    ...base,
    buildings,
    population: 500, // ROAD_TRAFFIC_ACTIVITY_REF — pins trafficActivity(s) to exactly 1
    funds: base.funds,
    administrationState: null,
    declineState: null,
    speed: 0, // see stripState(): never race the real 900 ms tick driver
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
  return dom;
}

interface RecordedFill {
  fillStyle: string;
  alpha: number;
}

function installStubCanvas(dom: JSDOM, recorded: RecordedFill[]) {
  const stubCtx: any = {
    setTransform() {},
    clearRect() {},
    beginPath() {},
    moveTo() {},
    lineTo() {},
    stroke() {},
    fillRect() {
      recorded.push({ fillStyle: stubCtx.fillStyle, alpha: stubCtx.globalAlpha });
    },
    strokeRect() {},
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

async function mountHarness() {
  const React = await import('react');
  const { createRoot } = await import('react-dom/client');
  const { act } = await import('react-dom/test-utils');
  const { SimProvider, useSim } = await import('../src/sim/store.tsx');
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
              React.default.createElement(MapView, { key: 'map' }),
            ]),
          })
        )
      )
    );
  });

  return {
    React,
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

/** Click the "Lines" toggle button by its accessible title (matches the
 *  literal tooltip text MapView.tsx renders on that button). */
async function clickLinesToggle(dom: JSDOM, act: any) {
  const btn = [...dom.window.document.querySelectorAll('button')].find((b) =>
    (b.getAttribute('title') || '').toLowerCase().includes('network-utilisation overlay')
  );
  assert.ok(btn, 'the "Lines" toggle button (network-utilisation overlay) must be present in the DOM');
  await act(async () => {
    btn!.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  });
}

// ---------------------------------------------------------------------------
// AC-7a — appears only when toggled.
// ---------------------------------------------------------------------------

test('FEAT-2326609772 inc3: network overlay paints nothing until the Lines toggle is clicked, then paints', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    await h.hydrate(stripState());

    const segOrHotBefore = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT).length;
    assert.equal(segOrHotBefore, 0, 'precondition: with the Lines overlay OFF, no OK/HOT segment-tint fillRect should ever be painted');

    await clickLinesToggle(dom, h.act);

    const segOrHotAfter = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT).length;
    assert.ok(segOrHotAfter > 0, 'toggling Lines ON must paint at least one OK/HOT segment-tint tile for a motorway strip on screen');

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// AC-7b — viewport culling: paints exactly the on-screen line tiles, no more.
// ---------------------------------------------------------------------------

test('FEAT-2326609772 inc3: network overlay paints exactly the visible motorway tiles, never the whole off-screen strip (BUG-659)', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    const state = stripState();
    await h.hydrate(state);
    await clickLinesToggle(dom, h.act);

    const segmentPaints = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT).length;

    // Independently recompute the expected visible tile count using the SAME
    // viewport-cull primitives MapView.tsx itself calls (viewportTileRect +
    // visibleBuildingsOf), from the SAME default camera/canvas geometry.
    const geom = computeGeom(CANVAS_SIZE, DEFAULT_VIEW);
    const rect = viewportTileRect(geom, CANVAS_SIZE);
    const expectedVisible = visibleBuildingsOf(state.buildings, rect).filter((b) => SEGMENT_ROAD_CLASSES.has(b.spec)).length;

    assert.ok(expectedVisible > 0, 'precondition: the default camera must show at least one motorway tile');
    assert.ok(expectedVisible < MAP_W, 'precondition: the strip must extend beyond the visible viewport (a real cull test, not a coincidence)');
    assert.equal(
      segmentPaints,
      expectedVisible,
      `segment-tint fillRect count (${segmentPaints}) must equal the independently-computed visible-tile count (${expectedVisible}), ` +
        `never the full MAP_W=${MAP_W}-tile strip — a mismatch means the overlay is drawing outside the BUG-659 culled pass`
    );

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// AC-7c — colour maps saturation buckets deterministically.
// ---------------------------------------------------------------------------

test('FEAT-2326609772 inc3: every painted tile colour/alpha matches its OWN segment saturation from lineSegmentsOf/lineSegmentIdByTileOf', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    const state = stripState();
    await h.hydrate(state);
    await clickLinesToggle(dom, h.act);

    const liveState = h.getState();
    const segmentIdByTile = lineSegmentIdByTileOf(liveState);
    const segmentById = new Map(lineSegmentsOf(liveState).map((seg) => [seg.segmentId, seg]));

    // The single contiguous m20 strip must fold into exactly ONE segment
    // (AC-1) covering every tile — pin this so the fixture's own precondition
    // can fail loud if the flood-fill scope ever changes.
    const ids = new Set([...segmentIdByTile.values()]);
    assert.equal(ids.size, 1, 'precondition: one contiguous m20 strip must be exactly one segment');
    const [seg] = [...segmentById.values()];
    assert.ok(seg, 'the segment must resolve via lineSegmentsOf');

    const expectedFillStyle = seg.overCapacity ? HOT : OK;
    const expectedAlpha = seg.overCapacity ? 0.85 : 0.25 + 0.6 * seg.saturation;

    const segmentPaints = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT);
    assert.ok(segmentPaints.length > 0, 'precondition: at least one segment tile must have painted');
    for (const paint of segmentPaints) {
      assert.equal(paint.fillStyle, expectedFillStyle, 'every tile of the SAME single segment must render the SAME colour bucket');
      assert.ok(
        Math.abs(paint.alpha - expectedAlpha) < 1e-9,
        `alpha ${paint.alpha} must equal the deterministic formula's ${expectedAlpha} for saturation ${seg.saturation}`
      );
    }

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// Coverage fix pins (round reject #2) — plain 'road' has NO segment (outside
// SEGMENT_ROAD_CLASSES), so pre-fix it fell through to sat=0/idle-green even
// while massively over capacity. These pin the CLASS-level fallback the fix
// adds: every isLineSpec tile is tinted, using the segment's own numbers
// where a segment exists and the tile's CLASS usage (lineUsageOf) otherwise.
//
// HONESTY NOTE (round reject #3 / Q100163, separate BOW item filed): a
// segment's saturation is apportioned by capacity share, so it is
// IDENTICAL to its class's saturation until a real per-segment flow basis
// lands — there is no per-segment bottleneck signal here today, only
// class-level network loading. These two pins exercise the class-level
// fallback path directly (plain 'road' has no segment at all), so they hold
// regardless of when/whether Q100163 ever changes the segment math.
// ---------------------------------------------------------------------------

test('FEAT-2326609772 inc3 coverage fix: an over-capacity plain-road city renders HOT at the same alpha as pre-inc3 (attacker-measured: HOT #ff7b72 alpha 0.85)', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    // 20 res_hut feeders x 8 residents = 160 feeder weight, activity=1 (pop=500)
    // => totalTraffic=160 far above the single road tile's capacity of 100 —
    // "far above capacity" per the reject's own wording.
    const state = plainRoadState(20);
    await h.hydrate(state);
    await clickLinesToggle(dom, h.act);

    const liveState = h.getState();
    const cls = lineUsageOf(liveState).find((u) => u.spec === 'road');
    assert.ok(cls, 'precondition: the road class must resolve via lineUsageOf');
    assert.ok(cls!.overCapacity, 'precondition: this fixture must actually be over capacity (usage 160 > capacity 100)');
    // Precondition: plain 'road' must have NO segment — this exercises the
    // class-level fallback path, not the segment path (GR#3 coverage fix).
    const segmentIdByTile = lineSegmentIdByTileOf(liveState);
    assert.equal(
      segmentIdByTile.get(`${ROAD_X},${STRIP_Y}`),
      undefined,
      'precondition: plain road is outside SEGMENT_ROAD_CLASSES and must have no segment'
    );

    const roadPaints = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT);
    assert.ok(roadPaints.length > 0, 'precondition: the visible road tile must have painted something');
    for (const paint of roadPaints) {
      assert.equal(paint.fillStyle, HOT, 'an over-capacity plain-road tile with no segment must fall back to its CLASS saturation and render HOT, not idle green');
      assert.ok(paint.alpha >= 0.8, `HOT alpha ${paint.alpha} must match the pre-inc3 fixed HOT alpha (>= 0.8, attacker-measured 0.85)`);
    }

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

test('FEAT-2326609772 inc3 coverage fix: a 72%-saturated plain-road city paints alpha within 1e-6 of the class formula', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    // 9 res_hut feeders x 8 residents = 72 feeder weight, activity=1 (pop=500)
    // => totalTraffic=72 against a 100-capacity single road tile: an exact
    // 72% class saturation, well within capacity (not the HOT branch).
    const state = plainRoadState(9);
    await h.hydrate(state);
    await clickLinesToggle(dom, h.act);

    const liveState = h.getState();
    const cls = lineUsageOf(liveState).find((u) => u.spec === 'road');
    assert.ok(cls, 'precondition: the road class must resolve via lineUsageOf');
    assert.ok(!cls!.overCapacity, 'precondition: this fixture must stay within capacity (72 < 100)');
    assert.ok(Math.abs(cls!.saturation - 0.72) < 1e-9, `precondition: saturation must be exactly 0.72 (got ${cls!.saturation})`);

    const expectedAlpha = 0.25 + 0.6 * cls!.saturation;
    const roadPaints = recorded.filter((r) => r.fillStyle === OK || r.fillStyle === HOT);
    assert.ok(roadPaints.length > 0, 'precondition: the visible road tile must have painted something');
    for (const paint of roadPaints) {
      assert.equal(paint.fillStyle, OK, 'a within-capacity plain-road tile must render OK, not HOT');
      assert.ok(
        Math.abs(paint.alpha - expectedAlpha) < 1e-6,
        `alpha ${paint.alpha} must equal the class formula's ${expectedAlpha} (0.25 + 0.6*saturation) within 1e-6`
      );
    }

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// Zero sim-state impact — toggling the overlay never mutates SimState.
// ---------------------------------------------------------------------------

test('FEAT-2326609772 inc3: toggling the Lines overlay on/off is byte-identical on SimState (render-only, GR#21)', async () => {
  const dom = installJsdom();
  try {
    const recorded: RecordedFill[] = [];
    installStubCanvas(dom, recorded);
    const h = await mountHarness();
    const state = stripState();
    await h.hydrate(state);

    const before = JSON.stringify(h.getState());
    await clickLinesToggle(dom, h.act); // ON
    const afterOn = JSON.stringify(h.getState());
    await clickLinesToggle(dom, h.act); // OFF
    const afterOff = JSON.stringify(h.getState());

    assert.equal(afterOn, before, 'toggling the overlay ON must not change a single byte of SimState');
    assert.equal(afterOff, before, 'toggling the overlay OFF must not change a single byte of SimState');

    await h.unmount();
  } finally {
    dom.window.close();
  }
});

// ---------------------------------------------------------------------------
// Source-scan purity pin — no Date.now/localStorage/Math.random in the
// segment-tint block itself (GR#21 / StrictMode-safety, BUG-756 class).
// ---------------------------------------------------------------------------

function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
}

test('FEAT-2326609772 inc3: source-scan purity — the network-overlay draw block contains no Date.now/Math.random/localStorage/ref-mutating setState', async () => {
  const fs = await import('node:fs');
  const src = fs.readFileSync(new URL('../src/components/MapView.tsx', import.meta.url), 'utf8');
  const start = src.indexOf('FEAT-2326609772 inc3: network-utilisation');
  assert.ok(start >= 0, 'precondition: must find the inc3 overlay block marker comment in MapView.tsx');
  const nextMarker = src.indexOf('// station connectivity dots', start);
  assert.ok(nextMarker > start, 'precondition: must find the next draw-pass marker after the network overlay block');
  const block = stripComments(src.slice(start, nextMarker));
  assert.ok(!/Date\.now/.test(block), 'the network overlay block (code, comments stripped) must not read the wall clock');
  assert.ok(!/Math\.random/.test(block), 'the network overlay block (code, comments stripped) must not use Math.random');
  assert.ok(!/localStorage/.test(block), 'the network overlay block (code, comments stripped) must not touch localStorage');
  assert.ok(!/set[A-Z]\w*\s*\(/.test(block), 'the network overlay block must not call any setState updater at all (it is a pure draw pass, never a state mutation — BUG-756 class)');
  assert.ok(!/\.current\s*=/.test(block), 'the network overlay block must not mutate a ref');
});
