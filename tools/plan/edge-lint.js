/**
 * tools/plan/edge-lint.js — Reverse-direction code.json edge lint (BUG-482).
 *
 * WHY THIS EXISTS: `tools/plan/codejson-audit.js` (FEAT-062) only checks the
 * FORWARD direction — every code.json-declared outbound call edge must be
 * backed by a real Go import (AC-3). It does NOT check the reverse: a real
 * Go import, or a "(edge A->B)" header annotation, between two REGISTERED
 * modules that has NO corresponding outbound edge in code.json. That
 * direction is exactly the drift class BUG-478 found and fixed by hand
 * (engine.attract/market/consumption imported internal/foundation/serialize
 * and carried "(edge X->int.serializer)" header comments, but code.json had
 * no such edge) — and the BUG-478 independent round proved it: deleting a
 * real edge from a scratch code.json left codejson-audit/astgate/spec-lint/
 * generate.test all unchanged (GR#25 drift is invisible to every existing
 * gate in that direction).
 *
 * WHAT THIS DOES (read-only, report-only — GR#3, mirrors codejson-audit.js):
 *   1. Maps every Go-tree directory (internal/**, cmd/**) that a registered
 *      code.json module's `path` resolves to, back to that module's key —
 *      REUSING codejson-audit.js's normalizeModulePath/isGoTreePath/
 *      runAstinfo helpers (never a second resolver, per GR#3/the brief).
 *   2. For every such directory, runs the SAME astinfo helper codejson-audit
 *      uses (a real go/ast parse, not a grep) to get its non-test-file
 *      import set, and resolves each in-module-tree import back to the
 *      registered module key(s) it belongs to.
 *   3. Separately, greps every non-test .go file under internal/ and cmd/
 *      for "(edge A->B)" / "(edge A→B)" header annotations (the exact
 *      spelling used by internal/engine's participant.go files — both the
 *      ASCII "->" and the Unicode "→" arrow are accepted; annotation content
 *      may wrap across a `//` comment line break, as engine.finance's does).
 *   4. EDGE-LINT-001: an import-backed or header-declared A->B between two
 *      registered modules with no matching entry in A's code.json
 *      `outbound.calls`.
 *   5. EDGE-LINT-002: a header annotation naming a module key (on either
 *      side of the arrow) that does not exist in code.json at all (the
 *      typo class).
 *   6. A directory can carry MORE THAN ONE registered key when a module and
 *      one of its features share a package directory (documented, common —
 *      e.g. internal/engine/citizens is both `engine.citizens` and
 *      `feat.deathwave`). An import into such a directory is import-backed
 *      as long as ANY of the keys sharing that directory appears in the
 *      importer's outbound.calls — this avoids false positives the code
 *      itself cannot distinguish (the Go compiler sees one package, not
 *      which registry key "owns" the import).
 *
 * BASELINE (tools/plan/edge-lint-baseline.json): the real repo already has
 * reverse-direction findings that predate this tool (the 2026-08-17
 * 292-missing-import finding was never fully closed). Per BUG-482's
 * instruction this lint does NOT hand-edit code.json and does NOT get
 * weakened to go quiet — every finding is always printed — but only a
 * finding NOT present in the baseline fails the gate (exit 1). The baseline
 * is an explicit, reviewable allowlist of (from,to,source) tuples; burning
 * it down is separate follow-up work, not this lint's job.
 *
 * NEVER hand-edits code.json, the master plan, or Go source.
 *
 * Usage:
 *   node tools/plan/edge-lint.js [--json <path>] [--quiet]
 *     --json <path>  also write the full JSON report to <path>
 *     --quiet        suppress the human-readable report on stdout
 *
 * Exit code: 0 when every finding is in the baseline, 1 on any NEW finding
 * (not in the baseline) or a run error. This IS a gate (unlike
 * codejson-audit.js, which is report-only) — see BUG-482.
 */

'use strict';

const fs = require('fs');
const path = require('path');

const {
  normalizeModulePath,
  isGoTreePath,
  runAstinfo,
} = require('./codejson-audit.js');

const ROOT = path.resolve(__dirname, '..', '..');
const CODE_JSON_PATH = path.join(ROOT, 'code.json');
const GO_MOD_PATH = path.join(ROOT, 'go.mod');
const BASELINE_PATH = path.join(__dirname, 'edge-lint-baseline.json');

// ── small utilities ────────────────────────────────────────────────────────

function readGoModule(rootDir) {
  const gomod = fs.readFileSync(path.join(rootDir, 'go.mod'), 'utf8');
  const m = gomod.match(/^module\s+(\S+)/m);
  if (!m) throw new Error('go.mod: cannot find a "module <path>" line');
  return m[1];
}

/** Recursively collect non-test *.go files under `dir` (sorted, deterministic,
 * relative-posix paths). Mirrors units-lint.js's collectGoFiles, plus the
 * test-file exclusion astinfo's analyzeDir also applies. */
function collectNonTestGoFiles(rootDir, dir, out) {
  const abs = path.join(rootDir, dir);
  let entries;
  try { entries = fs.readdirSync(abs, { withFileTypes: true }); } catch { return; }
  for (const e of entries) {
    if (e.isDirectory()) {
      collectNonTestGoFiles(rootDir, path.join(dir, e.name), out);
    } else if (e.isFile() && e.name.endsWith('.go') && !e.name.endsWith('_test.go')) {
      out.push(path.join(dir, e.name).replace(/\\/g, '/'));
    }
  }
}

// A module-key token: lowercase alnum segments joined by '.', e.g.
// "engine.attract", "int.serializer", "foundation.det". Matches the
// convention every code.json `key` in this repo actually uses.
const MODULE_KEY_RE = /^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9-]*)+$/;

/** Extract every "(edge A->B)"/"(edge A→B)" annotation from `text` (one Go
 * file's full source). The paren content may wrap across a `//` comment
 * line break (engine.finance's does) — `[^)]` already matches newlines in a
 * JS regex without the `s` flag, so the capture just needs `//` and
 * whitespace stripped before the two sides are pulled apart. Returns
 * [{from, to, raw}], `from`/`to` only populated when they parse as
 * module-key-shaped tokens (the "(edge prereq -> dependent)" prose comment
 * in unlock_trees.go, which is not a module citation, is filtered out here
 * by construction — neither side is module-key-shaped). */
function extractEdgeAnnotations(text) {
  const found = [];
  const re = /\(edge\s+([^)]+)\)/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const cleaned = m[1].replace(/\/\//g, ' ').replace(/\s+/g, ' ').trim();
    const parts = cleaned.split(/→|->/);
    if (parts.length < 2) continue;
    const from = parts[0].trim();
    // Only the token immediately after the arrow is the target; anything
    // past a comma/space-separated trailer ("int.serializer, registered
    // a6293cb") is annotation prose, not part of the key.
    const toRaw = parts[1].trim();
    const toMatch = toRaw.match(/^[a-z][a-z0-9.-]*/i);
    const to = toMatch ? toMatch[0].replace(/[.-]+$/, '') : toRaw;
    found.push({ from, to, raw: m[0] });
  }
  return found;
}

// ── main lint ───────────────────────────────────────────────────────────────

/**
 * Runs the full lint and returns the report object. `opts.repoDir` allows
 * test isolation against a synthetic repo tree (units-lint.js precedent);
 * every real invocation uses the live repo.
 */
function runLint(opts = {}) {
  const rootDir = opts.repoDir || ROOT;
  const codeJsonPath = opts.codeJsonPath || path.join(rootDir, 'code.json');
  const codeJson = JSON.parse(fs.readFileSync(codeJsonPath, 'utf8'));
  const modules = codeJson.modules;
  const byKey = new Map(modules.map(m => [m.key, m]));
  const goModule = readGoModule(rootDir);

  // dir -> Set<key> (a module and one of its features commonly share a
  // package directory — see header comment point 6).
  const dirToKeys = new Map();
  for (const m of modules) {
    const np = normalizeModulePath(m.path);
    if (!isGoTreePath(np)) continue;
    if (!fs.existsSync(path.join(rootDir, np))) continue;
    if (!dirToKeys.has(np)) dirToKeys.set(np, new Set());
    dirToKeys.get(np).add(m.key);
  }

  /** Canonical representative key for a shared directory: prefer a
   * non-"feat."-prefixed key (the module, not a feature increment) so
   * findings read naturally; falls back to alphabetically-first. */
  function canonicalKeyFor(dir) {
    const keys = [...(dirToKeys.get(dir) || [])].sort();
    const nonFeat = keys.find(k => !k.startsWith('feat.'));
    return nonFeat || keys[0] || null;
  }

  const callsSetByKey = new Map();
  for (const m of modules) {
    callsSetByKey.set(m.key, new Set(((m.outbound && m.outbound.calls) || []).map(c => c.key)));
  }

  // NOTE: codejson-audit's runAstinfo() shells `go run ./tools/plan/astinfo`
  // with a cwd hardcoded to the REAL repo root, so it only resolves real
  // directories under the real repo — fine for both real runs and the
  // baseline-deletion test (which uses a scratch code.json but the real Go
  // tree). A synthetic-Go-tree test would need its own resolver; this lint's
  // header-annotation check (below) is deliberately independent of astinfo
  // for exactly that reason, so it CAN be tested against a scratch repoDir.
  const dirs = [...dirToKeys.keys()].sort();
  const astinfo = dirs.length ? runAstinfo(dirs) : new Map();

  const findings = []; // {code, from, to, source, detail}
  let importBackedOkCount = 0;
  let unregisteredImportCount = 0;
  const unregisteredImportSamples = [];

  // ── import-backed edges ────────────────────────────────────────────────
  for (const dir of dirs) {
    const info = astinfo.get(dir);
    if (!info || info.error) continue; // unparseable dir — out of scope, not this lint's job
    const fromKeys = [...dirToKeys.get(dir)];
    for (const imp of info.imports) {
      if (!imp.startsWith(goModule + '/')) continue; // stdlib/3rd-party — not a module edge
      const rel = imp.slice(goModule.length + 1);
      const targetKeys = dirToKeys.get(rel);
      if (!targetKeys || targetKeys.size === 0) {
        unregisteredImportCount++;
        if (unregisteredImportSamples.length < 25) unregisteredImportSamples.push({ from: dir, importPath: imp });
        continue;
      }
      // Self-import across a shared directory's OTHER key (e.g.
      // engine.citizens importing something that resolves back to its own
      // dir, also registered as feat.deathwave) is not a cross-module edge.
      const isSelfDir = fromKeys.some(fk => targetKeys.has(fk));
      if (isSelfDir) continue;
      // ANY key sharing the FROM directory carrying the edge is sufficient
      // (point 6): a feature sharing its parent module's package directory
      // does not need its own duplicate outbound.calls entry when the
      // parent module already declares the edge — the Go compiler sees one
      // package, not which registry key "owns" a given import.
      const hasEdge = fromKeys.some(fk => {
        const calls = callsSetByKey.get(fk) || new Set();
        return [...targetKeys].some(tk => calls.has(tk));
      });
      if (hasEdge) { importBackedOkCount++; continue; }
      const from = canonicalKeyFor(dir);
      const to = canonicalKeyFor(rel);
      findings.push({
        code: 'EDGE-LINT-001', from, to, source: 'import',
        detail: `${dir} (module ${[...fromKeys].sort().join('/')}) imports ${imp} (module ${[...targetKeys].sort().join('/')}) — no outbound.calls edge ${from}->${to} in code.json (checked all co-located keys on both sides)`,
      });
    }
  }

  // ── header "(edge A->B)" annotations, scanned repo-wide ────────────────
  const allGoFiles = [];
  for (const top of ['internal', 'cmd']) {
    if (fs.existsSync(path.join(rootDir, top))) collectNonTestGoFiles(rootDir, top, allGoFiles);
  }
  allGoFiles.sort();
  let headerAnnotationCount = 0;
  for (const rel of allGoFiles) {
    const text = fs.readFileSync(path.join(rootDir, rel), 'utf8');
    for (const ann of extractEdgeAnnotations(text)) {
      if (!MODULE_KEY_RE.test(ann.from) || !MODULE_KEY_RE.test(ann.to)) continue; // not a module-key citation (e.g. "prereq -> dependent")
      headerAnnotationCount++;
      const fromExists = byKey.has(ann.from);
      const toExists = byKey.has(ann.to);
      if (!fromExists) {
        findings.push({ code: 'EDGE-LINT-002', key: ann.from, side: 'from', file: rel, detail: `${rel}: "${ann.raw}" — "${ann.from}" is not a registered code.json module key` });
      }
      if (!toExists) {
        findings.push({ code: 'EDGE-LINT-002', key: ann.to, side: 'to', file: rel, detail: `${rel}: "${ann.raw}" — "${ann.to}" is not a registered code.json module key` });
      }
      if (fromExists && toExists) {
        const calls = callsSetByKey.get(ann.from) || new Set();
        if (!calls.has(ann.to)) {
          findings.push({
            code: 'EDGE-LINT-001', from: ann.from, to: ann.to, source: 'header',
            detail: `${rel}: "${ann.raw}" declares ${ann.from}->${ann.to} but code.json has no such outbound.calls edge`,
          });
        }
      }
    }
  }

  return {
    goModule,
    dirsScanned: dirs.length,
    goFilesScannedForAnnotations: allGoFiles.length,
    headerAnnotationCount,
    importBackedOkCount,
    unregisteredImportCount,
    unregisteredImportSamples,
    findings,
  };
}

// ── baseline handling (BUG-482 point 6) ────────────────────────────────────

function findingIdentity(f) {
  if (f.code === 'EDGE-LINT-001') return `EDGE-LINT-001|${f.from}->${f.to}|${f.source}`;
  return `EDGE-LINT-002|${f.key}|${f.side}`;
}

function loadBaseline(baselinePath) {
  if (!fs.existsSync(baselinePath)) return { entries: [] };
  return JSON.parse(fs.readFileSync(baselinePath, 'utf8'));
}

/** Splits findings into {baselined, newFindings} using findingIdentity. */
function partitionAgainstBaseline(findings, baseline) {
  const baselineIds = new Set((baseline.entries || []).map(findingIdentity));
  const baselined = [];
  const newFindings = [];
  for (const f of findings) {
    if (baselineIds.has(findingIdentity(f))) baselined.push(f);
    else newFindings.push(f);
  }
  return { baselined, newFindings };
}

// ── rendering ───────────────────────────────────────────────────────────────

function renderReport(result, partition) {
  const lines = [];
  lines.push('# Reverse-direction code.json edge lint (BUG-482)');
  lines.push('');
  lines.push(`- go.mod module: \`${result.goModule}\``);
  lines.push(`- registered Go-tree directories scanned for imports: ${result.dirsScanned}`);
  lines.push(`- import-backed edges OK (registered->registered, edge present): ${result.importBackedOkCount}`);
  lines.push(`- imports into UNREGISTERED internal packages (not a module drift finding — printed for coverage): ${result.unregisteredImportCount}`);
  lines.push(`- non-test .go files scanned for "(edge A->B)" header annotations: ${result.goFilesScannedForAnnotations}`);
  lines.push(`- header annotations found (module-key-shaped): ${result.headerAnnotationCount}`);
  lines.push('');
  lines.push(`## Findings — ${result.findings.length} total (${partition.baselined.length} baselined, ${partition.newFindings.length} NEW)`);
  for (const f of result.findings) {
    const inBaseline = partition.baselined.includes(f);
    lines.push(`- [${f.code}]${inBaseline ? ' (baselined)' : ' (NEW)'} ${f.detail}`);
  }
  if (result.unregisteredImportSamples.length) {
    lines.push('');
    lines.push(`## Sample imports into unregistered internal packages (first ${result.unregisteredImportSamples.length})`);
    for (const s of result.unregisteredImportSamples) lines.push(`- ${s.from} imports ${s.importPath}`);
  }
  return lines.join('\n');
}

// ── CLI ─────────────────────────────────────────────────────────────────────

function cli() {
  const args = process.argv.slice(2);
  const flags = {};
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === '--quiet') flags.quiet = true;
    else if (a.startsWith('--')) { flags[a.slice(2)] = args[++i]; }
  }

  const result = runLint();
  const baseline = loadBaseline(BASELINE_PATH);
  const partition = partitionAgainstBaseline(result.findings, baseline);

  const report = { ...result, baselineEntryCount: (baseline.entries || []).length, newFindingCount: partition.newFindings.length };
  if (flags.json) fs.writeFileSync(flags.json, JSON.stringify(report, null, 2) + '\n', 'utf8');
  if (!flags.quiet) console.log(renderReport(result, partition));

  if (partition.newFindings.length > 0) {
    console.error(`\n❌ EDGE-LINT FAILED: ${partition.newFindings.length} NEW finding(s) not in the baseline (tools/plan/edge-lint-baseline.json).`);
    process.exit(1);
  }
  console.log(`\n✅ EDGE-LINT PASSED: 0 new findings (${partition.baselined.length} pre-existing, baselined).`);
  process.exit(0);
}

module.exports = {
  runLint, extractEdgeAnnotations, MODULE_KEY_RE, findingIdentity,
  loadBaseline, partitionAgainstBaseline, renderReport, readGoModule,
};

if (require.main === module) {
  try {
    cli();
  } catch (err) {
    console.error(err.stack || String(err));
    process.exit(1);
  }
}
