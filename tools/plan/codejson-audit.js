/**
 * tools/plan/codejson-audit.js — FEAT-062: code.json <-> Go-tree bidirectional
 * consistency audit (docs/planning/acceptance/plan.pipeline.md).
 *
 * Supersedes `.claude/commands/codejson-audit.md` item 4 ("source coverage")
 * only; items 1-3 of that skill (plan validity, plan/code.json drift, BOW
 * mirror spot-check) remain that skill's job.
 *
 * WHAT THIS DOES (read-only, report-only — GR#3):
 *   Direction A (registry -> code): every code.json module path resolves on
 *     disk (AC-1); every contract/GUID corresponds to a real exported Go
 *     symbol (AC-2), with GUID well-formedness + global uniqueness; every
 *     registered call edge corresponds to a real Go import (AC-3, via a real
 *     go/ast parse of imports, not a text grep).
 *   Direction B (code -> registry): every Go-bearing directory under
 *     internal/ and cmd/ is covered by a code.json entry, exactly or via a
 *     documented parent/child relationship (AC-4).
 *   Direction C: name/type character-for-character matching, separating
 *     "does not resolve at all" (AC-2) from "resolves to something similar
 *     but not identical" (AC-6, near-miss).
 *   Direction D (code -> registry, BUG-327): every real module-crossing Go
 *     import from a git-tracked .go file under internal/ or cmd/ must
 *     correspond to a registered outbound.calls edge. Attribution is
 *     ANY-owner: a directory
 *     resolves to every entry registering its nearest registered ancestor,
 *     and an import counts as registered if ANY owner of the importer has an
 *     edge to ANY owner of the imported. The only benign case is importer and
 *     imported resolving to the SAME registered path (a sibling package
 *     inside one module). Not gated by BOW status — an import that exists on
 *     disk needs no permission to be flagged.
 *
 * THE "EXPECTED TO EXIST" GATE (binding, see the acceptance doc's
 * Definitions section): a module is only faulted for a MISSING path/symbol/
 * edge if its live BOW status (queried fresh from the metro MariaDB, never a
 * hardcoded sprint cutoff — GR#15) is `done`. Everything else is
 * "not-yet-built" and reported separately, never as a Direction-A finding.
 * Direction B (orphan directories) is existence-only and is NOT gated by
 * status — code that exists on disk needs no permission to be flagged as
 * unregistered.
 *
 * NEVER HAND-EDITS code.json, master-plan-v2.1.json, or any Go source (AC-7/
 * AC-8) — every finding names its fix route explicitly as either
 * "master-plan-fix" (edit master-plan-v2.1.json, regenerate) or
 * "code-side-defect" (a separate BOW item against the Go source), per AC-9.
 * BOW filing (AC-10, one item per drift CLASS, BUG-058 precedent) only
 * happens when invoked with --file; the default run never writes to the BOW.
 *
 * KNOWN LIMITATION (BUG-184, accepted residual risk, not fixed):
 *   The AC-7/AC-8 self-check below is a point-in-time snapshot compare — one
 *   hash+git-status read taken before the run, one taken after. It proves
 *   the guarded paths (code.json, master-plan-v2.1.json, internal/, cmd/)
 *   were byte-identical at both instants, but it is structurally blind to a
 *   mutate-then-revert cycle that both starts AND completes inside the
 *   run's ~1s window (e.g. mutate at t=250ms, revert to the original bytes
 *   at t=400ms) — reproduced against code.json and multiple Go files.
 *   Exploiting this requires an attacker who already knows the run's
 *   internal timing (DB round-trip + `go run` duration) closely enough to
 *   land both the mutation and the revert inside that window — a much
 *   narrower bar than BUG-181's gap (any concurrent process touching a
 *   file at any point during the run, closed by the two-snapshot design
 *   itself). This tool is a local CI/dev-tooling report-only gate (GR#3,
 *   exit code always 0), not a live security boundary protecting a
 *   production system or untrusted input — the realistic threat is a
 *   misbehaving concurrent process or script, not a timing-precise
 *   adversary. Accepted as residual risk rather than built out into
 *   periodic polling; revisit only if the threat model changes (e.g. this
 *   tool starts gating an untrusted/adversarial CI path).
 *
 * Usage:
 *   node tools/plan/codejson-audit.js [--json <path>] [--md <path>] [--file] [--quiet]
 *
 *   --json <path>  also write the full JSON report to <path>
 *   --md <path>    also write the human-readable Markdown report to <path>
 *   --file         file findings to the BOW (one item per populated class,
 *                  AC-10) — OFF by default; the audit is report-only unless
 *                  this is explicitly passed.
 *   --quiet        suppress the Markdown report on stdout (JSON/--json still work)
 *
 * Exit code is always 0 — this is a report, not a gate (AC list has no pass/
 * fail exit-code criterion; a non-zero exit belongs to a future CI wiring
 * decision, out of FEAT-062's scope).
 */

'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execFileSync, spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..', '..');
const CODE_JSON_PATH = path.join(ROOT, 'code.json');
const MASTER_PLAN_PATH = path.join(ROOT, 'docs', 'planning', 'master-plan-v2.1.json');
const GO_MOD_PATH = path.join(ROOT, 'go.mod');
const ASTINFO_DIR = path.join(__dirname, 'astinfo');

// ── small utilities ────────────────────────────────────────────────────────

function sha256File(p) {
  return crypto.createHash('sha256').update(fs.readFileSync(p)).digest('hex');
}

function readGoModule() {
  const gomod = fs.readFileSync(GO_MOD_PATH, 'utf8');
  const m = gomod.match(/^module\s+(\S+)/m);
  if (!m) throw new Error('go.mod: cannot find a "module <path>" line');
  return m[1];
}

/** Normalize a code.json `path` field into a single posix-style directory
 * string with no trailing slash. Composite entries (e.g. "internal/harness/
 * replay/ + fixtures/") are reduced to their PRIMARY (first) component —
 * the audit records the raw string alongside for a reviewer to see, but
 * only the primary component participates in filesystem/Go checks. "/"
 * (repo root, e.g. foundation.repo) normalizes to "." — a real, always-
 * existing path, not a null/absent one. Returns null only for a genuinely
 * empty path field. */
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

function isGoTreePath(normPath) {
  return !!normPath && (normPath === 'internal' || normPath === 'cmd' ||
    normPath.startsWith('internal/') || normPath.startsWith('cmd/'));
}

// ── BOW status (live query — GR#15: never a hardcoded sprint cutoff) ──────

/** Returns a Map<mkey, {code, status}> read fresh from the metro MariaDB.
 * This is the "equivalent direct query" the acceptance doc allows as an
 * alternative to shelling `claude-bow.js show <mkey>` once per module
 * (106 round trips) — same table (`bow_items`), same `status` column
 * `claude-bow.js show` reads, one round trip. */
async function loadBowStatuses() {
  const mysql = require('mysql2/promise');
  const conn = await mysql.createConnection({
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
  });
  try {
    const [rows] = await conn.query('SELECT mkey, code, status FROM bow_items WHERE mkey IS NOT NULL');
    const byKey = new Map();
    for (const r of rows) byKey.set(r.mkey, { code: r.code, status: r.status });
    return byKey;
  } finally {
    await conn.end();
  }
}

// ── Go introspection via tools/plan/astinfo (real go/ast parse) ───────────

/** Runs the astinfo helper once over the full set of directories needed,
 * batched into a single `go run` invocation for determinism/perf. Returns
 * Map<dir, {error?, exported: [{name,kind,file,line}], imports: [string]}>. */
function runAstinfo(dirs) {
  const uniq = [...new Set(dirs)].sort();
  const result = new Map();
  if (uniq.length === 0) return result;
  const proc = spawnSync('go', ['run', './tools/plan/astinfo', ...uniq], {
    cwd: ROOT,
    encoding: 'utf8',
    maxBuffer: 64 * 1024 * 1024,
  });
  if (proc.status !== 0) {
    throw new Error(`astinfo helper failed (exit ${proc.status}): ${proc.stderr || proc.stdout}`);
  }
  const parsed = JSON.parse(proc.stdout);
  for (const dir of uniq) result.set(dir, parsed[dir] || { error: 'astinfo returned no entry' });
  return result;
}

// ── Go-bearing directory sweep (Direction B) ───────────────────────────────

/** Recursively lists every directory under `root` (relative posix paths,
 * sorted) that directly contains at least one .go file (test or non-test —
 * AC-4's language is "at least one .go file", no test/non-test carve-out). */
function findGoBearingDirs(root) {
  const found = [];
  function walk(rel) {
    const abs = path.join(ROOT, rel);
    let entries;
    try { entries = fs.readdirSync(abs, { withFileTypes: true }); } catch { return; }
    let hasGo = false;
    const subdirs = [];
    for (const e of entries) {
      if (e.isDirectory()) subdirs.push(e.name);
      else if (e.isFile() && e.name.endsWith('.go')) hasGo = true;
    }
    if (hasGo) found.push(rel.replace(/\\/g, '/'));
    for (const d of subdirs.sort()) walk(path.join(rel, d));
  }
  walk(root);
  return found.sort();
}

// ── Go import parsing (Direction D, BUG-327: code -> registry edge check) ──

/** Strips line (`//…`) and block (`/* … *​/`) comments from Go source, returning
 * the comment-free text. Block comments may span lines; an UNCLOSED block
 * comment consumes the rest of the source (that file wouldn't compile, so no
 * valid import follows it). */
function stripComments(src) {
  const s = String(src);
  let out = '';
  let i = 0;
  while (i < s.length) {
    if (s[i] === '/' && s[i + 1] === '/') {
      const nl = s.indexOf('\n', i);
      if (nl === -1) break;
      out += '\n';
      i = nl + 1;
    } else if (s[i] === '/' && s[i + 1] === '*') {
      const end = s.indexOf('*/', i + 2);
      if (end === -1) break; // unclosed block comment — rest is comment
      i = end + 2;
    } else {
      out += s[i];
      i++;
    }
  }
  return out;
}

/** Parses the import paths out of a single Go source file's text. Robust to
 * non-canonical-but-compilable forms gofmt would normalise (same-line
 * `import ("x")` / `import("x")`, no-space `import"x"`, and a `(` split onto
 * its own line): it extracts every quoted path literal in the import section
 * — everything before the first top-level func/type/const/var declaration —
 * after comments are stripped. Aliased/dot/blank imports (`alias "x"`,
 * `. "x"`, `_ "x"`) work because only the quoted path is captured. */
function parseGoImports(src) {
  const clean = stripComments(src);
  const imports = [];
  let inSection = false;
  for (const raw of clean.split('\n')) {
    const line = raw.trim();
    if (!line) continue;
    if (!inSection) {
      if (/^import\b/.test(line)) inSection = true;
      else if (/^(func|type|const|var)\b/.test(line)) break; // declaration before any import
      else continue;
    } else if (/^(func|type|const|var)\b/.test(line)) {
      break; // past the import section
    }
    const re = /"([^"]+)"/g;
    let m;
    while ((m = re.exec(line)) !== null) imports.push(m[1]);
  }
  return imports;
}

/** Lists git-TRACKED .go files under the given repo-relative root (posix
 * paths, sorted) — tracked-only so another lane's scratch/backup files never
 * pollute the audit (mirrors the AC-7/AC-8 self-check's git-status scope). */
function listTrackedGoFiles(rootRel) {
  const out = execFileSync('git', ['ls-files', '--', rootRel], { cwd: ROOT, encoding: 'utf8', maxBuffer: 64 * 1024 * 1024 });
  return out.split('\n').map(s => s.trim()).filter(f => f.endsWith('.go')).sort();
}

// ── Direction-D attribution (BUG-327): ANY-owner, not single-winner ────────

/** Deterministic tie-break when several registered entries share one path:
 * the module-type entry wins over interface-type, which wins over feature-
 * type; remaining ties break alphabetically by key. Used only for the
 * canonical from/to LABEL on a finding — the registered/benign DECISION is
 * ANY-owner (see isImportRegistered), never this single winner. */
const BOW_TYPE_RANK = { module: 0, interface: 1, feature: 2 };

/** Resolves a Go directory to its owning entries: the nearest registered
 * ancestor path (exact path or longest registered prefix) and the SET of
 * module keys that register exactly that path. Returns null for an
 * unregistered directory (Direction B's orphan-directory finding owns that
 * case). `registeredDirList` must be sorted longest-path-first so the first
 * prefix hit is the deepest. */
function resolveDirOwners(registeredDirList, ownersByDir, dir) {
  for (const rd of registeredDirList) {
    if (dir === rd || dir.startsWith(rd + '/')) {
      return { path: rd, owners: ownersByDir.get(rd) };
    }
  }
  return null;
}

/** ANY-owner registration test (BUG-327): an import is registered iff SOME
 * owner of the importing directory has an outbound.calls edge to SOME owner
 * of the imported directory. This is what fixes the single-winner
 * false-positive — several entries legitimately share one path
 * (engine.citizens and feat.deathwave on internal/engine/citizens), and an
 * edge the FEATURE registers must count as much as one the MODULE registers. */
function isImportRegistered(fromOwners, toOwners, registeredOutbound) {
  for (const f of fromOwners) {
    const outs = registeredOutbound.get(f);
    if (!outs) continue;
    for (const t of toOwners) {
      if (outs.has(t)) return true;
    }
  }
  return false;
}

/** Canonical single-key label for a directory's owner set (module over
 * interface over feature, then alphabetical) — display-only, never part of
 * the registered/benign decision. */
function primaryOwner(ownerSet, byKey) {
  let best = null;
  for (const k of ownerSet) {
    const m = byKey.get(k);
    const rank = (m && m.bowType in BOW_TYPE_RANK) ? BOW_TYPE_RANK[m.bowType] : 3;
    if (best === null || rank < best.rank || (rank === best.rank && k < best.key)) {
      best = { key: k, rank };
    }
  }
  return best ? best.key : [...ownerSet].sort()[0];
}

/** BUG-327 benign-sibling rule (two attacker rounds, 2026-08-21): an import is
 * benign ONLY when importer and imported resolve to the same registered path
 * AND the IMPORTED side is the LITERAL registered path (`relTarget ===
 * toPath`) — i.e. a subpackage importing its own module root (e.g.
 * internal/harness/replay/gen -> internal/harness/replay). Two distinct
 * unregistered siblings under one coarse ancestor are NOT benign (neither side
 * is literal); and — second attacker round — a module root importing a brand-
 * new unregistered child is NOT benign either: that child gets no orphan
 * finding (Direction B classifies it as a documented child-of), so the import
 * is the only signal that a registration is missing. Only the "descendant
 * imports its module root" direction is exempt. */
function isBenignSibling(fromPath, relTarget, toPath) {
  return fromPath === toPath && relTarget === toPath;
}

// ── name-matching helpers (Direction C) ────────────────────────────────────

function normalizeIdentForNearMiss(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]/g, '');
}

const UUID_V4_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

// ── fix-route labelling (AC-9) — one of exactly two labels per finding ────
// Assigned per drift CLASS (deterministic, documented), matching the worked
// examples in the acceptance doc's AC-9 text verbatim:
//   missing-path                    -> master-plan-fix  (plan lists a path renamed/typo'd on disk)
//   duplicate-guid                  -> master-plan-fix  (a plan/generator data-integrity defect)
//   orphan-directory                -> master-plan-fix  (code exists; the plan has no entry for it yet)
//   contract-does-not-resolve       -> code-side-defect (an exported symbol was renamed without updating the plan)
//   call-edge-not-backed-by-import  -> code-side-defect (an import was removed but the edge was never un-registered)
//   name-type-near-miss             -> code-side-defect (spelling/case drift in the Go declaration)
const FIX_ROUTE_BY_CLASS = {
  'missing-path': 'master-plan-fix',
  'duplicate-guid': 'master-plan-fix',
  'orphan-directory': 'master-plan-fix',
  'contract-does-not-resolve': 'code-side-defect',
  'call-edge-not-backed-by-import': 'code-side-defect',
  'name-type-near-miss': 'code-side-defect',
  // BUG-327 Direction-D (code -> registry edges): a PROD import that exists
  // in Go source but has no outbound.calls edge means the PLAN is missing an
  // edge the code already relies on — same routing logic as orphan-directory.
  'import-edge-not-registered': 'master-plan-fix',
  // Test-only imports are deliberately NOT registrable edges (GR#20 contracts
  // cover production dependencies); reported for visibility only.
  'import-edge-test-only': 'informational',
};

// Classes surfaced for visibility but never filed to the BOW by --file
// (BUG-327: test-only import edges are informational).
const INFORMATIONAL_CLASSES = new Set(['import-edge-test-only']);

const CLASS_TITLES = {
  'missing-path': 'Direction-A: code.json module path does not exist on disk (BOW status done)',
  'duplicate-guid': 'Direction-A: duplicate GUID(s) reused across code.json entries',
  'orphan-directory': 'Direction-B: Go-bearing directory has no code.json registry entry',
  'contract-does-not-resolve': 'Direction-A: inbound.name does not resolve to any exported Go symbol',
  'call-edge-not-backed-by-import': 'Direction-A: registered call edge has no matching Go import',
  'name-type-near-miss': 'Direction-C: recorded name/type is a near-miss (case/spelling drift) against the real Go identifier',
  'import-edge-not-registered': 'Direction-D: PROD Go import crosses module boundaries but no outbound.calls edge is registered in code.json',
  'import-edge-test-only': 'Direction-D (informational): module-crossing Go import appears ONLY in _test.go files and has no registered edge',
};

// ── main audit ──────────────────────────────────────────────────────────────

/**
 * Runs the full audit and returns the report object. Accepts an optional
 * `bowStatuses` override (Map<mkey,{code,status}>) purely for test isolation
 * (BUG-069-style pattern) — every real invocation queries the live DB.
 */
async function runAudit(opts = {}) {
  const runId = crypto.randomUUID();
  const startedAt = new Date().toISOString();

  // AC-7/AC-8 read-only self-check: snapshot everything we must never touch,
  // BEFORE doing any work, so a mid-run mutation (not just an unreverted one)
  // is caught. NOTE (BUG-184): this is a two-point-in-time compare, not
  // continuous monitoring — see the "KNOWN LIMITATION" block in this file's
  // header comment for the accepted mutate-then-revert-inside-the-window gap.
  const preHashes = {
    codeJson: sha256File(CODE_JSON_PATH),
    masterPlan: sha256File(MASTER_PLAN_PATH),
  };
  const preGitStatus = execFileSync('git', ['status', '--porcelain', '--', 'code.json', 'docs/planning/master-plan-v2.1.json', 'internal', 'cmd'],
    { cwd: ROOT, encoding: 'utf8' });

  // Test-only synchronization point (BUG-181 regression fix). If provided,
  // this is awaited HERE — immediately after the pre-run snapshot is taken
  // and before any scanning work (astinfo/`go run`, etc.) begins — so a test
  // can inject a mutation that is guaranteed BY CONSTRUCTION, not by
  // wall-clock timing, to land inside the window the AC-7/AC-8 self-check
  // below actually brackets (preGitStatus...postGitStatus). No production
  // caller (cli() below) passes this; it is a no-op unless a test supplies
  // it. This does not change what the self-check verifies or when the
  // pre/post snapshots are taken — it only gives tests a deterministic hook
  // instead of a timing guess for injecting the mutation they're proving
  // gets caught.
  if (typeof opts.afterPreSnapshot === 'function') {
    await opts.afterPreSnapshot();
  }

  const commitHash = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: ROOT, encoding: 'utf8' }).trim();
  const goModule = readGoModule();
  const codeJson = JSON.parse(fs.readFileSync(CODE_JSON_PATH, 'utf8'));
  const modules = codeJson.modules;
  const byKey = new Map(modules.map(m => [m.key, m]));

  const bowStatuses = opts.bowStatuses || await loadBowStatuses();
  const statusOf = (key) => (bowStatuses.get(key) || {}).status || null;
  const bowCodeOf = (key) => (bowStatuses.get(key) || {}).code || null;

  // ── AC-1: path resolution, three-state classification ────────────────────
  const pathRows = modules.map(m => {
    const normPath = normalizeModulePath(m.path);
    const exists = normPath ? fs.existsSync(path.join(ROOT, normPath)) : false;
    const status = statusOf(m.key);
    let state;
    if (status === 'done') state = exists ? 'path-exists-done' : 'path-missing-done';
    else state = 'not-yet-built';
    return {
      key: m.key, bowCode: bowCodeOf(m.key), rawPath: m.path, normPath, exists,
      bowStatus: status, noBowMatch: !bowStatuses.has(m.key), state,
    };
  }).sort((a, b) => a.key.localeCompare(b.key));

  const stateCounts = { 'path-exists-done': 0, 'path-missing-done': 0, 'not-yet-built': 0 };
  for (const r of pathRows) stateCounts[r.state]++;

  const findings = []; // {class, instances:[...]}
  const findingsByClass = new Map(Object.keys(CLASS_TITLES).map(c => [c, []]));

  for (const r of pathRows) {
    if (r.state === 'path-missing-done') {
      findingsByClass.get('missing-path').push({
        key: r.key, bowCode: r.bowCode, path: r.rawPath,
        detail: `code.json path "${r.rawPath}" (normalized: ${r.normPath}) does not exist on disk; BOW ${r.bowCode || r.key} is status done`,
      });
    }
  }

  // ── Go directories we need astinfo for: every done module's Go path
  // (for AC-2/AC-6 contract checks and AC-3 import checks). ────────────────
  const goPathForModule = new Map(); // key -> normPath (Go-tree paths that exist on disk, regardless of BOW status — BUG-327)
  for (const r of pathRows) {
    if (isGoTreePath(r.normPath) && r.exists) goPathForModule.set(r.key, r.normPath);
  }
  const astinfoDirs = [...new Set(goPathForModule.values())];
  const astinfo = astinfoDirs.length ? runAstinfo(astinfoDirs) : new Map();

  // ── AC-2 + AC-6: GUID well-formedness/uniqueness, contract name resolution ─
  const guidLocations = new Map(); // guid -> [{key, field}]
  function trackGuid(guid, key, field) {
    if (!guid) return;
    if (!guidLocations.has(guid)) guidLocations.set(guid, []);
    guidLocations.get(guid).push({ key, field });
  }
  for (const m of modules) {
    trackGuid(m.guid, m.key, 'guid');
    trackGuid(m.inbound && m.inbound.guid, m.key, 'inbound.guid');
    trackGuid(m.outbound && m.outbound.guid, m.key, 'outbound.guid');
  }
  const malformedGuids = [];
  for (const [guid, locs] of guidLocations) {
    if (!UUID_V4_RE.test(guid)) malformedGuids.push({ guid, locations: locs });
  }
  const duplicateGuids = [...guidLocations.entries()]
    .filter(([, locs]) => locs.length > 1)
    .map(([guid, locs]) => ({ guid, locations: locs }));
  for (const dup of duplicateGuids) {
    findingsByClass.get('duplicate-guid').push({
      guid: dup.guid,
      detail: `GUID ${dup.guid} is reused across ${dup.locations.length} location(s): ${dup.locations.map(l => `${l.key}.${l.field}`).join(', ')}`,
    });
  }

  const contractRows = [];
  for (const m of modules) {
    const inboundName = m.inbound && m.inbound.name;
    if (!inboundName) continue;
    if (statusOf(m.key) !== 'done') continue; // gated per Definitions section
    const dir = goPathForModule.get(m.key);
    if (!dir) {
      contractRows.push({ key: m.key, name: inboundName, result: 'unresolved', detail: 'module path is not a resolvable Go-tree directory (or is itself a missing-path finding — see AC-1)' });
      continue;
    }
    const info = astinfo.get(dir);
    if (!info || info.error) {
      contractRows.push({ key: m.key, name: inboundName, result: 'unresolved', detail: `astinfo could not parse ${dir}: ${info ? info.error : 'no info'}` });
      continue;
    }
    const exact = info.exported.find(s => s.name === inboundName);
    if (exact) {
      contractRows.push({ key: m.key, name: inboundName, result: 'contract-ok', detail: `${dir}/${exact.file}:${exact.line}` });
      continue;
    }
    const near = info.exported.find(s => normalizeIdentForNearMiss(s.name) === normalizeIdentForNearMiss(inboundName));
    if (near) {
      contractRows.push({ key: m.key, name: inboundName, result: 'near-miss', detail: `recorded "${inboundName}" vs actual Go identifier "${near.name}" at ${dir}/${near.file}:${near.line}` });
      findingsByClass.get('name-type-near-miss').push({
        key: m.key, recorded: inboundName, actual: near.name,
        detail: `code.json inbound.name "${inboundName}" vs real Go identifier "${near.name}" (${dir}/${near.file}:${near.line})`,
      });
    } else {
      const searched = info.exported.map(s => s.name).sort();
      contractRows.push({ key: m.key, name: inboundName, result: 'no-match', detail: `no exported Go symbol named "${inboundName}" in ${dir} — searched: [${searched.join(', ')}]` });
      findingsByClass.get('contract-does-not-resolve').push({
        key: m.key, name: inboundName,
        detail: `no exported Go symbol named "${inboundName}" found in ${dir} (files searched: exported set = [${searched.join(', ')}])`,
      });
    }
  }
  contractRows.sort((a, b) => a.key.localeCompare(b.key));

  // ── AC-3: call-edge correspondence via real Go imports (go/ast, not grep) ─
  const edgeRows = [];
  for (const m of modules) {
    for (const call of (m.outbound && m.outbound.calls) || []) {
      edgeRows.push({ from: m.key, to: call.key });
    }
  }
  edgeRows.sort((a, b) => (a.from + ' ' + a.to).localeCompare(b.from + ' ' + b.to));

  for (const edge of edgeRows) {
    // BUG-327: an edge worth registering is worth checking whether the items
    // are BOW-done or not. The only skip is an endpoint whose Go path does not
    // exist on disk yet (nothing to import / no source to parse) — a
    // registered edge whose code exists on disk must be backed by a real
    // import regardless of status.
    const fromDir = goPathForModule.get(edge.from);
    const toDir = goPathForModule.get(edge.to);
    if (!fromDir || !toDir) {
      edge.result = 'skip'; edge.detail = 'one or both endpoints have no Go-tree path on disk yet (see AC-1 missing-path / not-yet-built)'; continue;
    }
    const fromInfo = astinfo.get(fromDir);
    if (!fromInfo || fromInfo.error) {
      edge.result = 'skip'; edge.detail = `astinfo could not parse ${fromDir}: ${fromInfo ? fromInfo.error : 'no info'}`; continue;
    }
    const targetImportPath = `${goModule}/${toDir}`;
    const pass = fromInfo.imports.includes(targetImportPath);
    edge.result = pass ? 'pass' : 'fail';
    edge.detail = pass
      ? `${fromDir} imports ${targetImportPath}`
      : `${fromDir} does NOT import ${targetImportPath} (imports actually present: [${fromInfo.imports.join(', ')}])`;
    if (!pass) {
      findingsByClass.get('call-edge-not-backed-by-import').push({
        from: edge.from, to: edge.to, detail: edge.detail,
      });
    }
  }

  // ── AC-4: orphan directory detection (Direction B, existence-only) ────────
  const registeredDirs = new Set();
  for (const m of modules) {
    const np = normalizeModulePath(m.path);
    if (isGoTreePath(np)) registeredDirs.add(np);
  }
  const goBearingDirs = [
    ...(fs.existsSync(path.join(ROOT, 'internal')) ? findGoBearingDirs('internal') : []),
    ...(fs.existsSync(path.join(ROOT, 'cmd')) ? findGoBearingDirs('cmd') : []),
  ].sort();

  function nearestRegisteredAncestor(dir) {
    if (registeredDirs.has(dir)) return { dir, matchType: 'exact' };
    const parts = dir.split('/');
    for (let i = parts.length - 1; i >= 1; i--) {
      const candidate = parts.slice(0, i).join('/');
      if (registeredDirs.has(candidate)) return { dir: candidate, matchType: 'child-of' };
    }
    return null;
  }

  const orphanRows = [];
  for (const dir of goBearingDirs) {
    const match = nearestRegisteredAncestor(dir);
    if (match) {
      orphanRows.push({ dir, registered: true, matchType: match.matchType, matchedEntry: match.dir });
    } else {
      orphanRows.push({ dir, registered: false });
      findingsByClass.get('orphan-directory').push({
        dir, detail: `${dir} contains .go file(s) but no code.json entry's path resolves to it or an ancestor of it`,
      });
    }
  }

  // ── Direction D (BUG-327): code -> registry import-edge check ─────────────
  // The mirror image of AC-3: AC-3 asks "is every REGISTERED edge backed by a
  // real import?"; this asks "is every real module-crossing import REGISTERED
  // as an outbound.calls edge?". Existence-only and NOT gated by BOW status —
  // an import that exists in code needs no permission to be flagged. Tracked
  // (git ls-files) .go files under internal/ and cmd/; see the header's
  // Direction D note for the ANY-owner attribution + single benign-sibling
  // rule.
  //
  // ACCEPTED LIMITATIONS (adversarial review, documented not fixed):
  //   - tracked-only scan (git ls-files): an untracked/.gitignore'd .go file's
  //     imports are invisible until staged — deliberate, keeps other lanes'
  //     scratch out; Direction B (filesystem sweep) sees the dir first.
  //   - text scan is not build-aware: `//go:build ignore`/platform-tagged/
  //     testdata files still contribute PROD import findings (report-only).
  //   - ANY-owner is deliberately over-permissive on shared paths (the fix for
  //     the single-winner false positive) — an edge held by any co-owner
  //     registers the import, hiding a specific owner's gap.
  //   - only internal/ and cmd/ imports are considered (current repo scope).
  const ownersByDir = new Map(); // dir -> Set(mkey)
  for (const m of modules) {
    const np = normalizeModulePath(m.path);
    if (!isGoTreePath(np)) continue;
    if (!ownersByDir.has(np)) ownersByDir.set(np, new Set());
    ownersByDir.get(np).add(m.key);
  }
  const registeredDirList = [...ownersByDir.keys()]
    .sort((a, b) => (b.length - a.length) || a.localeCompare(b));

  const registeredOutbound = new Map(); // mkey -> Set(mkey)
  for (const m of modules) {
    registeredOutbound.set(m.key, new Set(((m.outbound && m.outbound.calls) || []).map(c => c.key)));
  }

  // Scan every registered Go-tree root — internal/ AND cmd/ (BUG-327: the
  // original internal/-only sweep was blind to unregistered imports originating
  // from cmd/metropolis and cmd/metctl, e.g. tickdriver.go -> foundation.num).
  const trackedGoFiles = [...listTrackedGoFiles('internal'), ...listTrackedGoFiles('cmd')].sort();
  const importEdges = new Map(); // "fromPath -> toPath" -> record
  for (const file of trackedGoFiles) {
    const dir = path.posix.dirname(file);
    const fromRes = resolveDirOwners(registeredDirList, ownersByDir, dir);
    if (!fromRes) continue; // unregistered source dir — Direction B's orphan finding owns it
    let src;
    try {
      src = fs.readFileSync(path.join(ROOT, file), 'utf8');
    } catch {
      continue; // tracked but unreadable mid-run — the AC-7/AC-8 self-check owns that condition
    }
    const isTest = file.endsWith('_test.go');
    for (const imp of parseGoImports(src)) {
      if (!imp.startsWith(goModule + '/')) continue; // external/stdlib import, not a module edge
      const relTarget = imp.slice(goModule.length + 1);
      if (!isGoTreePath(relTarget)) continue;
      const toRes = resolveDirOwners(registeredDirList, ownersByDir, relTarget);
      if (!toRes) continue; // unregistered target dir — orphan finding owns it
      if (isBenignSibling(fromRes.path, relTarget, toRes.path)) continue; // descendant imports its module root
      const edgeId = `${fromRes.path} -> ${toRes.path}`;
      if (!importEdges.has(edgeId)) {
        importEdges.set(edgeId, {
          fromPath: fromRes.path, toPath: toRes.path,
          fromDir: dir, toDir: relTarget, // literal dirs, for an accurate detail when a module imports its own child
          fromOwners: fromRes.owners, toOwners: toRes.owners,
          prodFiles: [], testFiles: [],
        });
      }
      const rec = importEdges.get(edgeId);
      (isTest ? rec.testFiles : rec.prodFiles).push(file);
    }
  }

  const importEdgeRows = [...importEdges.values()]
    .sort((a, b) => (a.fromPath + ' ' + a.toPath).localeCompare(b.fromPath + ' ' + b.toPath))
    .map(rec => {
      const registered = isImportRegistered(rec.fromOwners, rec.toOwners, registeredOutbound);
      const prodFiles = [...new Set(rec.prodFiles)].sort();
      const testFiles = [...new Set(rec.testFiles)].sort();
      const row = {
        fromPath: rec.fromPath, toPath: rec.toPath, registered,
        prodFileCount: prodFiles.length, testFileCount: testFiles.length,
      };
      if (!registered) {
        const fromKey = primaryOwner(rec.fromOwners, byKey);
        const toKey = primaryOwner(rec.toOwners, byKey);
        if (prodFiles.length > 0) {
          findingsByClass.get('import-edge-not-registered').push({
            from: fromKey, to: toKey, fromPath: rec.fromPath, toPath: rec.toPath,
            detail: `PROD import edge ${rec.fromDir} -> ${rec.toDir} exists in Go source (${prodFiles.length} non-test file(s), e.g. ${prodFiles[0]}) but no outbound.calls edge is registered in code.json (owners ${fromKey} -> ${toKey})`,
          });
        } else {
          findingsByClass.get('import-edge-test-only').push({
            from: fromKey, to: toKey, fromPath: rec.fromPath, toPath: rec.toPath,
            detail: `test-only import edge ${rec.fromDir} -> ${rec.toDir} (${testFiles.length} _test.go file(s), e.g. ${testFiles[0]}) has no registered outbound.calls edge — informational, test dependencies are not registrable contracts`,
          });
        }
      }
      return row;
    });

  // ── AC-9: attach the fix-route label to every finding instance ───────────
  for (const [cls, instances] of findingsByClass) {
    for (const inst of instances) inst.fixRoute = FIX_ROUTE_BY_CLASS[cls];
  }

  // ── AC-7/AC-8 read-only self-check, taken again at the end ───────────────
  const postHashes = {
    codeJson: sha256File(CODE_JSON_PATH),
    masterPlan: sha256File(MASTER_PLAN_PATH),
  };
  const postGitStatus = execFileSync('git', ['status', '--porcelain', '--', 'code.json', 'docs/planning/master-plan-v2.1.json', 'internal', 'cmd'],
    { cwd: ROOT, encoding: 'utf8' });
  const selfCheck = {
    codeJsonUnchanged: preHashes.codeJson === postHashes.codeJson,
    masterPlanUnchanged: preHashes.masterPlan === postHashes.masterPlan,
    gitStatusUnchangedForScannedPaths: preGitStatus === postGitStatus,
  };
  if (!selfCheck.codeJsonUnchanged || !selfCheck.masterPlanUnchanged || !selfCheck.gitStatusUnchangedForScannedPaths) {
    // This must never happen — the audit never writes to these files, and
    // nothing else should touch code.json/master-plan-v2.1.json/internal/cmd
    // while a run is in flight either (BUG-181: a third-party mutation of a
    // scanned Go file mid-run was previously invisible — it flipped
    // gitStatusUnchangedForScannedPaths to false but nothing checked it).
    // Fail loudly rather than silently report a violated read-only constraint.
    throw new Error(`AC-7/AC-8 VIOLATION: read-only constraint broken during this run — codeJsonUnchanged=${selfCheck.codeJsonUnchanged} masterPlanUnchanged=${selfCheck.masterPlanUnchanged} gitStatusUnchangedForScannedPaths=${selfCheck.gitStatusUnchangedForScannedPaths}${selfCheck.gitStatusUnchangedForScannedPaths ? '' : `\npre-run git status:\n${preGitStatus}\npost-run git status:\n${postGitStatus}`}`);
  }

  const populatedClasses = [...findingsByClass.entries()].filter(([, v]) => v.length > 0);

  const report = {
    $comment: 'GENERATED by tools/plan/codejson-audit.js (FEAT-062) — a report, never hand-edited, never itself a registry (AC-7/AC-8).',
    runId,
    startedAt,
    commitHash,
    codeJsonSha256: preHashes.codeJson,
    masterPlanSha256: preHashes.masterPlan,
    moduleCount: codeJson.moduleCount,
    actualModuleCount: modules.length,
    goModule,
    selfCheck,
    directionA: {
      pathResolution: { stateCounts, total: pathRows.length, rows: pathRows },
      guids: {
        totalGuids: guidLocations.size,
        malformed: malformedGuids,
        duplicates: duplicateGuids,
      },
      contracts: contractRows,
      callEdges: edgeRows,
    },
    directionB: {
      goBearingDirCount: goBearingDirs.length,
      rows: orphanRows,
    },
    directionD: {
      trackedGoFileCount: trackedGoFiles.length,
      rows: importEdgeRows,
    },
    findingsByClass: Object.fromEntries(
      Object.keys(CLASS_TITLES).map(cls => [cls, {
        title: CLASS_TITLES[cls],
        fixRoute: FIX_ROUTE_BY_CLASS[cls],
        instanceCount: findingsByClass.get(cls).length,
        instances: findingsByClass.get(cls),
      }])
    ),
    populatedClassCount: populatedClasses.length,
  };

  return report;
}

// ── AC-10: BOW filing, one item per populated class (never invoked unless
// --file is explicitly passed — off by default, per FEAT-062's report-only
// scope). ────────────────────────────────────────────────────────────────

function fileFindingsToBow(report) {
  const os = require('os');
  const filed = [];
  for (const [cls, data] of Object.entries(report.findingsByClass)) {
    if (data.instanceCount === 0) continue;
    if (INFORMATIONAL_CLASSES.has(cls)) continue; // test-only edges are never filed
    const title = `codejson-audit: ${data.title} (${data.instanceCount} instance(s), commit ${report.commitHash.slice(0, 10)})`;
    const lines = data.instances.map((inst, i) => `${i + 1}. ${inst.detail}`);
    const body = [
      `FEAT-062 codejson-audit finding class: ${cls}`,
      `Fix route: ${data.fixRoute}`,
      `Run: ${report.runId} at commit ${report.commitHash}`,
      '',
      'Instances:',
      ...lines,
    ].join('\n');
    const tmpFile = path.join(os.tmpdir(), `codejson-audit-${cls}-${Date.now()}.txt`);
    fs.writeFileSync(tmpFile, body, 'utf8');
    try {
      const proc = spawnSync(process.execPath, [
        path.join(ROOT, 'claude-bow.js'), 'add', 'bug', title,
        '--priority', 'P2', '--desc-file', tmpFile,
      ], { cwd: ROOT, encoding: 'utf8' });
      if (proc.status !== 0) {
        throw new Error(`claude-bow.js add failed for class "${cls}": ${proc.stderr || proc.stdout}`);
      }
      filed.push({ class: cls, output: proc.stdout.trim() });
    } finally {
      fs.rmSync(tmpFile, { force: true });
    }
  }
  return filed;
}

// ── Markdown rendering ─────────────────────────────────────────────────────

function renderMarkdown(report) {
  const lines = [];
  lines.push(`# code.json <-> Go tree consistency audit (FEAT-062)`);
  lines.push('');
  lines.push(`- Run: \`${report.runId}\` at \`${report.startedAt}\``);
  lines.push(`- Commit: \`${report.commitHash}\``);
  lines.push(`- code.json sha256: \`${report.codeJsonSha256}\``);
  lines.push(`- master-plan-v2.1.json sha256: \`${report.masterPlanSha256}\``);
  lines.push(`- go.mod module: \`${report.goModule}\``);
  lines.push(`- Read-only self-check: ${JSON.stringify(report.selfCheck)}`);
  lines.push('');
  lines.push(`## Direction A — path resolution (AC-1), ${report.moduleCount} code.json modules (actual: ${report.actualModuleCount})`);
  const sc = report.directionA.pathResolution.stateCounts;
  lines.push(`- path-exists-done: ${sc['path-exists-done']}`);
  lines.push(`- path-missing-done: ${sc['path-missing-done']}`);
  lines.push(`- not-yet-built: ${sc['not-yet-built']}`);
  lines.push(`- sum: ${sc['path-exists-done'] + sc['path-missing-done'] + sc['not-yet-built']} (expect ${report.directionA.pathResolution.total})`);
  lines.push('');
  lines.push(`## Direction A — GUIDs (AC-2)`);
  lines.push(`- total GUIDs seen: ${report.directionA.guids.totalGuids}`);
  lines.push(`- malformed: ${report.directionA.guids.malformed.length}`);
  lines.push(`- duplicates: ${report.directionA.guids.duplicates.length}`);
  lines.push('');
  lines.push(`## Direction A — contract resolution (AC-2/AC-6)`);
  const contractCounts = report.directionA.contracts.reduce((acc, r) => { acc[r.result] = (acc[r.result] || 0) + 1; return acc; }, {});
  lines.push(`- counts: ${JSON.stringify(contractCounts)}`);
  lines.push('');
  lines.push(`## Direction A — call edges (AC-3)`);
  const edgeCounts = report.directionA.callEdges.reduce((acc, r) => { acc[r.result] = (acc[r.result] || 0) + 1; return acc; }, {});
  lines.push(`- counts: ${JSON.stringify(edgeCounts)}`);
  lines.push('');
  lines.push(`## Direction B — orphan directories (AC-4)`);
  const orphanCount = report.directionB.rows.filter(r => !r.registered).length;
  lines.push(`- Go-bearing directories swept: ${report.directionB.goBearingDirCount}`);
  lines.push(`- orphans: ${orphanCount}`);
  lines.push('');
  lines.push(`## Direction D — import edges, code -> registry (BUG-327)`);
  const unregisteredProd = report.directionD.rows.filter(r => !r.registered && r.prodFileCount > 0).length;
  const unregisteredTestOnly = report.directionD.rows.filter(r => !r.registered && r.prodFileCount === 0).length;
  lines.push(`- tracked .go files under internal/ + cmd/: ${report.directionD.trackedGoFileCount}`);
  lines.push(`- module-crossing import edges: ${report.directionD.rows.length}`);
  lines.push(`- PROD edges with no registered outbound.calls edge: ${unregisteredProd}`);
  lines.push(`- test-only unregistered edges (informational): ${unregisteredTestOnly}`);
  lines.push('');
  lines.push(`## Findings by class (AC-9/AC-10) — ${report.populatedClassCount} populated of ${Object.keys(report.findingsByClass).length} defined`);
  for (const [cls, data] of Object.entries(report.findingsByClass)) {
    lines.push(`### ${cls} — ${data.instanceCount} instance(s) — fix route: ${data.fixRoute}`);
    lines.push(data.title);
    for (const inst of data.instances) lines.push(`- ${inst.detail} [fixRoute: ${inst.fixRoute}]`);
    lines.push('');
  }
  return lines.join('\n');
}

// ── CLI ─────────────────────────────────────────────────────────────────────

async function cli() {
  const args = process.argv.slice(2);
  const flags = {};
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (a === '--file' || a === '--quiet') flags[a.slice(2)] = true;
    else if (a.startsWith('--')) { flags[a.slice(2)] = args[++i]; }
  }

  const report = await runAudit();

  if (flags.json) fs.writeFileSync(flags.json, JSON.stringify(report, null, 2) + '\n', 'utf8');
  const md = renderMarkdown(report);
  if (flags.md) fs.writeFileSync(flags.md, md + '\n', 'utf8');
  if (!flags.quiet) console.log(md);

  if (flags.file) {
    const filed = fileFindingsToBow(report);
    console.log(`\nFiled ${filed.length} BOW item(s): ${filed.map(f => f.class).join(', ') || '(none — no populated classes)'}`);
  }

  return report;
}

module.exports = {
  runAudit, renderMarkdown, fileFindingsToBow,
  normalizeModulePath, isGoTreePath, findGoBearingDirs, runAstinfo,
  parseGoImports, listTrackedGoFiles, resolveDirOwners, isImportRegistered, primaryOwner, isBenignSibling,
  BOW_TYPE_RANK, INFORMATIONAL_CLASSES, CLASS_TITLES, FIX_ROUTE_BY_CLASS,
};

if (require.main === module) {
  cli().catch(err => { console.error(err.stack || String(err)); process.exit(1); });
}
