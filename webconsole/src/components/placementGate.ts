// placementGate.ts — BUG-652 follow-up, ROUND r4 (2026-09-04).
//
// Round r4 (INDEPENDENT DESTRUCTIVE, GR#23) found that round r3's
// placementAffordability() gate lived at exactly ONE dispatch site (MapView's
// single-tile build click) and was bypassed by every BATCH placement path:
//   - drag-paint: N tiles flush as one 'placeMany' action on pointerup
//   - stampRegion: a clone-paste of a captured clipboard region
//   - resolveDemand / resolveDemandAll: the advisor's "Fix"/"Fix All",
//     which build a whole demandFixPlan()'s worth of units in one dispatch
// Proven live: 3 Channel Tunnel Portals drag-painted for 180% of gross
// inflow with zero confirmation — the gate never saw the batch at all.
//
// THE FIX: hoist the check into this ONE shared function, called from EVERY
// UI dispatch site (MapView.tsx's build click / drag-paint flush / clone-
// paste, DemandDock.tsx's Fix/Fix All buttons) BEFORE constructing its
// action — never inside a reducer (round r3's architecture: a reducer that
// can refuse a journalled action breaks replay by construction, so the
// reducer stays pure and unguarded; this file is UI-only and touches no
// SimState field). A batch is evaluated as ONE aggregate confirmation (the
// round's own ask: "3 tunnels = the summed figure, one confirm for the
// whole batch, not three dialogs"), via data.ts's batchPlacementAffordability().

import type { SimState } from '../sim/types.ts';
import { SPECS, batchPlacementAffordability, type PlacementAffordability } from '../sim/data.ts';

/** One pending batch placement awaiting player confirmation — component-local
 *  UI state ONLY (MapView/DemandDock's own useState), never SimState, never
 *  journaled. `commit` is the exact dispatch the caller would have made had
 *  the gate not tripped; AffordabilityConfirm's "Build anyway" calls it
 *  verbatim, unmodified, so the confirmed action is byte-identical to what
 *  a below-threshold placement would have dispatched immediately. */
export interface PendingBatchPlacement {
  /** Display subject + real recurring cost, from batchPlacementAffordability(). */
  afford: PlacementAffordability;
  /** Dispatches the ORIGINAL action this batch represents. */
  commit: () => void;
}

/**
 * Evaluate a batch of spec ids about to be placed via a SINGLE dispatch
 * (whatever its shape — 'place', 'placeMany', 'stampRegion',
 * 'resolveDemand', 'resolveDemandAll'). Returns `null` when the batch is
 * fine to dispatch immediately (below threshold, or nothing job-bearing in
 * it); returns a `PendingBatchPlacement` (bundling the aggregate
 * affordability read-out + the caller-supplied `commit` callback) when the
 * player must confirm first.
 *
 * `specIds` may repeat (N copies of the same spec — drag-paint, a demand-fix
 * plan's `count`) or mix different ids (a multi-type clipboard paste) —
 * batchPlacementAffordability() aggregates either shape correctly. Unknown
 * spec ids are silently dropped (defensive; every real dispatch site only
 * ever supplies ids that exist in SPECS).
 *
 * Pure except for `commit`, which is never invoked here — this function only
 * DECIDES whether to gate, it never dispatches anything itself.
 */
export function evaluatePlacementBatch(
  state: SimState,
  specIds: string[],
  commit: () => void
): PendingBatchPlacement | null {
  const specs = specIds.map((id) => SPECS[id]).filter((sp): sp is (typeof SPECS)[string] => !!sp);
  const afford = batchPlacementAffordability(state, specs);
  if (!afford.exceedsThreshold) return null;
  return { afford, commit };
}
