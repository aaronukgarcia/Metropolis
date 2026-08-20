/**
 * tools/codebase-viz/generate.test.js — regression test for ASM-802.
 *
 * ASM-802 surfaced that the codebase-viz BA-story gate only detected acceptance
 * docs named by the exact module key (<module.key>.md), so feat.-prefixed docs
 * (feat.maintenance.md, feat.staffing.md, feat.helicopters.md) were missed and
 * engine.maintenance / engine.staffing / engine.airunits emitted status=null
 * (plan entry only) even though their acceptance criteria existed.
 *
 * The repo names acceptance docs under docs/planning/acceptance/ in (at least)
 * three families — module key (engine.citizens.md), feature key
 * (feat.maintenance.md), and BOW code (BUG-011.md) — plus a header-declared
 * BOW code that ties a feature-named file to a module whose feature name
 * differs (the old feat.helicopters.md declared "BOW code: MOD-074", which is
 * engine.airunits). hasAcceptanceForModule() must recognise all four routes.
 *
 * This is a unit test of the pure hasAcceptanceForModule() predicate, so it
 * runs fast and deterministically without the DB / git / go-test machinery of
 * the full generator (which is why main() is guarded by require.main === module).
 *
 * Run: node tools/codebase-viz/generate.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { hasAcceptanceForModule } = require('./generate.js');

// acceptance index shape as returned by loadAcceptanceDocs().
function acceptance(withName, withBowCode) {
  return { byName: new Set(withName || []), byBowCode: new Set(withBowCode || []) };
}

// ── ASM-802: feat.-prefixed acceptance docs ARE detected ──────────────────────

test('ASM-802: feat.-prefixed acceptance doc is detected (engine.maintenance ↔ feat.maintenance.md)', () => {
  const idx = acceptance(['feat.maintenance']);
  assert.equal(hasAcceptanceForModule('engine.maintenance', null, idx), true);
});

test('ASM-802: feat.-prefixed acceptance doc is detected (engine.staffing ↔ feat.staffing.md)', () => {
  const idx = acceptance(['feat.staffing']);
  assert.equal(hasAcceptanceForModule('engine.staffing', null, idx), true);
});

// ── module-key route (a): any layer prefix, not just engine. ──────────────────

test('module-key acceptance doc is detected (engine.citizens.md)', () => {
  const idx = acceptance(['engine.citizens']);
  assert.equal(hasAcceptanceForModule('engine.citizens', null, idx), true);
});

test('module-key acceptance doc is detected for a tool layer (tool.codebaseviz.md)', () => {
  const idx = acceptance(['tool.codebaseviz']);
  assert.equal(hasAcceptanceForModule('tool.codebaseviz', null, idx), true);
});

test('module-key acceptance doc is detected for a multi-segment key (ui.screen.map.md)', () => {
  const idx = acceptance(['ui.screen.map']);
  assert.equal(hasAcceptanceForModule('ui.screen.map', null, idx), true);
});

// ── BOW-code routes (c) / (c') ────────────────────────────────────────────────

test('BOW-code filename doc is detected (<code>.md, e.g. BUG-011.md)', () => {
  const idx = acceptance(['BUG-011']);
  const bowRec = { code: 'BUG-011' };
  assert.equal(hasAcceptanceForModule('engine.some-module', bowRec, idx), true);
});

test('header-declared BOW code is detected (feat.helicopters.md declared MOD-074 = engine.airunits)', () => {
  // The ASM-802 airunits case: the acceptance doc is named after the FEATURE
  // (feat.helicopters), not the module (engine.airunits), so the module-key and
  // feat.<stem> routes miss it — only the header-declared BOW code ties them.
  const idx = acceptance(['feat.helicopters'], ['MOD-074']);
  const bowRec = { code: 'MOD-074' };
  assert.equal(hasAcceptanceForModule('engine.airunits', bowRec, idx), true);
});

// ── no false positives / degradation ──────────────────────────────────────────

test('no acceptance doc matches → false', () => {
  const idx = acceptance(['engine.world']);
  assert.equal(hasAcceptanceForModule('engine.citizens', null, idx), false);
});

test('empty acceptance index (unreadable dir) → false, never throws', () => {
  const idx = acceptance();
  assert.equal(hasAcceptanceForModule('engine.citizens', null, idx), false);
});

test('module key without a second segment still evaluates safely', () => {
  const idx = acceptance(['root']);
  assert.equal(hasAcceptanceForModule('root', null, idx), true);       // route (a) exact key
  assert.equal(hasAcceptanceForModule('feat', null, idx), false);      // no stem to featurise
});
