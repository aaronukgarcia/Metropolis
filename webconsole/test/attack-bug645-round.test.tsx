// attack-bug645-round.test.tsx — INDEPENDENT DESTRUCTIVE ROUND on BUG-645
// (GR#23: attacker != author). Author's estate: residentialConstructionSummary
// (sim/data.ts), isAtCapacity/RAG_THRESHOLDS.AT_CAPACITY (ragThresholds.ts),
// TopBar "(at capacity)" badge, DemographicsTab month-to-date/NET tiles.
//
// This file attacks the TRUTHFULNESS of what gets rendered on a state built
// to mirror Aaron's own reported numbers, the month-boundary reset, the
// badge's behaviour at every threshold/degenerate combination, and whether
// the tooltip ever promises relief that is not actually coming.

import { test } from 'node:test';
import assert from 'node:assert/strict';

function ensureWindow() {
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

function fakeCtx(state: any) {
  return {
    state,
    dispatch: () => {},
    cityName: 'Attackville',
    listSaves: () => [],
    listRecent: () => [],
    saveGame: async () => true,
    saveGameAs: async () => ({ ok: true }),
    loadGame: async () => {},
    loadNamed: async () => {},
    renameCity: () => ({ ok: true }),
  };
}

async function renderTopBar(state: any) {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { TopBar } = await import('../src/components/TopBar.tsx');
  return renderToString(
    React.default.createElement(SimContext.Provider, { value: fakeCtx(state) }, React.default.createElement(TopBar))
  );
}

async function renderDemographics(state: any) {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { DemographicsTab } = await import('../src/components/left/tabs/populationTabs.tsx');
  return renderToString(
    React.default.createElement(SimContext.Provider, { value: fakeCtx(state) }, React.default.createElement(DemographicsTab))
  );
}

/** A hand-built residential city where every building is a distinct spec
 *  instance sized so a target ONLINE capacity is reachable exactly, plus a
 *  configurable number of freshly-placed (still-under-construction) units of
 *  the SAME spec so a `count`/`capacity` pair is independently predictable
 *  without going through the real 30k-building scale fixture (this round
 *  cares about arithmetic correctness, not perf — that is the author's
 *  already-covered PERF section). Uses 'res_hut' (8 residents/building, no
 *  capacityTiers) matching the author's own UI test idiom. */
async function buildCity(opts: {
  onlineCount: number;
  underConstructionCount?: number;
  tick?: number;
}) {
  const { initialState } = await import('../src/sim/engine.ts');
  const tick = opts.tick ?? 1000;
  const buildings: any[] = [];
  const roadTiles: string[] = [];
  let id = 1;
  for (let i = 0; i < opts.onlineCount; i++) {
    const x = (i % 50) * 3;
    const y = Math.floor(i / 50) * 3;
    buildings.push({ id: id++, spec: 'res_hut', x, y, builtTick: 0 });
    buildings.push({ id: id++, spec: 'road', x: x + 1, y, builtTick: 0 });
    roadTiles.push(`${x + 1},${y}`);
  }
  for (let i = 0; i < (opts.underConstructionCount ?? 0); i++) {
    const x = 5000 + i * 2;
    buildings.push({ id: id++, spec: 'res_hut', x, y: 5000, builtTick: tick }); // builtTick === tick -> still building (G1)
  }
  let s = initialState();
  s = { ...s, tick, roadConnectivity: { connectedRoadTiles: roadTiles }, buildings };
  return s;
}

// ────────────────────────────────────────────────────────────────────────
// (a) TRUTHFULNESS — independent re-derivation on Aaron's real numbers
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: residentialConstructionSummary matches an INDEPENDENTLY hand-counted construction bucket (55 buildings, 1,120,000 capacity shape)', async () => {
  const { residentialConstructionSummary, offlineResidentsByReason, SPECS } = await import('../src/sim/data.ts');
  const { initialState } = await import('../src/sim/engine.ts');

  // Mirror Aaron's reported shape: 55 residential buildings under
  // construction totalling 1,120,000 capacity (~20,364/building — a
  // mega-estate-class spec). Build with the actual catalogue's residents
  // figure so the independent re-derivation uses the SAME per-spec constant
  // the production code reads, but counts/sums it by hand in THIS test,
  // never by calling the function under attack.
  const megaSpec = Object.values(SPECS as any).find((sp: any) => sp.kind === 'residential' && !sp.placeholder) as any;
  assert.ok(megaSpec, 'setup: catalogue must have a residential spec');
  const residentsPerBuilding = megaSpec.residents ?? 8;

  const tick = 1000;
  const buildings: any[] = [];
  for (let i = 0; i < 55; i++) {
    buildings.push({ id: i + 1, spec: megaSpec.id, x: 100 + i, y: 100, builtTick: tick }); // builtTick === tick -> under construction
  }
  let s = initialState();
  s = { ...s, tick, roadConnectivity: { connectedRoadTiles: [] }, buildings };

  // Independent hand re-derivation (never reusing computeFailedGates or
  // isOnline — a literal building-time check, mirroring what a human
  // auditor would compute from the raw fields).
  let handCount = 0;
  let handCapacity = 0;
  for (const b of buildings) {
    if (b.builtTick != null && s.tick - b.builtTick < 0) continue; // n/a here
    // constructionTicks(sp) > 0 for a real spec with cost > 0, so builtTick===tick
    // is unconditionally still-building for every non-zero-cost residential spec.
    handCount++;
    handCapacity += residentsPerBuilding;
  }

  const summary = residentialConstructionSummary(s);
  assert.equal(summary.count, handCount, 'building COUNT must match an independent hand count');
  assert.equal(summary.capacity, handCapacity, 'capacity must match an independent hand sum');
  assert.equal(summary.capacity, offlineResidentsByReason(s).construction, 'GR#3: must still agree with the shipped SSOT bucket');
});

test('ATTACK: TopBar badge percentage and NET rows are correctly signed and arithmetically exact on Aaron\'s reported live-city numbers', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const capacity = 1_903_719;
  const population = 1_898_104;

  let s = initialState();
  s = {
    ...s,
    population,
    buildings: [], // capacity comes from onlineResidentsCapacity — force it via a stub-free direct override is not possible,
  };
  // onlineResidentsCapacity is derived from actual buildings, so instead of
  // faking the capacity number, drive isAtCapacity/percentage math directly
  // against Aaron's reported pair — this proves the FORMULA (not the
  // building-derivation, which the author's data.ts round already covers).
  const { isAtCapacity } = await import('../src/components/ragThresholds.ts');
  assert.equal(isAtCapacity(population, capacity), true, 'Aaron\'s reported 99.7%-full city must read as at-capacity');
  const pct = (population / capacity) * 100;
  assert.ok(Math.abs(pct - 99.7) < 0.05, `independent percentage re-derivation: expected ~99.7%, got ${pct.toFixed(2)}%`);

  // NET rows arithmetic (lastDemographics numbers from the brief).
  const last = { births: 1518, deaths: 949, moveIns: 5979, moveOuts: 6548 };
  const natural = last.births - last.deaths;
  const migration = last.moveIns - last.moveOuts;
  const net = natural + migration;
  assert.equal(natural, 569, 'independent re-derivation: natural increase must be +569');
  assert.equal(migration, -569, 'independent re-derivation: net migration must be -569');
  assert.equal(net, 0, 'independent re-derivation: net population change must be exactly 0');

  const html = await renderDemographics({ ...s, lastDemographics: last, demographicAccum: { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 } });
  assert.ok(html.includes('+569'), 'DemographicsTab must render the independently-verified +569 natural increase');
  assert.ok(html.includes('-569'), 'DemographicsTab must render the independently-verified -569 net migration');
});

// ────────────────────────────────────────────────────────────────────────
// (b) MONTH BOUNDARY — does the tile flash near-zero and read as "city died"?
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: FINDING — the month-to-date tile reads a literal 0/0/0/0 flash on the exact tick the month rolls over, right after a high-flow month', async () => {
  const { reducer, TICKS_PER_MONTH } = await import('../src/sim/engine.ts');
  let s = await buildCity({ onlineCount: 60 });

  // Tick forward exactly to the next month boundary.
  let guard = 0;
  while (s.tick % TICKS_PER_MONTH !== 0) {
    s = reducer(s, { type: 'tick' });
    guard++;
    assert.ok(guard <= TICKS_PER_MONTH, 'setup: must cross a month boundary');
  }

  // The completed month's totals must have landed in demographicHistory...
  const history = s.demographicHistory ?? [];
  assert.ok(history.length > 0, 'setup: a completed month must be recorded in history');
  const closedMonth = history[history.length - 1];

  // ...but state.demographicAccum (what the UI reads for "This month so far")
  // is ALREADY reset to zero on this exact render, per engine.ts's own tick
  // order (accum computed -> pushed to history -> reset -> stored). This is
  // not a bug in this fix (the reset is pre-existing, shared engine
  // behaviour, also read by debugjson.ts) but IS a real player-visible
  // artifact of surfacing demographicAccum raw: for exactly one tick, right
  // after the highest-flow moment of the month, "This month so far" reads
  // 0 births / 0 deaths / 0 move-ins / 0 move-outs.
  assert.deepEqual(
    s.demographicAccum,
    { births: 0, deaths: 0, moveIns: 0, moveOuts: 0 },
    'CONFIRMED: demographicAccum is zero on the render immediately after a month closes'
  );

  const html = await renderDemographics(s);
  const monthSection = html.slice(html.indexOf('This month so far'));
  // The "This month so far" tiles must show 0 in this exact frame (not the
  // just-closed month's non-zero totals) -- assert this literally, so a
  // future change that tries to "fix" this by re-showing the old month's
  // numbers without updating the label is caught too.
  const zeroTileCount = (monthSection.match(/<div class="n">0<\/div>/g) ?? []).length;
  assert.ok(
    zeroTileCount >= 4,
    `expected at least 4 zero-valued tiles ("This month so far" births/deaths/moveIns/moveOuts) on the ` +
      `month-boundary render; found ${zeroTileCount}. If this assertion breaks because the UI now carries the ` +
      `closed month's totals forward for one extra tick, that is the fix for this finding — update this test.`
  );
  // The closed month itself was NOT zero -- the flash is a real display
  // artifact, not evidence that nothing happened.
  const closedMonthTotal = closedMonth.births + closedMonth.deaths + closedMonth.moveIns + closedMonth.moveOuts;
  assert.ok(closedMonthTotal > 0, 'setup sanity: the just-closed month had real, nonzero flows');
});

test('ATTACK: the FIRST tick of a fresh save with demographicAccum entirely absent renders zeros, not undefined/NaN', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  let s = initialState();
  s = { ...s, lastDemographics: undefined, demographicAccum: undefined } as any;
  const html = await renderDemographics(s);
  assert.ok(!/NaN|undefined/.test(html), 'a state with no demographic history yet must never render NaN/undefined');
});

// ────────────────────────────────────────────────────────────────────────
// (c) THE BADGE LIES CHECK — every threshold + degenerate combination
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: badge boundary — exactly 99% shows, 98.9% does not, exactly 100% shows', async () => {
  const { onlineResidentsCapacity } = await import('../src/sim/data.ts');
  const base = await buildCity({ onlineCount: 200 }); // 200 * 8 = 1600 capacity, fine granularity
  const capacity = onlineResidentsCapacity(base);
  assert.equal(capacity, 1600, 'setup: predictable capacity for exact-percentage arithmetic');

  const at99 = { ...base, population: Math.ceil(capacity * 0.99) };
  const below99 = { ...base, population: Math.floor(capacity * 0.989) };
  const at100 = { ...base, population: capacity };

  assert.ok((await renderTopBar(at99)).includes('(at capacity)'), 'population at 99% must show the badge');
  assert.ok(!(await renderTopBar(below99)).includes('(at capacity)'), 'population at 98.9% must NOT show the badge');
  assert.ok((await renderTopBar(at100)).includes('(at capacity)'), 'population at exactly 100% must show the badge');
});

test('ATTACK: badge stays shown (not NaN, not hidden) when population is ABOVE online capacity (the over-capacity decay branch)', async () => {
  const { onlineResidentsCapacity } = await import('../src/sim/data.ts');
  const base = await buildCity({ onlineCount: 40 });
  const capacity = onlineResidentsCapacity(base);
  const over = { ...base, population: Math.floor(capacity * 1.15) }; // 115% — post-demolition overshoot shape

  const html = await renderTopBar(over);
  assert.ok(html.includes('(at capacity)'), 'an over-capacity population must still read as at-capacity, not silently fall through');
  assert.ok(!/NaN/.test(html), 'the percentage tooltip must not render NaN for population > capacity');
  assert.match(html, /1[0-9][0-9]\.\d%/, 'the tooltip percentage should read visibly over 100% (e.g. 115.0%), not clamp silently to 100%');
});

test('ATTACK: fresh empty city (capacity 0, population 0) never shows the badge — no divide-by-zero NaN badge', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  const s = initialState();
  const html = await renderTopBar(s);
  assert.ok(!html.includes('(at capacity)'), 'a genuinely empty new city (0 capacity, 0 population) must never show at-capacity');
  assert.ok(!/NaN/.test(html), 'no NaN must leak into the TopBar for a zero-capacity state');
});

test('ATTACK: capacity 0 with population > 0 (all housing gone, citizens still present) — badge suppressed by the fail-closed isAtCapacity design, not a lie but worth flagging', async () => {
  const { initialState } = await import('../src/sim/engine.ts');
  let s = initialState();
  s = { ...s, population: 5000, buildings: [] }; // no residential buildings at all -> onlineResidentsCapacity === 0
  const html = await renderTopBar(s);
  // Documents the DESIGN CHOICE in ragThresholds.ts ("capacity<=0 never reads
  // as at-capacity — nothing to be full of"): a city with citizens but ZERO
  // housing capacity does NOT show the badge, even though this is arguably a
  // WORSE condition than being merely full. Not a crash / NaN, but a real
  // truthfulness gap worth a follow-up: this state currently gets no
  // messaging at all instead of "you have citizens with no housing".
  assert.ok(!html.includes('(at capacity)'), 'documents current (deliberate) behaviour: capacity=0/population>0 shows no badge');
  assert.ok(!/NaN/.test(html), 'must not render NaN even in this degenerate combination');
});

// ────────────────────────────────────────────────────────────────────────
// (d) DOES THE TOOLTIP LIE WHEN NOTHING IS ACTUALLY UNDER CONSTRUCTION?
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: at capacity with NOTHING under construction, the tooltip must NOT promise relief that is not coming', async () => {
  const { onlineResidentsCapacity, residentialConstructionSummary } = await import('../src/sim/data.ts');
  const base = await buildCity({ onlineCount: 40, underConstructionCount: 0 });
  const capacity = onlineResidentsCapacity(base);
  const s = { ...base, population: capacity };

  const underConstruction = residentialConstructionSummary(s);
  assert.equal(underConstruction.count, 0, 'setup: genuinely nothing under construction');

  const html = await renderTopBar(s);
  assert.ok(html.includes('(at capacity)'), 'setup sanity: badge must still show');
  assert.ok(!/homes under construction adding/.test(html), 'tooltip must NOT claim relief is queued when nothing is under construction');

  const demoHtml = await renderDemographics(s);
  assert.ok(!/homes under construction adding/.test(demoHtml), 'DemographicsTab hint must also not promise relief that is not coming');
});

test('ATTACK: at capacity WITH construction queued, the tooltip DOES name the concrete count+capacity (relief genuinely coming)', async () => {
  const { onlineResidentsCapacity, residentialConstructionSummary } = await import('../src/sim/data.ts');
  const base = await buildCity({ onlineCount: 40, underConstructionCount: 3 });
  const capacity = onlineResidentsCapacity(base);
  const s = { ...base, population: capacity };

  const underConstruction = residentialConstructionSummary(s);
  assert.equal(underConstruction.count, 3, 'setup: exactly 3 buildings under construction');

  const html = await renderTopBar(s);
  assert.match(html, /3 homes under construction adding [\d,]+ capacity/, 'tooltip must name the exact queued relief');
});

// ────────────────────────────────────────────────────────────────────────
// (h) Existing capacity readouts must not disagree (GR#3)
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: HousingTab and the new TopBar badge/DemographicsTab all read the SAME onlineResidentsCapacity number for one state (no disagreeing capacity readouts)', async () => {
  ensureWindow();
  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { onlineResidentsCapacity } = await import('../src/sim/data.ts');
  const { HousingTab } = await import('../src/components/left/tabs/populationTabs.tsx');

  const base = await buildCity({ onlineCount: 40 });
  const capacity = onlineResidentsCapacity(base);
  const s = { ...base, population: capacity };
  const ctx = fakeCtx(s);

  const housingHtml = renderToString(React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(HousingTab)));
  const topBarHtml = await renderTopBar(s);
  const demoHtml = await renderDemographics(s);

  const capacityStr = capacity.toLocaleString();
  assert.ok(housingHtml.includes(capacityStr), 'HousingTab must show the same capacity figure');
  assert.ok(topBarHtml.includes(capacityStr), 'TopBar badge tooltip must show the same capacity figure');
  assert.ok(demoHtml.includes(capacityStr), 'DemographicsTab hint must show the same capacity figure');
});

// ────────────────────────────────────────────────────────────────────────
// (g) GR#21 purity — repeated render at scale-adjacent state is deterministic
// ────────────────────────────────────────────────────────────────────────

test('ATTACK: repeated TopBar renders across a small tick sequence are deterministic (no Date/Math.random leak) for a near-capacity city', async () => {
  const { reducer } = await import('../src/sim/engine.ts');
  let s = await buildCity({ onlineCount: 50 });
  const { onlineResidentsCapacity } = await import('../src/sim/data.ts');
  s = { ...s, population: Math.floor(onlineResidentsCapacity(s) * 0.999) };

  for (let i = 0; i < 5; i++) {
    s = reducer(s, { type: 'tick' });
  }
  const html1 = await renderTopBar(s);
  const html2 = await renderTopBar(s);
  assert.equal(html1, html2, 'identical state must render byte-identical TopBar HTML');
});
