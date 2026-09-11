/**
 * units-lint.js — Units-of-measurement registry lint (2026-08-22)
 *
 * Enforces that every unit of measurement used across the codebase is
 * registered in code.json's top-level `units` section (sourced verbatim from
 * docs/planning/master-plan-v2.1.json, like `conventions` — GR#3). Two checks:
 *
 *   UNITS-LINT-001 (unregistered unit): a unit token detected in Go source or
 *     data/*.json maps to a SPECIFIC unit key that is not registered. The
 *     detector vocabulary is a fixed map of token spelling -> unit key. The
 *     registered key set is derived from code.json at runtime (GR#15). A
 *     missing dimension with no unit is covered by this too (every dimension
 *     has at least one key in the vocabulary), but a unit missing WITHIN a
 *     covered dimension is also caught — the F3 reject fix.
 *
 *   UNITS-LINT-002 (stale definition): a registered unit's `definedAt`
 *     (path:line) no longer resolves on disk — the constant/type moved or was
 *     renamed, so the registry's definition pointer has drifted.
 *
 * Read-only, report-only. Never hand-edits code.json, the master plan, or Go
 * source — a finding names its fix route (register the unit in the master plan
 * and regenerate, or correct the `definedAt` pointer). Exit 1 on findings, 0
 * clean. Exported `runLint` so tests can prove each check can fail.
 *
 * SCOPE (documented, not a silent blind spot): the registry covers UNITS OF
 * MEASUREMENT — (1) physical dimensions and their scales (money/mass/volume/
 * energy/power/length/area/time/speed/noise), (2) fixed-point ratio units
 * (per-mille/basis-point/percent), and (3) concrete countable entities used as
 * the denominator of a money rate (cost/wage/subsidy/price/rate/grant/award/
 * penalty per entity — case, staff, place, offender, engineer-day, tile,
 * milestone, detective, vermin, …) plus count×time labour compounds. It
 * deliberately EXCLUDES dimensionless game-mechanic scores — "points",
 * "attainment/research/prestige points", "weight", "fraction", "rate",
 * "probability/month", the "-draw units" (prospect/visitor) values — and
 * per-METRIC (continuous-quantity) denominators such as "per-condition",
 * "per-contamination", "per-stress", "per-deprivation", "per-novelty",
 * "per-pressure", "per-wear-point", "per-exposure", "per-money", "per-funding".
 * Those carry no scale to mismatch (BUG-355); only a concrete entity or a
 * physical unit can.
 *
 * Usage: node tools/plan/units-lint.js
 */

'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_REPO_DIR = path.resolve(__dirname, '..', '..');

// Detector vocabulary: unit token spelling -> the SPECIFIC unit key it should
// resolve to. A token found in source whose key is absent from code.json is a
// UNITS-LINT-001 finding. Tokens are matched as plain substrings — each
// spelling is chosen to be unambiguous in source; a false positive on the
// occasional comment is an acceptable cost for a report-only lint (documented
// in the /units-lint skill), and the trade buys unit-level precision.
const VOCABULARY = [
  // money
  { token: 'Micropounds', key: 'money.micropound' },
  { token: 'MicroPounds', key: 'money.micropound' },
  { token: 'micro-pound', key: 'money.micropound' },
  { token: 'micropound', key: 'money.micropound' },
  { token: 'µ£', key: 'money.micropound' },
  { token: 'Pence', key: 'money.pence' },
  { token: 'PerPound', key: 'money.pound' },
  // ratio
  { token: 'PerMille', key: 'ratio.per-mille' },
  { token: 'per-mille', key: 'ratio.per-mille' },
  { token: 'per mille', key: 'ratio.per-mille' },
  { token: 'BasisPoints', key: 'ratio.basis-point' },
  { token: 'basis point', key: 'ratio.basis-point' },
  { token: 'Percent', key: 'ratio.percent' },
  { token: 'percent', key: 'ratio.percent' },
  // time
  { token: 'PerDay', key: 'time.day' },
  { token: 'PerMonth', key: 'time.month' },
  { token: 'PerYear', key: 'time.year' },
  { token: 'PerTick', key: 'time.day-tick' },
  { token: 'PerSecond', key: 'time.real-second' },
  { token: 'PerHour', key: 'time.hour' },
  { token: 'PerWeek', key: 'time.week' },
  { token: 'hoursPerWeek', key: 'time.week' },
  // mass
  { token: 'PerTonne', key: 'mass.tonne' },
  { token: 'Tonnes', key: 'mass.tonne' },
  { token: 'tonnes', key: 'mass.tonne' },
  { token: 'tonne', key: 'mass.tonne' },
  { token: 'kg', key: 'mass.kilogram' },
  { token: 'PerKg', key: 'mass.kilogram' },
  // volume
  { token: 'Litres', key: 'volume.litre' },
  { token: 'litres', key: 'volume.litre' },
  { token: 'litre', key: 'volume.litre' },
  { token: 'PerLitre', key: 'volume.litre' },
  { token: 'm³', key: 'volume.cubic-metre' },
  { token: 'm3', key: 'volume.cubic-metre' },
  // energy
  { token: 'kWh', key: 'energy.kilowatt-hour' },
  { token: 'KWh', key: 'energy.kilowatt-hour' },
  { token: 'kwh', key: 'energy.kilowatt-hour' },
  { token: 'MWh', key: 'energy.megawatt-hour' },
  // power
  { token: 'MW', key: 'power.megawatt' },
  { token: 'kW', key: 'power.kilowatt' },
  { token: 'KW', key: 'power.kilowatt' },
  // length
  { token: 'Metres', key: 'length.metre' },
  { token: 'metres', key: 'length.metre' },
  { token: 'Miles', key: 'length.mile' },
  { token: 'miles', key: 'length.mile' },
  { token: 'km', key: 'length.kilometre' },
  { token: 'Kmh', key: 'speed.kilometre-per-hour' },
  // area
  { token: 'Hectares', key: 'area.hectare' },
  { token: 'hectares', key: 'area.hectare' },
  { token: 'm²', key: 'area.square-metre' },
  // count
  { token: 'worker-day', key: 'count.worker-day' },
  { token: 'worker-days', key: 'count.worker-day' },
  { token: 'pax', key: 'count.pax' },
  { token: 'headPerCell', key: 'count.head' },
  { token: 'bed-day', key: 'count.bed-day' },
  { token: 'student-yr', key: 'count.student-yr' },
  { token: 'prisoner-yr', key: 'count.prisoner-yr' },
  { token: 'standby retainer', key: 'count.retainer' },
  { token: 'PerPerson', key: 'count.person' },
  { token: 'per person', key: 'count.person' },
  { token: 'children', key: 'count.child' },
  { token: 'Children', key: 'count.child' },
  { token: 'PerChild', key: 'count.child' },
  { token: 'PerQuarter', key: 'count.married-quarter' },
  { token: 'married quarter', key: 'count.married-quarter' },
  { token: 'PerCase', key: 'count.case' },
  { token: 'PerStaff', key: 'count.staff' },
  { token: 'PerPlace', key: 'count.place' },
  { token: 'PerOffender', key: 'count.offender' },
  { token: 'PerEngineerDay', key: 'count.engineer-day' },
  { token: 'EngineerDay', key: 'count.engineer-day' },
  { token: 'PerTile', key: 'count.tile' },
  { token: 'PerMilestone', key: 'count.milestone' },
  { token: 'PerDetective', key: 'count.detective' },
  { token: 'PerVermin', key: 'count.vermin' },
  { token: 'PerSeverity', key: 'count.severity' },
  { token: 'PerUnit', key: 'count.unit' },
  { token: 'PerCell', key: 'count.cell' },
  { token: 'PerWorker', key: 'count.worker' },
  // speed
  { token: 'Knots', key: 'speed.knot' },
  { token: 'knots', key: 'speed.knot' },
  { token: 'km/h', key: 'speed.kilometre-per-hour' },
  // noise
  { token: 'dBA', key: 'noise.decibel' },
  { token: 'decibel', key: 'noise.decibel' },
  // FEAT-2326609803 (BUG-905 follow-up) — the two webconsole traffic units
  // whose incommensurability BUG-905 missed. See MIXING_PAIRS below for the
  // dimensional-mismatch check between them.
  { token: 'capacityPcuPerLanePerHour', key: 'count.pcu-lane-hour' },
  { token: 'ROAD_TIER_CAPACITY', key: 'count.person-tick-tile' },
];

// ── FEAT-2326609803 rework (r2, BUG-980): a hand-typed per-file allowlist is
// EXACTLY the blind-spot class this lint exists to prevent — the r1 version
// of this list omitted trafficAssignment.ts and engine.ts, both of which use
// these very tokens today. So no allowlist: every *.ts file directly under
// webconsole/src/sim (flat directory listing, not recursive — see
// collectSimTsFiles) is scanned for BOTH the vocabulary above AND the
// dimensional-mismatch check (MIXING_PAIRS), in addition to internal/cmd Go
// source and data/*.json (top-level + EXTRA_DATA_DIRS subdirectories below).
// Widening to `webconsole/src/**` (recursive) is a separate, larger change
// (components/hooks are a different unit-scope question); this directory is
// where every current pcu/lane/hour and person/tick/tile consumer lives.

/** Every *.ts file directly under webconsole/src/sim, sorted (deterministic). */
function collectSimTsFiles(repoDir) {
  const dir = path.join(repoDir, 'webconsole', 'src', 'sim');
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir, { withFileTypes: true })
    .filter(e => e.isFile() && e.name.endsWith('.ts'))
    .map(e => `webconsole/src/sim/${e.name}`)
    .sort();
}

// data/*.json subdirectories scanned in addition to the top-level data/
// directory. link_capacity.json (the capacityPcuPerLanePerHour source) lives
// under data/traffic/, one level below the plain data/*.json scan. PINNED
// (BUG-981): tools/plan/units-lint.test.js proves removing 'traffic' from
// this list drops link_capacity.json from the scan.
const EXTRA_DATA_DIRS = ['traffic'];

/**
 * UNITS-LINT-003 dimensional-mismatch pairs (BUG-905 class): a value sourced
 * from group `a`'s tag tokens must never be combined via +/- with a value
 * sourced from group `b`'s tag tokens in the same file, because the two units
 * have no registered conversion. This is a syntactic heuristic, not a type
 * checker: pass 1 finds single-line `const|let|var NAME = <rhs containing a
 * tag token>` declarations and records NAME under that group; pass 2 looks
 * for a `+` or `-` directly between a group-a name and a group-b name
 * anywhere in the file. False negatives across files, through helper
 * functions, or through multi-line declarations are possible and accepted
 * for a report-only lint (documented in the /units-lint skill) — it exists
 * to catch the LITERAL BUG-905 shape (a same-file subtraction of two
 * incommensurable "capacity" figures), not to replace the Destructive round.
 */
const MIXING_PAIRS = [
  {
    aKey: 'count.pcu-lane-hour',
    aTags: ['capacityPcuPerLanePerHour', 'PcuPerLanePerHour', 'linkCapacityPerLane'],
    bKey: 'count.person-tick-tile',
    bTags: ['ROAD_TIER_CAPACITY', 'lineUsageOf', 'busLaneSpec()'],
    // A bare `.capacity` member access (LineUsage.capacity, e.g. `u.capacity`)
    // is itself a count.person-tick-tile value even where it is never
    // assigned to a named variable — matched directly in pass 2 rather than
    // via the named-variable taint set. Word-boundaried so it does NOT match
    // inside "capacityPcuPerLanePerHour" (no preceding "." there and no word
    // boundary after "capacity" mid-identifier).
    bMemberMarker: /\.capacity\b/,
  },
];

/**
 * Load code.json and derive the registered unit keys (GR#15). Returns
 * { units, registeredKeys }. Throws on missing/unparseable registry.
 */
function loadRegistry(repoDir) {
  const codeJsonPath = path.join(repoDir, 'code.json');
  if (!fs.existsSync(codeJsonPath)) {
    throw new Error(`code.json not found at ${codeJsonPath}`);
  }
  const codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8'));
  const units = Array.isArray(codeJson.units) ? codeJson.units : [];
  const registeredKeys = new Set(units.map(u => u.key).filter(Boolean));
  return { units, registeredKeys };
}

/** Resolve a `definedAt` value ("path:line") to { absPath, line } or null. */
function parseDefinedAt(repoDir, definedAt) {
  if (typeof definedAt !== 'string' || !definedAt) return null;
  const m = definedAt.match(/^([^:]+):(\d+)/);
  if (!m) return null;
  return {
    absPath: path.join(repoDir, m[1].replace(/\\/g, '/')),
    line: parseInt(m[2], 10),
  };
}

/** Recursively collect *.go files under a directory (sorted, deterministic). */
function collectGoFiles(dir, out) {
  if (!fs.existsSync(dir)) return;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) collectGoFiles(full, out);
    else if (entry.name.endsWith('.go')) out.push(full);
  }
}

/** Escape a string for safe interpolation into a RegExp source. */
function escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * stripCommentsAndStrings (BUG-995, r3 rework) — blanks `//` line comments,
 * `/* ... * /` block comments, and the bodies of `"..."`/`'...'`/`` `...` ``
 * literals to spaces (newlines preserved) before either findMixing pass ever
 * looks at the text. A remediation comment that merely NAMES both pair
 * tokens (e.g. a "// BUG-905: we used to do X - Y here" note) or a log
 * string doing the same must never fire UNITS-LINT-003 — now that BUG-983
 * wired the lint into ci.yml, a prose comment redding CI with no code defect
 * would be exactly the kind of false alarm that gets a real gate disabled.
 * Lexical, not a full tokenizer: it does not special-case regex literals
 * (`/foo-bar/`), an accepted narrow gap for a report-only heuristic that
 * already documents several other single-file/single-expression limits.
 */
function stripCommentsAndStrings(text) {
  let out = '';
  let i = 0;
  const n = text.length;
  while (i < n) {
    const c = text[i];
    const c2 = text[i + 1];
    if (c === '/' && c2 === '/') {
      while (i < n && text[i] !== '\n') { out += ' '; i++; }
    } else if (c === '/' && c2 === '*') {
      out += '  ';
      i += 2;
      while (i < n && !(text[i] === '*' && text[i + 1] === '/')) {
        out += text[i] === '\n' ? '\n' : ' ';
        i++;
      }
      if (i < n) { out += '  '; i += 2; }
    } else if (c === '"' || c === "'" || c === '`') {
      const quote = c;
      out += ' ';
      i++;
      while (i < n && text[i] !== quote) {
        if (text[i] === '\\' && i + 1 < n) { out += '  '; i += 2; continue; }
        out += text[i] === '\n' ? '\n' : ' ';
        i++;
      }
      if (i < n) { out += ' '; i++; }
    } else {
      out += c;
      i++;
    }
  }
  return out;
}

// BUG-994 (r3 rework): an operand's pair token may carry a RECEIVER prefix
// (`row.`, `linkCapacityRow(id).`) and/or an index/member SUFFIX (`[k]`,
// `.y`) — and either can appear on EITHER side of a `+`/`-`. The r2 version
// only allowed a receiver prefix on the operand appearing BEFORE the
// operator and a suffix on the operand appearing AFTER it, so it caught only
// the one literal ordering `row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[k]`
// and missed its mirror and the un-aliased BUG-905 shape
// (`ROAD_TIER_CAPACITY[k] - avenueCapPerLane`). Both wrappers now apply
// symmetrically to both sides, so direction never matters.
const RECEIVER_PREFIX = '(?:[A-Za-z_$][\\w$]*(?:\\([^()]*\\))?\\s*\\.\\s*)?';
const MEMBER_SUFFIX = '(?:\\s*\\[[^\\]\\n]*\\]|\\.[A-Za-z_$][\\w$]*)*';

/**
 * findMixing (UNITS-LINT-003, FEAT-2326609803) — see MIXING_PAIRS doc comment.
 *
 * HONEST SCOPE (BUG-982/BUG-994 lead amendments): this is a syntactic,
 * single-file, single-expression heuristic — not a type checker, not a
 * data-flow analysis. Within that scope it is direction-agnostic and
 * comment-blind (BUG-994/995 rework): either pair's own token literal (with
 * an optional single receiver prefix and/or index/member suffix on EITHER
 * side) or an already-classified variable name may appear on either side of
 * a `+`, `-`, `+=` or `-=`, optionally through one layer of parentheses, and
 * comments/string literals are stripped first so mentioning both tokens in
 * prose never fires. It will NOT catch mixing that only shows up across
 * files, through a helper function's parameters or return value, through
 * object/array/`this` field indirection two or more hops deep, or through
 * any operator ordering more than one hop from a literal or a simple decl.
 * It exists to catch the BUG-905 SHAPE (a same-file, same-statement
 * combination of the two units), not to replace the Destructive round.
 *
 * Two passes:
 *
 *   Pass 1 (variable tagging) — a single FORWARD scan over the file's
 *   `const|let|var NAME = RHS` declarations, each read from its `=` up to
 *   its terminating `;` regardless of how many lines it spans (BUG-997: JS/TS
 *   decl-before-use means file order IS dependency order — no fixed-point
 *   loop needed, and a multi-line RHS like a wrapped ternary is one
 *   declaration, not zero). Each declaration gets at most one status, 'A' or
 *   'B':
 *     1. Seed match — RHS textually contains one of the pair's aTags/bTags
 *        literal markers => that status, full stop (checked A first; when a
 *        single RHS cites BOTH sides' literals directly, e.g. the plainest
 *        BUG-979 shape, pass 2's literal scan below still catches the mixing
 *        independent of this tag — variable tagging is an ADDITION to the
 *        literal scan, not a replacement for it).
 *     2. Otherwise, reference propagation — RHS mentions already-classified
 *        name(s). If every referenced name shares the SAME tag AND the RHS
 *        contains a `/`, the result is treated as an untagged DIMENSIONLESS
 *        RATIO (BUG-982: a pcu/pcu or person-tick-tile/person-tick-tile
 *        division cancels the unit — e.g. the shipped busPriorityCapacityInfoOf
 *        laneShareFraction, a pcu-per-lane ratio spread across several lines
 *        of a wrapped ternary, is correctly left untagged rather than
 *        incorrectly carrying the 'A' tag through to the value it is later
 *        multiplied onto). Otherwise the foreign/dangerous unit for this
 *        pair ('A') dominates if referenced at all, else 'B' — either
 *        carries through +, -, *, alike so it can still be caught downstream
 *        (this project does not model general unit cancellation, only the
 *        same-tag-ratio special case above).
 *
 *   Pass 2 (mixing scan) — over the WHOLE (comment/string-stripped) file
 *   text, flags a `+`, `-`, `+=` or `-=` directly between one side (a
 *   pass-1 'A' variable name, OR any of the pair's raw aTags token literals,
 *   each optionally carrying one receiver prefix and/or index/member suffix)
 *   and the other side (the 'B' equivalent, or the bare member-access marker
 *   e.g. `.capacity`) — each side optionally wrapped in one layer of
 *   parentheses, in EITHER order. Literal tokens are always live (BUG-979):
 *   the pair's own spelling is itself a valid operand even when never bound
 *   to a name, so `row.capacityPcuPerLanePerHour - ROAD_TIER_CAPACITY[2]`
 *   fires as a bare one-statement expression, not only through the
 *   named-variable route, and so does its mirror
 *   `ROAD_TIER_CAPACITY[2] - row.capacityPcuPerLanePerHour` (BUG-994).
 */
function findMixing(text, fileRel) {
  const out = [];
  // Normalise CRLF -> LF (defensive; the repo is LF — .gitattributes pins
  // text=auto eol=lf and every real file scanned here is checked out LF —
  // but JS regex `.` never matches a line-terminator character including CR,
  // so a decl-splitting regex would silently see zero declarations on any
  // CRLF input this function is ever handed, e.g. from a caller reading a
  // file some other tool wrote with CRLF line endings).
  const normalized = stripCommentsAndStrings(text.replace(/\r\n/g, '\n'));
  // Read each `const|let|var NAME = RHS;` as one unit regardless of how many
  // lines RHS spans (BUG-997) — comments/strings are already stripped, so a
  // `;` inside a string literal can never be mistaken for the terminator.
  const declRe = /\b(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*([^;]*);/g;
  const decls = [];
  let dm;
  while ((dm = declRe.exec(normalized))) {
    decls.push({ name: dm[1], rhs: dm[2].replace(/\n/g, ' ') });
  }

  // A bare identifier/token can be matched with \b on both sides; a token
  // carrying non-word characters (e.g. "busLaneSpec()") cannot, so it is
  // matched as a plain literal instead.
  function tokenPattern(tok) {
    const esc = escapeRegex(tok);
    return /^[$A-Za-z_][\w$]*$/.test(tok) ? `\\b${esc}\\b` : esc;
  }

  // Wrap a token/name alternation with an optional single receiver prefix,
  // an optional index/member suffix, and an optional single paren layer
  // around the whole thing (BUG-979: "d - (p)"; BUG-994: prefix/suffix now
  // apply the same way regardless of which side of the operator this operand
  // lands on).
  function operandPattern(alt) {
    return `\\(?\\s*${RECEIVER_PREFIX}(?:${alt})${MEMBER_SUFFIX}\\s*\\)?`;
  }

  for (const pair of MIXING_PAIRS) {
    const status = new Map(); // name -> 'A' | 'B', in declaration order
    for (const d of decls) {
      if (pair.aTags.some(t => d.rhs.includes(t))) { status.set(d.name, 'A'); continue; }
      if (pair.bTags.some(t => d.rhs.includes(t))) { status.set(d.name, 'B'); continue; }
      const refs = [];
      for (const [name, st] of status) {
        if (new RegExp(`\\b${escapeRegex(name)}\\b`).test(d.rhs)) refs.push(st);
      }
      if (refs.length === 0) continue;
      const distinctTags = new Set(refs);
      if (distinctTags.size === 1 && d.rhs.includes('/')) {
        // BUG-982: a same-unit ratio (A/A or B/B) is dimensionless — leave
        // this declaration untagged rather than propagating the tag through
        // a division that cancels it.
        continue;
      }
      status.set(d.name, refs.includes('A') ? 'A' : 'B');
    }

    const aVars = [...status].filter(([, st]) => st === 'A').map(([n]) => n);
    const bVars = [...status].filter(([, st]) => st === 'B').map(([n]) => n);

    // Literal token spellings are ALWAYS live operands (BUG-979), independent
    // of whether any variable in this file happens to carry the tag.
    const aPatterns = [...aVars, ...pair.aTags].map(tokenPattern);
    const bPatterns = [...bVars, ...pair.bTags].map(tokenPattern);
    if (pair.bMemberMarker) bPatterns.push(pair.bMemberMarker.source);

    const aAlt = aPatterns.join('|');
    const bAlt = bPatterns.join('|');
    const aSide = operandPattern(aAlt);
    const bSide = operandPattern(bAlt);
    const infixOp = '\\s*[+-]\\s*';
    // Compound assignment: `<name> -= ... <other side> ...` on the same
    // line/statement — a lazy same-text match, not newline-bounded, since a
    // compound-assign RHS can itself span a short expression.
    const compoundOp = '\\s*[+-]=\\s*';
    const mixRe = new RegExp(
      [
        `${aSide}${infixOp}${bSide}`,
        `${bSide}${infixOp}${aSide}`,
        `(?:${aAlt})${compoundOp}[^;\\n]*?(?:${bAlt})`,
        `(?:${bAlt})${compoundOp}[^;\\n]*?(?:${aAlt})`,
      ].join('|'),
      'g'
    );
    let m;
    while ((m = mixRe.exec(normalized))) {
      out.push({
        code: 'UNITS-LINT-003',
        file: fileRel,
        aKey: pair.aKey,
        bKey: pair.bKey,
        match: m[0].replace(/\s+/g, ' ').trim(),
      });
    }
  }
  return out;
}

/**
 * runLint — never calls process.exit (the CLI wrapper owns exit codes).
 * Returns { totalErrors, findings, staleDefinitions, unitsChecked, filesScanned }.
 */
function runLint(opts = {}) {
  const repoDir = opts.repoDir || DEFAULT_REPO_DIR;
  const { units, registeredKeys } = loadRegistry(repoDir);

  const findings = [];        // UNITS-LINT-001
  const staleDefinitions = []; // UNITS-LINT-002
  const mixing = [];          // UNITS-LINT-003
  const filesScanned = [];

  function scanText(text, fileRel, src) {
    for (const v of VOCABULARY) {
      if (text.includes(v.token) && !registeredKeys.has(v.key)) {
        findings.push({ code: 'UNITS-LINT-001', key: v.key, token: v.token, file: fileRel, src });
      }
    }
  }

  // ── Go source ──────────────────────────────────────────────────────────────
  const goDirs = ['internal', 'cmd'].map(d => path.join(repoDir, d));
  const goFiles = [];
  for (const d of goDirs) collectGoFiles(d, goFiles);
  goFiles.sort();
  for (const abs of goFiles) {
    const rel = path.relative(repoDir, abs).replace(/\\/g, '/');
    scanText(fs.readFileSync(abs, 'utf8'), rel, 'go');
    filesScanned.push(rel);
  }

  // ── data/*.json (top-level + EXTRA_DATA_DIRS subdirectories) ────────────────
  const dataDir = path.join(repoDir, 'data');
  if (fs.existsSync(dataDir)) {
    const dataFiles = fs.readdirSync(dataDir).filter(f => f.endsWith('.json')).sort();
    for (const f of dataFiles) {
      const abs = path.join(dataDir, f);
      const rel = 'data/' + f;
      scanText(fs.readFileSync(abs, 'utf8'), rel, 'data');
      filesScanned.push(rel);
    }
  }
  for (const sub of EXTRA_DATA_DIRS) {
    const subDir = path.join(dataDir, sub);
    if (!fs.existsSync(subDir)) continue;
    const subFiles = fs.readdirSync(subDir).filter(f => f.endsWith('.json')).sort();
    for (const f of subFiles) {
      const abs = path.join(subDir, f);
      const rel = `data/${sub}/${f}`;
      scanText(fs.readFileSync(abs, 'utf8'), rel, 'data');
      filesScanned.push(rel);
    }
  }

  // ── webconsole/src/sim/*.ts (FEAT-2326609803 r2/BUG-980: every file in the
  // directory, not a hand-typed allowlist) — vocabulary + UNITS-LINT-003 ────
  for (const rel of collectSimTsFiles(repoDir)) {
    const abs = path.join(repoDir, rel);
    const text = fs.readFileSync(abs, 'utf8');
    scanText(text, rel, 'ts');
    mixing.push(...findMixing(text, rel));
    filesScanned.push(rel);
  }

  // ── stale definedAt pointers ───────────────────────────────────────────────
  for (const u of units) {
    if (!u.definedAt) continue;
    const loc = parseDefinedAt(repoDir, u.definedAt);
    if (!loc) {
      staleDefinitions.push({ key: u.key, definedAt: u.definedAt, reason: 'malformed (want "path:line")' });
      continue;
    }
    if (!fs.existsSync(loc.absPath)) {
      staleDefinitions.push({ key: u.key, definedAt: u.definedAt, reason: `file not found: ${path.relative(repoDir, loc.absPath).replace(/\\/g, '/')}` });
      continue;
    }
    const lines = fs.readFileSync(loc.absPath, 'utf8').split('\n');
    if (loc.line < 1 || loc.line > lines.length) {
      staleDefinitions.push({ key: u.key, definedAt: u.definedAt, reason: `line ${loc.line} out of range (file has ${lines.length})` });
    }
  }

  return {
    totalErrors: findings.length + staleDefinitions.length + mixing.length,
    findings,
    staleDefinitions,
    mixing,
    unitsChecked: units.length,
    filesScanned,
  };
}

module.exports = {
  runLint, loadRegistry, VOCABULARY, parseDefinedAt, findMixing, MIXING_PAIRS,
  collectSimTsFiles, EXTRA_DATA_DIRS,
};

if (require.main === module) {
  let result;
  try {
    result = runLint();
  } catch (err) {
    console.error(`ERROR: units-lint could not run: ${err.message}`);
    process.exit(1);
  }
  console.log(`units-lint: ${result.unitsChecked} registered units, ${result.filesScanned.length} files scanned`);
  for (const f of result.findings) {
    console.error(`[${f.code}] ${f.file}: unit token "${f.token}" resolves to unregistered key "${f.key}" — register it in master-plan-v2.1.json units and regenerate code.json`);
  }
  for (const s of result.staleDefinitions) {
    console.error(`[UNITS-LINT-002] unit "${s.key}": definedAt "${s.definedAt}" is stale (${s.reason})`);
  }
  for (const m of result.mixing || []) {
    console.error(`[UNITS-LINT-003] ${m.file}: dimensional mismatch — "${m.match}" combines a ${m.aKey} value with a ${m.bKey} value with no registered conversion`);
  }
  if (result.totalErrors > 0) {
    console.error(`❌ UNITS-LINT FAILED: ${result.totalErrors} finding(s).`);
    process.exit(1);
  }
  console.log('✅ UNITS-LINT PASSED: every unit in use is registered and every definition pointer resolves.');
  process.exit(0);
}
