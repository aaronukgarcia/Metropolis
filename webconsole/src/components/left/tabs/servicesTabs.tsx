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
import { GRID_IMPORT_TARIFF_PER_MW, GRID_IMPORT_ENABLED_DEFAULT } from '../../../sim/fiscal';
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
  const importCostPerTick = Math.round(importedMw * GRID_IMPORT_TARIFF_PER_MW);
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
  return (
    <>
      <div className="tiles">
        <div className={`tile ${cleanHeadroom < 0 ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(bal.clean)}</div>
          <div className="l">Clean capacity</div>
        </div>
        <div className={`tile ${wasteHeadroom < 0 || bal.leak ? 'neg' : 'pos'}`}>
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
      {bal.leak && (
        <p className="hint warn-text">
          Leakage risk: discharge is below 80% of clean capacity — sewage backs up (-5 approval).
          Build a Waste-Water Plant or upgrade pipes.
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
              <tr key={b.id}>
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
  const { state } = useSim();
  const m = wasteDisplayModel(state);
  const coveragePct = Math.round(m.coverage * 100);
  const rag = ragForWasteCollection(m.hasUncollected);
  const covCol = ragColor(rag);
  const divPct = Math.round(m.diversionRate * 100);
  const divCol = 'var(--done)';
  return (
    <>
      <div className="tiles">
        <div className={`tile ${m.hasUncollected ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(m.generated)}</div>
          <div className="l">Generated t/tick</div>
        </div>
        <div className={`tile ${m.hasUncollected ? 'neg' : 'pos'}`}>
          <div className="n">{fmtNum(m.capacity)}</div>
          <div className="l">Collection cap</div>
        </div>
      </div>
      <h4>Collection coverage</h4>
      <div className="wb-row" title={
        `${fmtNum(m.collected)} / ${fmtNum(m.generated)} t collected` +
        ` — uncollected ${fmtNum(m.uncollected)} t${m.hasUncollected ? ' (LEFT ON THE STREET)' : ''}`
      }>
        <span className="d-label">Collected</span>
        <div className="d-bar">
          <span
            className={`d-fill ${m.hasUncollected ? 'neg' : 'pos'}`}
            style={{ left: 0, width: `${Math.max(0, Math.min(100, coveragePct))}%`, background: covCol }}
          />
        </div>
        <span className="mono d-val" style={{ color: covCol }}>
          {coveragePct}%
        </span>
      </div>
      {m.hasUncollected ? (
        <p className="hint warn-text">
          {fmtNum(m.uncollected)} t/tick left uncollected — refuse accumulates and drives the
          waste-health penalty. Build more Refuse Depots to raise coverage.
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
