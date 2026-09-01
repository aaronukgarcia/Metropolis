/**
 * tools/vestige/ruling-ingest.js — Aaron ruling -> Vestige `decision` node
 * payload builder, with supersession (FEAT-2326609713).
 *
 * PROBLEM: Aaron's rulings (BUG-472 halt+surface, BUG-289 option B,
 * external-provision, BUG-484 emergency-drain class, ...) are recorded on
 * BOW items today (correct, stays — see bow_comments "AARON RULING <date>:
 * <text>") and ALSO land in Vestige only as ad-hoc session-summary prose. A
 * reversed ruling (an earlier decision later overturned) sits at EQUAL trust
 * with the current one in that prose soup — nothing marks the old one dead,
 * so a later session can re-apply a stale ruling or re-interview a settled
 * question. This module is the pure, side-effect-free half of the fix:
 * given the facts of a ruling, it builds the exact Vestige `decision`-node
 * payload and the stdout instruction block that carries it out of the
 * (MCP-less) claude-bow.js process.
 *
 * TRANSPORT DECISION — reused verbatim from the sibling FEAT-2326609712
 * (tools/vestige/lesson-ingest.js; see that file's header for the full
 * writeup, GR#3 single-source-of-truth: do not re-derive the rationale
 * here). Summary: claude-bow.js is a plain Node CLI with no MCP access, so
 * it can only PROMPT the interactive session — the same session that ran
 * the command — to call `mcp__vestige__smart_ingest` with the embedded
 * JSON payload. `emitRulingInstruction()` prints a fixed, greppable block;
 * `parseRulingInstruction()` is its exact inverse.
 *
 * SUPERSESSION DESIGN NOTE: this module has NO direct access to
 * `mcp__vestige__memory` (no MCP tools are reachable from a worktree-run
 * Node process) and so cannot confirm whether that action exposes an
 * update/supersede call. Rather than assume an unconfirmed API, when
 * `supersedes` is given the payload carries EXPLICIT, self-describing
 * fields (`supersedesHint`, `topic`, `supersedeInstruction`) so the
 * RECORDING SESSION — which does hold live MCP tools — can `recall` the
 * prior node by topic, and then either update its `validUntil` (if
 * `mcp__vestige__memory` exposes an update path) or delete + forceCreate
 * per the 2026-08-02 storage policy ("corrections need delete + forceCreate
 * because smart_ingest reinforces near-duplicates >= ~0.96"). This module
 * never calls an MCP tool itself and never assumes one beyond
 * `smart_ingest`, whose shape is already confirmed by the sibling feature.
 *
 * Storage policy invariants this module enforces in the payload shape
 * (Aaron-approved 2026-08-02, restated in the FEAT-2326609713 BOW item):
 *   - a durable ruling is a `decision` node, never `fact`/`event` (current
 *     sim/project STATE never goes into Vestige from here);
 *   - exactly one canonical project tag, `metropolis`;
 *   - every payload also carries `aaron-ruling` + the mkey/item-code, so a
 *     later `recall mode=contradictions topic=<x>` can filter precisely.
 *
 * Pure module: requiring this file has NO side effects (no file writes, no
 * network, no DB, no process.exit at module scope). The CLI wrapper (if
 * any) lives elsewhere; this file only exports functions.
 */

'use strict';

const CANONICAL_TAG = 'metropolis';
const RULING_TAG = 'aaron-ruling';
const NODE_TYPE = 'decision';
const INSTRUCTION_HEADER = '=== INGEST RULING TO VESTIGE (FEAT-2326609713) ===';
const INSTRUCTION_FOOTER = '=== END ===';

// Same stopword-filtered, length-gated keyword extraction used by
// backfill-rulings.js's supersession heuristic — kept here too so a
// caller building a `supersedes` payload without a pre-computed topic
// still gets a reasonable default (see deriveTopic below). Single source
// of the list: backfill-rulings.js requires it from here rather than
// keeping a second copy (GR#3).
const STOPWORDS = new Set([
  'about', 'after', 'again', 'against', 'because', 'before', 'being', 'below',
  'between', 'could', 'during', 'either', 'every', 'first', 'further', 'given',
  'having', 'other', 'over', 'shall', 'should', 'still', 'their', 'there',
  'these', 'this', 'those', 'through', 'under', 'until', 'when', 'where',
  'which', 'while', 'with', 'would', 'aaron', 'ruling', 'rulings', 'decision',
  'session', 'today', 'earlier', 'proceed', 'approved', 'confirmed',
]);

function isNonEmptyString(v) {
  return typeof v === 'string' && v.trim().length > 0;
}

/**
 * extractKeywords(text) -> string[] (deduped, stable declaration/appearance
 * order, lowercased). Words of length < 5 or in STOPWORDS are dropped — the
 * same threshold backfill-rulings.js's false-positive fixture relies on
 * (e.g. "seed" alone, at 4 chars, never becomes a topic keyword; "seeding"
 * or "seed corpus" -> "corpus" would still be eligible).
 */
function extractKeywords(text) {
  const hay = String(text || '').toLowerCase();
  const words = hay.match(/[a-z][a-z-]{4,}/g) || [];
  const seen = new Set();
  const out = [];
  for (const w of words) {
    if (STOPWORDS.has(w)) continue;
    if (seen.has(w)) continue;
    seen.add(w);
    out.push(w);
  }
  return out;
}

/**
 * deriveTopic(text) -> a short human-readable topic guess: the first three
 * extracted keywords joined by a space, or the trimmed text itself
 * (truncated) when no keyword survives the filter.
 */
function deriveTopic(text) {
  const kws = extractKeywords(text);
  if (kws.length) return kws.slice(0, 3).join(' ');
  const t = String(text || '').trim();
  return t.length > 60 ? `${t.slice(0, 57)}...` : t;
}

/**
 * buildRulingPayload(fields) -> Vestige decision-node payload.
 *
 * fields: {
 *   code        BOW item code, e.g. "BUG-472"                (required)
 *   text        the ruling text, verbatim (trimmed on output) (required)
 *   date        ISO date string (yyyy-mm-dd); defaults to today (UTC)
 *   mkey        module/feature key, e.g. "engine.finance" — falls back to
 *               `code` when omitted (a ruling recorded against an item with
 *               no mkey still needs a specific third tag, never just the
 *               two canonical ones)
 *   supersedes  optional: either a prior ruling's Vestige memory id, or a
 *               free-text description of what this ruling reverses/refines
 *               (e.g. "BUG-472 2026-08-20 swallow-and-continue ruling").
 *               When present, three extra fields are attached (see below)
 *               so the recording session can locate and retire the old
 *               node — this module never guesses an update API.
 *   topic       optional explicit topic string for the supersedes search;
 *               derived from `text` via deriveTopic() when omitted.
 * }
 *
 * Returns { content, nodeType, tags, source, validFrom, ...supersession }.
 * Throws a plain Error (not a registry error — see the module header of
 * lesson-ingest.js for why the sibling reserves registry errors for a
 * caller-facing CLI flag validation path, which this module does not own)
 * on any missing required field or a blank ruling text.
 */
function buildRulingPayload(fields) {
  const f = fields || {};
  const required = ['code', 'text'];
  for (const key of required) {
    if (!isNonEmptyString(f[key])) {
      throw new Error(`buildRulingPayload: missing required field "${key}"`);
    }
  }

  const date = isNonEmptyString(f.date) ? f.date : new Date().toISOString().slice(0, 10);
  const mkeyOrCode = isNonEmptyString(f.mkey) ? f.mkey : f.code;
  const content = f.text.trim();
  const source = `${f.code} ruling ${date}`;

  const payload = {
    content,
    nodeType: NODE_TYPE,
    tags: [CANONICAL_TAG, RULING_TAG, mkeyOrCode],
    source,
    validFrom: date,
  };

  if (isNonEmptyString(f.supersedes)) {
    const topic = isNonEmptyString(f.topic) ? f.topic.trim() : deriveTopic(content);
    payload.supersedesHint = f.supersedes.trim();
    payload.topic = topic;
    payload.supersedeInstruction =
      `Before ingesting this node, run mcp__vestige__recall (mode=contradictions or a plain ` +
      `topic search) for topic "${topic}" to locate the prior decision node described by ` +
      `supersedesHint. If found, set its validUntil to "${date}" via mcp__vestige__memory's ` +
      `update path if one exists; otherwise delete it and forceCreate this node instead of a ` +
      `plain smart_ingest call, per the 2026-08-02 storage policy (smart_ingest reinforces ` +
      `near-duplicates >= ~0.96 instead of correcting them). Then ingest THIS payload as a new ` +
      `decision node regardless of whether the old node was found.`;
  }

  return payload;
}

/**
 * emitRulingInstruction(payload) -> fixed-format stdout block (string).
 *
 * The recording session greps for the header/footer pair and executes the
 * embedded JSON via mcp__vestige__smart_ingest(payload) (plus the
 * supersession dance described in supersedeInstruction, when present).
 * Kept as a plain JSON.stringify so the block round-trips through
 * JSON.parse exactly, mirroring lesson-ingest.js's emitIngestInstruction.
 */
function emitRulingInstruction(payload) {
  if (!payload || typeof payload !== 'object') {
    throw new Error('emitRulingInstruction: payload must be an object');
  }
  const json = JSON.stringify(payload, null, 2);
  return `${INSTRUCTION_HEADER}\n${json}\n${INSTRUCTION_FOOTER}`;
}

/**
 * parseRulingInstruction(block) -> payload object, or null if the block
 * does not match the fixed header/footer format. Inverse of
 * emitRulingInstruction — used by tests and by any tooling that wants to
 * recover the payload from captured stdout.
 */
function parseRulingInstruction(block) {
  if (typeof block !== 'string') return null;
  const start = block.indexOf(INSTRUCTION_HEADER);
  const end = block.indexOf(INSTRUCTION_FOOTER);
  if (start === -1 || end === -1 || end <= start) return null;
  const jsonText = block.slice(start + INSTRUCTION_HEADER.length, end).trim();
  try {
    return JSON.parse(jsonText);
  } catch (_) {
    return null;
  }
}

module.exports = {
  CANONICAL_TAG,
  RULING_TAG,
  NODE_TYPE,
  INSTRUCTION_HEADER,
  INSTRUCTION_FOOTER,
  STOPWORDS,
  extractKeywords,
  deriveTopic,
  buildRulingPayload,
  emitRulingInstruction,
  parseRulingInstruction,
};
