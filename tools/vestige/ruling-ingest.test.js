/**
 * tools/vestige/ruling-ingest.test.js — tests for ruling-ingest.js
 * (FEAT-2326609713). node:test, discovered by root `node --test`.
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('fs');
const os = require('os');
const path = require('path');

const ri = require('./ruling-ingest.js');

test('buildRulingPayload produces the documented decision-node shape', () => {
  const payload = ri.buildRulingPayload({
    code: 'BUG-472',
    text: '  HALT + SURFACE. Pause the sim on a journal append failure.  ',
    date: '2026-09-01',
    mkey: 'engine.persist',
  });

  assert.equal(payload.nodeType, 'decision');
  assert.equal(payload.content, 'HALT + SURFACE. Pause the sim on a journal append failure.');
  assert.equal(payload.validFrom, '2026-09-01');
  assert.equal(payload.source, 'BUG-472 ruling 2026-09-01');
  assert.deepEqual(payload.tags, ['metropolis', 'aaron-ruling', 'engine.persist']);
  // No supersession requested -> none of the supersession fields present.
  assert.equal('supersedesHint' in payload, false);
  assert.equal('topic' in payload, false);
  assert.equal('supersedeInstruction' in payload, false);
});

test('buildRulingPayload falls back to the item code as the third tag when mkey is omitted', () => {
  const payload = ri.buildRulingPayload({ code: 'FEAT-057', text: 'Proceed with the rescale.' });
  assert.deepEqual(payload.tags, ['metropolis', 'aaron-ruling', 'FEAT-057']);
});

test('buildRulingPayload defaults date to today (UTC yyyy-mm-dd) when omitted', () => {
  const payload = ri.buildRulingPayload({ code: 'BUG-1', text: 'x' });
  assert.match(payload.validFrom, /^\d{4}-\d{2}-\d{2}$/);
});

test('buildRulingPayload throws on missing required fields', () => {
  assert.throws(() => ri.buildRulingPayload({ code: 'BUG-2' }), /missing required field "text"/);
  assert.throws(() => ri.buildRulingPayload({ text: 'x' }), /missing required field "code"/);
});

test('buildRulingPayload throws on a blank ruling text (whitespace-only)', () => {
  assert.throws(() => ri.buildRulingPayload({ code: 'BUG-3', text: '   ' }), /missing required field "text"/);
});

test('buildRulingPayload with supersedes carries explicit supersedesHint/topic/instruction fields', () => {
  const payload = ri.buildRulingPayload({
    code: 'BUG-472',
    text: 'HALT + SURFACE on journal append failure, replacing the earlier swallow-and-continue path.',
    date: '2026-09-01',
    mkey: 'engine.persist',
    supersedes: 'MET-E021 swallow-and-continue ruling',
  });

  assert.equal(payload.supersedesHint, 'MET-E021 swallow-and-continue ruling');
  assert.equal(typeof payload.topic, 'string');
  assert.ok(payload.topic.length > 0);
  assert.match(payload.supersedeInstruction, /mcp__vestige__recall/);
  assert.match(payload.supersedeInstruction, /validUntil to "2026-09-01"/);
  assert.match(payload.supersedeInstruction, /forceCreate/);
  // The instruction never assumes an unconfirmed update API is the only
  // path -- it documents the delete+forceCreate fallback too.
  assert.match(payload.supersedeInstruction, /if one exists/);
});

test('buildRulingPayload honours an explicit topic over the derived one', () => {
  const payload = ri.buildRulingPayload({
    code: 'BUG-1', text: 'some long ruling text about grid import metering bands',
    supersedes: 'prior ruling', topic: 'grid import metering',
  });
  assert.equal(payload.topic, 'grid import metering');
});

test('buildRulingPayload derives a topic from keywords when none is given', () => {
  const payload = ri.buildRulingPayload({
    code: 'BUG-1', text: 'RNG seed derivation moves to per-citizen streams, no shared global seed.',
    supersedes: 'prior seed ruling',
  });
  // deriveTopic keeps only length>=5, non-stopword keywords -- "streams"/
  // "derivation"/"citizen" are eligible, "seed" (4 chars) is not.
  assert.ok(payload.topic.length > 0);
  assert.doesNotMatch(payload.topic, /\bseed\b/);
});

test('extractKeywords drops short words and stopwords, dedupes, keeps order', () => {
  // "grid" (4 chars) is dropped by the length gate; "ruling"/"approved"/
  // "today" are dropped by STOPWORDS; "import" and "metering" survive,
  // each exactly once despite "import" appearing twice.
  const kws = ri.extractKeywords('The grid import metering grid import ruling was approved today');
  assert.deepEqual(kws, ['import', 'metering']);
});

test('extractKeywords keeps eligible words exactly once each, in first-seen order', () => {
  const kws = ri.extractKeywords('import import metering corpus');
  assert.deepEqual(kws, ['import', 'metering', 'corpus']);
});

test('emitRulingInstruction / parseRulingInstruction round-trip through JSON.parse', () => {
  const payload = ri.buildRulingPayload({
    code: 'BUG-289', text: 'OPTION B APPROVED - whole-month rollback.', date: '2026-09-01',
  });
  const block = ri.emitRulingInstruction(payload);
  assert.match(block, /^=== INGEST RULING TO VESTIGE \(FEAT-2326609713\) ===/);
  assert.match(block, /=== END ===$/);

  const headerEnd = block.indexOf('\n') + 1;
  const footerStart = block.lastIndexOf('=== END ===');
  const body = block.slice(headerEnd, footerStart).trim();
  const parsedDirect = JSON.parse(body);
  assert.deepEqual(parsedDirect, payload);

  const parsedViaHelper = ri.parseRulingInstruction(block);
  assert.deepEqual(parsedViaHelper, payload);
});

test('parseRulingInstruction returns null for a block missing the footer', () => {
  assert.equal(ri.parseRulingInstruction('=== INGEST RULING TO VESTIGE (FEAT-2326609713) ===\n{}'), null);
});

test('parseRulingInstruction returns null for non-string input', () => {
  assert.equal(ri.parseRulingInstruction(undefined), null);
  assert.equal(ri.parseRulingInstruction(42), null);
});

test('emitRulingInstruction throws on a non-object payload', () => {
  assert.throws(() => ri.emitRulingInstruction(null), /payload must be an object/);
  assert.throws(() => ri.emitRulingInstruction('x'), /payload must be an object/);
});

// --- RED proof -------------------------------------------------------------
// Prove the payload-shape test can actually fail: scratch-mutate the module
// (invert the nodeType, mirroring the sibling FEAT-2326609712's RED proof)
// so the shape assertion goes RED, capture the failing assertion text, then
// never touch the real tools/vestige/ruling-ingest.js on disk (GR#24 --
// never mutate the real tree for a can-fail proof; only a tmpdir scratch
// copy is written and read).
test('RED proof: buildRulingPayload nodeType assertion can fail', () => {
  const modulePath = require.resolve('./ruling-ingest.js');
  const original = fs.readFileSync(modulePath, 'utf8');
  const mutated = original.replace("const NODE_TYPE = 'decision';", "const NODE_TYPE = 'fact';");
  assert.notEqual(mutated, original, 'mutation must actually change the source');

  const scratchDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ruling-ingest-red-'));
  const scratchModule = path.join(scratchDir, 'ruling-ingest-mutated.js');
  fs.writeFileSync(scratchModule, mutated);

  delete require.cache[require.resolve(scratchModule)];
  const mutatedRi = require(scratchModule);
  const payload = mutatedRi.buildRulingPayload({ code: 'BUG-RED', text: 'red-proof ruling text' });

  let failed = false;
  let failureMessage = '';
  try {
    assert.equal(payload.nodeType, 'decision');
  } catch (e) {
    failed = true;
    failureMessage = e.message;
  }
  assert.equal(failed, true, 'the mutated module must fail the nodeType assertion');
  assert.match(failureMessage, /'fact'/);
  assert.match(failureMessage, /'decision'/);
});

// --- Independent destructive round r1 (Opus, FEAT-2326609713) --------------
// Regressions added by the attacker. Motivation: a 12-mutation sweep against
// a validated-green control found that mutating buildRulingPayload to
// `f.text.trim().slice(0, 80)` -- i.e. silently TRUNCATING the ruling -- left
// the whole suite GREEN, because every pre-existing fixture's text is short.
// A truncated ruling is a corrupted ruling (the content IS the decision), so
// the verbatim guarantee gets an explicit long-text regression here.

test('buildRulingPayload keeps long ruling text VERBATIM (no truncation)', () => {
  // >2000 chars, the length a real interview-transcript ruling reaches.
  const long = 'AARON RULING \u2014 ' +
    'the amortised capex band must be recomputed per tick with \u00a3 figures and em\u2014dashes. '.repeat(40);
  const payload = ri.buildRulingPayload({ code: 'BUG-477', text: long, date: '2026-09-01' });
  assert.equal(payload.content.length, long.trim().length);
  assert.equal(payload.content, long.trim());
  assert.ok(payload.content.length > 2000, 'fixture must actually exceed 2000 chars');
  // And it must survive the transport unchanged.
  assert.equal(ri.parseRulingInstruction(ri.emitRulingInstruction(payload)).content, long.trim());
});

test('emit/parse round-trip is lossless for JSON-hostile and unicode ruling text', () => {
  const BS = String.fromCharCode(92);
  const nasty = [
    'Use {externalCover{ for fire } but not }} for police \u2014 see \u00a33.2m capex band.',
    'Ruling: never run `rm -rf $(pwd)` \u2014 use $(git rev-parse) instead; cost \u00a31,234.56 \u201cquoted\u201d.',
    'Ruling \u2014 \u00a3250/month, caf\u00e9 na\u00efve r\u00e9sum\u00e9, \u65e5\u672c\u8a9e, tab\there\nnewline "quoted" ' + BS + 'backslash' + BS,
    'Ruling mentions === INGEST RULING TO VESTIGE (FEAT-2326609713) === in its own prose.',
  ];
  for (const text of nasty) {
    const payload = ri.buildRulingPayload({ code: 'BUG-X', text, date: '2026-09-01' });
    const parsed = ri.parseRulingInstruction(ri.emitRulingInstruction(payload));
    assert.deepEqual(parsed, payload, `round-trip must be lossless for: ${JSON.stringify(text).slice(0, 60)}`);
    assert.equal(parsed.content, text.trim());
  }
});

test('a ruling containing the footer sentinel fails CLOSED (null), never silently corrupted', () => {
  // The transport is sentinel-delimited, so a ruling whose own text contains
  // "=== END ===" produces a block that cannot be parsed back. The contract
  // that matters is that this is a CLEAN failure (null) and never a parse
  // that succeeds with different content -- a silently truncated ruling node
  // in Vestige would be worse than no node at all.
  const text = 'Aaron ruling: use the halt path.\n=== END ===\nthen continue with the rescale.';
  const payload = ri.buildRulingPayload({ code: 'BUG-X', text, date: '2026-09-01' });
  const parsed = ri.parseRulingInstruction(ri.emitRulingInstruction(payload));
  assert.equal(parsed, null, 'must fail closed rather than return partial content');
});
