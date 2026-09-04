// attack-bug652-round4.test.mjs — BUG-652 r4 INDEPENDENT DESTRUCTIVE ROUND
// (GR#23: attacker != author) — the BATCH BYPASS finding and its fix-proof.
// 2026-09-04.
//
// FINDING (r4, REJECT): round r3's placementAffordability() gate lived at
// exactly ONE UI dispatch site — MapView's single-tile 'place' click — and
// was BYPASSED by every batch path:
//   - drag-paint: N tiles flush as one 'placeMany' on pointerup
//   - stampRegion: a clone-paste of a captured clipboard region
//   - resolveDemand/resolveDemandAll: the advisor's "Fix"/"Fix All", which
//     build a WHOLE demandFixPlan()'s worth of units in one dispatch
// Proven live by the round: 3 Channel Tunnel Portals drag-painted for 180%
// of gross inflow with zero confirmation — the gate never saw the batch.
//
// THE FIX: a single shared UI-layer guard, src/components/placementGate.ts's
// evaluatePlacementBatch(), built on data.ts's new batchPlacementAffordability()
// (which generalises round r3's placementAffordability() to aggregate N
// specs' marginal wage impact in ONE comparison — the batch's TOTAL, not
// per-unit). Every UI dispatch site (MapView's single click / drag-paint
// flush / clone-paste, DemandDock's Fix / Fix All / Auto-build) now calls
// this SAME function before constructing its action. The reducer layer is
// UNTOUCHED by this round — it stays pure and unguarded (round r3's
// architecture: a reducer that can refuse a journalled action breaks replay
// by construction), which this file also re-confirms directly.
//
// GR#24: no git command was used at any point. The RED-proof below was run
// on a scratch copy (cp/scripted-edit/mv), documented, not automated.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import {
  SPECS,
  batchPlacementAffordability,
  placementAffordability,
  PLACEMENT_AFFORDABILITY_WAGE_FRACTION,
  orderedDemandFixPlan,
  totalJobsBySector,
  filledJobsBySector,
} from '../src/sim/data.ts';
import { sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { initialState, reducer } from '../src/sim/engine.ts';

const __dirname = dirname(fileURLToPath(import.meta.url));

function inject(s, spec, x, y, extra = {}) {
  const id = s.nextId ?? s.buildings.length + 1;
  return { ...s, nextId: id + 1, buildings: [...s.buildings, { id, spec, x, y, ...extra }] };
}

/** The round's own r1 mid-game fixture: 200 estates, 60k pop, one owned airport. */
function r1City(funds = 30_000_000, economyEpoch = 0) {
  let s = { ...initialState(), economyEpoch, funds, population: 60_000, unlockedAll: true };
  for (let i = 0; i < 200; i++) s = inject(s, 'res_estate', 100 + (i % 60) * 5, 5 + Math.floor(i / 60) * 4);
  s = inject(s, 'land_airport', 300, 5);
  return s;
}

// ══════════════════════════════════════════════════════════════════════════
// B1 — the round's own live reproduction: 3 Channel Tunnel Portals.
// ══════════════════════════════════════════════════════════════════════════

test('B1 (FIX-PROOF): 3 drag-painted Channel Tunnel Portals now aggregate to ONE confirmation at the batch total — round r4\'s exact live reproduction ("180% of gross inflow, zero confirmation")', () => {
  let city = r1City(5_000_000_000);
  for (let i = 0; i < 40; i++) city = reducer(city, { type: 'tick' });
  const gross = city.lastFlows.inflows.reduce((a, f) => a + f.value, 0);
  assert.ok(gross > 0, 'setup: city must have real recorded income');

  const sp = SPECS.land_tunnel;
  const singleAfford = placementAffordability(city, sp);
  const batch3 = batchPlacementAffordability(city, [sp, sp, sp]);

  // The round's own arithmetic: 3 tunnels together must be a MUCH bigger
  // proportion of income than any single one (this is what "bypassed" meant
  // — the old single-spec gate would have seen only ONE tunnel's marginal
  // wage, which may not trip, while three together genuinely do).
  assert.ok(batch3.marginalWagePerTick > singleAfford.marginalWagePerTick, 'the aggregate must exceed any single unit\'s figure');
  assert.ok(
    batch3.marginalWagePerTick > gross * PLACEMENT_AFFORDABILITY_WAGE_FRACTION,
    `FIX-PROOF setup check: 3 tunnels' aggregate wage (£${batch3.marginalWagePerTick}) must be genuinely disproportionate against gross inflow (£${gross}) — the round's own "180% of gross inflow" shape, exact ratio derived from real numbers rather than hand-typed`
  );
  assert.ok(batch3.exceedsThreshold, 'the aggregate must trip the gate');

  // ONE message, naming the WHOLE batch — never three separate dialogs.
  assert.match(batch3.message, /^3 x Channel Tunnel Portal/, 'the batch subject must read "3 x <name>", not a single unit');
  assert.match(batch3.message, /Build anyway\?$/);

  // FIX-PROOF: the reducer itself (what 'placeMany'/'stampRegion'/
  // resolveDemand* actually drive) is UNTOUCHED and places the WHOLE batch
  // once dispatched — proving the fix is UI-side only, never reducer-side.
  const tiles = [{ x: 10, y: 10 }, { x: 20, y: 10 }, { x: 30, y: 10 }];
  let s = { ...city, funds: 50_000_000_000 };
  s = reducer(s, { type: 'unlockAll' });
  const before = s.buildings.filter((b) => b.spec === 'land_tunnel').length;
  s = reducer(s, { type: 'placeMany', spec: 'land_tunnel', tiles });
  const after = s.buildings.filter((b) => b.spec === 'land_tunnel').length;
  assert.equal(after - before, 3, 'a confirmed/dispatched placeMany batch places ALL units, not a subset');
});

// ══════════════════════════════════════════════════════════════════════════
// B2 — batchPlacementAffordability(): the aggregation SSOT itself.
// ══════════════════════════════════════════════════════════════════════════

test('B2a: a uniform batch (N copies of ONE spec) aggregates additively and matches the real filledJobsBySector/sectorWagesPerTick SSOT exactly', () => {
  const s = { ...initialState(), buildings: [], population: 200_000, lastFlows: { inflows: [{ label: 'x', value: 1_000_000 }], outflows: [] } };
  const sp = SPECS.hea_teaching;
  const batch = batchPlacementAffordability(s, [sp, sp, sp]);

  const truthful = sectorWagesPerTick(
    filledJobsBySector({ ...s, buildings: [1, 2, 3].map((i) => ({ id: i, spec: 'hea_teaching', x: i, y: i })) })
  ).totalPerTick;

  assert.equal(batch.marginalWagePerTick, truthful, 'the batch figure must equal the real SSOT wage bill for those 3 buildings exactly');
});

test('B2b: a MIXED-spec batch aggregates correctly and reports a generic "N buildings" subject', () => {
  const s = { ...initialState(), buildings: [], population: 200_000, lastFlows: { inflows: [{ label: 'x', value: 1_000_000 }], outflows: [] } };
  const specs = [SPECS.land_airport, SPECS.hea_teaching, SPECS.land_tunnel];
  const batch = batchPlacementAffordability(s, specs);

  const truthful = sectorWagesPerTick(
    filledJobsBySector({
      ...s,
      buildings: specs.map((sp, i) => ({ id: i + 1, spec: sp.id, x: i, y: i })),
    })
  ).totalPerTick;

  assert.equal(batch.marginalWagePerTick, truthful, 'a mixed batch must sum to the real SSOT wage bill for all three specs together');
  assert.ok(batch.exceedsThreshold);
  assert.match(batch.message, /^3 buildings/, 'a mixed batch names a generic count, not a misleading single spec name');
});

test('B2c: an empty batch, or a batch with zero recorded income, never trips (matches the single-spec bootstrap exemption exactly)', () => {
  const s = { ...initialState(), buildings: [], lastFlows: { inflows: [], outflows: [] } };
  assert.equal(batchPlacementAffordability(s, []).exceedsThreshold, false);
  assert.equal(batchPlacementAffordability(s, [SPECS.land_airport, SPECS.land_airport]).exceedsThreshold, false);
});

test('B2d: placementAffordability(s, sp) is byte-identical to batchPlacementAffordability(s, [sp]) — the single-spec API is a true convenience wrapper, not a re-derived formula', () => {
  const s = { ...initialState(), buildings: [], population: 100_000, lastFlows: { inflows: [{ label: 'x', value: 300_000 }], outflows: [] } };
  for (const id of ['land_airport', 'hea_teaching', 'com_shop', 'off_tower']) {
    const sp = SPECS[id];
    assert.deepEqual(placementAffordability(s, sp), batchPlacementAffordability(s, [sp]), `${id}: single-spec and batch-of-1 must agree exactly`);
  }
});

// ══════════════════════════════════════════════════════════════════════════
// B3 — evaluatePlacementBatch() (placementGate.ts): the shared UI seam.
// ══════════════════════════════════════════════════════════════════════════

test('B3a: evaluatePlacementBatch returns null (dispatch immediately) below threshold, and a PendingBatchPlacement whose commit() is EXACTLY the caller-supplied callback above it', async () => {
  const { evaluatePlacementBatch } = await import('../src/components/placementGate.ts');
  const cheapCity = { ...initialState(), buildings: [], lastFlows: { inflows: [{ label: 'x', value: 1_000_000_000 }], outflows: [] } };
  const cheapGate = evaluatePlacementBatch(cheapCity, ['com_shop'], () => {});
  assert.equal(cheapGate, null, 'an affordable placement must not gate');

  const poorCity = { ...initialState(), buildings: [], population: 200_000, lastFlows: { inflows: [{ label: 'x', value: 100_000 }], outflows: [] } };
  let committed = false;
  const gate = evaluatePlacementBatch(poorCity, ['land_airport', 'land_airport'], () => { committed = true; });
  assert.ok(gate, 'a genuinely disproportionate batch must gate');
  assert.match(gate.afford.message, /^2 x International Airport/);
  gate.commit();
  assert.equal(committed, true, 'commit() must be the EXACT callback the caller supplied — nothing re-derived, nothing dropped');
});

test('B3b: evaluatePlacementBatch drops unknown spec ids defensively rather than throwing', async () => {
  const { evaluatePlacementBatch } = await import('../src/components/placementGate.ts');
  const s = { ...initialState(), buildings: [], lastFlows: { inflows: [{ label: 'x', value: 1_000 }], outflows: [] } };
  assert.doesNotThrow(() => evaluatePlacementBatch(s, ['not_a_real_spec_id'], () => {}));
});

// ══════════════════════════════════════════════════════════════════════════
// B4 — resolveDemandAll's confirm shows the PLAN TOTAL, not per-service.
// ══════════════════════════════════════════════════════════════════════════

test('B4: the Fix All plan\'s aggregate wage bill (every shortfall\'s whole build list, summed) is what a confirmation would show — never a single service\'s figure', () => {
  // A city with several real shortfalls at once, income small enough that
  // the SUM (but not necessarily any one row alone) is disproportionate.
  let s = { ...initialState(), unlockedAll: true, funds: 5_000_000_000, population: 300_000 };
  for (let i = 0; i < 40; i++) s = reducer(s, { type: 'tick' });
  const plan = orderedDemandFixPlan(s);
  assert.ok(plan.length > 0, 'setup: there must be at least one real shortfall to fix');

  const specIds = plan.flatMap((p) => Array(p.count).fill(p.specId));
  const wholePlanAfford = batchPlacementAffordability(s, specIds.map((id) => SPECS[id]));

  // The aggregate must be >= any single plan row's own affordability figure
  // (monotonic — adding more job-bearing buildings to the same batch can
  // only raise or hold the marginal wage bill, never lower it).
  for (const p of plan) {
    const rowAfford = batchPlacementAffordability(s, Array(p.count).fill(SPECS[p.specId]));
    assert.ok(
      wholePlanAfford.marginalWagePerTick >= rowAfford.marginalWagePerTick - 1,
      `${p.serviceKey}: the WHOLE plan's aggregate must be at least as large as any one row's own figure (small float slack for rounding)`
    );
  }
});

// ══════════════════════════════════════════════════════════════════════════
// B5 — single-click behaviour is UNCHANGED (round r3's own contract holds).
// ══════════════════════════════════════════════════════════════════════════

test('B5: a single placement (batch of exactly one) behaves exactly as round r3 shipped it — no regression from generalising to batches', () => {
  const s = { ...initialState(), buildings: [], population: 60_000, lastFlows: { inflows: [{ label: 'x', value: 135_967 }], outflows: [] } };
  const afford = placementAffordability(s, SPECS.land_airport);
  assert.ok(afford.exceedsThreshold, 'the round r2/r3 13.7x-ratio case must still trip identically');
  assert.equal(afford.message, `International Airport adds £${afford.marginalWagePerTick.toLocaleString()}/tick in wages once staffed — more than 50% of your current income (£135,967/tick). Build anyway?`);
});

// ══════════════════════════════════════════════════════════════════════════
// B6 — source-wiring: every UI dispatch site actually calls the shared seam.
// ══════════════════════════════════════════════════════════════════════════

test('B6 (WIRING, "weak but honest" per this suite\'s own precedent — mount.test.tsx\'s makeKeydownHandler check): every batch dispatch site imports and calls evaluatePlacementBatch BEFORE its dispatch', async () => {
  const mapViewSrc = await readFile(resolve(__dirname, '../src/components/MapView.tsx'), 'utf-8');
  const demandDockSrc = await readFile(resolve(__dirname, '../src/components/left/DemandDock.tsx'), 'utf-8');

  assert.ok(mapViewSrc.includes("from './placementGate'"), 'MapView.tsx must import the shared seam');
  assert.ok(demandDockSrc.includes("from '../placementGate'"), 'DemandDock.tsx must import the shared seam');

  // Every batch-shaped dispatch type must appear NEAR an evaluatePlacementBatch
  // call in the SAME file (a crude but honest proximity check — the whole
  // point of r4's finding was that these dispatches existed with NO gate
  // anywhere nearby at all).
  for (const marker of ["type: 'placeMany'", "type: 'stampRegion'"]) {
    const idx = mapViewSrc.indexOf(marker);
    assert.ok(idx > 0, `MapView.tsx must still dispatch ${marker}`);
    const window = mapViewSrc.slice(Math.max(0, idx - 600), idx + 200);
    assert.ok(window.includes('evaluatePlacementBatch'), `${marker}'s dispatch site must be gated by evaluatePlacementBatch nearby`);
  }
  for (const marker of ["type: 'resolveDemand'", "type: 'resolveDemandAll'"]) {
    const idx = demandDockSrc.indexOf(marker);
    assert.ok(idx > 0, `DemandDock.tsx must still dispatch ${marker}`);
    const window = demandDockSrc.slice(Math.max(0, idx - 600), idx + 200);
    assert.ok(window.includes('evaluatePlacementBatch'), `${marker}'s dispatch site must be gated by evaluatePlacementBatch nearby`);
  }
});
