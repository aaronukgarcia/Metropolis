// BUG-1186: an old Undo record whose rung was removed from the ladder (lei_stables -> lei_arena)
// must still load and undo exactly. Undo reads only the pass log, never consolidationLadder().
// RED proof: this test does not depend on the guard; it pins that removing the rung cannot strand a record.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { consolidationLadder } from '../src/sim/consolidator.ts';

test('BUG-1186: Undo of a pass logged on a now-removed rung (lei_stables x11 -> lei_arena) restores the stables', () => {
  assert.equal(consolidationLadder().some((r) => r.from === 'lei_stables' && r.to === 'lei_arena'), false, 'rung is gone');
  const base = initialState();
  const removed = [];
  for (let i = 0; i < 11; i++) removed.push({ id: 100 + i, spec: 'lei_stables', x: 4 + (i % 4) * 2, y: 20 + Math.floor(i / 4) * 2, builtTick: -1000, placedBy: 'player' });
  const added = [{ id: 500, spec: 'lei_arena', x: 4, y: 20, builtTick: 0, placedBy: 'auto' }];
  const s0 = {
    ...base,
    funds: 1_000_000_000,
    buildings: [{ id: 500, spec: 'lei_arena', x: 4, y: 20, builtTick: 0, placedBy: 'auto' }],
    nextId: 501,
    consolidatorLog: [{
      id: 1, tick: 0, skipped: [],
      transactions: [{ sectionKey: 0, kind: 'consolidate', removed, added, buildCost: 0, scrapRecovered: 0, netCost: 0 }],
    }],
    consolidatorUndoConsumed: false,
  };
  const s1 = reducer(s0, { type: 'consolidatorUndo' });
  const stables = s1.buildings.filter((b) => b.spec === 'lei_stables');
  assert.equal(stables.length, 11, s1.placeNotice);
  assert.equal(s1.buildings.some((b) => b.spec === 'lei_arena'), false);
  assert.equal(s1.consolidatorUndoConsumed, true);
});
