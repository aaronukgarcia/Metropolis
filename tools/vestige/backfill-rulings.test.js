/**
 * tools/vestige/backfill-rulings.test.js — tests for backfill-rulings.js
 * (FEAT-2326609713). node:test, discovered by root `node --test`.
 *
 * All tests exercise buildReviewEntries()/extractRulingFromComment()
 * against hand-built row fixtures -- no DB connection, per this module's
 * own "pure and directly testable" contract (fetchRows/main are the only
 * DB-touching exports and are left uncalled here).
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const br = require('./backfill-rulings.js');

function row(code, mkey, body, createdAt) {
  return { code, mkey: mkey || null, body, createdAt: createdAt || new Date(`${body.match(/\d{4}-\d{2}-\d{2}/) || '2026-01-01'}T00:00:00Z`) };
}

// --- extractRulingFromComment -----------------------------------------

test('extractRulingFromComment pulls the date and trims the marker prefix', () => {
  const r = br.extractRulingFromComment(row('BUG-472', 'engine.persist',
    'AARON RULING 2026-09-01 (live interview): HALT + SURFACE. Pause the sim.'));
  assert.equal(r.itemCode, 'BUG-472');
  assert.equal(r.date, '2026-09-01');
  assert.equal(r.text, 'HALT + SURFACE. Pause the sim.');
  assert.equal(r.lowConfidence, false);
});

test('extractRulingFromComment handles the plural "AARON RULINGS" marker', () => {
  const r = br.extractRulingFromComment(row('FEAT-2326609711', null,
    'AARON RULINGS 2026-09-01 (live interview) on the open questions - ALL FOUR selected: (1) inc1 ships as price premium.'));
  assert.equal(r.date, '2026-09-01');
  assert.match(r.text, /^on the open questions/);
});

test('extractRulingFromComment falls back to created_at when no date is printed in the body', () => {
  const r = br.extractRulingFromComment(row('BUG-9', null,
    'AARON RULING: proceed with the plan as discussed.', new Date('2026-08-15T12:00:00Z')));
  assert.equal(r.date, '2026-08-15');
});

test('extractRulingFromComment flags low_confidence when no marker anchors the sentence', () => {
  const r = br.extractRulingFromComment(row('FEAT-084', null,
    'FEAT-084 batch review: this is a genuine Bill/Aaron architectural ruling call, deferred to next session.'));
  assert.equal(r.lowConfidence, true);
  // Whole body kept verbatim (trimmed) when no marker was found to trim from.
  assert.match(r.text, /^FEAT-084 batch review/);
});

test('extractRulingFromComment returns null for an empty body', () => {
  assert.equal(br.extractRulingFromComment(row('BUG-1', null, '   ')), null);
});

// --- buildReviewEntries: supersession heuristic -------------------------

test('shared-keyword heuristic flags a real supersession-shaped pair (same item, shared topic keywords)', () => {
  const rows = [
    row('BUG-100', null, 'AARON RULING 2026-08-01: adopt GRID IMPORT metering as opt-in per district.'),
    row('BUG-100', null, 'AARON RULING 2026-08-20: REVERSES the earlier grid import metering ruling - now mandatory citywide, not opt-in.'),
  ];
  const entries = br.buildReviewEntries(rows);
  assert.equal(entries.length, 2);

  const later = entries.find((e) => e.date === '2026-08-20');
  assert.equal(later.possible_supersedes.length, 1);
  assert.equal(later.possible_supersedes[0].itemCode, 'BUG-100');
  assert.equal(later.possible_supersedes[0].date, '2026-08-01');
  assert.ok(later.possible_supersedes[0].sharedKeywords.length >= br.SHARED_KEYWORD_THRESHOLD
    || later.possible_supersedes[0].explicitMarker);

  const earlier = entries.find((e) => e.date === '2026-08-01');
  assert.equal(earlier.possible_supersedes.length, 0, 'an earlier ruling never supersedes a later one');
});

test('shared-keyword heuristic does NOT flag a false-positive-shaped pair (weak shared token only)', () => {
  const rows = [
    row('BUG-200', null, 'AARON RULING 2026-08-05: RNG seed derivation moves to per-citizen streams, no shared global seed.'),
    row('BUG-250', null, 'AARON RULING 2026-08-25: seed corpus for load tests fixed at 42 for reproducibility.'),
  ];
  const entries = br.buildReviewEntries(rows);
  const later = entries.find((e) => e.date === '2026-08-25');
  // "seed" alone (4 chars) never survives extractKeywords' length gate, and
  // there is no explicit supersession marker or shared item code, so this
  // pair must NOT be flagged despite the superficial "seed" overlap a naive
  // substring check would have caught.
  assert.equal(later.possible_supersedes.length, 0);
});

test('possible_supersedes only ever points backward in time (chronological reordering)', () => {
  // Deliberately supplied out of chronological order.
  const rows = [
    row('BUG-300', null, 'AARON RULING 2026-08-20: REVERSES the earlier borrowing capex ruling - now uncapped.'),
    row('BUG-300', null, 'AARON RULING 2026-08-01: borrowing capex is capped at the catalogue band.'),
  ];
  const entries = br.buildReviewEntries(rows);
  const earlyEntry = entries.find((e) => e.date === '2026-08-01');
  const lateEntry = entries.find((e) => e.date === '2026-08-20');
  assert.equal(earlyEntry.possible_supersedes.length, 0);
  assert.equal(lateEntry.possible_supersedes.length, 1);
  assert.equal(lateEntry.possible_supersedes[0].date, '2026-08-01');
});

test('identical repeated text is never flagged as a supersession of itself', () => {
  const body = 'AARON RULING 2026-08-01: proceed with the borrowing facility as scoped.';
  const rows = [row('BUG-400', null, body), row('BUG-400', null, body.replace('2026-08-01', '2026-08-01'))];
  // Both rows carry the exact same date and text (e.g. a duplicate posting).
  const entries = br.buildReviewEntries(rows);
  for (const e of entries) {
    assert.equal(e.possible_supersedes.length, 0);
  }
});

test('unrelated single rulings across different items produce no flags', () => {
  const rows = [
    row('BUG-500', null, 'AARON RULING 2026-08-01: rename the export button to Download.'),
    row('BUG-501', null, 'AARON RULING 2026-08-02: the tutorial popup shows once per profile.'),
    row('BUG-502', null, 'AARON RULING 2026-08-03: freight routing uses the coarse approximation for baseline one.'),
  ];
  const entries = br.buildReviewEntries(rows);
  assert.equal(entries.filter((e) => e.possible_supersedes.length > 0).length, 0);
});

test('every entry carries a topic_guess and approved:false', () => {
  const rows = [row('BUG-600', null, 'AARON RULING 2026-08-01: adopt the catalogue rescale bands as proposed.')];
  const entries = br.buildReviewEntries(rows);
  assert.equal(entries.length, 1);
  assert.equal(typeof entries[0].topic_guess, 'string');
  assert.ok(entries[0].topic_guess.length > 0);
  assert.equal(entries[0].approved, false);
});

test('printSummary runs without throwing on a mixed entry set (smoke test)', () => {
  const rows = [
    row('BUG-100', null, 'AARON RULING 2026-08-01: adopt GRID IMPORT metering as opt-in per district.'),
    row('BUG-100', null, 'AARON RULING 2026-08-20: REVERSES the earlier grid import metering ruling - now mandatory citywide.'),
  ];
  const entries = br.buildReviewEntries(rows);
  const originalLog = console.log;
  const lines = [];
  console.log = (...args) => lines.push(args.join(' '));
  try {
    br.printSummary(entries);
  } finally {
    console.log = originalLog;
  }
  assert.ok(lines.some((l) => l.includes('candidate ruling(s) extracted')));
  assert.ok(lines.some((l) => l.includes('possible-supersession flag')));
});

// --- RED proof -------------------------------------------------------------
// Prove the shared-keyword threshold assertion can actually fail: scratch-
// mutate the module (drop the threshold to an unreachable value) so a
// previously-flagged pair goes unflagged, capture the failing assertion,
// then never touch the real tools/vestige/backfill-rulings.js on disk
// (GR#24 -- only a tmpdir scratch copy is written and read).
test('RED proof: the supersession flag on a real-shaped pair can fail', () => {
  const modulePath = require.resolve('./backfill-rulings.js');
  const original = fs.readFileSync(modulePath, 'utf8');
  // Raise the threshold absurdly high AND neuter the explicit-marker path
  // so neither qualifying condition can ever fire.
  const mutated = original
    .replace('const SHARED_KEYWORD_THRESHOLD = 2;', 'const SHARED_KEYWORD_THRESHOLD = 999;')
    .replace(
      'const SUPERSESSION_MARKER_RE = /supersede|revers|replac|refin|override|overrule|no longer|changes\\b.*ruling/i;',
      'const SUPERSESSION_MARKER_RE = /$unmatchable^/;'
    );
  assert.notEqual(mutated, original, 'mutation must actually change the source');
  assert.ok(mutated.includes('999'), 'threshold mutation must have applied');

  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bfrule-test-'));
  // ruling-ingest.js is required by relative path ('./ruling-ingest.js')
  // from within backfill-rulings.js, so the scratch copy needs it alongside.
  const scratchModule = path.join(scratchDir, 'backfill-rulings-mutated.js');
  const riSource = fs.readFileSync(require.resolve('./ruling-ingest.js'), 'utf8');
  fs.writeFileSync(path.join(scratchDir, 'ruling-ingest.js'), riSource);
  fs.writeFileSync(scratchModule, mutated);

  delete require.cache[require.resolve(scratchModule)];
  const mutatedBr = require(scratchModule);

  const rows = [
    row('BUG-100', null, 'AARON RULING 2026-08-01: adopt GRID IMPORT metering as opt-in per district.'),
    row('BUG-100', null, 'AARON RULING 2026-08-20: REVERSES the earlier grid import metering ruling - now mandatory citywide, not opt-in.'),
  ];
  const entries = mutatedBr.buildReviewEntries(rows);
  const later = entries.find((e) => e.date === '2026-08-20');

  let failed = false;
  let failureMessage = '';
  try {
    assert.equal(later.possible_supersedes.length, 1);
  } catch (e) {
    failed = true;
    failureMessage = e.message;
  }
  assert.equal(failed, true, 'the mutated module must fail to flag the known supersession pair');
  assert.match(failureMessage, /0/);
});

// --- Independent destructive round r1 (Opus, FEAT-2326609713) --------------
// Two mutations survived the suite: removing the identical-text dedupe
// (`if (earlier.text === e.text) continue;`) and disabling the corpus-common
// word suppression. Both are real behaviours the review JSON depends on, so
// they get explicit coverage here.

test('an identical ruling posted twice is a duplicate, not a supersession', () => {
  const body = 'AARON RULING 2026-08-01: adopt GRID IMPORT metering as opt-in per district.';
  const entries = br.buildReviewEntries([
    row('BUG-100', null, body, new Date('2026-08-01T00:00:00Z')),
    row('BUG-100', null, body, new Date('2026-08-02T00:00:00Z')),
  ]);
  for (const e of entries) {
    assert.equal(e.possible_supersedes.length, 0,
      'identical text must never be flagged as superseding itself');
  }
});

test('corpus-common vocabulary is suppressed as supersession evidence', () => {
  // Seven rulings all sharing the same generic process vocabulary but on
  // plainly different subjects. Once a word crosses the absolute
  // COMMON_WORD_FLOOR (6 documents) it stops counting as topic evidence, so
  // no pair here may reach SHARED_KEYWORD_THRESHOLD on shared boilerplate
  // alone. Without the suppression this fixture flags heavily (the same
  // effect the author measured as 70/82 on the real corpus).
  const boiler = 'deterministic engine placeholder verdict rounding balance';
  const subjects = [
    'tourism arrival curves', 'airport noise footprint', 'refuse collection routing',
    'school catchment radius', 'harbour dredging depth', 'tram headway spacing',
    'allotment plot demand',
  ];
  const rows = subjects.map((s, i) =>
    row(`FEAT-${200 + i}`, null,
      `AARON RULING 2026-08-0${i + 1}: ${s} uses ${boiler} treatment for now.`,
      new Date(`2026-08-0${i + 1}T00:00:00Z`)));
  const entries = br.buildReviewEntries(rows);
  const flagged = entries.filter((e) => e.possible_supersedes.length > 0);
  assert.equal(flagged.length, 0,
    `shared boilerplate alone must not flag supersession (flagged: ${flagged.map((e) => e.itemCode).join(',')})`);
});
