/**
 * tools/plan/add-error.js — GR#7 error-registry allocation + registration
 * tool (BUG-273).
 *
 * The /new-error skill (.claude/commands/new-error.md) told devs to run
 * `node tools/plan/add-error.js --layer ... --msg ...`, but the script
 * never existed, so every module registered MET-* codes by hand-editing
 * data/errors.json. With several worktrees active in parallel that is a
 * structural collision hazard: two devs allocate the same free-looking
 * range at the same time because there is no single place that hands out
 * the "next" block. This tool is that single place.
 *
 * Three subcommands:
 *
 *   claim-range <mkey> [--size N] [--layer X] [--dry-run]
 *     Scans data/errors.json for every existing code AND every existing
 *     reservation (ranges.reserved), on the target layer letter, and
 *     allocates the lowest free N-wide (default 100) block aligned to N.
 *     Records the allocation as a new entry in ranges.reserved (that
 *     object IS the reservation ledger this project already uses — see
 *     the ~80 "reserved for ..." entries already there; this tool follows
 *     that exact shape rather than inventing a parallel one).
 *
 *   add <CODE> --mkey <mkey> --name <ErrName> --template "<msg>"
 *       [--remedy "..."] [--severity fatal|error|warn] [--dry-run]
 *     Validates the code format, that it falls inside a range reserved
 *     for --mkey, and that it is not already taken, then inserts a new
 *     entry into data/errors.json's "codes" object via a targeted text
 *     splice (never a full JSON.stringify of the whole file) so the rest
 *     of the 3900+ line file is untouched byte-for-byte.
 *
 *   check
 *     Full-registry lint: duplicate codes, codes with no owning
 *     reservation, overlapping reservations. Exits 1 on any violation —
 *     this is the CI-able form. Also compares the local file's
 *     reservations against origin/main's and WARNS (never fails) if the
 *     local worktree looks stale, so a dev rebases before claiming.
 *
 * Deliberately simple/testable pure functions + a thin CLI wrapper that
 * owns process.exit, following tools/plan/icd-lint.js's convention.
 */

'use strict';

const fs = require('fs');
const path = require('path');
const os = require('os');
const { execFileSync } = require('child_process');

const DEFAULT_REPO_DIR = path.resolve(__dirname, '..', '..');
const DEFAULT_ERRORS_PATH = path.join(DEFAULT_REPO_DIR, 'data', 'errors.json');

const CODE_RE = /^MET-([A-Z])(\d{3,4})$/;
const RANGE_KEY_RE = /^([A-Z])(\d{3,4})-([A-Z])(\d{3,4})$/;
const VALID_SEVERITIES = new Set(['fatal', 'error', 'warn']);

// mkey-prefix -> default layer letter, inferred by reading ranges.layers /
// ranges.reserved's own prose in data/errors.json (BUG-234's E/U overflow
// history): E and U are fully claimed, so new engine/ui ranges land in the
// overflow letters G/V respectively. feat.* is ambiguous (some feat.* live
// in engine's G layer, one (feat.devmode) lives in ui's U layer) so it has
// no default — callers must pass --layer explicitly for feat.* mkeys.
const LAYER_BY_PREFIX = [
  ['foundation.', 'F'],
  ['engine.', 'G'],
  ['ui.', 'V'],
  ['harness.', 'H'],
  ['protocol', 'P'],
  ['tool.', 'T'],
  ['tooling.', 'T'],
];

function inferLayer(mkey) {
  for (const [prefix, letter] of LAYER_BY_PREFIX) {
    if (mkey === prefix || mkey.startsWith(prefix)) return letter;
  }
  return null;
}

function padNum(n, width) {
  const s = String(n);
  return s.length >= width ? s : '0'.repeat(width - s.length) + s;
}

// A number under 1000 is always rendered 3-digit-padded; 1000+ renders at
// its natural width (4 digits) -- this matches every existing entry in the
// file (e.g. "G000-G099" vs "G1000-G1099").
function formatCode(letter, n) {
  return `${letter}${n < 1000 ? padNum(n, 3) : padNum(n, 4)}`;
}

function loadRegistry(errorsPath) {
  let raw;
  try {
    raw = fs.readFileSync(errorsPath, 'utf8');
  } catch (cause) {
    throw new Error(`could not read ${errorsPath}: ${cause.message}`);
  }
  let data;
  try {
    data = JSON.parse(raw);
  } catch (cause) {
    throw new Error(`${errorsPath} is not valid JSON: ${cause.message}`);
  }
  if (!data || typeof data !== 'object' || !data.codes || !data.ranges) {
    throw new Error(`${errorsPath} does not have the expected {ranges, codes} shape`);
  }
  return { raw, data };
}

// Parse every reservation key ("E000-E099", "G1000-G1099") into a
// structured record. Malformed keys are skipped (reported by `check`).
function parseReservations(reserved) {
  const out = [];
  for (const [key, description] of Object.entries(reserved || {})) {
    const m = RANGE_KEY_RE.exec(key);
    if (!m) {
      out.push({ key, description, malformed: true });
      continue;
    }
    const [, letterA, startStr, letterB, endStr] = m;
    if (letterA !== letterB) {
      out.push({ key, description, malformed: true });
      continue;
    }
    out.push({
      key,
      description,
      letter: letterA,
      start: parseInt(startStr, 10),
      end: parseInt(endStr, 10),
    });
  }
  return out;
}

function parseCodes(codes) {
  const out = [];
  for (const code of Object.keys(codes)) {
    const m = CODE_RE.exec(code);
    if (!m) {
      out.push({ code, malformed: true });
      continue;
    }
    out.push({ code, letter: m[1], num: parseInt(m[2], 10) });
  }
  return out;
}

/**
 * Allocate the lowest free `size`-wide, `size`-aligned block on `letter`
 * that overlaps neither an existing reservation nor an existing raw code
 * on that letter. Pure function — never touches disk.
 */
function allocateRange(data, letter, size) {
  const reservations = parseReservations(data.ranges.reserved).filter(
    (r) => !r.malformed && r.letter === letter
  );
  const codes = parseCodes(data.codes).filter((c) => !c.malformed && c.letter === letter);

  const occupied = (start, end) => {
    for (const r of reservations) {
      if (start <= r.end && end >= r.start) return true;
    }
    for (const c of codes) {
      if (c.num >= start && c.num <= end) return true;
    }
    return false;
  };

  let start = 0;
  // Hard ceiling: the code format caps at 4 digits (^MET-[A-Z]\d{3,4}$),
  // so there is nowhere to allocate past 9999.
  while (start <= 9999) {
    const end = start + size - 1;
    if (end > 9999) break;
    if (!occupied(start, end)) {
      return { start, end };
    }
    start += size;
  }
  return null;
}

function claimRange({ errorsPath, mkey, size, layerOverride, dryRun, actor }) {
  if (!mkey || typeof mkey !== 'string' || !mkey.trim()) {
    throw new Error('claim-range requires a non-empty <mkey>');
  }
  if (!Number.isInteger(size) || size <= 0) {
    throw new Error(`claim-range requires a positive integer size, got ${size}`);
  }
  const letter = layerOverride || inferLayer(mkey);
  if (!letter) {
    throw new Error(
      `could not infer a layer letter for mkey "${mkey}" (feat.* and unrecognised prefixes ` +
        'are ambiguous) -- pass --layer <LETTER> explicitly'
    );
  }
  if (!/^[A-Z]$/.test(letter)) {
    throw new Error(`--layer must be a single uppercase letter, got "${letter}"`);
  }

  const { raw, data } = loadRegistry(errorsPath);
  const block = allocateRange(data, letter, size);
  if (!block) {
    throw new Error(`no free ${size}-wide block remains on layer "${letter}" (0-9999 exhausted)`);
  }

  const startCode = formatCode(letter, block.start);
  const endCode = formatCode(letter, block.end);
  const rangeKey = `${startCode}-${endCode}`;
  const description =
    `reserved for ${mkey} (auto-claimed via tools/plan/add-error.js claim-range on ` +
    `${new Date().toISOString().slice(0, 10)} by ${actor || 'unknown'} -- BUG-273: lowest free ` +
    `${size}-wide block found after scanning existing codes and reservations on layer "${letter}")`;

  if (dryRun) {
    return { rangeKey, description, letter, block, wrote: false };
  }

  if (Object.prototype.hasOwnProperty.call(data.ranges.reserved, rangeKey)) {
    throw new Error(`internal error: computed range ${rangeKey} is already reserved`);
  }

  const newRaw = insertReservation(raw, rangeKey, description);
  // Round-trip-validate before committing to disk.
  const reparsed = JSON.parse(newRaw);
  if (!reparsed.ranges.reserved[rangeKey]) {
    throw new Error('internal error: reservation insertion did not round-trip');
  }
  atomicWrite(errorsPath, newRaw);

  return { rangeKey, description, letter, block, wrote: true };
}

// Insert a new "KEY": "description" entry into the ranges.reserved object,
// as the last entry, via a targeted text splice -- never re-serialising
// the whole file. Assumes the existing two-space/four-space/six-space
// indentation convention visible throughout data/errors.json.
function insertReservation(raw, key, description) {
  const marker = '"reserved": {';
  const markerIdx = raw.indexOf(marker);
  if (markerIdx === -1) {
    throw new Error('could not find "reserved": { in data/errors.json -- unexpected file shape');
  }
  // Find the matching close of this object: the first line consisting of
  // just `    },` or `    }` at the "reserved" object's own indentation
  // (4 spaces), scanning forward from the marker.
  const afterMarker = raw.slice(markerIdx + marker.length);
  const closeRe = /\n( {4})\}(,)?\r?\n/;
  const closeMatch = closeRe.exec(afterMarker);
  if (!closeMatch) {
    throw new Error('could not find the closing brace of "reserved" -- unexpected file shape');
  }
  const closeIdx = markerIdx + marker.length + closeMatch.index;

  // Everything strictly between markerIdx+marker.length and closeIdx is the
  // body of the reserved object (one or more "KEY": "value" entries).
  const bodyStart = markerIdx + marker.length;
  const body = raw.slice(bodyStart, closeIdx);
  const bodyEndsWithEntry = /"[^"]*"\s*$/.test(body.replace(/\r?\n\s*$/, ''));

  const escapedDescription = JSON.stringify(description);
  const newEntryLine = `\n      "${key}": ${escapedDescription}`;

  let newBody;
  if (bodyEndsWithEntry) {
    // Trailing entry has no comma yet (it's currently last) -- add one,
    // then append the new entry, preserving whatever trailing whitespace
    // existed before the close.
    const trailingWsMatch = /(\r?\n\s*)$/.exec(body);
    const trailingWs = trailingWsMatch ? trailingWsMatch[1] : '\n    ';
    const bodyNoTrailingWs = trailingWsMatch ? body.slice(0, -trailingWs.length) : body;
    newBody = `${bodyNoTrailingWs},${newEntryLine}${trailingWs}`;
  } else {
    // Empty object (no existing reservations) -- shouldn't happen here
    // since the file has ~80 already, but handle it for robustness/tests.
    newBody = `${newEntryLine}\n    `;
  }

  return raw.slice(0, bodyStart) + newBody + raw.slice(closeIdx);
}

function findReservationFor(data, code) {
  const m = CODE_RE.exec(code);
  if (!m) return null;
  const letter = m[1];
  const num = parseInt(m[2], 10);
  const reservations = parseReservations(data.ranges.reserved);
  return (
    reservations.find(
      (r) => !r.malformed && r.letter === letter && num >= r.start && num <= r.end
    ) || null
  );
}

function addCode({
  errorsPath,
  code,
  mkey,
  name,
  template,
  remedy,
  severity,
  dryRun,
}) {
  if (!CODE_RE.test(code)) {
    throw new Error(`code "${code}" does not match ^MET-[A-Z][0-9]{3,4}$`);
  }
  if (!mkey) throw new Error('add requires --mkey <mkey>');
  if (!name) throw new Error('add requires --name <ErrName>');
  if (!template || !template.trim()) throw new Error('add requires a non-empty --template');

  const sev = severity || 'error';
  if (!VALID_SEVERITIES.has(sev)) {
    throw new Error(`--severity must be one of fatal|error|warn, got "${sev}"`);
  }

  const { raw, data } = loadRegistry(errorsPath);

  if (Object.prototype.hasOwnProperty.call(data.codes, code)) {
    throw new Error(`code ${code} is already registered`);
  }

  const reservation = findReservationFor(data, code);
  if (!reservation) {
    throw new Error(
      `code ${code} does not fall inside any reserved range -- run ` +
        `"claim-range ${mkey}" first (or claim-range for the correct mkey)`
    );
  }
  // The reservation's free-text description names its owner as
  // "reserved for <mkey> " or "reserved for <mkey>(". Ownership is decided by
  // EXACT string equality of the extracted owner token, never substring/regex
  // containment: a \b boundary after the mkey would let a hyphen-prefix mkey
  // (engine.fiscal vs engine.fiscal-circuit, engine.invariant vs
  // engine.invariant-multiterm-design -- both live collisions in this repo's
  // mkey namespace) claim a foreign range, the exact bypass a destructive
  // round found on 2026-08-18 (BUG-273 r1 REJECT).
  const ownerMatch = /reserved for ([^\s(]+)/.exec(reservation.description);
  const ownerToken = ownerMatch ? ownerMatch[1] : null;
  if (ownerToken !== mkey) {
    throw new Error(
      `code ${code} falls inside range ${reservation.key}, which is reserved for a ` +
        `DIFFERENT owner (${ownerToken || 'unparseable owner'}): ${reservation.description}`
    );
  }

  const remedyText = remedy || 'See the module\'s documentation for remediation guidance.';

  if (dryRun) {
    return { code, reservation, wrote: false };
  }

  const newRaw = insertCode(raw, data, code, {
    severity: sev,
    module: mkey,
    message: template,
    remedy: remedyText,
  });
  const reparsed = JSON.parse(newRaw);
  if (!reparsed.codes[code]) {
    throw new Error('internal error: code insertion did not round-trip');
  }
  atomicWrite(errorsPath, newRaw);

  return { code, reservation, wrote: true };
}

// Insert a new code entry into the "codes" object via a targeted text
// splice. Placed immediately after the last existing entry that shares
// the new code's reservation range (keeping a module's codes contiguous,
// matching the file's existing convention for e.g. the G layer), or, if
// no sibling exists yet, appended as the last entry in "codes" overall.
function insertCode(raw, data, code, fields) {
  const entries = locateCodeEntries(raw);
  if (entries.length === 0) {
    throw new Error('could not locate any existing "codes" entries -- unexpected file shape');
  }

  const reservation = findReservationFor(data, code);
  let insertAfter = null;
  if (reservation) {
    for (const e of entries) {
      const m = CODE_RE.exec(e.code);
      if (!m) continue;
      if (m[1] !== reservation.letter) continue;
      const num = parseInt(m[2], 10);
      if (num < reservation.start || num > reservation.end) continue;
      if (!insertAfter || e.endLine > insertAfter.endLine) insertAfter = e;
    }
  }
  if (!insertAfter) {
    insertAfter = entries[entries.length - 1];
  }

  const isGlobalLast = insertAfter === entries[entries.length - 1];

  const lines = raw.split('\n');
  const entryLines = [
    `    "${code}": {`,
    `      "severity": "${fields.severity}",`,
    `      "module": ${JSON.stringify(fields.module)},`,
    `      "message": ${JSON.stringify(fields.message)},`,
    `      "remedy": ${JSON.stringify(fields.remedy)}`,
    isGlobalLast ? '    }' : '    },',
  ];

  if (isGlobalLast) {
    // The previous last entry's closing "    }" needs a trailing comma now
    // that it is no longer last.
    const closeLine = lines[insertAfter.endLine];
    if (closeLine.trim() !== '}') {
      throw new Error('internal error: expected global-last entry to close with a bare "}"');
    }
    lines[insertAfter.endLine] = closeLine.replace(/\}\s*$/, '},');
  }

  const insertPos = insertAfter.endLine + 1;
  lines.splice(insertPos, 0, ...entryLines);
  return lines.join('\n');
}

// Walk the file line-by-line inside the top-level "codes" object and
// return [{code, startLine, endLine}] for each flat entry. Entries in
// this file are always flat (four string fields, no nesting), so a
// simple brace-depth-relative scan is safe and avoids parsing the whole
// file as a token stream.
function locateCodeEntries(raw) {
  const lines = raw.split('\n');
  const codesLineIdx = lines.findIndex((l) => /^\s*"codes":\s*\{\s*$/.test(l));
  if (codesLineIdx === -1) return [];

  const entries = [];
  const entryOpenRe = /^ {4}"(MET-[A-Z]\d{3,4})":\s*\{\s*$/;
  const entryCloseRe = /^ {4}\}(,)?\s*$/;
  let i = codesLineIdx + 1;
  let current = null;
  while (i < lines.length) {
    const line = lines[i];
    if (current === null) {
      const m = entryOpenRe.exec(line);
      if (m) {
        current = { code: m[1], startLine: i };
      } else if (/^\s*\}\s*$/.test(line)) {
        // closing brace of "codes" itself
        break;
      }
    } else if (entryCloseRe.test(line)) {
      current.endLine = i;
      entries.push(current);
      current = null;
    }
    i += 1;
  }
  return entries;
}

function check({ errorsPath, repoDir }) {
  const { data } = loadRegistry(errorsPath);
  const problems = [];
  const warnings = [];

  // 1. Duplicate codes -- JSON.parse silently keeps the last of a
  // duplicate key, so scan the raw text for repeated key literals too.
  const raw = fs.readFileSync(errorsPath, 'utf8');
  const keyCounts = new Map();
  const keyRe = /^ {4}"(MET-[A-Z]\d{3,4})":\s*\{/gm;
  let km;
  while ((km = keyRe.exec(raw))) {
    keyCounts.set(km[1], (keyCounts.get(km[1]) || 0) + 1);
  }
  for (const [code, count] of keyCounts) {
    if (count > 1) problems.push(`duplicate code ${code} appears ${count} times`);
  }

  // 2. Codes outside any reservation.
  const codes = parseCodes(data.codes);
  for (const c of codes) {
    if (c.malformed) {
      problems.push(`code "${c.code}" does not match ^MET-[A-Z][0-9]{3,4}$`);
      continue;
    }
    const owner = findReservationFor(data, c.code);
    if (!owner) {
      problems.push(`code ${c.code} has no owning reservation in ranges.reserved`);
    }
  }

  // 3. Overlapping reservations (same letter, numeric ranges intersect).
  const reservations = parseReservations(data.ranges.reserved);
  for (const r of reservations) {
    if (r.malformed) {
      problems.push(`reservation key "${r.key}" is malformed (expected LETTERNNN-LETTERNNN)`);
    }
  }
  const byLetter = new Map();
  for (const r of reservations) {
    if (r.malformed) continue;
    if (!byLetter.has(r.letter)) byLetter.set(r.letter, []);
    byLetter.get(r.letter).push(r);
  }
  for (const [letter, list] of byLetter) {
    const sorted = [...list].sort((a, b) => a.start - b.start);
    // BUG-309: track the running maximum end, not just the immediate
    // predecessor — a short interval sandwiched between two wide ones
    // (e.g. G000-G100, G050-G050, G060-G080) must still flag the outer
    // pair's overlap, which a prev-only comparison misses.
    let maxEnd = -1;
    let maxEndKey = '';
    for (const cur of sorted) {
      if (cur.start <= maxEnd) {
        problems.push(
          `reservations overlap on layer ${letter}: ${maxEndKey} and ${cur.key}`
        );
      }
      if (cur.end > maxEnd) {
        maxEnd = cur.end;
        maxEndKey = cur.key;
      }
    }
  }

  // 4. Cross-worktree staleness warning against origin/main (never fails).
  try {
    const relPath = path
      .relative(repoDir, errorsPath)
      .split(path.sep)
      .join('/');
    const originRaw = execFileSync(
      'git',
      ['show', `origin/main:${relPath}`],
      { cwd: repoDir, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }
    );
    const originData = JSON.parse(originRaw);
    const originReserved = originData.ranges && originData.ranges.reserved
      ? Object.keys(originData.ranges.reserved)
      : [];
    const localReserved = new Set(Object.keys(data.ranges.reserved || {}));
    const missing = originReserved.filter((k) => !localReserved.has(k));
    if (missing.length > 0) {
      warnings.push(
        `local ${relPath} is missing ${missing.length} reservation(s) present on origin/main ` +
          `(${missing.slice(0, 8).join(', ')}${missing.length > 8 ? ', ...' : ''}) -- ` +
          'rebase before claiming a new range to avoid a collision.'
      );
    }
  } catch (cause) {
    warnings.push(
      `could not compare against origin/main (${cause.message.split('\n')[0]}) -- skipping ` +
        'the cross-worktree staleness check.'
    );
  }

  return { problems, warnings, totalCodes: codes.length, totalReservations: reservations.length };
}

function atomicWrite(targetPath, content) {
  const dir = path.dirname(targetPath);
  const tmpPath = path.join(
    dir,
    `.${path.basename(targetPath)}.tmp-${process.pid}-${Date.now()}`
  );
  fs.writeFileSync(tmpPath, content, 'utf8');
  fs.renameSync(tmpPath, targetPath);
}

function currentActor() {
  try {
    return execFileSync('git', ['config', 'user.name'], { encoding: 'utf8' }).trim() || os.userInfo().username;
  } catch {
    return os.userInfo().username;
  }
}

// ---------------------------------------------------------------------
// CLI wrapper
// ---------------------------------------------------------------------

function parseFlags(argv) {
  const flags = {};
  const positional = [];
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg.startsWith('--')) {
      const key = arg.slice(2);
      if (key === 'dry-run') {
        flags.dryRun = true;
        continue;
      }
      const value = argv[i + 1];
      flags[key] = value;
      i += 1;
    } else {
      positional.push(arg);
    }
  }
  return { flags, positional };
}

function main(argv) {
  const [sub, ...rest] = argv;
  const { flags, positional } = parseFlags(rest);
  const errorsPath = flags['errors-path'] || DEFAULT_ERRORS_PATH;
  const repoDir = flags['repo-dir'] || DEFAULT_REPO_DIR;

  try {
    if (sub === 'claim-range') {
      const mkey = positional[0];
      const size = flags.size ? parseInt(flags.size, 10) : 100;
      if (!Number.isInteger(size) || size <= 0) {
        throw new Error(`--size must be a positive integer, got "${flags.size}"`);
      }
      const result = claimRange({
        errorsPath,
        mkey,
        size,
        layerOverride: flags.layer,
        dryRun: !!flags.dryRun,
        actor: currentActor(),
      });
      const verb = result.wrote ? 'Claimed' : '[dry-run] Would claim';
      console.log(`${verb} ${result.rangeKey} for "${mkey}" (layer ${result.letter}, size ${size})`);
      console.log(result.description);
      return 0;
    }

    if (sub === 'add') {
      const code = positional[0];
      if (!code) throw new Error('add requires <CODE> as the first argument');
      const result = addCode({
        errorsPath,
        code,
        mkey: flags.mkey,
        name: flags.name,
        template: flags.template,
        remedy: flags.remedy,
        severity: flags.severity,
        dryRun: !!flags.dryRun,
      });
      const verb = result.wrote ? 'Added' : '[dry-run] Would add';
      console.log(`${verb} ${code} (owner range ${result.reservation.key})`);
      return 0;
    }

    if (sub === 'check') {
      const result = check({ errorsPath, repoDir });
      for (const w of result.warnings) console.warn(`WARN: ${w}`);
      if (result.problems.length === 0) {
        console.log(
          `OK: ${result.totalCodes} codes, ${result.totalReservations} reservations, no violations.`
        );
        return 0;
      }
      console.error(`FAIL: ${result.problems.length} violation(s):`);
      for (const p of result.problems) console.error(`  - ${p}`);
      return 1;
    }

    console.error(
      'usage:\n' +
        '  node tools/plan/add-error.js claim-range <mkey> [--size N] [--layer X] [--dry-run]\n' +
        '  node tools/plan/add-error.js add <CODE> --mkey <mkey> --name <ErrName> --template "<msg>" [--remedy "..."] [--severity fatal|error|warn] [--dry-run]\n' +
        '  node tools/plan/add-error.js check'
    );
    return 2;
  } catch (err) {
    console.error(`ERROR: ${err.message}`);
    return 1;
  }
}

if (require.main === module) {
  process.exit(main(process.argv.slice(2)));
}

module.exports = {
  inferLayer,
  formatCode,
  allocateRange,
  parseReservations,
  parseCodes,
  claimRange,
  addCode,
  check,
  locateCodeEntries,
  insertReservation,
  insertCode,
  findReservationFor,
  atomicWrite,
  main,
  DEFAULT_ERRORS_PATH,
  DEFAULT_REPO_DIR,
};
