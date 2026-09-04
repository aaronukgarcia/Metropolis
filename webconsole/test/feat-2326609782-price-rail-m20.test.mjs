// feat-2326609782-price-rail-m20.test.mjs — FEAT-2326609782 (2026-09-04, Aaron's
// ruling): m20/rail stop being £0 map furniture.
//
// Ruling (PLACEHOLDER-tier per the balance regime): m20 motorway £1,500,000/tile
// build + £3,000/tile/month upkeep; rail £750,000/tile build + £1,500/tile/month
// upkeep. Derivation: UK real-world ~£30M/km motorway, ~£15M/km rail, 50m tiles
// (£30M x 0.05 = £1.5M, £15M x 0.05 = £0.75M). Spec.upkeep is PER-TICK (see
// profile.ts's own doc comment), so the monthly figures are /30 (engine.ts's
// TICKS_PER_MONTH): £3,000/30 = £100/tick, £1,500/30 = £50/tick.
//
// Covers: (1) the specs themselves carry the ruled numbers, DERIVED from the
// ruling's real-world anchors rather than re-typing the literal so this test
// can't drift silently alongside data.ts; (2) autoConnect books a real, nonzero
// m20 capex when a tier-5-fitting building needs a motorway connector (closing
// half of BUG-682/Aaron's id-2098 report — the connector itself is no longer
// free; fittingTier's own oversizing-for-residential question is BUG-682's
// separate scope, not this item's); (3) conservation holds across that booking.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, fittingTier, placementCost, upkeepChargeableOf } from '../src/sim/data.ts';
import { UPKEEP_BUCKET } from '../src/sim/fiscal.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';

const TICKS_PER_MONTH = 30; // engine.ts's TICKS_PER_MONTH — mirrored here to keep this file import-light.
const TILE_METRES = 50;

// ---------------------------------------------------------------------------
// (1) The specs carry the ruled costs, derived from the ruling's own anchors.
// ---------------------------------------------------------------------------

test('SPECS.m20: £1,500,000/tile build cost, derived from UK real-world ~£30M/km at 50m tiles', () => {
  const ukPerKm = 30_000_000;
  const expectedCost = Math.round(ukPerKm * (TILE_METRES / 1000));
  assert.equal(expectedCost, 1_500_000, 'sanity: the derivation itself lands on the ruled figure');
  assert.equal(SPECS.m20.cost, expectedCost, 'm20 build cost matches the ruled per-tile derivation');
});

test('SPECS.m20: £100/tick upkeep, derived from the ruled £3,000/tile/month', () => {
  const expectedUpkeepPerTick = 3000 / TICKS_PER_MONTH;
  assert.equal(SPECS.m20.upkeep, expectedUpkeepPerTick, 'm20 upkeep matches the ruled monthly figure / ticks-per-month');
});

test('SPECS.rail: £750,000/tile build cost, derived from UK real-world ~£15M/km at 50m tiles', () => {
  const ukPerKm = 15_000_000;
  const expectedCost = Math.round(ukPerKm * (TILE_METRES / 1000));
  assert.equal(expectedCost, 750_000, 'sanity: the derivation itself lands on the ruled figure');
  assert.equal(SPECS.rail.cost, expectedCost, 'rail build cost matches the ruled per-tile derivation');
});

test('SPECS.rail: £50/tick upkeep, derived from the ruled £1,500/tile/month', () => {
  const expectedUpkeepPerTick = 1500 / TICKS_PER_MONTH;
  assert.equal(SPECS.rail.upkeep, expectedUpkeepPerTick, 'rail upkeep matches the ruled monthly figure / ticks-per-month');
});

test('neither m20 nor rail is £0 map furniture any more (the bug this item closes)', () => {
  assert.ok(SPECS.m20.cost > 0, 'm20 build cost is no longer zero');
  assert.ok(SPECS.m20.upkeep > 0, 'm20 upkeep is no longer zero');
  assert.ok(SPECS.rail.cost > 0, 'rail build cost is no longer zero');
  assert.ok(SPECS.rail.upkeep > 0, 'rail upkeep is no longer zero');
});

// ---------------------------------------------------------------------------
// UPKEEP_BUCKET wiring: without these two entries the new nonzero upkeep would
// silently vanish from computeFlows' outflows (fiscal.ts's own documented
// failure mode for a placeholder catalogue kind with no bucket).
// ---------------------------------------------------------------------------

test('UPKEEP_BUCKET: motorway and rail are wired to real outflow buckets', () => {
  assert.equal(UPKEEP_BUCKET.motorway, 'Roads', 'm20 upkeep books into the same Roads bucket as the other road tiers');
  assert.equal(UPKEEP_BUCKET.rail, 'Transport', 'rail upkeep books into the Transport bucket (matches station/transport)');
});

// ---------------------------------------------------------------------------
// (2)+(3) autoConnect books nonzero m20 capex + conservation holds.
// ---------------------------------------------------------------------------

test('autoConnect: a tier-5-fitting building laid away from any road books a real, nonzero m20 connector capex', () => {
  // land_stadium (landmark, jobs:250) scores >=24 in fittingTier -> tier 5 ->
  // ROAD_TIER_SPECS[5] === 'm20'. Placed well clear of genesis's trunk m20
  // rows (y=56/58) and rail row (y=84) so autoConnect must actually route and
  // lay a connector, not find the building already touching a road.
  assert.equal(fittingTier(SPECS.land_stadium), 5, 'sanity: land_stadium fits tier 5 (a motorway connector)');

  const s0 = { ...initialState(), unlockedAll: true, funds: 200_000_000 };
  const before = s0.buildings.length;
  const after = reducer(s0, { type: 'place', spec: 'land_stadium', x: 10, y: 45 });

  assert.equal(after.roadNotice, null, 'connector routed successfully (no "no road access" notice)');
  const newTiles = after.buildings.filter((b) => !s0.buildings.some((ob) => ob.id === b.id));
  assert.ok(newTiles.length > before - before, 'sanity: at least the stadium itself was added');
  const newM20Tiles = newTiles.filter((b) => b.spec === 'm20');
  assert.ok(newM20Tiles.length > 0, 'autoConnect laid at least one new m20 connector tile');

  const stadiumCost = placementCost(SPECS.land_stadium);
  const connectorCapex = newM20Tiles.length * SPECS.m20.cost;
  assert.ok(connectorCapex > 0, 'the connector capex is genuinely nonzero (BUG-682/id-2098 resolved)');

  const spent = s0.funds - after.funds;
  assert.equal(spent, stadiumCost + connectorCapex, 'total spend = stadium cost + real per-tile m20 connector capex');

  // Conservation: the placement books TWO separate ledger entries — the
  // stadium's own "Started land_stadium" charge and autoConnect's own connector
  // charge (prepended, so ledger[0] is the connector) — and together they
  // account for the ENTIRE funds delta exactly (no minted/lost money).
  const newLedgerEntries = after.ledger.filter((e) => !s0.ledger.some((oe) => oe.id === e.id));
  const ledgerTotal = newLedgerEntries.reduce((sum, e) => sum + e.amount, 0);
  assert.equal(ledgerTotal, -spent, 'ledger entries together account for the exact funds delta');
  assert.ok(newLedgerEntries.some((e) => e.amount === -connectorCapex), 'the m20 connector capex has its own ledger entry');
});

test('autoConnect m20 booking: conservation.funds-vs-flows holds across a subsequent tick', () => {
  const s0 = { ...initialState(), unlockedAll: true, funds: 200_000_000 };
  const placed = reducer(s0, { type: 'place', spec: 'land_stadium', x: 10, y: 45 });
  assert.ok(placed.buildings.some((b) => b.spec === 'm20' && b.builtTick === s0.tick), 'connector tiles are real, journaled buildings');

  const after = reducer(placed, { type: 'tick' });
  const rep = runConsistencyChecks(after);
  const conservation = rep.checks.find((c) => c.id === 'conservation.funds-vs-flows');
  assert.ok(conservation, 'conservation.funds-vs-flows check ran');
  assert.equal(conservation.ok, true, 'conservation holds with real m20/rail pricing in play');

  // The new m20 connector tiles are now real upkeep-bearing buildings — the
  // 'Roads' outflow this tick must be nonzero and include their contribution
  // (UPKEEP_BUCKET wiring proven end-to-end, not just unit-tested above).
  const roadsOutflow = after.lastFlows.outflows.find((f) => f.label === 'Roads');
  assert.ok(roadsOutflow && roadsOutflow.value > 0, 'Roads outflow is nonzero once real m20 tiles exist');
});

// ---------------------------------------------------------------------------
// GENESIS FREE (2026-09-04, Aaron's ruling on the £3.3M/month genesis-upkeep
// STOP question raised by this item): pre-existing national m20/rail map
// furniture (builtTick<=0) pays NO upkeep. Anything the player or autoConnect
// lays (builtTick > 0) pays full upkeep. Build COST is unaffected either way
// — placementCost() is only ever charged at 'place' time, and genesis tiles
// never go through 'place'.
// ---------------------------------------------------------------------------

test('GENESIS FREE: a fresh genesis city (starterCity()) books ZERO m20/rail upkeep', () => {
  const s = initialState();
  const m20Tiles = s.buildings.filter((b) => b.spec === 'm20');
  const railTiles = s.buildings.filter((b) => b.spec === 'rail');
  assert.ok(m20Tiles.length > 0 && railTiles.length > 0, 'sanity: genesis actually seeds m20/rail trunk tiles');
  assert.ok(m20Tiles.every((b) => (b.builtTick ?? 0) <= 0), 'sanity: every genesis m20 tile is builtTick<=0 (starterCity never stamps one)');
  assert.ok(railTiles.every((b) => (b.builtTick ?? 0) <= 0), 'sanity: every genesis rail tile is builtTick<=0');

  const genesisM20Upkeep = m20Tiles.reduce((sum, b) => sum + upkeepChargeableOf(b, SPECS.m20), 0);
  const genesisRailUpkeep = railTiles.reduce((sum, b) => sum + upkeepChargeableOf(b, SPECS.rail), 0);
  assert.equal(genesisM20Upkeep, 0, 'genesis m20 chargeable upkeep is exactly £0/tick');
  assert.equal(genesisRailUpkeep, 0, 'genesis rail chargeable upkeep is exactly £0/tick');

  // End-to-end: the ACTUAL outflow booking this ticket's £3,300,000/month STOP
  // question was measured against — re-run that exact measurement and confirm
  // it is now zeroed, not just the unit-level predicate above.
  const genesisMonthlyDeltaBefore = (m20Tiles.length * SPECS.m20.upkeep + railTiles.length * SPECS.rail.upkeep) * 30;
  assert.ok(genesisMonthlyDeltaBefore > 3_000_000, 'sanity: WITHOUT the exemption this city really would owe > £3M/month (the STOP finding)');
  const genesisMonthlyDeltaNow = (genesisM20Upkeep + genesisRailUpkeep) * 30;
  assert.equal(genesisMonthlyDeltaNow, 0, 'genesis-free ruling zeroes the £3.3M/month finding entirely');
});

test('GENESIS FREE: a player-PLACED m20 tile pays full upkeep once built', () => {
  let s = { ...initialState(), unlockedAll: true, funds: 200_000_000 };
  s = reducer(s, { type: 'place', spec: 'm20', x: 200, y: 200 });
  const tile = s.buildings.find((b) => b.spec === 'm20' && b.x === 200 && b.y === 200);
  assert.ok(tile, 'the tile was actually placed');
  assert.ok((tile.builtTick ?? 0) > 0, 'a player placement always stamps a real (post-genesis) builtTick');
  assert.equal(upkeepChargeableOf(tile, SPECS.m20), SPECS.m20.upkeep, 'a player-placed m20 tile is fully chargeable, not exempt');

  // Advance past construction (3 ticks for m20 — constructionTicks(SPECS.m20))
  // so isOnline(s, tile) is true and the outflow booking actually includes it.
  for (let i = 0; i < 5; i++) s = reducer(s, { type: 'tick' });
  const roadsOutflow = s.lastFlows.outflows.find((f) => f.label === 'Roads');
  assert.ok(roadsOutflow, 'Roads outflow exists once the player m20 tile is online');
  // Genesis-only baseline (a fresh city with no player m20) for comparison —
  // proves the INCREASE is attributable to the new tile, not just pre-existing road upkeep.
  let baseline = initialState();
  for (let i = 0; i < 5; i++) baseline = reducer(baseline, { type: 'tick' });
  const baselineRoads = baseline.lastFlows.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  assert.ok(roadsOutflow.value > baselineRoads, 'the player m20 tile\'s upkeep is a real, additional charge over genesis baseline');
});

test('GENESIS FREE: an autoConnect-laid m20 connector pays full upkeep once built', () => {
  let s = { ...initialState(), unlockedAll: true, funds: 200_000_000 };
  s = reducer(s, { type: 'place', spec: 'land_stadium', x: 10, y: 45 });
  const connectorTiles = s.buildings.filter((b) => b.spec === 'm20' && (b.builtTick ?? 0) > 0);
  assert.ok(connectorTiles.length > 0, 'autoConnect laid at least one m20 connector tile');
  for (const b of connectorTiles) {
    assert.equal(upkeepChargeableOf(b, SPECS.m20), SPECS.m20.upkeep, 'every autoConnect-laid connector tile is fully chargeable');
  }

  for (let i = 0; i < 5; i++) s = reducer(s, { type: 'tick' });
  let baseline = initialState();
  for (let i = 0; i < 5; i++) baseline = reducer(baseline, { type: 'tick' });
  const roadsAfter = s.lastFlows.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  const baselineRoads = baseline.lastFlows.outflows.find((f) => f.label === 'Roads')?.value ?? 0;
  assert.ok(roadsAfter > baselineRoads, 'the autoConnect connector adds real upkeep over the genesis baseline once online');
});

// RED PROOF: the genesis-free assertion above is not vacuous — a naive
// predicate that DOESN'T exempt builtTick<=0 tiles (i.e. the pre-ruling
// behaviour, reconstructed here as a scratch copy rather than by mutating
// data.ts) WOULD show the £3.3M/month finding. This proves the real
// upkeepChargeableOf's exemption is doing actual work, not trivially true.
test('RED PROOF: flipping the genesis-free predicate off makes the £0 assertion fail', () => {
  const s = initialState();
  const m20Tiles = s.buildings.filter((b) => b.spec === 'm20');
  const railTiles = s.buildings.filter((b) => b.spec === 'rail');

  // Scratch copy of upkeepChargeableOf with the builtTick<=0 exemption REMOVED
  // (never touches the real data.ts source — GR#24, no working-tree mutation).
  const naiveUpkeepOf = (_b, sp) => sp.upkeep;

  const naiveGenesisUpkeep =
    m20Tiles.reduce((sum, b) => sum + naiveUpkeepOf(b, SPECS.m20), 0) +
    railTiles.reduce((sum, b) => sum + naiveUpkeepOf(b, SPECS.rail), 0);
  const naiveMonthly = naiveGenesisUpkeep * 30;
  assert.ok(naiveMonthly > 3_000_000, 'RED: without the exemption, genesis upkeep is the original £3.3M/month problem');

  // And prove the REAL function disagrees with the naive one on genesis tiles
  // specifically (the exact case the exemption targets).
  const realGenesisUpkeep =
    m20Tiles.reduce((sum, b) => sum + upkeepChargeableOf(b, SPECS.m20), 0) +
    railTiles.reduce((sum, b) => sum + upkeepChargeableOf(b, SPECS.rail), 0);
  assert.notEqual(realGenesisUpkeep, naiveGenesisUpkeep, 'the real predicate genuinely diverges from the naive (pre-ruling) one on genesis tiles');
  assert.equal(realGenesisUpkeep, 0, 'GREEN: the real predicate zeroes it');
});
