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
 * acyclic dependency graph. Errors are collected and reported together with
 * tool error codes MET-T0xx; the script writes nothing unless everything
 * validates.
 *
 * Usage: node tools/plan/generate.js [--check]   (--check validates only)
 */

'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const ROOT = path.resolve(__dirname, '..', '..');
const PLAN_PATH = path.join(ROOT, 'docs', 'planning', 'master-plan-v2.1.json');
const CODE_JSON_PATH = path.join(ROOT, 'code.json');
const BOW_IMPORT_PATH = path.join(__dirname, 'bow-import.json');

const TYPES = ['module', 'feature', 'interface', 'bug'];
const PRIORITIES = ['P0', 'P1', 'P2', 'P3'];
const MILESTONES = ['M0', 'M1', 'M2', 'M3', 'M4', 'future'];

function fail(code, msg) {
  console.error(`[${code}] ${msg}`);
  process.exit(1);
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
    status: priorByKey.get(it.key)?.status || 'planned',
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

const codeJson = {
  "$comment": "GENERATED by tools/plan/generate.js from docs/planning/master-plan-v2.1.json — do not hand-edit (GR#3, GR#6). Regenerate after any master-plan change; GUIDs are stable across regenerations.",
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
  fs.writeFileSync(CODE_JSON_PATH, JSON.stringify(codeJson, null, 2) + '\n', 'utf8');
  fs.writeFileSync(BOW_IMPORT_PATH, JSON.stringify(bowImport, null, 2) + '\n', 'utf8');
} catch (err) {
  fail('MET-T030', `failed writing outputs: ${err.message}`);
}
console.log(`wrote code.json (${modules.length} modules, GUIDs ${priorByKey.size ? 'carried over where existing' : 'freshly minted'})`);
console.log(`wrote tools/plan/bow-import.json (${bowImport.items.length} items)`);
