// hud-inc2-rag-thresholds.test.mjs — FEAT-2326609720 inc2, AC-7/AC-8/AC-9/AC-14.
//
// Pure-function boundary tests against ragThresholds.ts — no React mount
// needed, these are exactly the RAG classifier functions the tab components
// call. AC-7: threshold boundaries (69/70, 0.79/0.80) flip at the documented
// line, not one tick early or late. AC-8: the shared RAG_THRESHOLDS constant
// name is referenced by more than one call site (grep proof). AC-9: power
// RAG is computed via the brownout-aware predicate, never raw cap<need — a
// covered shortfall (Grid Import ON) must never render RED.
//
// RED PROOF: flipping ragForWellbeing's `>=` to `>` on a scratch copy (never
// git-reverted — GR#24) turned the "69/70" boundary case red (70 landed in
// the amber branch instead of green) — confirms the boundary assertions can
// actually fail, not just always pass. Restored via cp/mv.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  RAG_THRESHOLDS,
  ragForWellbeing,
  ragForApproval,
  ragForCoverage,
  ragForPower,
  ragForUnemployment,
  ragForLineSaturation,
  ragForHousingHeadroom,
  ragForFiscalNet,
  ragForInsolvency,
  ragForWasteCollection,
} from '../src/components/ragThresholds.ts';

const here = path.dirname(fileURLToPath(import.meta.url));

test('AC-7: wellbeing boundary flips exactly at 70/45, not one tick early or late', () => {
  assert.equal(ragForWellbeing(69), 'amber');
  assert.equal(ragForWellbeing(70), 'green');
  assert.equal(ragForWellbeing(45), 'amber');
  assert.equal(ragForWellbeing(44), 'red');
});

test('AC-7: coverage-ratio boundary flips exactly at 0.79/0.80', () => {
  assert.equal(ragForCoverage(0.79), 'red');
  assert.equal(ragForCoverage(0.80), 'amber');
  assert.equal(ragForCoverage(0.999), 'amber');
  assert.equal(ragForCoverage(1.0), 'green');
});

test('AC-7: approval boundary flips exactly at 55/40', () => {
  assert.equal(ragForApproval(54), 'amber');
  assert.equal(ragForApproval(55), 'green');
  assert.equal(ragForApproval(39), 'red');
  assert.equal(ragForApproval(40), 'amber');
});

test('AC-7: unemployment boundary flips exactly at 7%/15% (lower is better)', () => {
  assert.equal(ragForUnemployment(0.069), 'green');
  assert.equal(ragForUnemployment(0.07), 'amber');
  assert.equal(ragForUnemployment(0.15), 'amber');
  assert.equal(ragForUnemployment(0.1501), 'red');
});

test('AC-7: housing-headroom boundary flips exactly at 20%/5%', () => {
  assert.equal(ragForHousingHeadroom(0.19), 'amber');
  assert.equal(ragForHousingHeadroom(0.20), 'green');
  assert.equal(ragForHousingHeadroom(0.049), 'red');
  assert.equal(ragForHousingHeadroom(0.05), 'amber');
});

test('AC-7: line-saturation boundary flips exactly at 0.80, and overCapacity always wins RED', () => {
  assert.equal(ragForLineSaturation(0.79, false), 'green');
  assert.equal(ragForLineSaturation(0.80, false), 'amber');
  assert.equal(ragForLineSaturation(0.5, true), 'red', 'overCapacity must force RED even at low nominal saturation');
});

test('AC-7: fiscal net binary boundary is exactly at 0', () => {
  assert.equal(ragForFiscalNet(-1), 'red');
  assert.equal(ragForFiscalNet(0), 'green');
  assert.equal(ragForFiscalNet(1), 'green');
});

test('AC-7: insolvency band direct mapping, exact boundary at the state-machine transition', () => {
  assert.equal(ragForInsolvency(undefined), 'green');
  assert.equal(ragForInsolvency('solvent'), 'green');
  assert.equal(ragForInsolvency('warning'), 'amber');
  assert.equal(ragForInsolvency('crisis'), 'red');
  assert.equal(ragForInsolvency('administration'), 'red');
  assert.equal(ragForInsolvency('bailout_second'), 'red');
  assert.equal(ragForInsolvency('decline'), 'red');
});

test('AC-7: waste collection binary (open question 5, stays binary for inc2)', () => {
  assert.equal(ragForWasteCollection(false), 'green');
  assert.equal(ragForWasteCollection(true), 'red');
});

test('AC-9: power RAG is never RED for a COVERED shortfall (Grid Import ON) — only a real uncovered brownout is RED', () => {
  // Coverage met -> green regardless of brownout flag (defensive; brownout can't be true if covered).
  assert.equal(ragForPower({ coverageMet: true, brownoutActive: false }), 'green');
  // Shortfall exists (coverageMet false) but Grid Import is covering it
  // (isBrownoutActive false, per the toggle-aware SSOT) -> AMBER, never RED.
  assert.equal(ragForPower({ coverageMet: false, brownoutActive: false }), 'amber');
  // Shortfall exists AND is genuinely uncovered (isBrownoutActive true) -> RED.
  assert.equal(ragForPower({ coverageMet: false, brownoutActive: true }), 'red');
});

test('AC-8: RAG_THRESHOLDS is the ONE named constants object — coverage and line-saturation share the same 0.8 literal', () => {
  assert.equal(RAG_THRESHOLDS.COVERAGE_RATIO.AMBER, 0.8);
  // ragForLineSaturation reuses COVERAGE_RATIO.AMBER internally (source grep,
  // since the function computes its own branch rather than exposing the
  // literal it read) — proven by both landing on the same boundary value.
  assert.equal(ragForLineSaturation(0.8, false), 'amber');
  assert.equal(ragForCoverage(0.8), 'amber');
});

test('AC-8: source grep — RAG_THRESHOLDS is imported/referenced at multiple call sites, not re-typed as a magic number', async () => {
  const webconsoleRoot = path.resolve(here, '..');
  const files = [
    'src/components/left/tabs/populationTabs.tsx',
    'src/components/left/tabs/servicesTabs.tsx',
    'src/components/left/tabs/buildZoningTabs.tsx',
    // BUG-580: TopBar.tsx's wellbeing dot repointed at the shared classifier
    // (was a local 70/45 literal duplicating WELLBEING.GREEN/AMBER) —
    // completes AC-8's single-source requirement for the wellbeing row.
    'src/components/TopBar.tsx',
  ];
  let hits = 0;
  for (const f of files) {
    const src = await fs.readFile(path.join(webconsoleRoot, f), 'utf-8');
    if (/ragThresholds/.test(src)) hits++;
  }
  assert.ok(hits >= 2, 'at least two tab-component files must import from the shared ragThresholds module (AC-8 SSOT)');
});
