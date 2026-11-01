// buildings-tranche1-economy.test.mjs — FEAT-9995385590 lane C: twelve commercial /
// industrial / mine placeholders graduated to real, placeable buildings.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SPECS, isPlaceable } from '../src/sim/data.ts';
import { initialState } from '../src/sim/engine.ts';

// id -> [name, w, h, unlock, heavy]
const EXPECT = {
  com_darkstore: ['Dark Store', 2, 2, 9, false],
  com_discounter: ['Discount Store', 2, 2, 5, false],
  farm_abattoir: ['Abattoir', 2, 2, 5, false],
  farm_dairy: ['Dairy', 2, 2, 3, false],
  harbour_fishing: ['Fishing Harbour', 3, 2, 8, false],
  ind_chemworks: ['Chemical Works', 3, 3, 10, true],
  ind_fulfilment: ['Fulfilment Centre', 3, 3, 11, false],
  ind_parcelhub: ['Parcel Hub', 2, 2, 9, false],
  ind_refinery: ['Oil Refinery', 4, 4, 13, true],
  mine_chalk: ['Chalk Pit', 2, 2, 4, true],
  mine_clay: ['Clay Pit', 2, 2, 5, true],
  mine_coal: ['Deep Coal Mine', 3, 3, 7, true],
};

for (const [id, [name, w, h, unlock, heavy]] of Object.entries(EXPECT)) {
  test(`${id} is a real, placeable building`, () => {
    const sp = SPECS[id];
    assert.ok(sp, 'exists');
    assert.ok(!sp.placeholder, 'no placeholder flag');
    assert.equal(sp.name, name);
    assert.equal(sp.w, w);
    assert.equal(sp.h, h);
    assert.equal(sp.unlock, unlock);
    assert.ok(sp.cost > 0, 'cost > 0');
    assert.ok(sp.upkeep > 0, 'upkeep > 0');
    assert.ok(sp.jobs > 0, 'jobs > 0');
    if (heavy) assert.equal(sp.tag, 'pollution');
    else assert.notEqual(sp.tag, 'pollution');
    // placeable once unlocked: god-mode unlockedAll opens the unlock gate
    const s = { ...initialState(), unlockedAll: true };
    assert.ok(isPlaceable(s, sp), 'placeable when unlocked');
  });
}
