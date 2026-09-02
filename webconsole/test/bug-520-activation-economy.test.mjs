// bug-520-activation-economy.test.mjs — BUG-520 (remaining part): the
// activation-consistency class (BUG-430/431 family) applied to TAX and
// GROWTH DEMAND. PART of BUG-520 was already fixed on origin/main: sumBy()
// (BUG-527), totalJobs() (BUG-525), and waterCaps() (BUG-534) are already
// isOnline()-gated. The remaining hole was countByKind() in data.ts, which
// drives:
//
//   1. TAX: computeFlows() (engine.ts) uses countByKind for Business /
//      Freight / Office tax — an OFFLINE / road-disconnected commercial,
//      industrial, office, or mine building paid FULL TAX every tick while
//      paying ZERO upkeep (upkeep IS isOnline-gated) — a direct money leak,
//      the inverse of intent.
//   2. GROWTH DEMAND: demandOf() (engine.ts) uses countByKind for
//      commercial/industrial demand — an offline job building still
//      accelerated population growth via attractiveness/moveIns.
//
// Fix: countByKindOnline(s) in data.ts, isOnline-gated exactly like
// powerStats()/sumBy()/totalJobs(), used by computeFlows(), demandOf(), and
// the consistency.ts recompute (which must track computeFlows() or it would
// falsely redden). The plain (ungated) countByKind() is kept for legitimate
// "total placed, online or not" consumers: debugjson.ts/snapshot.ts byKind
// diagnostics, the MapView advisor tip text, and the MILESTONES achievement
// badges — none of those are economy paths.
//
// Run with the scoped test runner (never a full glob — see
// docs/planning/agent-test-verification-contract.md); node type-strips the
// imported .ts so these assertions exercise the exact shipped aggregation.
//
// RED-PROOF: every offline-must-be-zero assertion below is written to FAIL
// if the countByKindOnline gate is dropped (i.e. computeFlows/demandOf
// revert to the plain countByKind). Verified by temporarily reverting
// engine.ts's `countByKindOnline(s)` calls back to `countByKind(s.buildings)`
// (scratch cp/mv, never git — GR#24): the four offline-zero assertions below
// went RED (business tax computed as if the shop were online, and growth
// demand included the offline shop's -10 commercial term), then were
// restored to GREEN. See task report for the captured RED transcript.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  isOnline,
  computeRoadConnectivity,
} from '../src/sim/data.ts';
import { initialState, computeFlows, demandOf } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

let _id = 520000;
const B = (spec, x, y, extra = {}) => ({ id: _id++, spec, x, y, ...extra });

// A fresh state whose ONLY buildings are the given list, with road
// connectivity computed exactly as advance() does every tick (same harness
// as bug-525-527-activation-coverage.test.mjs / bug430-power-gate.test.mjs).
function city(buildings, tick = 200, population = 1000) {
  const s = initialState();
  const st = { ...s, buildings: [...buildings], population, tick };
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

// ─────────────────────────────────────────────────────────────────────────
// TAX: computeFlows() — Business Tax must count only ONLINE commercial.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-520: an ONLINE commercial building pays business tax', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const s = city([B('com_shop', 1, 10, { builtTick: 0 }), ...roads]);
  const shop = s.buildings[0];
  assert.equal(isOnline(s, shop), true, 'setup: connected + built-out shop must be online');

  const { inflows } = computeFlows(s);
  const businessTax = inflows.find((f) => f.label === 'Business Tax');
  assert.ok(businessTax, 'sanity: Business Tax line exists');
  assert.ok(businessTax.value > 0, 'an online commercial building must pay non-zero business tax');
});

test('BUG-520: the SAME commercial building road-disconnected pays ZERO business tax', () => {
  // Disconnected: road stub at (1,10) is interior (not a map edge, not near
  // a trunk) so it never joins the connected network — the shop at (2,10)
  // is road-adjacent but NOT road-connected (same pattern as bug430/525/527).
  const s = city([B('com_shop', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })]);
  const shop = s.buildings[0];
  assert.equal(isOnline(s, shop), false, 'setup: disconnected shop must be offline');

  const { inflows } = computeFlows(s);
  const businessTax = inflows.find((f) => f.label === 'Business Tax');
  assert.equal(
    businessTax?.value ?? 0,
    0,
    'an OFFLINE commercial building must pay ZERO business tax (was the leak)'
  );
});

// ─────────────────────────────────────────────────────────────────────────
// GROWTH DEMAND: demandOf() — an offline commercial/industrial building
// must not move commercial/industrial demand off its offline-population
// baseline.
// ─────────────────────────────────────────────────────────────────────────
// demandOf's commercial term is `(shopBase - c.commercial*10)/shopBase * 130`
// with shopBase = max(population*0.22, 12): at population>=55 the zero-shop
// baseline (ratio 1 * 130 = 130) already saturates the +-100 clamp, so a
// single shop's effect can be masked by the clamp ceiling. Use a small
// population (20, well under the 54.5 threshold where shopBase floors at 12)
// so the unclamped math is visible and one shop produces a clear, non-clamped
// delta.
const DEMAND_POP = 20;

test('BUG-520: an ONLINE commercial building adds growth-demand pressure (reduces commercial demand from the empty-city baseline)', () => {
  const roads = [B('road', 0, 10, { builtTick: 0 })];
  const empty = city([...roads], 200, DEMAND_POP);
  const withShop = city([B('com_shop', 1, 10, { builtTick: 0 }), ...roads], 200, DEMAND_POP);
  const shop = withShop.buildings[0];
  assert.equal(isOnline(withShop, shop), true, 'setup: connected + built-out shop must be online');

  const demandEmpty = demandOf(empty).commercial;
  const demandWithShop = demandOf(withShop).commercial;
  assert.notEqual(
    demandWithShop,
    demandEmpty,
    'an online commercial building must move commercial demand away from the empty-city baseline'
  );
});

test('BUG-520: the SAME commercial building road-disconnected adds ZERO growth-demand pressure (demand == empty-city baseline)', () => {
  const empty = city([B('road', 0, 10, { builtTick: 0 })], 200, DEMAND_POP);
  const disconnected = city(
    [B('com_shop', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })],
    200,
    DEMAND_POP
  );
  const shop = disconnected.buildings[0];
  assert.equal(isOnline(disconnected, shop), false, 'setup: disconnected shop must be offline');

  const demandEmpty = demandOf(empty).commercial;
  const demandDisconnected = demandOf(disconnected).commercial;
  assert.equal(
    demandDisconnected,
    demandEmpty,
    'an OFFLINE commercial building must add ZERO growth-demand pressure (was the leak)'
  );
});

// ─────────────────────────────────────────────────────────────────────────
// consistency.ts must track computeFlows() — no false-red once tax is gated.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-520: consistency Business Tax cross-check does not falsely redden with an offline commercial building present', () => {
  const s = city([B('com_shop', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })]);
  s.lastFlows = computeFlows(s);
  const report = runConsistencyChecks(s);
  const row = report.checks.find((c) => c.id === 'flows.business-tax-matches');
  assert.ok(row, 'sanity: the business-tax consistency check exists');
  assert.equal(row.ok, true, 'recompute must match actual flows even with an offline commercial building present');
});

// ─────────────────────────────────────────────────────────────────────────
// Determinism (GR#21) — pure functions of state; no Date/Math.random.
// ─────────────────────────────────────────────────────────────────────────
test('BUG-520: computeFlows/demandOf are deterministic across identical states', () => {
  const mk = () =>
    city([B('com_shop', 2, 10, { builtTick: 0 }), B('road', 1, 10, { builtTick: 0 })]);
  const a = computeFlows(mk());
  const b = computeFlows(mk());
  assert.deepEqual(a, b, 'identical states must yield identical flows');
  assert.deepEqual(demandOf(mk()), demandOf(mk()), 'identical states must yield identical demand');
});
