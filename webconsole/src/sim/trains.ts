// ════════════════════════════════════════════════════════════════════════════
// FEAT-1972079902 — RAIL NETWORK inc2: LIVE DETERMINISTIC TRAINS.
//
// This module is the DETERMINISM FIREWALL for the live-train render. Every train
// glyph's position, count, dwell and colour is a PURE function of
// (tick, line geometry, demand) — NO Date.now / new Date / performance.now /
// Math.random / requestAnimationFrame time anywhere (GR#21). MapView draws
// exactly what trainPositions() returns, so replays and the consistency checker
// stay byte-identical: the same (tick, board, demand) always yields the same
// glyphs. Nothing here is stored in SimState, costs money, or mutates state —
// trains are UI-derived display only (costing trains is inc3, the router epic).
//
// ⚠ BALANCE-NUMBER REGIME (Aaron's blanket rule): the train-count ladder, the
// per-segment travel time and the station dwell below are all PLACEHOLDERS —
// directional only, pending Aaron's row-by-row balance pass. Do not tune
// gameplay against these numbers.
// ════════════════════════════════════════════════════════════════════════════

/** A tile coordinate (top-left origin, tile units — MapView adds the +0.5 centre). */
export interface Pt {
  x: number;
  y: number;
}

/** One rail-line tile as MapView extracts it from a building: its line CLASS + tile. */
export interface RailTile {
  /** Line spec id — 'rail' (slow) or 'hs1' (high-speed). */
  spec: string;
  x: number;
  y: number;
}

/** A single tile occupied by a station building (footprint already expanded). */
export interface StationTile {
  x: number;
  y: number;
}

/**
 * The deterministic route geometry for ONE rail line class. `points` is the
 * ordered polyline of that class's tiles (sorted, so insertion order can never
 * change it). `circuit` indexes into `points` describing the path a train runs:
 * forward end-to-end then back again (ping-pong), so a train visibly traverses
 * the whole line and returns with no teleporting wrap. `stationIdx` are the
 * `points` indices where a station sits on the route (a train dwells there).
 */
export interface LineGeometry {
  spec: string;
  points: Pt[];
  circuit: number[];
  stationIdx: number[];
}

/** Per-line demand read-out (from inc1's lineUsageOf), the ONLY demand input. */
export interface LineDemand {
  spec: string;
  /** 0..1 saturation — drives the train COUNT and the colour bucket. */
  saturation: number;
  /** headroom < 0 — the BUG-425 over-capacity split (red vs green). */
  overCapacity: boolean;
}

/** Colour bucket — reuses inc1's BUG-425 tokens ONLY (no new colour language). */
export type TrainBucket = 'ok' | 'hot';

/** One train glyph to draw. Fractional x/y are interpolated tile coordinates. */
export interface TrainGlyph {
  x: number;
  y: number;
  /** 0..1 position of the train through its lap (deterministic from tick). */
  progress: number;
  /** 'hot' when the line is over capacity, else 'ok' (BUG-425 split). */
  bucket: TrainBucket;
  /** The line's saturation, so MapView can size the glyph by load. */
  saturation: number;
  /** true while the train is holding at a station tile (the visible stop). */
  stoppedAtStation: boolean;
}

/** All trains for one line class. */
export interface LineTrains {
  spec: string;
  trains: TrainGlyph[];
}

// ── PLACEHOLDER-balance constants (Aaron's sign-off pending) ─────────────────

/** Sim ticks a train takes to cross ONE polyline segment (inverse of speed). */
export const TRAVEL_TICKS = 2;
/** Sim ticks a train holds stationary at a station tile (the visible stop). */
export const DWELL_TICKS = 3;
/** Saturation band width per extra train above the first. */
export const TRAIN_BAND = 0.25;
/** Hard cap on trains drawn per line (keeps a saturated line legible). */
export const MAX_TRAINS = 6;

/**
 * Train COUNT for a line from its saturation (0..1). Deterministic ladder:
 *   saturation 0        → 0 trains (an idle line runs nothing)
 *   (0, 0.25]           → 1 train
 *   (0.25, 0.50]        → 2
 *   (0.50, 0.75]        → 3
 *   (0.75, 1.0]         → 4..5 (1 + floor(sat / band)), capped at MAX_TRAINS.
 * Monotonic non-decreasing in saturation — more demand always means ≥ as many
 * trains. ⚠ PLACEHOLDER-balance (TRAIN_BAND / MAX_TRAINS).
 */
export function trainCountFor(saturation: number): number {
  if (!(saturation > 0)) return 0; // also rejects NaN / negatives
  const n = 1 + Math.floor(Math.min(saturation, 1) / TRAIN_BAND);
  return Math.min(n, MAX_TRAINS);
}

const key = (x: number, y: number): string => `${x},${y}`;

/**
 * Build the per-line-class route geometry from raw rail + station tiles. PURE and
 * DETERMINISTIC: rail tiles are grouped by line class and sorted by (x, then y),
 * so ANY insertion order of the same board yields byte-identical geometry. A
 * station index is any polyline point coincident with, or 4-adjacent to, a
 * station tile — that is where trains dwell.
 */
export function buildRailGeometry(
  railTiles: RailTile[],
  stationTiles: StationTile[]
): LineGeometry[] {
  const stationSet = new Set<string>();
  for (const st of stationTiles) stationSet.add(key(st.x, st.y));

  // Group tiles by line class, de-duplicating exact coordinates per class.
  const bySpec = new Map<string, Map<string, Pt>>();
  for (const t of railTiles) {
    let m = bySpec.get(t.spec);
    if (!m) bySpec.set(t.spec, (m = new Map()));
    m.set(key(t.x, t.y), { x: t.x, y: t.y });
  }

  const out: LineGeometry[] = [];
  for (const [spec, m] of bySpec) {
    const points = [...m.values()].sort((a, b) => a.x - b.x || a.y - b.y);
    const S = points.length;

    // Which points sit on a station (self or 4-neighbour is a station tile).
    const stationIdx: number[] = [];
    for (let i = 0; i < S; i++) {
      const p = points[i];
      if (
        stationSet.has(key(p.x, p.y)) ||
        stationSet.has(key(p.x + 1, p.y)) ||
        stationSet.has(key(p.x - 1, p.y)) ||
        stationSet.has(key(p.x, p.y + 1)) ||
        stationSet.has(key(p.x, p.y - 1))
      ) {
        stationIdx.push(i);
      }
    }

    // Ping-pong circuit: 0,1,…,S-1, S-2,…,1 — run up the line and back, no wrap
    // teleport. For S < 2 there is no circuit (a single tile carries no train).
    const circuit: number[] = [];
    if (S >= 2) {
      for (let i = 0; i < S; i++) circuit.push(i);
      for (let i = S - 2; i >= 1; i--) circuit.push(i);
    }

    out.push({ spec, points, circuit, stationIdx });
  }

  // Deterministic, spec-id-sorted output (mirrors lineUsageOf).
  out.sort((a, b) => (a.spec < b.spec ? -1 : a.spec > b.spec ? 1 : 0));
  return out;
}

/**
 * Compute every train glyph for every line, PURELY from (lines, demand, tick).
 *
 * Movement model (per line): a train runs the `circuit` at one schedule-tick per
 * sim tick. At each circuit node it dwells DWELL_TICKS ticks IF that node is a
 * station (holding position = a visible stop), then travels to the next node over
 * TRAVEL_TICKS ticks, linearly interpolated. One lap = Σ(dwell + travel) ticks
 * and repeats, so positions are periodic and byte-identical for a given tick.
 * `count` trains per line are spaced evenly by a per-train tick OFFSET (index-
 * derived, never random), so they sit at different points of the same lap.
 */
export function trainPositions(
  lines: LineGeometry[],
  demand: LineDemand[],
  tick: number
): LineTrains[] {
  const demandBySpec = new Map<string, LineDemand>();
  for (const d of demand) demandBySpec.set(d.spec, d);

  const out: LineTrains[] = [];
  for (const line of lines) {
    const d = demandBySpec.get(line.spec);
    const saturation = d ? d.saturation : 0;
    const bucket: TrainBucket = d && d.overCapacity ? 'hot' : 'ok';
    const count = trainCountFor(saturation);
    const trains: TrainGlyph[] = [];

    const C = line.circuit.length;
    if (count > 0 && C >= 2 && line.points.length >= 2) {
      const stationNodes = new Set(line.stationIdx);
      // Dwell ticks at each circuit node (0 unless the node is a station).
      const dwell = line.circuit.map((pi) => (stationNodes.has(pi) ? DWELL_TICKS : 0));
      let lap = 0;
      for (let j = 0; j < C; j++) lap += dwell[j] + TRAVEL_TICKS;

      for (let k = 0; k < count; k++) {
        const offset = Math.floor((k * lap) / count);
        // Local tick within the lap for this train (always in [0, lap)).
        const local = (((tick - offset) % lap) + lap) % lap;

        let acc = 0;
        let glyph: TrainGlyph | null = null;
        for (let j = 0; j < C && glyph === null; j++) {
          // Dwell phase at circuit node j.
          if (local < acc + dwell[j]) {
            const p = line.points[line.circuit[j]];
            glyph = {
              x: p.x,
              y: p.y,
              progress: local / lap,
              bucket,
              saturation,
              stoppedAtStation: true,
            };
            break;
          }
          acc += dwell[j];
          // Travel phase from node j to node j+1 (wrapping the circuit).
          if (local < acc + TRAVEL_TICKS) {
            const f = (local - acc) / TRAVEL_TICKS;
            const a = line.points[line.circuit[j]];
            const b = line.points[line.circuit[(j + 1) % C]];
            glyph = {
              x: a.x + (b.x - a.x) * f,
              y: a.y + (b.y - a.y) * f,
              progress: local / lap,
              bucket,
              saturation,
              stoppedAtStation: false,
            };
            break;
          }
          acc += TRAVEL_TICKS;
        }

        // `local < lap` guarantees the walk assigned a glyph; guard for safety.
        if (glyph !== null) trains.push(glyph);
      }
    }

    out.push({ spec: line.spec, trains });
  }
  return out;
}
