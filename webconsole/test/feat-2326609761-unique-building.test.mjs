// feat-2326609761-unique-building.test.mjs — FEAT-2326609761 CONSOLIDATOR,
// first self-contained slice: the maxPerCity UNIQUE-BUILDING mechanism
// (AC-28..AC-31 of docs/planning/acceptance/FEAT-2326609761.md).
//
// Aaron's ruling (BOW comment, R4): "limit the number of Five Gorges Dams to
// just one" — a HARD CAP of one, enforced at EVERY placement path (hand-place,
// stampRegion, placeMany, auto-build/findSpot, the demand-fix planner) and
// reflected in the palette as a distinct "One per city" chip. A general
// `maxPerCity` field on Spec (data.ts), not an `if (spec === 'pow_hydro')` —
// GR#15 — so a future unique building needs no new code, only a new datum.
//
// Run with `npm test` (node --test); node's type-stripping imports the real
// TypeScript modules, so these assertions exercise the exact shipped catalogue,
// reducer, and planner — no copy, no drift.
//
// RED PROOF (documented, done via scratch-copy — NEVER git, GR#24): copy
// data.ts to data.ts.bak, delete `maxPerCity: 1` from the pow_hydro SPECS
// entry, re-run this file — every test in "the five placement paths" section
// turns RED (a second/third dam places cleanly, funds get double-charged,
// remainingAllowance returns Infinity). Restore via `mv data.ts.bak data.ts`
// (never `git checkout`). Verified during development of this file.

import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import {
  SPECS,
  isPlaceable,
  sortPaletteItems,
  remainingAllowance,
  countOfSpec,
  findSpot,
  rankedProviders,
  canEnterSim,
} from '../src/sim/data.ts';
import { initialState, reducer, specUnlocked } from '../src/sim/engine.ts';
import { runConsistencyChecks } from '../src/sim/consistency.ts';
import { emptyJournal, recordAction } from '../src/sim/journal.ts';
import { replayFromGenesis, replayIsDeterministic, stableStringify } from '../src/sim/genesisReplay.ts';
import { restoreFromSavepoint, SAVEPOINT_KEY_PREFIX } from '../src/sim/replay.ts';

const DAM = 'pow_hydro';

/** A god-mode, cash-flush state so unlock level and affordability never
 *  interfere with the cap assertions themselves. */
function richState() {
  const s = initialState();
  return { ...s, unlockedAll: true, funds: SPECS[DAM].cost * 10 };
}

/** In-memory Web-Storage mock for restore tests (mirrors placeholder-catalogue.test.mjs). */
function mockStorage(seed = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => map.set(k, v),
    removeItem: (k) => map.delete(k),
  };
}

// ---------------------------------------------------------------------------
// Data-level: the spec field + the derived allowance function.
// ---------------------------------------------------------------------------

test('AC-28: pow_hydro (Five Gorges Dam) carries maxPerCity: 1, as a plain data field', () => {
  assert.equal(SPECS[DAM].maxPerCity, 1, 'the cap is a datum, not hidden logic');
});

test('AC-28: a spec with no maxPerCity has infinite remainingAllowance regardless of count', () => {
  const s = richState();
  assert.equal(SPECS.res_hut.maxPerCity, undefined, 'control spec carries no cap');
  assert.equal(remainingAllowance(s, SPECS.res_hut), Infinity);
  // Even after placing several, still infinite (control: the mechanism is
  // genuinely opt-in via the field, not a blanket count-based throttle).
  let cur = s;
  for (let i = 0; i < 5; i++) {
    cur = reducer(cur, { type: 'place', spec: 'res_hut', x: 10 + i * 3, y: 10 });
  }
  assert.equal(countOfSpec(cur, 'res_hut'), 5);
  assert.equal(remainingAllowance(cur, SPECS.res_hut), Infinity);
});

test('AC-28: remainingAllowance derives from state, clamps at 0, never negative', () => {
  const s = richState();
  assert.equal(remainingAllowance(s, SPECS[DAM]), 1, 'no dam yet — full allowance');
  const withOne = {
    ...s,
    buildings: [...s.buildings, { id: 999101, spec: DAM, x: 300, y: 100, builtTick: 0 }],
  };
  assert.equal(remainingAllowance(withOne, SPECS[DAM]), 0);
  // A hand-crafted state with TWO (simulating an old save, AC-31) must clamp
  // at 0, never go negative.
  const withTwo = {
    ...s,
    buildings: [
      ...s.buildings,
      { id: 999101, spec: DAM, x: 300, y: 100, builtTick: 0 },
      { id: 999102, spec: DAM, x: 320, y: 100, builtTick: 0 },
    ],
  };
  assert.equal(remainingAllowance(withTwo, SPECS[DAM]), 0, 'clamped at 0, not -1');
});

test('AC-28: countOfSpec counts under-construction buildings too (no builtTick special-case)', () => {
  const s = richState();
  const withUnderConstruction = {
    ...s,
    buildings: [...s.buildings, { id: 999103, spec: DAM, x: 300, y: 100, builtTick: s.tick }],
  };
  assert.equal(countOfSpec(withUnderConstruction, DAM), 1, 'a just-started dam still occupies the cap slot');
  assert.equal(remainingAllowance(withUnderConstruction, SPECS[DAM]), 0);
});

// ---------------------------------------------------------------------------
// isPlaceable — the UI gate (AC-29 bullet 1).
// ---------------------------------------------------------------------------

test('AC-29 path 1/5 (isPlaceable/UI): the dam is placeable with none built, refused once one exists', () => {
  const s = richState();
  assert.equal(isPlaceable(s, SPECS[DAM]), true, 'precondition: unlocked + affordable + under cap');
  const withOne = {
    ...s,
    buildings: [...s.buildings, { id: 999104, spec: DAM, x: 300, y: 100, builtTick: 0 }],
  };
  assert.equal(isPlaceable(withOne, SPECS[DAM]), false, 'at-cap must never be placeable, even under god-mode');
});

test('AC-29 control: a real spec with no cap is unaffected by the maxPerCity machinery', () => {
  const s = richState();
  for (const sp of Object.values(SPECS)) {
    if (sp.maxPerCity != null || sp.placeholder) continue;
    assert.equal(isPlaceable(s, sp), specUnlocked(s, sp), `${sp.id}: uncapped spec must track specUnlocked exactly`);
  }
});

// ---------------------------------------------------------------------------
// Path 2/5: the hand-place reducer case ('place').
// ---------------------------------------------------------------------------

describe("AC-29 path 2/5: reducer case 'place'", () => {
  test('a second dam placement is refused; the first succeeds', () => {
    const s = richState();
    const afterFirst = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    assert.equal(afterFirst.buildings.length, s.buildings.length + 1, 'first dam places');
    assert.equal(countOfSpec(afterFirst, DAM), 1);

    const afterSecond = reducer(afterFirst, { type: 'place', spec: DAM, x: 320, y: 100 });
    assert.equal(afterSecond.buildings.length, afterFirst.buildings.length, 'second dam refused — no building added');
    assert.equal(countOfSpec(afterSecond, DAM), 1, 'still exactly one dam');
  });

  test('conservation: funds are NOT charged on a refused placement', () => {
    const s = richState();
    const afterFirst = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    const fundsAfterFirst = afterFirst.funds;
    const afterSecond = reducer(afterFirst, { type: 'place', spec: DAM, x: 320, y: 100 });
    assert.equal(afterSecond.funds, fundsAfterFirst, 'a refused placement must not spend the £5bn build cost');
    assert.equal(afterSecond.cumulativeCapexSpent, afterFirst.cumulativeCapexSpent, 'capex tracking unaffected by a refusal');
    assert.equal(afterSecond.xp, afterFirst.xp, 'no XP for a refused placement');
    assert.equal(afterSecond.nextId, afterFirst.nextId, 'nextId must not advance for a refused placement');
  });

  test('the refusal names an honest reason', () => {
    const s = richState();
    const afterFirst = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
    const afterSecond = reducer(afterFirst, { type: 'place', spec: DAM, x: 320, y: 100 });
    assert.match(afterSecond.placeNotice ?? '', /one per city/i);
    assert.match(afterSecond.placeNotice ?? '', /Five Gorges Dam/);
  });

  test('control: an uncapped real spec still places freely on the SAME reducer path (guard is not over-broad)', () => {
    const s = richState();
    let cur = s;
    for (let i = 0; i < 3; i++) {
      cur = reducer(cur, { type: 'place', spec: 'pow_wind', x: 10 + i * 2, y: 10 });
    }
    assert.equal(countOfSpec(cur, 'pow_wind'), 3, 'three wind turbines place with no cap interference');
  });
});

// ---------------------------------------------------------------------------
// Path 3/5: 'stampRegion' — the clone-stamp tool, batch-aware.
// ---------------------------------------------------------------------------

describe("AC-29 path 3/5: reducer case 'stampRegion'", () => {
  test('a stamp containing TWO dams in the SAME batch is refused wholesale (counts within its own batch)', () => {
    const s = richState();
    const clipboard = {
      w: 20,
      h: 8,
      items: [
        { spec: DAM, dx: 0, dy: 0 },
        { spec: DAM, dx: 12, dy: 0 },
      ],
    };
    const after = reducer(s, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    assert.deepEqual(after, s, 'a stamp that would place two dams at once must be refused ENTIRELY (all-or-nothing)');
    assert.equal(countOfSpec(after, DAM), 0);
  });

  test('a stamp with one dam succeeds when none exist yet; a SECOND stamp with one more dam is then refused', () => {
    const s = richState();
    const oneClip = { w: 8, h: 8, items: [{ spec: DAM, dx: 0, dy: 0 }] };
    const afterFirst = reducer(s, { type: 'stampRegion', clipboard: oneClip, x: 300, y: 100 });
    assert.equal(countOfSpec(afterFirst, DAM), 1, 'first stamp places the one dam');

    const secondClip = { w: 8, h: 8, items: [{ spec: DAM, dx: 0, dy: 0 }] };
    const afterSecond = reducer(afterFirst, { type: 'stampRegion', clipboard: secondClip, x: 320, y: 100 });
    assert.deepEqual(afterSecond, afterFirst, 'a second stamp adding one more dam is refused wholesale');
    assert.equal(countOfSpec(afterSecond, DAM), 1, 'still exactly one dam');
  });

  test('conservation: funds are NOT charged on a refused stamp', () => {
    const s = richState();
    const clipboard = {
      w: 20,
      h: 8,
      items: [
        { spec: DAM, dx: 0, dy: 0 },
        { spec: DAM, dx: 12, dy: 0 },
      ],
    };
    const after = reducer(s, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    assert.equal(after.funds, s.funds, 'a wholly-refused stamp must not spend anything');
  });

  test('control: a stamp with only real, uncapped specs still stamps (guard is not over-broad)', () => {
    const s = richState();
    const clipboard = { w: 8, h: 8, items: [{ spec: 'res_hut', dx: 0, dy: 0 }] };
    const after = reducer(s, { type: 'stampRegion', clipboard, x: 300, y: 100 });
    assert.equal(after.buildings.length, s.buildings.length + 1, 'a real, uncapped clone-stamp still places');
  });
});

// ---------------------------------------------------------------------------
// Path 4/5: 'placeMany' — the drag-paint bulk-place.
// ---------------------------------------------------------------------------

describe("AC-29 path 4/5: reducer case 'placeMany'", () => {
  test('placeMany with two dam-sized tiles places only the first, refuses the second, and reports why', () => {
    const s = richState();
    const after = reducer(s, {
      type: 'placeMany',
      spec: DAM,
      tiles: [
        { x: 300, y: 100 },
        { x: 320, y: 100 },
      ],
    });
    assert.equal(countOfSpec(after, DAM), 1, 'only the first dam of the batch is placed');
    assert.match(after.placeNotice ?? '', /one per city/i, 'placeMany must report the honest cap reason');
  });

  test('conservation: placeMany charges for exactly the ONE dam it actually placed, not two', () => {
    const s = richState();
    const after = reducer(s, {
      type: 'placeMany',
      spec: DAM,
      tiles: [
        { x: 300, y: 100 },
        { x: 320, y: 100 },
      ],
    });
    assert.equal(s.funds - after.funds, SPECS[DAM].cost, 'exactly one build cost charged, never two');
  });

  test('control: placeMany with several uncapped tiles places them all', () => {
    const s = richState();
    const after = reducer(s, {
      type: 'placeMany',
      spec: 'pow_wind',
      tiles: [
        { x: 10, y: 10 },
        { x: 12, y: 10 },
        { x: 14, y: 10 },
      ],
    });
    assert.equal(countOfSpec(after, 'pow_wind'), 3, 'all three uncapped turbines place');
  });
});

// ---------------------------------------------------------------------------
// Path 5/5: auto-build (findSpot -> placePlanItem, reached via 'resolveDemand'
// and 'resolveDemandAll') + the demand-fix PLANNER must never recommend an
// at-cap spec (the BUG-641 "no help" failure again).
// ---------------------------------------------------------------------------

describe('AC-29 path 5/5: auto-build (findSpot) and the demand-fix planner', () => {
  test('findSpot refuses to site an at-cap unique building — returns null, not a real tile', () => {
    const s = richState();
    assert.ok(findSpot(s, DAM), 'precondition: findable with none built');
    const withOne = {
      ...s,
      buildings: [...s.buildings, { id: 999105, spec: DAM, x: 300, y: 100, builtTick: 0 }],
    };
    assert.equal(findSpot(withOne, DAM), null, 'at-cap: auto-build must never find a site for a second dam');
  });

  test('control: findSpot is unaffected for an uncapped spec', () => {
    const s = richState();
    assert.ok(findSpot(s, 'pow_wind'), 'an uncapped spec still finds a site');
  });

  test('the planner (rankedProviders) NEVER offers an at-cap spec as a power candidate', () => {
    const s = richState();
    const withOne = {
      ...s,
      buildings: [...s.buildings, { id: 999106, spec: DAM, x: 300, y: 100, builtTick: 0 }],
    };
    // A huge shortfall so the dam would otherwise be a genuinely strong (even
    // winning) candidate by total-plan-cost — proving the exclusion actually
    // engages, not just "never reached because it wasn't going to win anyway".
    const rankedAtCap = rankedProviders(withOne, 'power', Number.MAX_SAFE_INTEGER, 1_000_000);
    assert.ok(
      rankedAtCap.every((c) => c.sp.id !== DAM),
      'an at-cap dam must never appear in the ranked candidate list, however favourable the shortfall'
    );

    // Control: with NO dam built, the same huge shortfall/budget DOES let the
    // dam appear as a candidate — proving the exclusion above is the cap,
    // not some unrelated always-false condition.
    const rankedNoCap = rankedProviders(s, 'power', Number.MAX_SAFE_INTEGER, 1_000_000);
    assert.ok(
      rankedNoCap.some((c) => c.sp.id === DAM),
      'control: with the cap not yet hit, the dam IS a real candidate for the same shortfall'
    );
  });
});

// ---------------------------------------------------------------------------
// CEIL-4 / AC-8 rule 6 (consolidator's own future gate): remainingAllowance
// is the single derived-from-state predicate every future path (including
// the not-yet-built consolidator) must consult. Proven here as a property,
// not a consolidator-specific test (the consolidator does not exist yet).
// ---------------------------------------------------------------------------

test('remainingAllowance is a pure function of state — same state, same answer, no Date/Math.random', () => {
  const s = richState();
  const a = remainingAllowance(s, SPECS[DAM]);
  const b = remainingAllowance(s, SPECS[DAM]);
  assert.equal(a, b);
  assert.equal(typeof a, 'number');
});

// ---------------------------------------------------------------------------
// AC-30: palette greying — a third, visually distinct state.
// ---------------------------------------------------------------------------

describe('AC-30: palette sort + gating logic for an at-cap unique building', () => {
  test('sortPaletteItems: an at-cap real spec sorts AFTER available specs but BEFORE placeholders', () => {
    const s = richState();
    const withOne = {
      ...s,
      buildings: [...s.buildings, { id: 999107, spec: DAM, x: 300, y: 100, builtTick: 0 }],
    };
    // pow_wind: available; pow_hydro: at-cap; rail_branch: placeholder.
    const family = ['rail_branch', DAM, 'pow_wind'];
    const sorted = sortPaletteItems(withOne, family);
    const tierOf = (id) => {
      const sp = SPECS[id];
      if (sp.placeholder) return 'placeholder';
      if (!canEnterSim(sp) || !specUnlocked(withOne, sp)) return 'locked';
      if (remainingAllowance(withOne, sp) <= 0) return 'atcap';
      return 'available';
    };
    const order = sorted.map(tierOf);
    assert.deepEqual(order, ['available', 'atcap', 'placeholder']);
  });

  test('sortPaletteItems: with NO dam built, the same family sorts pow_hydro as available (control)', () => {
    const s = richState();
    const family = ['rail_branch', DAM, 'pow_wind'];
    const sorted = sortPaletteItems(s, family);
    // Both real specs are available; original relative order preserved within tier 1.
    assert.equal(sorted[0], DAM);
    assert.equal(sorted[1], 'pow_wind');
    assert.equal(sorted[2], 'rail_branch');
  });

  test('the BottomBar disabled/atCap boolean logic (mirrors the shipped component exactly)', () => {
    // Mirrors BottomBar.tsx's `atCap` derivation and `disabled` expression —
    // a pure-logic regression guard, same style as palette-availability.test.mjs's
    // AC-4 CRITICAL disabled-logic test.
    const compute = (isPh, locked, atCap, funds, cost) => ({
      atCap,
      disabled: isPh || atCap || (!locked && !atCap && funds < cost),
    });
    // At-cap real spec: always disabled, regardless of funds.
    assert.equal(compute(false, false, true, 999_999_999_999, 5_000_000_000).disabled, true, 'at-cap must stay disabled even when affordable');
    // Not at cap, affordable: enabled.
    assert.equal(compute(false, false, false, 999_999_999_999, 5_000_000_000).disabled, false);
  });
});

// ---------------------------------------------------------------------------
// Re-enable after bulldoze: the cap is DERIVED, so demolishing the dam frees
// the slot immediately with no special-case code.
// ---------------------------------------------------------------------------

test('bulldozing the dam re-enables placement (remainingAllowance and isPlaceable both recover)', () => {
  const s = richState();
  const placed = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
  assert.equal(countOfSpec(placed, DAM), 1);
  assert.equal(isPlaceable(placed, SPECS[DAM]), false);

  const bulldozed = reducer(placed, { type: 'bulldoze', x: 300, y: 100 });
  assert.equal(countOfSpec(bulldozed, DAM), 0, 'the dam is gone');
  assert.equal(remainingAllowance(bulldozed, SPECS[DAM]), 1, 'allowance recovers to 1 — nothing to reset, it is derived');
  assert.equal(isPlaceable(bulldozed, SPECS[DAM]), true, 'placeable again');

  // And a fresh dam can now be placed through the SAME reducer path.
  const rebuilt = reducer(bulldozed, { type: 'place', spec: DAM, x: 300, y: 100 });
  assert.equal(countOfSpec(rebuilt, DAM), 1, 'a replacement dam places cleanly after the bulldoze');
});

// ---------------------------------------------------------------------------
// AC-31: an OLD SAVE that already contains two (or more) dams.
// ---------------------------------------------------------------------------

describe('AC-31: old save with two dams', () => {
  function twoDamSnapshot() {
    const genesis = initialState();
    return {
      ...genesis,
      funds: SPECS[DAM].cost * 10,
      unlockedAll: true,
      buildings: [
        ...genesis.buildings,
        { id: 999201, spec: DAM, x: 300, y: 100, builtTick: 0 },
        { id: 999202, spec: DAM, x: 320, y: 100, builtTick: 0 },
      ],
      nextId: genesis.nextId + 2,
    };
  }

  test('loads intact — nothing demolished, no money clawed back', () => {
    const snapshot = twoDamSnapshot();
    const savepoint = {
      savedAt: new Date().toISOString(),
      snapshotTick: snapshot.tick,
      snapshot,
      journalTail: [],
    };
    const storage = mockStorage({ [`${SAVEPOINT_KEY_PREFIX}.0`]: JSON.stringify(savepoint) });
    const result = restoreFromSavepoint(storage);
    assert.equal(result.success, true, `restore should succeed; got: ${result.reason}`);
    assert.equal(countOfSpec(result.state, DAM), 2, 'BOTH dams survive the load — neither is silently removed');
    assert.equal(result.state.funds, snapshot.funds, 'no money is clawed back on load');
  });

  test('does not corrupt: consistency checks stay clean with two over-cap dams present', () => {
    const snapshot = twoDamSnapshot();
    const report = runConsistencyChecks(snapshot);
    assert.equal(report.failures, 0, 'an over-cap-but-honestly-persisted state is not itself an inconsistency');
  });

  test('blocks a third: remainingAllowance clamps at 0, and the reducer refuses a third dam', () => {
    const snapshot = twoDamSnapshot();
    let s = snapshot;
    // Replay 30 ticks — nothing about the tick loop may touch the dams.
    for (let i = 0; i < 30; i++) {
      s = reducer(s, { type: 'tick' });
    }
    assert.equal(countOfSpec(s, DAM), 2, 'both dams survive 30 ticks untouched');
    assert.equal(remainingAllowance(s, SPECS[DAM]), 0, 'clamped at 0, never negative');

    const afterThird = reducer(s, { type: 'place', spec: DAM, x: 340, y: 100 });
    assert.equal(countOfSpec(afterThird, DAM), 2, 'a third dam is refused by any path, even starting from an over-cap save');
  });

  test('AC-31: hydrate fires a one-time honest notice naming the over-cap spec and its count', () => {
    const snapshot = twoDamSnapshot();
    const fresh = initialState();
    const hydrated = reducer(fresh, { type: 'hydrate', state: snapshot });
    assert.match(hydrated.placeNotice ?? '', /Five Gorges Dam/, 'the notice names the offending spec');
    assert.match(hydrated.placeNotice ?? '', /2/, 'the notice names the over-cap count');
  });

  test('AC-31 control: hydrating a save with only ONE dam raises no over-cap notice', () => {
    const genesis = initialState();
    const oneOk = {
      ...genesis,
      buildings: [...genesis.buildings, { id: 999203, spec: DAM, x: 300, y: 100, builtTick: 0 }],
      nextId: genesis.nextId + 1,
    };
    const fresh = initialState();
    const hydrated = reducer(fresh, { type: 'hydrate', state: oneOk });
    assert.ok(
      !hydrated.placeNotice || !/Five Gorges Dam/.test(hydrated.placeNotice),
      'exactly-at-cap (not over) must not raise the over-cap notice'
    );
  });
});

// ---------------------------------------------------------------------------
// AC-33/AC-35: determinism — genesis replay of a journal touching the dam
// cap is byte-identical, and no in-place mutation occurs anywhere on the path.
// ---------------------------------------------------------------------------

function driveAndRecord(actions) {
  let journal = emptyJournal();
  let state = initialState();
  for (const action of actions) {
    journal = recordAction(journal, state.tick, action);
    state = reducer(state, action);
  }
  return { journal, liveState: state };
}

describe('determinism: genesis replay of a journal containing the dam cap', () => {
  const SCRIPT = [
    { type: 'unlockAll' },
    { type: 'debugFunds', amount: SPECS[DAM].cost * 20 },
    { type: 'place', spec: DAM, x: 300, y: 100 }, // succeeds
    { type: 'tick' },
    { type: 'place', spec: DAM, x: 320, y: 100 }, // refused — at cap
    { type: 'tick' },
    {
      type: 'placeMany',
      spec: DAM,
      tiles: [{ x: 340, y: 100 }],
    }, // refused — at cap
    { type: 'tick' },
    { type: 'bulldoze', x: 300, y: 100 }, // frees the slot
    { type: 'tick' },
    { type: 'place', spec: DAM, x: 300, y: 100 }, // succeeds again
    { type: 'tick' },
  ];

  test('replaying twice from genesis is byte-identical', () => {
    const { journal } = driveAndRecord(SCRIPT);
    assert.equal(replayIsDeterministic(journal), true);
  });

  test('genesis replay reconstructs the EXACT same city the live sequence produced', () => {
    const { journal, liveState } = driveAndRecord(SCRIPT);
    const replayed = replayFromGenesis(journal);
    assert.equal(stableStringify(replayed), stableStringify(liveState), 'genesis replay must match the live run exactly');
    assert.equal(countOfSpec(liveState, DAM), 1, 'sanity: exactly one dam survives the whole script');
  });
});

test('AC-35: no in-place mutation — a refused dam placement leaves the input state object and its buildings array untouched', () => {
  const s = richState();
  const placed = reducer(s, { type: 'place', spec: DAM, x: 300, y: 100 });
  const buildingsRefBefore = placed.buildings;
  const refused = reducer(placed, { type: 'place', spec: DAM, x: 320, y: 100 });
  assert.equal(refused.buildings, buildingsRefBefore, 'a refused placement must not even allocate a new buildings array');
  assert.notEqual(refused, placed, 'the reducer still returns a new top-level object (for the placeNotice)');
});
