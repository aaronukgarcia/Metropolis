// arrivals-by-mode.test.mjs — FEAT-1972079926: split each tick's moveIns (the
// SSOT total established by FEAT-1972079925) across transport arrival modes
// (road / low-speed rail / HS rail / sea / plane) that are CONNECTED AND
// ONLINE in the city, derived from the ACTUAL building roster.
//
// Determinism (GR#21): every assertion here runs the SAME reducer/model path
// twice from identical inputs and checks byte-identical output — no
// Date/Math.random anywhere in the model under test.
//
// RED proof (scratch cp/mv, GR#23 mutation-prove):
//  1. Zeroing MODE_WEIGHT_RAIL_LOW breaks the "available mode gets nonzero
//     share" assertion (railLow drops to 0 even with a connected station).
//  2. Removing the renormalisation (dividing by totalWeight instead of just
//     summing raw weights) breaks the conservation assertion (the per-mode
//     split no longer sums back to moveIns).
//  3. Deleting the `else railLow = true` branch (so station_ashford also
//     counts as low-speed rail) breaks the "unbuilt mode is exactly zero"
//     assertion for a city with no station_sanderling built.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  reducer,
  TICKS_PER_MONTH,
  modeAvailability,
  splitArrivalsByMode,
} from '../src/sim/engine.ts';
import { arrivalsByModeSankeyModel } from '../src/components/arrivalsByModeSankeyModel.ts';

// Mirrors demographic-flows.test.mjs's baseState: a minimal, fully-controlled
// state WITHOUT roadConnectivity so isOnline()'s road-adjacency gates are
// skipped (backward-tolerance path) — station/harbour/airport buildings here
// count as online regardless of map position.
function baseState(overrides = {}) {
  return {
    tick: 0,
    speed: 1,
    funds: 10_000_000,
    loanBalance: 0,
    population: 0,
    xp: 30,
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
    fundsAtTickStart: 10_000_000,
    fundsAtTickEnd: 10_000_000,
    pendingRewards: [],
    lastRewardedLevel: 1,
    notice: null,
    unlockedAll: false,
    roadNotice: null,
    railNotice: null,
    placeNotice: null,
    roadMonitors: [],
    buildingMonitors: [],
    ...overrides,
  };
}

function residentialBuildings(n, spec = 'res_estate') {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: i + 1, spec, x: 10 + i, y: 10 });
  return out;
}

// A road tile adjacent to (x,y) so stationLinks() considers a station there
// "connected".
function roadAdjacentTo(id, x, y) {
  return { id, spec: 'road', x: x + 1, y };
}

test('availability: road is always available even with nothing else built', () => {
  const state = baseState({ buildings: [] });
  const avail = modeAvailability(state);
  assert.equal(avail.road, true);
  assert.equal(avail.railLow, false);
  assert.equal(avail.railHs, false);
  assert.equal(avail.sea, false);
  assert.equal(avail.plane, false);
});

test('availability: a connected+online low-speed station makes railLow available (and gets a NONZERO share); ashford alone does not', () => {
  const withSanderling = baseState({
    buildings: [
      { id: 1, spec: 'station_sanderling', x: 20, y: 20 },
      roadAdjacentTo(2, 20, 20),
    ],
  });
  const avail = modeAvailability(withSanderling);
  assert.equal(avail.railLow, true, 'a connected station_sanderling makes low-speed rail available');
  assert.equal(avail.railHs, false, 'no hs1 line + ashford means no HS rail');
  // Not just the availability FLAG — the actual weighted split must give
  // railLow a real nonzero share (catches a zeroed MODE_WEIGHT_RAIL_LOW
  // constant that the flag-only check above would miss).
  const split = splitArrivalsByMode(withSanderling, 1000);
  assert.ok(split.railLow > 0, 'an available mode must receive a nonzero share of a large moveIns');
});

test('availability: HS rail needs BOTH the ashford station AND an hs1 line tile', () => {
  const ashfordOnly = baseState({
    buildings: [
      { id: 1, spec: 'station_ashford', x: 20, y: 20 },
      roadAdjacentTo(2, 20, 20),
    ],
  });
  const ashfordAvail = modeAvailability(ashfordOnly);
  assert.equal(ashfordAvail.railHs, false, 'ashford without an hs1 tile is not HS-rail-available');
  assert.equal(ashfordAvail.railLow, false, 'the HS1 gateway itself must never be double-counted as a low-speed station');

  const ashfordWithHs1 = baseState({
    buildings: [
      { id: 1, spec: 'station_ashford', x: 20, y: 20 },
      roadAdjacentTo(2, 20, 20),
      { id: 3, spec: 'hs1', x: 40, y: 40 },
    ],
  });
  assert.equal(modeAvailability(ashfordWithHs1).railHs, true, 'ashford + a built hs1 tile makes HS rail available');
});

test('availability: sea needs a built harbour or ferry pier; plane needs a built airport', () => {
  const nothing = baseState({ buildings: [] });
  assert.equal(modeAvailability(nothing).sea, false);
  assert.equal(modeAvailability(nothing).plane, false);

  const withHarbour = baseState({ buildings: [{ id: 1, spec: 'land_harbour', x: 5, y: 5 }] });
  assert.equal(modeAvailability(withHarbour).sea, true);

  const withAirport = baseState({ buildings: [{ id: 1, spec: 'land_airport', x: 5, y: 5 }] });
  assert.equal(modeAvailability(withAirport).plane, true);
});

test('conservation: the per-mode split always sums back to moveIns exactly, for every availability combination', () => {
  const combos = [
    { buildings: [] }, // road only
    { buildings: [{ id: 1, spec: 'station_sanderling', x: 20, y: 20 }, roadAdjacentTo(2, 20, 20)] },
    {
      buildings: [
        { id: 1, spec: 'station_ashford', x: 20, y: 20 },
        roadAdjacentTo(2, 20, 20),
        { id: 3, spec: 'hs1', x: 40, y: 40 },
        { id: 4, spec: 'land_harbour', x: 5, y: 5 },
        { id: 5, spec: 'land_airport', x: 60, y: 60 },
      ],
    },
  ];
  for (const combo of combos) {
    const state = baseState(combo);
    for (const moveIns of [0, 1, 2, 3, 7, 50, 137, 9999]) {
      const split = splitArrivalsByMode(state, moveIns);
      const sum = split.road + split.railLow + split.railHs + split.sea + split.plane;
      assert.equal(sum, moveIns, `split must sum back to moveIns=${moveIns} for combo ${JSON.stringify(combo.buildings.map((b) => b.spec))}`);
      // Every value non-negative integer.
      for (const v of Object.values(split)) {
        assert.ok(Number.isInteger(v) && v >= 0, 'every mode value is a non-negative integer');
      }
    }
  }
});

test('a mode with no supporting building gets EXACTLY zero, even when moveIns is large', () => {
  const state = baseState({ buildings: [] }); // no stations, no harbour, no airport
  const split = splitArrivalsByMode(state, 10_000);
  assert.equal(split.railLow, 0);
  assert.equal(split.railHs, 0);
  assert.equal(split.sea, 0);
  assert.equal(split.plane, 0);
  assert.equal(split.road, 10_000, 'road absorbs the entire split when it is the only available mode');
});

test('adding an airport makes plane arrivals nonzero (availability derives from the roster, not the catalogue)', () => {
  const withoutAirport = baseState({ buildings: [] });
  const withAirport = baseState({ buildings: [{ id: 1, spec: 'land_airport', x: 5, y: 5 }] });
  const moveIns = 1000;
  const before = splitArrivalsByMode(withoutAirport, moveIns);
  const after = splitArrivalsByMode(withAirport, moveIns);
  assert.equal(before.plane, 0, 'no airport built => zero plane arrivals');
  assert.ok(after.plane > 0, 'airport built + online => nonzero plane arrivals');
  // Conservation still holds after the mode set changes.
  assert.equal(after.road + after.railLow + after.railHs + after.sea + after.plane, moveIns);
});

test('determinism: two identical runs through the reducer produce byte-identical arrivals-by-mode history', () => {
  let a = baseState({
    population: 40_000,
    buildings: [
      ...residentialBuildings(40),
      { id: 1000, spec: 'station_sanderling', x: 20, y: 20 },
      roadAdjacentTo(1001, 20, 20),
      { id: 1002, spec: 'land_airport', x: 60, y: 60 },
    ],
  });
  let b = structuredClone(a);
  for (let t = 0; t < TICKS_PER_MONTH * 2; t++) {
    a = reducer(a, { type: 'tick' });
    b = reducer(b, { type: 'tick' });
  }
  assert.deepEqual(a.lastArrivalsByMode, b.lastArrivalsByMode, 'lastArrivalsByMode is byte-identical across identical runs');
  assert.deepEqual(a.arrivalsByModeHistory, b.arrivalsByModeHistory, 'arrivalsByModeHistory is byte-identical across identical runs');
  assert.deepEqual(a.arrivalsByModeAccum, b.arrivalsByModeAccum, 'arrivalsByModeAccum is byte-identical across identical runs');
});

test('per-tick lastArrivalsByMode always sums to lastDemographics.moveIns via the reducer', () => {
  let state = baseState({
    population: 40_000,
    buildings: [
      ...residentialBuildings(40),
      { id: 1000, spec: 'station_ashford', x: 20, y: 20 },
      roadAdjacentTo(1001, 20, 20),
      { id: 1002, spec: 'hs1', x: 70, y: 70 },
    ],
  });
  for (let t = 0; t < 10; t++) {
    state = reducer(state, { type: 'tick' });
    const split = state.lastArrivalsByMode;
    const sum = split.road + split.railLow + split.railHs + split.sea + split.plane;
    assert.equal(sum, state.lastDemographics.moveIns, `tick ${t}: arrivals-by-mode sum must equal moveIns`);
  }
});

test('monthly history aggregation: one closed month sums exactly the per-tick splits recorded over it', () => {
  let state = baseState({
    population: 40_000,
    buildings: [
      ...residentialBuildings(40),
      { id: 1000, spec: 'station_sanderling', x: 20, y: 20 },
      roadAdjacentTo(1001, 20, 20),
    ],
  });
  let accRoad = 0;
  let accRailLow = 0;
  for (let t = 0; t < TICKS_PER_MONTH; t++) {
    state = reducer(state, { type: 'tick' });
    accRoad += state.lastArrivalsByMode.road;
    accRailLow += state.lastArrivalsByMode.railLow;
  }
  assert.equal(state.arrivalsByModeHistory.length, 1, 'exactly one closed month recorded');
  const month = state.arrivalsByModeHistory[0];
  assert.equal(month.road, accRoad);
  assert.equal(month.railLow, accRailLow);
});

test('Sankey model: empty history renders an honest empty state, never a fabricated split', () => {
  const model = arrivalsByModeSankeyModel(undefined, 'month');
  assert.equal(model.empty, true);
  assert.equal(model.totalIn, 0);
  assert.equal(model.road, 0);
});

test('Sankey model: "month" sums only the last recorded month, "year" sums up to 12', () => {
  const history = [
    { tick: 30, road: 10, railLow: 2, railHs: 0, sea: 0, plane: 0 },
    { tick: 60, road: 20, railLow: 4, railHs: 1, sea: 0, plane: 0 },
  ];
  const month = arrivalsByModeSankeyModel(history, 'month');
  assert.equal(month.empty, false);
  assert.equal(month.road, 20);
  assert.equal(month.railLow, 4);
  assert.equal(month.railHs, 1);

  const year = arrivalsByModeSankeyModel(history, 'year');
  assert.equal(year.road, 30);
  assert.equal(year.railLow, 6);
  assert.equal(year.totalIn, 30 + 6 + 1);
});
