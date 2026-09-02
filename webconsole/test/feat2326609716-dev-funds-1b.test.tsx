// feat2326609716-dev-funds-1b.test.tsx — FEAT-2326609716 acceptance.
//
// The +1B dev funds button (DevFundsLargeButton) added next to the existing
// +10m button (DevFundsButton) for start-over/big-capex testing. Both dispatch
// the same 'debugFunds' action with different amounts.
//
// Acceptance criteria:
// AC-1: Button exists and renders next to +10m when import.meta.env.DEV is true
// AC-2: Button dispatches 'debugFunds' with amount = 1_000_000_000 (exactly 1B)
// AC-3: Funds conservation ledger shows the inflow identically to +10m pattern
// AC-4: Action is journaled and replays identically (same path as +10m)
// AC-5: Production build omits the button (DEV=false)
//
// NOTE: AC-1 and AC-5 rendering behavior is validated by
// bug584-envless-render.test.tsx (lines 75-101) which asserts both "+£10m"
// and "+£1B" text presence when DEV=true, and their absence when DEV=false.
// This file focuses on the action dispatch and journaling logic.
//
// AC-3/AC-4: Reducer and journal behavior is validated by the existing
// store-dispatch.test.tsx suite which proves debugFunds works correctly.
// The +1B variant uses the same action type, just a different amount constant.
//
// RED-PROOF (scratch cp/mv, not in tree — GR#24): reverting
// DEV_FUNDS_GRANT_LARGE to a wrong value or removing DevFundsLargeButton
// turns the assertions below red.

import { test } from 'node:test';
import assert from 'node:assert/strict';

test('FEAT-2326609716 AC-1: DevFundsLargeButton component exists', async () => {
  const { DevFundsLargeButton } = await import('../src/components/TopBar.tsx');

  // AC-1: button component exists and is a function (React component)
  assert.strictEqual(
    typeof DevFundsLargeButton,
    'function',
    'DevFundsLargeButton must be a React component (function)'
  );
});

test('FEAT-2326609716 AC-2: DEV_FUNDS_GRANT_LARGE constant is 1_000_000_000_000 (1T, Aaron Q100094)', async () => {
  const { DEV_FUNDS_GRANT_LARGE, DEV_FUNDS_GRANT } = await import(
    '../src/components/TopBar.tsx'
  );

  // AC-2: constant exists and has the correct value (1T = 1_000_000_000_000, raised from 1B per Aaron Q100094 2026-09-03)
  assert.strictEqual(
    DEV_FUNDS_GRANT_LARGE,
    1_000_000_000_000,
    'DEV_FUNDS_GRANT_LARGE must be exactly 1_000_000_000_000'
  );

  // Sanity: 1T is 100,000x the 10m button (raised from 100x/1B per Aaron
  // Q100094, 2026-09-03).
  assert.strictEqual(
    DEV_FUNDS_GRANT_LARGE / DEV_FUNDS_GRANT,
    100_000,
    '+1T button should be 100,000x the +10m button'
  );
});

test('FEAT-2326609716 AC-4: debugFunds action is journaled', async () => {
  const { isStateAffecting } = await import('../src/sim/journal.ts');

  // AC-4: debugFunds must be journaled (state-affecting action).
  // Both 10m and 1B amounts use the same action type 'debugFunds',
  // so both follow the same journal recording path.
  const action = { type: 'debugFunds' as const, amount: 1_000_000_000 };
  assert.ok(
    isStateAffecting(action),
    'debugFunds action must be state-affecting and journaled (journal.ts line 138-139)'
  );
});
