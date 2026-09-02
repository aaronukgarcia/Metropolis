// populationTabs.tsx — FEAT-2326609720 inc2, Population group child tabs.
//
// §1 rows 8/9/11/12/27: Wellbeing + Housing are SPLIT OUT of the old
// RightDock `status` tab (unchanged content, two homes instead of one).
// Demographics is a direct move of the old `population` tab. Employment
// (row 12) and Migration (row 27) are the two population-side NEW surfaces —
// Employment renders REAL numbers (AC-5) from the already-landed
// totalJobs()/unemploymentOf() selectors; Migration is explicitly PARTIAL
// (AC-6) because `attractiveness` is not yet exposed on SimState (§1 row 27,
// BLOCKED pending a selector) — it renders the real births/moveIns/deaths/
// moveOuts it CAN show and an explicit "not yet available" fallback for the
// attractiveness figure, never a fabricated number (GR#1).

import { useSim } from '../../../sim/simContext';
import { wellbeingOf, approvalOf } from '../../../sim/engine';
import {
  onlineResidentsCapacity,
  offlineResidentsByReason,
  totalJobs,
  unemploymentOf,
  WORKING_AGE_FRACTION,
} from '../../../sim/data';
import { fmtNum, fmtPct } from '../../../sim/utils';
import { PopulationSankey } from '../../PopulationSankey';
import { ArrivalsByModeSankey } from '../../ArrivalsByModeSankey';
import { RAG_THRESHOLDS, ragForWellbeing, ragForApproval, ragForUnemployment, ragColor, STUB_LABEL } from '../../ragThresholds';

// §1 row 8 — split from RightDock `status`.
export function WellbeingTab() {
  const { state } = useSim();
  const approval = approvalOf(state);
  const wb = wellbeingOf(state);
  const approvalRag = ragForApproval(approval);
  const wbRag = ragForWellbeing(wb.overall);
  return (
    <>
      <div className="tiles">
        <div className={`tile ${approvalRag === 'red' ? 'neg' : 'pos'}`} style={{ borderColor: ragColor(approvalRag) }}>
          <div className="n" style={{ color: ragColor(approvalRag) }}>{approval}</div>
          <div className="l">Approval</div>
        </div>
        {/* AC-7/AC-8: reads RAG_THRESHOLDS.WELLBEING at BOTH call sites in this
            file (this tile AND the per-part breakdown below) — never a second
            locally re-derived 70/45 literal (GR#3). */}
        <div className={`tile ${wbRag === 'red' ? 'neg' : 'pos'}`} style={{ borderColor: ragColor(wbRag) }}>
          <div className="n" style={{ color: ragColor(wbRag) }}>{wb.overall}</div>
          <div className="l">Wellbeing</div>
        </div>
      </div>
      <h4>Wellbeing breakdown</h4>
      <div className="wb-list">
        {wb.parts.map((p) => {
          const rag = ragForWellbeing(p.value);
          const col = ragColor(rag);
          return (
            <div key={p.label} className="wb-row">
              <span className="d-label">{p.label}</span>
              <div className="d-bar">
                <span
                  className="d-fill pos"
                  style={{ left: 0, width: `${Math.max(0, Math.min(100, p.value))}%`, background: col }}
                />
              </div>
              <span className="mono d-val" style={{ color: col }}>
                {p.value}
              </span>
            </div>
          );
        })}
      </div>
      <p className="hint">
        Wellbeing thresholds: GREEN ≥ {RAG_THRESHOLDS.WELLBEING.GREEN}, AMBER ≥ {RAG_THRESHOLDS.WELLBEING.AMBER}, else RED.
      </p>
    </>
  );
}

// §1 row 9 — split from RightDock `status`.
export function HousingTab() {
  const { state } = useSim();
  // BUG-417: the headline "Housing cap" is the ONLINE figure — the capacity
  // engine growth can actually fill (offline / under-construction dwellings do
  // not house anyone yet). The gross total stays visible as a "+N under
  // construction" breakdown so the mismatch is legible, not hidden.
  const capacity = onlineResidentsCapacity(state);
  const { construction: buildingResidents, disconnected } = offlineResidentsByReason(state);
  return (
    <div className="tiles">
      <div className="tile acc">
        <div className="n">{fmtNum(state.population)}</div>
        <div className="l">Citizens</div>
      </div>
      <div className="tile">
        <div className="n">{fmtNum(capacity)}</div>
        <div className="l">
          Housing cap
          {buildingResidents > 0 && (
            <>
              {' '}
              <span className="sub" title="Residential capacity still under construction — not yet online, so nobody lives here yet">
                (+{fmtNum(buildingResidents)} building)
              </span>
            </>
          )}
          {disconnected > 0 && (
            <>
              {' '}
              <span
                className="sub warn-text"
                title="Residential capacity built but NOT on the road network — connect roads to these dwellings to grow the population"
              >
                +{fmtNum(disconnected)} not on road network — connect to grow
              </span>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

// §1 row 11 — direct move of the old RightDock `population` tab.
export function DemographicsTab() {
  const { state } = useSim();
  const last = state.lastDemographics;
  return (
    <>
      <div className="tiles">
        <div className="tile pos">
          <div className="n">{fmtNum(last?.births ?? 0)}</div>
          <div className="l">Births (last tick)</div>
        </div>
        <div className="tile acc">
          <div className="n">{fmtNum(last?.moveIns ?? 0)}</div>
          <div className="l">Move-ins (last tick)</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(last?.deaths ?? 0)}</div>
          <div className="l">Deaths (last tick)</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(last?.moveOuts ?? 0)}</div>
          <div className="l">Move-outs (last tick)</div>
        </div>
      </div>
      <h4>Demographic flow</h4>
      <PopulationSankey history={state.demographicHistory} />
      <h4>Arrivals by mode</h4>
      <ArrivalsByModeSankey history={state.arrivalsByModeHistory} />
    </>
  );
}

// §1 row 12 — NEW tab (AC-5): real totalJobs/unemploymentOf numbers, no
// placeholder. Order-independent pure folds (AC-14) — same selectors
// serviceCoverageOf/wellbeingOf already consume.
export function EmploymentTab() {
  const { state } = useSim();
  const jobs = totalJobs(state);
  const workers = Math.round(state.population * WORKING_AGE_FRACTION);
  const unemployment = unemploymentOf(state);
  const rag = ragForUnemployment(unemployment);
  return (
    <>
      <div className="tiles">
        <div className="tile acc">
          <div className="n">{fmtNum(jobs)}</div>
          <div className="l">Total jobs</div>
        </div>
        <div className="tile">
          <div className="n">{fmtNum(workers)}</div>
          <div className="l">Working-age population</div>
        </div>
        <div className={`tile ${rag === 'red' ? 'neg' : rag === 'green' ? 'pos' : ''}`} style={{ borderColor: ragColor(rag) }}>
          <div className="n" style={{ color: ragColor(rag) }}>{fmtPct(unemployment)}</div>
          <div className="l">Unemployment</div>
        </div>
      </div>
      <p className="hint">
        Working-age population = population × {fmtPct(WORKING_AGE_FRACTION)} (PLACEHOLDER, matches
        serviceCoverageOf's jobs basis). Unemployment thresholds: GREEN &lt; {fmtPct(RAG_THRESHOLDS.UNEMPLOYMENT.GREEN)},
        AMBER ≤ {fmtPct(RAG_THRESHOLDS.UNEMPLOYMENT.AMBER)}, else RED.
      </p>
    </>
  );
}

// §1 row 27 — PARTIAL/BLOCKED (AC-6): births/moveIns/deaths/moveOuts are real
// (state.lastDemographics), attractiveness is NOT exposed on SimState yet
// (engine.ts local variable only) — render an explicit fallback, no colour.
export function MigrationTab() {
  const { state } = useSim();
  const last = state.lastDemographics;
  return (
    <>
      <div className="tiles">
        <div className="tile acc">
          <div className="n">{fmtNum(last?.moveIns ?? 0)}</div>
          <div className="l">Move-ins (last tick)</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(last?.moveOuts ?? 0)}</div>
          <div className="l">Move-outs (last tick)</div>
        </div>
      </div>
      <div className="tile stub" data-testid="migration-attractiveness-stub">
        <div className="n muted">{STUB_LABEL}</div>
        <div className="l">Migration attractiveness score</div>
      </div>
      <p className="hint">
        Attractiveness is computed inside engine.ts's move-in calculation but is not yet exposed on
        SimState (§1 row 27, BLOCKED pending a selector) — no colour is applied to this row (AC-10).
      </p>
    </>
  );
}
