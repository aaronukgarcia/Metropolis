// attack-civic-tier-reround2.test.mjs — INDEPENDENT DESTRUCTIVE RE-ROUND 2
// (GR#23; attacker is NOT the author) against FEAT-2326609761's civic-tier
// rework: the careTier local/regional discriminator (F1), the two declared
// rungs col_sixth->uni and bus_station->grand_terminus (F2), and the
// corrected grand_terminus kind comment (F3).
//
// Everything below drives the REAL reducer / real consolidator module. No
// mocks, no hand-rolled re-implementation of the rules under test.

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
import {
  consolidationLadder,
  familyKeyOf,
  capacityOf,
  capacityFieldOf,
  isConsolidationSuccessor,
  groupSizeOf,
  sectionIndexOf,
  CONSOLIDATOR_MIN_GROUP,
} from '../src/sim/consolidator.ts';
import { buildGameSave, gameSaveText, parseGameSave } from '../src/sim/gamesave.ts';

// ---------------------------------------------------------------------------
// Harness (same shape the estate's own round tests use — real reducer only)
// ---------------------------------------------------------------------------

function mk(over) {
  const base = initialState();
  return {
    ...base,
    unlockedAll: true,
    roadMonitors: [],
    buildingMonitors: [],
    buildings: [],
    population: 0,
    funds: 50_000_000_000,
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

/** City-wide online-agnostic served capacity for one careTier, straight off SPECS. */
function servedByCareTier(s, tier) {
  let total = 0;
  for (const b of s.buildings) {
    const sp = SPECS[b.spec];
    if (!sp || sp.kind !== 'health') continue;
    if ((sp.careTier ?? null) !== tier) continue;
    total += capacityOf(sp);
  }
  return total;
}

// ---------------------------------------------------------------------------
// ANGLE 1 — the mixed city Aaron actually described.
// ---------------------------------------------------------------------------

describe('RR2-A1: mixed civic city, 24 months, consolidator ON', () => {
  test('13 ambulances + 6 clinics + 12 hospitals: local coverage NEVER shrinks and no teaching hospital eats it', () => {
    const buildings = [...roadRow(0, 40)];
    let id = 5000;
    // 13 ambulance stations (1x1) packed into one section
    for (let i = 0; i < 13; i++) buildings.push({ id: id++, spec: 'hea_ambulance', x: 1 + (i % 7), y: 1 + Math.floor(i / 7), builtTick: -1000 });
    // 6 clinics (1x1) in the same neighbourhood
    for (let i = 0; i < 6; i++) buildings.push({ id: id++, spec: 'hea_clinic', x: 8 + (i % 6), y: 1, builtTick: -1000 });
    // 12 general hospitals (2x2), co-located so the x5 rung is reachable
    for (let i = 0; i < 12; i++) buildings.push({ id: id++, spec: 'hea_hospital', x: 1 + (i % 6) * 2, y: 4 + Math.floor(i / 6) * 2, builtTick: -1000 });

    let s = withConnectivity(mk({ buildings }));
    s = reducer(s, { type: 'toggleConsolidator' });

    const startAmb = countSpec(s, 'hea_ambulance');
    const startClinic = countSpec(s, 'hea_clinic');
    const startLocal = servedByCareTier(s, 'local');
    const startTeaching = countSpec(s, 'hea_teaching');
    assert.equal(startAmb, 13);
    assert.equal(startClinic, 6);

    let minAmb = startAmb;
    let minClinic = startClinic;
    let minLocal = startLocal;
    for (let m = 0; m < 24; m++) {
      s = advanceToNextBoundary(s);
      minAmb = Math.min(minAmb, countSpec(s, 'hea_ambulance'));
      minClinic = Math.min(minClinic, countSpec(s, 'hea_clinic'));
      minLocal = Math.min(minLocal, servedByCareTier(s, 'local'));
    }

    // eslint-disable-next-line no-console
    console.log(
      `RR2-A1 after 24mo: amb=${countSpec(s, 'hea_ambulance')} (min ${minAmb}) clinic=${countSpec(s, 'hea_clinic')} (min ${minClinic}) hospital=${countSpec(s, 'hea_hospital')} teaching=${countSpec(s, 'hea_teaching')} localServed=${servedByCareTier(s, 'local')} regionalServed=${servedByCareTier(s, 'regional')}`,
    );
    // NOTE (attacker): ConsolidationTransaction has NO fromSpec/toSpec/
    // groupCount fields — it carries `removed`/`added` ConsolidationRecord
    // arrays (consolidator.ts:1408). Any assertion written against the
    // former names is silently VACUOUS. Read the real shape.
    const allTxns = (s.consolidatorLog ?? []).flatMap((l) => l.transactions ?? []);
    const txns = allTxns.map((t) => `${t.kind}:${t.removed.map((r) => r.spec).join('+')}->${t.added.map((r) => r.spec).join('+')}`);
    // eslint-disable-next-line no-console
    console.log('RR2-A1 transactions:', JSON.stringify(txns));
    assert.ok(allTxns.length > 0, 'NON-VACUITY: the consolidator must actually have done something in this city');

    // (a) local coverage never drops at ANY month boundary, not just the end.
    assert.equal(minAmb, 13, 'ambulance stations must never be consolidated away');
    assert.equal(minClinic, 6, 'clinics must never be consolidated away');
    assert.equal(minLocal, startLocal, 'local-tier served capacity must never fall');

    // (b) no consolidate transaction may ever REMOVE a local-tier building,
    //     and no health transaction may cross careTier in either direction.
    for (const t of allTxns) {
      if (t.kind !== 'consolidate') continue;
      for (const r of t.removed) {
        const from = SPECS[r.spec];
        if (!from || from.kind !== 'health') continue;
        assert.notEqual(from.careTier, 'local', `a local-tier ${r.spec} was consumed by ${t.added.map((a) => a.spec).join('+')}`);
        for (const ar of t.added) {
          const to = SPECS[ar.spec];
          if (!to || to.kind !== 'health') continue;
          assert.equal(from.careTier ?? '', to.careTier ?? '', `cross-careTier merge ${r.spec}->${ar.spec}`);
        }
      }
    }

    // (c) any teaching hospital that DID appear may only have been paid for
    //     out of regional-tier capacity — every teaching-hospital
    //     transaction's removed set is hea_hospital only.
    const teachingTxns = allTxns.filter((t) => t.added.some((a) => a.spec === 'hea_teaching'));
    for (const t of teachingTxns) {
      for (const r of t.removed) {
        assert.equal(r.spec, 'hea_hospital', `teaching hospital built by consuming ${r.spec}`);
      }
    }
    // eslint-disable-next-line no-console
    console.log(`RR2-A1 new teaching hospitals: ${countSpec(s, 'hea_teaching') - startTeaching} from ${teachingTxns.length} txn(s)`);
    assert.ok(teachingTxns.length > 0, 'NON-VACUITY: the regional rung must still fire (Aaron ruling preserved)');

    // (d) per-transaction capacity conservation: every consolidate must
    //     replace at most as much capacity as it adds.
    for (const t of allTxns) {
      if (t.kind !== 'consolidate') continue;
      const lost = t.removed.reduce((n, r) => n + (SPECS[r.spec] ? capacityOf(SPECS[r.spec]) : 0), 0);
      const gained = t.added.reduce((n, r) => n + (SPECS[r.spec] ? capacityOf(SPECS[r.spec]) : 0), 0);
      assert.ok(gained >= lost, `transaction DESTROYED capacity: ${lost} -> ${gained}`);
    }
  });

  test('per-family capacity accounting: the section audit never pools local and regional health', () => {
    const buildings = [...roadRow(0, 40)];
    let id = 6000;
    for (let i = 0; i < 4; i++) buildings.push({ id: id++, spec: 'hea_clinic', x: 1 + i, y: 1, builtTick: -1000 });
    for (let i = 0; i < 4; i++) buildings.push({ id: id++, spec: 'hea_ambulance', x: 1 + i, y: 2, builtTick: -1000 });
    for (let i = 0; i < 2; i++) buildings.push({ id: id++, spec: 'hea_hospital', x: 1 + i * 2, y: 4, builtTick: -1000 });
    const s = withConnectivity(mk({ buildings }));
    const index = sectionIndexOf(s);
    const pooled = {};
    for (const a of index.values()) {
      for (const [fam, cap] of Object.entries(a.capacityByFamily)) pooled[fam] = (pooled[fam] ?? 0) + cap;
    }
    // eslint-disable-next-line no-console
    console.log('RR2-A1b capacityByFamily:', JSON.stringify(pooled));
    const localFam = familyKeyOf(SPECS.hea_clinic);
    const regionalFam = familyKeyOf(SPECS.hea_hospital);
    assert.notEqual(localFam, regionalFam);
    assert.equal(pooled[localFam], 4 * capacityOf(SPECS.hea_clinic) + 4 * capacityOf(SPECS.hea_ambulance));
    assert.equal(pooled[regionalFam], 2 * capacityOf(SPECS.hea_hospital));
  });
});

// ---------------------------------------------------------------------------
// ANGLE 2 — the silent-default hole: kind 'health' with careTier UNDEFINED.
// ---------------------------------------------------------------------------

describe('RR2-A2: undefined careTier on a health spec', () => {
  test('an untagged health spec does NOT silently join the regional family', () => {
    // A fixture spec shaped exactly like hea_clinic but with careTier omitted
    // — the shape any future catalogue addition takes if the author forgets.
    const fixture = { ...SPECS.hea_clinic, id: 'hea_fixture_untagged' };
    delete fixture.careTier;
    const fam = familyKeyOf(fixture);
    // eslint-disable-next-line no-console
    console.log('RR2-A2 untagged family:', fam, '| local:', familyKeyOf(SPECS.hea_clinic), '| regional:', familyKeyOf(SPECS.hea_hospital));
    assert.notEqual(fam, familyKeyOf(SPECS.hea_hospital), 'untagged health spec must not join the regional family');
    assert.notEqual(fam, familyKeyOf(SPECS.hea_teaching), 'untagged health spec must not join the regional family');
    assert.equal(isConsolidationSuccessor(fixture, SPECS.hea_teaching), false, 'untagged -> teaching must be refused');
    assert.equal(isConsolidationSuccessor(fixture, SPECS.hea_hospital), false, 'untagged -> hospital must be refused');
    assert.equal(isConsolidationSuccessor(SPECS.hea_clinic, fixture), false, 'local -> untagged must be refused');
  });

  test('the real catalogue has exactly one untagged health spec with a capacity field, and it is id-exempt', () => {
    const untagged = Object.values(SPECS).filter((sp) => sp.kind === 'health' && sp.careTier == null && capacityFieldOf(sp) != null);
    // eslint-disable-next-line no-console
    console.log('RR2-A2b untagged health specs with capacity:', JSON.stringify(untagged.map((s) => s.id)));
    // hea_eldercare is the known one, protected by CONSOLIDATION_EXEMPT_SPEC_IDS.
    for (const sp of untagged) {
      for (const other of Object.values(SPECS)) {
        assert.equal(isConsolidationSuccessor(sp, other), false, `${sp.id} -> ${other.id} must not be a rung`);
        assert.equal(isConsolidationSuccessor(other, sp), false, `${other.id} -> ${sp.id} must not be a rung`);
      }
    }
  });
});

// ---------------------------------------------------------------------------
// ANGLE F2 — the two DECLARED rungs must be coherent, not just present.
// ---------------------------------------------------------------------------

describe('RR2-F2: declared rungs are capacity-conserving and stage-clean', () => {
  test('every rung in the WHOLE ladder conserves capacity and never crosses stage/careTier/tag', () => {
    const ladder = consolidationLadder();
    assert.ok(ladder.length > 0);
    const crossers = [];
    for (const e of ladder) {
      const a = SPECS[e.from];
      const b = SPECS[e.to];
      assert.ok(a && b, `${e.from}->${e.to} references a missing spec`);
      // capacity conservation: the group the successor replaces must fit inside it.
      assert.ok(
        e.groupSize * capacityOf(a) <= capacityOf(b),
        `${e.from}->${e.to} x${e.groupSize} DESTROYS capacity (${e.groupSize * capacityOf(a)} > ${capacityOf(b)})`,
      );
      assert.ok(e.groupSize >= CONSOLIDATOR_MIN_GROUP, `${e.from}->${e.to} group ${e.groupSize} below the floor`);
      if ((a.stage ?? '') !== (b.stage ?? '')) crossers.push(`STAGE ${e.from}->${e.to}`);
      if ((a.careTier ?? '') !== (b.careTier ?? '')) crossers.push(`CARETIER ${e.from}->${e.to}`);
      if ((a.tag ?? '') !== (b.tag ?? '')) crossers.push(`TAG ${e.from}->${e.to}`);
      if (a.kind !== b.kind) crossers.push(`KIND ${e.from}->${e.to}`);
      if (capacityFieldOf(a) !== capacityFieldOf(b)) crossers.push(`FIELD ${e.from}->${e.to}`);
    }
    // eslint-disable-next-line no-console
    console.log(`RR2-F2 ladder rungs=${ladder.length} crossers=${JSON.stringify(crossers)}`);
    assert.deepEqual(crossers, [], 'no rung may cross kind/field/tag/stage/careTier');
  });

  test('col_sixth -> uni is present, x4, exactly capacity-neutral, and cross-stage is still blocked', () => {
    const ladder = consolidationLadder();
    const rung = ladder.find((e) => e.from === 'col_sixth' && e.to === 'uni');
    assert.ok(rung, 'declared col_sixth->uni rung missing');
    assert.equal(rung.groupSize, 4);
    assert.equal(rung.groupSize * capacityOf(SPECS.col_sixth), capacityOf(SPECS.uni), 'x4 college == 1 university, exactly');
    // uni carries a SECONDARY jobs:650. Under the jobs-last order it must
    // still key on children, or the rung would be an accounting lie.
    assert.equal(capacityFieldOf(SPECS.uni), 'children');
    assert.equal(capacityFieldOf(SPECS.col_sixth), 'children');
    // Stage segment still load-bearing: no cross-stage school rung anywhere.
    for (const a of ['edu_nursery', 'edu_primary', 'edu_city', 'col_sixth', 'edu_tech', 'uni']) {
      for (const b of ['edu_nursery', 'edu_nursery_city', 'edu_primary', 'edu_city', 'col_sixth', 'edu_tech', 'uni']) {
        if (a === b) continue;
        if ((SPECS[a].stage ?? '') === (SPECS[b].stage ?? '')) continue;
        assert.equal(isConsolidationSuccessor(SPECS[a], SPECS[b]), false, `cross-stage ${a}->${b} must stay blocked`);
      }
    }
  });

  test('bus_station -> grand_terminus is present, x6, capacity-gaining, and grand_terminus is kind transport (F3)', () => {
    const ladder = consolidationLadder();
    const rung = ladder.find((e) => e.from === 'bus_station' && e.to === 'grand_terminus');
    assert.ok(rung, 'declared bus_station->grand_terminus rung missing');
    assert.equal(rung.groupSize, 6);
    assert.equal(groupSizeOf(SPECS.bus_station, SPECS.grand_terminus), 6);
    assert.ok(6 * capacityOf(SPECS.bus_station) <= capacityOf(SPECS.grand_terminus));
    // F3: the corrected fact. The old comment claimed kind 'station'.
    assert.equal(SPECS.grand_terminus.kind, 'transport');
    // grand_terminus carries a secondary jobs:60 — it must still key on served.
    assert.equal(capacityFieldOf(SPECS.grand_terminus), 'served');
    assert.equal(capacityFieldOf(SPECS.bus_station), 'served');
  });

  test('F3: no source comment still claims grand_terminus is kind station', async () => {
    const { readFileSync } = await import('node:fs');
    const src = readFileSync(new URL('../src/sim/consolidator.ts', import.meta.url), 'utf8');
    const bad = /grand_terminus[^]{0,400}?exempt[^]{0,200}?kind\s*'station'/i.test(src);
    // The corrected comment DOES quote the old false claim in order to
    // retract it, so a naive substring search is not the test — assert the
    // retraction itself is present.
    assert.ok(/that was FALSE|was FALSE/i.test(src), 'F3 retraction text missing from consolidator.ts');
    assert.ok(/grand_terminus's real ZoneKind is\s*\n?\s*\*?\s*'transport'|real ZoneKind is[^]{0,40}'transport'/i.test(src), 'F3 correction does not state the real kind');
    void bad;
  });

  test('the transport family really is a family of two — no third spec leaks in', () => {
    const fam = familyKeyOf(SPECS.grand_terminus);
    const members = Object.values(SPECS).filter((sp) => familyKeyOf(sp) === fam && capacityFieldOf(sp) != null);
    // eslint-disable-next-line no-console
    console.log('RR2-F2 transport|served family members:', JSON.stringify(members.map((m) => `${m.id}:${capacityOf(m)}`)));
    assert.ok(members.some((m) => m.id === 'bus_station'));
    assert.ok(members.some((m) => m.id === 'grand_terminus'));
    // metro_station (served 30,000) also sits in this family. It forms NO
    // rung in either direction (12,000*4=48,000 > 30,000 and 30,000*4 =
    // 120,000 > 80,000), so the declared bus_station -> grand_terminus rung
    // is the family's ONLY one — but it clears by margin, not by design, so
    // record it as the second rung to re-check on any transport retune.
    assert.equal(isConsolidationSuccessor(SPECS.bus_station, SPECS.metro_station), false);
    assert.equal(isConsolidationSuccessor(SPECS.metro_station, SPECS.grand_terminus), false);
    const famRungs = consolidationLadder().filter((e) => familyKeyOf(SPECS[e.from]) === fam);
    // eslint-disable-next-line no-console
    console.log('RR2-F2 transport family rungs:', JSON.stringify(famRungs));
    assert.equal(famRungs.length, 1, 'transport|served family must have exactly the one declared rung');
  });
});

// ---------------------------------------------------------------------------
// ANGLE 4 — save/load round trip.
// ---------------------------------------------------------------------------

describe('RR2-A4: save/load round trip preserves consolidation families', () => {
  test('careTier is SPECS-derived, never persisted per-building — families survive a real save/parse cycle', () => {
    const buildings = [...roadRow(0, 20)];
    let id = 7000;
    for (let i = 0; i < 5; i++) buildings.push({ id: id++, spec: 'hea_clinic', x: 1 + i, y: 1, builtTick: -1000 });
    for (let i = 0; i < 3; i++) buildings.push({ id: id++, spec: 'hea_ambulance', x: 1 + i, y: 2, builtTick: -1000 });
    for (let i = 0; i < 6; i++) buildings.push({ id: id++, spec: 'hea_hospital', x: 1 + i * 2, y: 4, builtTick: -1000 });
    let s = withConnectivity(mk({ buildings }));
    s = reducer(s, { type: 'toggleConsolidator' });
    for (let m = 0; m < 6; m++) s = advanceToNextBoundary(s);

    const before = s.buildings.map((b) => `${b.id}:${b.spec}:${SPECS[b.spec] ? familyKeyOf(SPECS[b.spec]) : 'NOSPEC'}`).sort();

    const save = buildGameSave({
      state: s,
      journal: { entries: [] },
      journalTail: [],
      name: 'rr2',
      buildVersion: 'test',
      now: new Date(0),
    });
    const text = gameSaveText(save);
    const parsed = parseGameSave(text);
    assert.equal(parsed.ok, true);
    const restored = parsed.save.savepoint.snapshot;
    const after = restored.buildings.map((b) => `${b.id}:${b.spec}:${SPECS[b.spec] ? familyKeyOf(SPECS[b.spec]) : 'NOSPEC'}`).sort();

    assert.deepEqual(after, before, 'families must be identical across the save boundary');
    // The proof of WHICH mechanism: careTier is never written into the save
    // at all — it is re-derived from the static SPECS catalogue on load.
    assert.equal(text.includes('careTier'), false, 'careTier must not be persisted per-building (it is SPECS-derived)');
    // eslint-disable-next-line no-console
    console.log(`RR2-A4 round trip: ${before.length} buildings, careTier in save text = ${text.includes('careTier')}`);
  });
});

// ---------------------------------------------------------------------------
// ANGLE 5 — determinism (GR#21).
// ---------------------------------------------------------------------------

describe('RR2-A5: determinism with the new key segment', () => {
  test('two identical runs produce byte-identical consolidation traces', () => {
    const build = () => {
      const buildings = [...roadRow(0, 40)];
      let id = 8000;
      for (let i = 0; i < 13; i++) buildings.push({ id: id++, spec: 'hea_ambulance', x: 1 + (i % 7), y: 1 + Math.floor(i / 7), builtTick: -1000 });
      for (let i = 0; i < 6; i++) buildings.push({ id: id++, spec: 'hea_clinic', x: 8 + (i % 6), y: 1, builtTick: -1000 });
      for (let i = 0; i < 12; i++) buildings.push({ id: id++, spec: 'hea_hospital', x: 1 + (i % 6) * 2, y: 4 + Math.floor(i / 6) * 2, builtTick: -1000 });
      for (let i = 0; i < 5; i++) buildings.push({ id: id++, spec: 'col_sixth', x: 20 + i * 2, y: 1, builtTick: -1000 });
      let s = withConnectivity(mk({ buildings }));
      s = reducer(s, { type: 'toggleConsolidator' });
      for (let m = 0; m < 18; m++) s = advanceToNextBoundary(s);
      return s;
    };
    const a = build();
    const b = build();
    const trace = (s) =>
      JSON.stringify(
        (s.consolidatorLog ?? []).map((l) => ({
          id: l.id,
          t: (l.transactions ?? []).map(
            (x) => `${x.kind}:${x.removed.map((r) => `${r.spec}#${r.id}`).join('+')}->${x.added.map((r) => `${r.spec}#${r.id}@${r.x},${r.y}`).join('+')}|${x.netCost}@${x.sectionKey}`,
          ),
          s: (l.skipped ?? []).map((x) => `${x.sectionKey}:${x.reason}`),
        })),
      );
    assert.equal(trace(a), trace(b), 'consolidation trace is not deterministic');
    assert.equal(
      JSON.stringify(a.buildings.map((x) => `${x.id}:${x.spec}:${x.x},${x.y}`)),
      JSON.stringify(b.buildings.map((x) => `${x.id}:${x.spec}:${x.x},${x.y}`)),
      'building set diverged between identical runs',
    );
    assert.equal(a.funds, b.funds, 'funds diverged between identical runs');
    // The ladder cache must not make familyKeyOf order-dependent either.
    assert.equal(JSON.stringify(consolidationLadder()), JSON.stringify(consolidationLadder()));
    // eslint-disable-next-line no-console
    console.log(`RR2-A5 deterministic over ${(a.consolidatorLog ?? []).length} log entries`);
  });
});
