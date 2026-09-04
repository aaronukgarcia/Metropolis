import { useEffect, useMemo, useRef, useState } from 'react';
import {
  MAP_H,
  MAP_W,
  SPECS,
  POWER_LINES,
  countByKind,
  coordLabel,
  ROW_BAND,
  yLabel,
  serviceDemandOf,
  fits,
  occupiedSet,
  stationLinks,
  isOnline,
  plantEffServed,
  placementCost,
  densityTier,
  TIER_COLORS,
  blockOccupancy,
  PIPE_TIERS,
  utilisationOf,
  buildingDisplayStates,
  constructionTicks,
  lineUsageOf,
  isLineSpec,
  isRoadSpec,
  isRailSpec,
  computeFailedGates,
  AUTO_BUILD_DEMAND_PERCENT,
  footprintOf,
} from '../sim/data';
import { computePath, type Tile } from '../sim/roadTracker';
import { viewportTileRect, visibleBuildingsOf } from '../render/viewportCull';
import { buildRailGeometry, trainPositions, type RailTile, type StationTile } from '../sim/trains';
import { useSim } from '../sim/simContext';
import { demandOf, specUnlocked, SPEED_MS, forcedSaleAssets, TICKS_PER_YEAR, CONSOLIDATOR_ENABLED_DEFAULT, CONSOLIDATOR_MODE_DEFAULT } from '../sim/engine';
import { monthlyScopeOf, sectionOriginOf, sectionTilesOf } from '../sim/consolidator';
import { currentConsolidatorFocus } from '../sim/consolidatorFocus';
import { glideWindowForDay } from '../sim/consolidatorGlide';
import { isConsolidatorBoxVisible } from '../sim/consolidatorDisplayFlag';
import {
  BAILOUT_DURATION_TICKS,
  ADMINISTRATION_DURATION_TICKS,
  SECOND_BAILOUT_DURATION_TICKS,
} from '../sim/fiscal';
import { publishMapUi } from '../sim/uistate';
import { consumePersistedCamera, type StorageLike } from '../sim/cameraStash';
import { applyStashedCameraToView } from '../sim/cameraApply';
import { buildingRef, buildingRefLabel } from '../sim/refs';
import { useBusy } from './Busy';
import { HelpOverlay } from './HelpOverlay';
import { AffordabilityConfirm } from './AffordabilityConfirm';
import { evaluatePlacementBatch, type PendingBatchPlacement } from './placementGate';
import { makeKeydownHandler } from '../sim/keyhandler';
import type { Building, ZoneKind, TaxRates } from '../sim/types';
import type { Spec } from '../sim/data';
import { fmtMoney, fmtNum, formatPower } from '../sim/utils';
import { buildingProfile, specClassLabel, buildingCopyPayload, type ProfileLine } from '../sim/profile';
import { useBlockingOverlay, useEscapeKey } from './overlayManager';
import { BLOCKING_OVERLAY_ID, BLOCKING_OVERLAY_RANK } from './overlayLayers';
import {
  formatBuildingCount,
  DEMAND_FIX_SERVICE_LABELS,
  worstDemandFix,
  demandFixMessage,
  zoneDemandFix,
  zoneDemandMessage,
} from './demandFixUi';

const MIN_ZOOM = 1;
const MAX_ZOOM = 48;

interface View {
  zoom: number;
  cx: number;
  cy: number;
}

function clampView(v: View, w: number, h: number): View {
  const fit = Math.min(w / MAP_W, h / MAP_H);
  const s = fit * v.zoom;
  if (s <= 0 || w <= 0 || h <= 0) return v;
  const hw = w / (2 * s);
  const hh = h / (2 * s);
  const cx = MAP_W <= 2 * hw ? MAP_W / 2 : Math.min(Math.max(v.cx, hw), MAP_W - hw);
  const cy = MAP_H <= 2 * hh ? MAP_H / 2 : Math.min(Math.max(v.cy, hh), MAP_H - hh);
  return { zoom: v.zoom, cx, cy };
}

/**
 * BUG b2d31bc7 FIX 5 — overlay building-subset cache. The water/power/line
 * overlay passes below (draw effect) each used to `for (const b of
 * state.buildings)` and filter down to their own tiny subset (water tiles,
 * pylons, line-class tiles) EVERY redraw — a full O(buildings) scan per
 * overlay, per frame, on top of the 5-6 other full passes the draw effect
 * already makes. Memoised on the buildings array reference (immutable per
 * tick — same idiom as data.ts's roadTileSetOf/occupiedSet caches), one
 * shared classification pass replaces three separate ones, and repeated
 * redraws of an unchanged city (panning/zooming/hovering — no sim tick) hit
 * the cache instead of re-scanning every building again.
 */
interface OverlayBuildingSubsets {
  water: Building[];
  pylonIds: Set<number>;
  pylons: Building[];
  lineSpecs: Building[];
}
const overlaySubsetsCache = new WeakMap<object, OverlayBuildingSubsets>();
function overlaySubsetsOf(buildings: Building[]): OverlayBuildingSubsets {
  const cached = overlaySubsetsCache.get(buildings);
  if (cached) return cached;
  const water: Building[] = [];
  const pylonIds = new Set<number>();
  const pylons: Building[] = [];
  const lineSpecs: Building[] = [];
  for (const b of buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue;
    if (sp.kind === 'water') water.push(b);
    if (sp.kind === 'pylon') {
      pylonIds.add(b.id);
      pylons.push(b);
    }
    if (isLineSpec(sp)) lineSpecs.push(b);
  }
  const result: OverlayBuildingSubsets = { water, pylonIds, pylons, lineSpecs };
  overlaySubsetsCache.set(buildings, result);
  return result;
}

export function MapView() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [selected, setSelected] = useState<Building | null>(null);
  const [hover, setHover] = useState<{ x: number; y: number } | null>(null);
  const [view, setView] = useState<View>({ zoom: 2.2, cx: 165, cy: 76 });
  const [showWater, setShowWater] = useState(false);
  const [showPower, setShowPower] = useState(false);
  // FEAT-1972079903: per-building reference-id overlay toggle. UI-only, default
  // OFF — component-local like showWater/showPower, deliberately NOT in SimState
  // or the journal (it never affects the sim; genesis-replay stays deterministic).
  const [showRefs, setShowRefs] = useState(false);
  // FEAT-1972079902 rail-inc1: line-saturation overlay toggle. UI-only, default
  // OFF — component-local like showWater/showPower, never in SimState/journal.
  const [showLines, setShowLines] = useState(false);
  // FEAT-1972079861: Help overlay toggle. UI-only, component-local state.
  const [helpOpen, setHelpOpen] = useState(false);
  // BUG-652 follow-up, ROUND r3+r4 (2026-09-04): component-local ONLY — never
  // SimState, never journaled (see AffordabilityConfirm.tsx/placementGate.ts's
  // own headers for the full round r2/r4 finding this replaces). ONE pending
  // slot shared by every UI dispatch path in THIS component (single build
  // click, drag-paint flush, clone-paste stampRegion, the advisor's
  // resolveDemand prompt) — placementGate.ts's evaluatePlacementBatch() is
  // the single seam all of them call before dispatching.
  const [pendingAfford, setPendingAfford] = useState<PendingBatchPlacement | null>(null);
  const [cloneSelection, setCloneSelection] = useState<{ sx: number; sy: number; ex: number; ey: number } | null>(null);
  // FEAT-1972079910 inc1: road tracker state. Tracks anchor point and current path
  // during a road-placement drag. The preview renders the path; pointerup commits.
  const [roadTracker, setRoadTracker] = useState<{
    anchorX: number;
    anchorY: number;
    currentPath: Tile[];
    totalCost: number;
  } | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const panRef = useRef<{ sx: number; sy: number; cx: number; cy: number; moved: boolean; btn: number } | null>(null);
  const paintRef = useRef(false);
  const lastPaintRef = useRef<string | null>(null);
  // BUG b2d31bc7 FIX 3: build-mode drag-paint buffer. The FIRST tile of a
  // build-mode drag still dispatches an ordinary 'place' immediately at
  // pointerdown (unchanged single-click behaviour — a plain click never
  // touches this buffer at all). Every SUBSEQUENT tile touched while the
  // pointer is held down and moving is buffered here instead of dispatching
  // per-tile, and the whole buffer commits as ONE atomic 'placeMany' action
  // on pointerup — mirrors placeRoadPath's atomic-dispatch pattern, turning
  // an N-tile drag into 2 reducer round-trips (1 place + 1 placeMany) instead
  // of N.
  const dragTilesRef = useRef<{ x: number; y: number }[]>([]);
  const dragSpecRef = useRef<string | null>(null);
  const selectionAnchorRef = useRef<{ x: number; y: number } | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });

  // Consolidator marching-ants overlay (see the CONSOLIDATOR ruling block
  // further down this file, BOW item 2326609761 increment 2, for the full
  // rationale): the dashed-border animation counter. RENDER-ONLY — never sim
  // state, never read by any reducer/journal/replay path (GR#21's
  // determinism boundary is "does this affect what the city becomes", and
  // this affects only pixels). Advanced once per animation frame by the
  // dedicated rAF effect below, independent of the state-driven draw
  // effect's own redraw cadence (so the ants keep marching even while the
  // sim is paused/idle).
  const consolidatorAntsOffsetRef = useRef(0);
  // Rebuilt every time the main (state-driven) draw effect runs, so it always
  // closes over the CURRENT ctx/geom/state; invoked every animation frame by
  // the rAF effect so the highlighted box redraws with a fresh dash offset
  // without needing the whole map to redraw 60x/sec.
  const drawConsolidatorOverlayRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const r = entries[0].contentRect;
      setSize({ w: r.width, h: r.height });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // BUG-622 (P1, 2026-09-03): REMOVED the 20fps `setFrame` repaint-pump timer
  // that used to sit here. Investigation confirmed it drove ZERO pixels — its
  // only two uses anywhere in this file were `void frame;` (line below, a
  // no-op) and its own entry in the draw effect's dependency array. Every
  // actually-animated element already reads from a real, already-tracked
  // dependency: the disconnected-road flash and the train positions are both
  // pure functions of `state.tick` (GR#21 — see the trains comment a few
  // hundred lines down: "the 50ms `frame` repaint only redraws the SAME
  // positions [...] a repaint pump, never a position source"), camera moves
  // go through `view`, hover/selection through their own state. So this timer
  // forced the ENTIRE draw effect body — every building, every overlay full-
  // array pass — to re-run 20 times per SECOND, forever, regardless of
  // whether the sim ticked, the camera moved, or anything else changed.
  //
  // MEASURED IMPACT (BUG-622 profiling, 13k-building/1.4M-population fixture,
  // tmp-profile/drawloop-bench.mjs): the per-building draw-loop JS alone
  // (isOnline + blockOccupancy + utilisationOf for every building) measured
  // ~13,900ms for ONE pass at this scale — data.ts's blockOccupancy()/
  // utilisationOf() each call the UNMEMOIZED O(buildings) residentsCapacity()/
  // totalJobs() aggregate scans fresh, per building, making the per-building
  // draw loop O(buildings^2) (a SEPARATE finding reported to the wage lane,
  // which owns data.ts, for a residentsCapacity()/totalJobs()/powerStats()
  // memoisation fix mirroring this file's own overlaySubsetsOf() WeakMap
  // cache). At 20fps that O(n^2) cost was being paid TWENTY TIMES A SECOND
  // regardless of sim activity — removing the forced 20fps re-run does not
  // fix the O(n^2) itself, but it stops multiplying it by 20x/sec while the
  // player is doing nothing (which is nearly always — Aaron's report was
  // "each game day takes ~2 minutes", i.e. the sim is mostly idle/waiting
  // between ticks), collapsing the dominant real-world cost even before the
  // data.ts fix lands. See test/attack-bug622-frame-pump.test.mjs.
  //
  // BUG-630 follow-up (2026-09-03): the data.ts memoisation fix landed —
  // buildingDisplayStates(state) computes isOnline/blockOccupancy/
  // utilisationOf/densityTier for every building in ONE pass, memoised on the
  // `state` object identity (memoOnState). The draw loop below now does one
  // Map lookup per building instead of four SSOT function calls; a redraw
  // triggered by camera pan/zoom alone (same `state`, no tick advanced) hits
  // the memo and pays only the lookups. See test/attack-bug630-display-state.test.mjs.

  // FEAT-1972079897 inc2 RE-APPLY: on mount, restore the camera the player was
  // looking at before a rebuild reload. cameraStash persisted it to localStorage
  // (read-once); consumePersistedCamera returns the stashed {zoom, cx, cy} or
  // null (missing / corrupt / non-finite already rejected there). A real camera
  // re-homes the view so the map lands where it was — no snap-back to start. No
  // stash → do nothing; the default start view stands. UI-only, deterministic:
  // the camera never touches SimState or the journal. Runs exactly once ([]).
  useEffect(() => {
    let storage: StorageLike | null = null;
    try {
      storage = typeof window !== 'undefined' ? window.localStorage : null;
    } catch {
      // localStorage getter can throw in sandboxed frames — a lost restore is
      // cosmetic; fall through to the default view.
      storage = null;
    }
    const cam = consumePersistedCamera(storage);
    if (cam) setView((v) => applyStashedCameraToView(v, cam));
  }, []);

  // FEAT-1972079886: publish camera / selection / layer state to the debug
  // mailbox on every change, so the debug.json capture can report what the
  // player is looking at. Write-only — nothing here re-renders off it.
  useEffect(() => {
    publishMapUi({
      view: { zoom: view.zoom, cx: view.cx, cy: view.cy },
      selectedBuildingId: selected?.id ?? null,
      showWater,
      showPower,
    });
  }, [view, selected, showWater, showPower]);

  const geom = useMemo(() => {
    if (size.w <= 0 || size.h <= 0) return { s: 0, ox: 0, oy: 0 };
    const s = Math.min(size.w / MAP_W, size.h / MAP_H) * view.zoom;
    return { s, ox: size.w / 2 - view.cx * s, oy: size.h / 2 - view.cy * s };
  }, [size, view]);

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const rect = cv.getBoundingClientRect();
      const px = e.clientX - rect.left;
      const py = e.clientY - rect.top;
      setView((v) => {
        const nz = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.zoom * Math.exp(-e.deltaY * 0.0016)));
        const s = Math.min(size.w / MAP_W, size.h / MAP_H) * v.zoom;
        if (s <= 0) return v;
        const ox = size.w / 2 - v.cx * s;
        const oy = size.h / 2 - v.cy * s;
        const tx = (px - ox) / s;
        const ty = (py - oy) / s;
        const ns = Math.min(size.w / MAP_W, size.h / MAP_H) * nz;
        const nx = (size.w / 2 - px) / ns + tx;
        const ny = (size.h / 2 - py) / ns + ty;
        return clampView({ zoom: nz, cx: nx, cy: ny }, size.w, size.h);
      });
    };
    cv.addEventListener('wheel', onWheel, { passive: false });
    return () => cv.removeEventListener('wheel', onWheel);
  }, [size]);

  useEffect(() => {
    const cv = canvasRef.current;
    if (!cv || geom.s <= 0) return;
    const dpr = window.devicePixelRatio || 1;
    cv.width = Math.round(size.w * dpr);
    cv.height = Math.round(size.h * dpr);
    const ctx = cv.getContext('2d');
    if (!ctx) return;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, size.w, size.h);

    const step = geom.s > 8 ? 1 : geom.s > 3 ? 5 : 10;
    ctx.strokeStyle = 'rgba(58, 66, 76, 0.3)';
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = Math.floor((0 - geom.ox) / geom.s / step) * step; x <= MAP_W; x += step) {
      const px = Math.round(geom.ox + x * geom.s) + 0.5;
      ctx.moveTo(px, Math.max(geom.oy, 0));
      ctx.lineTo(px, geom.oy + MAP_H * geom.s);
    }
    for (let y = Math.floor((0 - geom.oy) / geom.s / step) * step; y <= MAP_H; y += step) {
      const py = Math.round(geom.oy + y * geom.s) + 0.5;
      ctx.moveTo(Math.max(geom.ox, 0), py);
      ctx.lineTo(geom.ox + MAP_W * geom.s, py);
    }
    ctx.stroke();

    ctx.fillStyle = 'rgba(144, 153, 166, 0.85)';
    ctx.font = '9px ui-monospace, Consolas, monospace';
    let lx = step;
    while (lx * geom.s < 44) lx *= 2;
    ctx.textAlign = 'center';
    for (let x = 0; x <= MAP_W; x += lx) {
      const px = geom.ox + x * geom.s + geom.s / 2;
      if (px < 14 || px > size.w - 4) continue;
      ctx.fillText(String(x + 1), px, 11);
    }
    const bandPx = ROW_BAND * geom.s;
    const stride = Math.max(1, Math.ceil(16 / bandPx));
    ctx.textAlign = 'left';
    for (let b = 0; b < 26; b++) {
      if (b % stride) continue;
      const py = geom.oy + b * bandPx + bandPx / 2 + 3;
      if (py < 16 || py > size.h - 4) continue;
      ctx.fillText(yLabel(b * ROW_BAND), 4, py);
    }
    ctx.textAlign = 'start';

    // BUG-659 (P0, 2026-09-04): Aaron's 49,174-building dogfood city stalled
    // the map 6.1s per repaint even though the sim itself (engine tick +
    // every derivation) sums to well under 700ms — the render path was doing
    // THREE full unconditional O(buildings) passes every frame (this main
    // loop, the disconnected-road-flash pass below, and the station-dot
    // pass) regardless of camera position. `visibleBuildings` is the exact
    // set of buildings whose real footprint (footprintOf — a grown building
    // can occupy more tiles than its spec's base w/h) intersects the current
    // viewport, computed via a cached spatial grid (viewportCull.ts) rather
    // than a linear scan+filter, so panning/zooming a redraw with the SAME
    // `state.buildings` reference reuses the grid instead of rebucketing.
    // See viewportCull.ts's header for the full correctness contract (never
    // a superset that drops the culling's perf value, never — and this is
    // the non-negotiable part — a subset that clips something that should
    // be on screen) and test/attack-bug659-viewport-cull.test.mjs for the
    // proof.
    const viewportRect = viewportTileRect(geom, size);
    const visibleBuildings = visibleBuildingsOf(state.buildings, viewportRect);
    const displayStates = buildingDisplayStates(state);
    for (const b of visibleBuildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      const ds = displayStates.get(b.id);
      // ds is only absent when SPECS[b.spec] is undefined (buildingDisplayStates'
      // own guard) — already ruled out by the `if (!sp) continue` above, so this
      // is unreachable in practice; the fallback keeps the loop fail-soft (GR#1)
      // rather than throwing on an unexpected cache miss.
      const online = ds ? ds.online : isOnline(state, b);
      const px = geom.ox + b.x * geom.s;
      const py = geom.oy + b.y * geom.s;
      // F5 (independent round REJECT, 2026-09-03): a building that has
      // scaled OUT (FEAT-2326609740) draws bigger than its spec's base
      // rect — always read the building's OWN current footprint.
      const { w: fpW, h: fpH } = footprintOf(b, sp);
      const pw = fpW * geom.s;
      const ph = fpH * geom.s;
      const baseAlpha = b.id === state.movingId ? 0.6 : online ? 1 : 0.45;
      ctx.globalAlpha = baseAlpha;
      const rx = px + 0.5;
      const ry = py + 0.5;
      const rw = Math.max(pw - 1, 1.5);
      const rh = Math.max(ph - 1, 1.5);
      // FEAT-1972079882 occupancy fill: null => full colour; else draw a dim
      // empty underlay and fill only the bottom `occ` fraction at full colour
      // (a half-occupied block shows a half-height fill, growing bottom-up).
      const occ = online ? (ds ? ds.occupancy : blockOccupancy(state, b)) : null;
      ctx.fillStyle = sp.color;
      if (occ == null) {
        ctx.fillRect(rx, ry, rw, rh);
      } else {
        ctx.globalAlpha = baseAlpha * 0.28;
        ctx.fillRect(rx, ry, rw, rh);
        ctx.globalAlpha = baseAlpha;
        const fh = rh * occ;
        if (fh > 0) ctx.fillRect(rx, ry + rh - fh, rw, fh);
      }

      // FEAT-1972079855 map utilisation cue: thin edge bar at bottom showing
      // per-building utilisation for non-residential kinds. Color-coded:
      // green (good 0-30%), amber (medium 30-70%), red (over 70%).
      // Residential skips this (occupancy fill above is the cue).
      // Null-basis kinds don't render (util returns null).
      if (sp.kind !== 'residential' && online && geom.s > 3) {
        const util = ds ? ds.utilisation : utilisationOf(state, b);
        if (util !== null) {
          const barH = Math.max(2, geom.s * 0.4);
          // ⚠ BALANCE-NUMBER PLACEHOLDER (Aaron 2026-08-13 regime): the 0.3/0.7
          // thresholds and green/amber/red hazard semantics await the balance
          // pass. Hazard reading (red = strained) fits services; a full shop is
          // also "red" — per-kind semantics is an open design question for Aaron.
          const barCol = util.ratio < 0.3 ? '#3fb950' : util.ratio < 0.7 ? '#e3b341' : '#ff7b72';
          ctx.globalAlpha = baseAlpha * 0.8;
          ctx.fillStyle = barCol;
          ctx.fillRect(rx, ry + rh - barH, rw, barH);
        }
      }

      if (!online && geom.s > 3) {
        ctx.strokeStyle = 'rgba(255,255,255,0.7)';
        ctx.lineWidth = 1;
        ctx.beginPath();
        for (let o = -ph; o < pw; o += 6) {
          ctx.moveTo(px + o, py);
          ctx.lineTo(px + o + ph, py + ph);
        }
        ctx.stroke();
      }
      if ((fpW > 1 || fpH > 1) && geom.s > 4) {
        ctx.strokeStyle = 'rgba(15, 18, 22, 0.55)';
        ctx.lineWidth = 1;
        ctx.strokeRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      // FEAT-1972079882 density/level tier border: colour the block outline by
      // its tier (grey→blue→gold). Only zone blocks carry a tier; drawn when the
      // block is big enough on screen to read the border.
      if (sp.category === 'zones' && geom.s > 3) {
        ctx.globalAlpha = baseAlpha;
        ctx.strokeStyle = TIER_COLORS[ds ? ds.tier : densityTier(sp)];
        ctx.lineWidth = geom.s > 8 ? 2 : 1.25;
        ctx.strokeRect(rx, ry, rw, rh);
      }
      if (selected && b.id === selected.id) {
        ctx.globalAlpha = 1;
        ctx.strokeStyle = '#ffffff';
        ctx.lineWidth = 2;
        ctx.strokeRect(px - 1, py - 1, pw + 2, ph + 2);
      }
      // FEAT-1972079903: reference-id overlay — small text at the lower-left of
      // the footprint so a player can report "re building 44". Deterministic
      // (id-derived, no clock/random). Hidden at very low zoom where it would be
      // unreadable — that's acceptable; the info panel still carries the ref.
      if (showRefs && geom.s > 5) {
        const label = buildingRef(b);
        ctx.globalAlpha = 1;
        ctx.font = '9px ui-monospace, Consolas, monospace';
        ctx.textAlign = 'left';
        ctx.textBaseline = 'alphabetic';
        const tx = px + 2;
        const ty = py + ph - 2.5;
        ctx.fillStyle = 'rgba(15, 18, 22, 0.72)';
        const tw = ctx.measureText(label).width;
        ctx.fillRect(tx - 1, ty - 8.5, tw + 2, 10);
        ctx.fillStyle = 'rgba(240, 246, 252, 0.95)';
        ctx.fillText(label, tx, ty);
        ctx.textAlign = 'start';
      }
    }
    ctx.globalAlpha = 1;

    // FEAT-1972079891 inc1 (AC-6) — DISCONNECTED-ROAD FLASH. Any drivable-road tile
    // NOT in the connected network pulses so the player can see which orphan road
    // needs joining up. Rendered AFTER the building loop, on top of the road tile.
    // Render-only + deterministic: the pulse is a pure function of state.tick (NOT
    // Date.now — GR#21), and it NEVER feeds a gate. ⚠ PLACEHOLDER (DD2, flag Aaron):
    // colour #ffd166 (warn yellow) + the sinusoidal cadence below.
    {
      const connected = new Set(state.roadConnectivity?.connectedRoadTiles ?? []);
      const FLASH_COLOR = '#ffd166'; // PLACEHOLDER (DD2)
      // Deterministic pulse from the sim tick (per AC-6 placeholder formula).
      const alpha = 0.5 + 0.3 * Math.sin(((state.tick * SPEED_MS[state.speed]) / 500) * Math.PI * 2);
      // BUG-659: an off-screen disconnected road still flashes, just not on
      // screen — visibleBuildings is the correct set to scan here too.
      for (const b of visibleBuildings) {
        const sp = SPECS[b.spec];
        if (!sp || !isRoadSpec(sp)) continue;
        if (connected.has(`${b.x},${b.y}`)) continue; // connected road — no flash.
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        ctx.globalAlpha = alpha;
        ctx.fillStyle = FLASH_COLOR;
        ctx.fillRect(px + 0.5, py + 0.5, sp.w * geom.s - 1, sp.h * geom.s - 1);
      }
      ctx.globalAlpha = 1;
    }

    // optional water layer: service radii, abstraction/discharge pipes
    if (showWater) {
      for (const b of overlaySubsetsOf(state.buildings).water) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        const tier = state.pipeTier[b.id] ?? 0;
        const cxp = geom.ox + (b.x + sp.w / 2) * geom.s;
        const cyp = geom.oy + (b.y + sp.h / 2) * geom.s;
        const cleanTag = sp.tag === 'clean';
        const col = cleanTag ? '#39c5cf' : '#8fbf7f';
        ctx.beginPath();
        ctx.arc(cxp, cyp, (12 + tier * 4) * geom.s, 0, Math.PI * 2);
        ctx.strokeStyle = col;
        ctx.lineWidth = 1.2;
        if (!cleanTag) ctx.setLineDash([6, 4]);
        ctx.stroke();
        ctx.setLineDash([]);
        const stubLen = 10 * geom.s;
        const dir = cleanTag ? -1 : 1;
        ctx.strokeStyle = col;
        ctx.lineWidth = 1.5 + tier * 1.5;
        ctx.beginPath();
        ctx.moveTo(cxp, cyp);
        ctx.lineTo(cxp, cyp + dir * stubLen);
        ctx.stroke();
        ctx.fillStyle = col;
        const m = Math.max(geom.s * 0.8, 3);
        ctx.fillRect(cxp - m / 2, cyp + dir * stubLen - m / 2, m, m);
      }
    }

    // FEAT-1972079851: Power overlay — colour power infrastructure by class
    // (currently only localGrid/pylons are real; superGrid/HVDC forward-declared).
    // Dim the rest like water does: base alpha 0.4, full saturation on power tiles.
    if (showPower) {
      const powerColorMap = new Map(POWER_LINES.map((pc) => [pc.id, pc.color]));
      // BUG b2d31bc7 FIX 5: pylonIds/pylons come from the shared cached
      // classification (overlaySubsetsOf) instead of a fresh full-buildings
      // scan every redraw.
      const { pylonIds, pylons } = overlaySubsetsOf(state.buildings);
      // Dim pass: all non-power infrastructure at 0.4× alpha, restricted to
      // the visible set (BUG-659) — no longer pays a SEPARATE full-city scan
      // just to build pylonIds first, AND no longer dims off-screen buildings
      // nobody will see.
      for (const b of visibleBuildings) {
        const sp = SPECS[b.spec];
        if (!sp || pylonIds.has(b.id)) continue;
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        // F5: pow_nuke (the reactor ladder) can scale OUT — draw its real footprint.
        const { w: dimW, h: dimH } = footprintOf(b, sp);
        const pw = dimW * geom.s;
        const ph = dimH * geom.s;
        ctx.globalAlpha = 0.4;
        ctx.fillStyle = sp.color;
        ctx.fillRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      ctx.globalAlpha = 1;
      // Full-saturation pass: power infrastructure at native colour + full
      // alpha — iterates only the cached pylon subset, not the whole city.
      for (const b of pylons) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        const { w: satW, h: satH } = footprintOf(b, sp);
        const pw = satW * geom.s;
        const ph = satH * geom.s;
        ctx.fillStyle = powerColorMap.get('localGrid') || sp.color;
        ctx.fillRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      ctx.globalAlpha = 1;
    }

    // FEAT-1972079902 rail-inc1: line-saturation overlay. Tints every LINE tile
    // (road/m20/rail/hs1) by how loaded its line class is. Colour reuses the
    // BUG-425 surplus-vs-shortfall split: over-capacity → danger red; within
    // capacity → the "done" green, alpha scaled by saturation so a busy-but-OK
    // line reads bright green and an idle line reads faint. Pure/deterministic —
    // derived from lineUsageOf(state), never from Date.now.
    if (showLines) {
      const OK = '#3fb950'; // --done (surplus / within capacity)
      const HOT = '#ff7b72'; // --danger (over capacity / shortfall)
      const satBySpec = new Map<string, { saturation: number; over: boolean }>();
      for (const lu of lineUsageOf(state)) {
        satBySpec.set(lu.spec, { saturation: lu.saturation, over: lu.overCapacity });
      }
      for (const b of overlaySubsetsOf(state.buildings).lineSpecs) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        const info = satBySpec.get(b.spec);
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        const pw = sp.w * geom.s;
        const ph = sp.h * geom.s;
        const sat = info?.saturation ?? 0;
        if (info?.over) {
          ctx.globalAlpha = 0.85;
          ctx.fillStyle = HOT;
        } else {
          ctx.globalAlpha = 0.25 + 0.6 * sat;
          ctx.fillStyle = OK;
        }
        ctx.fillRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      ctx.globalAlpha = 1;
    }

    // station connectivity dots (BUG-659: off-screen stations don't need a dot).
    const links = stationLinks(state);
    for (const b of visibleBuildings) {
      const sp = SPECS[b.spec];
      if (!sp || sp.kind !== 'station') continue;
      const px = geom.ox + (b.x + sp.w / 2) * geom.s;
      const py = geom.oy + (b.y + sp.h / 2) * geom.s;
      const on = links.connectedIds.has(b.id);
      ctx.beginPath();
      ctx.arc(px, py, Math.max(geom.s * 0.45, 2.5), 0, Math.PI * 2);
      ctx.fillStyle = on ? '#3fb950' : '#4a525c';
      ctx.fill();
      ctx.strokeStyle = '#14181d';
      ctx.lineWidth = 1;
      ctx.stroke();
    }

    // FEAT-1972079902 inc2: LIVE DETERMINISTIC TRAINS. Positions come solely from
    // trainPositions(geometry, demand, state.tick) — a pure function of the sim
    // tick (see trains.ts). NO Date.now / random: the old wall-clock preview glyph
    // it replaces is gone. Trains are quantised to whole ticks so a replay draws
    // byte-identical glyphs. BUG-622: this file used to also carry a 20fps
    // `frame` repaint-pump timer whose comment here claimed it kept these
    // positions "pumped" on screen between ticks — investigation proved that
    // was never true (positions are 100% a function of state.tick; the pump
    // redrew the exact same frame) and the pump has been removed (see its
    // former call site's BUG-622 comment above, near the `size` ResizeObserver
    // effect) — trains now redraw exactly when state.tick (or any other real
    // dependency) actually changes, never on a wall-clock heartbeat. Gated
    // behind the existing "Lines" overlay toggle, so it rides the same demand
    // read-out.
    if (showLines) {
      const railTiles: RailTile[] = [];
      const stationTiles: StationTile[] = [];
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp) continue;
        // FEAT-1972079910 inc3 (AC-7): isRailSpec includes both native rail ('rail', 'hs1')
        // and rd_railbridge (grade-separated crossing, rail runs through it).
        // AC-7 FIX: for rd_railbridge tiles, use bridgeOver to restore original line continuity
        // so buildRailGeometry groups the bridge with its original line class.
        if (isRailSpec(sp)) {
          const lineSpec = b.bridgeOver ?? b.spec; // Use original line if bridged, else spec
          railTiles.push({ spec: lineSpec, x: b.x, y: b.y });
        } else if (sp.kind === 'station') {
          for (let dx = 0; dx < sp.w; dx++)
            for (let dy = 0; dy < sp.h; dy++)
              stationTiles.push({ x: b.x + dx, y: b.y + dy });
        }
      }
      const geoms = buildRailGeometry(railTiles, stationTiles);
      const railDemand = lineUsageOf(state)
        .filter((l) => l.kind === 'rail')
        .map((l) => ({ spec: l.spec, saturation: l.saturation, overCapacity: l.overCapacity }));
      const lineTrains = trainPositions(geoms, railDemand, state.tick);
      for (const lt of lineTrains) {
        for (const tr of lt.trains) {
          const px = geom.ox + (tr.x + 0.5) * geom.s;
          const py = geom.oy + (tr.y + 0.5) * geom.s;
          // Size grows a little with load (colour/size by usage, #6).
          const ts = Math.max(geom.s * (1.1 + 0.6 * tr.saturation), 4);
          // BUG-425 split tokens ONLY: over capacity → danger red, else done green.
          const col = tr.bucket === 'hot' ? '#ff7b72' : '#3fb950';
          ctx.globalAlpha = 1;
          ctx.fillStyle = '#22272e';
          ctx.fillRect(px - ts / 2, py - ts / 2, ts, ts);
          const pad = Math.max(1, ts * 0.2);
          ctx.fillStyle = col;
          ctx.fillRect(px - ts / 2 + pad, py - ts / 2 + pad, ts - pad * 2, ts - pad * 2);
          // A stopped train gets a bright halt ring so the station pause reads.
          if (tr.stoppedAtStation) {
            ctx.strokeStyle = '#ffffff';
            ctx.lineWidth = 2;
            ctx.strokeRect(px - ts / 2 - 1, py - ts / 2 - 1, ts + 2, ts + 2);
          }
          ctx.strokeStyle = '#14181d';
          ctx.lineWidth = 1;
          ctx.strokeRect(px - ts / 2, py - ts / 2, ts, ts);
        }
      }
      ctx.globalAlpha = 1;
    }

    // FEAT-1972079910 inc1: road tracker preview. Display the path as ghost tiles
    // during a drag with running cost total. Renders only when tracker is active.
    if (roadTracker && state.tool.spec && geom.s > 2) {
      const sp = SPECS[state.tool.spec];
      if (sp) {
        ctx.globalAlpha = 0.35;
        ctx.fillStyle = sp.color;
        for (const tile of roadTracker.currentPath) {
          const px = geom.ox + tile.x * geom.s;
          const py = geom.oy + tile.y * geom.s;
          ctx.fillRect(px + 0.5, py + 0.5, sp.w * geom.s - 1, sp.h * geom.s - 1);
        }
        ctx.globalAlpha = 1;
      }
    }

    if (state.tool.mode === 'build' && state.tool.spec && hover && geom.s > 2 && !roadTracker) {
      const sp = SPECS[state.tool.spec];
      if (sp) {
        const ax = Math.min(hover.x, MAP_W - sp.w);
        const ay = Math.min(hover.y, MAP_H - sp.h);
        const blocked =
          !fits(occupiedSet(state), sp.w, sp.h, ax, ay) ||
          state.funds < placementCost(sp) ||
          !specUnlocked(state, sp);
        const px = geom.ox + ax * geom.s;
        const py = geom.oy + ay * geom.s;
        ctx.globalAlpha = 0.45;
        ctx.fillStyle = sp.color;
        ctx.fillRect(px + 0.5, py + 0.5, sp.w * geom.s - 1, sp.h * geom.s - 1);
        ctx.globalAlpha = 1;
        ctx.strokeStyle = blocked ? '#ff7b72' : '#ffffff';
        ctx.lineWidth = 1.5;
        ctx.strokeRect(px - 1, py - 1, sp.w * geom.s + 2, sp.h * geom.s + 2);
      }
    }

    // FEAT-1972079853: Clone-stamp ghost preview — render clipboard contents
    // at the cursor position when clipboard is set and clone mode is active.
    if (state.tool.mode === 'clone' && state.clipboard && hover && geom.s > 2) {
      const cb = state.clipboard;
      ctx.globalAlpha = 0.35;
      for (const item of cb.items) {
        const sp = SPECS[item.spec];
        if (!sp) continue;
        const ax = hover.x + item.dx;
        const ay = hover.y + item.dy;
        if (ax < 0 || ay < 0 || ax + sp.w > MAP_W || ay + sp.h > MAP_H) continue;
        const px = geom.ox + ax * geom.s;
        const py = geom.oy + ay * geom.s;
        ctx.fillStyle = sp.color;
        ctx.fillRect(px + 0.5, py + 0.5, sp.w * geom.s - 1, sp.h * geom.s - 1);
      }
      ctx.globalAlpha = 1;
    }

    // FEAT-2326609761 inc1 (Aaron: "let's draw a red box on the area"): the
    // consolidator's current-month section focus. Deliberately PURE ARITHMETIC
    // from section geometry (sectionOriginOf) — never a scan over
    // state.buildings. His city blocks 6.1s/tick at 49,174 buildings; a
    // per-frame O(buildings) overlay here would be an instant regression on
    // top of that. Cost is O(sections in this month's scope), bounded by
    // TOTAL_SECTIONS (476 at the ruled 800m size) — negligible even in the
    // "month 12 = whole map" case. Hidden entirely (draws nothing) while the
    // consolidator is OFF — same "no cost when off" contract the tab itself
    // observes (consolidatorTab.tsx).
    // FEAT-2326609761 inc2 (Aaron's ruling, 2026-09-03 addendum): the
    // month-12 whole-tile big-picture pass "always still runs regardless of
    // the player's chosen section size" or traversal mode — so the static
    // scope grid (every section this month's rotation covers) used to be
    // drawn unconditionally of consolidatorMode, exactly as inc1 landed it.
    // SUPERSEDED (Aaron ruling, 2026-09-04 — "multiple red boxes", display
    // confusion in glide mode): the monthly PASS still runs identically in
    // glide mode regardless (engine behaviour untouched, see
    // consolidatorGlide.ts/consolidator.ts — this block is display-only),
    // but the dim static scope-grid outline is now DRAWN only in
    // monthly-twelfth mode; glide mode (the default) shows ONLY the bright
    // marching-ants glide cursor box below, so the player never sees a wall
    // of overlapping dim boxes for sections the glide cursor already swept.
    const consolidatorMode = state.consolidatorMode ?? CONSOLIDATOR_MODE_DEFAULT;
    const consolidatorSectionTilesNow = sectionTilesOf(state);
    const consolidatorBoxOn = (state.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT) && geom.s > 0 && isConsolidatorBoxVisible();
    if (consolidatorBoxOn && consolidatorMode === 'monthly-twelfth') {
      const scope = monthlyScopeOf(state.tick);
      const focusKey = currentConsolidatorFocus();
      ctx.save();
      ctx.strokeStyle = 'rgba(255, 40, 40, 0.55)';
      ctx.lineWidth = Math.max(1, geom.s * 0.08);
      for (const key of scope.sectionKeys) {
        // This whole block only runs in monthly-twelfth mode now (2026-09-04
        // ruling above) — the ranked focus section is drawn separately
        // (solid+ants, below/via the rAF overlay) so it isn't double-outlined
        // here.
        if (key === focusKey) continue;
        const { x0, y0, w, h } = sectionOriginOf(key);
        ctx.strokeRect(geom.ox + x0 * geom.s, geom.oy + y0 * geom.s, w * geom.s, h * geom.s);
      }
      ctx.restore();
    }

    // The ANIMATED (marching-ants) highlighted box — GLIDE MODE (the
    // default, Aaron's 2026-09-04 ruling) shows the pure tick-derived glide
    // cursor (consolidatorGlide.ts, zero building/audit cost); monthly-
    // twelfth mode keeps inc1's ranked-top-opportunity mailbox highlight.
    // Captured as a closure over THIS render's ctx/geom/state so the
    // dedicated rAF effect (mount-once, below) can redraw just this box
    // every animation frame — with a fresh dash offset — without re-running
    // the whole (expensive, buildings-dependent) map draw 60x/sec.
    drawConsolidatorOverlayRef.current = () => {
      const cv = canvasRef.current;
      const c2 = cv?.getContext('2d');
      if (!cv || !c2 || !consolidatorBoxOn) return;
      let box: { x0: number; y0: number; w: number; h: number } | null = null;
      if (consolidatorMode === 'glide') {
        box = glideWindowForDay(state.tick, consolidatorSectionTilesNow);
      } else {
        const scope = monthlyScopeOf(state.tick);
        const focusKey = currentConsolidatorFocus();
        if (focusKey != null && scope.sectionKeys.includes(focusKey)) box = sectionOriginOf(focusKey);
      }
      if (!box) return;
      c2.save();
      c2.strokeStyle = '#ff2828';
      c2.lineWidth = Math.max(2, geom.s * 0.16);
      // Marching ants: a dashed stroke whose offset advances every frame
      // (consolidatorAntsOffsetRef, render-only — see its own doc comment).
      const dash = Math.max(4, geom.s * 0.5);
      const gap = Math.max(3, geom.s * 0.35);
      c2.setLineDash([dash, gap]);
      c2.lineDashOffset = -consolidatorAntsOffsetRef.current;
      c2.strokeRect(geom.ox + box.x0 * geom.s, geom.oy + box.y0 * geom.s, box.w * geom.s, box.h * geom.s);
      c2.setLineDash([]);
      c2.restore();
    };
    drawConsolidatorOverlayRef.current();
  }, [state.buildings, state.movingId, state.tool, state.funds, state.clipboard, state.tick, state.speed, state.roadConnectivity, state.consolidatorEnabled, state.consolidatorMode, state.consolidatorSectionMetres, selected, hover, showWater, showPower, showLines, showRefs, cloneSelection, roadTracker, geom, size]);

  // FEAT-2326609761 inc2 (Aaron's marching-ants ruling): a dedicated,
  // mount-once animation loop. Deliberately SEPARATE from the state-driven
  // draw effect above (which only re-runs when sim/UI state actually
  // changes) — the ants must keep moving every frame regardless, and
  // redrawing the ENTIRE map at 60fps purely to animate one dashed rectangle
  // would reintroduce the buildings-scan cost this overlay was designed to
  // avoid (see the inc1 comment on the static-scope block above). Reads
  // drawConsolidatorOverlayRef.current (rebuilt by the effect above on every
  // real redraw) so it always draws against fresh geometry/state.
  useEffect(() => {
    let raf = 0;
    const step = () => {
      consolidatorAntsOffsetRef.current = (consolidatorAntsOffsetRef.current + 1) % 10000;
      drawConsolidatorOverlayRef.current?.();
      raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, []);

  function tileFrom(clientX: number, clientY: number): { x: number; y: number } | null {
    const cv = canvasRef.current;
    if (!cv || geom.s <= 0) return null;
    const rect = cv.getBoundingClientRect();
    const x = Math.floor((clientX - rect.left - geom.ox) / geom.s);
    const y = Math.floor((clientY - rect.top - geom.oy) / geom.s);
    if (x < 0 || y < 0 || x >= MAP_W || y >= MAP_H) return null;
    return { x, y };
  }

  function act(t: { x: number; y: number }) {
    switch (state.tool.mode) {
      case 'build': {
        if (!state.tool.spec) break;
        const sp = SPECS[state.tool.spec];
        const ax = Math.min(t.x, MAP_W - sp.w);
        const ay = Math.min(t.y, MAP_H - sp.h);
        // BUG-652 follow-up, ROUND r3/r4 (2026-09-04): the affordability
        // check lives HERE, at the dispatch site — never in the reducer
        // (r2's REJECT findings F1/F2: a reducer-side gate silently drops
        // every pre-existing journalled 'place' entry on load, and its
        // notice had no UI reader at all). evaluatePlacementBatch() (the
        // SAME shared seam drag-paint/stampRegion/resolveDemand below all
        // use, r4's fix) is pure/read-only; dispatching only happens once
        // the player has actually confirmed (or never needed to).
        const gate = evaluatePlacementBatch(state, [state.tool.spec], () => {
          dispatch({ type: 'place', spec: state.tool.spec!, x: ax, y: ay });
        });
        if (gate) {
          setPendingAfford(gate);
          break;
        }
        dispatch({ type: 'place', spec: state.tool.spec, x: ax, y: ay });
        break;
      }
      case 'bulldoze': {
        // F5 (independent round REJECT, 2026-09-03): hit-test against the
        // building's REAL footprint (footprintOf) — a spec-only test made a
        // grown building's newly-claimed tiles un-bulldozable dead tiles.
        const hit = state.buildings.find((b) => {
          const sp = SPECS[b.spec];
          if (!sp) return false;
          const { w, h } = footprintOf(b, sp);
          return t.x >= b.x && t.x < b.x + w && t.y >= b.y && t.y < b.y + h;
        });
        if (hit) {
          dispatch({ type: 'bulldoze', x: t.x, y: t.y });
          if (selected?.id === hit.id) setSelected(null);
        }
        break;
      }
      case 'move':
        if (state.movingId != null) dispatch({ type: 'relocate', x: t.x, y: t.y });
        else {
          const hit = state.buildings.find((b) => {
            const sp = SPECS[b.spec];
            if (!sp) return false;
            const { w, h } = footprintOf(b, sp);
            return t.x >= b.x && t.x < b.x + w && t.y >= b.y && t.y < b.y + h;
          });
          if (hit) dispatch({ type: 'pickup', id: hit.id });
        }
        break;
      case 'select': {
        const hit = state.buildings.find((b) => {
          const sp = SPECS[b.spec];
          if (!sp) return false;
          const { w, h } = footprintOf(b, sp);
          return t.x >= b.x && t.x < b.x + w && t.y >= b.y && t.y < b.y + h;
        });
        setSelected(hit ?? null);
        break;
      }
    }
  }

  function nudgeZoom(factor: number) {
    setView((v) =>
      clampView(
        { ...v, zoom: Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.zoom * factor)) },
        size.w,
        size.h
      )
    );
  }

  function beginPan(e: React.PointerEvent<HTMLCanvasElement>) {
    panRef.current = { sx: e.clientX, sy: e.clientY, cx: view.cx, cy: view.cy, moved: false, btn: e.button };
    e.currentTarget.setPointerCapture(e.pointerId);
  }

  function cancelToSelect() {
    // FEAT-1972079910: Cancel road tracker if active.
    if (roadTracker) {
      setRoadTracker(null);
      return;
    }
    if (state.movingId != null) {
      dispatch({ type: 'cancelMove' });
      return;
    }
    if (state.tool.mode !== 'select') {
      dispatch({ type: 'tool', tool: { mode: 'select' } });
      return;
    }
    setSelected(null);
  }

  useEffect(() => {
    // FEAT-1972079861: Use the real handler factory (testable via spy injection).
    // The factory returns the ACTUAL event handler that listens to KEYBINDINGS.
    const isTextInput = (target: any): boolean => {
      return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement;
    };

    const onKey = makeKeydownHandler({
      dispatch,
      getState: () => state,
      setView,
      clampView,
      nudgeZoom,
      setShowWater,
      setShowPower,
      setShowLines,
      setShowRefs,
      setHelpOpen,
      helpOpen,
      view,
      size,
      MAP_W,
      MAP_H,
      MIN_ZOOM,
      isTextInput,
      cancelToSelect,
    });

    // Attach the real handler (from factory)
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [state.speed, helpOpen, size, state, view, dispatch]);

  const advisorContent = useMemo(() => {
    const s = state;
    const c = countByKind(s.buildings);
    const income = s.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
    const expense = s.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
    if (c.residential === 0 && s.population === 0)
      return {
        text: 'Welcome to the Hythe turning. Draw roads off the roundabout, then drag-paint housing alongside them.',
        go: undefined as (() => void) | undefined,
      };

    // FEAT-2326609728 inc2: the advisor's build prompt is now QUANTIFIED — it
    // names the count that fixes 50% of the outstanding shortfall (BUG-601,
    // Aaron ruling 2026-09-02 — was the whole shortfall +5% headroom until
    // then), reading demandFixPlan(state) (the SAME pure plan the DemandDock
    // "Fix (N)" buttons use — SSOT, GR#3) rather than pickAutoSpec()'s
    // single-building pick. pickAutoSpec() / serviceDemandOf() still drive
    // OTHER advisor branches below (unlock nagging) — DemandDock's own
    // Auto-build button ALSO now routes through this same demandFixPlan
    // sizing (BUG-601) rather than always placing one unit; this branch only
    // replaces the "here's what to build next" prompt.
    const fix = worstDemandFix(s);
    if (fix) {
      const sp = SPECS[fix.specId];
      if (sp) {
        const label = DEMAND_FIX_SERVICE_LABELS[fix.serviceKey] ?? fix.serviceKey;
        // BUG-587: "N x <Name>" (formatBuildingCount), matching the placeNotice
        // wording BUG-583 landed for the same reason — no English pluralisation
        // rule survives the full SPECS catalogue (e.g. "Water Works").
        const buildingLabel = formatBuildingCount(sp.name, fix.count);
        // BUG-606 (Aaron, 2026-09-03: "'citizens want shops' no help - how
        // much what type ... is this one hypermarket or 50?"): append the
        // SIZED, plan-derived detail (raw shortfall + the chosen pick's cost
        // + the next-best alternative when one exists) — demandFixMessage()
        // reads the SAME `fix` object this branch's click handler dispatches
        // against, so the two can never disagree (agreement-by-construction).
        // The original "Do you want to place N x Name? (fixes P% of X
        // demand)" prefix is kept in shape so it stays a real question with a
        // click affordance; the detail is additive. `AUTO_BUILD_DEMAND_PERCENT`
        // (GR#15) so this sentence can never hardcode a stale "50%" once
        // Aaron's 2026-09-03 superseding ruling changed the real fraction.
        return {
          text: `Do you want to place ${buildingLabel}? (fixes ${AUTO_BUILD_DEMAND_PERCENT}% of ${label} demand) — ${demandFixMessage(fix)}`,
          // BUG-652 follow-up, ROUND r4 (2026-09-04): resolveDemand builds
          // `fix.count` units of `fix.specId` in ONE dispatch — gated as the
          // WHOLE plan's aggregate wage bill, not per-unit (the finding that
          // the advisor's own "Fix" button was one of the bypassed batch
          // paths, "currently unreachable only by pricing accident").
          go: () => {
            const gate = evaluatePlacementBatch(
              state,
              Array(fix.count).fill(fix.specId),
              () => run(() => dispatch({ type: 'resolveDemand', serviceKey: fix.serviceKey }))
            );
            if (gate) setPendingAfford(gate);
            else run(() => dispatch({ type: 'resolveDemand', serviceKey: fix.serviceKey }));
          },
        };
      }
    }

    const links = stationLinks(s);
    if (c.residential > 0 && links.total > 0 && links.connectedIds.size === 0)
      return {
        text: 'Sanderling Station is idle — run one road tile up to it and citizens will auto-use the train, boosting growth and income.',
        go: undefined,
      };

    if (income - expense < 0 && s.funds < 12000)
      return { text: 'The treasury is bleeding — raise tax rates (right dock) or take the municipal loan.', go: undefined };

    // BUG-641 (Aaron, 2026-09-03, THIRD time on the same complaint: "'citizens
    // want shops' no help - how much what type a clue would be nice is this one
    // hypermarket or 50?"). BUG-606 sized the twelve COVERAGE services above but
    // left the three ZONE demands on these unsized legacy banners, so his literal
    // example — shops — still read "paint Commercial zones" with no quantity, no
    // type and no cost. zoneDemandFix() sizes a zone gap the same way
    // demandFixPlan() sizes a coverage gap (AUTO_BUILD_DEMAND_FRACTION of a
    // PHYSICAL jobs/residents shortfall, cheapest unlocked provider, alternative
    // named), and zoneDemandMessage() renders it in the identical BUG-606 shape
    // from that same item — agreement-by-construction, never a re-derivation.
    //
    // ORDERING: coverage first (this branch is only reached when worstDemandFix
    // returned null), deliberately NOT worstAnyDemandFix's combined ranking —
    // that compares a -100..100 zone INDEX against a coverage gap measured in
    // PEOPLE, so a trivial shops index can outrank a real hospital shortfall
    // (BUG-647). Keeping the two tiers separate makes the ranking question moot
    // here; wire the combined ranker only once BUG-647 gives it a common basis.
    const zoneFix = zoneDemandFix(s);
    if (zoneFix) return { text: zoneDemandMessage(zoneFix), go: undefined };

    const d = demandOf(s);
    if (d.residential > 40) return { text: 'High housing demand — paint more Residential zones.', go: undefined };
    if (d.commercial > 40) return { text: 'Citizens want shops — paint Commercial zones.', go: undefined };
    if (d.industrial > 40) return { text: 'Industry is demanded — freight jobs are waiting.', go: undefined };

    // BUG-392: positive demand = shortfall, so the WORST-covered service is
    // the highest value (the old surplus-positive index put it at the lowest).
    const worst = serviceDemandOf(s).sort((a, b) => b.value - a.value)[0];
    if (worst && worst.value > 30 && SPECS[worst.spec] && !specUnlocked(s, SPECS[worst.spec]))
      return {
        text: `${worst.label} is under-provided — the right structure unlocks at city level ${SPECS[worst.spec].unlock}.`,
        go: undefined,
      };

    if (s.population >= residentsCap(s.buildings) && c.residential > 0)
      return { text: 'Housing is full — zone more Residential to keep growing.', go: undefined };
    return { text: 'Steady hands. Watch Flow (left) and Rates (right) to steer the budget.', go: undefined };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.tick, state.buildings, state.funds, state.lastFlows, state.xp, state.policies, size.w, size.h]);

  return (
    <div className="map-wrap" ref={wrapRef}>
      <canvas
        ref={canvasRef}
        style={{ width: size.w, height: size.h }}
        className={`map-canvas cur-${state.tool.mode}`}
        onContextMenu={(e) => e.preventDefault()}
        onPointerDown={(e) => {
          // FEAT-1972079853: Clone mode drag-select (mutually exclusive with paint).
          if (state.tool.mode === 'clone' && e.button === 0) {
            const t = tileFrom(e.clientX, e.clientY);
            if (t) {
              setCloneSelection({ sx: t.x, sy: t.y, ex: t.x, ey: t.y });
              selectionAnchorRef.current = { x: t.x, y: t.y };
            }
            e.currentTarget.setPointerCapture(e.pointerId);
            return;
          }
          // FEAT-1972079910 inc1: road tracker activation. Anchor the start tile
          // when placing a road-like spec.
          if (state.tool.mode === 'build' && state.tool.spec && e.button === 0) {
            const sp = SPECS[state.tool.spec];
            if (sp && isRoadSpec(sp)) {
              const t = tileFrom(e.clientX, e.clientY);
              if (t) {
                setRoadTracker({
                  anchorX: t.x,
                  anchorY: t.y,
                  currentPath: [{ x: t.x, y: t.y }],
                  totalCost: placementCost(sp),
                });
                e.currentTarget.setPointerCapture(e.pointerId);
                return;
              }
            }
          }
          // Non-road build mode and other tool modes.
          if (state.tool.mode !== 'select' && state.tool.mode !== 'move' && e.button === 0) {
            paintRef.current = true;
            lastPaintRef.current = null;
            // BUG b2d31bc7 FIX 3: (re)arm the drag-batch buffer for a fresh
            // drag. Only 'build' mode with a (non-road — road uses its own
            // roadTracker path above) spec ever populates it.
            dragTilesRef.current = [];
            dragSpecRef.current = state.tool.mode === 'build' ? state.tool.spec ?? null : null;
            e.currentTarget.setPointerCapture(e.pointerId);
            const t = tileFrom(e.clientX, e.clientY);
            if (t) {
              lastPaintRef.current = `${t.x},${t.y}`;
              act(t);
            }
          } else {
            beginPan(e);
          }
        }}
        onPointerMove={(e) => {
          setHover(tileFrom(e.clientX, e.clientY));
          // FEAT-1972079910 inc1: road tracker path update. Compute path from anchor
          // to current cursor and display the preview.
          if (roadTracker) {
            const t = tileFrom(e.clientX, e.clientY);
            if (t) {
              const path = computePath(roadTracker.anchorX, roadTracker.anchorY, t.x, t.y);
              const sp = SPECS[state.tool.spec!];
              const cost = sp ? placementCost(sp) * path.length : 0;
              setRoadTracker((prev) =>
                prev
                  ? {
                      ...prev,
                      currentPath: path,
                      totalCost: cost,
                    }
                  : null
              );
            }
            return;
          }
          // FEAT-1972079853: Clone mode drag-select rectangle.
          if (cloneSelection && state.tool.mode === 'clone') {
            const t = tileFrom(e.clientX, e.clientY);
            if (t) {
              setCloneSelection((prev) => (prev ? { ...prev, ex: t.x, ey: t.y } : null));
            }
            return;
          }
          const p = panRef.current;
          if (p && geom.s > 0) {
            const dx = e.clientX - p.sx;
            const dy = e.clientY - p.sy;
            if (Math.abs(dx) + Math.abs(dy) > 4) p.moved = true;
            if (p.moved)
              setView((v) =>
                clampView({ ...v, cx: p.cx - dx / geom.s, cy: p.cy - dy / geom.s }, size.w, size.h)
              );
            return;
          }
          if (paintRef.current && state.tool.mode !== 'move') {
            const t = tileFrom(e.clientX, e.clientY);
            const k = t ? `${t.x},${t.y}` : null;
            if (t && k !== lastPaintRef.current) {
              lastPaintRef.current = k;
              // BUG b2d31bc7 FIX 3: build-mode tiles beyond the first (already
              // placed at pointerdown) go into the drag buffer instead of
              // dispatching immediately — flushed as one atomic 'placeMany'
              // on pointerup. Bulldoze/other paint-capable modes are
              // unaffected — they keep the original per-tile 'act' dispatch.
              if (state.tool.mode === 'build' && dragSpecRef.current) {
                dragTilesRef.current.push(t);
              } else {
                act(t);
              }
            }
          }
        }}
        onPointerUp={(e) => {
          // FEAT-1972079910 inc1: road tracker completion. Commit the full path
          // as a single atomic action. Single-click (path length 1) still places one tile.
          if (roadTracker && state.tool.spec) {
            if (roadTracker.currentPath.length > 0) {
              dispatch({
                type: 'placeRoadPath',
                spec: state.tool.spec,
                tiles: roadTracker.currentPath,
              });
            }
            setRoadTracker(null);
            return;
          }
          // FEAT-1972079853: Clone mode completion.
          if (cloneSelection && state.tool.mode === 'clone' && selectionAnchorRef.current) {
            const sel = cloneSelection;
            const minX = Math.min(sel.sx, sel.ex);
            const maxX = Math.max(sel.sx, sel.ex);
            const minY = Math.min(sel.sy, sel.ey);
            const maxY = Math.max(sel.sy, sel.ey);

            if (state.clipboard === null) {
              // Capture buildings in the selection rect into clipboard.
              const capturedItems: Array<{ spec: string; dx: number; dy: number }> = [];
              for (const b of state.buildings) {
                const sp = SPECS[b.spec];
                if (!sp) continue;
                // Check if building's footprint origin (top-left) is in the selection rect.
                if (b.x >= minX && b.x <= maxX && b.y >= minY && b.y <= maxY) {
                  const dx = b.x - minX;
                  const dy = b.y - minY;
                  capturedItems.push({ spec: b.spec, dx, dy });
                }
              }
              const clipboardRect = { w: maxX - minX + 1, h: maxY - minY + 1, items: capturedItems };
              dispatch({ type: 'setClipboard', clipboard: capturedItems.length > 0 ? clipboardRect : null });
            } else {
              // Stamp the clipboard at the selection anchor. BUG-652 follow-up,
              // ROUND r4 (2026-09-04): a multi-item clipboard is a BATCH — the
              // whole paste is gated as ONE aggregate confirmation (the
              // finding that a clone-paste bypassed the r3 gate entirely).
              const clip = state.clipboard;
              const anchorX = selectionAnchorRef.current.x;
              const anchorY = selectionAnchorRef.current.y;
              const gate = evaluatePlacementBatch(
                state,
                clip.items.map((it) => it.spec),
                () => dispatch({ type: 'stampRegion', clipboard: clip, x: anchorX, y: anchorY })
              );
              if (gate) setPendingAfford(gate);
              else dispatch({ type: 'stampRegion', clipboard: clip, x: anchorX, y: anchorY });
            }
            setCloneSelection(null);
            selectionAnchorRef.current = null;
          }
          // BUG b2d31bc7 FIX 3: commit the whole build-mode drag buffer as ONE
          // atomic 'placeMany' (the first tile of the drag already went
          // through 'place' at pointerdown; this covers everything after it).
          // Runs before the paint refs are reset below so it only fires for
          // an actual build-mode drag that populated the buffer. BUG-652
          // follow-up, ROUND r4: gated as ONE aggregate batch confirmation —
          // the finding that N drag-painted tiles bypassed the r3 gate
          // entirely (proven live: 3 Channel Tunnel Portals for 180% of
          // gross inflow, zero confirmation).
          if (dragTilesRef.current.length > 0 && dragSpecRef.current) {
            const tiles = dragTilesRef.current;
            const spec = dragSpecRef.current;
            const gate = evaluatePlacementBatch(
              state,
              tiles.map(() => spec),
              () => dispatch({ type: 'placeMany', spec, tiles })
            );
            if (gate) setPendingAfford(gate);
            else dispatch({ type: 'placeMany', spec, tiles });
          }
          dragTilesRef.current = [];
          dragSpecRef.current = null;
          const p = panRef.current;
          panRef.current = null;
          paintRef.current = false;
          lastPaintRef.current = null;
          if (p && !p.moved) {
            if (p.btn === 2) cancelToSelect();
            else if (p.btn === 0) {
              const t = tileFrom(e.clientX, e.clientY);
              if (t) act(t);
            }
          }
        }}
        onPointerLeave={() => setHover(null)}
      />
      <Compass />
      <span className="map-hint">wheel zoom · right-drag pan · left-drag paint · 1-9 pick · Esc cancel · Lines overlay shows live trains (green = headroom, red = over capacity)</span>
      <div
        className="tier-legend"
        title="Zone block border = density/level tier. Block fill height = percent occupied (residents vs capacity; workers vs jobs)."
      >
        <b>Density</b>
        <span><i className="tier-dot" style={{ background: TIER_COLORS[1] }} />1 low</span>
        <span><i className="tier-dot" style={{ background: TIER_COLORS[2] }} />2 med</span>
        <span><i className="tier-dot" style={{ background: TIER_COLORS[3] }} />3 high</span>
        <span className="tier-fill-note">fill = % occupied</span>
      </div>
      <LevelUpBanner />
      <MilestoneBanner />
      <PlaceNoticeBanner />
      <InsolvencyBanner />
      <AdministrationBanner />
      <SecondBailoutBanner />
      <InsolvencyPopup />
      <ForcedAssetSalesPanel />
      <DeclineScreen />
      <PlayModeBanner />
      <div
        className={`advisor${advisorContent.go ? ' clickable' : ''}`}
        onClick={advisorContent.go}
        title={advisorContent.go ? 'Click to place it now at the best site' : undefined}
      >
        {advisorContent.text}
        {advisorContent.go && <span className="adv-hint">click to place</span>}
      </div>
      {(state.tool.mode !== 'select' || state.movingId != null) && (
        <div className="tool-chip">
          <b>
            {state.movingId != null
              ? `Moving structure #${state.movingId}`
              : state.tool.mode === 'build' && state.tool.spec
                ? `Placing: ${SPECS[state.tool.spec]?.name}`
                : state.tool.mode === 'bulldoze'
                  ? 'Bulldozing'
                  : state.tool.mode === 'clone'
                    ? `Clone tool${state.clipboard ? ' (clipboard ready)' : ' (drag-select to capture)'}`
                    : 'Move tool'}
          </b>
          <span>Esc / right-click to let go</span>
        </div>
      )}
      <div className="map-zoom">
        <button className="btn tiny" title="Zoom out" onClick={() => nudgeZoom(1 / 1.5)}>
          −
        </button>
        <span className="mono">{view.zoom.toFixed(1)}×</span>
        <button className="btn tiny" title="Zoom in" onClick={() => nudgeZoom(1.5)}>
          +
        </button>
        <button
          className={`btn tiny${showWater ? ' active' : ''}`}
          title="Toggle water network layer: service radii, abstraction and discharge pipes"
          onClick={() => setShowWater((v) => !v)}
        >
          Water
        </button>
        <button
          className={`btn tiny${showPower ? ' active' : ''}`}
          title="Toggle power infrastructure overlay: highlights pylons and grid classes"
          onClick={() => setShowPower((v) => !v)}
        >
          Power
        </button>
        <button
          className={`btn tiny${showLines ? ' active' : ''}`}
          title="Toggle line-saturation overlay: colours road/rail lines by how loaded they are (green = headroom, red = over capacity)"
          onClick={() => setShowLines((v) => !v)}
        >
          Lines
        </button>
        <button
          className={`btn tiny${showRefs ? ' active' : ''}`}
          title="Toggle building reference ids on the map so you can report a bug by building number"
          onClick={() => setShowRefs((v) => !v)}
        >
          Refs
        </button>
        <button
          className="btn tiny"
          title="Show whole map"
          onClick={() => setView({ zoom: MIN_ZOOM, cx: MAP_W / 2, cy: MAP_H / 2 })}
        >
          All
        </button>
      </div>
      {hover && <span className="map-coord mono">{coordLabel(hover.x, hover.y)}</span>}
      {hover &&
        state.tool.mode === 'build' &&
        state.tool.spec &&
        (() => {
          const sp = SPECS[state.tool.spec];
          if (!sp) return null;
          let reason: string | null = null;
          const ax = Math.min(hover.x, MAP_W - sp.w);
          const ay = Math.min(hover.y, MAP_H - sp.h);
          if (!fits(occupiedSet(state), sp.w, sp.h, ax, ay))
            reason = `Needs a clear ${sp.w}×${sp.h} area`;
          else if (!specUnlocked(state, sp))
            reason = `Locked — reach city level ${sp.unlock}`;
          else if (state.funds < placementCost(sp)) reason = 'Insufficient funds';
          return reason ? (
            <span className="map-block">
              {reason}
            </span>
          ) : null;
        })()}
      {selected && (
        <BuildingCard
          building={selected}
          connected={stationLinks(state).connectedIds.has(selected.id)}
          showRefs={showRefs}
          onClose={() => setSelected(null)}
        />
      )}
      <HelpOverlay isOpen={helpOpen} onClose={() => setHelpOpen(false)} />
      {pendingAfford && (
        <AffordabilityConfirm
          message={pendingAfford.afford.message}
          onConfirm={() => {
            pendingAfford.commit();
            setPendingAfford(null);
          }}
          onCancel={() => setPendingAfford(null)}
        />
      )}
    </div>
  );

  function residentsCap(buildings: typeof state.buildings): number {
    let cap = 0;
    for (const b of buildings) {
      const sp = SPECS[b.spec];
      if (sp?.kind === 'residential') cap += sp.residents ?? 8;
    }
    return cap;
  }
}

// FEAT-1972079884 — dismissible level-up banner. Reads the reward notice the
// reducer stamped on state when experience crossed a new level, showing the
// cash injection (through fmtMoney) and what the level unlocked. Dismiss clears
// it; it fires exactly once because the reward is guarded by lastRewardedLevel.
function LevelUpBanner() {
  const { state, dispatch } = useSim();
  const n = state.notice;
  if (!n) return null;
  return (
    <div className="levelup-banner" role="status">
      <div className="levelup-head">
        <b>Level {n.level} reached</b>
        <button className="btn tiny" onClick={() => dispatch({ type: 'dismissNotice' })}>
          Dismiss
        </button>
      </div>
      {n.cash > 0 ? (
        <p className="levelup-cash">
          Cash injection <b>{fmtMoney(n.cash)}</b> granted.
        </p>
      ) : (
        <p className="levelup-cash">No cash injection this level.</p>
      )}
      <p className="levelup-unlocks">
        {n.unlocked.length > 0
          ? `Unlocked: ${n.unlocked.join(', ')}`
          : 'No new structures at this level — keep building.'}
      </p>
    </div>
  );
}

// FEAT-milestone-cash-rewards-2026-09-02 (Q100047b ruling B1) — dismissible
// milestone-reward banner. Mirrors LevelUpBanner exactly: reads the
// milestoneNotice the reducer stamped on state when a MILESTONES predicate
// was first observed met, showing the cash injection (through fmtMoney).
// Dismiss clears it; it fires exactly once per milestone because the reward
// is guarded by claimedMilestones (engine.ts's advance()).
function MilestoneBanner() {
  const { state, dispatch } = useSim();
  const n = state.milestoneNotice;
  if (!n) return null;
  return (
    <div className="levelup-banner milestone-banner" role="status">
      <div className="levelup-head">
        <b>Milestone reached: {n.label}</b>
        <button className="btn tiny" onClick={() => dispatch({ type: 'dismissMilestoneNotice' })}>
          Dismiss
        </button>
      </div>
      {n.cash > 0 ? (
        <p className="levelup-cash">
          Cash injection <b>{fmtMoney(n.cash)}</b> awarded.
        </p>
      ) : (
        <p className="levelup-cash">No cash injection for this milestone.</p>
      )}
    </div>
  );
}

// FEAT-1972079923 inc1 (AC-9 companion to BUG-396): renders the cannot-afford
// placement notice the reducer already stamps on state.placeNotice — the fix
// for BUG-396's silent-no-op complaint was never visible because nothing
// rendered this field. Auto-clears on the next successful place() (existing
// reducer behaviour); Dismiss lets the player acknowledge it explicitly too.
function PlaceNoticeBanner() {
  const { state, dispatch } = useSim();
  const msg = state.placeNotice;
  if (!msg) return null;
  return (
    <div className="place-notice-banner" role="alert">
      <span>{msg}</span>
      <button className="btn tiny" onClick={() => dispatch({ type: 'dismissPlaceNotice' })}>
        Dismiss
      </button>
    </div>
  );
}

// FEAT-1972079923 inc1 (AC-1): persistent status banner for the insolvency band.
// Solvent renders nothing. 'warning' gives advance notice before the crisis
// threshold is crossed (task point 3) so the eventual bailout is not a surprise;
// 'crisis' is the AC-1 banner text — it also covers the FIRST bailout (a plain
// `bailoutState` overlays nothing onto `insolvencyState`, so the first bailout
// still reads as the raw 'crisis' band; there is no separate first-bailout
// banner component, so this text IS the first-bailout banner).
function InsolvencyBanner() {
  const { state } = useSim();
  const band = state.insolvencyState ?? 'solvent';
  // BUG-501: 'administration', 'bailout_second' and 'decline' each have their
  // own dedicated overlay/banner (AdministrationBanner, SecondBailoutBanner,
  // DeclineScreen) — this banner must return null for ALL of them, never just
  // 'administration'. Before this fix, 'bailout_second' fell through to the
  // `crisis` check below (false, since band !== 'crisis' literally once
  // overlaid), rendering the FALSE 'warning' copy ("funds are approaching the
  // insolvency threshold ... before the IMF steps in") stacked on top of
  // SecondBailoutBanner at the same top-right anchor — a contradiction, since
  // the IMF is already two bailouts deep. GR#1: an unrecognised/overlaid band
  // must fall back to "render nothing" here, never to the generic warning text.
  if (band === 'solvent' || band === 'administration' || band === 'bailout_second' || band === 'decline') {
    return null;
  }
  const crisis = band === 'crisis';
  return (
    <div className={`insolvency-banner ${crisis ? 'crisis' : 'warning'}`} role="status">
      {crisis
        ? 'BAILOUT: You have 1 year to restore solvency. Sell assets or enter Administration.'
        : 'Treasury warning — funds are approaching the insolvency threshold. Raise revenue or cut spending before the IMF steps in.'}
    </div>
  );
}

// FEAT-1972079923 inc3 (AC-5, AC-7): the ADMINISTRATION MODE banner. Visible
// for the whole active administration window; shows the entry tick and ticks
// remaining until the AC-7 year-end re-evaluation (pure tick arithmetic, no
// wall-clock). The city REMAINS PLAYABLE while this is shown — nothing here
// stops the clock.
function AdministrationBanner() {
  const { state } = useSim();
  const admin = state.administrationState;
  if (!admin) return null;
  const ticksLeft = Math.max(0, admin.enteredAt + ADMINISTRATION_DURATION_TICKS - state.tick);
  return (
    <div className="insolvency-banner administration" role="status">
      ADMINISTRATION MODE — spending frozen to mandatory obligations since tick {admin.enteredAt}
      {' '}({ticksLeft} ticks until re-evaluation).
    </div>
  );
}

// FEAT-1972079923 inc1 (AC-8, scenario 1 only): the one-shot bailout-entry
// popup. Reads insolvencyPopup, which the reducer stamps EXACTLY ONCE — on the
// tick the band transitions into 'crisis' — so this never reappears on later
// ticks while still in crisis. "I understand" dismisses it via the
// dismissInsolvencyPopup action (UI-only, not journaled). The forced-sales
// list and the Administration button are inc2/3 deliverables, not built yet.
function InsolvencyPopup() {
  const { state, dispatch } = useSim();
  const popup = state.insolvencyPopup;
  // FEAT-2326609720 inc1: registers with the app-wide single-blocking-overlay
  // resolver (overlayManager.tsx) — even though engine.ts already force-clears
  // insolvencyPopup the tick declineState is set (BUG-497(1)), the resolver is
  // a structural second line of defence against the whole CLASS of co-mount
  // bug, not a re-fix of that one already-closed state-layer defect. Hooks
  // called unconditionally (rules-of-hooks) BEFORE the early return below.
  const isTop = useBlockingOverlay(BLOCKING_OVERLAY_ID.INSOLVENCY_POPUP, BLOCKING_OVERLAY_RANK.insolvencyPopup, !!popup);
  // AC-5/AC-13: Escape dismisses the popup exactly like "I understand" — it
  // does NOT resolve any bailout choice, only acknowledges the notice.
  useEscapeKey(isTop, () => dispatch({ type: 'dismissInsolvencyPopup' }));
  if (!popup || !isTop) return null;
  return (
    <div className="insolvency-popup-overlay" role="alertdialog" aria-modal="true">
      <div className="insolvency-popup">
        <button
          className="btn tiny insolvency-popup-close"
          aria-label="Dismiss (does not resolve the bailout)"
          title="Dismiss — same as “I understand” below (Esc also works)"
          onClick={() => dispatch({ type: 'dismissInsolvencyPopup' })}
        >
          ×
        </button>
        <h3>BAILOUT: 1 Game-Year Intervention</h3>
        <p>
          The treasury has crossed the insolvency threshold at tick {popup.enteredAt}
          {' '}(funds {fmtMoney(state.funds)}). Once in force you will need to:
        </p>
        <ul>
          <li>Sell city assets to reduce debt, or</li>
          <li>Enter Administration Mode — spending cut, one year to recover.</li>
        </ul>
        <p className="insolvency-popup-note">
          (Administration Mode lands in a later increment — for now, sell assets
          from the FORCED ASSET SALES panel below to restore solvency.)
        </p>
        <button className="btn" onClick={() => dispatch({ type: 'dismissInsolvencyPopup' })}>
          I understand
        </button>
      </div>
    </div>
  );
}

// FEAT-1972079923 inc2/inc4 (AC-2, AC-3, AC-4, AC-10): the FORCED ASSET SALES
// panel. Visible for the full duration of EITHER active bailout
// (state.bailoutState OR state.bailoutSecondState != null — inc4 reuses this
// panel for the auto-triggered second bailout, per the brief's "the forced-
// sales list + administration option reappear for the second bailout"),
// regardless of whether the InsolvencyPopup has been dismissed — the player
// needs the panel available throughout the bailout year, not just on entry.
// Lists sellable assets sorted by CAPITAL VALUE DESCENDING (Aaron's ruling:
// biggest first, "the stadium goes before the corner shop" — NOT construction
// order). Selling dispatches 'sellAsset', which atomically removes the
// building and credits the treasury the placeholder sale value (journaled
// through replay like any other action).
// BUG-498: the panel is year-pinned BY DESIGN (visibility clears at bailout
// year-end or on enterAdministration, see engine.ts) — that is not a trap,
// but the player still feels stuck with no way to get it off the screen
// mid-year. `dismissed` is PURE UI-local React state, never touches
// SimState/dispatch, so it cannot alter funds, bailoutState, or any
// journaled/conserved value (GR#3: no second source of truth for money).
// The effect below resets `dismissed` whenever `active.enteredAt` changes
// identity (a NEW bailout starting — first or second — after this one ended)
// so a dismissal of THIS bailout's panel never silently suppresses a LATER
// bailout's panel too.
function ForcedAssetSalesPanel() {
  const { state, dispatch } = useSim();
  const bailout = state.bailoutState;
  const bailoutSecond = state.bailoutSecondState;
  const active = bailout ?? bailoutSecond;
  const [dismissed, setDismissed] = useState(false);
  const activeEnteredAt = active?.enteredAt ?? null;
  useEffect(() => {
    setDismissed(false);
  }, [activeEnteredAt]);
  // FEAT-2326609720 inc1: single-blocking-overlay invariant — this is the
  // LOWEST-priority of the four known blocking candidates (Decline outranks
  // Insolvency Popup outranks Forced Asset Sales, per Aaron's ordering), so it
  // is structurally suppressed while either of those is up, even though its
  // own bailoutState/bailoutSecondState condition remains true underneath.
  const wantsToShow = !!active && !dismissed;
  const isTop = useBlockingOverlay(BLOCKING_OVERLAY_ID.FORCED_ASSET_SALES, BLOCKING_OVERLAY_RANK.forcedAssetSales, wantsToShow);
  // AC-4/AC-13: Escape dismisses the panel exactly like the × button — UI-only,
  // never touches bailoutState/funds (GR#3, mirrors the existing close button).
  useEscapeKey(isTop, () => setDismissed(true));
  if (!active) return null;
  if (dismissed) return null;
  if (!isTop) return null;
  const isSecond = bailout === null && bailoutSecond !== null;
  const duration = isSecond ? SECOND_BAILOUT_DURATION_TICKS : BAILOUT_DURATION_TICKS;
  const assets = forcedSaleAssets(state);
  const ticksLeft = Math.max(0, active.enteredAt + duration - state.tick);
  return (
    <div className="forced-asset-sales-panel" role="region" aria-label="Forced Asset Sales">
      <button
        className="btn tiny forced-asset-sales-close"
        aria-label="Dismiss Forced Asset Sales panel"
        title="Dismiss (reappears next time a bailout starts; bailout is unaffected)"
        onClick={() => setDismissed(true)}
      >
        ×
      </button>
      <h4>FORCED ASSET SALES{isSecond ? ' — SECOND BAILOUT (WORSE TERMS)' : ''}</h4>
      <p className="forced-asset-sales-note">
        IMF {isSecond ? 'second ' : ''}bailout active since tick {active.enteredAt} ({ticksLeft} ticks
        remaining this year). Sell assets, biggest capital value first, to restore solvency.
      </p>
      {/* FEAT-1972079923 inc3 (AC-5): the alternative to forced asset sales —
          enter Administration Mode instead. Closes this panel + starts the
          360-tick discretionary-spend freeze (AC-6/AC-7). */}
      <button
        className="btn tiny forced-asset-sales-admin-btn"
        onClick={() => dispatch({ type: 'enterAdministration' })}
      >
        Enter Administration
      </button>
      {assets.length === 0 ? (
        <p className="forced-asset-sales-empty">No sellable assets remain.</p>
      ) : (
        <ul className="forced-asset-sales-list">
          {/* BUG-625: keyed on id+x+y, not id alone — forcedSaleAssets copies
              b.id straight from state.buildings, and two DIFFERENT buildings
              can share an id under the nextId-desync class (BUG-413/BUG-631)
              while never sharing a tile. Same fix as ConstructionQueue.tsx /
              servicesTabs.tsx. */}
          {assets.map((a) => (
            <li key={`${a.id}-${a.x}-${a.y}`}>
              <span className="fas-name">{a.name}</span>
              <span className="fas-loc">({a.x}, {a.y})</span>
              <span className="fas-value">{fmtMoney(a.saleValue)}</span>
              <button className="btn tiny" onClick={() => dispatch({ type: 'sellAsset', id: a.id })}>
                Sell
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// FEAT-1972079923 inc4 (AC-10): the SECOND bailout banner — mirrors
// InsolvencyBanner's 'crisis' copy but names the worse-terms second event and
// makes clear it was AUTO-TRIGGERED (no button, no player choice to decline
// it — Aaron's round-2 ruling overrides the BA doc's stale "user-initiated"
// text). Visible for the whole active second-bailout window, whether the
// player is selling from the FORCED ASSET SALES panel or has entered
// Administration again (AdministrationBanner takes over display in that case
// via the exposed 'administration' overlay, same as the first bailout).
function SecondBailoutBanner() {
  const { state } = useSim();
  if (state.insolvencyState !== 'bailout_second' || !state.bailoutSecondState) return null;
  const ticksLeft = Math.max(
    0,
    state.bailoutSecondState.enteredAt + SECOND_BAILOUT_DURATION_TICKS - state.tick,
  );
  return (
    <div className="insolvency-banner crisis second-bailout" role="status">
      SECOND IMF BAILOUT (worse terms) — auto-triggered, still insolvent after the first bailout
      year. {ticksLeft} ticks left this year. Sell assets or enter Administration — no third
      bailout will follow.
    </div>
  );
}

// FEAT-1972079923 inc4 (AC-11) — the FINAL DECLINE screen: hard game-over.
// Rendered once state.declineState is set (permanent — advance() freezes the
// clock the instant it is set, see engine.ts). Shows the stats CAPTURED at the
// decline tick (real computed values from trackers maintained every tick since
// game start, never fabricated defaults — GR#15). "Start Over" routes through
// the wrapped dispatch's GR#27 capture-before-wipe path (attemptWipe in
// store.tsx); "Load Save" reuses the SAME captureOutgoingOrDownload-guarded
// loadGame() flow every other load path uses — neither button bypasses the
// fail-closed pre-wipe archive.
function DeclineScreen() {
  const { state, dispatch, loadGame } = useSim();
  const decline = state.declineState;
  // FEAT-2326609720 inc1: registers as the HIGHEST-priority of the three
  // MapView candidates (only RebuildPrompt, a different subtree entirely,
  // outranks it) — see overlayLayers.ts's BLOCKING_OVERLAY_PRIORITY.
  const isTop = useBlockingOverlay(BLOCKING_OVERLAY_ID.DECLINE_SCREEN, BLOCKING_OVERLAY_RANK.declineScreen, !!decline);
  // AC-6/I-4: the Decline Screen must be closable WITHOUT resuming the game —
  // `closed` is pure UI-local state (never touches SimState/declineState,
  // which is what actually freezes the clock in engine.ts). Closing just
  // hides the dialog; a small reopen chip (below) keeps the still-required
  // Start Over / Load Save / Play Mode choice reachable so the player is
  // never permanently stranded (I-4: no modal may trap with no escape path).
  const [closed, setClosed] = useState(false);
  const declineEnteredAt = decline?.enteredAt ?? null;
  useEffect(() => {
    setClosed(false);
  }, [declineEnteredAt]);
  useEscapeKey(isTop && !closed, () => setClosed(true));
  if (!decline || !isTop) return null;
  const yearsPlayed = Math.round(decline.enteredAt / TICKS_PER_YEAR);
  if (closed) {
    return (
      <button
        type="button"
        className="decline-reopen-chip"
        onClick={() => setClosed(false)}
        title="City is in decline and the clock is paused. Reopen to choose Start Over, Load Save, or Play Mode."
      >
        ⏸ City declined — reopen
      </button>
    );
  }
  return (
    <div className="decline-screen-overlay" role="alertdialog" aria-modal="true">
      <div className="decline-screen">
        <button
          type="button"
          className="btn tiny decline-screen-close"
          aria-label="Close (the game stays paused — reopen from the chip to choose Start Over, Load Save, or Play Mode)"
          title="Close without resuming — the clock stays paused (Esc also works)"
          onClick={() => setClosed(true)}
        >
          ×
        </button>
        <h2>⏸ City in Decline: Insolvency Unresolved</h2>
        <p className="decline-cause">Persistent insolvency after 2 bailout years.</p>
        <ul className="decline-stats">
          <li>Peak population: {fmtNum(decline.peakPopulation)}</li>
          <li>Final population: {fmtNum(decline.finalPopulation)}</li>
          <li>Years played: {yearsPlayed}</li>
          <li>Min funds reached: {fmtMoney(decline.minFundsEver)}</li>
          <li>Total spending: {fmtMoney(decline.totalSpending)}</li>
        </ul>
        <div className="decline-screen-actions">
          <button className="btn" onClick={() => dispatch({ type: 'reset' })}>
            Start Over
          </button>
          <button className="btn" onClick={() => void loadGame()}>
            Load Save
          </button>
          {/* FEAT-2326609723 (Play Mode) — the ONE-WAY sandbox escape hatch,
              a deliberate player choice offered ONLY from this game-over
              screen. 'enterPlayMode' is exempted from the decline freeze
              (engine.ts reduceCore) specifically so this button works. */}
          <button
            className="btn btn-playmode"
            onClick={() => dispatch({ type: 'enterPlayMode' })}
            title="Sandbox mode: injects a trillion in play money. Not a simulation — a deliberate, irreversible choice to keep building."
          >
            Keep playing — sandbox
          </button>
        </div>
      </div>
    </div>
  );
}

// FEAT-2326609723 (Play Mode) — the PERSISTENT, unmissable banner that stays
// visible for the rest of the session once Play Mode is latched (the latch
// never clears — see SimState.playModeLatched's doc). Rendered unconditionally
// alongside the other insolvency-ladder banners so it survives Decline-screen
// dismissal, save/load, and every subsequent tick.
function PlayModeBanner() {
  const { state } = useSim();
  if (!state.playModeLatched) return null;
  return (
    <div className="playmode-banner" role="status">
      PLAY MODE — not a simulation
    </div>
  );
}

function Compass() {
  return (
    <svg className="compass" viewBox="0 0 64 76" role="img" aria-label="Compass, north up">
      <text x="32" y="13" textAnchor="middle" className="cmp-n">
        N
      </text>
      <circle cx="32" cy="46" r="24" className="cmp-ring" />
      <polygon points="32,26 38,56 32,50 26,56" className="cmp-needle" />
      <circle cx="32" cy="46" r="3" className="cmp-hub" />
    </svg>
  );
}

// Exported (not just used internally) so the Q100092 display tests can render
// it directly against a hand-built SimContext fixture without mounting the
// whole map canvas — mirrors constructionQueueOf's export-for-testability idiom
// in ConstructionQueue.tsx.
export function BuildingCard({
  building,
  connected,
  showRefs,
  onClose,
}: {
  building: Building;
  connected: boolean;
  showRefs: boolean;
  onClose: () => void;
}) {
  const { state } = useSim();
  const [copied, setCopied] = useState(false);
  const sp = SPECS[building.spec];
  if (!sp) return null;
  const util = utilisationOf(state, building);
  // Q100092 (Aaron, confirmed design, BUG-569 display half): while a building
  // fails the 'construction' gate it must show CONSTRUCTION PROGRESS, never
  // utilisation/output/served/revenue — those start truthfully at 0 the day it
  // actually comes online. `failedGates`/`underConstruction` reuse the SAME
  // SSOT the Construction Queue tab uses (computeFailedGates + constructionTicks,
  // both imported here, never re-derived — GR#3) so this can never drift from
  // the map's own WHY-offline tooltip or the queue panel's ticks-remaining.
  const failedGates = isOnline(state, building) ? [] : computeFailedGates(state, building);
  const underConstruction = failedGates.some((g) => g.gate === 'construction');
  // Percent complete = ticks elapsed / constructionTicks(sp), clamped 0-100.
  // constructionTicks() is the SAME flag-scaled helper the gate itself calls
  // (BUG-613 fast-build scaling), so a scaled build shows a consistent percent
  // — no second formula.
  const constructionPct = (() => {
    if (!underConstruction || building.builtTick == null) return 0;
    const total = constructionTicks(sp);
    // BUG-623: while the 'construction' gate is still failing, the building is
    // BY DEFINITION not yet complete — so the displayed percent must never
    // read 100% here. Round-off on the last ~0.5% of ticks (elapsed/total close
    // to 1) was producing "N ticks remaining (100%)" with N>0, a direct
    // contradiction of the still-offline gate reason on the same line. Clamp
    // to 99 whenever underConstruction is true; true 100% is only reachable the
    // tick the gate stops failing, at which point this whole branch (and the
    // percent-complete line) no longer renders — so literal 100% is now
    // unobservable by construction, not just by luck.
    if (total <= 0) return 99;
    const elapsed = state.tick - building.builtTick;
    return Math.min(99, Math.max(0, Math.round((elapsed / total) * 100)));
  })();
  // FEAT-1972079903: when the Refs overlay is on, append the building ref to the
  // panel's name·provision label (e.g. "Small Holding · #44") so the visible
  // report number matches the map overlay and the debug JSON's buildings[].id.
  const refLabel = buildingRefLabel(building, showRefs);
  // Class-type number NNNN.L — stable per-TYPE id + level (distinct from #id).
  const classLabel = specClassLabel(sp);

  // Copy the building's full data as JSON so Aaron can paste it into chat.
  // Guard navigator.clipboard (undefined in some contexts / insecure origins) —
  // never throw if absent.
  function copyJson() {
    const payload = buildingCopyPayload(building, sp!, state.taxRates);
    const text = JSON.stringify(payload, null, 2);
    const clip = typeof navigator !== 'undefined' ? navigator.clipboard : undefined;
    if (!clip || typeof clip.writeText !== 'function') return;
    clip.writeText(text).then(
      () => {
        setCopied(true);
        setTimeout(() => setCopied(false), 1200);
      },
      () => {
        /* clipboard write rejected (permissions) — silent, no throw */
      },
    );
  }

  return (
    <div className="building-card">
      <header>
        <span className="swatch big" style={{ background: sp.color }} />
        <b>
          {sp.name}
          <span className="mono"> · class {classLabel}</span>
          {refLabel && <span className="mono"> · {refLabel}</span>}
        </b>
        <button className="btn tiny" title="Copy this building's data as JSON" onClick={copyJson}>
          {copied ? 'Copied' : 'Copy'}
        </button>
        <button className="btn tiny" onClick={onClose}>
          Close
        </button>
      </header>
      {(() => {
        // F5 (independent round REJECT, 2026-09-03): the label must read the
        // building's REAL current footprint (footprintOf), not the spec's
        // base w/h — a grown building's card previously kept showing its
        // stale original size forever.
        const { w: cardW, h: cardH } = footprintOf(building, sp);
        const heightStoreys = building.heightStoreys ?? 1;
        return (
          <p className="mono">
            id #{building.id} · grid {coordLabel(building.x, building.y)} · footprint {cardW}×{cardH}
            {heightStoreys > 1 && ` · ${heightStoreys} storeys`}
            {building.scaleLocked && ' · fully developed (at max height and footprint)'}
          </p>
        );
      })()}
      <p>{sp.blurb}</p>
      {sp.dims && (
        <p className="mono">
          {sp.dims.x} × {sp.dims.y} m footprint ·{' '}
          {sp.dims.z < 0 ? `depth ${Math.abs(sp.dims.z)} m` : `${sp.dims.z} m tall`}
        </p>
      )}
      {/* Q100092: never utilisation while under construction — the failed-gates
          block below carries "Under construction — N ticks remaining (X%)"
          instead. Once online, utilisation renders as it always has. */}
      {util && !underConstruction && (
        <p>
          Utilisation {Math.round(util.ratio * 100)}% ({util.basis})
        </p>
      )}
      {sp.kind === 'water' && (
        <>
          <p className={connected ? 'in' : 'out'}>
            {sp.tag === 'clean'
              ? 'Source: aquifer abstraction pipe (cyan stub, north)'
              : 'Discharge: sea outfall pipe (olive stub, south)'}
          </p>
          {/* Q100092: "serves N" is a live output figure — suppressed while
              under construction, same as utilisation above. */}
          {!underConstruction && (
            <p className="mono">
              Pipe {PIPE_TIERS[state.pipeTier[building.id] ?? 0].label} · serves{' '}
              {fmtNum(plantEffServed(state, building))}
            </p>
          )}
        </>
      )}
      {/* FEAT-1972079891 inc1 (AC-5) — WHY offline: list the failed activation
          gate(s). Construction keeps its existing wording (plus the Q100092
          percent-complete, computed above from the SAME constructionTicks() the
          gate itself calls); the road gates add the "not road-side" / "road not
          connected" reasons. Falls back to a generic line if the building is
          offline for a reason inc1 doesn't itemise. */}
      {!isOnline(state, building) &&
        (() => {
          if (failedGates.length === 0) {
            return <p className="out">Offline</p>;
          }
          return failedGates.map((g) => (
            <p key={g.gate} className="out">
              {g.gate === 'construction' ? `${g.reason} (${constructionPct}%)` : g.reason}
            </p>
          ));
        })()}
      {sp.kind === 'station' && (
        <p className={connected ? 'in' : 'out'}>
          {connected
            ? 'Connected — citizens auto-use this station'
            : 'Not connected — no road touches this station yet'}
        </p>
      )}
      <BuildingProfileView spec={sp} taxRates={state.taxRates} hideProduces={underConstruction} />
    </div>
  );
}

// FEAT-1972079866: full per-object economic profile, sourced purely from the
// spec (+ tax rates for the two clean fiscal contributions). Groups the spec's
// inputs (REQUIRES) against its outputs (PRODUCES) so the player can inspect
// exactly what a selected building costs, needs, and generates.
function fmtProfileValue(line: ProfileLine): string {
  if (line.value === null) return '';
  switch (line.unit) {
    case 'power':
      return formatPower(line.value);
    case 'moneyPerTick':
      return `${fmtMoney(line.value)}/tick`;
    case 'count':
    default:
      return fmtNum(line.value);
  }
}

function ProfileLineRow({ line }: { line: ProfileLine }) {
  const value = fmtProfileValue(line);
  return (
    <li>
      <span>{line.label}</span>{' '}
      {value ? <b>{value}</b> : line.note ? <i className="muted">{line.note}</i> : null}
      {value && line.note ? <span className="muted"> ({line.note})</span> : null}
    </li>
  );
}

function BuildingProfileView({
  spec,
  taxRates,
  hideProduces,
}: {
  spec: Spec;
  taxRates: TaxRates;
  // Q100092 (Aaron, confirmed design): PRODUCES is the spec's nameplate output
  // (e.g. "Power 1120") — Aaron's original repro ("pow_nuke showing produces
  // power 1120... while still being built") was this very line reading as a
  // live figure. Suppressed while the instance it's attached to is under
  // construction; REQUIRES/CAPEX/OPEX (inputs + cost) are catalogue facts, not
  // output, and stay visible.
  hideProduces?: boolean;
}) {
  const profile = buildingProfile(spec, taxRates);
  return (
    <div className="building-profile">
      <p>
        <b>CAPEX</b> {fmtMoney(profile.capex)} build · <b>OPEX</b> {fmtMoney(profile.opex)}/tick upkeep
      </p>
      {profile.requires.length > 0 && (
        <div>
          <p className="profile-heading">REQUIRES</p>
          <ul className="profile-list">
            {profile.requires.map((line) => (
              <ProfileLineRow key={line.key} line={line} />
            ))}
          </ul>
        </div>
      )}
      {profile.produces.length > 0 &&
        (hideProduces ? (
          <p className="muted">PRODUCES — available once construction completes</p>
        ) : (
          <div>
            <p className="profile-heading">PRODUCES</p>
            <ul className="profile-list">
              {profile.produces.map((line) => (
                <ProfileLineRow key={line.key} line={line} />
              ))}
            </ul>
          </div>
        ))}
    </div>
  );
}

export type { ZoneKind };
