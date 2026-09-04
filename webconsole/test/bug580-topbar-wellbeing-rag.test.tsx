// bug580-topbar-wellbeing-rag.test.mjs — BUG-580.
//
// TopBar.tsx's wellbeing dot used to compute its colour from a LOCAL literal
// (`wb.overall >= 70 ? done : wb.overall >= 45 ? warn : danger`) duplicating
// ragThresholds.ts's RAG_THRESHOLDS.WELLBEING (GREEN 70 / AMBER 45, HUD inc2
// FEAT-2326609720 AC-8), which populationTabs.tsx's WellbeingTab already
// consumes via ragForWellbeing/ragColor. The fix repoints TopBar at the SAME
// pair so a future retune of RAG_THRESHOLDS.WELLBEING moves BOTH consumers
// (AC-8's whole point — single source of truth).
//
// hud-inc2-rag-thresholds.test.mjs already pins the EXACT boundary
// (ragForWellbeing(69)==='amber', (70)==='green', (45)==='amber',
// (44)==='red') at the shared-constant level. This file proves TopBar is
// actually WIRED to that same function (not a parallel copy that happens to
// agree today):
//   1. Source-integrity: TopBar.tsx contains no local wellbeing threshold
//      literal (69/70/45/44/'--done'/'--warn'/'--danger' ternary) and calls
//      ragForWellbeing/ragColor instead.
//   2. Render cross-check: TopBar's rendered wb-dot colour, for a REAL
//      simulated city, equals ragColor(ragForWellbeing(wb.overall)) computed
//      independently from the SAME wellbeingOf(state) — so if TopBar ever
//      forked its own copy of the classifier, this diverges and reddens.
//
// RED-PROOF: reverting TopBar.tsx's `wbColor` line to a literal with a
// DIVERGING boundary (e.g. `wb.overall >= 71 ? ... : ...`) makes assertion 1
// (the literal-threshold grep) fail immediately; it would also desync
// assertion 2 for any city whose wb.overall lands in [70, 71) — reproduced
// by hand during development and restored via cp/mv (GR#24 — no git revert).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const topBarPath = path.resolve(here, '../src/components/TopBar.tsx');

test('BUG-580: TopBar.tsx has no local wellbeing threshold literal and imports the shared RAG classifier', async () => {
  const src = await fs.readFile(topBarPath, 'utf-8');

  assert.ok(
    /import\s*\{[^}]*ragForWellbeing[^}]*ragColor[^}]*\}\s*from\s*['"]\.\/ragThresholds['"]|import\s*\{[^}]*ragColor[^}]*ragForWellbeing[^}]*\}\s*from\s*['"]\.\/ragThresholds['"]/.test(src),
    'TopBar.tsx must import ragForWellbeing and ragColor from ./ragThresholds'
  );

  // CRITICAL: the old naive duplication — a bare `>= 70 ... >= 45` ternary
  // over wb.overall — must be gone. This is the literal that would silently
  // drift from RAG_THRESHOLDS.WELLBEING on a future retune.
  assert.ok(
    !/wb\.overall\s*>=\s*70/.test(src) && !/wb\.overall\s*>=\s*45/.test(src),
    'TopBar.tsx must not hardcode the 70/45 wellbeing thresholds locally — it must read RAG_THRESHOLDS.WELLBEING via ragForWellbeing'
  );

  assert.ok(
    /ragColor\(\s*ragForWellbeing\(\s*wb\.overall\s*\)\s*\)/.test(src),
    'TopBar.tsx must compute its wellbeing colour as ragColor(ragForWellbeing(wb.overall))'
  );
});

test('BUG-580: TopBar wb-dot colour matches ragColor(ragForWellbeing(wb.overall)) for a real simulated city', async () => {
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

  const React = await import('react');
  const { renderToString } = await import('react-dom/server');
  const { SimContext } = await import('../src/sim/simContext.ts');
  const { initialState, wellbeingOf } = await import('../src/sim/engine.ts');
  const { ragForWellbeing, ragColor } = await import('../src/components/ragThresholds.ts');
  const { TopBar } = await import('../src/components/TopBar.tsx');

  const state = initialState();
  const wb = wellbeingOf(state);
  const expectedColor = ragColor(ragForWellbeing(wb.overall));

  const ctx = {
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
    exportCity: async () => true,
    importCity: async () => true,
  };

  const html = renderToString(
    React.default.createElement(SimContext.Provider, { value: ctx }, React.default.createElement(TopBar))
  );

  assert.ok(html.length > 0, 'TopBar must render');
  const dotMatch = html.match(/class="wb-dot" style="([^"]*)"/);
  assert.ok(dotMatch, 'the wb-dot element with an inline style must render');
  assert.ok(
    dotMatch[1].includes(`background:${expectedColor}`) || dotMatch[1].includes(`background: ${expectedColor}`),
    `wb-dot background must be ${expectedColor} (ragColor(ragForWellbeing(${wb.overall}))), got style: ${dotMatch[1]}`
  );
});
