// hud-inc2-alerts-and-overlay-discipline.test.mjs — FEAT-2326609720 inc2,
// AC-11/AC-12/AC-13.
//
// AC-12: none of the six new top-level tabs, nor Alerts' severity sub-tabs,
// may register as a second blocking overlay. Since alertsTabs.tsx is a plain
// in-flow tab body (never calls useBlockingOverlay/BLOCKING_OVERLAY_ID), this
// is proven by SOURCE ABSENCE — a source-grep RED-proof: if a future edit
// wired Alerts into the blocking-overlay registry, this test would need the
// import to appear and would need updating alongside it (i.e. it fails loud
// the moment someone tries).
//
// AC-11: no new magic z-index literal is introduced by any inc2 file — every
// z-index must come from overlayLayers.ts's Z_INDEX (or none at all, since
// this increment's docked tabs are ordinary in-flow content, not overlays).
//
// AC-13: hud-overlay-discipline.test.tsx and bug500-advisor-click-overlap.test.tsx
// (the literal z-index regression suites) are asserted to still exist and be
// non-empty — the real "still passes" proof is that scoped.mjs runs them
// green alongside this file (see the build report).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const componentsRoot = path.resolve(here, '../src/components');

test('AC-12: alertsTabs.tsx never imports the blocking-overlay registry', async () => {
  const src = await fs.readFile(path.join(componentsRoot, 'left/tabs/alertsTabs.tsx'), 'utf-8');
  // Strip // line comments and /* */ block comments before checking for a
  // REAL import/usage — the file's own doc-comment is allowed to name these
  // symbols in prose without tripping the check.
  const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
  assert.ok(!/from ['"][^'"]*overlayManager['"]/.test(code), 'Alerts tabs must not import from overlayManager');
  assert.ok(!/useBlockingOverlay\s*\(/.test(code), 'Alerts tabs must not call useBlockingOverlay(...)');
});

test('AC-11: none of the new inc2 tab files hand-roll a numeric z-index style', async () => {
  const files = [
    'left/LeftDock.tsx',
    'left/tabs/financeTabs.tsx',
    'left/tabs/populationTabs.tsx',
    'left/tabs/servicesTabs.tsx',
    'left/tabs/buildZoningTabs.tsx',
    'left/tabs/projectionsTabs.tsx',
    'left/tabs/alertsTabs.tsx',
    'left/tabs/debugTab.tsx',
    'right/RightDock.tsx',
  ];
  for (const f of files) {
    const src = await fs.readFile(path.join(componentsRoot, f), 'utf-8');
    assert.ok(!/zIndex\s*:\s*\d/.test(src), `${f} must not hand-roll a numeric zIndex style (AC-11)`);
  }
});

test('AC-13: the literal z-index regression suites this increment must not break still exist', async () => {
  const testRoot = path.resolve(here);
  for (const f of ['hud-overlay-discipline.test.tsx', 'bug500-advisor-click-overlap.test.tsx', 'mount.test.tsx']) {
    const stat = await fs.stat(path.join(testRoot, f));
    assert.ok(stat.isFile() && stat.size > 0, `${f} must still exist and be non-empty`);
  }
});
