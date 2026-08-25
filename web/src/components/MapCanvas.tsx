import { useEffect, useRef } from "react";
import type { ViewportPatch, ViewportCell, PowerLine } from "../ws/messages";

const TERRAIN_COLORS: Record<string, string> = {
  grass: "#5a8f3c",
  woodland: "#2e5d28",
  water: "#2b6a9b",
  shingle: "#b8ad94",
  rock: "#7d7466",
};

// FEAT-1972079851: one colour per pylon class, matched by the tcell map
// (internal/ui/screens/map/render.go powerClassStyle). Unknown classes
// (a future trio slice's tier this build predates) render in a neutral
// fallback rather than being guessed at.
const POWER_COLORS: Record<string, string> = {
  localPole: "#f1c40f", // power yellow
  standardLattice: "#e67e22", // lattice orange
  superGrid: "#c0392b", // super-grid red
};
const POWER_COLOR_FALLBACK = "#95a5a6";

function cellColor(cell: ViewportCell): string {
  if (cell.building) return "#c0392b";
  return TERRAIN_COLORS[cell.terrain ?? ""] ?? "#333333";
}

/**
 * SpanWindow is the visible region ([x0,x1]x[y0,y1], inclusive) a span
 * walk is clamped to — the browser twin of the tcell renderer's
 * gridWindow (internal/ui/screens/map/render.go walkPowerSpan).
 */
export interface SpanWindow {
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

// Bound every endpoint before the dx/dy subtraction: the decode-side
// gates live server-side (compose publishes only bounded spans), so this
// ceiling exists purely so a hostile payload that reached the browser
// anyway cannot drive precision-loss walks or iteration proportional to
// the endpoint magnitudes — the pre-window approach walk is capped at
// O(MAX_SAFE_SPAN_COORD) steps (a few million worst case). Inputs beyond
// it can never arrive from a well-formed patch (the engine's spans live
// in a ~10^3-cell domain).
const MAX_SAFE_SPAN_COORD = 2 ** 20;

/**
 * lineCells walks the integer Bresenham line between two inclusive
 * endpoints — the same walk the tcell renderer does, so both consumers
 * paint the identical set of cells for a span. When win is given, only
 * cells inside the window are collected and the walk terminates early
 * once any axis has moved permanently past it (the walk is monotone per
 * axis and a cell must be in-range on EVERY axis, so one exhausted axis
 * means no further cell can be inside), so no span — however hostile its
 * endpoints — can make the array length or iteration count proportional
 * to the endpoint magnitudes.
 */
export function lineCells(
  fromX: number,
  fromY: number,
  toX: number,
  toY: number,
  win?: SpanWindow,
): Array<[number, number]> {
  if (win && (win.x1 < win.x0 || win.y1 < win.y0)) return [];
  const clampCoord = (v: number) =>
    Math.max(-MAX_SAFE_SPAN_COORD, Math.min(MAX_SAFE_SPAN_COORD, v));
  fromX = clampCoord(fromX);
  fromY = clampCoord(fromY);
  toX = clampCoord(toX);
  toY = clampCoord(toY);
  const dx = toX - fromX;
  const dy = toY - fromY;
  const sx = dx > 0 ? 1 : -1;
  const sy = dy > 0 ? 1 : -1;
  const adx = Math.abs(dx);
  const ady = Math.abs(dy);
  let err = adx - ady;
  const cells: Array<[number, number]> = [];
  let x = fromX;
  let y = fromY;
  const pastAxis = (v: number, step: number, lo: number, hi: number) =>
    step > 0 ? v > hi : step < 0 ? v < lo : v < lo || v > hi;
  for (;;) {
    if (!win || (x >= win.x0 && x <= win.x1 && y >= win.y0 && y <= win.y1)) {
      cells.push([x, y]);
    }
    if (x === toX && y === toY) break;
    const e2 = 2 * err;
    if (e2 > -ady) {
      err -= ady;
      x += sx;
    }
    if (e2 < adx) {
      err += adx;
      y += sy;
    }
    if (
      win &&
      (pastAxis(x, sx, win.x0, win.x1) || pastAxis(y, sy, win.y0, win.y1))
    ) {
      break;
    }
  }
  return cells;
}

function powerColor(line: PowerLine): string {
  return POWER_COLORS[line.class] ?? POWER_COLOR_FALLBACK;
}

interface MapCanvasProps {
  patch: ViewportPatch | null;
  /** FEAT-1972079851's 'Power' layer toggle; default OFF. */
  showPower?: boolean;
}

/**
 * MapCanvas is the S1 placeholder consumer of the f1.viewport stream: it
 * paints the latest full snapshot as a flat colour grid. d3-based
 * rendering and pan/zoom arrive later; this only proves the delta path
 * lights pixels. When showPower is true, placed pylon spans are painted
 * over the terrain grid, one coloured cell per span cell.
 */
export function MapCanvas({ patch, showPower = false }: MapCanvasProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !patch) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    ctx.fillStyle = "#111111";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    if (patch.cells.length === 0) return;

    const scale = Math.max(
      1,
      Math.floor(canvas.width / patch.extent.width),
    );
    for (const cell of patch.cells) {
      ctx.fillStyle = cellColor(cell);
      ctx.fillRect(cell.x * scale, cell.y * scale, scale, scale);
    }
    if (showPower && patch.powerLines) {
      // Clamp every span walk to the patch extent's window — the browser
      // twin of the tcell renderer's drawPowerLines clamp (SEC-039 class).
      const ext = patch.extent;
      const win: SpanWindow | undefined =
        Number.isFinite(ext.width) &&
        Number.isFinite(ext.height) &&
        ext.width > 0 &&
        ext.height > 0
          ? { x0: 0, y0: 0, x1: ext.width - 1, y1: ext.height - 1 }
          : undefined;
      if (!win) return;
      for (const line of patch.powerLines) {
        ctx.fillStyle = powerColor(line);
        for (const [cx, cy] of lineCells(
          line.fromX,
          line.fromY,
          line.toX,
          line.toY,
          win,
        )) {
          ctx.fillRect(cx * scale, cy * scale, scale, scale);
        }
      }
    }
  }, [patch, showPower]);

  return (
    <div className="map-canvas">
      <canvas ref={canvasRef} width={400} height={400} />
      {patch === null && <p className="placeholder">awaiting f1.viewport…</p>}
    </div>
  );
}
