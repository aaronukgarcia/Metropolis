import { useState } from 'react';
import { Histogram } from './Histogram';
import { Sankey } from './Sankey';
import { useSim } from '../../sim/simContext';
import { LOAN_PRINCIPAL } from '../../sim/engine';
import { Panel } from '../Tabs';
import { fmtMoney, fmtPct, fmtSigned } from '../../sim/utils';

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'flow', label: 'Flow' },
  { id: 'ledger', label: 'Ledger' },
  { id: 'trend', label: 'Trend' },
];

function Tile({ n, l, cls }: { n: string; l: string; cls?: string }) {
  return (
    <div className={`tile ${cls ?? ''}`}>
      <div className="n">{n}</div>
      <div className="l">{l}</div>
    </div>
  );
}

export function LeftDock() {
  const { state, dispatch } = useSim();
  const [tab, setTab] = useState('overview');

  const income = state.lastFlows.inflows.reduce((a, b) => a + b.value, 0);
  const expense = state.lastFlows.outflows.reduce((a, b) => a + b.value, 0);
  const net = income - expense;

  return (
    <Panel title="Fiscal" tabs={TABS} active={tab} onSelect={setTab}>
      {tab === 'overview' && (
        <>
          <div className="tiles">
            <Tile n={fmtMoney(state.funds)} l="Treasury" cls="acc" />
            <Tile n={fmtSigned(net)} l="Net / tick" cls={net >= 0 ? 'pos' : 'neg'} />
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
      )}

      {tab === 'flow' && (
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
      )}

      {tab === 'ledger' && (
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
      )}

      {tab === 'trend' && (
        <>
          <Histogram history={state.history} />
          <TrendSummary />
        </>
      )}
    </Panel>
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
