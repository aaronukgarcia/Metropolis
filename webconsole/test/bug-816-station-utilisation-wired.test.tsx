// bug-816-station-utilisation-wired.test.tsx — BUG-816: stationUtilisationOf
// (AC-3, FEAT-2326609772 inc2, src/sim/data.ts) had no production caller —
// the per-station utilisation attribution computed there never reached a
// screen. Wired into BuildingCard (src/components/MapView.tsx), the building-
// detail card the player sees when a station tile is selected (RightDock is
// retired — MapView's BuildingCard is the real "selected building" surface,
// verified by reading the component tree before touching code).
//
// The wired line is "Line utilisation: NN%" for connected stations whose
// class has a real usage basis; "Line utilisation: no line yet" when
// stationUtilisationOf reports null (disconnected, or connected but the
// class has no LineUsage entry — BUG-814's honest-absence convention). The
// percentage and HOT/OK styling come from the SAME class-level
// saturation/overCapacity lineUsageOf already computes and the Lines overlay
// already tints with (GR#3: no second saturation derivation) — .in (done
// green) when within capacity, .out (danger red) when overCapacity.
//
// Render-side only: derives during render off the memoised stationUtilisationOf
// / lineUsageOf exports, no new state, no Date.now/localStorage/Math.random,
// StrictMode-safe (no ref-mutating setState updaters — none added).
//
// Mount idiom follows test/q100092-construction-display.test.tsx exactly
// (renderToString + SimContext.Provider + BuildingCard), the established
// pattern for this file's mount tests. Fixtures follow
// test/bug-814-815-pins.test.mjs's board()/roadNear()/lineRun() idiom.
//
// Every assertion below states its own mutant (GR#21 prove-can-fail
// discipline). RED-PROOF: each pin was checked against a scratch-sabotaged
// copy (`cp MapView.tsx MapView.tsx.bak`, apply the stated mutant, confirm
// the assertion fails, `mv MapView.tsx.bak MapView.tsx` to restore — never
// git, GR#24) before this report.
//
// Run with the scoped test runner (never a full glob):
// npx tsx --test test/bug-816-station-utilisation-wired.test.tsx

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { computeRoadConnectivity } from '../src/sim/data.ts';
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

// Same board()/roadNear()/lineRun() idiom as bug-814-815-pins.test.mjs — a
// controlled board, explicit building list + population, no starter city.
function board(buildings: unknown[], population = 0): SimState {
  const base = initialState();
  let maxId = 0;
  for (const b of buildings as { id: number }[]) if (b.id > maxId) maxId = b.id;
  const s = {
    ...base,
    unlockedAll: true,
    buildings: buildings as SimState['buildings'],
    nextId: maxId + 1,
    roadNotice: null,
    population,
  } as SimState;
  s.roadConnectivity = computeRoadConnectivity(s);
  return s;
}

// A road tile adjacent to (x,y) so a station there is road-connected
// (stationLinks connects via an ADJACENT ROAD tile, not the rail tile itself).
function roadNear(id: number, x: number, y: number) {
  return { id, spec: 'rd_aroad', x: x + 1, y, builtTick: 0 };
}

// A run of `n` tiles of `spec` along the x-axis starting at (x0,y).
function lineRun(spec: string, startId: number, x0: number, y: number, n: number) {
  const out = [];
  for (let i = 0; i < n; i++) out.push({ id: startId + i, spec, x: x0 + i, y, builtTick: 0 });
  return out;
}

async function renderCard(state: SimState, building: SimState['buildings'][number], connected: boolean) {
  ensureMountWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { BuildingCard } = await import('../src/components/MapView.tsx');

  const value = {
    state,
    dispatch: () => {},
    cityName: 'Test City',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
    exportCity: async () => true,
    importCity: async () => true,
  };

  return renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value },
      React.default.createElement(BuildingCard, {
        building,
        connected,
        showRefs: false,
        onClose: () => {},
      })
    )
  );
}

// Deterministic, JSON-serialisable snapshot of the parts of SimState a
// selection/render could plausibly mutate. roadConnectivity is already a
// plain { connectedRoadTiles: string[] } (data.ts:997), so JSON.stringify is
// exact — no Map/Set to normalise.
function snapshot(s: SimState): string {
  return JSON.stringify({ buildings: s.buildings, population: s.population, tick: s.tick, roadConnectivity: s.roadConnectivity });
}

test('BUG-816 (1): a connected station WITH a rail usage basis shows the percentage from stationUtilisationOf/lineUsageOf, styled .in (within capacity)', async () => {
  const buildings = [
    ...lineRun('rail', 100, 0, 5, 3),
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
  ];
  // Probed fixture (scratch-probe.mjs, deleted after use): 3 rail tiles,
  // population 20000 -> rail usage 1600 / capacity 3600 -> saturation
  // 0.4444... -> rounds to 44%, overCapacity false.
  const s = board(buildings, 20000);
  const station = s.buildings.find((b) => b.spec === 'station_sanderling')!;
  const html = await renderCard(s, station, true);

  // React SSR inserts `<!-- -->` comment markers around interpolated
  // expressions, so the literal number/text are matched with a tolerant
  // regex rather than a single substring.
  assert.match(html, /Line utilisation:\s*(?:<!-- -->)?44(?:<!-- -->)?%/, `expected "Line utilisation: 44%" in card, got: ${html}`);
  // MUTANT: hardcoding the .out (danger/HOT) class instead of reading
  // cls.overCapacity would fail this — this fixture is within capacity.
  assert.match(html, /class="in">Line utilisation:/, 'within-capacity styling must use the .in (done/green) class, matching the Lines overlay OK convention');
  assert.ok(!html.includes('no line yet'), 'a station with a real usage basis must not show the no-line-yet fallback');
});

test('BUG-816 (2): a connected station with NO rail usage basis shows "no line yet", never a fabricated percentage', async () => {
  // BUG-814's own fixture: road-connected station, zero rail tiles anywhere
  // on the map -> lineUsageOf never emits a 'rail' class entry.
  const buildings = [roadNear(1, 0, 0), { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const html = await renderCard(s, s.buildings[1], true);

  // MUTANT: falling back to `stationStat?.utilisation ?? 0` instead of
  // checking `!== null` would render "Line utilisation: 0%" here instead of
  // the honest "no line yet" — a fabricated number indistinguishable from a
  // genuinely idle-but-connected line (BUG-814's exact class of bug, now on
  // the display side).
  assert.ok(html.includes('Line utilisation: no line yet'), `expected the no-line-yet fallback, got: ${html}`);
  assert.ok(!/Line utilisation: \d+%/.test(html), 'must never show a numeric percentage when stationUtilisationOf reports null');
});

test('BUG-816 (2b): a DISCONNECTED station on a map that DOES have a rail LineUsage entry elsewhere still shows "no line yet" (guards the null-check itself, not just the missing-class case)', async () => {
  // Rail tiles far away (feed a real rail LineUsage entry) but this station
  // has NO adjacent road, so it is disconnected — stationUtilisationOf must
  // report null via the connectedIds branch, even though `lineUsageOf(state)
  // .find(u => u.spec === 'rail')` WOULD resolve to a real entry if the null
  // check were dropped.
  const buildings = [...lineRun('rail', 100, 5, 5, 3), { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 }];
  const s = board(buildings, 100000);
  const station = s.buildings.find((b) => b.spec === 'station_sanderling')!;
  const html = await renderCard(s, station, false);

  // MUTANT: computing `cls = lineUsageOf(state).find(u => u.spec ===
  // stationStat?.lineSpec)` WITHOUT first checking `stationStat.utilisation
  // !== null` would find the real rail entry here (it exists on this map)
  // and render a fabricated percentage for a disconnected station.
  assert.ok(html.includes('Line utilisation: no line yet'), `expected the no-line-yet fallback for a disconnected station, got: ${html}`);
  assert.ok(!/Line utilisation: \d+%/.test(html), 'a disconnected station must never show a numeric percentage even when its class has real usage elsewhere');
});

test('BUG-816 (3): a non-station building shows no "Line utilisation" line at all', async () => {
  const buildings = [roadNear(1, 0, 0)];
  const s = board(buildings, 50000);
  const html = await renderCard(s, s.buildings[0], false);

  // MUTANT: dropping the `sp.kind === 'station'` guard on the new block
  // would make this road tile's card show a spurious utilisation line too.
  assert.ok(!html.includes('Line utilisation'), `a road building must never show a Line utilisation line, got: ${html}`);
});

test('BUG-816 (4): mounting/selecting a station card mutates no SimState (byte-identical before/after)', async () => {
  const buildings = [
    ...lineRun('rail', 100, 0, 5, 3),
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
  ];
  const s = board(buildings, 20000);
  const station = s.buildings.find((b) => b.spec === 'station_sanderling')!;
  const before = snapshot(s);

  await renderCard(s, station, true);
  await renderCard(s, station, true); // second mount — StrictMode-style double render

  const after = snapshot(s);
  // MUTANT: any implementation that wrote back to state (e.g. caching the
  // computed percentage onto the building object, or re-deriving
  // roadConnectivity into a NEW object each render instead of reading the
  // memoised one) would diverge this snapshot.
  assert.equal(after, before, 'rendering the station detail card twice must not mutate SimState at all');
});

test('BUG-816 (5): source-scan purity — the new station-utilisation block adds no Date.now/localStorage/Math.random and no ref-mutating setState updater (BUG-756)', async () => {
  const fs = await import('node:fs');
  const mapViewPath = new URL('../src/components/MapView.tsx', import.meta.url);
  const src = fs.readFileSync(mapViewPath, 'utf8');
  const marker = "stationUtilisationOf(state).find((x) => x.id === building.id)";
  const idx = src.indexOf(marker);
  assert.ok(idx >= 0, 'could not locate the BUG-816 station-utilisation block in MapView.tsx');
  // Scope the scan to the surrounding IIFE (from the preceding `(() => {` to
  // its matching `})()`), not the whole file — this pin is about the NEW
  // code, not a whole-file purity re-assertion.
  const blockStart = src.lastIndexOf('(() => {', idx);
  const blockEnd = src.indexOf('})()', idx) + '})()'.length;
  assert.ok(blockStart >= 0 && blockEnd > blockStart, 'could not bound the new block for scanning');
  const block = src.slice(blockStart, blockEnd);

  // MUTANT: adding `Date.now()`/`localStorage`/`Math.random()` or a
  // `setState(prev => { prev.x = ...; return prev })`-style ref-mutating
  // updater inside this exact block would trip these.
  assert.ok(!/Date\.now\(\)/.test(block), 'no Date.now in the new block');
  assert.ok(!/localStorage/.test(block), 'no localStorage in the new block');
  assert.ok(!/Math\.random\(\)/.test(block), 'no Math.random in the new block');
  assert.ok(!/setState/.test(block), 'no setState call in the new block (render-side derivation only, no new state)');
});

// ─────────────────────────────────────────────────────────────────────────────
// BUG-816 round finding 1 (opus-round-bug816, 2026-09-06). The pins above only
// ever assert the CLASS percentage — which is IDENTICAL for every station on a
// class — so they pass just as well against an implementation that never reads
// stationUtilisationOf's per-station `utilisation` at all (the shape as
// submitted: the station record was consulted ONLY for its null-ness). That
// leaves BUG-816's actual subject — the AC-3 PER-STATION attribution — still
// not on any screen. The pin below fails against that shape and passes only
// once the station's own attributed figure is rendered.
// ─────────────────────────────────────────────────────────────────────────────

test('BUG-816 (6): two stations on the SAME class with different attributed commuter counts render DIFFERENT figures — the per-station number, not just the class percentage', async () => {
  // Probed fixture (scratch816/probe2.mjs, deleted after use): 3 rail tiles,
  // TWO road-connected rail stations, population 20005 -> class usage 3201
  // apportioned floor-per-station with the remainder on the LAST station by
  // id, so id 2 -> 1600 and id 4 -> 1601. Both stations share the class
  // saturation 3201/3600 = 88.9% -> 89%.
  const buildings = [
    ...lineRun('rail', 100, 0, 5, 3),
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(3, 3, 0),
    { id: 4, spec: 'station_sanderling', x: 3, y: 0, builtTick: 0 },
  ];
  const s = board(buildings, 20005);
  const a = s.buildings.find((b) => b.id === 2)!;
  const b = s.buildings.find((b) => b.id === 4)!;
  const htmlA = await renderCard(s, a, true);
  const htmlB = await renderCard(s, b, true);

  const strip = (h: string) => h.replace(/<!-- -->/g, '');

  // Both stations sit on the same class, so the class percentage is the same
  // on both cards — which is exactly why the class percentage ALONE cannot
  // evidence per-station attribution.
  assert.match(strip(htmlA), /Line utilisation: 89% of class/, `station A class %: ${htmlA}`);
  assert.match(strip(htmlB), /Line utilisation: 89% of class/, `station B class %: ${htmlB}`);

  // MUTANT (the shape as originally submitted): rendering only the class
  // saturation and using stationUtilisationOf solely for its null check makes
  // these two cards byte-identical, and both assertions below fail.
  assert.ok(
    strip(htmlA).includes('1,600 commuters via this station'),
    `station id 2 must show its OWN attributed 1,600, got: ${htmlA}`
  );
  assert.ok(
    strip(htmlB).includes('1,601 commuters via this station'),
    `station id 4 must show its OWN attributed 1,601, got: ${htmlB}`
  );

  // MUTANT: displaying cls.usage (the CLASS total, 3,201) instead of
  // stationStat.utilisation would show the same wrong number on both cards.
  assert.ok(!strip(htmlA).includes('3,201'), 'must show the station share, not the class total');
  assert.ok(!strip(htmlB).includes('3,201'), 'must show the station share, not the class total');
});

test('BUG-816 (7): the displayed commuter figure equals stationUtilisationOf(state) for that id exactly (no re-derivation on the display side)', async () => {
  const { stationUtilisationOf } = await import('../src/sim/data.ts');
  const buildings = [
    ...lineRun('rail', 100, 0, 5, 3),
    ...lineRun('hs1', 200, 0, 7, 3),
    roadNear(1, 0, 0),
    { id: 2, spec: 'station_sanderling', x: 0, y: 0, builtTick: 0 },
    roadNear(3, 3, 0),
    { id: 4, spec: 'station_ashford', x: 3, y: 0, builtTick: 0 },
  ];
  const s = board(buildings, 20000);
  const stats = stationUtilisationOf(s);

  // Memo identity (BUG-602 discipline): the SAME state object must return the
  // SAME array instance, so a BuildingCard re-render recomputes nothing
  // city-proportional.
  assert.equal(stationUtilisationOf(s), stats, 'stationUtilisationOf must memo on state identity');

  for (const st of stats) {
    const building = s.buildings.find((b) => b.id === st.id)!;
    const html = (await renderCard(s, building, true)).replace(/<!-- -->/g, '');
    // MUTANT: any independent re-derivation on the display side (e.g.
    // apportioning class usage by station count in the component) would drift
    // from the SSOT export for the mixed rail(1,600)/hs1(4,800) board here.
    assert.ok(
      html.includes(`${st.utilisation!.toLocaleString('en-GB')} commuters via this station`),
      `card for station ${st.id} must show exactly ${st.utilisation}, got: ${html}`
    );
  }
});
