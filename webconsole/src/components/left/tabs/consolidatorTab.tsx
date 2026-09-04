// consolidatorTab.tsx — FEAT-2326609761 inc1 DISCOVERY + SECTION AUDIT half.
//
// READ-ONLY reporting panel: shows what the consolidator's analysis layer
// (sim/consolidator.ts) sees, on the CURRENT live city. The only WRITE this
// tab performs is dispatching the journalled `toggleConsolidator` action
// (mirrored from ConfigMenu.tsx) — it has no Undo and no way to apply
// anything, that surface belongs to the separate mutation-half lane (AC-25's
// log, AC-26's Undo). This tab exists so Aaron can SEE the analysis land
// before the "it moves" half ships (task brief: "so he gets something
// watchable within days rather than months").
//
// Follows the DebugTab idiom (../debugTab.tsx) for its refresh discipline:
// sectionIndexOf/topOpportunities/strandedCapacityReport are O(buildings)
// per call (memoOnState-cached per state OBJECT, but a running city gets a
// new state object every tick) — recomputing on every render of a
// continuously-ticking city would repeat that walk far more often than a
// human can read the numbers change. The panel freezes a snapshot and only
// retakes it every REFRESH_MS of WALL CLOCK time (a UI-layer concern, not a
// sim one — the underlying analysis functions themselves stay pure
// GR#21-compliant functions of SimState with zero clock reads).
//
// While OFF (consolidatorEnabled false/absent): the panel does ZERO
// sectionIndexOf/topOpportunities/strandedCapacityReport work — same "no
// cost when off" contract the map overlay observes (mapOverlays.ts).
//
// NOTE (independent-round finding 1, 2026-09-04 — "TAB-MOUNTED-ONLY"): this
// tab used to ALSO be the audit trail's only call site (logConsolidatorAudit,
// fired from the effect below). That meant the trail silently stopped the
// moment Aaron looked at any other LeftDock tab — his ruling is "the audit
// runs while the CONSOLIDATOR is enabled", not "while its tab is visible".
// The posting call site has moved to a store-level subscriber in store.tsx
// (gated on state.consolidatorEnabled, independent of any tab) — see that
// effect's comment and consolidatorAudit.ts's file header. This tab keeps
// its own display refresh exactly as before; it simply no longer generates
// the trail itself.

import { useEffect, useRef, useState } from 'react';
import { useSim } from '../../../sim/simContext';
import { fmtMoney, fmtNum } from '../../../sim/utils';
import { nextRefreshDue } from '../../../sim/throttle';
import { CONSOLIDATOR_ENABLED_DEFAULT } from '../../../sim/engine';
import { publishConsolidatorFocus } from '../../../sim/consolidatorFocus';
import type { SimState } from '../../../sim/types';
import {
  strandedCapacityReport,
  topOpportunities,
  currentMonthOpportunities,
  monthlyScopeOf,
  TOTAL_SECTIONS,
  type StrandedCapacityReport,
  type TopOpportunity,
} from '../../../sim/consolidator';

const REFRESH_MS = 5000;
const TOP_LIMIT = 20;

interface Frame {
  tick: number;
  twelfth: number;
  full: boolean;
  sectionsInScope: number;
  report: StrandedCapacityReport;
  /**
   * Round-1 destructive finding 3 (2026-09-04): the tab previously displayed
   * "Month N scope" text directly above a table that actually scanned the
   * WHOLE map every refresh (`currentMonthOpportunities` was exported by
   * consolidator.ts but never imported here) — the header lied about what
   * the list below it showed. Fixed by keeping TWO distinct, honestly
   * labelled lists instead of one ambiguous one:
   *   - `monthTop`: reconnect + consolidate opportunities SCOPED to this
   *     month's actual rotation (`scope.sectionKeys` — ruling 7's 1/12, or
   *     the whole map only on month 12). This is genuinely "what this
   *     month's pass would act on".
   *   - `wholeMapTop`: the informational, always-whole-map view, labelled
   *     as such — useful context, never confused with what a real pass
   *     would touch this month.
   * `monthDensityCount` directly wires `currentMonthOpportunities` (the
   * density-only convenience export) so it is no longer dead code, per the
   * round's explicit "wire it" instruction.
   */
  monthDensityCount: number;
  monthTop: TopOpportunity[];
  wholeMapTop: TopOpportunity[];
}

function buildFrame(state: SimState): Frame {
  const scope = monthlyScopeOf(state.tick);
  const report = strandedCapacityReport(state);
  const allKeys = Array.from({ length: TOTAL_SECTIONS }, (_, i) => i);
  return {
    tick: state.tick,
    twelfth: scope.twelfth,
    full: scope.full,
    sectionsInScope: scope.sectionKeys.length,
    report,
    monthDensityCount: currentMonthOpportunities(state).length,
    monthTop: topOpportunities(state, scope.sectionKeys, TOP_LIMIT),
    wholeMapTop: topOpportunities(state, allKeys, TOP_LIMIT),
  };
}

export function ConsolidatorTab() {
  const { state, dispatch } = useSim();
  const enabled = state.consolidatorEnabled ?? CONSOLIDATOR_ENABLED_DEFAULT;
  const [frame, setFrame] = useState<Frame | null>(null);
  const lastRefreshRef = useRef<number | null>(null);

  useEffect(() => {
    // Off means ZERO cost: no sectionIndexOf/topOpportunities/
    // strandedCapacityReport work at all while the toggle is off — mirrors
    // AC-2's "the pass returns the input state by reference identity" idiom
    // for this read-only half (there is no state to return here, so the
    // equivalent contract is simply: do not compute).
    if (!enabled) {
      lastRefreshRef.current = null;
      setFrame(null);
      publishConsolidatorFocus(null); // off -> MapView's box/highlight hide immediately
      return;
    }
    const now = Date.now();
    const { due } = nextRefreshDue(lastRefreshRef.current, now, REFRESH_MS);
    if (due || frame == null) {
      lastRefreshRef.current = now;
      const f = buildFrame(state);
      setFrame(f);
      publishConsolidatorFocus(f.monthTop[0]?.sectionKey ?? null);
    }
    // Poll for the next due refresh rather than recomputing on every state
    // change — mirrors DebugTab's "freeze a frame, retake on a timer" idiom.
    const id = setInterval(() => {
      const { due: dueNow } = nextRefreshDue(lastRefreshRef.current, Date.now(), REFRESH_MS);
      if (dueNow) {
        lastRefreshRef.current = Date.now();
        const f = buildFrame(state);
        setFrame(f);
        publishConsolidatorFocus(f.monthTop[0]?.sectionKey ?? null);
      }
    }, 1000);
    return () => clearInterval(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentional: frame refresh is TIME-driven, not state-driven (see file header)
  }, [state, enabled]);

  // Clear the mailbox on unmount so a closed tab never leaves a stale
  // highlight on the map (MapView degrades to "no highlight" once this fires).
  useEffect(() => () => publishConsolidatorFocus(null), []);

  const toggleRow = (
    <label
      className="brand-menu-row consolidator-toggle"
      title="Consolidator (urban regenerator): while enabled, demolishes and rebuilds parts of the city automatically to reduce clutter and cost. Costs real money when it acts. Mirrors the same toggle in the Config menu."
    >
      <input type="checkbox" checked={enabled} onChange={() => dispatch({ type: 'toggleConsolidator' })} />
      Consolidator (urban regenerator)
    </label>
  );

  if (!enabled) {
    return (
      <div className="consolidator-tab">
        {toggleRow}
        <p className="consolidator-empty">
          Off — no analysis is running and the map's section-focus box is hidden. Turning this on
          costs nothing by itself; it only starts the (currently read-only) discovery/audit pass.
        </p>
      </div>
    );
  }

  if (!frame) return (
    <div className="consolidator-tab">
      {toggleRow}
      <div className="dock-empty">Loading consolidator analysis…</div>
    </div>
  );

  const { report, monthTop, wholeMapTop, monthDensityCount } = frame;

  return (
    <div className="consolidator-tab">
      {toggleRow}
      <p className="consolidator-note">
        Read-only discovery + audit (inc1). No changes are made by this tab — the automatic
        apply/Undo half lands separately.
      </p>

      <section>
        <h4>Stranded capacity (built, paid for, not contributing)</h4>
        <div className="tiles">
          <div className="tile neg">
            <div className="n">{fmtNum(report.totalActionableCapacity)}</div>
            <div className="l">Residents stranded (road cause)</div>
          </div>
          <div className="tile">
            <div className="n">{report.clusterCount}</div>
            <div className="l">Section-clusters affected</div>
          </div>
          <div className="tile" title="A LOWER BOUND, not a quote: this is a straight-line section-grid estimate. The real reconnect cost can be many times higher once you count obstacles (buildings, water, terrain) a real route must go around.">
            <div className="n">at least {fmtMoney(report.totalEstimatedReconnectCost)}</div>
            <div className="l">Reconnect cost (lower bound)</div>
          </div>
          <div className="tile">
            <div className="n">{fmtNum(report.totalConstructionCapacity)}</div>
            <div className="l">Under construction (self-resolving)</div>
          </div>
        </div>
        {report.clusterCount === 0 && (
          <p className="consolidator-empty">No road-caused stranded capacity right now.</p>
        )}
      </section>

      <section>
        <h4>
          Month {frame.twelfth + 1} scope: {frame.full ? 'whole map (big-picture pass)' : `1/12 of the map`} —{' '}
          {fmtNum(frame.sectionsInScope)} sections, {fmtNum(monthDensityCount)} density-consolidation
          {monthDensityCount === 1 ? ' rung' : ' rungs'} found in scope
        </h4>
        {renderOpportunityTable(monthTop, `No opportunities found in this month's scope.`)}
      </section>

      <section>
        <h4>Whole-map opportunities (informational — NOT this month's scope; shown for context only)</h4>
        {renderOpportunityTable(wholeMapTop, 'No opportunities found anywhere on the map.')}
      </section>
    </div>
  );
}

/**
 * Round-1 destructive finding 3 fix: this is now called TWICE with two
 * DIFFERENT, correctly-scoped section-key lists (see buildFrame's
 * monthTop/wholeMapTop) rather than once with a whole-map list sitting
 * under a "Month N scope" header that lied about what it showed.
 */
function renderOpportunityTable(top: TopOpportunity[], emptyMessage: string) {
  if (top.length === 0) return <p className="consolidator-empty">{emptyMessage}</p>;
  return (
    <table className="consolidator-opportunities">
      <thead>
        <tr>
          <th>#</th>
          <th>Kind</th>
          <th>Section</th>
          <th>Detail</th>
          <th>Net cost</th>
        </tr>
      </thead>
      <tbody>
        {top.map((o) =>
          o.kind === 'reconnect' ? (
            <tr key={`r-${o.sectionKey}`}>
              <td>{o.rank}</td>
              <td>Reconnect</td>
              <td>{o.sectionKey}</td>
              <td>
                {fmtNum(o.strandedCapacity)} residents stranded ({o.cause})
                {o.approxSpurSections != null
                  ? `, at least ~${o.approxSpurSections} sections to connected road (real route may be much longer)`
                  : ', no connected road found'}
              </td>
              <td>{o.estimatedReconnectCost != null ? `at least ${fmtMoney(o.estimatedReconnectCost)}` : '—'}</td>
            </tr>
          ) : (
            <tr key={`c-${o.sectionKey}-${o.fromSpec}-${o.toSpec}`}>
              <td>{o.rank}</td>
              <td>Consolidate</td>
              <td>{o.sectionKey}</td>
              <td>
                {o.groupCount}x {o.fromSpec} → {o.toSpec} ({o.buildingCountReduction} fewer buildings, capacity{' '}
                {o.capacityGain >= 0 ? `+${fmtNum(o.capacityGain)}` : fmtNum(o.capacityGain)})
              </td>
              <td>{fmtMoney(o.netCost)}</td>
            </tr>
          ),
        )}
      </tbody>
    </table>
  );
}
