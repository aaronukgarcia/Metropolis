// q100092-construction-display.test.tsx — Q100092 (Aaron, confirmed design):
// the DISPLAY half of BUG-569. The engine-side isOnline gates already stop an
// under-construction building's output/utilisation from reaching the economy
// (bug569-construction-gating.test.mjs). This file proves the TOOLTIP/PANEL
// (BuildingCard in MapView.tsx) tells the same truth: while a building fails
// the 'construction' gate it shows CONSTRUCTION PROGRESS (ticks elapsed /
// constructionTicks(sp), the SAME SSOT the Construction Queue tab and the
// map's WHY-offline tooltip already call — never a second formula, GR#3) and
// NEVER utilisation, nameplate output (PRODUCES), or served/revenue lines.
// Once online, utilisation/PRODUCES render exactly as before.
//
// Aaron's original repro: a pow_nuke (1,120 MW, £1.568bn -> constructionTicks
// = round(1568000000 / 1500000) = 1045 ticks) showing "produces power 1120"
// and a utilisation % while still being built. pow_nuke is used here for
// exactly that reason.
//
// RED-PROOF (GR#23/GR#21 verification standards): each assertion below was
// checked against a scratch-sabotaged copy (`cp MapView.tsx MapView.tsx.bak`,
// then commenting out the `underConstruction` guard on the Utilisation/
// PRODUCES/served lines and reverting the % suffix) and confirmed to fail
// (raw utilisation % and "produces power 1120" both reappeared on the
// under-construction card) before restoring via `mv MapView.tsx.bak
// MapView.tsx` (never git — GR#24). See task report for the transcript.
//
// Run with the scoped test runner (never a full glob):
// node ../tools/test/scoped.mjs test/q100092-construction-display.test.tsx

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { constructionTicks, computeRoadConnectivity } from '../src/sim/data.ts';
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

let _id = 100092000;
const B = (spec: string, x: number, y: number, extra: Record<string, unknown> = {}) => ({
  id: _id++,
  spec,
  x,
  y,
  ...extra,
});

// Same harness idiom as bug569-construction-gating.test.mjs: road connectivity
// computed exactly as advance() does every tick, so isOnline's road gates
// resolve real (not "connectivity not yet computed" pass-through).
function city(buildings: unknown[], tick: number): SimState {
  const s = initialState();
  const st = { ...s, buildings: buildings as SimState['buildings'], tick } as SimState;
  st.roadConnectivity = computeRoadConnectivity(st);
  return st;
}

async function renderCard(state: SimState, building: SimState['buildings'][number]) {
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
  };

  return renderToString(
    React.default.createElement(
      SimContext.Provider,
      { value },
      React.default.createElement(BuildingCard, {
        building,
        connected: true,
        showRefs: false,
        onClose: () => {},
      })
    )
  );
}

const NUKE_TICKS = constructionTicks({ cost: 1568000000, kind: 'power' } as any);

test('Q100092 sanity: pow_nuke constructionTicks matches Aaron repro scale (hundreds-to-low-thousands, not the £-cost itself)', () => {
  assert.ok(NUKE_TICKS > 100 && NUKE_TICKS < 10000, `expected a sane tick count, got ${NUKE_TICKS}`);
});

test('Q100092: an under-construction pow_nuke shows construction progress, never utilisation/output', async () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const halfway = Math.round(NUKE_TICKS / 2);
  const nuke = B('pow_nuke', 1, 10, { builtTick: 0 });
  // A SECOND, already-online power plant is required so powerStats().cap > 0 —
  // utilisationOf('power') is a CITYWIDE stat (powerStats gates capacity by
  // isOnline per BUG-430/431), so with only the still-building nuke on the
  // map the citywide cap is 0 and util comes back null "for free", which would
  // make the "no Utilisation line" assertion pass even with the display guard
  // removed. This wind turbine (cheap, road-adjacent, built at tick 0) is
  // online long before `halfway`, giving the citywide figure real magnitude so
  // the assertion actually exercises the underConstruction guard.
  const windRoad = B('road', 0, 20, { builtTick: 0 });
  const wind = B('pow_wind', 1, 20, { builtTick: 0 });
  const s = city([nuke, road, wind, windRoad], halfway);
  assert.ok(s.roadConnectivity, 'setup: road connectivity must be computed');

  const html = await renderCard(s, s.buildings[0]);

  assert.ok(html.includes('Under construction'), 'must show the Under construction line');
  assert.match(html, /\(\d+%\)/, 'the Under construction line must carry a percent-complete figure');
  assert.ok(!html.includes('Utilisation'), 'must NOT show a Utilisation line while under construction');
  assert.ok(!/produces power 1120/i.test(html), 'must NOT show the nameplate PRODUCES output while under construction (Aaron repro)');
  assert.ok(!html.includes('1120'), 'must NOT show the 1120 MW nameplate figure while under construction (Aaron repro)');
  assert.ok(
    html.includes('available once construction completes'),
    'must show the PRODUCES-suppressed placeholder note instead of the real output list'
  );
});

test('Q100092: construction percent complete tracks ticks elapsed / constructionTicks(sp), clamped and non-decreasing', async () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const early = Math.round(NUKE_TICKS * 0.1);
  const late = Math.round(NUKE_TICKS * 0.9);

  const nukeEarly = B('pow_nuke', 1, 10, { builtTick: 0 });
  const sEarly = city([nukeEarly, road], early);
  const htmlEarly = await renderCard(sEarly, sEarly.buildings[0]);
  const pctEarly = Number(/Under construction — .*\((\d+)%\)/.exec(htmlEarly)?.[1]);

  const nukeLate = B('pow_nuke', 1, 10, { builtTick: 0 });
  const sLate = city([nukeLate, road], late);
  const htmlLate = await renderCard(sLate, sLate.buildings[0]);
  const pctLate = Number(/Under construction — .*\((\d+)%\)/.exec(htmlLate)?.[1]);

  assert.ok(Number.isFinite(pctEarly) && Number.isFinite(pctLate), 'both cards must carry a parseable percent');
  assert.ok(pctEarly >= 8 && pctEarly <= 12, `expected ~10%, got ${pctEarly}%`);
  assert.ok(pctLate >= 88 && pctLate <= 92, `expected ~90%, got ${pctLate}%`);
  assert.ok(pctLate > pctEarly, 'percent complete must increase as ticks elapse');
});

test('Q100092: the SAME pow_nuke, once online, shows utilisation and PRODUCES starting from its real value', async () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  const nuke = B('pow_nuke', 1, 10, { builtTick: 0 });
  const s = city([nuke, road], NUKE_TICKS + 500); // well past construction

  const html = await renderCard(s, s.buildings[0]);

  assert.ok(!html.includes('Under construction'), 'an online building must not show construction wording');
  assert.ok(html.includes('Utilisation'), 'an online building must show its Utilisation line');
  assert.ok(html.includes('PRODUCES'), 'an online building must show its PRODUCES section');
  assert.ok(/produces|Power/i.test(html), 'PRODUCES content must be present for a power plant');
});

test('Q100092: a genesis building (builtTick: null) is never construction-gated and displays as today', async () => {
  const road = B('road', 0, 10, { builtTick: 0 });
  // No builtTick at all — the FEAT-1972079891 legacy/genesis exemption:
  // isOnline(s,b) returns true unconditionally when b.builtTick == null.
  const nuke = B('pow_nuke', 1, 10, {});
  const s = city([nuke, road], 0);

  const html = await renderCard(s, s.buildings[0]);

  assert.ok(!html.includes('Under construction'), 'a genesis building must never show construction wording');
  assert.ok(html.includes('Utilisation'), 'a genesis building must show its Utilisation line immediately');
  assert.ok(html.includes('PRODUCES'), 'a genesis building must show its PRODUCES section immediately');
});
