/**
 * tools/vestige/backfill-rulings.js — one-off backfill of recorded "AARON
 * RULING" bow_comments into a reviewable JSON artefact, with a probable-
 * supersession heuristic (FEAT-2326609713).
 *
 * PROBLEM: bow_comments already holds ~40-80 rows recording a ruling as
 * plain text ("AARON RULING <date>: <text>", or a close variant — see the
 * RULING_PATTERNS below, tuned against the real rows read 2026-09-01 via
 * `SELECT ... FROM bow_comments WHERE body REGEXP '...'`). Some of those
 * rulings supersede an earlier one on the same subject (e.g. BUG-472 got a
 * "HALT + SURFACE" ruling that a later same-day comment says "replaces the
 * earlier proposal"; BUG-477's capex-rescale ruling was itself restated
 * more precisely later). Ingesting all of them as equal-trust decision
 * nodes with no supersession marker would leave a reversed ruling sitting
 * at the same trust as its replacement forever.
 *
 * This script is READ-ONLY end to end: it reads bow_comments (joined to
 * bow_items for code/mkey/title) via a SELECT, extracts (item code, date,
 * ruling text) per candidate row, groups by a simple shared-keyword +
 * same-item heuristic to flag PROBABLE supersession pairs, and writes
 * tools/vestige/backfill-rulings.review.json for the lead to review before
 * anyone calls buildRulingPayload/emitRulingInstruction (in
 * tools/vestige/ruling-ingest.js) on any of them. It NEVER calls
 * smart_ingest and NEVER writes to the live Vestige store, the metro DB,
 * or any BOW item — the only write in this whole file is the local review
 * JSON.
 *
 * Usage:
 *   node tools/vestige/backfill-rulings.js [--out <path>] [--limit N]
 *
 * Requires the metro MariaDB (same connection as claude-bow.js, via the
 * shared claude-db.js helper) and mysql2 (lives in the MAIN checkout's
 * node_modules — set NODE_PATH=E:\git\Metropolis\node_modules when running
 * this from a worktree that has no node_modules of its own).
 */

'use strict';

const fs = require('fs');
const path = require('path');

const { extractKeywords, deriveTopic } = require('./ruling-ingest.js');

const DEFAULT_OUT = path.join(__dirname, 'backfill-rulings.review.json');

// Tuned 2026-09-01 against the real bow_comments rows (78 REGEXP hits) --
// deliberately broad per the BOW item's own instruction ("/AARON RULING/i
// or /Aaron ruling/i or /Aaron('s)? decision/i"); a broad SQL filter is
// cheap here because every hit still goes through isLikelyRuling() below
// before being treated as a ruling row, and every output row is reviewed
// by a human before anything is ingested.
const SQL_FILTER = "AARON RULING|Aaron ruling|Aaron'?s? decision|Aaron ruled";

// A tighter, in-process re-check: a comment merely mentioning "Aaron" and
// "ruling" in unrelated prose (e.g. "a genuine Bill/Aaron architectural
// ruling call") should not be extracted as if the comment WERE the ruling
// text -- only comments where an AARON RULING(S) marker actually anchors
// the sentence are extracted with a trimmed prefix; anything else is kept
// but flagged low_confidence so the lead can eyeball it.
const RULING_MARKER_RE = /AARON RULINGS?\b\s*(\d{4}-\d{2}-\d{2})?\s*(\([^)]*\))?\s*:?\s*-?\s*/i;
const DATE_NEAR_MARKER_RE = /AARON RULINGS?\b[^0-9]{0,20}(\d{4}-\d{2}-\d{2})/i;

// Explicit supersession/reversal language -- when present on a LATER
// ruling against the SAME item, that alone is enough to flag the pair
// even if the shared-keyword count falls short (a short "REVERSES the
// earlier X ruling" sentence may not repeat many of X's own keywords).
const SUPERSESSION_MARKER_RE = /supersede|revers|replac|refin|override|overrule|no longer|changes\b.*ruling/i;

const SHARED_KEYWORD_THRESHOLD = 4;

function isNonEmptyString(v) {
  return typeof v === 'string' && v.trim().length > 0;
}

/**
 * extractRulingFromComment(row) -> { itemCode, mkey, date, text, lowConfidence }
 * or null if the row does not look like a ruling at all (should not happen
 * given the SQL filter already required one of the marker phrases, but the
 * check is repeated here so buildReviewEntries stays testable against
 * hand-built fixtures that skip the SQL step entirely).
 *
 * row: { code, mkey, body, createdAt }
 */
function extractRulingFromComment(row) {
  const body = String((row && row.body) || '');
  if (!body.trim()) return null;

  const marker = RULING_MARKER_RE.exec(body);
  const lowConfidence = !marker;

  // Date: prefer one printed right after the marker ("AARON RULING
  // 2026-09-01: ..."); fall back to any yyyy-mm-dd in the body; fall back
  // to the comment's own created_at timestamp (always present).
  let date = null;
  const dateNearMarker = DATE_NEAR_MARKER_RE.exec(body);
  if (dateNearMarker) {
    date = dateNearMarker[1];
  } else {
    const anyDate = /(\d{4}-\d{2}-\d{2})/.exec(body);
    if (anyDate) date = anyDate[1];
  }
  if (!date && row.createdAt) {
    const d = new Date(row.createdAt);
    if (!Number.isNaN(d.getTime())) date = d.toISOString().slice(0, 10);
  }

  // Text: everything after the marker prefix when found, else the whole
  // body (low-confidence case) trimmed.
  let text = body.trim();
  if (marker) {
    text = body.slice(marker.index + marker[0].length).trim();
  }
  if (!text) text = body.trim();

  return {
    itemCode: row.code || null,
    mkey: row.mkey || null,
    date: date || null,
    text,
    lowConfidence,
  };
}

/**
 * buildReviewEntries(rows) -> array of {
 *   itemCode, date, text, topic_guess, possible_supersedes: [ref...],
 *   approved: false
 * }
 *
 * rows: array of { code, mkey, body, createdAt } (raw comment rows, one per
 * candidate ruling comment; NOT deduped/classed the way backfill-lessons.js
 * dedups by class -- each ruling is a distinct decision worth its own node,
 * unlike the many-verdicts-one-lesson-class shape of the sibling script).
 *
 * Pure function (no I/O) so it is directly unit-testable against
 * hand-built fixtures.
 */
function buildReviewEntries(rows) {
  const extracted = [];
  for (const row of rows || []) {
    const r = extractRulingFromComment(row);
    if (r) extracted.push(r);
  }

  // Stable chronological order (undated entries sort last, original order
  // preserved among themselves) so "possible_supersedes" always points
  // BACKWARD in time -- a later ruling can supersede an earlier one, never
  // the reverse.
  const withIndex = extracted.map((e, i) => ({ ...e, _i: i }));
  withIndex.sort((a, b) => {
    const ad = a.date || '9999-99-99';
    const bd = b.date || '9999-99-99';
    if (ad !== bd) return ad < bd ? -1 : 1;
    return a._i - b._i;
  });

  const keywordSets = withIndex.map((e) => new Set(extractKeywords(e.text)));

  // Corpus-common-word suppression: a word repeated across MANY unrelated
  // rulings (project-wide process/domain vocabulary -- "build", "engine",
  // "placeholder", "verdict", "deterministic", ...) is not evidence of a
  // shared TOPIC and would otherwise flood possible_supersedes with noise
  // (measured 2026-09-01: unfiltered, 70/82 real rows got flagged). Only an
  // ABSOLUTE occurrence count is used (never a fraction of entries.length)
  // so small hand-built test fixtures are never affected by this filter --
  // no word can hit the absolute floor without a real, large corpus.
  const docFreq = new Map();
  for (const set of keywordSets) {
    for (const k of set) docFreq.set(k, (docFreq.get(k) || 0) + 1);
  }
  const COMMON_WORD_FLOOR = 6;
  const isCorpusCommon = (word) => (docFreq.get(word) || 0) >= COMMON_WORD_FLOOR;

  const entries = withIndex.map((e, idx) => {
    const possible = [];
    for (let j = 0; j < idx; j++) {
      const earlier = withIndex[j];
      if (earlier.text === e.text) continue; // identical text isn't a supersession, it's a duplicate post
      const shared = [...keywordSets[idx]].filter((k) => keywordSets[j].has(k) && !isCorpusCommon(k));
      const sameItem = earlier.itemCode && e.itemCode && earlier.itemCode === e.itemCode;
      const explicitMarker = SUPERSESSION_MARKER_RE.test(e.text);
      const qualifies = shared.length >= SHARED_KEYWORD_THRESHOLD || (sameItem && explicitMarker);
      if (qualifies) {
        possible.push({
          itemCode: earlier.itemCode,
          date: earlier.date,
          sharedKeywords: shared,
          sameItem: !!sameItem,
          explicitMarker,
        });
      }
    }
    return {
      itemCode: e.itemCode,
      date: e.date,
      text: e.text,
      topic_guess: deriveTopic(e.text),
      low_confidence: e.lowConfidence,
      possible_supersedes: possible,
      approved: false,
    };
  });

  return entries;
}

function printSummary(entries) {
  const flagged = entries.filter((e) => e.possible_supersedes.length > 0);
  console.log(`backfill-rulings: ${entries.length} candidate ruling(s) extracted.`);
  console.log(`backfill-rulings: ${flagged.length} carry a possible-supersession flag.`);
  const lowConf = entries.filter((e) => e.low_confidence).length;
  if (lowConf) console.log(`backfill-rulings: ${lowConf} low-confidence extraction(s) (no anchored "AARON RULING" marker found -- review the raw text).`);
  if (flagged.length) {
    console.log('backfill-rulings: sample flagged pairs:');
    for (const e of flagged.slice(0, 5)) {
      const prior = e.possible_supersedes[e.possible_supersedes.length - 1];
      console.log(`  - ${e.itemCode || '?'} (${e.date || '?'}) possibly supersedes ${prior.itemCode || '?'} (${prior.date || '?'}) [shared: ${prior.sharedKeywords.join(', ') || '(marker only)'}]`);
    }
  }
}

async function fetchRows(limit) {
  // Lazily required so `require('./backfill-rulings.js')` from a test
  // never needs mysql2/the DB to be present (buildReviewEntries etc. are
  // pure and directly testable against hand-built row fixtures).
  const { connect } = require(path.join('..', '..', 'claude-db.js'));
  const db = await connect({ connectTimeout: 8000 });
  try {
    const sql = `
      SELECT c.body, c.created_at, i.code, i.mkey, i.title
      FROM bow_comments c
      JOIN bow_items i ON i.guid = c.item_guid
      WHERE c.body REGEXP ?
      ORDER BY c.created_at ASC
      ${limit ? 'LIMIT ?' : ''}
    `;
    const params = limit ? [SQL_FILTER, limit] : [SQL_FILTER];
    const [rawRows] = await db.execute(sql, params);
    return rawRows.map((r) => ({
      code: r.code,
      mkey: r.mkey,
      title: r.title,
      body: r.body,
      createdAt: r.created_at,
    }));
  } finally {
    await db.end();
  }
}

async function main(argv) {
  const args = argv.slice(2);
  let outPath = DEFAULT_OUT;
  let limit = null;
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--out' && args[i + 1]) { outPath = path.resolve(args[i + 1]); i++; }
    else if (args[i] === '--limit' && args[i + 1]) { limit = Number(args[i + 1]); i++; }
  }

  console.log('backfill-rulings: reading bow_comments (READ-ONLY) ...');
  const rows = await fetchRows(limit);
  console.log(`backfill-rulings: ${rows.length} candidate comment row(s) read.`);

  const entries = buildReviewEntries(rows);
  printSummary(entries);

  const output = {
    generatedAt: new Date().toISOString(),
    sourceRowCount: rows.length,
    entryCount: entries.length,
    flaggedCount: entries.filter((e) => e.possible_supersedes.length > 0).length,
    note: 'Reviewable proposal only -- nothing here has been ingested into Vestige. ' +
          'Confirm/clear each possible_supersedes entry and set approved:true per row ' +
          'before the lead runs the ingest pass via tools/vestige/ruling-ingest.js.',
    entries,
  };
  if (!isNonEmptyString(output.generatedAt)) throw new Error('backfill-rulings: internal error building output');
  fs.writeFileSync(outPath, JSON.stringify(output, null, 2) + '\n', 'utf8');
  console.log(`backfill-rulings: wrote ${entries.length} entries to ${outPath}`);
}

module.exports = {
  RULING_MARKER_RE,
  DATE_NEAR_MARKER_RE,
  SUPERSESSION_MARKER_RE,
  SHARED_KEYWORD_THRESHOLD,
  extractRulingFromComment,
  buildReviewEntries,
  printSummary,
  fetchRows,
  main,
};

if (require.main === module) {
  main(process.argv).catch((err) => {
    console.error('backfill-rulings: FAILED:', err && err.message ? err.message : err);
    process.exitCode = 1;
  });
}
