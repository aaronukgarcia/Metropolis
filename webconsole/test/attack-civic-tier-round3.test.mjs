// attack-civic-tier-round3.test.mjs — INDEPENDENT DESTRUCTIVE ROUND (GR#23),
// anti-pattern adjudication half. Attacker: opus-round-civic-tier.
//
// Q: does the jobs-last field-order fix accidentally enable coverage-
// destroying consolidations the design never intended, and does CEIL-3 stop
// them? Answered by driving the REAL mutation lane, not by inspection.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, computeRoadConnectivity } from '../src/sim/data.ts';
import {
  initialState,
  reducer,
  TICKS_PER_MONTH,
  CONSOLIDATOR_UNLOCK_LEVEL,
  xpForLevel,
  levelOf,
} from '../src/sim/engine.ts';
import { consolidationLadder, capacityOf } from '../src/sim/consolidator.ts';

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 20_000_000_000,
    tick: 0,
    consolidatorEnabled: false,
    consolidatorLog: [],
    xp: xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL),
    lastRewardedLevel: levelOf(xpForLevel(CONSOLIDATOR_UNLOCK_LEVEL)),
    consolidatorMode: 'monthly-twelfth',
    ...over,
  };
}
function roadRow(y, maxX) {
  const roads = [];
  for (let x = 0; x <= maxX; x++) roads.push({ id: 1000 + y * 1000 + x, spec: 'road', x, y, builtTick: -1000 });
  return roads;
}
const withConnectivity = (s) => ({ ...s, roadConnectivity: computeRoadConnectivity(s) });
function advanceToNextBoundary(s) {
  let cur = s;
  do {
    cur = reducer(cur, { type: 'tick' });
  } while (cur.tick % TICKS_PER_MONTH !== 0);
  return cur;
}
const countSpec = (s, id) => s.buildings.filter((b) => b.spec === id).length;

describe('ATTACK-11: undeclared blast-radius rungs', () => {
  test('the reorder created col_sixth->uni and bus_station->grand_terminus, NOT declared by the author', () => {
    const ladder = consolidationLadder();
    const find = (f, t) => ladder.find((e) => e.from === f && e.to === t);
    const cu = find('col_sixth', 'uni');
    const bg = find('bus_station', 'grand_terminus');
    // eslint-disable-next-line no-console
    console.log('UNDECLARED RUNGS:', JSON.stringify({ col_sixth_to_uni: cu, bus_station_to_grand_terminus: bg }));
    // grand_terminus is kind 'transport', NOT kind 'station' — so it is NOT
    // covered by CONSOLIDATION_EXEMPT_KINDS, contrary to the estate's own
    // doc comment in consolidator.ts.
    assert.equal(SPECS.grand_terminus.kind, 'transport', 'estate comment claims terminus is exempt via kind station');
    assert.ok(cu, 'col_sixth->uni rung present');
    assert.ok(bg, 'bus_station->grand_terminus rung present');
  });
});

describe('ATTACK-12: clinic/ambulance -> teaching hospital anti-pattern', () => {
  test('40 Clinics in one section: does the mutation lane actually eat them?', () => {
    const clinics = [];
    for (let i = 0; i < 40; i++) {
      clinics.push({ id: 300 + i, spec: 'hea_clinic', x: 1 + (i % 14), y: 1 + Math.floor(i / 14), builtTick: -1000 });
    }
    // Family headroom elsewhere so CEIL-3 is not trivially the only blocker.
    const headroom = [
      { id: 900, spec: 'hea_teaching', x: 200, y: 200, builtTick: -1000 },
      { id: 901, spec: 'hea_teaching', x: 210, y: 200, builtTick: -1000 },
    ];
    let s = withConnectivity(mk({ buildings: [...roadRow(0, 15), ...clinics, ...headroom] }));
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 24; m++) s = advanceToNextBoundary(s);
    const built = countSpec(s, 'hea_teaching') - 2;
    // eslint-disable-next-line no-console
    console.log(`CLINIC SCENARIO: clinics left=${countSpec(s, 'hea_clinic')} hospitals=${countSpec(s, 'hea_hospital')} NEW teaching=${built}`);
    // eslint-disable-next-line no-console
    console.log('LOG:', JSON.stringify((s.consolidatorLog ?? []).slice(0, 3).map((l) => ({ t: l.transactions?.map((x) => `${x.fromSpec}->${x.toSpec}x${x.groupCount}`), skipped: l.skipped }))));
    // F1 MUTATION-PROOF: clinics may still legitimately escalate toward a
    // General Hospital (kept as a live in-family case elsewhere), but a
    // clinic must NEVER be eaten by a TEACHING hospital — that was the
    // "latent, blocked only by an alphabetical tiebreak" finding. Removing
    // careTier from the catalogue (or from familyKeyOf) resurrects this rung
    // and turns `built` positive, redding this assertion.
    assert.equal(built, 0, 'no NEW teaching hospital may be built by consuming local clinic coverage');
  });

  test('13 Ambulance Stations: the NEW rung the reorder enabled (was impossible before — 15k*4 > 40k hospital)', () => {
    const amb = [];
    for (let i = 0; i < 13; i++) {
      amb.push({ id: 400 + i, spec: 'hea_ambulance', x: 1 + (i % 7) * 2, y: 1 + Math.floor(i / 7) * 2, builtTick: -1000 });
    }
    const headroom = [
      { id: 900, spec: 'hea_teaching', x: 200, y: 200, builtTick: -1000 },
      { id: 901, spec: 'hea_teaching', x: 210, y: 200, builtTick: -1000 },
    ];
    let s = withConnectivity(mk({ buildings: [...roadRow(0, 15), ...amb, ...headroom] }));
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 24; m++) s = advanceToNextBoundary(s);
    // eslint-disable-next-line no-console
    console.log(`AMBULANCE SCENARIO: ambulance left=${countSpec(s, 'hea_ambulance')} NEW teaching=${countSpec(s, 'hea_teaching') - 2}`);
    // eslint-disable-next-line no-console
    console.log('AMBULANCE cap=', capacityOf(SPECS.hea_ambulance), 'HOSPITAL cap=', capacityOf(SPECS.hea_hospital), '(4x ambulance = 60000 > 40000, hence no pre-existing rung)');
    // F1 MUTATION-PROOF (the P1 headline bug): 13 Ambulance Stations must
    // stay 13 Ambulance Stations — a background consolidation pass must
    // never delete emergency-response coverage in favour of a hospital.
    // Before the careTier fix this reproduced exactly as the brief
    // described: ambulance count fell and a NEW teaching hospital appeared.
    assert.equal(countSpec(s, 'hea_ambulance'), 13, 'ambulance stations must never be consolidated away');
    assert.equal(countSpec(s, 'hea_teaching') - 2, 0, 'no NEW teaching hospital may be built by consuming ambulance coverage');
  });
});
