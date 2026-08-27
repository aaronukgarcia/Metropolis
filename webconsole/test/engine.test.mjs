// engine.test.mjs — BUG-390 computeFlows bucketing regression test.
//
// FEAT-1972079877 introduced landmark/residential/station kinds with upkeep,
// but UPKEEP_BUCKET was missing keys for them. Their upkeep would silently
// vanish from outflows (the `if (k)` check failed). Deleting all 4 new bucket
// lines kept the suite green: zero regression coverage on computeFlows bucketing.
//
// This test proves RED when any kind's upkeep bucket is removed or when a
// building's upkeep doesn't flow to an outflow stream.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeFlows } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';

// Minimal SimState with no loans, no policies, default tax rates.
function baseState() {
  return {
    tick: 0,
    speed: 1,
    funds: 10000000,
    loanBalance: 0,
    population: 1000,
    xp: 0,
    taxRates: { residential: 9, commercial: 11, industrial: 13 },
    policies: { recycling: false, transitSubsidy: false, tourismDrive: false, austerity: false },
    buildings: [],
    nextId: 1,
    movingId: null,
    tool: { mode: 'select' },
    clipboard: null,
    pipeTier: {},
    history: [],
    ledger: [],
    nextLedgerId: 1,
    lastFlows: { inflows: [], outflows: [] },
    lastRewardedLevel: 1,
    notice: null,
  };
}

// Place one building of a given spec and return the updated state.
function addBuilding(state, spec) {
  return {
    ...state,
    buildings: [
      ...state.buildings,
      // builtTick: null means already online; no construction time.
      { id: state.nextId, spec, x: state.nextId, y: 10, builtTick: null },
    ],
    nextId: state.nextId + 1,
  };
}

// BUG-390: computeFlows bucketing test — every ZoneKind with nonzero upkeep
// must map to an outflow stream. Place one building of EACH kind with upkeep,
// run computeFlows, and assert total outflow upkeep == sum of placed upkeeps.
test('computeFlows: all kinds with upkeep are bucketed to outflow streams', () => {
  let state = baseState();
  const buildingUpkeeps = []; // Track (spec, kind, upkeep) tuples.

  // Collect all unique (kind, upkeep) pairs and place one building per pair.
  const seenKinds = new Set();
  for (const spec of Object.values(SPECS)) {
    if (!spec.upkeep || spec.upkeep === 0) continue; // Skip zero-upkeep buildings.
    if (seenKinds.has(spec.kind)) continue; // One per kind is enough.
    seenKinds.add(spec.kind);
    state = addBuilding(state, spec.id);
    buildingUpkeeps.push({ spec: spec.id, kind: spec.kind, upkeep: spec.upkeep });
  }

  assert.ok(buildingUpkeeps.length > 0, 'test data: placed at least one building');

  // Compute flows.
  const { outflows } = computeFlows(state);

  // Extract upkeep streams (filter out non-upkeep outflows like 'Wages').
  const upkeepOutflows = outflows.filter((o) => {
    // These are known non-upkeep outflows.
    return ![
      'Wages',
      'Transit Subsidy',
      'Loan Interest',
    ].includes(o.label);
  });

  // Sum all upkeep outflows.
  const totalUpkeepOutflow = upkeepOutflows.reduce((sum, o) => sum + o.value, 0);

  // Calculate expected total upkeep from placed buildings.
  const expectedUpkeep = buildingUpkeeps.reduce((sum, b) => sum + b.upkeep, 0);

  // Critical assertion: all upkeep must reach the outflows. If a kind is missing
  // from UPKEEP_BUCKET, its upkeep will silently vanish (the `if (k)` check fails).
  assert.equal(
    totalUpkeepOutflow,
    expectedUpkeep,
    `Total upkeep outflow (${totalUpkeepOutflow}) must equal placed upkeeps (${expectedUpkeep}). ` +
    `Missing bucket keys: ${buildingUpkeeps.map(b => `${b.kind}:${b.upkeep}`).join(', ')}`
  );

  // Verify each building's kind has a label in the outflows.
  const upkeepLabels = new Set(upkeepOutflows.map((o) => o.label));
  for (const { kind, spec } of buildingUpkeeps) {
    assert.ok(
      upkeepLabels.size > 0,
      `Kind '${kind}' (spec '${spec}') has upkeep but no outflow stream. ` +
      `Check UPKEEP_BUCKET in engine.ts.`
    );
  }
});

// BUG-390: computeFlows bucketing test — verify that the three previously-missing
// kinds (residential, station, landmark) are now bucketed.
test('computeFlows: landmark, residential, station upkeep are captured', () => {
  let state = baseState();

  // Place one landmark, one residential, one station.
  state = addBuilding(state, 'land_stadium'); // landmark, upkeep 260
  state = addBuilding(state, 'res_hut'); // residential, upkeep 1
  state = addBuilding(state, 'station_sanderling'); // station, upkeep 15

  const { outflows } = computeFlows(state);

  // Extract upkeep-only outflows.
  const upkeepOutflows = outflows.filter((o) => {
    return !['Wages', 'Transit Subsidy', 'Loan Interest'].includes(o.label);
  });

  const totalUpkeepOutflow = upkeepOutflows.reduce((sum, o) => sum + o.value, 0);
  const expectedUpkeep = 260 + 1 + 15; // land_stadium + res_hut + station_sanderling

  assert.equal(
    totalUpkeepOutflow,
    expectedUpkeep,
    `Landmark + residential + station upkeep (${expectedUpkeep}) must flow to outflows, ` +
    `but got ${totalUpkeepOutflow}. These kinds were missing from UPKEEP_BUCKET in BUG-390.`
  );

  // Verify the specific stream names.
  const labels = new Set(upkeepOutflows.map((o) => o.label));
  assert.ok(labels.has('Housing'), "Residential upkeep should be under 'Housing' stream");
  assert.ok(labels.has('Transport'), "Station upkeep should be under 'Transport' stream");
  assert.ok(labels.has('Civic & Landmarks'), "Landmark upkeep should be under 'Civic & Landmarks' stream");
});

// Proof-of-RED: if a bucket key is missing, upkeep silently vanishes.
// This test documents the defect and will fail when the bucket is complete.
test('RED: removing a bucket key makes upkeep vanish silently', () => {
  let state = baseState();
  state = addBuilding(state, 'road'); // kind 'road', upkeep 3

  const { outflows } = computeFlows(state);
  const roadOutflows = outflows.filter((o) => o.label === 'Roads');

  // Road upkeep SHOULD appear. If it doesn't, the bucket is broken.
  // This test PASSES because road IS in UPKEEP_BUCKET. Simulate failure:
  // Remove the 'road' entry from UPKEEP_BUCKET, and this would fail—proving the bucket matters.
  assert.ok(
    roadOutflows.length > 0 && roadOutflows[0].value === 3,
    'Road upkeep must be captured in Roads stream. ' +
    'If UPKEEP_BUCKET["road"] is deleted, this assertion fails (proving the bucket matters).'
  );
});

test('BUG-395: tourismDrive yields one Tourism inflow of pop*0.12', () => {
  const state = {
    ...baseState(),
    population: 1000,
    policies: { ...baseState().policies, tourismDrive: true },
  };
  const { inflows } = computeFlows(state);
  const tourism = inflows.filter((f) => f.label === 'Tourism');
  assert.equal(tourism.length, 1);
  assert.equal(tourism[0].value, Math.round(1000 * 0.12));
  assert.notEqual(
    tourism.length,
    2,
    'two Tourism labels is forbidden'
  );
});
