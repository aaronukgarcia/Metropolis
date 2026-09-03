// instanceBuilder.ts — FEAT-2326609760 GPU acceleration spike, Phase 0/1
// (Aaron 2026-09-03 fast-track ruling: WebGPU-first, Canvas2D fallback).
//
// Pure, side-effect-free Structure-of-Arrays (SoA) builders that turn
// `SimState.buildings` + the SPECS catalogue into flat Float32Array instance
// buffers, per §2.1/§2.2 of docs/planning/proposals/gpu-acceleration-deepdive-2026-09-03.md.
//
// DETERMINISM / GR#21: this module reads SimState, never writes it. Nothing
// here feeds a reducer/dispatch path — see mapRenderer.ts's doc comment for
// the full "display-only, never re-enters the sim" argument. Building this
// buffer twice from the same (state, filter) always produces byte-identical
// output (no Date.now()/Math.random()), which is also what the "dirty
// upload only on identity change" contract in mapRenderer.ts depends on:
// callers key their own "already uploaded" cache on `state.buildings`
// array-identity, the same idiom overlaySubsetsOf (MapView.tsx) already uses.
//
// LAYOUT (documented explicitly so the WGSL shader's vertex-buffer stride
// declarations in shaders.ts stay in lockstep with this file):
//   STATIC per instance  (8 floats, changes only on place/bulldoze/relocate):
//     [0] x       world tile X of the building's top-left corner
//     [1] y       world tile Y
//     [2] w       footprint width in tiles  (sp.w)
//     [3] h       footprint height in tiles (sp.h)
//     [4] r       colour red   0..1
//     [5] g       colour green 0..1
//     [6] b       colour blue  0..1
//     [7] a       colour alpha 0..1 (always 1 today — reserved for a future
//                 per-instance alpha need rather than baking alpha into the
//                 dynamic block, keeping STATIC self-contained for colour)
//   DYNAMIC per instance (4 floats, re-derived every tick):
//     [0] online       1.0 online, 0.0 offline (drives the shader's dim/hatch)
//     [1] occupancy    0..1 fill fraction, or -1 sentinel = "not applicable"
//                      (mirrors MapView.tsx's `occ == null` branch)
//     [2] utilisation  0..1 ratio, or -1 sentinel = "not applicable"
//                      (mirrors utilisationOf() returning null)
//     [3] tier         1|2|3 density tier for zone kinds, 0 = not a zone
//
// Visual-parity note (plan §5 Phase 1 acceptance shape): this buffer
// reproduces position/size/colour/online/occupancy/utilisation/tier exactly
// as MapView.tsx's Canvas2D loop reads them (same isOnline/blockOccupancy/
// utilisationOf/densityTier calls). Deliberately NOT reproduced yet (documented
// difference, out of Phase-1 scope per the plan): the offline hatch stroke
// pattern, the multi-tile border stroke, selection outline, and the ref-id
// label overlay — those are drawn as thin decorative strokes/text in the
// Canvas2D path and are exactly the "overlays" the plan defers past Phase 1
// ("the building layer only, not overlays yet").

import {
  SPECS,
  isOnline,
  blockOccupancy,
  utilisationOf,
  densityTier,
  isRoadSpec,
  footprintOf,
  type Spec,
} from '../sim/data.ts';
import type { Building, SimState } from '../sim/types.ts';

/** Number of Float32 slots per instance in the STATIC buffer. See file header. */
export const STATIC_FLOATS_PER_INSTANCE = 8;

/** Number of Float32 slots per instance in the DYNAMIC buffer. See file header. */
export const DYNAMIC_FLOATS_PER_INSTANCE = 4;

/** Sentinel written into a DYNAMIC slot for "not applicable" (occupancy/utilisation). */
export const NOT_APPLICABLE = -1;

export interface InstanceBuffers {
  staticData: Float32Array;
  dynamicData: Float32Array;
  /** Number of instances encoded (both arrays are exactly count * FLOATS_PER_INSTANCE long). */
  count: number;
  /** Building ids in the same index order as the two arrays — debug/spot-check aid only,
   * never uploaded to the GPU. */
  ids: number[];
}

/** True for every spec MapView's main building loop draws (everything except
 * drivable roads/motorways, which get their own instance batch — see
 * roadInstanceFilter). Matches the plan's "buildings/roads as two batches". */
export function buildingInstanceFilter(sp: Spec): boolean {
  return !isRoadSpec(sp) && sp.kind !== 'motorway';
}

/** True for drivable road/motorway tiles — the second instance batch. */
export function roadInstanceFilter(sp: Spec): boolean {
  return isRoadSpec(sp) || sp.kind === 'motorway';
}

/**
 * Parses a `Spec.color` hex string ("#rrggbb") into 0..1 RGB floats.
 * A spec with a malformed colour is a data-registry bug (GR#7/GR#15 —
 * colours are registry data, never guessed): rather than silently rendering
 * something plausible-looking, this returns a loud, unmistakable magenta so
 * the defect is visible on-screen instead of hidden (GR#1 aggressive error
 * trapping — a rendering-layer equivalent of "never swallow the failure").
 */
export function hexToRgbUnit(hex: string): [number, number, number] {
  const m = /^#?([0-9a-fA-F]{6})$/.exec(hex);
  if (!m) return [1, 0, 1];
  const n = parseInt(m[1], 16);
  return [((n >> 16) & 0xff) / 255, ((n >> 8) & 0xff) / 255, (n & 0xff) / 255];
}

/**
 * Builds the SoA instance buffers for every building in `state.buildings`
 * that passes `filter(spec)`. Order is stable (source array order), so the
 * same (state, filter) pair always yields the same index-to-building mapping
 * — required for the "spot-check instance i against building i" test
 * contract and for dirty-range addressing in a future finer-grained upload.
 */
export function buildInstances(
  state: SimState,
  filter: (sp: Spec) => boolean
): InstanceBuffers {
  const filtered: { b: Building; sp: Spec }[] = [];
  for (const b of state.buildings) {
    const sp = SPECS[b.spec];
    if (!sp) continue; // unknown spec id — defensively skipped, matches MapView.tsx's `if (!sp) continue`
    if (!filter(sp)) continue;
    filtered.push({ b, sp });
  }

  const count = filtered.length;
  const staticData = new Float32Array(count * STATIC_FLOATS_PER_INSTANCE);
  const dynamicData = new Float32Array(count * DYNAMIC_FLOATS_PER_INSTANCE);
  const ids: number[] = new Array(count);

  for (let i = 0; i < count; i++) {
    const { b, sp } = filtered[i];
    const [r, g, bl] = hexToRgbUnit(sp.color);

    // F5 (independent round REJECT, 2026-09-03): a building that has scaled
    // OUT (FEAT-2326609740) draws bigger than sp.w/sp.h — read the building's
    // OWN current footprint (footprintOf) so the GPU buffer stays in visual
    // parity with MapView.tsx's Canvas2D loop and with debug.json.
    const { w: fpW, h: fpH } = footprintOf(b, sp);
    const so = i * STATIC_FLOATS_PER_INSTANCE;
    staticData[so + 0] = b.x;
    staticData[so + 1] = b.y;
    staticData[so + 2] = fpW;
    staticData[so + 3] = fpH;
    staticData[so + 4] = r;
    staticData[so + 5] = g;
    staticData[so + 6] = bl;
    staticData[so + 7] = 1;

    const online = isOnline(state, b);
    const occ = online ? blockOccupancy(state, b) : null;
    const util = online ? utilisationOf(state, b) : null;
    const tier = sp.category === 'zones' ? densityTier(sp) : 0;

    const dof = i * DYNAMIC_FLOATS_PER_INSTANCE;
    dynamicData[dof + 0] = online ? 1 : 0;
    dynamicData[dof + 1] = occ == null ? NOT_APPLICABLE : occ;
    dynamicData[dof + 2] = util == null ? NOT_APPLICABLE : util.ratio;
    dynamicData[dof + 3] = tier;

    ids[i] = b.id;
  }

  return { staticData, dynamicData, count, ids };
}

/** Rebuilds only the DYNAMIC buffer for an existing (state, filter, ids) index
 * mapping — used by mapRenderer's "buildings unchanged, only dynamic fields
 * may have moved" tick path so a re-derivation never re-walks the STATIC
 * fields or re-parses colours for buildings that haven't moved. Callers are
 * responsible for proving `ids` still matches `state.buildings` filtered by
 * `filter` in the same order (true whenever state.buildings identity hasn't
 * changed, since buildInstances's iteration order is a pure function of the
 * array itself).
 */
export function rebuildDynamicOnly(
  state: SimState,
  filter: (sp: Spec) => boolean,
  ids: number[]
): Float32Array {
  const byId = new Map<number, Building>();
  for (const b of state.buildings) byId.set(b.id, b);

  const dynamicData = new Float32Array(ids.length * DYNAMIC_FLOATS_PER_INSTANCE);
  for (let i = 0; i < ids.length; i++) {
    const b = byId.get(ids[i]);
    const sp = b ? SPECS[b.spec] : undefined;
    const dof = i * DYNAMIC_FLOATS_PER_INSTANCE;
    if (!b || !sp || !filter(sp)) {
      // Building vanished or changed category since the static buffer was
      // built — leave it fully "offline/not-applicable" rather than guess;
      // the caller's identity-change detection should make this unreachable
      // in practice (a vanished building changes state.buildings identity),
      // but a rendering bug here must never crash the frame (GR#1).
      dynamicData[dof + 0] = 0;
      dynamicData[dof + 1] = NOT_APPLICABLE;
      dynamicData[dof + 2] = NOT_APPLICABLE;
      dynamicData[dof + 3] = 0;
      continue;
    }
    const online = isOnline(state, b);
    const occ = online ? blockOccupancy(state, b) : null;
    const util = online ? utilisationOf(state, b) : null;
    const tier = sp.category === 'zones' ? densityTier(sp) : 0;
    dynamicData[dof + 0] = online ? 1 : 0;
    dynamicData[dof + 1] = occ == null ? NOT_APPLICABLE : occ;
    dynamicData[dof + 2] = util == null ? NOT_APPLICABLE : util.ratio;
    dynamicData[dof + 3] = tier;
  }
  return dynamicData;
}
