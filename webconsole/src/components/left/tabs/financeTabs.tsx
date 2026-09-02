// financeTabs.tsx — FEAT-2326609720 inc2, Finance group child tabs.
//
// §1 grouping rows 1-7: Overview/Flow/Ledger/Trend are DIRECT MOVES from the
// old LeftDock (unchanged markup/logic — just relocated into this file so
// LeftDock.tsx can host the six-group tree). Tax Settings/Earnings/Policies
// are RELOCATIONS from RightDock's `rates`/`earnings`/`policy` tabs (also
// unchanged markup/logic — AC-2 requires the exact same content, just a new
// home). EarningsTab keeps its RightDock.tsx export name/signature (mount.test.tsx
// imports it from there — RightDock.tsx now re-exports it for that reason).

import { useSim } from '../../../sim/simContext';
import { LOAN_PRINCIPAL } from '../../../sim/engine';
import { Histogram } from '../Histogram';
import { Sankey } from '../Sankey';
import { fmtMoney, fmtMoneyEach, fmtNum, fmtPct, fmtSigned } from '../../../sim/utils';
import { countByKind, POLICIES, SPECS } from '../../../sim/data';
import type { PolicyId, TaxRates } from '../../../sim/types';
import { ragForFiscalNet } from '../../ragThresholds';

function Tile({ n, l, cls }: { n: string; l: string; cls?: string }) {
  return (
    <div className={`tile ${cls ?? ''}`}>
      <div className="n">{n}</div>
      <div className="l">{l}</div>
    </div>
  );
}

export function FinanceOverviewTab() {
  const { state, dispatch } = useSim();
  const income = state.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const net = income - expense;
  // §2 row 10 — fiscal net/tick RAG (binary per the spec's recommendation).
  const netRag = ragForFiscalNet(net);
  return (
    <>
      <div className="tiles">
        <Tile n={fmtMoney(state.funds)} l="Treasury" cls="acc" />
        <Tile n={fmtSigned(net)} l="Net / tick" cls={netRag === 'green' ? 'pos' : 'neg'} />
        <Tile n={fmtMoney(income)} l="Income" cls="pos" />
        <Tile n={fmtMoney(expense)} l="Expense" cls="neg" />
        <Tile n={fmtMoney(state.loanBalance)} l="Loan owed" />
      </div>
      <div className="row-actions">
        {state.loanBalance === 0 ? (
          <button className="btn" onClick={() => dispatch({ type: 'loan' })}>
            Take loan (+{fmtMoney(LOAN_PRINCIPAL)})
          </button>
        ) : (
          <button
            className="btn"
            disabled={state.funds < state.loanBalance}
            onClick={() => dispatch({ type: 'repay' })}
          >
            Repay loan (-{fmtMoney(state.loanBalance)})
          </button>
        )}
      </div>
      <p className="hint">
        Margin {income > 0 ? fmtPct(net / income) : '—'} · {state.buildings.length} structures on the rate roll
      </p>
    </>
  );
}

export function FinanceFlowTab() {
  const { state } = useSim();
  const income = state.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  return (
    <>
      <Sankey state={state} />
      <table className="table">
        <thead>
          <tr><th>Stream</th><th>Per tick</th><th>Share</th></tr>
        </thead>
        <tbody>
          {state.lastFlows.inflows.map((f) => (
            <tr key={f.label}>
              <td className="in">{f.label}</td>
              <td>{fmtMoney(f.value)}</td>
              <td>{income > 0 ? fmtPct(f.value / income) : '—'}</td>
            </tr>
          ))}
          {state.lastFlows.outflows.map((f) => (
            <tr key={f.label}>
              <td className="out">{f.label}</td>
              <td>{fmtMoney(-f.value)}</td>
              <td>{expense > 0 ? fmtPct(f.value / expense) : '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

export function FinanceLedgerTab() {
  const { state } = useSim();
  return (
    <table className="table ledger">
      <thead>
        <tr><th>Tick</th><th>Event</th><th>Amount</th></tr>
      </thead>
      <tbody>
        {state.ledger.length === 0 && (
          <tr><td colSpan={3} className="muted">No events yet — build or take a loan.</td></tr>
        )}
        {state.ledger.map((e) => (
          <tr key={e.id}>
            <td>{e.tick}</td>
            <td>{e.label}</td>
            <td className={e.amount >= 0 ? 'in' : 'out'}>{fmtSigned(e.amount)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function FinanceTrendTab() {
  const { state } = useSim();
  return (
    <>
      <Histogram history={state.history} />
      <TrendSummary />
    </>
  );
}

function TrendSummary() {
  const { state } = useSim();
  const h = state.history.slice(-72);
  if (h.length < 2) return null;
  const avgNet = h.reduce((a, b) => a + b.income - b.expense, 0) / h.length;
  const first = h[0];
  const last = h[h.length - 1];
  const fundGrowth = first.funds !== 0 ? (last.funds - first.funds) / Math.abs(first.funds) : 0;
  const popGrowth = first.population > 0 ? (last.population - first.population) / first.population : 0;
  return (
    <div className="tiles">
      <Tile n={fmtSigned(avgNet)} l="Avg net/tick" cls={avgNet >= 0 ? 'pos' : 'neg'} />
      <Tile n={fmtPct(fundGrowth)} l={`Funds ×72t`} cls={fundGrowth >= 0 ? 'pos' : 'neg'} />
      <Tile n={fmtPct(popGrowth)} l={`Pop ×72t`} cls="pos" />
    </div>
  );
}

const TAX_LABELS: Record<keyof TaxRates, string> = {
  residential: 'Council tax',
  commercial: 'Business tax',
  industrial: 'Freight tax',
};

// §1 row 5 — relocated from RightDock `rates`.
export function TaxSettingsTab() {
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

// §1 row 6 — relocated from RightDock `earnings`. Named export preserved
// (RightDock.tsx re-exports it) because mount.test.tsx imports EarningsTab
// from '../src/components/right/RightDock.tsx'.
export function EarningsTab() {
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
  const gridImportFlow = state.lastFlows.outflows.find((f) => f.label === 'Grid Import');
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
        {gridImportFlow && (
          <tr>
            <td>Grid Import</td>
            <td className="muted">external power cover</td>
            <td className="out">{fmtMoney(-gridImportFlow.value)}</td>
            <td />
          </tr>
        )}
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

// §1 row 7 — relocated from RightDock `policy`.
export function PoliciesTab() {
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
