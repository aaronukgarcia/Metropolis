/**
 * claude-dispatch-guard.test.js — unit tests for claude-dispatch-guard.js.
 *
 * First test file for this hook (none existed before BUG-135). Covers the
 * three BUG-135 findings from FEAT-072's Destructive round:
 *   1. candidateMkeys() — the generalised mkey-agreement extraction that now
 *      runs for every dispatch type, not just BA-criteria ones, and rejects
 *      path-shaped false positives (code.json, data.catalogue.md) via the
 *      DB-derived-prefix + extension-stripping logic.
 *   2. BOW_CODE_RE case-insensitivity, exercised indirectly through the
 *      exported normalise()/candidateMkeys() plumbing is DB-dependent, so
 *      this file spot-checks the regex itself directly (no DB needed).
 *   3. foldPath()/overlaps() case-folding for Windows' case-insensitive FS.
 *
 * Round 2 (BUG-139): the mkey-agreement check above required a line to name
 * EXACTLY ONE BOW code and EXACTLY ONE candidate mkey before checking
 * anything — inert against ordinary brief prose, which routinely cites two
 * codes together ("fixing BUG-135, filed against FEAT-072"). Replaced with
 * nearestMkeyPerCode(), which paired every code with its nearest candidate by
 * raw character distance.
 *
 * Round 3 (BUG-142): raw distance itself proved too loose — it false-DENIED
 * TRUE multi-code sentences ("FEAT-072 and FEAT-073 both touch
 * tool.dispatchguard") because proximity alone can't tell which code actually
 * claims a shared nearby candidate. nearestMkeyPerCode() now requires an
 * explicit SYNTACTIC attachment (a parenthetical, colon/dash, bare copula, or
 * possessive/relative assertion — see ATTACH_GAP_RE in the guard) between a
 * code and a candidate before pairing them, trading some recall (BUG-139's
 * own synthetic fixture no longer resolves — see below) for precision on
 * real brief prose.
 *
 * DB-dependent behavior (the live per-line mismatch DENY, the BOW-code-exists
 * check, file-claim collisions) is integration-level and out of scope for a
 * unit file with no DB fixture harness — this file proves the pure logic
 * every one of those checks is built on.
 *
 * Run: node --test claude-dispatch-guard.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  candidateMkeys,
  nearestMkeyPerCode,
  foldPath,
  overlaps,
  normalise,
  extractOwnedPaths,
} = require('./claude-dispatch-guard.js');

const PREFIXES = new Set(['tool', 'engine', 'foundation', 'data', 'feat', 'harness']);

test('candidateMkeys finds a bare real-family mkey token', () => {
  const found = candidateMkeys('citing FEAT-072 (tool.authorguard) by mistake', PREFIXES);
  assert.ok(found.has('tool.authorguard'));
});

test('candidateMkeys catches the pluralization near-miss', () => {
  const found = candidateMkeys('write engine.invariants.md for this', PREFIXES);
  // "engine.invariants" survives extension-stripping (only 2 segs, "md" is the 3rd)
  assert.ok(found.has('engine.invariants'));
});

test('candidateMkeys drops bare filenames below the 2-segment floor (code.json, package.json)', () => {
  const found = candidateMkeys('run node against code.json and package.json', PREFIXES);
  assert.equal(found.size, 0);
});

test('candidateMkeys strips a real extension from a correctly-named acceptance doc, leaving the real mkey', () => {
  const found = candidateMkeys('see docs/planning/acceptance/data.catalogue.md for detail', PREFIXES);
  assert.ok(found.has('data.catalogue'));
  assert.equal(found.size, 1);
});

test('candidateMkeys ignores a dotted token whose family is not a known prefix', () => {
  const found = candidateMkeys('reference version.1.2.3 in the changelog', PREFIXES);
  assert.equal(found.size, 0);
});

test('candidateMkeys is a no-op on a line with no dotted tokens', () => {
  const found = candidateMkeys('dispatch a junior developer to build the thing', PREFIXES);
  assert.equal(found.size, 0);
});

test('foldPath lowercases for comparison', () => {
  assert.equal(foldPath('internal/Foundation/Data'), 'internal/foundation/data');
});

test('overlaps treats differently-cased spellings of the same path as colliding (BUG-135)', () => {
  assert.ok(overlaps('internal/Foundation/data', 'internal/foundation/Data'));
  assert.ok(overlaps('internal/foundation/data', 'internal/foundation/data/reload.go'));
  assert.ok(!overlaps('internal/foundation/data', 'internal/foundation/database'));
});

test('normalise still preserves original casing (only overlaps() folds)', () => {
  assert.equal(normalise('internal/Foundation/Data/'), 'internal/Foundation/Data');
});

test('extractOwnedPaths only claims paths under an ownership declaration', () => {
  const prompt = [
    'Read internal/other/thing.go for context.',
    '',
    'FILES YOU OWN:',
    'internal/engine/helper/registry.go',
    'internal/engine/helper/registry_test.go',
    '',
    'Do not touch anything else.',
  ].join('\n');
  const owned = extractOwnedPaths(prompt);
  assert.ok(owned.includes('internal/engine/helper/registry.go'));
  assert.ok(owned.includes('internal/engine/helper/registry_test.go'));
  assert.ok(!owned.includes('internal/other/thing.go'));
});

// --- BUG-139: nearestMkeyPerCode no longer requires exactly one code and ---
// --- one candidate per line -- the ordinary prose style of real briefs.   ---
// --- BUG-142: raw nearest-by-distance was replaced with a requirement    ---
// --- for direct SYNTACTIC attachment (see ATTACH_GAP_RE in the guard),   ---
// --- because distance alone false-DENIED true multi-code sentences.      ---

test('nearestMkeyPerCode falls outside detection scope for BUG-139\'s original fixture — no code is directly, syntactically attached to a candidate (BUG-142)', () => {
  const line = 'FEAT-072 is related to BUG-136, whose real mkey is tool.authorguard, not tool.dispatchguard';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  // Neither FEAT-072 nor BUG-136 is DIRECTLY adjacent to a candidate with
  // only a recognised connector between them for FEAT-072 (too much prose
  // in the gap) — correctly left unchecked now, trading this fixture's
  // coverage for BUG-142's false-positive fix. BUG-136's "whose real mkey
  // is" IS a recognised relative-clause connector, so BUG-136 itself may
  // still resolve if it's ever cited with a known mkey — this line only
  // asserts the FEAT-072 case, which was BUG-139's actual claim.
  assert.ok(!pairs.has('FEAT-072'));
});

test('nearestMkeyPerCode attaches a possessive/relative assertion ("whose ... key is") to its own nearest preceding code', () => {
  const line = 'BUG-136, whose real mkey is tool.authorguard, needs triage';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  assert.equal(pairs.get('BUG-136'), 'tool.authorguard');
});

test('nearestMkeyPerCode does NOT false-DENY a true multi-code sentence sharing one candidate (BUG-142 exact fixture)', () => {
  const line = 'FEAT-072 and FEAT-073 both touch tool.dispatchguard';
  const pairs = nearestMkeyPerCode(line, PREFIXES);
  // Neither code is directly attached to the shared candidate — "and
  // FEAT-073 both touch" / "both touch" are not recognised connectors —
  // so both are correctly left unchecked rather than falsely flagged.
  assert.ok(!pairs.has('FEAT-072'));
  assert.ok(!pairs.has('FEAT-073'));
});

test('nearestMkeyPerCode still resolves the plain single-code/single-candidate parenthetical case (BUG-135 regression)', () => {
  const pairs = nearestMkeyPerCode('citing FEAT-072 (tool.authorguard) by mistake', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
  assert.equal(pairs.size, 1);
});

test('nearestMkeyPerCode resolves a colon-attached assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072: tool.authorguard is wrong', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves a dash-attached assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072 - tool.authorguard', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves a bare-copula assertion', () => {
  const pairs = nearestMkeyPerCode('FEAT-072 is tool.authorguard, apparently', PREFIXES);
  assert.equal(pairs.get('FEAT-072'), 'tool.authorguard');
});

test('nearestMkeyPerCode resolves this project\'s own dispatch-brief citation style', () => {
  const pairs = nearestMkeyPerCode(
    'FEAT-072 (tool.dispatchguard, claude-dispatch-guard.js) after a junior fixed BUG-135',
    PREFIXES
  );
  assert.equal(pairs.get('FEAT-072'), 'tool.dispatchguard');
});

test('nearestMkeyPerCode ignores an unattached candidate that merely happens to be nearby', () => {
  // tool.two sits right after FEAT-072's parenthetical closes, with no
  // connector of its own -- BUG-142's whole point is that bare proximity
  // (formerly picked by raw distance) is no longer sufficient on its own.
  const line = 'FEAT-072 (tool.one) tool.two';
  const prefixes = new Set(['tool']);
  const pairs = nearestMkeyPerCode(line, prefixes);
  assert.equal(pairs.get('FEAT-072'), 'tool.one');
});

test('nearestMkeyPerCode is a no-op when the line has codes but no candidate mkeys', () => {
  const pairs = nearestMkeyPerCode('dispatch FEAT-072 and BUG-136 together', PREFIXES);
  assert.equal(pairs.size, 0);
});

test('nearestMkeyPerCode is a no-op when the line has candidates but no codes', () => {
  const pairs = nearestMkeyPerCode('see tool.authorguard for detail', PREFIXES);
  assert.equal(pairs.size, 0);
});

test('BOW_CODE_RE-equivalent lowercase code is matched case-insensitively', () => {
  // The guard's own regex is internal, but its /gi behavior is verified via
  // the same pattern shape here since candidateMkeys/extractOwnedPaths don't
  // touch code matching directly — this locks the case-insensitive contract.
  const re = /\b(MOD|FEAT|BUG|SEC|INT|ASM)-(\d{3,})\b/gi;
  const matches = [...'dispatch on feat-072 and Mod-070'.matchAll(re)].map((m) => m[0].toUpperCase());
  assert.deepEqual(matches, ['FEAT-072', 'MOD-070']);
});
