// construction-queue.test.tsx — BUG-605: "I still don't see the queue" (Aaron).
//
// Proves the Construction Queue panel is a pure, unconditionally-visible
// derivation of existing SimState — no new state, no re-derived build-time
// formula (it must go through data.ts's own constructionTicks/
// computeFailedGates, the same SSOT the map's WHY tooltip already uses).
//
// Fixtures are built via initialState() + the REAL reducer (unlockAll /
// debugFunds / place / tick), never by hand-poking `buildings[]` — so these
// tests exercise the exact code path a player triggers in the game.
//
// RED PROOF (documented, not left in the tree — GR#21 "verification
// standards"): `cp ConstructionQueue.tsx ConstructionQueue.tsx.bak`, then
// dropped the `|| a.id - b.id` tie-break from rows.sort() (comparator became
// `(a, b) => a.ticksRemaining - b.ticksRemaining`). The naive tie-break test
// (asserting sorted-ascending id order straight off `s.buildings`) stayed
// GREEN even sabotaged — V8's stable sort happened to preserve insertion
// order, which already was id-ascending. Rewriting the test to feed
// constructionQueueOf() the SAME buildings in REVERSE array order (a case a
// stable no-tie-break sort gets wrong) made it fail correctly:
// actual [1857,1856] vs expected [1856,1857]. Restored the fix via
// `mv ConstructionQueue.tsx.bak ConstructionQueue.tsx` (never git) and
// re-ran green. GR#24 bans tree-reverting git ops for exactly this cycle.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { SPECS, constructionTicks, computeFailedGates, placementCost } from '../src/sim/data.ts';
import { constructionQueueOf } from '../src/components/left/ConstructionQueue.tsx';
import type { SimState } from '../src/sim/types.ts';

function ensureMountWindow() {
  if (typeof globalThis.window === 'undefined') {
    globalThis.window = {
      localStorage: {
        getItem: () => null,
        setItem: () => {},
        removeItem: () => {},
        clear: () => {},
        key: () => null,
        length: 0,
      },
      performance: { now: () => 0 },
    } as any;
  }
}

// A funded, all-unlocked, clean-board starting state built through the REAL
// reducer (debugFunds + unlockAll), never hand-assembled.
function freshFundedState(): SimState {
  let s = initialState();
  s = reducer(s, { type: 'debugFunds', amount: 500_000_000 });
  s = reducer(s, { type: 'unlockAll' });
  return { ...s, buildings: [] as SimState['buildings'], tick: 0 };
}

// ===================== (1) empty queue =====================

test('BUG-605: constructionQueueOf returns [] on a state with no buildings', () => {
  const s = freshFundedState();
  assert.deepEqual(constructionQueueOf(s), []);
});

// ===================== (2) one building under construction =====================

test('BUG-605: constructionQueueOf includes a freshly-placed building, ticksRemaining matches the SSOT helper (not a copied formula)', () => {
  let s = freshFundedState();
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 10, y: 10 });
  const found = s.buildings.find((x) => x.spec === 'res_hut');
  assert.ok(found, 'placement must have landed a res_hut building');
  const b = found!;

  const rows = constructionQueueOf(s);
  assert.equal(rows.length, 1);
  const row = rows[0];
  assert.equal(row.id, b.id);
  assert.equal(row.name, SPECS['res_hut'].name);
  assert.equal(row.x, b.x);
  assert.equal(row.y, b.y);
  assert.equal(row.cost, placementCost(SPECS['res_hut']));

  // Independently derive expected remaining ticks via the SAME SSOT helpers
  // ConstructionQueue.tsx uses internally (computeFailedGates + constructionTicks),
  // never a hand-copied arithmetic formula.
  const gates = computeFailedGates(s, b);
  assert.equal(gates[0].gate, 'construction');
  const expectedRemaining = constructionTicks(SPECS['res_hut']) - (s.tick - (b.builtTick as number));
  assert.equal(row.ticksRemaining, expectedRemaining);
});

// ===================== (3) a building that has FINISHED is excluded =====================

test('BUG-605: an online (finished) building never appears in the queue', () => {
  let s = freshFundedState();
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 20, y: 20 });
  const found = s.buildings.find((x) => x.spec === 'res_hut');
  assert.ok(found, 'placement must have landed a res_hut building');
  const b = found!;
  const ticks = constructionTicks(SPECS['res_hut']);
  // Advance well past the building's own construction time.
  for (let i = 0; i < ticks + 5; i++) s = reducer(s, { type: 'tick' });

  const stillThere = s.buildings.find((x) => x.id === b.id);
  assert.ok(stillThere, 'building must not have been removed by advance()');
  const rows = constructionQueueOf(s);
  assert.ok(!rows.some((r) => r.id === b.id), 'a finished building must not be in the queue');
});

// ===================== (4) sort order: ticks-remaining ascending =====================

test('BUG-605: many buildings — queue sorts ticks-remaining ascending', () => {
  let s = freshFundedState();
  // Place the first hut, advance a couple of ticks, then place a second hut —
  // the first now has FEWER ticks remaining than the second (it started earlier).
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 10, y: 10 });
  const foundFirst = s.buildings.find((x) => x.spec === 'res_hut');
  assert.ok(foundFirst, 'first res_hut must have been placed');
  const first = foundFirst!;
  s = reducer(s, { type: 'tick' });
  s = reducer(s, { type: 'tick' });
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 30, y: 30 });
  const foundSecond = s.buildings.find((x) => x.id !== first.id && x.spec === 'res_hut');
  assert.ok(foundSecond, 'second res_hut must have been placed');
  const second = foundSecond!;

  const rows = constructionQueueOf(s);
  const rowIds = rows.map((r) => r.id);
  const iFirst = rowIds.indexOf(first.id);
  const iSecond = rowIds.indexOf(second.id);
  assert.ok(iFirst >= 0 && iSecond >= 0, 'both buildings must still be queued');
  assert.ok(iFirst < iSecond, 'the earlier-started building (fewer ticks remaining) must sort first');
  for (let i = 1; i < rows.length; i++) {
    assert.ok(rows[i - 1].ticksRemaining <= rows[i].ticksRemaining, 'queue must be non-decreasing in ticksRemaining');
  }
});

// ===================== (5) deterministic tie-break by id =====================

test('BUG-605: equal ticksRemaining ties break by building id ascending (GR#21 determinism)', () => {
  let s = freshFundedState();
  // Two huts placed on the SAME tick have identical ticksRemaining; tie-break
  // must be id ascending, and placement always assigns ids in increasing order.
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 10, y: 10 });
  s = reducer(s, { type: 'place', spec: 'res_hut', x: 40, y: 40 });
  const huts = s.buildings.filter((b) => b.spec === 'res_hut').sort((a, b) => a.id - b.id);
  assert.equal(huts.length, 2);
  assert.ok(huts[0].id < huts[1].id);

  // Feed constructionQueueOf the buildings in REVERSE insertion order — a
  // stable sort with the tie-break DROPPED would preserve this (wrong) order
  // for the equal-ticksRemaining pair, so this is the case that actually
  // distinguishes "sorts by id" from "just happens to be stable".
  s = { ...s, buildings: [...s.buildings].reverse() };

  const rows = constructionQueueOf(s);
  const rIds = rows.filter((r) => r.id === huts[0].id || r.id === huts[1].id).map((r) => r.id);
  assert.deepEqual(rIds, [huts[0].id, huts[1].id], 'equal-ticksRemaining rows must tie-break by id ascending');
});

// ===================== (6) component render: empty state =====================

test('BUG-605: ConstructionQueueTab renders the empty state unconditionally (no dev flag)', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { ConstructionQueueTab } = await import('../src/components/left/ConstructionQueue.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(ConstructionQueueTab) }),
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(html.includes('Nothing under construction.'), 'panel must show the explicit empty state');
  assert.ok(html.includes('Construction Queue'), 'panel heading must always render');
});

// ===================== (7) component render: with rows =====================

test('BUG-605: ConstructionQueueTab renders building name, location, ticks-left and cost', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { ConstructionQueueTab } = await import('../src/components/left/ConstructionQueue.tsx');
  const { reducer: coreReducer, initialState: coreInitial } = await import('../src/sim/engine.ts');

  let s = coreInitial();
  s = coreReducer(s, { type: 'debugFunds', amount: 500_000_000 });
  s = coreReducer(s, { type: 'unlockAll' });
  s = { ...s, buildings: [], tick: 0 } as typeof s;
  // Use a PAID building so the cost column is provably non-zero.
  s = coreReducer(s, { type: 'place', spec: 'edu_nursery', x: 15, y: 15 });
  const foundEdu = s.buildings.find((x) => x.spec === 'edu_nursery');
  assert.ok(foundEdu, 'edu_nursery placement must have landed');
  const b = foundEdu!;

  // Render the component against THIS EXACT state via a minimal stub context
  // value (SimProvider itself only ever boots a fresh/persisted city, so a
  // direct SimContext.Provider is the only way to render a specific fixture
  // state — this is read-only rendering, dispatch is never invoked).
  const stubValue = {
    state: s,
    dispatch: () => {},
    cityName: 'Test City',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => false,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
  };
  const html = renderToString(
    React.default.createElement(SimContext.Provider, {
      value: stubValue,
      children: React.default.createElement(ConstructionQueueTab),
    }),
  );
  assert.ok(!html.includes('useSim must be used inside SimProvider'));
  assert.ok(html.includes(SPECS['edu_nursery'].name), 'building name must render');
  // React SSR interleaves HTML comment markers between adjacent expression
  // text nodes, so match tolerant of those rather than a literal "(15, 15)".
  assert.ok(/\(<!--\s*-->15<!--\s*-->, <!--\s*-->15<!--\s*-->\)/.test(html), 'location must render');
  const expectedTicks = constructionTicks(SPECS['edu_nursery']) - (s.tick - (b.builtTick as number));
  assert.ok(html.includes(`>${expectedTicks}<`), 'ticks-remaining must render, derived from the SSOT helper');
  const rows = constructionQueueOf(s);
  assert.equal(rows.length, 1);
  assert.ok(rows[0].cost > 0, 'a paid building must show a non-zero cost');
  assert.ok(html.includes('£'), 'cost must render with the £ formatter');
});

// ===================== (8) badge count (LeftDock "Queue (N)" label) =====================

test('BUG-605: LeftDock declares a "queue" child tab under Build & Zoning, mounted to ConstructionQueueTab', async () => {
  const fs = await import('node:fs/promises');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const here = path.dirname(fileURLToPath(import.meta.url));
  const src = await fs.readFile(path.resolve(here, '../src/components/left/LeftDock.tsx'), 'utf-8');
  assert.ok(/id: 'queue', label: 'Queue', Body: ConstructionQueueTab/.test(src), 'queue child tab must be wired into the buildZoning group');
});

test('BUG-605: queueChildLabel folds the count into "Queue (N)" only for the queue tab and only when non-empty (GR#21 pure)', async () => {
  const { queueChildLabel } = await import('../src/components/left/LeftDock.tsx');
  assert.equal(queueChildLabel('queue', 'Queue', 0), 'Queue', 'zero count renders no badge');
  assert.equal(queueChildLabel('queue', 'Queue', 3), 'Queue (3)', 'non-zero count folds into the label');
  assert.equal(queueChildLabel('structures', 'Structures', 3), 'Structures', 'non-queue tabs are never relabelled');
});

test('BUG-605: LeftDock top-level tabs include Build & Zoning unconditionally (queue lives inside it)', async () => {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimProvider } = await import('../src/sim/store.tsx');
  const { LeftDock } = await import('../src/components/left/LeftDock.tsx');

  const html = renderToString(
    React.default.createElement(SimProvider, { children: React.default.createElement(LeftDock) }),
  );
  assert.ok(html.includes('>Build &amp; Zoning<'), 'Build & Zoning group tab must always render (queue is unconditionally reachable)');
});
