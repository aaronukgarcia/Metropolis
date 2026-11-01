// buildings-tranche1-services.test.mjs — FEAT-9995385590 lane B: 13 service/stay
// placeholders graduated to real placeable buildings (placeholder balance numbers).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, isPlaceable } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// id -> [kind, name, w, h, unlock, required stat fields (at least one must be > 0 beyond cost/upkeep)]
const EXPECT = {
  lei_gym: ['leisure', 'Gym', 1, 1, 3, ['served', 'jobs']],
  lei_sportsground: ['leisure', 'Sports Ground', 2, 2, 4, ['served', 'jobs']],
  lei_stables: ['leisure', 'Stables', 2, 2, 6, ['tourism', 'jobs']],
  stay_bb: ['leisure', 'B&B / guesthouse', 1, 1, 20, ['tourism', 'jobs']],
  stay_caravan: ['leisure', 'Caravan & camping', 3, 3, 20, ['tourism', 'jobs']],
  stay_hotel: ['leisure', 'City hotel', 2, 2, 20, ['tourism', 'jobs']],
  stay_luxury: ['leisure', 'Luxury hotel', 3, 2, 20, ['tourism', 'jobs']],
  stay_resort: ['leisure', 'Resort campus', 4, 4, 20, ['tourism', 'jobs']],
  air_heliport: ['health', 'Air-Ambulance Pad', 1, 1, 8, ['jobs']],
  death_cemetery: ['health', 'Cemetery', 3, 3, 4, ['jobs']],
  death_crematorium: ['health', 'Crematorium', 2, 2, 7, ['jobs']],
  air_fire_helibase: ['fire', 'Fire Heli Base', 2, 2, 10, ['served', 'jobs']],
  air_police_helibase: ['police', 'Police Heli Base', 2, 2, 10, ['served', 'jobs']],
};

test('13 tranche-1 service specs are real, placeable and keep identity', () => {
  for (const [id, [kind, name, w, h, unlock, fields]] of Object.entries(EXPECT)) {
    const sp = SPECS[id];
    assert.ok(sp, `${id} missing`);
    assert.notEqual(sp.placeholder, true, `${id} still a placeholder`);
    assert.equal(sp.kind, kind, `${id} kind`);
    assert.equal(sp.name, name, `${id} name`);
    assert.equal(sp.w, w, `${id} w`);
    assert.equal(sp.h, h, `${id} h`);
    assert.equal(sp.unlock, unlock, `${id} unlock`);
    assert.ok(sp.cost > 0, `${id} cost`);
    assert.ok(sp.upkeep > 0, `${id} upkeep`);
    assert.ok(!/^Planned/.test(sp.blurb), `${id} blurb still 'Planned'`);
    for (const f of fields) assert.ok(sp[f] > 0, `${id} needs ${f}`);
    const s = { ...initialState(), unlockedAll: true };
    assert.equal(isPlaceable(s, sp), true, `${id} not placeable when unlocked`);
  }
});

test('health-kind death/air specs carry no served (would inflate totalServedCapacity)', () => {
  for (const id of ['air_heliport', 'death_cemetery', 'death_crematorium']) {
    assert.equal(SPECS[id].served, undefined, id);
  }
});
