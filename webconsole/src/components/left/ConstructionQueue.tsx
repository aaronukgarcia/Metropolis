// ConstructionQueue.tsx — BUG-605: "I still don't see the queue" (Aaron).
//
// A read-only panel that makes the CONSTRUCTION QUEUE unconditionally visible
// in the left dock. No new state, no new actions — every row is derived from
// existing SimState via the SAME SSOT helpers the game already uses to gate
// online-ness and to render the AC-5 "Under construction — N ticks remaining"
// WHY tooltip (data.ts: isOnline, computeFailedGates, constructionTicks,
// placementCost — see debugBuildSpeed.ts's own note that constructionTicks()
// is "the single place lead-time is derived"). This component does NOT
// re-derive the ticks-remaining formula itself; it reuses computeFailedGates'
// 'construction' gate (the same call that drives the map tooltip) to decide
// membership, and constructionTicks() (imported, not copied) for the
// remaining-ticks number, exactly mirroring data.ts:684's
// `constructionTicks(sp) - (s.tick - b.builtTick)`.
//
// GR#21 determinism: sorting is ticks-remaining ascending, tie-broken by
// building id ascending — a pure, order-independent, total order over
// `state.buildings` (no Map/Set iteration order dependency).
//
// Unconditional visibility (Aaron's complaint): this panel always renders,
// with an explicit empty state, and carries NO dev/debug flag gate.

import { useSim } from '../../sim/simContext';
import { SPECS, computeFailedGates, constructionTicks, memoOnState, placementCost } from '../../sim/data';
import { fmtMoney } from '../../sim/utils';
import type { SimState } from '../../sim/types';

export interface QueueRow {
  id: number;
  name: string;
  x: number;
  y: number;
  ticksRemaining: number;
  cost: number;
}

/**
 * Pure derivation of the construction queue from SimState — exported so tests
 * can assert against it directly without going through React. Reuses
 * computeFailedGates (data.ts) to identify "still under construction"
 * buildings (the same predicate the map's WHY tooltip already uses) and
 * constructionTicks (data.ts) for the remaining-ticks arithmetic, so this
 * file never invents its own build-time formula (GR#3 SSOT).
 */
// BUG-610 (round finding): memoised on state identity — LeftDock reads this
// on every render regardless of the active tab (the badge count), and the walk
// measured ~10ms at a 13,000-building city. Pure derivation of s, so
// memoOnState is exact (the house idiom for this class, see data.ts).
export const constructionQueueOf: (s: SimState) => QueueRow[] = memoOnState((s) => {
  const rows: QueueRow[] = [];
  for (const b of s.buildings) {
    if (b.builtTick == null) continue; // no builtTick recorded => never gated by construction (legacy/instant)
    const sp = SPECS[b.spec];
    if (!sp) continue;
    const gates = computeFailedGates(s, b);
    const stillBuilding = gates.length > 0 && gates[0].gate === 'construction';
    if (!stillBuilding) continue;
    const ticksRemaining = constructionTicks(sp) - (s.tick - b.builtTick);
    rows.push({
      id: b.id,
      name: sp.name,
      x: b.x,
      y: b.y,
      ticksRemaining,
      cost: placementCost(sp),
    });
  }
  // Deterministic sort: ticks-remaining ascending, id ascending tie-break.
  rows.sort((a, b) => a.ticksRemaining - b.ticksRemaining || a.id - b.id);
  return rows;
});

export function ConstructionQueueTab() {
  const { state } = useSim();
  const rows = constructionQueueOf(state);

  return (
    <>
      <h4>Construction Queue{rows.length > 0 ? ` — ${rows.length}` : ''}</h4>
      {rows.length === 0 ? (
        <p className="muted">Nothing under construction.</p>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Building</th>
              <th>Location</th>
              <th>Ticks left</th>
              <th>Cost</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <td>{r.name}</td>
                <td className="mono">
                  ({r.x}, {r.y})
                </td>
                <td className="mono">{r.ticksRemaining}</td>
                <td className="mono">{fmtMoney(r.cost)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {/* Secondary line: the only other queryable "pending build" signal in
          SimState today is placeNotice, a one-shot post-placement string (not
          a queue) — surfaced here as a hint rather than invented as a second
          list. See BUG-605 report: no other pending-auto-build state exists. */}
      {state.placeNotice && <p className="hint">{state.placeNotice}</p>}
    </>
  );
}
