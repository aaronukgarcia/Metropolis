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
  residentialConstructionSummary,
  totalJobs,
  unemploymentOf,
  WORKING_AGE_FRACTION,
} from '../../../sim/data';
import { isAtCapacity } from '../../ragThresholds';
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

/** BUG-645 — natural increase (births - deaths) and net migration
 *  (moveIns - moveOuts) for a DemographicFlow-shaped object, plus their sum
 *  (the actual population delta the two forces net out to). Pure arithmetic
 *  over already-computed fields (GR#15) — never re-derived from anything
 *  else, so it is exactly consistent with the tiles above/below it. */
function netsOf(flow: { births: number; deaths: number; moveIns: number; moveOuts: number }) {
  const natural = flow.births - flow.deaths;
  const migration = flow.moveIns - flow.moveOuts;
  return { natural, migration, net: natural + migration };
}

function signed(n: number): string {
  return n >= 0 ? `+${fmtNum(n)}` : fmtNum(n);
}

// §1 row 11 — direct move of the old RightDock `population` tab.
//
// BUG-645 (Aaron: "as the days go past with 1.9m people the population stays
// the same — why do births and deaths not make it go up and down per day or
// at month end?"). The mechanic was always correct — births beat deaths, but
// move-outs almost exactly cancel that natural increase once the city sits
// at online housing capacity — the DEFECT was that nothing showed the two
// forces or explained why they net to ~zero. This tab now shows BOTH asked-
// for granularities (last tick AND month-to-date, state.lastDemographics /
// state.demographicAccum — both already computed by engine.ts, no new
// derivation) plus the NET row that makes "+569 natural increase vs -569 net
// migration = 0 net change" legible, and repeats the at-capacity explanation
// (mirrored from TopBar's badge, same isAtCapacity/onlineResidentsCapacity
// SSOT) for the player who opens this tab looking for the reason.
export function DemographicsTab() {
  const { state } = useSim();
  const last = state.lastDemographics ?? { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 };
  const monthSoFar = state.demographicAccum ?? { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 };
  const lastNets = netsOf(last);
  const monthNets = netsOf(monthSoFar);
  const onlineCapacity = onlineResidentsCapacity(state);
  const atCapacity = isAtCapacity(state.population, onlineCapacity);
  const underConstruction = residentialConstructionSummary(state);
  return (
    <>
      {atCapacity && (
        <p className="hint warn-text">
          At online housing capacity ({fmtNum(state.population)} of {fmtNum(onlineCapacity)}) — births/deaths/
          moves below are all real, but arrivals can only replace departures, not grow the total, until more
          housing comes online.
          {underConstruction.count > 0 &&
            ` ${fmtNum(underConstruction.count)} homes under construction adding ${fmtNum(underConstruction.capacity)} capacity when they finish.`}
        </p>
      )}
      <h4>Last tick</h4>
      <div className="tiles">
        <div className="tile pos">
          <div className="n">{fmtNum(last.births)}</div>
          <div className="l">Births</div>
        </div>
        <div className="tile acc">
          <div className="n">{fmtNum(last.moveIns)}</div>
          <div className="l">Move-ins</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(last.deaths)}</div>
          <div className="l">Deaths</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(last.moveOuts)}</div>
          <div className="l">Move-outs</div>
        </div>
      </div>
      <div className="tiles" data-testid="demographics-last-nets">
        <div className={`tile ${lastNets.natural >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(lastNets.natural)}</div>
          <div className="l">Natural increase (births − deaths)</div>
        </div>
        <div className={`tile ${lastNets.migration >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(lastNets.migration)}</div>
          <div className="l">Net migration (moves in − out)</div>
        </div>
        <div className={`tile ${lastNets.net >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(lastNets.net)}</div>
          <div className="l">Net population change</div>
        </div>
      </div>
      <h4>This month so far</h4>
      <div className="tiles" data-testid="demographics-month-accum">
        <div className="tile pos">
          <div className="n">{fmtNum(monthSoFar.births)}</div>
          <div className="l">Births</div>
        </div>
        <div className="tile acc">
          <div className="n">{fmtNum(monthSoFar.moveIns)}</div>
          <div className="l">Move-ins</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(monthSoFar.deaths)}</div>
          <div className="l">Deaths</div>
        </div>
        <div className="tile neg">
          <div className="n">{fmtNum(monthSoFar.moveOuts)}</div>
          <div className="l">Move-outs</div>
        </div>
      </div>
      <div className="tiles" data-testid="demographics-month-nets">
        <div className={`tile ${monthNets.natural >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(monthNets.natural)}</div>
          <div className="l">Natural increase (births − deaths)</div>
        </div>
        <div className={`tile ${monthNets.migration >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(monthNets.migration)}</div>
          <div className="l">Net migration (moves in − out)</div>
        </div>
        <div className={`tile ${monthNets.net >= 0 ? 'pos' : 'neg'}`}>
          <div className="n">{signed(monthNets.net)}</div>
          <div className="l">Net population change</div>
        </div>
      </div>
      <p className="hint">
        Resets to zero at the start of each in-game month; last month's totals are recorded below in the
        demographic flow history.
      </p>
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
