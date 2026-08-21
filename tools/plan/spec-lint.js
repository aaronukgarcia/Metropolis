/**
 * spec-lint.js — Static Acceptance Criteria Graph-Lint Tool (BUG-246)
 * Enforces Golden Rule #25: BAs must not write criteria referencing cross-module
 * interactions or Go API calls unless registered in code.json and implemented in Go.
 *
 * BUG-246 repair wave (2026-08-17):
 *   - SPEC-LINT-002 is now identifier-aware (declaration-form regex match) —
 *     the old whole-file `String.includes()` was satisfied by comments and
 *     longer identifiers and had never produced a finding.
 *   - SPEC-LINT-003 (WARNING, non-fatal): a cited Go package that IS a
 *     registered module (by key segment or registered path basename) but has
 *     no verifiable Go directory on disk is surfaced as a warning instead of
 *     being skipped silently.
 *   - Sentence-terminal citations ("...engine.finance.") are no longer
 *     skipped: trailing dots are stripped before lookup.
 *   - The module-key prefix set is derived FROM code.json at runtime (GR#15),
 *     not hardcoded.
 *   - Unmapped spec files are still skipped, but files whose name uses a real
 *     code.json key prefix are listed in a visible summary line.
 *   - Refactored into exported functions (runLint / goContentExportsSymbol /
 *     loadRegistry) so tests can prove each check CAN fail; CLI behavior
 *     (exit 1 on findings) is unchanged.
 *
 * BUG-246 fix round 2 (Destructive-BUG246-r1 REJECT, 2026-08-17):
 *
 *   FILE -> MODULE RESOLUTION (reject finding 2): filename-keyed mapping alone
 *   skipped 36 real specs whose filename differs from their registry key
 *   (e.g. feat.worklife.md is registered as engine.worklife). Resolution order:
 *     1. exact:  "<key>.md" where <key> is a registered module key;
 *     2. title:  the file's first markdown heading cites exactly ONE distinct
 *                registered key (e.g. engine.headless.md's title names
 *                engine.core) — content-derived, per the reject's guidance;
 *     3. suffix: the filename's portion after its first segment matches
 *                exactly ONE registered key's suffix (feat.worklife ->
 *                engine.worklife) — an alias map derived from code.json.
 *   Files that still resolve to nothing (BUG-nnn / SEC-nnn / README and features that
 *   exist only in the BOW, not the module registry) keep skipping, and
 *   real-prefix ones stay visible in the unmapped summary.
 *
 *   SPEC-LINT-001 SEMANTICS (reject finding 4 — documented here as required):
 *   a citation of module X by spec S (module M) passes iff an edge between
 *   M and X is registered ANYWHERE in code.json, in EITHER direction, on
 *   EITHER endpoint's record (M.outbound.calls∋X, M.inbound.consumers∋X,
 *   X.outbound.calls∋M, or X.inbound.consumers∋M). Rationale: acceptance
 *   prose does not distinguish "we call X" from "X calls us", so flagging a
 *   correctly-narrated consumer as a missing outbound edge is a false
 *   positive; but a relationship registered NOWHERE must still fail (GR#25).
 *   Investigation result for the reject's "why do 487 fire" question: the
 *   code.json graph is fully dual-recorded (every outbound call has a
 *   reciprocal consumer entry — verified 497/497 edges), so the previous
 *   per-module union was ALREADY equivalent to this relationship-level check
 *   for today's registry; the 487 flagged citations are relationships
 *   registered NOWHERE in the graph. That flood is registry staleness (the
 *   known ~292 unregistered Go-import edges awaiting the code.json
 *   regeneration in flight on the registry lane), not a lint defect, and per
 *   this rule's own semantics those citations MUST keep failing until the
 *   regenerated graph lands. The check is now additionally robust to a
 *   one-sidedly-recorded registry (edge present only on the cited module's
 *   record), which the old per-module-record union was not.
 *
 *   SPEC-LINT-004 (reject finding 1, WARNING, non-fatal): a citation whose
 *   key (after ancestor resolution — "engine.finance.tick" resolves to the
 *   registered ancestor "engine.finance") matches NO registered module key at
 *   all is a distinct finding class; it used to pass silently. Non-fatal
 *   because live prose legitimately references BOW-only feature names and
 *   planned-but-unregistered modules; fail-closed enforcement belongs to the
 *   registry regeneration, not this warning. Tokens that are plainly file
 *   references (a .md/.go/.json/... extension tail), bare prefixes with no
 *   second segment, and the spec's own filename key (for alias-mapped files)
 *   are excluded from this class by design.
 *
 *   EXEMPT_MODULES (reject finding 3): the declared exemption list is now
 *   validated against code.json at runtime — only declared entries that are
 *   REGISTERED keys take effect; dead entries (e.g. foundation.serialize,
 *   which is not a registry key) are dropped and reported so they cannot rot
 *   silently.
 *
 *   SPEC-LINT-002/003 GATING (reject finding 5): the method-citation regex is
 *   now identifier-aware. A dotted token `pkg.Method` enters the pipeline
 *   only when `pkg` is (a) the basename of a registered module's Go-tree
 *   path (derived from code.json — GR#15), searched across ALL registered
 *   dirs sharing that basename; or (b) a registered module key's last
 *   segment with no registered Go dir (SPEC-LINT-003: unverifiable). Go
 *   stdlib package names (a fixed language fact, not project data) are
 *   skipped UNLESS they collide with a registered path basename, in which
 *   case the registered module wins (e.g. "build" -> internal/engine/build,
 *   not go/build). A lookbehind rejects CamelCase tails ("Response.Payload"
 *   can no longer contribute "esponse.Payload"). Everything else — stdlib
 *   idioms like time.Now/json.Marshal, plain prose — never enters the
 *   pipeline.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_REPO_DIR = path.resolve(__dirname, '..', '..');

// Global exception/exemption list (e.g. foundation libraries are universally
// allowed). DECLARED here; the EFFECTIVE set is declared ∩ registered keys —
// see effectiveExemptions() (BUG-246 reject finding 3: foundation.serialize
// was declared but is not a registry key, so it could never match; dead
// entries are now dropped at runtime and surfaced in the lint output).
const EXEMPT_MODULES = new Set([
  'foundation.num', 'foundation.errors', 'foundation.registry', 'foundation.serialize', 'foundation.repo'
]);

// Go standard-library package basenames. These are language facts (stable,
// versioned with Go itself), NOT project data — GR#15 governs project-derived
// expectations, which all come from code.json above. Registered module path
// basenames always take precedence over this set (see the gating note in the
// header), so a project module named like a stdlib package is still checked.
const GO_STDLIB_PKG_NAMES = new Set((
  'bufio bytes cmp context crypto embed encoding errors expvar flag fmt hash html image io iter log maps math mime net os path plugin reflect regexp runtime slices sort strconv strings structs sync syscall testing time unicode unique unsafe weak ' +
  'json xml csv gob binary hex base64 ascii85 pem asn1 sha256 sha512 sha1 sha3 md5 hmac hkdf pbkdf2 aes cipher des dsa ecdh ecdsa ed25519 elliptic mlkem rand rc4 rsa subtle tls x509 boring fips140 ' +
  'ast build constant doc format importer parser printer scanner token types version comment ' +
  'bits big cmplx heap list ring color draw gif jpeg png suffixarray tabwriter template quick fstest iotest txtar race httptest cookiejar httputil pprof trace metrics cgo msan asan coverage exec signal user filepath url textproto smtp mail rpc jsonrpc netip http http2 utf8 utf16 atomic fs zip tar gzip zlib bzip2 lzw flate slog syslog maphash fnv crc32 crc64 adler32 dwarf elf macho pe plan9obj gosym xcoff heapdump v2'
).split(/\s+/));

// File-extension tails that mark a dotted token as a FILE reference, not a
// module-key citation (e.g. "see engine.finance.md", "engine.go").
const FILE_EXT_RE = /\.(?:md|go|json|js|ts|ya?ml|txt|csv|html|sql|bat|ps1|sh)$/;

/** Normalize a code.json module `path` into a posix dir string (primary
 * component of composites, no trailing slash), or null. Mirrors
 * codejson-audit.js's normalizeModulePath. */
function normalizeModulePath(rawPath) {
  if (!rawPath) return null;
  let s = String(rawPath).replace(/\\/g, '/').trim();
  if (s.includes(' + ')) s = s.split(' + ')[0].trim();
  if (s.includes(',')) s = s.split(',')[0].trim();
  if (!s) return null;
  if (s === '/') return '.';
  if (s.endsWith('/')) s = s.slice(0, -1);
  return s;
}

function isGoTreePath(p) {
  return !!p && (p === 'internal' || p === 'cmd' || p.startsWith('internal/') || p.startsWith('cmd/'));
}

/**
 * Load code.json and derive every registry-sourced structure the lint needs
 * (GR#15: expected values come from data files at runtime, never hardcoded
 * constants). Throws on a missing/unparseable registry — the CLI wrapper
 * turns that into exit 1. Returns:
 *   modulesByKey   — key -> module record
 *   keyPrefixes    — sorted distinct first segments of all keys
 *   edges          — Set of "from|to" directed edges from BOTH record sides
 *                    (outbound.calls AND inbound.consumers), so a
 *                    one-sidedly-recorded edge still counts
 *   pkgDirsByName  — Go package basename -> Set of registered Go-tree dirs
 *   keyLastSegs    — Set of last segments of all registered keys
 *   keysBySuffix   — key-suffix (portion after first segment) -> [keys]
 */
function loadRegistry(repoDir) {
  const codeJsonPath = path.join(repoDir, 'code.json');
  if (!fs.existsSync(codeJsonPath)) {
    throw new Error(`code.json not found at ${codeJsonPath}`);
  }
  const codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8'));
  const modulesByKey = {};
  for (const m of codeJson.modules || []) {
    modulesByKey[m.key] = m;
  }
  const keyPrefixes = [...new Set(Object.keys(modulesByKey).map(k => k.split('.')[0]))].sort();
  if (keyPrefixes.length === 0) {
    throw new Error(`code.json at ${codeJsonPath} contains no modules — cannot derive the key namespace`);
  }

  const edges = new Set();
  const pkgDirsByName = new Map();
  const keyLastSegs = new Set();
  const keysBySuffix = new Map();
  for (const m of Object.values(modulesByKey)) {
    for (const call of (m.outbound && m.outbound.calls) || []) {
      if (call && call.key) edges.add(`${m.key}|${call.key}`);
    }
    for (const cons of (m.inbound && m.inbound.consumers) || []) {
      if (cons && cons.key) edges.add(`${cons.key}|${m.key}`);
    }
    keyLastSegs.add(m.key.split('.').pop());
    const suffix = m.key.split('.').slice(1).join('.');
    if (suffix) {
      if (!keysBySuffix.has(suffix)) keysBySuffix.set(suffix, []);
      keysBySuffix.get(suffix).push(m.key);
    }
    const np = normalizeModulePath(m.path);
    if (isGoTreePath(np)) {
      const base = np.split('/').pop();
      if (!pkgDirsByName.has(base)) pkgDirsByName.set(base, new Set());
      pkgDirsByName.get(base).add(np);
    }
  }

  return { modulesByKey, keyPrefixes, edges, pkgDirsByName, keyLastSegs, keysBySuffix };
}

/** BUG-246 reject finding 3: the effective exemption set is the declared
 * EXEMPT_MODULES ∩ registered keys. Returns { effective:Set, dead:[names] }. */
function effectiveExemptions(modulesByKey, declared = EXEMPT_MODULES) {
  const effective = new Set();
  const dead = [];
  for (const k of declared) {
    if (modulesByKey[k]) effective.add(k);
    else dead.push(k);
  }
  return { effective, dead: dead.sort() };
}

/** SPEC-LINT-001 relationship test (semantics documented in the header):
 * true iff an edge between a and b is registered in EITHER direction on
 * EITHER endpoint's record. */
function edgeRegisteredEitherDirection(edges, a, b) {
  return edges.has(`${a}|${b}`) || edges.has(`${b}|${a}`);
}

/**
 * Resolve a raw mkey-regex token to a registered module key.
 * Returns one of:
 *   { kind: 'key', key }            — registered key (exact or nearest
 *                                     registered ancestor: "engine.finance.tick"
 *                                     -> "engine.finance")
 *   { kind: 'unregistered', token } — dotted, non-file token matching no
 *                                     registered key at any ancestor level
 *   { kind: 'skip' }                — not a key citation (file reference,
 *                                     bare prefix, empty)
 */
function resolveCitedKey(rawToken, modulesByKey) {
  const token = String(rawToken).replace(/\.+$/, '');
  if (!token || !token.includes('.')) return { kind: 'skip' };
  if (FILE_EXT_RE.test(token)) return { kind: 'skip' };
  let k = token;
  while (k.includes('.')) {
    if (modulesByKey[k]) return { kind: 'key', key: k };
    k = k.slice(0, k.lastIndexOf('.'));
  }
  return { kind: 'unregistered', token };
}

/**
 * Resolve a spec FILE to its registered module key (BUG-246 reject finding 2).
 * Order: exact filename key -> unique registered key in the first markdown
 * heading -> unique registered-key suffix alias. Returns
 * { key, via: 'exact'|'title'|'suffix' } or null.
 */
function resolveSpecFileModule(fileKey, content, registry) {
  const { modulesByKey, keysBySuffix, keyPrefixes } = registry;
  if (modulesByKey[fileKey]) return { key: fileKey, via: 'exact' };

  const mkeyRegex = new RegExp(`\\b(?:${keyPrefixes.join('|')})\\.[a-z0-9.-]+`, 'g');
  const titleLine = String(content).split(/\r?\n/).find(l => l.trim().startsWith('#')) || '';
  const titleKeys = [...new Set(
    (titleLine.match(mkeyRegex) || [])
      .map(t => t.replace(/\.+$/, ''))
      .filter(k => modulesByKey[k])
  )];
  if (titleKeys.length === 1) return { key: titleKeys[0], via: 'title' };

  const suffix = fileKey.split('.').slice(1).join('.');
  const suffixHits = suffix ? (keysBySuffix.get(suffix) || []) : [];
  if (suffixHits.length === 1) return { key: suffixHits[0], via: 'suffix' };

  return null;
}

/**
 * SPEC-LINT-002 symbol matcher — identifier-aware, NOT a substring scan.
 * A cited Go symbol `pkg.Method` counts as present only if the package's
 * source declares it in one of the real Go declaration forms:
 *   - `func Method(` / `func Method[` (plain function, incl. generics)
 *   - `func (r *Recv) Method(`         (method declaration)
 *   - `type Method` / `const Method` / `var Method`
 *   - a line-LEADING `Method ...` (grouped `const (`/`var (` block entries,
 *     struct fields, interface method lines — all real Go declaration forms
 *     that put the bare identifier first on its own line; a line-leading
 *     unqualified identifier in a package file must resolve in package scope
 *     to compile, so it is sound evidence the symbol exists there)
 * The old `goContent.includes(method)` matched comments and any longer
 * identifier containing the name (mid-line, no word boundary), so the check
 * could never fail (BUG-246). All forms here are word-boundary/line-anchored:
 * `// mentions DepositPool` and `DepositPoolLedger` no longer count.
 */
function goContentExportsSymbol(goContent, symbol) {
  // Symbol names come from the /[A-Z][A-Za-z0-9_]+/ capture, but escape
  // defensively so a malformed caller input can never build a rogue regex.
  const s = String(symbol).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const funcDeclRe = new RegExp(`\\bfunc\\s+(\\([^)]*\\)\\s*)?${s}\\s*[([]`);
  const typeDeclRe = new RegExp(`\\b(?:type|const|var)\\s+${s}\\b`);
  const lineLeadingRe = new RegExp(`(?:^|\\n)[ \\t]*${s}\\b`);
  return funcDeclRe.test(goContent) || typeDeclRe.test(goContent) || lineLeadingRe.test(goContent);
}

/**
 * Run the full lint. Options (all optional, defaults are the live repo):
 *   repoDir        — repository root containing code.json + Go trees
 *   acceptanceDir  — directory of acceptance *.md files
 *   log/warn/error — output sinks (console.* by default; injectable for tests)
 * Returns { totalErrors, totalWarnings, findingsByFile, warningsByFile,
 *           unmappedFiles, unmappedRealSpecs, aliasMappedFiles,
 *           deadExemptions, filesChecked }.
 * Never calls process.exit — the CLI wrapper owns exit codes.
 */
function runLint(opts = {}) {
  const repoDir = opts.repoDir || DEFAULT_REPO_DIR;
  const log = opts.log || console.log;
  const warn = opts.warn || console.warn;
  const error = opts.error || console.error;

  const registry = loadRegistry(repoDir);
  const { modulesByKey, keyPrefixes, edges, pkgDirsByName, keyLastSegs } = registry;
  const { effective: exemptKeys, dead: deadExemptions } = effectiveExemptions(modulesByKey);

  const acceptanceDir = opts.acceptanceDir || path.join(repoDir, 'docs', 'planning', 'acceptance');
  if (!fs.existsSync(acceptanceDir)) {
    log('No acceptance directory found, skipping spec-lint.');
    return { totalErrors: 0, totalWarnings: 0, findingsByFile: {}, warningsByFile: {}, unmappedFiles: [], unmappedRealSpecs: [], aliasMappedFiles: {}, deadExemptions, filesChecked: 0 };
  }

  const files = fs.readdirSync(acceptanceDir).filter(f => f.endsWith('.md'));
  let totalErrors = 0;
  let totalWarnings = 0;
  const findingsByFile = {};
  const warningsByFile = {};
  const unmappedFiles = [];
  const unmappedRealSpecs = [];
  const aliasMappedFiles = {};

  // Key-citation regex built from the RUNTIME prefix namespace (GR#15).
  // The old hardcoded (engine|feat|ui|int|protocol) list silently ignored
  // every data.*, tool.*, foundation.*, harness.*, ... citation.
  const mkeyRegex = new RegExp(`\\b(?:${keyPrefixes.join('|')})\\.[a-z0-9.-]+`, 'g');
  const realSpecFileRe = new RegExp(`^(?:${keyPrefixes.join('|')})\\.`);

  log(`=== RUNNING SPEC-LINT ON ${files.length} ACCEPTANCE FILES ===`);
  log(`(key namespace derived from code.json: ${keyPrefixes.join(', ')})`);
  if (deadExemptions.length > 0) {
    warn(`⚠️  EXEMPT_MODULES entries not registered in code.json (dropped, exempting nothing): ${deadExemptions.join(', ')}`);
  }
  log('');

  for (const file of files) {
    const filePath = path.join(acceptanceDir, file);
    const content = fs.readFileSync(filePath, 'utf8');

    // Resolve the spec file to a registered module (BUG-246 reject finding 2):
    // exact filename key, else (for real-prefix filenames only — BUG-*/SEC-*/
    // README keep skipping) title-line citation, else code.json suffix alias.
    const fileKey = file.slice(0, -3);
    let resolution = modulesByKey[fileKey] ? { key: fileKey, via: 'exact' } : null;
    if (!resolution && realSpecFileRe.test(fileKey)) {
      resolution = resolveSpecFileModule(fileKey, content, registry);
    }

    if (!resolution) {
      unmappedFiles.push(file);
      // A BUG-*/SEC-*/README file legitimately maps to no module key; a file
      // named with a real registry prefix (engine.*, feat.*, tool.*, ...) that
      // ALSO resolves to nothing by title/suffix is a visible coverage gap —
      // collected for the summary line below.
      if (realSpecFileRe.test(fileKey)) unmappedRealSpecs.push(file);
      warn(`⚠️  WARNING: Spec file "${file}" has no registered module in code.json (exact/title/suffix resolution all failed). Skipping graph checks.`);
      continue;
    }
    const currentMkey = resolution.key;
    const m = modulesByKey[currentMkey];
    if (resolution.via !== 'exact') {
      aliasMappedFiles[file] = { key: currentMkey, via: resolution.via };
    }

    const fileErrors = [];
    const fileWarnings = [];

    // --- CHECK 1: Dependency Citations (SPEC-LINT-001 / SPEC-LINT-004) ---
    // Semantics (BUG-246 reject finding 4 — full statement in the header):
    // a cited registered key passes iff an edge between this module and the
    // cited module is registered ANYWHERE in code.json, in EITHER direction,
    // on EITHER endpoint's record; a relationship registered NOWHERE fails.
    // A citation resolving to NO registered key at any ancestor level is
    // SPEC-LINT-004 (reject finding 1, warning).
    const mkeyMatches = new Set(content.match(mkeyRegex) || []);

    for (const rawCitedKey of mkeyMatches) {
      const resolved = resolveCitedKey(rawCitedKey, modulesByKey);
      if (resolved.kind === 'skip') continue;
      if (resolved.kind === 'unregistered') {
        // The spec's own filename key (alias-mapped files cite their own
        // name in the title) is not a dependency claim.
        if (resolved.token === fileKey) continue;
        fileWarnings.push(`[SPEC-LINT-004] UNREGISTERED KEY: Spec cites "${resolved.token}", which matches no registered module key in code.json (no ancestor segment registered either) — GR#25 prose must only reference registered modules.`);
        continue;
      }
      const citedKey = resolved.key;
      if (citedKey === currentMkey || exemptKeys.has(citedKey)) continue;
      if (!edgeRegisteredEitherDirection(edges, currentMkey, citedKey)) {
        fileErrors.push(`[SPEC-LINT-001] GRAPH VIOLATION: Spec cites dependency on "${citedKey}", but no edge between "${currentMkey}" and "${citedKey}" is registered in code.json in either direction!`);
      }
    }

    // --- CHECK 2: Go Method-Interface Invariant (SPEC-LINT-002 / -003) ---
    // Identifier-aware gating (BUG-246 reject finding 5): the lookbehind
    // rejects CamelCase tails ("Response.Payload" no longer yields
    // "esponse.Payload"), and only tokens whose package resolves to the
    // registry enter the pipeline — see the header's gating note.
    const methodRegex = /(?<![A-Za-z0-9_])([a-z0-9]+)\.([A-Z][A-Za-z0-9_]+)/g;
    let match;
    const seenCitations = new Set();
    const methodMatches = [];
    while ((match = methodRegex.exec(content)) !== null) {
      const citation = `${match[1]}.${match[2]}`;
      if (seenCitations.has(citation)) continue; // dedupe per file
      seenCitations.add(citation);
      methodMatches.push({ pkg: match[1], method: match[2] });
    }

    for (const { pkg, method } of methodMatches) {
      const registeredDirs = pkgDirsByName.has(pkg) ? [...pkgDirsByName.get(pkg)].sort() : [];

      if (registeredDirs.length > 0) {
        // Registered module package (registry wins over any stdlib name
        // collision, e.g. "build"). A short basename may be registered under
        // multiple layers — search ALL existing dirs: the symbol exists if
        // ANY of them declares it.
        const existingDirs = registeredDirs.filter(d => {
          const abs = path.join(repoDir, d);
          try { return fs.statSync(abs).isDirectory(); } catch { return false; }
        });
        if (existingDirs.length === 0) {
          fileWarnings.push(`[SPEC-LINT-003] UNVERIFIABLE PACKAGE: Spec cites Go symbol "${pkg}.${method}"; "${pkg}" is a registered module package (${registeredDirs.join(', ')}) but no such directory exists on disk yet — citation cannot be verified against any Go source.`);
          continue;
        }
        let found = false;
        for (const d of existingDirs) {
          let goFiles;
          try {
            goFiles = fs.readdirSync(path.join(repoDir, d)).filter(f => f.endsWith('.go'));
          } catch (e) {
            fileWarnings.push(`[SPEC-LINT-003] UNVERIFIABLE PACKAGE: package directory "${d}" could not be read (${e.message}) — citation "${pkg}.${method}" cannot be verified.`);
            continue;
          }
          for (const gf of goFiles) {
            const goContent = fs.readFileSync(path.join(repoDir, d, gf), 'utf8');
            if (goContentExportsSymbol(goContent, method)) { found = true; break; }
          }
          if (found) break;
        }
        if (!found) {
          fileErrors.push(`[SPEC-LINT-002] INTERFACE MISMATCH: Spec cites Go method "${pkg}.${method}", but no registered Go package directory for "${pkg}" (checked ${existingDirs.join(', ')}) declares this symbol (func/method/type/const/var declaration forms searched)!`);
        }
      } else if (GO_STDLIB_PKG_NAMES.has(pkg)) {
        // Go stdlib idiom (time.Now, json.Marshal, sync.Mutex, errors.Is...)
        // with no registered module dir of that name — never a module-key
        // citation; stays out of the pipeline entirely (reject finding 5).
        continue;
      } else if (keyLastSegs.has(pkg)) {
        // The name claims to be a registered module (matches a key's last
        // segment) but no registered Go-tree path has that basename — the
        // citation cannot be verified against Go source.
        fileWarnings.push(`[SPEC-LINT-003] UNVERIFIABLE PACKAGE: Spec cites Go symbol "${pkg}.${method}"; "${pkg}" matches a registered module key segment but no registered module has a Go directory named "${pkg}" — citation cannot be verified against any Go source.`);
      }
      // else: not a registered package, not stdlib, not a key segment —
      // plain prose / third-party token; not a module citation, skip.
    }

    if (fileErrors.length > 0) {
      findingsByFile[file] = fileErrors;
      error(`❌ ${file} is out-of-compliance:`);
      for (const err of fileErrors) {
        error(`  ${err}`);
        totalErrors++;
      }
      error('');
    }
    if (fileWarnings.length > 0) {
      warningsByFile[file] = fileWarnings;
      warn(`⚠️  ${file} has ${fileWarnings.length} warning(s):`);
      for (const w of fileWarnings) {
        warn(`  ${w}`);
        totalWarnings++;
      }
      warn('');
    }
  }

  const aliasEntries = Object.entries(aliasMappedFiles);
  if (aliasEntries.length > 0) {
    log(`\nℹ️  ALIAS-MAPPED SPEC FILES (${aliasEntries.length} filename≠key specs resolved to registered modules via title/suffix — BUG-246):`);
    for (const [f, info] of aliasEntries) log(`   ${f} -> ${info.key} (via ${info.via})`);
  }

  // Visibility summary for unmapped-but-real-looking spec files: these skip
  // ALL graph checks today. Skipping stays correct for BUG-*/SEC-*/README,
  // but a real module spec with no code.json key is an audit blind spot the
  // team must be able to see.
  if (unmappedRealSpecs.length > 0) {
    log(`\nℹ️  UNMAPPED REAL SPEC FILES (${unmappedRealSpecs.length} of ${unmappedFiles.length} skipped files use a real code.json key prefix but map to no registered module key — all graph checks were SKIPPED for these):`);
    log(`   ${unmappedRealSpecs.join(', ')}`);
  }

  log(`\n(spec-lint warnings: ${totalWarnings} — warnings are informational and never fail the lint)`);

  return {
    totalErrors,
    totalWarnings,
    findingsByFile,
    warningsByFile,
    unmappedFiles,
    unmappedRealSpecs,
    aliasMappedFiles,
    deadExemptions,
    filesChecked: files.length - unmappedFiles.length,
  };
}

module.exports = {
  runLint, loadRegistry, goContentExportsSymbol, EXEMPT_MODULES,
  effectiveExemptions, resolveCitedKey, resolveSpecFileModule,
  edgeRegisteredEitherDirection, GO_STDLIB_PKG_NAMES,
};

if (require.main === module) {
  let result;
  try {
    result = runLint();
  } catch (err) {
    console.error(`ERROR: spec-lint could not run: ${err.message}`);
    process.exit(1);
  }
  if (result.totalErrors > 0) {
    console.error(`❌ SPEC-LINT FAILED: Found ${result.totalErrors} out-of-compliance spec findings. Aborting commit.`);
    process.exit(1);
  } else {
    console.log('✅ SPEC-LINT PASSED: All specifications conform perfectly to the architectural graph and code interfaces.');
    process.exit(0);
  }
}
