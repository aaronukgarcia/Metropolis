/**
 * tools/plan/icd-lint.js — ICD (Interface Control Document) validation
 * harness, Integration Engine Increment 4 (FEAT-190).
 *
 * Validates every `docs/planning/icd/*.md` file (skipping TEMPLATE.md,
 * which is instructional, not a real ICD) against the contract
 * `docs/planning/proposals/integration-engine.md` §5/§7/§8 point 4
 * requires:
 *
 *   1. SECTION PRESENCE  — every one of the 12 required `## N. <Name>`
 *      sections (Identity, Purpose, Inputs, Outputs, Update Class, Shard
 *      Scope, Determinism Guarantee, Error / Registry Codes, Resilience
 *      Behaviour, Monitoring Signals, Required Tests, Change Control) is
 *      present and non-empty.
 *   2. GUID CROSS-REFERENCE — the Identity section's `**GUID:**` value
 *      matches a real GUID actually present somewhere in code.json (a
 *      module/inbound/outbound/edge GUID), read-only. code.json/master-
 *      plan are never written by this tool.
 *   3. UPDATE CLASS ENUM — the Update Class section names exactly one of
 *      the literal tokens T0 / T1 / T2 (proposal §3's closed enum,
 *      mirrored by `internal/foundation/integration.Class`).
 *   4. MKEY EXISTENCE — the Identity section's "Owning module (mkey)"
 *      value is a key actually registered in code.json.
 *   5. NO-WALL-CLOCK DECLARATION — the Determinism Guarantee section
 *      contains an explicit "no wall-clock" declaration (GR#21).
 *
 * Deliberately simple and testable — section-presence + cross-reference
 * checks only, no prose NLP — following tools/plan/spec-lint.js's own
 * conventions (exported pure functions + a thin CLI wrapper that owns
 * process.exit, never called from the exported functions themselves).
 */

'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_REPO_DIR = path.resolve(__dirname, '..', '..');

// The 12 required sections (proposal §5/§8 point 4), in canonical order.
// Matched against a `## <optional-N.> <Name>` heading, case-insensitively,
// after collapsing whitespace — the numbering prefix ("1.", "12.") is
// cosmetic and not part of the match key.
const REQUIRED_SECTIONS = [
  'Identity',
  'Purpose',
  'Inputs',
  'Outputs',
  'Update Class',
  'Shard Scope',
  'Determinism Guarantee',
  'Error / Registry Codes',
  'Resilience Behaviour',
  'Monitoring Signals',
  'Required Tests',
  'Change Control',
];

// The closed update-class enum (mirrors internal/foundation/integration.
// Class's three literal values exactly — proposal §3). This is a fixed
// language/contract fact of the Integration Engine itself, not project
// balance data, so (unlike spec-lint's GR#15-derived-from-code.json sets)
// it is legitimately a literal here.
const VALID_UPDATE_CLASSES = ['T0', 'T1', 'T2'];

/** Normalize a heading/section name for matching: lowercase, collapse
 * whitespace, strip a leading "N. " ordinal prefix. */
function normalizeSectionName(raw) {
  return String(raw)
    .replace(/^\s*\d+\.\s*/, '')
    .trim()
    .replace(/\s+/g, ' ')
    .toLowerCase();
}

/**
 * Split a markdown ICD body into { normalizedSectionName: content }.
 * Only top-level `## ` headings are section boundaries (a `### ` sub-
 * heading stays inside its parent section's content). Content is
 * everything between one `## ` heading and the next, trimmed.
 */
function splitSections(content) {
  const lines = String(content).split(/\r?\n/);
  const sections = {};
  let current = null;
  let buf = [];
  const flush = () => {
    if (current !== null) {
      sections[current] = buf.join('\n').trim();
    }
  };
  for (const line of lines) {
    const m = /^##\s+(.+?)\s*$/.exec(line);
    // Guard against a `### ` sub-heading being mistaken for a `## `
    // section boundary — `##\s+` alone would also match `### foo`'s
    // trailing "# foo" after the first `#` is consumed by `##`, so
    // explicitly reject lines whose third character is also `#`.
    if (m && line[2] !== '#') {
      flush();
      current = normalizeSectionName(m[1]);
      buf = [];
    } else if (current !== null) {
      buf.push(line);
    }
  }
  flush();
  return sections;
}

/** Collect every GUID-shaped string value appearing anywhere in a parsed
 * code.json (module guids, inbound/outbound guids, edge guids — any key
 * at any depth), so the Identity GUID cross-reference check (#2) is not
 * tied to one specific field path. Read-only: never mutates codeJson. */
function collectAllGuids(codeJson) {
  const guidRe = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
  const found = new Set();
  const walk = (node) => {
    if (typeof node === 'string') {
      if (guidRe.test(node)) found.add(node.toLowerCase());
      return;
    }
    if (Array.isArray(node)) {
      for (const v of node) walk(v);
      return;
    }
    if (node && typeof node === 'object') {
      for (const v of Object.values(node)) walk(v);
    }
  };
  walk(codeJson);
  return found;
}

/**
 * Load code.json read-only and derive the structures icd-lint needs:
 * allGuids (every GUID string anywhere in the registry) and
 * modulesByKey (key -> module record, for the mkey existence check).
 * Throws on a missing/unparseable registry — the CLI wrapper turns that
 * into exit 1, mirroring spec-lint.js's loadRegistry.
 */
function loadRegistry(repoDir) {
  const codeJsonPath = path.join(repoDir, 'code.json');
  if (!fs.existsSync(codeJsonPath)) {
    throw new Error(`code.json not found at ${codeJsonPath}`);
  }
  const codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8'));
  const modulesByKey = {};
  for (const m of codeJson.modules || []) {
    if (m && m.key) modulesByKey[m.key] = m;
  }
  return { modulesByKey, allGuids: collectAllGuids(codeJson) };
}

/** Extract the Identity section's `**GUID:**` value (backtick-quoted or
 * bare), or null if absent/unparseable. */
function extractGuid(identitySection) {
  const m = /\*\*GUID:\*\*\s*`?([0-9a-fA-F-]{36})`?/.exec(identitySection || '');
  return m ? m[1] : null;
}

/** Extract the Identity section's `**Owning module (mkey):**` value, or
 * null if absent. Accepts an optional backtick/angle-bracket wrapping. */
function extractMkey(identitySection) {
  const m = /\*\*Owning module \(mkey\):\*\*\s*[`<]?([A-Za-z0-9_.-]+)[`>]?/.exec(identitySection || '');
  return m ? m[1] : null;
}

/** Extract the single declared update class token from the Update Class
 * section, or null if none/more-than-one distinct token is present. A
 * word-boundary match so "T0" inside a longer token ("T01x") never
 * counts. */
function extractUpdateClass(updateClassSection) {
  const matches = new Set(
    (String(updateClassSection || '').match(/\bT[0-2]\b/g) || [])
  );
  if (matches.size === 1) return [...matches][0];
  return null;
}

/** True iff the Determinism Guarantee section contains an explicit
 * no-wall-clock declaration (GR#21) — "no wall-clock" or "no wall
 * clock", case-insensitive. */
function hasNoWallClockDeclaration(determinismSection) {
  return /no\s+wall[-\s]?clock/i.test(String(determinismSection || ''));
}

/**
 * Lint a single ICD file's already-read content against the registry.
 * Returns an array of error strings (empty = clean). Pure, no I/O.
 */
function lintIcdContent(content, registry) {
  const errors = [];
  const sections = splitSections(content);

  for (const required of REQUIRED_SECTIONS) {
    const key = normalizeSectionName(required);
    const body = sections[key];
    if (body === undefined) {
      errors.push(`[ICD-LINT-001] MISSING SECTION: required section "## ${required}" not found.`);
    } else if (body.trim().length === 0) {
      errors.push(`[ICD-LINT-002] EMPTY SECTION: "## ${required}" is present but has no content.`);
    }
  }

  const identity = sections[normalizeSectionName('Identity')];
  if (identity !== undefined && identity.trim().length > 0) {
    const guid = extractGuid(identity);
    if (!guid) {
      errors.push('[ICD-LINT-003] MISSING GUID: Identity section has no parseable "**GUID:** `<uuid>`" line.');
    } else if (!registry.allGuids.has(guid.toLowerCase())) {
      errors.push(`[ICD-LINT-004] UNKNOWN GUID: Identity GUID "${guid}" does not match any GUID registered in code.json.`);
    }

    const mkey = extractMkey(identity);
    if (!mkey) {
      errors.push('[ICD-LINT-005] MISSING MKEY: Identity section has no parseable "**Owning module (mkey):**" line.');
    } else if (!registry.modulesByKey[mkey]) {
      errors.push(`[ICD-LINT-006] UNKNOWN MKEY: Identity "Owning module (mkey)" value "${mkey}" is not a registered code.json module key.`);
    }
  }

  const updateClassSection = sections[normalizeSectionName('Update Class')];
  if (updateClassSection !== undefined && updateClassSection.trim().length > 0) {
    const cls = extractUpdateClass(updateClassSection);
    if (!cls) {
      errors.push(`[ICD-LINT-007] INVALID UPDATE CLASS: "## Update Class" must name exactly one of ${VALID_UPDATE_CLASSES.join('/')}.`);
    }
  }

  const determinismSection = sections[normalizeSectionName('Determinism Guarantee')];
  if (determinismSection !== undefined && determinismSection.trim().length > 0) {
    if (!hasNoWallClockDeclaration(determinismSection)) {
      errors.push('[ICD-LINT-008] MISSING NO-WALL-CLOCK DECLARATION: "## Determinism Guarantee" must explicitly state that no wall-clock time is read (GR#21).');
    }
  }

  return errors;
}

/**
 * Run the full lint over every docs/planning/icd/*.md file (skipping
 * TEMPLATE.md). Options mirror spec-lint.js's runLint: repoDir, icdDir,
 * log/warn/error sinks. Never calls process.exit.
 * Returns { totalErrors, findingsByFile, filesChecked }.
 */
function runLint(opts = {}) {
  const repoDir = opts.repoDir || DEFAULT_REPO_DIR;
  const log = opts.log || console.log;
  const error = opts.error || console.error;

  const registry = loadRegistry(repoDir);
  const icdDir = opts.icdDir || path.join(repoDir, 'docs', 'planning', 'icd');

  if (!fs.existsSync(icdDir)) {
    log('No docs/planning/icd directory found, skipping icd-lint.');
    return { totalErrors: 0, findingsByFile: {}, filesChecked: 0 };
  }

  const files = fs.readdirSync(icdDir)
    .filter(f => f.endsWith('.md') && f !== 'TEMPLATE.md');

  let totalErrors = 0;
  const findingsByFile = {};

  log(`=== RUNNING ICD-LINT ON ${files.length} ICD FILE(S) (TEMPLATE.md skipped) ===`);

  for (const file of files) {
    const content = fs.readFileSync(path.join(icdDir, file), 'utf8');
    const fileErrors = lintIcdContent(content, registry);
    if (fileErrors.length > 0) {
      findingsByFile[file] = fileErrors;
      error(`❌ ${file} is out-of-compliance:`);
      for (const err of fileErrors) {
        error(`  ${err}`);
        totalErrors++;
      }
    } else {
      log(`✅ ${file} OK`);
    }
  }

  return { totalErrors, findingsByFile, filesChecked: files.length };
}

module.exports = {
  runLint, loadRegistry, lintIcdContent, splitSections, normalizeSectionName,
  extractGuid, extractMkey, extractUpdateClass, hasNoWallClockDeclaration,
  collectAllGuids, REQUIRED_SECTIONS, VALID_UPDATE_CLASSES,
};

if (require.main === module) {
  let result;
  try {
    result = runLint();
  } catch (err) {
    console.error(`ERROR: icd-lint could not run: ${err.message}`);
    process.exit(1);
  }
  if (result.totalErrors > 0) {
    console.error(`❌ ICD-LINT FAILED: Found ${result.totalErrors} out-of-compliance finding(s). Aborting.`);
    process.exit(1);
  } else {
    console.log('✅ ICD-LINT PASSED: every ICD conforms to the template contract.');
    process.exit(0);
  }
}
