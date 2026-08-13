/**
 * tools/plan/generate.js — Metropolis planning generator
 *
 * Single source of truth: docs/planning/master-plan-v2.1.json (hand-authored).
 * This script derives, deterministically and idempotently:
 *
 *   1. code.json (repo root)        — the code-module registry: one entry per
 *      planned module/feature/interface with a stable GUID, an INBOUND
 *      interface GUID (what others call — name/format/pattern), an OUTBOUND
 *      interface GUID (the identity it presents when calling others),
 *      forward pointers (outbound.calls -> target inbound GUIDs) and reverse
 *      pointers (inbound.consumers -> caller outbound GUIDs).
 *   2. tools/plan/bow-import.json   — the Book of Work load file consumed by
 *      `node claude-bow.js import` (idempotent by mkey).
 *
 * GUID stability: on regeneration, GUIDs are carried over from the existing
 * code.json (keyed by module key), so references never churn (GR#6).
 * Validation before any output (GR#1): unique keys/seqs, known deps/calls,
 * acyclic dependency graph, and (BUG-058) collaborations drift — an item's
 * optional `collaborations: { consumesFrom, suppliesTo }` field records a
 * spec-derived requirement that a call edge must exist; validation fails if
 * a declared collaboration has no matching entry in calls[]/consumers[].
 * Errors are collected and reported together with tool error codes MET-T0xx;
 * the script writes nothing unless everything validates.
 *
 * Usage: node tools/plan/generate.js [--check]   (--check validates only)
 */

'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const ROOT = path.resolve(__dirname, '..', '..');
// MET_PLAN_PATH override exists solely for test isolation (BUG-069): tests
// that need to mutate the plan point this at a temp copy instead of the
// live SSOT. Unset in every real invocation, so production behaviour is
// unchanged.
const PLAN_PATH = process.env.MET_PLAN_PATH || path.join(ROOT, 'docs', 'planning', 'master-plan-v2.1.json');
const CODE_JSON_PATH = path.join(ROOT, 'code.json');
const BOW_IMPORT_PATH = path.join(__dirname, 'bow-import.json');

const TYPES = ['module', 'feature', 'interface', 'bug'];
const PRIORITIES = ['P0', 'P1', 'P2', 'P3'];
const MILESTONES = ['M0', 'M1', 'M2', 'M3', 'M4', 'future'];

function fail(code, msg) {
  console.error(`[${code}] ${msg}`);
  process.exit(1);
}

// BUG-111: concurrent invocations of this script (two dev-team sessions, or
// a CI job overlapping an interactive run) used to race on a plain
// fs.writeFileSync of code.json/bow-import.json — two processes writing the
// same path at once can interleave their bytes, producing a torn file (the
// "Unterminated string in JSON" failure mode BUG-111 was filed from). Fix:
// the standard atomic-write pattern — write the full content to a temp file
// in the SAME directory as the target (same filesystem, so the rename below
// is atomic), then fs.renameSync it over the target. A rename is a single
// filesystem metadata operation; a concurrent reader/writer can only ever
// observe the old complete file or the new complete file, never a partial
// one. The temp filename includes pid + a random suffix so two concurrent
// invocations never collide on their OWN temp file either.
function writeFileAtomic(targetPath, content) {
  const dir = path.dirname(targetPath);
  const tmpPath = path.join(dir, `.${path.basename(targetPath)}.tmp-${process.pid}-${crypto.randomBytes(6).toString('hex')}`);
  fs.writeFileSync(tmpPath, content, 'utf8');
  try {
    fs.renameSync(tmpPath, targetPath);
  } catch (err) {
    // Best-effort cleanup of the temp file if the rename itself failed, so
    // a failed write doesn't leave litter behind — but don't let a cleanup
    // failure mask the real error.
    try { fs.unlinkSync(tmpPath); } catch { /* ignore */ }
    throw err;
  }
}

// ── Load ──────────────────────────────────────────────────────────────────────

let plan;
try {
  plan = JSON.parse(fs.readFileSync(PLAN_PATH, 'utf8'));
} catch (err) {
  fail('MET-T001', `cannot read/parse master plan at ${PLAN_PATH}: ${err.message}`);
}
const items = Array.isArray(plan.items) ? plan.items : fail('MET-T002', 'master plan has no items[] array');

// Existing code.json (for GUID stability across regenerations).
let prior = { modules: [] };
try {
  if (fs.existsSync(CODE_JSON_PATH)) prior = JSON.parse(fs.readFileSync(CODE_JSON_PATH, 'utf8'));
} catch (err) {
  fail('MET-T003', `existing code.json is unreadable — refusing to overwrite it blind: ${err.message}`);
}
const priorByKey = new Map((prior.modules || []).map(m => [m.key, m]));

// ── Validate (all errors reported together, nothing written on failure) ───────

const errors = [];
const byKey = new Map();
const seqSeen = new Map();
for (const it of items) {
  const id = it.key || it.title || '?';
  if (!it.key || !/^[a-z0-9][a-z0-9._-]*$/.test(it.key)) errors.push(`MET-T010 "${id}": missing/invalid key`);
  else if (byKey.has(it.key)) errors.push(`MET-T011 "${id}": duplicate key`);
  else byKey.set(it.key, it);
  if (!TYPES.includes(it.type)) errors.push(`MET-T012 "${id}": invalid type "${it.type}"`);
  if (!it.title) errors.push(`MET-T013 "${id}": missing title`);
  if (!Number.isInteger(it.seq)) errors.push(`MET-T014 "${id}": seq must be an integer`);
  else if (seqSeen.has(it.seq)) errors.push(`MET-T015 "${id}": duplicate seq ${it.seq} (also "${seqSeen.get(it.seq)}")`);
  else seqSeen.set(it.seq, it.key);
  if (!PRIORITIES.includes(it.priority)) errors.push(`MET-T016 "${id}": invalid priority "${it.priority}"`);
  if (!MILESTONES.includes(it.milestone)) errors.push(`MET-T017 "${id}": invalid milestone "${it.milestone}"`);
  if (it.sprint != null && (!Number.isInteger(it.sprint) || it.sprint < 0)) errors.push(`MET-T019 "${id}": sprint must be a non-negative integer or null`);
  if (!it.specRef) errors.push(`MET-T018 "${id}": missing specRef — every item must trace to the master document`);
}
for (const it of items) {
  for (const dep of it.deps || []) {
    if (dep === it.key) errors.push(`MET-T020 "${it.key}": depends on itself`);
    else if (!byKey.has(dep)) errors.push(`MET-T021 "${it.key}": unknown dependency "${dep}"`);
  }
  for (const call of it.calls || []) {
    if (call === it.key) errors.push(`MET-T022 "${it.key}": calls itself`);
    else if (!byKey.has(call)) errors.push(`MET-T023 "${it.key}": unknown call target "${call}"`);
  }
}
// Collaborations drift check (BUG-058 part 2): an optional per-item
// `collaborations: { consumesFrom: [...], suppliesTo: [...] }` field records
// a spec-derived requirement — "the GDD says these two modules must
// collaborate" — captured as data at the moment a BA/dev reads the spec,
// instead of staying implicit in a specRef citation string or a BOW comment.
// It is deliberately NOT a duplicate of `calls`/`consumers`: `calls` is the
// realized wiring (and `consumers` is mechanically derived FROM `calls`, so
// those two can never drift apart from each other — see the BA-Registry
// symmetry sweep on BUG-058). `collaborations` is the separate claim "the
// spec requires this edge to exist", authored independently of whether the
// edge has been wired yet. The check below is one-directional on purpose: a
// declared collaboration with no realized edge is an error (the exact class
// of defect BUG-058 catalogued — a spec-mandated call path a GR#20-compliant
// developer has no legal way to make); a realized `calls` edge with no
// matching collaboration entry is NOT an error, since most edges are
// legitimate architecture that nobody needs to have footnoted from a spec
// citation, and demanding 100% collaboration coverage would just recreate
// the "two places to keep in sync" duplication risk this field exists to
// avoid.
for (const it of items) {
  const collab = it.collaborations;
  if (!collab) continue;
  for (const target of collab.consumesFrom || []) {
    if (!byKey.has(target)) { errors.push(`MET-T025 "${it.key}": collaborations.consumesFrom references unknown module "${target}"`); continue; }
    if (!(it.calls || []).includes(target)) {
      errors.push(`MET-T025 "${it.key}": declares collaborations.consumesFrom "${target}" but has no "${target}" in its own calls[] — spec-mandated edge is not registered`);
    }
  }
  for (const target of collab.suppliesTo || []) {
    if (!byKey.has(target)) { errors.push(`MET-T025 "${it.key}": collaborations.suppliesTo references unknown module "${target}"`); continue; }
    const supplier = byKey.get(target);
    if (!(supplier.calls || []).includes(it.key)) {
      errors.push(`MET-T025 "${it.key}": declares collaborations.suppliesTo "${target}" but "${target}".calls[] does not include "${it.key}" — spec-mandated edge is not registered`);
    }
  }
}
// Acyclicity of the dependency graph (Kahn).
{
  const indeg = new Map([...byKey.keys()].map(k => [k, 0]));
  const out = new Map([...byKey.keys()].map(k => [k, []]));
  for (const it of items) for (const dep of it.deps || []) {
    if (byKey.has(dep)) { indeg.set(it.key, indeg.get(it.key) + 1); out.get(dep).push(it.key); }
  }
  const q = [...indeg.entries()].filter(([, d]) => d === 0).map(([k]) => k);
  let visited = 0;
  while (q.length) {
    const k = q.pop(); visited++;
    for (const nxt of out.get(k)) { indeg.set(nxt, indeg.get(nxt) - 1); if (indeg.get(nxt) === 0) q.push(nxt); }
  }
  if (visited < byKey.size) {
    errors.push(`MET-T024 dependency cycle among: ${[...indeg.entries()].filter(([, d]) => d > 0).map(([k]) => k).join(', ')}`);
  }
}
if (errors.length) {
  console.error(`master plan FAILED validation with ${errors.length} error(s):`);
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}
console.log(`master plan validates clean: ${items.length} items, seqs ${Math.min(...seqSeen.keys())}..${Math.max(...seqSeen.keys())}, dependency graph acyclic.`);
// BUG-070: the collaborations forward-drift-gate (see the block above) is
// scoped to whichever items happen to carry a `collaborations` field — it
// has zero coverage on everything else. That scope limit was previously
// only documented in prose (this file's header comment, a BOW thread), so
// a reviewer reading only this script's stdout had no way to learn how
// partial the gate's coverage actually is. Report it live, derived from the
// same `items` array validation just ran over — never hardcoded (GR#15).
{
  const withCollab = items.filter(it => it.collaborations).length;
  console.log(`collaborations declared for ${withCollab}/${items.length} modules (${items.length - withCollab} uncovered by this gate)`);
}
if (process.argv.includes('--check')) process.exit(0);

// ── GUID assignment (stable across regenerations) ─────────────────────────────

function guidsFor(key) {
  const prev = priorByKey.get(key);
  return {
    guid: prev?.guid || crypto.randomUUID(),
    guidIn: prev?.inbound?.guid || crypto.randomUUID(),
    guidOut: prev?.outbound?.guid || crypto.randomUUID(),
  };
}
const assigned = new Map([...byKey.keys()].map(k => [k, guidsFor(k)]));

// ── code.json ─────────────────────────────────────────────────────────────────

// Reverse pointers: for each item, who calls it (caller key + caller outbound GUID).
const consumers = new Map([...byKey.keys()].map(k => [k, []]));
for (const it of items) {
  for (const call of it.calls || []) {
    consumers.get(call).push({ key: it.key, outboundGuid: assigned.get(it.key).guidOut });
  }
}

const modules = [...items].sort((a, b) => a.seq - b.seq).map(it => {
  const g = assigned.get(it.key);
  return {
    guid: g.guid,
    key: it.key,
    bowType: it.type,
    seq: it.seq,
    sprint: it.sprint ?? null,
    title: it.title,
    priority: it.priority,
    milestone: it.milestone,
    layer: it.layer,
    specRef: it.specRef,
    path: it.path || null,
    // No status field: item status lives ONLY in the BOW (GR#3 — a status
    // mirror here drifted stale within one sprint; QA audit 2026-08-08).
    inbound: {
      guid: g.guidIn,
      name: it.inbound?.name || null,
      format: it.inbound?.format || null,
      pattern: it.inbound?.pattern || null,
      consumers: consumers.get(it.key).sort((a, b) => a.key.localeCompare(b.key)),
    },
    outbound: {
      guid: g.guidOut,
      calls: (it.calls || []).map(c => ({
        key: c,
        moduleGuid: assigned.get(c).guid,
        inboundGuid: assigned.get(c).guidIn,
      })),
    },
  };
});

// ── Security-scan stamps (Destructive agent, GR#3) ────────────────────────────
//
// code.json must never be hand-edited, so adversarial-review state lives in
// data/security-scans.json and is merged in here. A stamp therefore appears in
// code.json (where readers look for it) while surviving every regeneration,
// which a hand-edit would not. Absent file or absent module key = never
// scanned, which is the correct default: unscanned must never look scanned.
let securityScans = {};
let knownOrphanedKeys = {};
try {
  const raw = fs.readFileSync(path.join(__dirname, '..', '..', 'data', 'security-scans.json'), 'utf8');
  const parsed = JSON.parse(raw);
  securityScans = parsed.scans || {};
  knownOrphanedKeys = parsed.knownOrphanedKeys || {};
} catch {
  // Ledger missing or unreadable — every module simply reports unscanned.
}
for (const m of modules) {
  m.securityScan = securityScans[m.key] || null;
}
// BUG-023: a ledger key with no matching code.json module used to be merged
// with `|| null` and silently discarded — indistinguishable from a typo that
// vanishes with no trace. Warn loudly (non-fatal) so a real typo is caught by
// a human/agent reading generator output, not silently eaten. Keys explicitly
// documented in data/security-scans.json's `knownOrphanedKeys` (e.g.
// legacy.versionguard — root tooling, exempt per CLAUDE.md/GR#2) still warn,
// but with a pointer to that documented reason instead of reading as a fresh
// typo.
{
  const moduleKeys = new Set(modules.map(m => m.key));
  const orphanedScanKeys = Object.keys(securityScans).filter(k => !moduleKeys.has(k));
  for (const k of orphanedScanKeys) {
    if (Object.prototype.hasOwnProperty.call(knownOrphanedKeys, k)) {
      console.warn(`[MET-T031] WARNING: data/security-scans.json has a scan entry for "${k}", which matches no code.json module key — this entry is being DROPPED from code.json (documented exemption: ${knownOrphanedKeys[k]}).`);
    } else {
      console.warn(`[MET-T031] WARNING: data/security-scans.json has a scan entry for "${k}", which matches no code.json module key — this entry is being DROPPED. If this is a typo, fix the key. If it is deliberate (e.g. root tooling exempt per CLAUDE.md), add it to "knownOrphanedKeys" in data/security-scans.json explaining why.`);
    }
  }
}

const codeJson = {
  "$comment": "GENERATED by tools/plan/generate.js from docs/planning/master-plan-v2.1.json — do not hand-edit (GR#3, GR#6). Regenerate after any master-plan change; GUIDs are stable across regenerations. Item STATUS is deliberately absent: the BOW is the only status source (node claude-bow.js show <mkey>). Each module's `securityScan` is merged from data/security-scans.json — edit THAT file, never this one.",
  project: plan.project,
  planVersion: plan.planVersion,
  specSource: plan.source,
  updated: plan.updated,
  conventions: plan.conventions,
  moduleCount: modules.length,
  modules,
};

// ── bow-import.json ───────────────────────────────────────────────────────────

const bowImport = {
  "$comment": "GENERATED by tools/plan/generate.js — consumed by `node claude-bow.js import tools/plan/bow-import.json`. Idempotent by mkey.",
  source: plan.source,
  items: [...items].sort((a, b) => a.seq - b.seq).map(it => {
    const g = assigned.get(it.key);
    return {
      mkey: it.key,
      type: it.type,
      title: it.title,
      desc: `${it.desc} [spec: ${it.specRef}]${it.path ? ` [path: ${it.path}]` : ''}`,
      seq: it.seq,
      sprint: it.sprint ?? null,
      priority: it.priority,
      milestone: it.milestone,
      layer: it.layer,
      specRef: it.specRef,
      guid: g.guid,
      guidIn: g.guidIn,
      guidOut: g.guidOut,
      deps: it.deps || [],
    };
  }),
};

// ── Write ─────────────────────────────────────────────────────────────────────

try {
  writeFileAtomic(CODE_JSON_PATH, JSON.stringify(codeJson, null, 2) + '\n');
  writeFileAtomic(BOW_IMPORT_PATH, JSON.stringify(bowImport, null, 2) + '\n');
} catch (err) {
  fail('MET-T030', `failed writing outputs: ${err.message}`);
}
console.log(`wrote code.json (${modules.length} modules, GUIDs ${priorByKey.size ? 'carried over where existing' : 'freshly minted'})`);
console.log(`wrote tools/plan/bow-import.json (${bowImport.items.length} items)`);
