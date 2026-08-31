import { useEffect, useState } from 'react';
import { useSim } from '../../sim/simContext';
import { levelOf, xpForLevel, wellbeingOf, approvalOf } from '../../sim/engine';
import {
  MILESTONES,
  POLICIES,
  UNIT_REGISTRY,
  countByKind,
  FAMILIES,
  SPECS,
  PIPE_TIERS,
  PHYSICAL_ENTITIES,
  waterBalanceOf,
  waterDemandOf,
  waterPipeInfo,
  plantEffServed,
  lineUsageOf,
  onlineResidentsCapacity,
  offlineResidentsByReason,
} from '../../sim/data';
import type { PolicyId, TaxRates } from '../../sim/types';
import { Panel } from '../Tabs';
import { fmtMoney, fmtMoneyEach, fmtNum, fmtPct, formatPower } from '../../sim/utils';
import { wasteDisplayModel } from './wasteModel';
import { useBusy } from '../Busy';
import { commitDebug, errorListModel, pendingCommits, recentErrors } from '../../sim/backend';
import { debugActions } from '../../sim/debugactions';
import { buildDebugJson, debugJsonText } from '../../sim/debugjson';
import { currentMapUi } from '../../sim/uistate';
import { versionRaw } from '../../sim/version';
import { nextRefreshDue } from '../../sim/throttle';

const TABS = [
  { id: 'status', label: 'Status' },
  { id: 'rates', label: 'Rates' },
  { id: 'units', label: 'Units' },
  { id: 'water', label: 'Water' },
  { id: 'waste', label: 'Waste' },
  { id: 'lines', label: 'Lines' },
  { id: 'earnings', label: 'Earnings' },
  { id: 'milestones', label: 'Milestones' },
  { id: 'xp', label: 'Experience' },
  { id: 'specialists', label: 'Specialists' },
  { id: 'policy', label: 'Policy' },
  { id: 'debug', label: 'Debug' },
];

const TAX_LABELS: Record<keyof TaxRates, string> = {
  residential: 'Council tax',
  commercial: 'Business tax',
  industrial: 'Freight tax',
};

export function RightDock() {
  const [tab, setTab] = useState('status');
  return (
    <Panel title="Information" tabs={TABS} active={tab} onSelect={setTab}>
      {tab === 'status' && <StatusTab />}
      {tab === 'rates' && <RatesTab />}
      {tab === 'units' && <UnitsTab />}
      {tab === 'water' && <WaterTab />}
      {tab === 'waste' && <WasteTab />}
      {tab === 'lines' && <LinesTab />}
      {tab === 'earnings' && <EarningsTab />}
      {tab === 'milestones' && <MilestonesTab />}
      {tab === 'xp' && <XpTab />}
      {tab === 'specialists' && <SpecialistsTab />}
      {tab === 'policy' && <PolicyTab />}
      {tab === 'debug' && <DebugTab />}
    </Panel>
  );
}

function StatusTab() {
  const { state } = useSim();
  // BUG-417: the headline "Housing cap" is the ONLINE figure — the capacity
  // engine growth can actually fill (offline / under-construction dwellings do
  // not house anyone yet). The gross total stays visible as a "+N under
  // construction" breakdown so the mismatch is legible, not hidden.
  const capacity = onlineResidentsCapacity(state);
  // BUG-394: split the offline "+N" by root cause so the pop-freeze is legible.
  // `disconnected` is the ACTIONABLE reason (dwellings built but off the road
  // network — they never house anyone until the player connects roads);
  // `building` self-resolves once construction finishes.
  const { construction: buildingResidents, disconnected } = offlineResidentsByReason(state);
  const approval = approvalOf(state);
  const wb = wellbeingOf(state);
  const income = state.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  return (
    <>
      <div className="tiles">
        <div className={`tile ${approval >= 55 ? 'pos' : 'neg'}`}>
          <div className="n">{approval}</div>
          <div className="l">Approval</div>
        </div>
        <div className={`tile ${wb.overall >= 55 ? 'pos' : 'neg'}`}>
          <div className="n">{wb.overall}</div>
          <div className="l">Wellbeing</div>
        </div>
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
      <h4>Wellbeing breakdown</h4>
      <div className="wb-list">
        {wb.parts.map((p) => {
          const col = p.value >= 70 ? 'var(--done)' : p.value >= 45 ? 'var(--warn)' : 'var(--danger)';
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
      <h4>Structures</h4>
      <table className="table">
        <thead>
          <tr><th>Type</th><th>Count</th><th>Upkeep</th></tr>
        </thead>
        <tbody>
          {FAMILIES.map((fam) => {
            let count = 0;
            let upkeep = 0;
            for (const b of state.buildings) {
              const sp = SPECS[b.spec];
              if (sp?.kind === fam.kind) {
                count++;
                upkeep += sp.upkeep;
              }
            }
            if (count === 0) return null;
            return (
              <tr key={fam.kind}>
                <td>
                  <span className="swatch" style={{ background: fam.color }} />
                  {fam.label}
                </td>
                <td>{count}</td>
                <td>{fmtMoney(upkeep)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="hint">
        Fiscal state {income - expense >= 0 ? 'solvent' : 'in deficit'} · net{' '}
        {fmtMoney(income - expense)}/tick
      </p>
    </>
  );
}

function RatesTab() {
  const { state, dispatch } = useSim();
  const c = countByKind(state.buildings);
  const yields: Record<keyof TaxRates, number> = {
    residential: Math.round((state.population * state.taxRates.residential * 2) / 100),
    commercial: Math.round(c.commercial * state.taxRates.commercial * 0.4),
    industrial: Math.round(c.industrial * state.taxRates.industrial * 0.55),
  };
  const bases: Record<keyof TaxRates, string> = {
    residential: `${fmtNum(state.population)} citizens × ${fmtMoney(2)} × rate`,
    commercial: `${fmtNum(c.commercial)} zones × ${fmtMoney(40)} × rate`,
    industrial: `${fmtNum(c.industrial)} plants × ${fmtMoney(55)} × rate`,
  };
  return (
    <>
      {(Object.keys(TAX_LABELS) as (keyof TaxRates)[]).map((k) => (
        <div key={k} className="slider-row">
          <label>
            <span>{TAX_LABELS[k]}</span>
            <b>{state.taxRates[k]}%</b>
          </label>
          <input
            type="range"
            min={0}
            max={30}
            value={state.taxRates[k]}
            onChange={(e) => dispatch({ type: 'tax', which: k, rate: Number(e.target.value) })}
          />
          <p className="hint">
            {bases[k]} → <b className="in">{fmtMoney(yields[k])}/tick</b>
          </p>
        </div>
      ))}
      <p className="hint warn-text">
        High average rates suppress migration and approval. Current avg{' '}
        {(
          (state.taxRates.residential + state.taxRates.commercial + state.taxRates.industrial) /
          3
        ).toFixed(1)}
        %
      </p>
    </>
  );
}

function UnitsTab() {
  return (
    <>
      <p className="hint">Registry-sourced units — every displayed figure derives from these dimensions.</p>
      <table className="table">
        <thead>
          <tr><th>Unit</th><th>Dimension</th><th>Note</th></tr>
        </thead>
        <tbody>
          {UNIT_REGISTRY.map((u) => (
            <tr key={u.unit}>
              <td className="mono">{u.unit}</td>
              <td>{u.dimension}</td>
              <td className="muted">{u.note}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h4>Physical entities (metres)</h4>
      <table className="table">
        <thead>
          <tr><th>Entity</th><th>L × W × H</th></tr>
        </thead>
        <tbody>
          {PHYSICAL_ENTITIES.map((e) => (
            <tr key={e.id}>
              <td>{e.label}</td>
              <td className="mono">{e.x} × {e.y} × {e.z} m</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

function WaterTab() {
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

// FEAT-1972079906 inc3 (FEAT-1972079864): the waste/recycling rota read-out —
// collection coverage, the processing mix + diversion %, and recovered power/
// materials. Display-only: wasteDisplayModel is a pure reshaping of the LANDED
// inc1/inc2 derived reads (wasteStatsOf / processingMixOf / efwPowerOf / revenue
// fns) — nothing is stored in SimState and no policy controls (those need the
// unbuilt policy framework, out of scope). Meters reuse the water/Lines d-bar
// idiom and the BUG-425 surplus/shortfall colour split (over/uncollected → `neg`
// danger, headroom/diversion → `pos` done). Formatters are the shared SSOT ones.
export function WasteTab() {
  const { state } = useSim();
  const m = wasteDisplayModel(state);
  const coveragePct = Math.round(m.coverage * 100);
  const covCol = m.hasUncollected ? 'var(--danger)' : 'var(--done)';
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

// FEAT-1972079902 rail-inc1: line capacity / commuter-flow usage / saturation.
// Display-only read-out (lineUsageOf is pure/derived — nothing stored in SimState).
// Colours reuse the BUG-425 surplus-vs-shortfall split: over-capacity → `neg`
// (danger), headroom → `pos` (done) — same tokens the water panel uses.
function LinesTab() {
  const { state } = useSim();
  const lines = lineUsageOf(state);
  return (
    <>
      <h4>Line saturation</h4>
      {lines.length === 0 && (
        <p className="muted">No road or rail lines on the map yet.</p>
      )}
      {lines.map((ln) => {
        const pct = Math.round(ln.saturation * 100);
        const over = ln.overCapacity;
        const col = over ? 'var(--danger)' : 'var(--done)';
        return (
          <div key={ln.spec} className="wb-row" title={
            `${ln.name}: ${fmtNum(ln.usage)} / ${fmtNum(ln.capacity)} per tick across ${ln.tiles} tile${ln.tiles === 1 ? '' : 's'}` +
            ` — headroom ${fmtNum(ln.headroom)}${over ? ' (OVER CAPACITY)' : ''}`
          }>
            <span className="d-label">{ln.name}</span>
            <div className="d-bar">
              <span
                className={`d-fill ${over ? 'neg' : 'pos'}`}
                style={{ left: 0, width: `${Math.max(0, Math.min(100, pct))}%`, background: col }}
              />
            </div>
            <span className="mono d-val" style={{ color: col }}>
              {pct}%
            </span>
          </div>
        );
      })}
      {lines.length > 0 && (
        <p className="hint">
          Usage is commuter flow (rail/HS1, from connected stations) or traffic demand (road/M20),
          against per-tile capacity. Green = headroom; red = over capacity (capacity − usage &lt; 0).
          Capacities are placeholder-balance pending sign-off.
        </p>
      )}
    </>
  );
}

function EarningsTab() {
  const { state } = useSim();
  const c = countByKind(state.buildings);
  const rows = [
    {
      type: 'Residential',
      count: Math.max(state.population, 1),
      unit: 'per citizen',
      gross: state.lastFlows.inflows.find((f) => f.label === 'Council Tax')?.value ?? 0,
    },
    {
      type: 'Commercial',
      count: Math.max(c.commercial, 1),
      unit: 'per zone',
      gross: state.lastFlows.inflows.find((f) => f.label === 'Business Tax')?.value ?? 0,
    },
    {
      type: 'Offices',
      count: Math.max(
        state.buildings.filter((b) => SPECS[b.spec]?.kind === 'office').length,
        1
      ),
      unit: 'per block',
      gross: state.lastFlows.inflows.find((f) => f.label === 'Office Tax')?.value ?? 0,
    },
    {
      type: 'Industrial',
      count: Math.max(c.industrial, 1),
      unit: 'per plant',
      gross: state.lastFlows.inflows.find((f) => f.label === 'Freight Tax')?.value ?? 0,
    },
    {
      type: 'Tourism',
      count: state.policies.tourismDrive ? Math.max(state.population, 1) : 0,
      unit: 'policy-driven',
      gross: state.lastFlows.inflows.find((f) => f.label === 'Tourism')?.value ?? 0,
    },
  ];
  const totalOut = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const totalIn = rows.reduce((a, r) => a + r.gross, 0);
  return (
    <table className="table">
      <thead>
        <tr><th>Type</th><th>Basis</th><th>Gross/tick</th><th>Each</th></tr>
      </thead>
      <tbody>
        {rows.map((r) => (
          <tr key={r.type}>
            <td>{r.type}</td>
            <td className="muted">{r.count > 0 ? `${fmtNum(r.count)} ${r.unit}` : '—'}</td>
            <td className="in">{fmtMoney(r.gross)}</td>
            <td>{r.count > 0 ? fmtMoneyEach(r.gross / r.count) : '—'}</td>
          </tr>
        ))}
        <tr>
          <td><b>Total in</b></td><td /><td className="in"><b>{fmtMoney(totalIn)}</b></td><td />
        </tr>
        <tr>
          <td><b>Total out</b></td><td /><td className="out"><b>{fmtMoney(-totalOut)}</b></td><td />
        </tr>
        <tr>
          <td><b>Margin</b></td><td />
          <td className={totalIn - totalOut >= 0 ? 'in' : 'out'}>
            <b>{totalIn > 0 ? fmtPct((totalIn - totalOut) / totalIn) : '—'}</b>
          </td>
          <td />
        </tr>
      </tbody>
    </table>
  );
}

function MilestonesTab() {
  const { state } = useSim();
  return (
    <ul className="milestone-list">
      {MILESTONES.map((m) => {
        const met = m.test(state);
        return (
          <li key={m.id} className={met ? 'met' : ''}>
            <span className="ms-dot" />
            <div>
              <b>{m.label}</b>
              <p className="muted">{m.detail}</p>
            </div>
            <span className={`chip ${met ? 'done' : 'open'}`}>{met ? 'Met' : 'Open'}</span>
          </li>
        );
      })}
    </ul>
  );
}

function XpTab() {
  const { state } = useSim();
  const level = levelOf(state.xp);
  const cur = xpForLevel(level);
  const next = xpForLevel(level + 1);
  const frac = next > cur ? (state.xp - cur) / (next - cur) : 0;
  // FEAT-1972079877: ladder runs the full 1–20 spread now that the placeholder
  // catalogue populates every level (99 = always-available seed infrastructure).
  const byUnlock = new Map<number, string[]>();
  for (const sp of Object.values(SPECS)) {
    if (sp.unlock > 20 || sp.category === 'network') continue;
    const list = byUnlock.get(sp.unlock) ?? [];
    list.push(sp.name);
    byUnlock.set(sp.unlock, list);
  }
  return (
    <>
      <div className="xp-head">
        <span className="level-badge">{level}</span>
        <div>
          <b>City level {level}</b>
          <p className="muted">
            {fmtNum(state.xp)} XP · {fmtNum(Math.max(0, next - state.xp))} to level {level + 1}
          </p>
        </div>
      </div>
      <div className="xp-bar">
        <div style={{ width: `${Math.min(100, frac * 100)}%` }} />
      </div>
      <h4>Unlock ladder</h4>
      <table className="table">
        <thead>
          <tr><th>Lv</th><th>Unlocks</th></tr>
        </thead>
        <tbody>
          {Array.from({ length: 20 }, (_, i) => i + 1).map((lv) => (
            <tr key={lv} className={level >= lv ? '' : 'muted'}>
              <td>{lv}</td>
              <td>{(byUnlock.get(lv) ?? []).join(', ') || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="hint">XP accrues per tick (+1) and per structure placed (+4).</p>
    </>
  );
}

function SpecialistsTab() {
  const { state } = useSim();
  const level = levelOf(state.xp);
  const landmarks = Object.values(SPECS).filter((sp) => sp.kind === 'landmark' || sp.id === 'uni');
  return (
    <ul className="specialist-list">
      {landmarks.map((sp) => {
        const locked = level < sp.unlock;
        const count = state.buildings.filter((b) => b.spec === sp.id).length;
        return (
          <li key={sp.id} className={locked ? 'locked' : ''}>
            <div>
              <b>{sp.name}</b>
              <p className="muted">{sp.blurb}</p>
            </div>
            <span className={`chip ${locked ? 'blocked' : 'done'}`}>
              {locked ? `Lv ${sp.unlock}` : `${count} built`}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function PolicyTab() {
  const { state, dispatch } = useSim();
  return (
    <ul className="policy-list">
      {POLICIES.map((p) => (
        <li key={p.id}>
          <div>
            <b>{p.label}</b>
            <p className="muted">{p.description}</p>
          </div>
          <button
            className={`btn toggle ${state.policies[p.id as PolicyId] ? 'on' : ''}`}
            onClick={() => dispatch({ type: 'policy', id: p.id as PolicyId })}
          >
            {state.policies[p.id as PolicyId] ? 'On' : 'Off'}
          </button>
        </li>
      ))}
    </ul>
  );
}

// FEAT-1972079880: the snapshot JSON used to be rebuilt from live state on
// EVERY render — the sim context updates each tick (as fast as 160 ms), so the
// <pre> text mutated constantly and text selection was destroyed before a
// human could copy it. The panel now freezes a "frame" (formatted text + the
// data it came from) and only retakes it when nextRefreshDue says a full
// SNAPSHOT_REFRESH_MS (15 s) has passed. Between refreshes the <pre> renders
// the SAME string, so React never touches the DOM text node and selection
// survives; the element itself is never re-mounted (stable tree position, no
// key changes). A 1 s wall-clock interval drives the countdown label — that
// updates a SIBLING element, which does not disturb selection inside the pre.
function DebugTab() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(pendingCommits());

  // FEAT-1972079886: the frame is now the FULL-STATE debug.json — every UI
  // tab's status, raw numbers — built by the pure serializer in debugjson.ts.
  // Non-sim inputs (version, wall clock, map camera, captured errors) are
  // gathered HERE, at frame time, so the builder stays pure and the frozen
  // frame is a complete, self-consistent capture of that instant.
  const takeFrame = (s: typeof state) => {
    const at = Date.now();
    const dj = buildDebugJson(s, {
      appVersion: versionRaw,
      frameAtMs: at,
      map: currentMapUi(),
      errors: recentErrors(),
    });
    return { at, dj, text: debugJsonText(dj) };
  };
  const [frame, setFrame] = useState(() => takeFrame(state));
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  // Retake the frame only when the 15 s window has elapsed. The effect closure
  // is recreated every render, so when `now` ticks it sees the LATEST state.
  useEffect(() => {
    if (nextRefreshDue(frame.at, now).due) setFrame(takeFrame(state));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [now]);

  const remainingS = Math.ceil(nextRefreshDue(frame.at, now).remainingMs / 1000);

  function commit() {
    run(async () => {
      setStatus('Committing debug.json…');
      // Commit exactly the frame on screen (WYSIWYG), not fresher live state.
      const r = await commitDebug(frame.dj);
      setStatus(r.message);
      setPending(pendingCommits());
    });
  }

  // Download the EXACT on-screen frozen text as debug.json (WYSIWYG — the file
  // is byte-identical to the <pre> contents, not a fresher re-serialization).
  function download() {
    const blob = new Blob([frame.text], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'debug.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }
  // FEAT-1972079885: the state-mutating cheats are DEV-gated exactly like the
  // TopBar +£10m button — debugActions() returns [] in production builds, so
  // the row (and every cheat) vanishes from `vite build` output entirely.
  const devButtons = debugActions(import.meta.env.DEV);
  const errList = errorListModel(recentErrors());

  return (
    <>
      {devButtons.length > 0 && (
        <div className="row-actions wrap">
          {devButtons.map((a) => (
            <button
              key={a.id}
              className={`btn${a.danger ? ' danger' : ''}`}
              title={a.title}
              onClick={() => dispatch(a.action)}
            >
              {a.label}
            </button>
          ))}
        </div>
      )}
      <div className="row-actions wrap">
        <button className="btn accent" title="Save the on-screen debug.json to the backend for processing (queues locally if offline)" onClick={commit}>
          Commit snapshot
        </button>
        <button className="btn" title="Save the on-screen frozen frame to a local debug.json file" onClick={download}>
          Download debug.json
        </button>
        <button
          className="btn"
          title="Retake the snapshot now instead of waiting for the 15 s refresh"
          onClick={() => setFrame(takeFrame(state))}
        >
          Refresh now
        </button>
        <span className="hint">
          {pending} queued · {status ?? 'no commit this session'}
        </span>
      </div>
      <h4>Errors captured</h4>
      {errList.empty ? (
        <p className="hint">No errors captured this session.</p>
      ) : (
        <ul className="error-list mono">
          {errList.rows.map((e) => (
            <li key={e.correlationId}>
              <details>
                <summary>
                  <span className="muted">{e.time}</span>{' '}
                  <span className="muted">#{e.correlationId}</span>{' '}
                  <span className="muted">[{e.type}]</span> {e.msg}
                  {e.count > 1 && <span className="muted"> ×{e.count}</span>}
                </summary>
                <div className="error-detail">
                  {e.count > 1 && (
                    <div className="muted">
                      first {e.firstTime} · last {e.lastTime}
                    </div>
                  )}
                  {e.stateSummary && (
                    <div className="muted">
                      heap: tick {fmtNum(e.stateSummary.tick)} · funds{' '}
                      {fmtNum(e.stateSummary.funds)} · pop {fmtNum(e.stateSummary.population)} ·
                      speed {e.stateSummary.speed}
                    </div>
                  )}
                  {e.componentStack && (
                    <>
                      <div className="muted">component stack (trigger):</div>
                      <pre className="mono">{e.componentStack}</pre>
                    </>
                  )}
                  {e.stack && (
                    <>
                      <div className="muted">stack:</div>
                      <pre className="mono">{e.stack}</pre>
                    </>
                  )}
                </div>
              </details>
            </li>
          ))}
        </ul>
      )}
      <p className="hint">
        Snapshot taken {new Date(frame.at).toLocaleTimeString()} · next update in {fmtNum(remainingS)}s
        — frame is frozen so it can be selected and copied.
      </p>
      <pre className="debug-json mono">{frame.text}</pre>
    </>
  );
}
