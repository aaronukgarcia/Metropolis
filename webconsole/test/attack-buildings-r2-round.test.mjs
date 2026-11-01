// attack-buildings-r2-round.test.mjs - FEAT-9995385590 destructive round r2
// (opus-round-r2-buildings). Regressions kept from the round over the reworked
// "all the buildings built" estate.
//
// T1 (traffic table): every landUseDensityMapping entry equals the band the
//    file's own densityBandMagnitudeThresholds give its tier-0 magnitude, and
//    the runtime band agrees. The existing monotone test only catches ORDER
//    violations; this pins the exact derivation rule for all entries.
// T2 (F1 money): a reduced city of nothing but the eight staffed leisure
//    specs pays wages on every filled leisure job; sector sum == totalJobs.
// T3 (F3): the reducer refuses a third pow_hvdc placement (maxPerCity 2).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { SPECS, capacityAtTier, totalJobs, totalJobsBySector, filledJobsBySector, computeRoadConnectivity, remainingAllowance } from '../src/sim/data.ts';
import { sectorWagesPerTick } from '../src/sim/fiscal.ts';
import { landUseDensityBandFor } from '../src/sim/trafficModeSplit.ts';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const LOCAL = JSON.parse(readFileSync(path.join(repoRoot, 'data', 'traffic', 'mode_split_local.json'), 'utf8'));

function tierZeroMagnitude(sp) {
  if (sp.jobs != null) {
    const shared = sp.residents != null || sp.children != null || sp.served != null;
    return shared ? sp.jobs : capacityAtTier(sp, 0);
  }
  if (sp.residents != null) return capacityAtTier(sp, 0);
  return 0;
}

function bandByRule(mag) {
  const th = LOCAL.densityBandMagnitudeThresholds;
  let band = th[0].band;
  for (const t of th) {
    if (t.minMagnitude <= mag) band = t.band;
    else break;
  }
  return band;
}

test('T1: every landUseDensityMapping entry is exactly the threshold band of its tier-0 magnitude, and the runtime agrees', () => {
  const bad = [];
  for (const [id, band] of Object.entries(LOCAL.landUseDensityMapping)) {
    const sp = SPECS[id];
    assert.ok(sp, `mapping entry ${id} has no spec`);
    const mag = tierZeroMagnitude(sp);
    const want = bandByRule(mag);
    const runtime = landUseDensityBandFor(id, 0);
    if (band !== want || runtime !== band) bad.push(`${id} mag=${mag} json=${band} rule=${want} runtime=${runtime}`);
  }
  assert.deepEqual(bad, []);
});

const roadRow = () => {
  const r = [];
  for (let x = 0; x <= 80; x++) r.push({ id: 10000 + x, spec: 'road', x, y: 100, builtTick: -100000 });
  return r;
};

test('T2: a reduced city of only staffed leisure specs pays wages on every filled leisure job', () => {
  const lei = Object.values(SPECS).filter((sp) => sp.kind === 'leisure' && (sp.jobs ?? 0) > 0).map((sp) => sp.id);
  assert.ok(lei.length >= 8, `expected the eight staffed leisure specs (got ${lei.length})`);
  const leiJobs = lei.reduce((a, id) => a + SPECS[id].jobs, 0);
  const bs = roadRow();
  let x = 2;
  lei.forEach((id, i) => { bs.push({ id: i + 1, spec: id, x, y: 101, builtTick: -100000 }); x += SPECS[id].w; bs[bs.length - 1].x = x - SPECS[id].w; });
  for (const population of [300, 1000, 1_000_000]) {
    let s = { ...initialState(), unlockedAll: true, buildings: bs, population, tick: 5000, nextId: 99999, funds: 1e9 };
    s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
    assert.equal(totalJobs(s), leiJobs, 'every leisure building must be online and counted');
    const by = totalJobsBySector(s);
    assert.equal(Object.values(by).reduce((a, b) => a + b, 0), leiJobs, 'no leisure job may fall outside every wage sector');
    const filled = filledJobsBySector(s);
    const filledSum = Object.values(filled).reduce((a, b) => a + b, 0);
    assert.ok(filledSum > 0, `pop ${population}: some leisure jobs must be filled`);
    const w = sectorWagesPerTick(filled).totalPerTick;
    assert.ok(w > 0, `pop ${population}: filled leisure jobs must carry wages`);
    const flowWages = computeFlows(s).outflows.find((f) => f.label === 'Wages')?.value ?? 0;
    assert.equal(flowWages, w, `pop ${population}: the Wages flow line must equal the sector wage bill`);
  }
});

test('T3: the reducer refuses a third HVDC Interconnector', () => {
  let s = { ...initialState(), unlockedAll: true, buildings: roadRow(), population: 0, tick: 0, nextId: 20000, funds: 1e12 };
  s = { ...s, roadConnectivity: computeRoadConnectivity(s) };
  for (let i = 0; i < 4; i++) s = reducer(s, { type: 'place', spec: 'pow_hvdc', x: 2 + i * 4, y: 101 });
  assert.equal(s.buildings.filter((b) => b.spec === 'pow_hvdc').length, 2);
  assert.equal(remainingAllowance(s, SPECS.pow_hvdc), 0);
});
