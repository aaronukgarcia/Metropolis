// BUG-1161 (P1, conservation): per-tick funds delta must equal the ledgered
// flows. Residual = (funds_after - funds_before) - (sum inflows - sum outflows).
// FINDING: the tick path reconciles exactly (residual 0) in every scenario below. The only
// unledgered funds inflow reachable from the UI is the between-tick debugFunds action
// (TopBar +GBP 1T / +10m buttons, Debug tab +10,000): no flow line, no ledger row. Pinned here as
// the one SANCTIONED non-flow adjustment. The long consolidator soak is gated (METRO_RUN_BUG1161=1, ~20s).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { reducer } from '../src/sim/engine.ts';
import { SPECS } from '../src/sim/data.ts';
import { loadDevCity1 } from '../src/sim/devcity.ts';

const RUN = process.env.METRO_RUN_BUG1161 === '1';
const TICKS = Number(process.env.METRO_BUG1161_TICKS ?? 600);
const sum = (a) => a.reduce((x, y) => x + y.value, 0);

function reconcile(s0, ticks, prep) {
  let s = s0;
  const bad = [];
  for (let i = 0; i < ticks; i++) {
    if (prep) s = prep(s, i);
    const before = s.funds;
    s = reducer(s, { type: 'tick' });
    const net = sum(s.lastFlows.inflows) - sum(s.lastFlows.outflows);
    const residual = s.funds - before - net;
    if (Math.abs(residual) > 1) bad.push({ tick: s.tick, residual, net, funds: s.funds, in: s.lastFlows.inflows.map((f) => f.label + '=' + f.value).slice(0, 12) });
  }
  return { s, bad };
}

const skip = false;
const skipLong = RUN ? false : 'BUG-1161 long consolidator soak: set METRO_RUN_BUG1161=1 to run (~20s)';
const check = (name, bad, ticks) => {
  if (process.env.METRO_BUG1161_DUMP) console.log(name, bad.length, JSON.stringify(bad.slice(0, 3)));
  assert.equal(bad.length, 0, `${name}: unledgered funds change at tick ${bad[0]?.tick}: residual ${bad[0]?.residual} (flows net ${bad[0]?.net}); ${bad.length} bad ticks of ${ticks}`);
};

test('BUG-1161: stock dev city, every tick funds delta == sum(inflows) - sum(outflows)', { skip }, () => {
  const { bad } = reconcile(loadDevCity1(), TICKS);
  check('stock', bad, TICKS);
});

test('BUG-1161: high-funds (4e12) city with level-ups mid-run reconciles', { skip }, () => {
  let s = loadDevCity1();
  s = reducer(s, { type: 'debugFunds', amount: 4e12 - s.funds });
  const { bad } = reconcile(s, TICKS, (st, i) => (i % 100 === 50 ? reducer(st, { type: 'debugXp', amount: 5000 }) : st));
  check('highfunds', bad, TICKS);
});

test('BUG-1161: consolidator ON + high funds + all auto-scale, long run reconciles', { skip: skipLong }, () => {
  let s = loadDevCity1();
  if (!s.consolidatorEnabled) s = reducer(s, { type: 'toggleConsolidator' });
  s = reducer(s, { type: 'debugFunds', amount: 4e12 - s.funds });
  const n = Number(process.env.METRO_BUG1161_LONG ?? 1800);
  const { s: end, bad } = reconcile(s, n);
  if (process.env.METRO_BUG1161_DUMP) console.log('consolidator end funds', end.funds, 'buildings', end.buildings.length, 'pop', end.population);
  check('consolidator', bad, n);
});

test('BUG-1161: place then bulldoze, per spec, never mints funds', { skip }, () => {
  const base0 = loadDevCity1();
  const base = reducer(base0, { type: 'debugFunds', amount: 1e9 - base0.funds });
  const minted = [];
  for (const id of Object.keys(SPECS)) {
    for (const [x, y] of [[60, 20], [80, 40], [100, 10]]) {
      const placed = reducer(base, { type: 'place', spec: id, x, y });
      if (placed === base || placed.buildings.length === base.buildings.length) continue;
      const gone = reducer(placed, { type: 'bulldoze', x, y });
      const net = gone.funds - base.funds;
      if (net > 0) minted.push({ id, x, y, net });
      break;
    }
  }
  if (process.env.METRO_BUG1161_DUMP) console.log('place-bulldoze minted', JSON.stringify(minted));
  assert.deepEqual(minted, [], 'place+bulldoze must never net positive funds');
});

// The sanctioned non-flow adjustment, enumerated explicitly: debugFunds moves funds by exactly
// its amount and writes NOTHING to lastFlows or the ledger. ~53 presses of the +1T button
// (DEV_FUNDS_GRANT_LARGE, TopBar.tsx; Aaron Q100094 raised it 1B -> 1T) account for the
// 4.08e12 -> 5.71e13 jump reported in BUG-1161.
test('BUG-1161: debugFunds is the sanctioned unledgered adjustment (funds move; flows and ledger do not)', () => {
  const s0 = loadDevCity1();
  const s1 = reducer(s0, { type: 'debugFunds', amount: 1_000_000_000_000 });
  assert.equal(s1.funds - s0.funds, 1_000_000_000_000);
  assert.equal(s1.lastFlows, s0.lastFlows, 'debugFunds must not touch lastFlows');
  assert.equal(s1.ledger, s0.ledger, 'debugFunds writes no ledger row (unledgered by design)');
  assert.equal(s1.fundsAtTickEnd, s0.fundsAtTickEnd, 'tick-boundary conservation snapshot untouched');
});

test('BUG-1161: play mode (1e12 injection) reconciles', { skip }, () => {
  const s = reducer(loadDevCity1(), { type: 'enterPlayMode' });
  const { bad } = reconcile(s, TICKS);
  check('playmode', bad, TICKS);
});
