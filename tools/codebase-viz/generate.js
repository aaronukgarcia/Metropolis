#!/usr/bin/env node
'use strict';
// Module key: tool.codebaseviz (FEAT-134) — root tooling, not game code.
//
// Codebase-visualisation generator. Reads the CURRENT working tree and emits a
// self-contained HTML page (tools/codebase-viz/index.html) with the data inlined,
// so the viz works by just opening the file (no CDN, no server, no build step).
//
// Sources (all derived from reality, none hardcoded):
//   - code.json                (modules: key, guid, seq, path, outbound.calls, inbound.consumers, ...)
//   - metro MariaDB BOW        (bow_items joined to bow_destructive_verdicts, by mkey)
//   - git                      (ls-files, diff HEAD, status --porcelain, rev-parse)
//   - the filesystem           (line counts of tracked + untracked code files)
//   - `go test ./... -count=1 -json`  (per-package pass/fail)
//
// Determinism: no wall-clock is emitted; every list is sorted; the emitted
// `headCommit` + `dirty` flag pin the tree the viz reflects.

const fs = require('fs');
const path = require('path');
const { execFileSync, spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..', '..');
const OUT = path.join(__dirname, 'index.html');
const MYSQL_BIN = process.env.MYSQL_BIN || 'C:\\Program Files\\MariaDB 12.2\\bin\\mysql.exe';
const GO_TEST_TIMEOUT_MS = 600000;

// File extensions that count as "code" for line counts / the build stage.
// Deliberately excludes docs (.md), GPG armor (.asc), test golden files (.golden),
// and VCS metadata, so `docs/`-anchored placeholder modules do not balloon.
const CODE_EXTS = new Set([
  '.go', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx',
  '.json', '.yaml', '.yml', '.toml', '.sql', '.proto',
  '.ps1', '.sh', '.bat', '.mod', '.sum', '.txt',
]);

// ── shell helpers ────────────────────────────────────────────────────────────

function run(cmd, args, opts) {
  // stdin ignored, stdout+stderr piped so a failing child's stderr is captured
  // (surfaced via the WARN line) rather than leaking its raw usage text (ASM-803).
  return execFileSync(cmd, args, Object.assign({ encoding: 'utf8', maxBuffer: 256 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] }, opts || {}));
}
function git(args) { return run('git', ['-C', ROOT].concat(args)); }
// Git shell-outs degrade: a non-git directory, an empty repo with no HEAD, or any
// other git failure must warn and yield '' rather than hard-fail the run (ASM-803).
// Only the first stderr line is echoed — git can dump a full usage screen on
// failure (e.g. `git diff HEAD` with no HEAD), which would drown the warning.
function tryGit(args) {
  try { return git(args); }
  catch (e) {
    const firstErr = String((e && e.stderr) || '').split(/\r?\n/)[0];
    process.stderr.write('WARN: git ' + args.join(' ') + ' failed (continuing with empty result)' + (firstErr ? ': ' + firstErr : '') + '\n');
    return '';
  }
}
function splitLines(s) { return String(s).replace(/\r\n/g, '\n').split('\n').filter((l) => l.length > 0); }
function extOf(p) { return path.extname(p).toLowerCase(); }
function isCodeFile(p) { return CODE_EXTS.has(extOf(p)); }

// Logical line count (strip a single trailing newline; CRLF-normalised).
function countLines(rel) {
  try {
    let c = fs.readFileSync(path.join(ROOT, rel), 'utf8').replace(/\r\n/g, '\n');
    if (c.endsWith('\n')) c = c.slice(0, -1);
    return c === '' ? 0 : c.split('\n').length;
  } catch (e) { return 0; }
}

// ── module path → file/dir claims ────────────────────────────────────────────
// code.json `path` values are loose: dirs ("internal/engine/core/"), single files
// ("claude-bow.js"), compound ("claude-bow.js + .claude/commands/sprint.md"),
// annotated placeholders ("cloud/ (planned — unbuilt)"), and "/" (repo root, claims nothing).
function parseClaims(raw) {
  const claims = [];
  for (let part of String(raw).split('+')) {
    part = part.trim().replace(/\s*\([^)]*\)\s*$/, '').trim().replace(/\\/g, '/');
    if (!part || part === '/' || part === '.') continue;
    if (part.endsWith('/')) part = part.slice(0, -1);
    const lastSeg = part.split('/').pop();
    claims.push({ kind: lastSeg.indexOf('.') !== -1 ? 'file' : 'dir', rel: part });
  }
  return claims;
}
function underClaims(relFile, claims) {
  relFile = relFile.replace(/\\/g, '/');
  for (const c of claims) {
    if (c.kind === 'file') { if (relFile === c.rel) return true; }
    else if (relFile === c.rel || relFile.indexOf(c.rel + '/') === 0) return true;
  }
  return false;
}

// ── BOW (single MariaDB round-trip) ──────────────────────────────────────────

function loadBow() {
  const args = [];
  if (process.env.METRO_DB_HOST) args.push('-h', process.env.METRO_DB_HOST);
  if (process.env.METRO_DB_PORT) args.push('-P', process.env.METRO_DB_PORT);
  args.push('-u', process.env.METRO_DB_USER || 'root');
  if (process.env.METRO_DB_PASSWORD) args.push('-p' + process.env.METRO_DB_PASSWORD);
  args.push('-N', '-B', process.env.METRO_DB_NAME || 'metro', '-e',
    'SELECT i.mkey, i.guid, i.code, i.status, ' +
    'IFNULL(SUM(v.verdict = \'accept\'), 0) AS accepts, ' +
    'IFNULL(SUM(v.verdict = \'reject\'), 0) AS rejects ' +
    'FROM bow_items i ' +
    'LEFT JOIN bow_destructive_verdicts v ON v.item_guid = i.guid ' +
    'WHERE i.mkey IS NOT NULL ' +
    'GROUP BY i.mkey, i.guid, i.code, i.status');
  try {
    const out = run(MYSQL_BIN, args);
    const map = {};
    for (const line of splitLines(out)) {
      const [mkey, guid, code, status, accepts, rejects] = line.split('\t');
      if (!mkey) continue;
      map[mkey] = { guid, code, status, accepts: Number(accepts) || 0, rejects: Number(rejects) || 0 };
    }
    return map;
  } catch (e) {
    process.stderr.write('WARN: BOW query failed (continuing without BOW/QA data): ' + e.message + '\n');
    return {};
  }
}

// ── git-derived file sets ────────────────────────────────────────────────────

function loadGitFiles() {
  const trackedAll = splitLines(tryGit(['ls-files']));
  const tracked = new Set(trackedAll);
  const untracked = splitLines(tryGit(['ls-files', '--others', '--exclude-standard']));
  const modified = splitLines(tryGit(['diff', 'HEAD', '--name-only']));
  const porcelain = splitLines(tryGit(['status', '--porcelain']));
  return { trackedAll, tracked, untracked, modified, porcelain };
}

// ── go test (single full-suite run) ──────────────────────────────────────────

function loadGoTest() {
  // `go test ./...` exits non-zero when any package fails, so we use spawnSync
  // (which does not throw on a non-zero exit) and parse stdout regardless.
  const r = spawnSync('go', ['test', './...', '-count=1', '-json'],
    { cwd: ROOT, encoding: 'utf8', maxBuffer: 512 * 1024 * 1024, timeout: GO_TEST_TIMEOUT_MS });
  const result = new Map(); // importPath -> 'pass' | 'fail'
  if (r.error) {
    process.stderr.write('WARN: go test ./... could not be spawned (marking test stage unmet): ' + r.error.message + '\n');
    return { ran: false, result };
  }
  for (const line of splitLines(r.stdout || '')) {
    let o;
    try { o = JSON.parse(line); } catch (e) { continue; }
    if (!o.Package) continue;
    if (o.Action === 'pass') result.set(o.Package, 'pass');
    else if (o.Action === 'fail') result.set(o.Package, 'fail');
  }
  if (result.size === 0) {
    process.stderr.write('WARN: go test ./... produced no package results (marking test stage unmet)\n');
    return { ran: false, result };
  }
  return { ran: true, result };
}

// ── acceptance docs (BA-story gate) ──────────────────────────────────────────
// Acceptance criteria live under docs/planning/acceptance/ and are named THREE
// ways (ASM-802): by module key (engine.citizens.md), by feature key
// (feat.maintenance.md), and by BOW code (BUG-011.md). The maintenance cluster
// was re-keyed from feature to module registration (ASM-806/ASM-842) but the
// files still carry their old feat.* names while their headers now declare the
// module's BOW code — e.g. feat.helicopters.md declares "BOW code: MOD-074"
// (engine.airunits). We index BOTH the basename and the header-declared BOW
// code; a module reaches BA story if ANY route resolves to an existing file.
// A missing/unreadable acceptance dir degrades to an empty index (BA-story gate
// simply unmet) rather than crashing the run (ASM-803).
function loadAcceptanceDocs() {
  const empty = { byName: new Set(), byBowCode: new Set() };
  const dir = path.join(ROOT, 'docs', 'planning', 'acceptance');
  let files;
  try { files = fs.readdirSync(dir); }
  catch (e) {
    process.stderr.write('WARN: acceptance dir unreadable (BA-story gate unmet): ' + e.message + '\n');
    return empty;
  }
  const byName = new Set();
  const byBowCode = new Set();
  for (const f of files) {
    if (!/\.md$/i.test(f) || f === 'README.md') continue;
    byName.add(f.replace(/\.md$/i, ''));
    let code = null;
    try {
      const head = fs.readFileSync(path.join(dir, f), 'utf8').split(/\r?\n/).slice(0, 5);
      for (const line of head) {
        const m = /^BOW code:\s*(\S+)/.exec(line.trim());
        if (m) { code = m[1]; break; }
      }
    } catch (e) { /* unreadable file: the filename index entry still counts */ }
    if (code) byBowCode.add(code);
  }
  return { byName, byBowCode };
}

// Shape helpers for the code.json consumers. code.json is generated from the
// master plan but is treated as untrusted input: syntactically-valid JSON with
// the wrong shape must degrade (WARN + continue) exactly like malformed JSON,
// never throw (FEAT-134).
function isPlainObject(v) { return v !== null && typeof v === 'object' && !Array.isArray(v); }
function describe(v) { try { return JSON.stringify(v); } catch (e) { return String(v); } }

// code.json is the primary data source; a malformed or missing file degrades to
// an empty module set rather than a raw JSON.parse crash (ASM-803). A file that
// parses but is not a JSON object (top-level `null`, an array, or a scalar) is
// equally malformed and degrades the same way (FEAT-134).
function loadCodeJson() {
  let parsed;
  try {
    parsed = JSON.parse(fs.readFileSync(path.join(ROOT, 'code.json'), 'utf8'));
  } catch (e) {
    process.stderr.write('WARN: code.json unreadable/invalid (continuing with empty module set): ' + e.message + '\n');
    return {};
  }
  if (!isPlainObject(parsed)) {
    process.stderr.write('WARN: code.json parsed to ' + (parsed === null ? 'null' : 'a non-object') + ' (continuing with empty module set)\n');
    return {};
  }
  return parsed;
}

// outbound.calls / inbound.consumers are arrays of edge references. Each entry
// must be an object with a non-empty string `key`; null or non-object entries
// (e.g. `[null]`) would crash the `c.key` read below, so they degrade away with
// a WARN, never a throw (FEAT-134).
function edgeKeys(arr) {
  const keys = [];
  if (!Array.isArray(arr)) return keys;
  for (const c of arr) {
    if (isPlainObject(c) && typeof c.key === 'string' && c.key !== '') keys.push(c.key);
    else process.stderr.write('WARN: code.json edge entry without a string `key` skipped: ' + describe(c) + '\n');
  }
  return keys;
}

// go.mod supplies the import-path prefix for go-test matching; a missing file
// degrades to an empty module name (go-test gates unmet) rather than ENOENT (ASM-803).
function loadModuleName() {
  try {
    return (fs.readFileSync(path.join(ROOT, 'go.mod'), 'utf8').match(/^module\s+(\S+)/m) || [])[1] || '';
  } catch (e) {
    process.stderr.write('WARN: go.mod unreadable (continuing with empty module name): ' + e.message + '\n');
    return '';
  }
}

// ── main ─────────────────────────────────────────────────────────────────────

function main() {
  const codejson = loadCodeJson();
  // Tolerate a missing/non-array `modules` and null/blank entries (ASM-803):
  // only object-shaped modules with a non-empty STRING key are rendered. A
  // non-string key (e.g. `{"key":123}`) would crash `m.key.split(...)` below,
  // so it degrades away with a WARN rather than throwing (FEAT-134).
  const modulesIn = [];
  if (Array.isArray(codejson.modules)) {
    for (const m of codejson.modules) {
      if (!isPlainObject(m)) continue;                                    // null/scalar/array entry: silent (unchanged)
      const k = m.key;
      if (k === undefined || k === null || k === '') continue;            // keyless/empty: silent (unchanged)
      if (typeof k !== 'string') {                                        // e.g. {"key":123}: would crash m.key.split below
        process.stderr.write('WARN: code.json module with non-string `key` skipped: ' + describe(k) + '\n');
        continue;
      }
      modulesIn.push(m);
    }
  }
  const moduleName = loadModuleName();

  const bow = loadBow();
  const g = loadGitFiles();
  const gt = loadGoTest();
  const acceptance = loadAcceptanceDocs();

  const modified = new Set(g.modified);
  const headCommit = tryGit(['rev-parse', 'HEAD']).trim();

  // line counts for every code file (tracked + untracked), computed once.
  const lineCounts = new Map();
  const codeFileSet = new Set();
  for (const f of g.trackedAll) if (isCodeFile(f)) codeFileSet.add(f);
  for (const f of g.untracked) if (isCodeFile(f)) codeFileSet.add(f);
  for (const f of codeFileSet) lineCounts.set(f, countLines(f));

  const moduleByKey = new Map();
  const modules = modulesIn.map((m) => {
    const claims = parseClaims(m.path);

    const codeFiles = [];
    for (const f of codeFileSet) if (underClaims(f, claims)) codeFiles.push(f);
    codeFiles.sort();
    const goFiles = codeFiles.filter((f) => extOf(f) === '.go');
    const testFiles = goFiles.filter((f) => /_test\.go$/.test(f));
    const codeLines = codeFiles.reduce((s, f) => s + (lineCounts.get(f) || 0), 0);
    const goLines = goFiles.reduce((s, f) => s + (lineCounts.get(f) || 0), 0);

    // BA-story gate (ASM-802): resolve the module's acceptance doc via all three
    // naming routes — (a) <module.key>.md, (b) feat.<name>.md (feature key; name
    // is the module key with its layer prefix stripped), (c) <BOW code>.md — plus
    // the header-declared BOW code, which covers files re-keyed from a feature key
    // to a module (feat.helicopters.md declares BOW code MOD-074 = engine.airunits).
    const bowRec = bow[m.key] || null;
    const nameStem = m.key.split('.')[1] || '';
    const hasAcceptance =
      acceptance.byName.has(m.key) ||                                     // (a) module key
      (nameStem !== '' && acceptance.byName.has('feat.' + nameStem)) ||   // (b) feature key
      (bowRec !== null && acceptance.byName.has(bowRec.code)) ||          // (c) BOW code (filename)
      (bowRec !== null && acceptance.byBowCode.has(bowRec.code));         // (c') BOW code (header-declared)

    // go-test failure across every package under this module's path.
    let goTestFail = false;
    for (const f of goFiles) {
      const dir = path.posix.dirname(f.replace(/\\/g, '/'));
      const pkg = moduleName + '/' + dir;
      const r = gt.result.get(pkg);
      if (r === 'fail') { goTestFail = true; break; }
    }
    const testPassed = testFiles.length > 0 && gt.ran && !goTestFail;

    // committed = some tracked file under path AND nothing differs from HEAD there.
    let trackedUnder = 0, modifiedUnder = 0;
    for (const f of g.trackedAll) {
      if (underClaims(f, claims)) { trackedUnder++; if (modified.has(f)) modifiedUnder++; }
    }
    const committed = trackedUnder > 0 && modifiedUnder === 0;

    const deps = Array.from(new Set(edgeKeys(m.outbound && m.outbound.calls))).sort();
    const consumers = Array.from(new Set(edgeKeys(m.inbound && m.inbound.consumers))).sort();

    const rec = {
      key: m.key,
      guid: m.guid,
      seq: m.seq != null ? m.seq : null,
      layer: m.layer || null,
      priority: m.priority || null,
      milestone: m.milestone || null,
      title: m.title || null,
      path: m.path,
      status: 'null', // set below
      codeFiles: codeFiles.length,
      codeLines,
      goFiles: goFiles.length,
      goLines,
      testFiles: testFiles.length,
      hasAcceptance,
      goTestFail,
      testPassed,
      committed,
      bow: bowRec,
      deps,
      consumers,
    };
    moduleByKey.set(m.key, rec);
    return rec;
  });

  // status pipeline (cumulative; see ASM for precedence). Non-Go modules skip the
  // Go-specific test/QA gates and go null → BA story → build → committed.
  for (const m of modules) {
    const isGo = m.goFiles > 0;
    if (!isGo) {
      if (m.committed) m.status = 'committed';
      else if (m.codeFiles > 0) m.status = 'build';
      else if (m.hasAcceptance) m.status = 'BA story';
      else m.status = 'null';
    } else {
      if (m.committed && m.testPassed && m.bow && m.bow.accepts > 0) m.status = 'committed';
      else if (m.testPassed && m.bow && m.bow.accepts > 0) m.status = 'QA';
      else if (m.testPassed) m.status = 'test';
      else m.status = 'build';
    }
  }
  modules.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));

  // ── edges (directed; outbound.calls ∪ reverse inbound.consumers) ───────────
  const edgeSet = new Map(); // "s->t" -> true
  for (const m of modules) {
    for (const t of m.deps) edgeSet.set(m.key + '->' + t, true);
    for (const s of m.consumers) edgeSet.set(s + '->' + m.key, true);
  }
  const edges = Array.from(edgeSet.keys()).map((e) => {
    const i = e.indexOf('->');
    return { s: e.slice(0, i), t: e.slice(i + 2) };
  }).sort((a, b) => (a.s + '->' + a.t).localeCompare(b.s + '->' + b.t));

  // ghost nodes: edge endpoints that are not code.json modules.
  const ghostNodes = Array.from(new Set(edges.reduce((acc, e) => {
    if (!moduleByKey.has(e.s)) acc.push(e.s);
    if (!moduleByKey.has(e.t)) acc.push(e.t);
    return acc;
  }, []))).sort();

  // ── lost & found ───────────────────────────────────────────────────────────
  // (a) tracked .go files not under ANY module's directory claim.
  const dirClaims = [];
  for (const m of modulesIn) for (const c of parseClaims(m.path)) if (c.kind === 'dir') dirClaims.push(c.rel);
  const orphanedGo = g.trackedAll
    .filter((f) => extOf(f) === '.go')
    .filter((f) => !dirClaims.some((d) => f === d || f.indexOf(d + '/') === 0))
    .sort();

  // (b) modules with zero working-tree code (planned, unbuilt).
  const unbuiltModules = modules.filter((m) => m.codeFiles === 0).map((m) => m.key);

  // (c) untracked files (porcelain ?? lines, verbatim).
  const untrackedFiles = g.porcelain.filter((l) => l.indexOf('??') === 0).map((l) => l.slice(3).trim()).sort();

  const data = {
    project: 'Metropolis',
    moduleName,
    headCommit,
    dirty: g.porcelain.length > 0,
    goTestRan: gt.ran,
    stats: {
      modules: modules.length,
      edges: edges.length,
      ghostNodes: ghostNodes.length,
      totalCodeLines: modules.reduce((s, m) => s + m.codeLines, 0),
      totalGoLines: modules.reduce((s, m) => s + m.goLines, 0),
      orphanedGo: orphanedGo.length,
      unbuiltModules: unbuiltModules.length,
      untrackedFiles: untrackedFiles.length,
      statusCounts: (() => {
        const c = {};
        for (const m of modules) c[m.status] = (c[m.status] || 0) + 1;
        return c;
      })(),
    },
    modules,
    edges,
    ghostNodes,
    orphanedGo,
    unbuiltModules,
    untrackedFiles,
  };

  const html = TEMPLATE.replace('__METRO_VIZ_DATA__', JSON.stringify(data).replace(/</g, '\\u003c'));
  fs.writeFileSync(OUT, html);
  process.stdout.write('Wrote ' + OUT + ' (' + html.length + ' bytes)\n');
  process.stdout.write('modules=' + modules.length + ' edges=' + edges.length +
    ' goLines=' + data.stats.totalGoLines + ' codeLines=' + data.stats.totalCodeLines +
    ' orphanedGo=' + orphanedGo.length + ' unbuilt=' + unbuiltModules.length +
    ' untracked=' + untrackedFiles.length + '\n');
}

// ── HTML template ────────────────────────────────────────────────────────────
// Self-contained: inline CSS/JS, no external requests. The page JS below is
// written without backticks or template literals so it survives embedding here.
const TEMPLATE = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Metropolis — codebase viz</title>
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; }
  body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; background: var(--page); color: var(--ink); }
  .viz {
    --surface: #fcfcfb; --page: #f9f9f7; --ink: #0b0b0b; --ink2: #52514e; --muted: #898781;
    --grid: #e1e0d9; --border: rgba(11,11,11,0.12); --edge: #b6b5af;
    --st-null: #d6d5cf; --st-null-ink: #3c3b37;
    --st-bastory: #a9cdf2; --st-bastory-ink: #14324f;
    --st-build: #4b87d0; --st-build-ink: #ffffff;
    --st-test: #2a78d6; --st-test-ink: #ffffff;
    --st-qa: #1c5cab; --st-qa-ink: #ffffff;
    --st-committed: #0d366b; --st-committed-ink: #ffffff;
    color-scheme: light;
    min-height: 100vh;
  }
  @media (prefers-color-scheme: dark) {
    :root:not([data-theme="light"]) .viz {
      color-scheme: dark;
      --surface: #1a1a19; --page: #0d0d0d; --ink: #ffffff; --ink2: #c3c2b7; --muted: #898781;
      --grid: #2c2c2a; --border: rgba(255,255,255,0.14); --edge: #4a4a47;
      --st-null: #3b3b38; --st-null-ink: #d6d5cf;
      --st-bastory: #29486e; --st-bastory-ink: #cfe0f5;
      --st-build: #2c5a90; --st-build-ink: #dbe8f8;
      --st-test: #3573b8; --st-test-ink: #ffffff;
      --st-qa: #5b96d9; --st-qa-ink: #0b1622;
      --st-committed: #9ec5f4; --st-committed-ink: #0b1622;
    }
  }
  :root[data-theme="dark"] .viz {
    color-scheme: dark;
    --surface: #1a1a19; --page: #0d0d0d; --ink: #ffffff; --ink2: #c3c2b7; --muted: #898781;
    --grid: #2c2c2a; --border: rgba(255,255,255,0.14); --edge: #4a4a47;
    --st-null: #3b3b38; --st-null-ink: #d6d5cf;
    --st-bastory: #29486e; --st-bastory-ink: #cfe0f5;
    --st-build: #2c5a90; --st-build-ink: #dbe8f8;
    --st-test: #3573b8; --st-test-ink: #ffffff;
    --st-qa: #5b96d9; --st-qa-ink: #0b1622;
    --st-committed: #9ec5f4; --st-committed-ink: #0b1622;
  }
  .st-null { background: var(--st-null); color: var(--st-null-ink); }
  .st-bastory { background: var(--st-bastory); color: var(--st-bastory-ink); }
  .st-build { background: var(--st-build); color: var(--st-build-ink); }
  .st-test { background: var(--st-test); color: var(--st-test-ink); }
  .st-qa { background: var(--st-qa); color: var(--st-qa-ink); }
  .st-committed { background: var(--st-committed); color: var(--st-committed-ink); }
  svg circle.st-null { fill: var(--st-null); }
  svg circle.st-bastory { fill: var(--st-bastory); }
  svg circle.st-build { fill: var(--st-build); }
  svg circle.st-test { fill: var(--st-test); }
  svg circle.st-qa { fill: var(--st-qa); }
  svg circle.st-committed { fill: var(--st-committed); }
  svg line.g-edge { stroke: var(--edge); }
  svg path.g-arrow { fill: var(--edge); }
  svg circle.g-ghost { stroke: var(--muted); }
  svg circle.g-ring { stroke: var(--surface); }
  svg text.g-lbl { fill: var(--ink2); }

  header { padding: 22px 28px 8px; max-width: 1400px; margin: 0 auto; }
  header h1 { margin: 0 0 4px; font-size: 22px; }
  header .sub { color: var(--ink2); font-size: 13px; }
  header .sub code { background: var(--surface); border: 1px solid var(--border); padding: 1px 6px; border-radius: 4px; }

  .controls { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; padding: 12px 28px; max-width: 1400px; margin: 0 auto; }
  .controls select, .controls input { font: inherit; font-size: 13px; padding: 5px 8px; background: var(--surface); color: var(--ink); border: 1px solid var(--border); border-radius: 6px; }
  .controls input { min-width: 200px; }

  .section { max-width: 1400px; margin: 0 auto; padding: 14px 28px 8px; }
  .section h2 { font-size: 15px; margin: 8px 0 4px; }
  .section .note { color: var(--muted); font-size: 12px; margin: 0 0 8px; }

  .legend { display: flex; flex-wrap: wrap; gap: 8px; margin: 8px 0; }
  .legend .chip { display: inline-flex; align-items: center; gap: 7px; padding: 5px 10px; border: 1px solid var(--border); border-radius: 20px; font-size: 12px; background: var(--surface); }
  .legend .swatch { width: 12px; height: 12px; border-radius: 3px; flex: 0 0 auto; }
  .legend .ord { color: var(--muted); font-size: 10px; font-variant-numeric: tabular-nums; }

  .panels { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; max-width: 1400px; margin: 0 auto; padding: 8px 28px 28px; }
  @media (max-width: 980px) { .panels { grid-template-columns: 1fr; } }
  .panel { border: 1px solid var(--border); border-radius: 10px; background: var(--surface); overflow: hidden; }
  .panel .ph { padding: 9px 14px; font-size: 13px; font-weight: 600; border-bottom: 1px solid var(--border); }
  .panel .body { position: relative; }
  .panel svg { display: block; width: 100%; height: 100%; }

  #graph { height: 560px; }
  #heatmap { height: 560px; }
  .hm-box { position: absolute; overflow: hidden; cursor: pointer; border: 1px solid var(--surface); }
  .hm-box .lbl { font-size: 11px; line-height: 1.1; padding: 2px 4px; overflow: hidden; }
  .hm-box.dim { opacity: 0.15; }

  .lostfound { max-width: 1400px; margin: 0 auto; padding: 8px 28px 30px; }
  .lf-grid { display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 16px; }
  @media (max-width: 980px) { .lf-grid { grid-template-columns: 1fr; } }
  .lf { border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .lf h3 { font-size: 13px; margin: 0; padding: 9px 14px; border-bottom: 1px solid var(--border); }
  .lf h3 .cnt { color: var(--muted); font-weight: 400; font-size: 11px; margin-left: 6px; }
  .lf ul { list-style: none; margin: 0; padding: 6px 14px 12px; max-height: 320px; overflow: auto; }
  .lf li { font-family: ui-monospace, "Cascadia Mono", Consolas, monospace; font-size: 12px; padding: 2px 0; border-bottom: 1px dashed var(--grid); }

  #tooltip { position: fixed; z-index: 50; pointer-events: none; max-width: 360px; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 8px 10px; font-size: 12px; box-shadow: 0 4px 18px rgba(0,0,0,0.18); display: none; }
  #tooltip .tt-key { font-weight: 700; font-family: ui-monospace, Consolas, monospace; }
  #tooltip .tt-row { margin-top: 3px; color: var(--ink2); }
  #tooltip .tt-row b { color: var(--ink); font-weight: 600; }

  #detail { max-width: 1400px; margin: 0 auto; padding: 4px 28px 24px; }
  #detail .box { border: 1px solid var(--border); border-radius: 10px; background: var(--surface); padding: 14px 16px; display: none; }
  #detail .box.open { display: block; }
  #detail h3 { margin: 0 0 8px; font-family: ui-monospace, Consolas, monospace; font-size: 15px; }
  #detail table { border-collapse: collapse; font-size: 12px; }
  #detail td { padding: 3px 10px 3px 0; vertical-align: top; }
  #detail td.k { color: var(--muted); white-space: nowrap; }
  .stage-check { display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; border-radius: 12px; font-size: 11px; margin: 2px 4px 2px 0; border: 1px solid var(--border); }
  .stage-check.ok { opacity: 1; }
  .stage-check.no { opacity: 0.35; text-decoration: line-through; }
  .stage-check .tick { font-weight: 700; }
  footer { max-width: 1400px; margin: 0 auto; padding: 8px 28px 30px; color: var(--muted); font-size: 12px; }
</style>
</head>
<body>
<div class="viz">
  <header>
    <h1>Metropolis — codebase viz</h1>
    <div class="sub" id="subhead"></div>
  </header>

  <div class="controls">
    <select id="layerFilter"></select>
    <input id="search" type="search" placeholder="filter module key (e.g. engine.core)">
    <span class="sub" id="activeCount"></span>
  </div>

  <div class="section">
    <h2>Status pipeline</h2>
    <div class="legend" id="legend"></div>
    <p class="note">null &rarr; BA story &rarr; build &rarr; test &rarr; QA &rarr; committed. A module shows the deepest stage it has genuinely reached (see tooltip for the full checklist).</p>
  </div>

  <div class="panels">
    <div class="panel">
      <div class="ph">Dependency graph (who depends on whom)</div>
      <div class="body"><svg id="graph" preserveAspectRatio="xMidYMid meet"></svg></div>
    </div>
    <div class="panel">
      <div class="ph">Heat map (area &prop; line count)</div>
      <div class="body" id="heatmap" style="position:relative;"></div>
    </div>
  </div>

  <div class="lostfound">
    <div class="lf-grid">
      <div class="lf"><h3>Orphaned Go files<span class="cnt" id="orphanCnt"></span></h3><ul id="orphanList"></ul></div>
      <div class="lf"><h3>Planned / unbuilt modules<span class="cnt" id="unbuiltCnt"></span></h3><ul id="unbuiltList"></ul></div>
      <div class="lf"><h3>Untracked files<span class="cnt" id="untrackedCnt"></span></h3><ul id="untrackedList"></ul></div>
    </div>
  </div>

  <div id="detail"><div class="box" id="detailBox"></div></div>
  <footer id="foot"></footer>
  <div id="tooltip"></div>
</div>
<script>
var DATA = __METRO_VIZ_DATA__;

var STATUS_ORDER = ['null', 'BA story', 'build', 'test', 'QA', 'committed'];
var STATUS_CLASS = ['st-null', 'st-bastory', 'st-build', 'st-test', 'st-qa', 'st-committed'];
var STATUS_DESC = {
  'null': 'no acceptance criteria and no code (plan entry only)',
  'BA story': 'acceptance criteria exist under docs/planning/acceptance/ (module key, feature key, or BOW code)',
  'build': 'code exists under the module path',
  'test': 'has *_test.go files and go test passes',
  'QA': 'a Destructive accept verdict is recorded for the module BOW item',
  'committed': 'code is in the committed tree with no uncommitted diff'
};

function clsFor(status) { return STATUS_CLASS[STATUS_ORDER.indexOf(status)] || 'st-null'; }

var MODULES = DATA.modules;
var BYKEY = {};
MODULES.forEach(function (m) { BYKEY[m.key] = m; });

// ---- shared selection / filtering ----
var activeSet = null; // null = all active
var searchTerm = '';

function isActive(key) {
  if (activeSet && !activeSet.has(key)) return false;
  if (searchTerm && key.indexOf(searchTerm) === -1) return false;
  return true;
}

function applyFilters() {
  renderLegend();
  renderGraph();
  renderHeatmap();
  var active = MODULES.filter(function (m) { return isActive(m.key); });
  document.getElementById('activeCount').textContent = active.length + ' / ' + MODULES.length + ' modules';
}

// ---- header / footer / legend ----
(function () {
  var sub = document.getElementById('subhead');
  sub.innerHTML = 'head <code>' + DATA.headCommit.slice(0, 8) + '</code>' +
    (DATA.dirty ? ' <span style="color:#d03b3b">(working tree dirty)</span>' : ' (clean)') +
    ' &middot; ' + DATA.stats.modules + ' modules &middot; ' + DATA.stats.edges + ' edges &middot; ' +
    DATA.stats.totalCodeLines.toLocaleString() + ' code lines (' + DATA.stats.totalGoLines.toLocaleString() + ' Go)';

  var foot = document.getElementById('foot');
  foot.textContent = 'Generated by tools/codebase-viz/generate.js from the working tree (code.json + BOW + git + go test). Areas are proportional to line counts, not literal 1px\u00b2=1line.';
})();

function renderLegend() {
  var el = document.getElementById('legend');
  el.innerHTML = '';
  STATUS_ORDER.forEach(function (s, i) {
    var n = 0;
    MODULES.forEach(function (m) { if (m.status === s && isActive(m.key)) n++; });
    var chip = document.createElement('span');
    chip.className = 'chip';
    chip.innerHTML = '<span class="swatch ' + clsFor(s) + '"></span><span class="ord">' + i + '</span>' +
      '<b>' + s + '</b> <span style="color:var(--muted)">(' + n + ')</span>';
    chip.title = STATUS_DESC[s];
    el.appendChild(chip);
  });
}

// ---- tooltip ----
var tip = document.getElementById('tooltip');
function showTip(html, x, y) {
  tip.innerHTML = html;
  tip.style.display = 'block';
  var w = tip.offsetWidth, h = tip.offsetHeight;
  tip.style.left = Math.min(x + 14, window.innerWidth - w - 8) + 'px';
  tip.style.top = Math.min(y + 14, window.innerHeight - h - 8) + 'px';
}
function hideTip() { tip.style.display = 'none'; }

function stageChecks(m) {
  var checks = [
    ['BA story', m.hasAcceptance],
    ['build', m.codeFiles > 0],
    ['test', m.testPassed],
    ['QA', !!(m.bow && m.bow.accepts > 0)],
    ['committed', m.committed]
  ];
  var html = '<div style="margin-top:6px">';
  checks.forEach(function (c) {
    var ok = c[1];
    html += '<span class="stage-check ' + (ok ? 'ok' : 'no') + ' ' + clsFor(c[0]) + '">' +
      '<span class="tick">' + (ok ? '\u2713' : '\u00d7') + '</span>' + c[0] + '</span>';
  });
  return html + '</div>';
}

function tipHTML(m) {
  var bow = m.bow ? (m.bow.code + ' (' + m.bow.status + ')') : '(no BOW item)';
  var h = '<div class="tt-key">' + m.key + '</div>';
  h += '<div class="tt-row">status: <b>' + m.status + '</b></div>';
  h += '<div class="tt-row">layer: <b>' + (m.layer || '-') + '</b> &middot; ' + (m.priority || '-') + ' &middot; ' + (m.milestone || '-') + '</div>';
  h += '<div class="tt-row">code lines: <b>' + m.codeLines.toLocaleString() + '</b> (' + m.codeFiles + ' files, ' + m.goLines.toLocaleString() + ' Go)</div>';
  h += '<div class="tt-row">go test: <b>' + (m.goFiles === 0 ? 'n/a (non-Go)' : (m.goTestFail ? 'FAILING' : (m.testPassed ? 'passing' : 'no tests'))) + '</b></div>';
  h += '<div class="tt-row">BOW: <b>' + bow + '</b></div>';
  if (m.deps.length) h += '<div class="tt-row">depends on: <b>' + m.deps.join(', ') + '</b></div>';
  if (m.consumers.length) h += '<div class="tt-row">consumed by: <b>' + m.consumers.join(', ') + '</b></div>';
  h += stageChecks(m);
  return h;
}

function showDetail(m) {
  var box = document.getElementById('detailBox');
  var h = '<h3>' + m.key + '</h3>';
  h += '<table>';
  h += '<tr><td class="k">guid</td><td>' + m.guid + '</td></tr>';
  h += '<tr><td class="k">path</td><td>' + m.path + '</td></tr>';
  h += '<tr><td class="k">seq</td><td>' + (m.seq == null ? '-' : m.seq) + '</td></tr>';
  h += '<tr><td class="k">layer</td><td>' + (m.layer || '-') + '</td></tr>';
  h += '<tr><td class="k">priority</td><td>' + (m.priority || '-') + '</td></tr>';
  h += '<tr><td class="k">milestone</td><td>' + (m.milestone || '-') + '</td></tr>';
  h += '<tr><td class="k">title</td><td>' + (m.title || '-') + '</td></tr>';
  h += '<tr><td class="k">status</td><td><b>' + m.status + '</b></td></tr>';
  h += '<tr><td class="k">code</td><td>' + m.codeFiles + ' files / ' + m.codeLines.toLocaleString() + ' lines (' + m.goLines.toLocaleString() + ' Go, ' + m.testFiles + ' test files)</td></tr>';
  h += '<tr><td class="k">BOW</td><td>' + (m.bow ? m.bow.code + ' (' + m.bow.status + '), accepts=' + m.bow.accepts + ', rejects=' + m.bow.rejects : 'no BOW item') + '</td></tr>';
  h += '<tr><td class="k">depends on</td><td>' + (m.deps.length ? m.deps.join(', ') : '(none)') + '</td></tr>';
  h += '<tr><td class="k">consumed by</td><td>' + (m.consumers.length ? m.consumers.join(', ') : '(none)') + '</td></tr>';
  h += '</table>' + stageChecks(m);
  box.innerHTML = h;
  box.className = 'box open';
  box.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
}

// ---- dependency graph (deterministic force layout) ----
var graphSvg = document.getElementById('graph');
var graphNodes = [];
var graphPos = {};
var G_W = 1100, G_H = 620;

function buildGraphNodes() {
  graphNodes = [];
  var keys = Object.keys(BYKEY);
  keys.forEach(function (k) { graphNodes.push({ id: k, ghost: false }); });
  DATA.ghostNodes.forEach(function (k) { graphNodes.push({ id: k, ghost: true }); });
  // deterministic initial ring placement by sorted index
  var n = graphNodes.length;
  graphNodes.forEach(function (nd, i) {
    var a = (i / n) * Math.PI * 2;
    var r = 200 + (i % 5) * 22;
    graphPos[nd.id] = { x: G_W / 2 + Math.cos(a) * r, y: G_H / 2 + Math.sin(a) * r };
  });
}

function runForce() {
  var edges = DATA.edges;
  var idx = {};
  graphNodes.forEach(function (nd, i) { idx[nd.id] = i; });
  var N = graphNodes.length;
  var area = G_W * G_H;
  var k = Math.sqrt(area / N); // ideal spacing
  var k2 = k * k;
  var iters = 320;
  var t = 1.0;
  for (var it = 0; it < iters; it++) {
    var fx = new Array(N).fill(0);
    var fy = new Array(N).fill(0);
    // repulsion
    for (var i = 0; i < N; i++) {
      for (var j = i + 1; j < N; j++) {
        var dx = graphPos[graphNodes[j].id].x - graphPos[graphNodes[i].id].x;
        var dy = graphPos[graphNodes[j].id].y - graphPos[graphNodes[i].id].y;
        var d2 = dx * dx + dy * dy;
        if (d2 < 0.01) { dx = (i - j) * 0.1 + 0.01; dy = (i % 7) - 3; d2 = dx * dx + dy * dy; }
        var d = Math.sqrt(d2);
        var f = k2 / d2 * 6.0;
        var ux = dx / d, uy = dy / d;
        fx[i] -= ux * f; fy[i] -= uy * f;
        fx[j] += ux * f; fy[j] += uy * f;
      }
    }
    // springs
    for (var e = 0; e < edges.length; e++) {
      var a = idx[edges[e].s], b = idx[edges[e].t];
      if (a === undefined || b === undefined) continue;
      var dx = graphPos[graphNodes[b].id].x - graphPos[graphNodes[a].id].x;
      var dy = graphPos[graphNodes[b].id].y - graphPos[graphNodes[a].id].y;
      var d = Math.sqrt(dx * dx + dy * dy) || 0.01;
      var f = (d - k) * 0.06;
      var ux = dx / d, uy = dy / d;
      fx[a] += ux * f; fy[a] += uy * f;
      fx[b] -= ux * f; fy[b] -= uy * f;
    }
    // centering + damping
    for (var i = 0; i < N; i++) {
      var id = graphNodes[i].id;
      var px = graphPos[id].x, py = graphPos[id].y;
      fx[i] += (G_W / 2 - px) * 0.004;
      fy[i] += (G_H / 2 - py) * 0.004;
      fx[i] *= t; fy[i] *= t;
      graphPos[id] = { x: Math.max(20, Math.min(G_W - 20, px + fx[i])), y: Math.max(20, Math.min(G_H - 20, py + fy[i])) };
    }
    t = Math.max(0.04, t * 0.985);
  }
}

function radiusFor(m) {
  if (!m) return 5;
  var r = Math.sqrt(m.codeLines) / 4 + 4;
  return Math.max(5, Math.min(42, r));
}

var graphVB = { x: 0, y: 0, w: G_W, h: G_H };
var dragState = { active: false, sx: 0, sy: 0 };
function initGraphInteractions() {
  var svg = graphSvg;
  svg.addEventListener('wheel', function (ev) {
    ev.preventDefault();
    var f = ev.deltaY < 0 ? 1.1 : 1 / 1.1;
    var cx = graphVB.x + graphVB.w / 2, cy = graphVB.y + graphVB.h / 2;
    graphVB.w = Math.max(120, Math.min(4000, graphVB.w * f));
    graphVB.h = graphVB.w * G_H / G_W;
    graphVB.x = cx - graphVB.w / 2; graphVB.y = cy - graphVB.h / 2;
    renderGraph();
  });
  svg.addEventListener('mousedown', function (ev) { dragState.active = true; dragState.sx = ev.clientX; dragState.sy = ev.clientY; });
  window.addEventListener('mouseup', function () { dragState.active = false; });
  svg.addEventListener('mousemove', function (ev) {
    if (!dragState.active) return;
    var scale = graphVB.w / svg.clientWidth;
    graphVB.x -= (ev.clientX - dragState.sx) * scale;
    graphVB.y -= (ev.clientY - dragState.sy) * scale;
    dragState.sx = ev.clientX; dragState.sy = ev.clientY;
    svg.setAttribute('viewBox', graphVB.x + ' ' + graphVB.y + ' ' + graphVB.w + ' ' + graphVB.h);
  });
}
function renderGraph() {
  var svg = graphSvg;
  svg.innerHTML = '';
  svg.setAttribute('viewBox', graphVB.x + ' ' + graphVB.y + ' ' + graphVB.w + ' ' + graphVB.h);

  var defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
  var mk = document.createElementNS('http://www.w3.org/2000/svg', 'marker');
  mk.setAttribute('id', 'arrow'); mk.setAttribute('viewBox', '0 0 10 10');
  mk.setAttribute('refX', 10); mk.setAttribute('refY', 5);
  mk.setAttribute('markerWidth', 5); mk.setAttribute('markerHeight', 5); mk.setAttribute('orient', 'auto');
  var mp = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  mp.setAttribute('d', 'M 0 0 L 10 5 L 0 10 z'); mp.setAttribute('class', 'g-arrow');
  mk.appendChild(mp); defs.appendChild(mk); svg.appendChild(defs);

  var g = document.createElementNS('http://www.w3.org/2000/svg', 'g');

  // edges (only among active nodes)
  var activeKeys = {};
  graphNodes.forEach(function (nd) { activeKeys[nd.id] = isActive(nd.id); });
  var edgeCount = 0;
  DATA.edges.forEach(function (e) {
    if (!activeKeys[e.s] || !activeKeys[e.t]) return;
    var a = graphPos[e.s], b = graphPos[e.t];
    if (!a || !b) return;
    var line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    line.setAttribute('x1', a.x); line.setAttribute('y1', a.y);
    line.setAttribute('x2', b.x); line.setAttribute('y2', b.y);
    line.setAttribute('class', 'g-edge'); line.setAttribute('stroke-width', 1);
    line.setAttribute('opacity', 0.5); line.setAttribute('marker-end', 'url(#arrow)');
    g.appendChild(line);
    edgeCount++;
  });

  graphNodes.forEach(function (nd) {
    if (!activeKeys[nd.id]) return;
    var p = graphPos[nd.id];
    var m = BYKEY[nd.id];
    var status = m ? m.status : 'null';
    var r = nd.ghost ? 4 : radiusFor(m);
    var circ = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    circ.setAttribute('cx', p.x); circ.setAttribute('cy', p.y); circ.setAttribute('r', r);
    circ.setAttribute('class', nd.ghost ? 'st-null g-ghost' : clsFor(status) + ' g-ring');
    if (nd.ghost) { circ.setAttribute('stroke-dasharray', '2 2'); }
    else { circ.setAttribute('stroke-width', 1.5); }
    g.appendChild(circ);
    if (!nd.ghost) {
      var lbl = document.createElementNS('http://www.w3.org/2000/svg', 'text');
      lbl.setAttribute('x', p.x); lbl.setAttribute('y', p.y + r + 11);
      lbl.setAttribute('text-anchor', 'middle');
      lbl.setAttribute('font-size', r >= 9 ? 9 : 8);
      lbl.setAttribute('class', 'g-lbl');
      lbl.setAttribute('font-family', 'ui-monospace, Consolas, monospace');
      lbl.textContent = m.key;
      g.appendChild(lbl);
    }
    circ.addEventListener('mousemove', function (ev) { showTip(tipHTML(m || { key: nd.id, status: 'ghost' }), ev.clientX, ev.clientY); });
    circ.addEventListener('mouseleave', hideTip);
    circ.addEventListener('click', function () { if (m) showDetail(m); });
  });

  svg.appendChild(g);
}

// ---- heat map (squarified treemap) ----
function squarify(items, x, y, w, h) {
  // Squarified treemap (Bruls/Huizing/van Wijk). Areas are line counts; we rescale
  // them so their total equals the container's pixel area, then lay rows along the
  // longer side while the greedy row-growth minimises the worst aspect ratio.
  var total = 0;
  for (var i = 0; i < items.length; i++) total += items[i].area;
  var scale = Math.sqrt((w * h) / total);
  var arr = items.slice().sort(function (a, b) { return b.area - a.area; })
    .map(function (it) { return { id: it.id, area: it.area * scale * scale }; });
  var out = [];
  function worst(row, run, sum) {
    var max = -Infinity, min = Infinity;
    for (var i = 0; i < row.length; i++) { max = Math.max(max, row[i].area); min = Math.min(min, row[i].area); }
    var s2 = sum * sum, l2 = run * run;
    return Math.max((l2 * max) / s2, s2 / (l2 * min));
  }
  function layout(list, x0, y0, w0, h0) {
    if (!list.length) return;
    if (list.length === 1) { out.push({ id: list[0].id, x: x0, y: y0, w: w0, h: h0 }); return; }
    var horizontal = w0 >= h0;
    var run = horizontal ? w0 : h0;
    var row = [list[0]], sum = list[0].area, i = 1;
    while (i < list.length) {
      var trial = row.concat([list[i]]);
      var trialSum = sum + list[i].area;
      if (worst(trial, run, trialSum) <= worst(row, run, sum)) { row = trial; sum = trialSum; i++; }
      else break;
    }
    var rest = list.slice(i);
    if (horizontal) {
      var th = sum / w0;
      var cx = x0;
      for (var r = 0; r < row.length; r++) { var rw = row[r].area / th; out.push({ id: row[r].id, x: cx, y: y0, w: rw, h: th }); cx += rw; }
      layout(rest, x0, y0 + th, w0, h0 - th);
    } else {
      var tw = sum / h0;
      var cy = y0;
      for (var r2 = 0; r2 < row.length; r2++) { var rh = row[r2].area / tw; out.push({ id: row[r2].id, x: x0, y: cy, w: tw, h: rh }); cy += rh; }
      layout(rest, x0 + tw, y0, w0 - tw, h0);
    }
  }
  layout(arr, x, y, w, h);
  return out;
}

function renderHeatmap() {
  var host = document.getElementById('heatmap');
  host.innerHTML = '';
  var W = host.clientWidth || 700, H = 560;
  var items = [];
  MODULES.forEach(function (m) {
    if (!isActive(m.key)) return;
    items.push({ id: m.key, area: Math.max(m.codeLines, 1) });
  });
  var rects = squarify(items, 0, 0, W, H);
  var byId = {};
  rects.forEach(function (r) { byId[r.id] = r; });

  rects.forEach(function (r) {
    var m = BYKEY[r.id];
    var box = document.createElement('div');
    box.className = 'hm-box ' + clsFor(m.status);
    box.style.left = r.x + 'px'; box.style.top = r.y + 'px';
    box.style.width = Math.max(2, r.w - 1) + 'px'; box.style.height = Math.max(2, r.h - 1) + 'px';
    var showLabel = r.w > 34 && r.h > 16;
    var lbl = document.createElement('div');
    lbl.className = 'lbl';
    lbl.textContent = showLabel ? m.key : '';
    box.appendChild(lbl);
    box.addEventListener('mousemove', function (ev) { showTip(tipHTML(m), ev.clientX, ev.clientY); });
    box.addEventListener('mouseleave', hideTip);
    box.addEventListener('click', function () { showDetail(m); });
    host.appendChild(box);
  });
}

// ---- lost & found ----
(function () {
  document.getElementById('orphanCnt').textContent = '(' + DATA.orphanedGo.length + ')';
  document.getElementById('unbuiltCnt').textContent = '(' + DATA.unbuiltModules.length + ')';
  document.getElementById('untrackedCnt').textContent = '(' + DATA.untrackedFiles.length + ')';
  function fill(id, list) {
    var ul = document.getElementById(id);
    ul.innerHTML = '';
    if (!list.length) { var li = document.createElement('li'); li.textContent = '(none)'; li.style.border = 'none'; ul.appendChild(li); return; }
    list.forEach(function (s) { var li = document.createElement('li'); li.textContent = s; ul.appendChild(li); });
  }
  fill('orphanList', DATA.orphanedGo);
  fill('unbuiltList', DATA.unbuiltModules);
  fill('untrackedList', DATA.untrackedFiles);
})();

// ---- controls ----
(function () {
  var layers = [];
  MODULES.forEach(function (m) { if (layers.indexOf(m.layer) === -1) layers.push(m.layer); });
  layers.sort();
  var sel = document.getElementById('layerFilter');
  var opt = document.createElement('option'); opt.value = ''; opt.textContent = 'all layers'; sel.appendChild(opt);
  layers.forEach(function (l) { var o = document.createElement('option'); o.value = l; o.textContent = l; sel.appendChild(o); });
  sel.addEventListener('change', function () {
    activeSet = sel.value ? new Set(MODULES.filter(function (m) { return m.layer === sel.value; }).map(function (m) { return m.key; })) : null;
    applyFilters();
  });
  document.getElementById('search').addEventListener('input', function (ev) {
    searchTerm = ev.target.value.trim();
    applyFilters();
  });
})();

// ---- boot ----
buildGraphNodes();
runForce();
renderLegend();
renderGraph();
renderHeatmap();
initGraphInteractions();
</script>
</body>
</html>
`;

main();
