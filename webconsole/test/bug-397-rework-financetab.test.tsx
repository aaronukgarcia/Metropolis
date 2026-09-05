// bug-397-rework-financetab.test.tsx — BUG-397 F4 render coverage.
//
// F4: financeTabs.tsx's FinanceLedgerTab rendered an amount:0 ledger row
// (today: only the Transit Subsidy cap notice, engine.ts) via the
// `amount >= 0 ? 'in' : 'out'` branch, showing a literal "+£0" as if income
// had actually arrived. Fixed to render exactly-zero amounts as a neutral,
// unsigned 'muted' info row — only a genuinely nonzero amount earns the
// signed +/- in/out styling.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import React from 'react';
import { renderToString } from 'react-dom/server';
import { FinanceLedgerTab } from '../src/components/left/tabs/financeTabs';
import { SimContext } from '../src/sim/simContext';
import { initialState } from '../src/sim/engine';

function renderWithLedger(ledger: Array<{ id: number; tick: number; label: string; amount: number }>) {
  const state = { ...initialState(), ledger };
  return renderToString(
    React.createElement(
      SimContext.Provider,
      // Only `state` is read by FinanceLedgerTab — the rest of
      // SimContextValue is irrelevant to this render and safely omitted.
      { value: { state } as any },
      React.createElement(FinanceLedgerTab),
    ),
  );
}

test('F4: a zero-amount ledger row renders neutral (muted, unsigned), not as "+£0" income', () => {
  const html = renderWithLedger([
    { id: 3, tick: 10, label: 'Transit Subsidy capped at 100 (uncapped: 200) — 50% of tax income cap', amount: 0 },
    { id: 2, tick: 9, label: 'Loan Taken', amount: 50000 },
    { id: 1, tick: 8, label: 'Connector Road', amount: -3000 },
  ]);

  assert.ok(!html.includes('+£0<'), 'zero-amount row rendered as if it were positive income (+£0)');
  assert.ok(
    /class="muted">£0</.test(html),
    `zero-amount row must render unsigned "£0" in the neutral muted class; got: ${html}`,
  );
  assert.ok(!/class="in">[^<]*£0</.test(html), 'zero-amount row must not carry the "in" (income) class');
  assert.ok(/class="in">\+£50,000</.test(html), 'positive row must keep its "in" class and + sign');
  assert.ok(/class="out">-£3,000</.test(html), 'negative row must keep its "out" class and - sign');
});

test('F4: multiple zero-amount rows (e.g. bind + release notices) all render neutral', () => {
  const html = renderWithLedger([
    { id: 2, tick: 20, label: 'Transit Subsidy cap released — the subsidy is no longer clamped', amount: 0 },
    { id: 1, tick: 10, label: 'Transit Subsidy capped at 100 (uncapped: 200) — 50% of tax income cap', amount: 0 },
  ]);
  const mutedZeroCount = (html.match(/class="muted">£0</g) ?? []).length;
  assert.equal(mutedZeroCount, 2, 'both zero-amount notices must render neutral');
  assert.ok(!html.includes('class="in"'), 'no zero row should ever pick up the income "in" class');
});
