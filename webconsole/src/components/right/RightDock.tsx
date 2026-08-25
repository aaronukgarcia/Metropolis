import { useState } from 'react';
import { useSim, levelOf, xpForLevel, wellbeingOf, approvalOf } from '../../sim/store';
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
  plantEffServed,
} from '../../sim/data';
import type { PolicyId, TaxRates } from '../../sim/types';
import { Panel } from '../Tabs';
import { fmtMoney, fmtPct } from '../../sim/utils';
import { useBusy } from '../Busy';
import { commitDebug, pendingCommits, recentErrors } from '../../sim/backend';

const TABS = [
  { id: 'status', label: 'Status' },
  { id: 'rates', label: 'Rates' },
  { id: 'units', label: 'Units' },
  { id: 'water', label: 'Water' },
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
  const capacity = (() => {
    let cap = 0;
    for (const b of state.buildings) {
      const sp = SPECS[b.spec];
      if (sp?.kind === 'residential') cap += sp.residents ?? 8;
    }
    return cap;
  })();
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
          <div className="n">{state.population.toLocaleString()}</div>
          <div className="l">Citizens</div>
        </div>
        <div className="tile">
          <div className="n">{capacity.toLocaleString()}</div>
          <div className="l">Housing cap</div>
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
        {(income - expense).toLocaleString()}/tick
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
    residential: `${state.population} citizens × ¤2 × rate`,
    commercial: `${c.commercial} zones × ¤40 × rate`,
    industrial: `${c.industrial} plants × ¤55 × rate`,
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
  const plants = state.buildings.filter((b) => SPECS[b.spec]?.kind === 'water');
  return (
    <>
      <div className="tiles">
        <div className="tile pos">
          <div className="n">{bal.clean.toLocaleString()}</div>
          <div className="l">Clean capacity</div>
        </div>
        <div className={`tile ${bal.leak ? 'neg' : 'pos'}`}>
          <div className="n">{bal.waste.toLocaleString()}</div>
          <div className="l">Discharge capacity</div>
        </div>
      </div>
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
          <tr><th>Plant</th><th>Grid</th><th>Pipe</th><th>Served</th><th /></tr>
        </thead>
        <tbody>
          {plants.length === 0 && (
            <tr><td colSpan={5} className="muted">No water infrastructure yet.</td></tr>
          )}
          {plants.map((b) => {
            const sp = SPECS[b.spec];
            const tier = state.pipeTier[b.id] ?? 0;
            const eff = plantEffServed(state, b);
            const next = PIPE_TIERS[tier + 1];
            return (
              <tr key={b.id}>
                <td className={sp.tag === 'clean' ? 'in' : 'out'}>{sp.name}</td>
                <td className="mono">{b.x},{b.y}</td>
                <td>{PIPE_TIERS[tier].label}</td>
                <td>{eff.toLocaleString()}</td>
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
            <td className="muted">{r.count > 0 ? `${r.count.toLocaleString()} ${r.unit}` : '—'}</td>
            <td className="in">{fmtMoney(r.gross)}</td>
            <td>{r.count > 0 ? fmtMoney(r.gross / r.count) : '—'}</td>
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
  const byUnlock = new Map<number, string[]>();
  for (const sp of Object.values(SPECS)) {
    if (sp.unlock > 7 || sp.category === 'network') continue;
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
            {state.xp.toLocaleString()} XP · {Math.max(0, next - state.xp).toLocaleString()} to level {level + 1}
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
          {[1, 2, 3, 4, 5, 6, 7].map((lv) => (
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

function DebugTab() {
  const { state, dispatch } = useSim();
  const { run } = useBusy();
  const [status, setStatus] = useState<string | null>(null);
  const [pending, setPending] = useState(pendingCommits());
  const snapshot = {
    tick: state.tick,
    speed: state.speed,
    funds: state.funds,
    loanBalance: state.loanBalance,
    population: state.population,
    xp: state.xp,
    taxRates: state.taxRates,
    policies: state.policies,
    buildingCount: state.buildings.length,
    movingId: state.movingId,
    lastFlows: state.lastFlows,
  };
  function commit() {
    run(async () => {
      setStatus('Committing snapshot…');
      const r = await commitDebug(snapshot);
      setStatus(r.message);
      setPending(pendingCommits());
    });
  }
  return (
    <>
      <div className="row-actions wrap">
        <button className="btn" onClick={() => dispatch({ type: 'debugFunds', amount: 10000 })}>+¤10,000</button>
        <button className="btn" onClick={() => dispatch({ type: 'debugXp', amount: 500 })}>+500 XP</button>
        <button className="btn" onClick={() => dispatch({ type: 'speed', speed: 3 })}>Force fast</button>
        <button className="btn danger" onClick={() => dispatch({ type: 'reset' })}>Reset city</button>
      </div>
      <div className="row-actions wrap">
        <button className="btn accent" title="Save this debug snapshot to the backend for processing (queues locally if offline)" onClick={commit}>
          Commit snapshot
        </button>
        <span className="hint">
          {pending} queued · {status ?? 'no commit this session'}
        </span>
      </div>
      <h4>Errors captured</h4>
      {recentErrors().length === 0 ? (
        <p className="hint">No errors captured this session.</p>
      ) : (
        <ul className="error-list mono">
          {recentErrors().map((e, i) => (
            <li key={i}>
              <span className="muted">{new Date(e.at).toLocaleTimeString()}</span> {e.msg}
            </li>
          ))}
        </ul>
      )}
      <pre className="debug-json mono">{JSON.stringify(snapshot, null, 2)}</pre>
    </>
  );
}
