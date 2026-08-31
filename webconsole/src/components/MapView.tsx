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
  findSpot,
  pickAutoSpec,
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
  lineUsageOf,
  isLineSpec,
  isRoadSpec,
  isRailSpec,
  computeFailedGates,
} from '../sim/data';
import { computePath, type Tile } from '../sim/roadTracker';
import { buildRailGeometry, trainPositions, type RailTile, type StationTile } from '../sim/trains';
import { useSim } from '../sim/simContext';
import { demandOf, specUnlocked, SPEED_MS } from '../sim/engine';
import { publishMapUi } from '../sim/uistate';
import { consumePersistedCamera, type StorageLike } from '../sim/cameraStash';
import { applyStashedCameraToView } from '../sim/cameraApply';
import { buildingRef, buildingRefLabel } from '../sim/refs';
import { useBusy } from './Busy';
import { HelpOverlay } from './HelpOverlay';
import { makeKeydownHandler } from '../sim/keyhandler';
import type { Building, ZoneKind, TaxRates } from '../sim/types';
import type { Spec } from '../sim/data';
import { fmtMoney, fmtNum, formatPower } from '../sim/utils';
import { buildingProfile, specClassLabel, buildingCopyPayload, type ProfileLine } from '../sim/profile';

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

export function MapView() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [selected, setSelected] = useState<Building | null>(null);
  const [hover, setHover] = useState<{ x: number; y: number } | null>(null);
  const [view, setView] = useState<View>({ zoom: 2.2, cx: 165, cy: 76 });
  const [frame, setFrame] = useState(0);
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
  const selectionAnchorRef = useRef<{ x: number; y: number } | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });

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

  useEffect(() => {
    const h = setInterval(() => setFrame((f) => f + 1), 50);
    return () => clearInterval(h);
  }, []);

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
    void frame;
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

    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (!sp) continue;
      const online = isOnline(state, b);
      const px = geom.ox + b.x * geom.s;
      const py = geom.oy + b.y * geom.s;
      const pw = sp.w * geom.s;
      const ph = sp.h * geom.s;
      const baseAlpha = b.id === state.movingId ? 0.6 : online ? 1 : 0.45;
      ctx.globalAlpha = baseAlpha;
      const rx = px + 0.5;
      const ry = py + 0.5;
      const rw = Math.max(pw - 1, 1.5);
      const rh = Math.max(ph - 1, 1.5);
      // FEAT-1972079882 occupancy fill: null => full colour; else draw a dim
      // empty underlay and fill only the bottom `occ` fraction at full colour
      // (a half-occupied block shows a half-height fill, growing bottom-up).
      const occ = online ? blockOccupancy(state, b) : null;
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
        const util = utilisationOf(state, b);
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
      if ((sp.w > 1 || sp.h > 1) && geom.s > 4) {
        ctx.strokeStyle = 'rgba(15, 18, 22, 0.55)';
        ctx.lineWidth = 1;
        ctx.strokeRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      // FEAT-1972079882 density/level tier border: colour the block outline by
      // its tier (grey→blue→gold). Only zone blocks carry a tier; drawn when the
      // block is big enough on screen to read the border.
      if (sp.category === 'zones' && geom.s > 3) {
        ctx.globalAlpha = baseAlpha;
        ctx.strokeStyle = TIER_COLORS[densityTier(sp)];
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
      for (const b of state.buildings) {
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
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp || sp.kind !== 'water') continue;
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
      // Classify buildings by their power class. Currently: pylon → localGrid only.
      const pylonIds = new Set<number>();
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (sp?.kind === 'pylon') pylonIds.add(b.id);
      }
      // Dim pass: all non-power infrastructure at 0.4× alpha
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp || pylonIds.has(b.id)) continue;
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        const pw = sp.w * geom.s;
        const ph = sp.h * geom.s;
        ctx.globalAlpha = 0.4;
        ctx.fillStyle = sp.color;
        ctx.fillRect(px + 0.5, py + 0.5, Math.max(pw - 1, 1.5), Math.max(ph - 1, 1.5));
      }
      ctx.globalAlpha = 1;
      // Full-saturation pass: power infrastructure at native colour + full alpha
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!sp || !pylonIds.has(b.id)) continue;
        const px = geom.ox + b.x * geom.s;
        const py = geom.oy + b.y * geom.s;
        const pw = sp.w * geom.s;
        const ph = sp.h * geom.s;
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
      for (const b of state.buildings) {
        const sp = SPECS[b.spec];
        if (!isLineSpec(sp)) continue;
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

    // station connectivity dots
    const links = stationLinks(state);
    for (const b of state.buildings) {
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
    // byte-identical glyphs; the 50ms `frame` repaint only redraws the SAME
    // positions (a repaint pump, never a position source). Gated behind the
    // existing "Lines" overlay toggle, so it rides the same demand read-out.
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
  }, [state.buildings, state.movingId, state.tool, state.funds, state.clipboard, state.tick, state.speed, state.roadConnectivity, selected, hover, showWater, showPower, showLines, showRefs, cloneSelection, roadTracker, geom, size, frame]);

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
        dispatch({ type: 'place', spec: state.tool.spec, x: ax, y: ay });
        break;
      }
      case 'bulldoze': {
        const hit = state.buildings.find((b) => {
          const sp = SPECS[b.spec];
          return (
            sp &&
            t.x >= b.x &&
            t.x < b.x + sp.w &&
            t.y >= b.y &&
            t.y < b.y + sp.h
          );
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
            return sp && t.x >= b.x && t.x < b.x + sp.w && t.y >= b.y && t.y < b.y + sp.h;
          });
          if (hit) dispatch({ type: 'pickup', id: hit.id });
        }
        break;
      case 'select': {
        const hit = state.buildings.find((b) => {
          const sp = SPECS[b.spec];
          return sp && t.x >= b.x && t.x < b.x + sp.w && t.y >= b.y && t.y < b.y + sp.h;
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

    const auto = pickAutoSpec(s);
    if (auto) {
      const sp = SPECS[auto.spec];
      if (specUnlocked(s, sp)) {
        return {
          text: `Do you want a ${sp.name} (${sp.blurb}) to cover ${auto.label.toLowerCase()}?`,
          go: () => {
            run(() => {
              const spot = findSpot(s, auto.spec);
              if (!spot) return;
              dispatch({ type: 'place', spec: auto.spec, x: spot.x, y: spot.y });
              setView((v) =>
                clampView({ zoom: Math.max(v.zoom, 8), cx: spot.x, cy: spot.y }, size.w, size.h)
              );
            });
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
              act(t);
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
              // Stamp the clipboard at the selection anchor.
              dispatch({
                type: 'stampRegion',
                clipboard: state.clipboard,
                x: selectionAnchorRef.current.x,
                y: selectionAnchorRef.current.y,
              });
            }
            setCloneSelection(null);
            selectionAnchorRef.current = null;
          }
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
      <PlaceNoticeBanner />
      <InsolvencyBanner />
      <InsolvencyPopup />
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
// 'crisis' is the AC-1 banner text (the IMF bailout EVENT itself — forced sales,
// administration — is inc2/3, not built yet; this only surfaces the state).
function InsolvencyBanner() {
  const { state } = useSim();
  const band = state.insolvencyState ?? 'solvent';
  if (band === 'solvent') return null;
  const crisis = band === 'crisis';
  return (
    <div className={`insolvency-banner ${crisis ? 'crisis' : 'warning'}`} role="status">
      {crisis
        ? 'BAILOUT: You have 1 year to restore solvency. Sell assets or enter Administration.'
        : 'Treasury warning — funds are approaching the insolvency threshold. Raise revenue or cut spending before the IMF steps in.'}
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
  if (!popup) return null;
  return (
    <div className="insolvency-popup-overlay" role="alertdialog" aria-modal="true">
      <div className="insolvency-popup">
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
          (Forced asset sales and Administration Mode land in a later increment —
          this notice exists so the bailout is never a surprise.)
        </p>
        <button className="btn" onClick={() => dispatch({ type: 'dismissInsolvencyPopup' })}>
          I understand
        </button>
      </div>
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

function BuildingCard({
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
      <p className="mono">
        id #{building.id} · grid {coordLabel(building.x, building.y)} · footprint {sp.w}×{sp.h}
      </p>
      <p>{sp.blurb}</p>
      {sp.dims && (
        <p className="mono">
          {sp.dims.x} × {sp.dims.y} m footprint ·{' '}
          {sp.dims.z < 0 ? `depth ${Math.abs(sp.dims.z)} m` : `${sp.dims.z} m tall`}
        </p>
      )}
      {util && (
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
          <p className="mono">
            Pipe {PIPE_TIERS[state.pipeTier[building.id] ?? 0].label} · serves{' '}
            {fmtNum(plantEffServed(state, building))}
          </p>
        </>
      )}
      {/* FEAT-1972079891 inc1 (AC-5) — WHY offline: list the failed activation
          gate(s). Construction keeps its existing wording; the road gates add the
          "not road-side" / "road not connected" reasons. Falls back to a generic
          line if the building is offline for a reason inc1 doesn't itemise. */}
      {!isOnline(state, building) &&
        (() => {
          const failed = computeFailedGates(state, building);
          if (failed.length === 0) {
            return <p className="out">Offline</p>;
          }
          return failed.map((g) => (
            <p key={g.gate} className="out">
              {g.reason}
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
      <BuildingProfileView spec={sp} taxRates={state.taxRates} />
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

function BuildingProfileView({ spec, taxRates }: { spec: Spec; taxRates: TaxRates }) {
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
      {profile.produces.length > 0 && (
        <div>
          <p className="profile-heading">PRODUCES</p>
          <ul className="profile-list">
            {profile.produces.map((line) => (
              <ProfileLineRow key={line.key} line={line} />
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

export type { ZoneKind };
