// FEAT-2326609716 — the +£1B dev funds button (Aaron 2026-09-01: "I need a
// debug button to add 1B it needs to be next to the +10m button").
//
// Three guarantees pinned here:
//   1. The button exists in TopBar.tsx as a SIBLING of the +£10m button,
//      mounted immediately after it, with the identical import.meta.env.DEV
//      production-omission gate (RightDock.tsx:~902's documented guarantee:
//      debug affordances never reach a production build).
//   2. The amount is the named constant DEV_FUNDS_GRANT_LARGE = 1_000_000_000
//      dispatched through the SAME debugFunds action the +£10m button uses —
//      no second money-mutation path (GR#3).
//   3. Reducer-level: a 1B debugFunds grant lands exactly, and the
//      conservation checker still PASSES afterward (debugFunds re-baselines,
//      the same contract consistency.test.mjs pins for the +10M grant).
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const topBarSource = fs.readFileSync(path.join(here, '..', 'src', 'components', 'TopBar.tsx'), 'utf8');

test('FEAT-2326609716: DevFundsLargeButton exists, DEV-gated, next to DevFundsButton', () => {
  assert.ok(topBarSource.includes('DEV_FUNDS_GRANT_LARGE = 1_000_000_000'),
    'the +£1B amount must be the named constant DEV_FUNDS_GRANT_LARGE (never an inline literal)');
  assert.ok(/export function DevFundsLargeButton\(\)/.test(topBarSource),
    'DevFundsLargeButton must be exported from TopBar.tsx');
  const largeBody = topBarSource.split('export function DevFundsLargeButton()')[1] ?? '';
  // BUG-584: the gate now reads `import.meta.env?.DEV` (optional-chain idiom,
  // codebase-wide, so a runtime without `import.meta.env` itself — SSR-style
  // render, the tsx test runner — doesn't throw). The substring pinned here
  // is retargeted to match; intent (the +£1B button carries the identical
  // DEV-only gate as +£10m) is unchanged.
  assert.ok(largeBody.includes('if (!import.meta.env?.DEV) return null;'),
    'the +£1B button must carry the identical DEV-only gate as the +£10m button');
  assert.ok(largeBody.includes("dispatch({ type: 'debugFunds', amount: DEV_FUNDS_GRANT_LARGE })"),
    'the +£1B button must grant via the SAME debugFunds action path (GR#3)');
  // Sibling placement: mounted immediately after DevFundsButton.
  assert.ok(/<DevFundsButton \/>\s*<DevFundsLargeButton \/>/.test(topBarSource),
    'DevFundsLargeButton must be mounted immediately next to DevFundsButton');
});

test('FEAT-2326609716: a 1B debugFunds grant lands exactly and conservation still passes', async () => {
  const { initialState, reducer } = await import('../src/sim/engine.ts');
  const { runConsistencyChecks } = await import('../src/sim/consistency.ts');
  let s = initialState();
  const before = s.funds;
  s = reducer(s, { type: 'debugFunds', amount: 1_000_000_000 });
  assert.equal(s.funds, before + 1_000_000_000, 'the grant must land exactly, no clamping or overflow');
  // The grant must re-baseline, not break, the conservation check on the next
  // tick -- the exact contract consistency.test.mjs pins for the +10M grant.
  s = reducer(s, { type: 'tick' });
  const report = runConsistencyChecks(s);
  const check = report.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.equal(check.ok, true,
    'conservation must still pass after a 1B grant + tick (debugFunds re-baselines)');
});
