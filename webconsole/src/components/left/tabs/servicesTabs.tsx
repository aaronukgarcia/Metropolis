// servicesTabs.tsx — FEAT-2326609720 inc2, Services group child tabs.
//
// Aaron's domain-split (2026-09-02, approved amendment to §1's tree):
// "power/water/waste are all utilities, we need one for education, and one
// for health, one for industry [safety]" — supersedes the spec's single
// "Coverage Map" child (§1 row 16) with FOUR domain tabs: Utilities (Power +
// Water + Waste & Recycling, kept as sibling sub-tabs so each existing tab's
// content stays intact per Aaron's "keep the existing three tab contents
// intact" instruction), Education (nursery/primary/college), Health
// (GP/Hospital), Safety (fire/police). Power/Water/Waste tab BODIES are
// UNCHANGED relocations from RightDock (§1 rows 13-15); Education/Health/
// Safety are the NEW coverage-grid surfaces built from serviceCoverageOf()
// rows not already owned by Power/Water (§1 row 16's NEW-tab rationale,
// re-partitioned across the four domain tabs instead of one grid).

import { useState } from 'react';
import { useSim } from '../../../sim/simContext';
import {
  SPECS,
  PIPE_TIERS,
  waterBalanceOf,
  waterDemandOf,
  waterPipeInfo,
  plantEffServed,
  powerStats,
  serviceCoverageOf,
  type ServiceCoverage,
} from '../../../sim/data';
import { isBrownoutActive } from '../../../sim/data';
import {
  GRID_IMPORT_TARIFF_PER_MW,
  GRID_IMPORT_ENABLED_DEFAULT,
  WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK,
  WATER_IMPORT_ENABLED_DEFAULT,
  WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK,
  WASTEWATER_CONTRACT_ENABLED_DEFAULT,
  REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK,
  REFUSE_CONTRACT_ENABLED_DEFAULT,
  REFUSE_CONTRACT_OUTFLOW_LABEL,
  gridImportCostPerTick,
  utilityBuyInCostPerTick,
} from '../../../sim/fiscal';
import { fmtMoney, fmtMoneyEach, fmtNum, fmtPct, formatPower } from '../../../sim/utils';
import { wasteDisplayModel } from '../../right/wasteModel';
import { TabStrip } from '../../Tabs';
import { ragForCoverage, ragForPower, ragForWasteCollection, ragColor } from '../../ragThresholds';

// ---------------------------------------------------------------------------
// Power (§1 row 13 — direct relocation, unchanged content).
// ---------------------------------------------------------------------------
export function PowerTab() {
  const { state, dispatch } = useSim();
  const pw = powerStats(state);
  const importOn = state.gridImportEnabled ?? GRID_IMPORT_ENABLED_DEFAULT;
  const importedMw = importOn ? Math.max(0, pw.need - pw.cap) : 0;
  const shortfallMw = Math.max(0, pw.need - pw.cap);
  // BUG-1050 (GR#3, no local arithmetic): call the fiscal SSOT directly
  // instead of re-deriving the ceil-rounded product here — the panel can
  // never drift from the booked outflow line.
  const importCostPerTick = gridImportCostPerTick(pw.cap, pw.need, GRID_IMPORT_TARIFF_PER_MW);
  // §2 row 5 / AC-9: RAG via isBrownoutActive, never raw cap<need.
  const brownoutActive = isBrownoutActive(state);
  const rag = ragForPower({ coverageMet: pw.cap >= pw.need, brownoutActive });
  return (
    <>
      <div className="tiles">
        <div className={`tile ${rag === 'red' ? 'neg' : rag === 'green' ? 'pos' : ''}`} style={{ borderColor: ragColor(rag) }}>
          <div className="n" style={{ color: ragColor(rag) }}>{formatPower(pw.cap)}</div>
          <div className="l">Capacity</div>
        </div>
        <div className="tile">
          <div className="n">{formatPower(pw.need)}</div>
          <div className="l">Need</div>
        </div>
        <div className={`tile ${importedMw > 0 ? 'neg' : ''}`}>
          <div className="n">{formatPower(importedMw)}</div>
          <div className="l">Imported MW</div>
        </div>
      </div>
      <div className="wb-row">
        <div>
          <b>Use external power cover</b>
          <p className="muted">
            Buys in any shortfall from the regional grid at {fmtMoneyEach(GRID_IMPORT_TARIFF_PER_MW)}/MW/tick
            instead of a brownout. Off forces local self-sufficiency (legacy shortage penalty applies).
          </p>
        </div>
        <button
          className={`btn toggle ${importOn ? 'on' : ''}`}
          onClick={() => dispatch({ type: 'toggleGridImport' })}
        >
          {importOn ? 'On' : 'Off'}
        </button>
      </div>
      {shortfallMw > 0 && importOn && (
        <p className="hint">
          Importing {formatPower(importedMw)} this tick — {fmtMoney(importCostPerTick)}/tick (Grid Import,
          shown in the Earnings tab).
        </p>
      )}
      {shortfallMw > 0 && !importOn && (
        <p className="hint warn-text">
          Shortfall not covered — brownout active, powered business income is reduced. Toggle external
          cover back on, or build more local capacity.
        </p>
      )}
      {shortfallMw === 0 && (
        <p className="hint">No shortfall — capacity meets or exceeds demand.</p>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Water (§1 row 14 — direct relocation, unchanged content).
// ---------------------------------------------------------------------------
export function WaterTab() {
  const { state, dispatch } = useSim();
  const bal = waterBalanceOf(state);
  const demand = waterDemandOf(state);
  const pipeInfo = waterPipeInfo(state);
  const plantUtil = new Map(pipeInfo.plants.map((p) => [p.id, p]));
  const plants = state.buildings.filter((b) => SPECS[b.spec]?.kind === 'water');
  const cleanHeadroom = bal.clean - demand.clean;
  const wasteHeadroom = bal.waste - demand.waste;
  // FEAT-2326609711 inc2 (AC-9/AC-10): external cover toggles for clean
  // water and wastewater treatment — same shape as PowerTab's toggle above.
  const waterOn = state.waterImportEnabled ?? WATER_IMPORT_ENABLED_DEFAULT;
  const waterShortfall = Math.max(0, demand.clean - bal.clean);
  const waterImportedPersons = waterOn ? waterShortfall : 0;
  // BUG-1050 (GR#3, no local arithmetic): call the fiscal SSOT.
  const waterImportCostPerTick = utilityBuyInCostPerTick(bal.clean, demand.clean, WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK);
  const wastewaterOn = state.wastewaterContractEnabled ?? WASTEWATER_CONTRACT_ENABLED_DEFAULT;
  const wastewaterShortfall = Math.max(0, demand.waste - bal.waste);
  const wastewaterImportedPersons = wastewaterOn ? wastewaterShortfall : 0;
  const wastewaterContractCostPerTick = utilityBuyInCostPerTick(
    bal.waste,
    demand.waste,
    WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK
  );
  // BUG-1061 (supersedes the r2 BUG-1048 gate): the leak banner/tile are a
  // CONSEQUENCE display, the exact UI twin of approvalOf's -5 penalty — so
  // it must gate on the same CONTRACT TOGGLE, not on shortage-existence
  // (isWastewaterShortageActive stays false on an over-built-clean-network
  // leak even with the contract OFF, which had wrongly hidden the legacy
  // warning on the byte-identical-with-main path). Contract ON -> fully
  // substitutes, neutral line; contract OFF -> the raw physical-fact
  // bal.leak drives the legacy "-5 approval" warning, matching main.
  const leakConsequenceActive = bal.leak && !wastewaterOn;
  return (
    <>
      <div className="tiles">
        <div className={`tile ${cleanHeadroom < 0 ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(bal.clean)}</div>
          <div className="l">Clean capacity</div>
        </div>
        <div className={`tile ${wasteHeadroom < 0 || leakConsequenceActive ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(bal.waste)}</div>
          <div className="l">Discharge capacity</div>
        </div>
      </div>
      <div className="tiles">
        <div className={`tile ${cleanHeadroom < 0 ? 'neg' : ''}`}>
          <div className="n">{fmtNum(demand.clean)}</div>
          <div className="l">Clean demand</div>
        </div>
        <div className={`tile ${wasteHeadroom < 0 ? 'neg' : ''}`}>
          <div className="n">{fmtNum(demand.waste)}</div>
          <div className="l">Waste demand</div>
        </div>
      </div>
      <p className="hint">
        Clean headroom {fmtNum(cleanHeadroom)} · discharge headroom {fmtNum(wasteHeadroom)}{' '}
        (capacity − demand; negative = the network is over capacity and short).
      </p>
      <div className="wb-row">
        <div>
          <b>Use external water cover</b>
          <p className="muted">
            Buys in any clean-water shortfall at {fmtMoneyEach(WATER_IMPORT_TARIFF_PER_PERSON_PER_TICK)}
            /person/tick instead of a shortage. Off applies the legacy shortage penalty.
          </p>
        </div>
        <button
          className={`btn toggle ${waterOn ? 'on' : ''}`}
          onClick={() => dispatch({ type: 'toggleWaterImport' })}
        >
          {waterOn ? 'On' : 'Off'}
        </button>
      </div>
      {waterShortfall > 0 && waterOn && (
        <p className="hint">
          Importing {fmtNum(waterImportedPersons)} persons' worth this tick —{' '}
          {fmtMoney(waterImportCostPerTick)}/tick (Water Import, shown in the Earnings tab).
        </p>
      )}
      {waterShortfall > 0 && !waterOn && (
        <p className="hint warn-text">
          Clean-water shortfall not covered — legacy shortage applies. Toggle external cover back on,
          or build more local capacity.
        </p>
      )}
      <div className="wb-row">
        <div>
          <b>Use external sewage cover</b>
          <p className="muted">
            Buys in any wastewater shortfall at{' '}
            {fmtMoneyEach(WASTEWATER_CONTRACT_TARIFF_PER_PERSON_PER_TICK)}/person/tick instead of a
            shortage. Off applies the legacy shortage penalty.
          </p>
        </div>
        <button
          className={`btn toggle ${wastewaterOn ? 'on' : ''}`}
          onClick={() => dispatch({ type: 'toggleWastewaterContract' })}
        >
          {wastewaterOn ? 'On' : 'Off'}
        </button>
      </div>
      {wastewaterShortfall > 0 && wastewaterOn && (
        <p className="hint">
          Contracting {fmtNum(wastewaterImportedPersons)} persons' worth this tick —{' '}
          {fmtMoney(wastewaterContractCostPerTick)}/tick (Waste-Water Contract, shown in the Earnings
          tab).
        </p>
      )}
      {wastewaterShortfall > 0 && !wastewaterOn && (
        <p className="hint warn-text">
          Wastewater shortfall not covered — legacy shortage applies. Toggle external cover back on,
          or build more local capacity.
        </p>
      )}
      {leakConsequenceActive && (
        <p className="hint warn-text">
          Leakage risk: discharge is below 80% of clean capacity — sewage backs up (-5 approval).
          Build a Waste-Water Plant or upgrade pipes.
        </p>
      )}
      {bal.leak && !leakConsequenceActive && (
        <p className="hint">
          Discharge is below 80% of clean capacity, but the Waste-Water Contract is covering the
          shortfall — no approval penalty while the contract is on.
        </p>
      )}
      {!bal.leak && bal.clean > 0 && bal.waste > 0 && (
        <p className="hint">
          Network balanced — discharge/clean ratio {(bal.ratio * 100).toFixed(0)}% (keep above 80%).
        </p>
      )}
      <h4>Plants &amp; pipes</h4>
      <table className="table">
        <thead>
          <tr><th>Plant</th><th>Grid</th><th>Pipe</th><th>Served</th><th>Pipe use</th><th /></tr>
        </thead>
        <tbody>
          {plants.length === 0 && (
            <tr><td colSpan={6} className="muted">No water infrastructure yet.</td></tr>
          )}
          {plants.map((b) => {
            const sp = SPECS[b.spec];
            const tier = state.pipeTier[b.id] ?? 0;
            const eff = plantEffServed(state, b);
            const next = PIPE_TIERS[tier + 1];
            const pu = plantUtil.get(b.id);
            const atCeiling = pu?.atCeiling ?? false;
            return (
              // BUG-625: id+x+y, not id alone — see ConstructionQueue.tsx's
              // note. `b.id` is the raw SimState building id, which a
              // desynced `state.nextId` (BUG-413 class, reachable via a
              // savepoint/rebuild path that doesn't resync it against the
              // real max building id at scale) can hand to two DIFFERENT
              // buildings. Position is occupancy-checked at placement time,
              // so id+x+y stays unique even when id alone collides.
              <tr key={`${b.id}-${b.x}-${b.y}`}>
                <td className={sp.tag === 'clean' ? 'in' : 'out'}>{sp.name}</td>
                <td className="mono">{b.x},{b.y}</td>
                <td>{PIPE_TIERS[tier].label}</td>
                <td>{fmtNum(eff)}</td>
                <td className={atCeiling ? 'neg' : ''} title={atCeiling
                  ? 'Widest main fitted — this pipe is at capacity; add another plant to grow'
                  : 'Diameter headroom: this pipe can still be upgraded to a wider main'}>
                  {fmtPct(pu?.tierUtil ?? 0)}{atCeiling ? ' • max' : ''}
                </td>
                <td>
                  {next && (
                    <button
                      className="btn tiny"
                      title={`Upgrade to ${next.label} — ${fmtMoney(next.upgradeCost)}`}
                      disabled={state.funds < next.upgradeCost}
                      onClick={() => dispatch({ type: 'pipeUpgrade', id: b.id })}
                    >
                      ↑ {fmtMoney(next.upgradeCost)}
                    </button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="hint">
        Clean plants draw from the aquifer via their abstraction pipe (cyan stub on the map);
        waste plants must discharge seaward (olive stub). Pipe capacity caps each plant's served
        population — upgrade when demand exceeds it.
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// Waste & Recycling (§1 row 15 — direct relocation, unchanged content).
// ---------------------------------------------------------------------------
export function WasteTab() {
  const { state, dispatch } = useSim();
  const m = wasteDisplayModel(state);
  const coveragePct = Math.round(m.coverage * 100);
  // FEAT-2326609711 inc2 rework (BUG-1027, LEAD RULING point 2): m.hasUncollected
  // itself is now the fixed SSOT (wasteModel.ts routes it through data.ts's
  // isRefuseShortageActive instead of raw `uncollected > 0`), so the RAG /
  // tooltip / banner below read it unchanged — they can no longer assert a
  // penalty the engine has in fact suppressed while the refuse contract is ON.
  const shortageActive = m.hasUncollected;
  const rag = ragForWasteCollection(shortageActive);
  const covCol = ragColor(rag);
  const divPct = Math.round(m.diversionRate * 100);
  const divCol = 'var(--done)';
  // FEAT-2326609711 inc2 (AC-9/AC-10): external cover toggle for refuse
  // collection — same shape as PowerTab/WaterTab's toggles.
  const refuseOn = state.refuseContractEnabled ?? REFUSE_CONTRACT_ENABLED_DEFAULT;
  const refuseShortfallTonnes = Math.max(0, m.generated - m.capacity);
  const refuseContractedTonnes = refuseOn ? refuseShortfallTonnes : 0;
  // BUG-1050 (GR#3, no local arithmetic): call the fiscal SSOT.
  const refuseContractCostPerTick = utilityBuyInCostPerTick(m.capacity, m.generated, REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK);
  return (
    <>
      <div className="tiles">
        <div className={`tile ${shortageActive ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(m.generated)}</div>
          <div className="l">Generated t/tick</div>
        </div>
        <div className={`tile ${shortageActive ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(m.capacity)}</div>
          <div className="l">Collection cap</div>
        </div>
      </div>
      <div className="wb-row">
        <div>
          <b>Use contracted refuse collection</b>
          <p className="muted">
            Contracts out any uncollected shortfall at{' '}
            {fmtMoneyEach(REFUSE_CONTRACT_TARIFF_PER_TONNE_PER_TICK)}/tonne/tick instead of leaving it
            on the street. Off applies the legacy shortage penalty.
          </p>
        </div>
        <button
          className={`btn toggle ${refuseOn ? 'on' : ''}`}
          onClick={() => dispatch({ type: 'toggleRefuseContract' })}
        >
          {refuseOn ? 'On' : 'Off'}
        </button>
      </div>
      {refuseShortfallTonnes > 0 && refuseOn && (
        <p className="hint">
          Contracting {fmtNum(refuseContractedTonnes)} t/tick this tick —{' '}
          {fmtMoney(refuseContractCostPerTick)}/tick (Contracted Refuse, shown in the Earnings tab).
        </p>
      )}
      {refuseShortfallTonnes > 0 && !refuseOn && (
        <p className="hint warn-text">
          Uncollected shortfall not covered — legacy shortage applies. Toggle contracted collection
          back on, or build more local capacity.
        </p>
      )}
      <h4>Collection coverage</h4>
      <div className="wb-row" title={
        `${fmtNum(m.collected)} / ${fmtNum(m.generated)} t collected` +
        ` — uncollected ${fmtNum(m.uncollected)} t${shortageActive ? ' (LEFT ON THE STREET)' : ''}`
      }>
        <span className="d-label">Collected</span>
        <div className="d-bar">
          <span
            className={`d-fill ${shortageActive ? 'neg' : 'pos'}`}
            style={{ left: 0, width: `${Math.max(0, Math.min(100, coveragePct))}%`, background: covCol }}
          />
        </div>
        <span className="mono d-val" style={{ color: covCol }}>
          {coveragePct}%
        </span>
      </div>
      {shortageActive ? (
        <p className="hint warn-text">
          {fmtNum(m.uncollected)} t/tick left uncollected — refuse accumulates and drives the
          waste-health penalty. Build more Refuse Depots to raise coverage.
        </p>
      ) : refuseShortfallTonnes > 0 ? (
        <p className="hint">
          Contracted refuse collecting the shortfall — {fmtNum(refuseShortfallTonnes)} t/tick bought
          in via {REFUSE_CONTRACT_OUTFLOW_LABEL} at {fmtMoney(refuseContractCostPerTick)}/tick, no
          waste-health penalty while the contract is on.
        </p>
      ) : (
        <p className="hint">
          All generated refuse is collected (capacity ≥ generation). Green = headroom; red = refuse
          left on the street (capacity − generated &lt; 0).
        </p>
      )}
      <h4>Diversion rate</h4>
      <div className="wb-row" title={
        `${fmtNum(m.diverted)} t diverted / ${fmtNum(m.collected)} t collected` +
        ` — ${fmtNum(m.landfilled)} t to landfill`
      }>
        <span className="d-label">Recycled / recovered</span>
        <div className="d-bar">
          <span
            className="d-fill pos"
            style={{ left: 0, width: `${Math.max(0, Math.min(100, divPct))}%`, background: divCol }}
          />
        </div>
        <span className="mono d-val" style={{ color: divCol }}>
          {fmtPct(m.diversionRate, 0)}
        </span>
      </div>
      <p className="hint">
        Diversion % = tonnage kept out of landfill (EfW + recycling + compost) ÷ collected. The
        total-recycling KPI — build MRF / compost / EfW capacity to drive it toward 100%.
      </p>
      <h4>Processing mix</h4>
      <table className="table">
        <thead>
          <tr><th>Route</th><th>Tonnes/tick</th><th>Share</th></tr>
        </thead>
        <tbody>
          {m.collected === 0 && (
            <tr><td colSpan={3} className="muted">Nothing collected yet — no refuse to process.</td></tr>
          )}
          {m.collected > 0 && m.mixRows.map((r) => (
            <tr key={r.key}>
              <td className={r.isSink ? 'out' : 'in'}>{r.label}</td>
              <td>{fmtNum(r.tonnes)}</td>
              <td className="mono">{fmtPct(r.fraction, 0)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h4>Recovered</h4>
      <div className="tiles">
        <div className="tile">
          <div className="n">{formatPower(m.efwPowerMw)}</div>
          <div className="l">EfW power</div>
        </div>
        <div className="tile in">
          <div className="n">{fmtMoney(m.materialRevenue)}</div>
          <div className="l">Material revenue</div>
        </div>
      </div>
      <p className="hint">
        EfW power feeds the grid (surplus sells as Grid Export). Material revenue = recycling{' '}
        {fmtMoney(m.recyclingRevenue)} + compost {fmtMoney(m.compostRevenue)} per tick. Balance
        numbers are placeholder pending sign-off.
      </p>
    </>
  );
}

// ---------------------------------------------------------------------------
// Utilities — Aaron's domain-split wrapper: Power/Water/Waste as sibling
// sub-tabs under ONE "Utilities" child tab, each tab's content kept intact.
// ---------------------------------------------------------------------------
const UTILITY_SUBTABS = [
  { id: 'power', label: 'Power' },
  { id: 'water', label: 'Water' },
  { id: 'waste', label: 'Waste & Recycling' },
];

export function UtilitiesTab() {
  const [sub, setSub] = useState('power');
  return (
    <>
      <TabStrip tabs={UTILITY_SUBTABS} active={sub} onSelect={setSub} />
      <div className="panel-body sub-tab-body">
        {sub === 'power' && <PowerTab />}
        {sub === 'water' && <WaterTab />}
        {sub === 'waste' && <WasteTab />}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Coverage grid rows shared by Education/Health/Safety (AC-5's NEW-tab rows,
// re-partitioned by domain per Aaron's split). Pure presentational — every
// number comes straight from serviceCoverageOf() (SSOT, GR#3).
// ---------------------------------------------------------------------------
function CoverageGrid({ rows }: { rows: ServiceCoverage[] }) {
  if (rows.length === 0) {
    return <p className="muted">No coverage rows for this domain.</p>;
  }
  return (
    <table className="table">
      <thead>
        <tr><th>Service</th><th>Need</th><th>Capacity</th><th>Coverage</th></tr>
      </thead>
      <tbody>
        {rows.map((r) => {
          const rag = ragForCoverage(r.coverage);
          const col = ragColor(rag);
          const pct = Math.round(r.coverage * 100);
          return (
            <tr key={r.id}>
              <td>{r.label}</td>
              <td className="mono">{fmtNum(r.need)}</td>
              <td className="mono">{fmtNum(r.cap)}</td>
              <td className="mono" style={{ color: col }}>{pct}%</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

export function EducationTab() {
  const { state } = useSim();
  const rows = serviceCoverageOf(state).filter((r) => ['nursery', 'primary', 'college'].includes(r.id));
  return (
    <>
      <p className="hint">Nursery / Primary / College coverage — need is population-derived (PLACEHOLDER rates).</p>
      <CoverageGrid rows={rows} />
    </>
  );
}

export function HealthTab() {
  const { state } = useSim();
  const rows = serviceCoverageOf(state).filter((r) => ['gp', 'hosp'].includes(r.id));
  return (
    <>
      <p className="hint">GP clinics / Hospital coverage.</p>
      <CoverageGrid rows={rows} />
    </>
  );
}

export function SafetyTab() {
  const { state } = useSim();
  const rows = serviceCoverageOf(state).filter((r) => ['fire', 'police'].includes(r.id));
  return (
    <>
      <p className="hint">Fire / Police coverage.</p>
      <CoverageGrid rows={rows} />
    </>
  );
}
