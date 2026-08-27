// FEAT-1972079853: Clone-stamp tool tests
// Tests for capture, determinism, flatten, journaling, cost, validation.

import { describe, it } from 'node:test';
import { strictEqual, deepStrictEqual, ok } from 'node:assert';
import { reducer, initialState } from '../src/sim/engine.ts';
import type { Clipboard, Building } from '../src/sim/types.ts';

describe('clone-stamp', () => {
  it('captures buildings into clipboard with correct relative offsets', () => {
    const state = initialState();
    const buildings: Building[] = [
      { id: 1, spec: 'road', x: 10, y: 10 },
      { id: 2, spec: 'road', x: 12, y: 10 },
      { id: 3, spec: 'res_hut', x: 15, y: 15 },
    ];
    const testState = { ...state, buildings, nextId: 4 };

    const clipboard: Clipboard = {
      w: 3,
      h: 1,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 2, dy: 0 },
      ],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 20,
      y: 20,
    });

    // Verify the clipboard items were placed.
    ok(result.buildings.length === testState.buildings.length + 2);
    // Find the newly placed buildings.
    const newBuildings = result.buildings.filter((b) => b.id >= testState.nextId);
    strictEqual(newBuildings.length, 2);

    // Verify positions.
    const first = newBuildings.find((b) => b.x === 20 && b.y === 20);
    ok(first !== undefined);
    strictEqual(first?.spec, 'road');

    const second = newBuildings.find((b) => b.x === 22 && b.y === 20);
    ok(second !== undefined);
    strictEqual(second?.spec, 'road');
  });

  it('deterministically places buildings: same clipboard + anchor → identical result', () => {
    const state = initialState();
    const clipboard: Clipboard = {
      w: 2,
      h: 2,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 1, dy: 0 },
      ],
    };

    const result1 = reducer(state, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    const result2 = reducer(state, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    strictEqual(result1.buildings.length, result2.buildings.length);
    strictEqual(result1.nextId, result2.nextId);

    for (let i = 0; i < result1.buildings.length; i++) {
      const b1 = result1.buildings[i];
      const b2 = result2.buildings[i];
      strictEqual(b1.spec, b2.spec);
      strictEqual(b1.x, b2.x);
      strictEqual(b1.y, b2.y);
    }
  });

  it('flattens existing buildings in the landing zone', () => {
    const state = initialState();
    const testState = {
      ...state,
      buildings: [{ id: 1, spec: 'road', x: 50, y: 50 }],
      nextId: 2,
    };

    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [{ spec: 'road', dx: 0, dy: 0 }],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    strictEqual(result.buildings.length, 1);
    strictEqual(result.buildings[0].id, 2);
    strictEqual(result.buildings[0].spec, 'road');
  });

  it('respects bounds validation — rejects out-of-bounds stamps', () => {
    const state = initialState();
    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [{ spec: 'road', dx: 0, dy: 0 }],
    };

    // Should succeed (fits barely).
    const result1 = reducer(state, {
      type: 'stampRegion',
      clipboard,
      x: 439, // MAP_W - 1
      y: 259, // MAP_H - 1
    });
    ok(result1.buildings.length === state.buildings.length + 1);

    // Should fail (out of bounds).
    const result2 = reducer(state, {
      type: 'stampRegion',
      clipboard,
      x: 440, // MAP_W
      y: 50,
    });
    ok(result2.buildings.length === state.buildings.length);

    // Large clipboard extending past map.
    const largeclipboard: Clipboard = {
      w: 5,
      h: 5,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 440, dy: 0 },
      ],
    };
    const result3 = reducer(state, {
      type: 'stampRegion',
      clipboard: largeclipboard,
      x: 0,
      y: 0,
    });
    ok(result3.buildings.length === state.buildings.length);
  });

  it('charges correct cost: sum of placement costs for placed buildings', () => {
    const state = initialState();
    const testState = { ...state, funds: 10000000 };

    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 2, dy: 0 },
      ],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    // Roads cost 40 each, so total cost = 80.
    ok(result.funds === testState.funds - 80);
  });

  it('grants XP: 4 per placed item', () => {
    const state = initialState();
    const initialXp = state.xp;

    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 1, dy: 0 },
        { spec: 'road', dx: 2, dy: 0 },
      ],
    };

    const result = reducer(state, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    strictEqual(result.xp, initialXp + 12);
  });

  it('journal records stampRegion as state-affecting', async () => {
    const { isStateAffecting } = await import('../src/sim/journal.ts');

    const action = {
      type: 'stampRegion',
      clipboard: { w: 1, h: 1, items: [{ spec: 'road', dx: 0, dy: 0 }] },
      x: 50,
      y: 50,
    };

    ok(isStateAffecting(action as any) === true);
  });

  it('refunds partial cost for demolished buildings', () => {
    const state = initialState();
    // Use station_sanderling (network category, costs 0 placement but is a service).
    // Actually use m20 motorway which is network, costs 0 placement.
    // Use a road (network, costs 40).
    const testState = {
      ...state,
      funds: 50000,
      buildings: [{ id: 1, spec: 'road', x: 50, y: 50 }],
      nextId: 2,
    };

    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [{ spec: 'road', dx: 0, dy: 0 }],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    // Road to demolish costs 40, so 25% refund = 10.
    // New road costs 40. Net cost = 40 - 10 = 30.
    ok(result.funds === testState.funds - 30);
  });

  it('setClipboard action stores clipboard without affecting game state', () => {
    const state = initialState();
    const initialFunds = state.funds;

    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [{ spec: 'road', dx: 0, dy: 0 }],
    };

    const result = reducer(state, {
      type: 'setClipboard',
      clipboard,
    });

    strictEqual(result.funds, initialFunds);
    strictEqual(result.xp, state.xp);
    deepStrictEqual(result.clipboard, clipboard);
  });

  it('cannot stamp without funds to cover placement cost', () => {
    const state = initialState();
    const testState = { ...state, funds: 1 };

    // pow_wind (service, costs 1400) — with funds=1 we cannot place it.
    const clipboard: Clipboard = {
      w: 1,
      h: 1,
      items: [{ spec: 'pow_wind', dx: 0, dy: 0 }],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 100,
      y: 100,
    });

    // Should be rejected due to insufficient funds.
    ok(result.buildings.length === testState.buildings.length);
  });

  it('handles multi-building flatten with overlapping footprints', () => {
    const state = initialState();
    const testState = {
      ...state,
      buildings: [{ id: 1, spec: 'res_block', x: 50, y: 50 }],
      nextId: 2,
    };

    const clipboard: Clipboard = {
      w: 3,
      h: 3,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 2, dy: 2 },
      ],
    };

    const result = reducer(testState, {
      type: 'stampRegion',
      clipboard,
      x: 50,
      y: 50,
    });

    ok(!result.buildings.find((b) => b.id === 1));
    ok(result.buildings.length === testState.buildings.length + 1);
  });

  it('determinism: clipboard ordering does not affect final state', () => {
    const state = initialState();
    const clipboard1: Clipboard = {
      w: 2,
      h: 1,
      items: [
        { spec: 'road', dx: 0, dy: 0 },
        { spec: 'road', dx: 1, dy: 0 },
      ],
    };
    const clipboard2: Clipboard = {
      w: 2,
      h: 1,
      items: [
        { spec: 'road', dx: 1, dy: 0 },
        { spec: 'road', dx: 0, dy: 0 },
      ],
    };

    const result1 = reducer(state, {
      type: 'stampRegion',
      clipboard: clipboard1,
      x: 50,
      y: 50,
    });

    const result2 = reducer(state, {
      type: 'stampRegion',
      clipboard: clipboard2,
      x: 50,
      y: 50,
    });

    const b1Specs = result1.buildings
      .filter((b) => b.x >= 50 && b.x <= 51 && b.y === 50)
      .sort((a, b) => a.x - b.x)
      .map((b) => b.spec);
    const b2Specs = result2.buildings
      .filter((b) => b.x >= 50 && b.x <= 51 && b.y === 50)
      .sort((a, b) => a.x - b.x)
      .map((b) => b.spec);

    deepStrictEqual(b1Specs, b2Specs);
  });
});
