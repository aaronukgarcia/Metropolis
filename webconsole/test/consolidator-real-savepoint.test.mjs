// consolidator-real-savepoint.test.mjs — FEAT-2326609761 inc1 discovery/audit
// half: the report Aaron actually asked for ("tell me what the audit finds
// on HIS real savepoint").
//
// This is deliberately NOT a hard CI gate: it reads a LOCAL file
// (C:\Users\aarongarcia\.claude\jobs\f9ac9353\tmp\aaron-savepoint.lz) that
// exists only on this machine's job workspace, not in the repo and not on
// any CI runner. `test.skip` (not a failure) when the file is absent — the
// SAME house pattern as scale-gate.test.mjs's half-B skip for unimplemented
// work, just for "input not available" instead of "feature not built".
//
// PERF BOUND DERIVATION (house rule: never a wall-clock total, prefer the
// robust MEDIAN over max, document the baseline measurement — see
// scale-gate.test.mjs's own header for the precedent this mirrors):
// measured locally (Windows, Node 25.3.0, 7 independent FRESH (memo-missed)
// sectionIndexOf builds over Aaron's real 29,831-building/3,944-occupied-
// section savepoint): 32.20, 35.07, 36.25, 38.81, 39.57, 42.59, 60.07 ms —
// i.e. a ~33-60ms range, median ~39ms. SECTION_INDEX_FRESH_BOUND_MS is set
// to ~4x the highest observed median-ish figure (60ms x 4 ~= 240ms, rounded
// to 250ms) for CI-hardware margin — never tightened without a fresh
// measurement on the actual CI runner (this file never runs there anyway,
// but the constant is kept honest regardless).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { decode } from '../src/sim/saveCodec.ts';
import { offlineResidentsByReason } from '../src/sim/data.ts';
import {
  sectionIndexOf,
  currentMonthOpportunities,
  monthlyScopeOf,
  findOpportunities,
  strandedCapacityReport,
  topOpportunities,
  TOTAL_SECTIONS,
} from '../src/sim/consolidator.ts';

const SAVEPOINT_PATH = String.raw`C:\Users\aarongarcia\.claude\jobs\f9ac9353\tmp\aaron-savepoint.lz`;
const SECTION_INDEX_FRESH_BOUND_MS = 250;

function loadRealState() {
  const buf = fs.readFileSync(SAVEPOINT_PATH);
  const decoded = decode(buf.toString('utf8'));
  const parsed = JSON.parse(decoded);
  return parsed.snapshot ?? parsed;
}

test('REAL SAVEPOINT: Aaron\'s 29,831-building city — discovery, audit, stranded capacity, top opportunities', (t) => {
  if (!fs.existsSync(SAVEPOINT_PATH)) {
    t.skip(`savepoint not present at ${SAVEPOINT_PATH} — local-machine-only report, not a CI gate`);
    return;
  }

  const state = loadRealState();
  assert.ok(Array.isArray(state.buildings) && state.buildings.length > 0, 'savepoint decoded to a real SimState');

  // --- Perf: median FRESH (memo-cache-missed) sectionIndexOf build ---
  const N = 7;
  const fresh = [];
  for (let i = 0; i < N; i++) {
    const clone = { ...state, buildings: state.buildings.slice() }; // new identity -> memoOnState cache miss
    const t0 = performance.now();
    sectionIndexOf(clone);
    fresh.push(performance.now() - t0);
  }
  fresh.sort((a, b) => a - b);
  const medianFreshMs = fresh[Math.floor(fresh.length / 2)];
  console.log(
    `[consolidator/real-savepoint] buildings=${state.buildings.length} tick=${state.tick} ` +
      `medianFreshSectionIndexOfMs=${medianFreshMs.toFixed(2)} (bound ${SECTION_INDEX_FRESH_BOUND_MS}ms)`,
  );
  assert.ok(
    medianFreshMs < SECTION_INDEX_FRESH_BOUND_MS,
    `median fresh sectionIndexOf ${medianFreshMs.toFixed(2)}ms exceeds the ${SECTION_INDEX_FRESH_BOUND_MS}ms bound`,
  );

  // --- Stranded capacity: Aaron's mid-build ruling, cross-checked against the SSOT ---
  const report = strandedCapacityReport(state);
  const ssot = offlineResidentsByReason(state);
  assert.equal(
    report.totalActionableCapacity,
    ssot.disconnected,
    'per-section stranded (road-cause) capacity must sum to EXACTLY data.ts\'s offlineResidentsByReason(s).disconnected (GR#3 — no drift from the Housing tab\'s own number)',
  );
  assert.equal(
    report.totalConstructionCapacity,
    ssot.construction,
    'per-section under-construction capacity must sum to EXACTLY offlineResidentsByReason(s).construction',
  );
  console.log(
    `[consolidator/real-savepoint] STRANDED (actionable, road-caused): ${report.totalActionableCapacity} residents ` +
      `across ${report.clusterCount} section-clusters; under-construction (self-resolving): ${report.totalConstructionCapacity}; ` +
      `total estimated reconnect cost: £${report.totalEstimatedReconnectCost.toLocaleString()}`,
  );
  if (report.clusterCount > 0) {
    console.log('[consolidator/real-savepoint] top stranded clusters:', JSON.stringify(report.clusters.slice(0, 5)));
  }

  // --- Monthly rotation scope for this savepoint's tick ---
  const scope = monthlyScopeOf(state.tick);
  console.log(
    `[consolidator/real-savepoint] tick ${state.tick} -> twelfth ${scope.twelfth} (full=${scope.full}), ` +
      `${scope.sectionKeys.length} sections in this month's scope`,
  );

  // --- Density-consolidation ladder opportunities (current month + full map) ---
  const currentOpps = currentMonthOpportunities(state);
  const fullOpps = findOpportunities(state, Array.from({ length: TOTAL_SECTIONS }, (_, i) => i));
  console.log(
    `[consolidator/real-savepoint] density-consolidation opportunities: ${currentOpps.length} in this month's ` +
      `twelfth, ${fullOpps.length} across the whole map (Aaron's ruled 800m/16x16-tile sections — see ` +
      `consolidator.ts's SECTION_TILES derivation comment for the measurement that set this size)`,
  );

  // --- The combined, ranked panel list Aaron sees ---
  const top = topOpportunities(state, Array.from({ length: TOTAL_SECTIONS }, (_, i) => i), 15);
  console.log(`[consolidator/real-savepoint] top ${top.length} ranked opportunities (reconnect ranked first):`);
  for (const o of top) {
    if (o.kind === 'reconnect') {
      console.log(
        `  #${o.rank} RECONNECT section ${o.sectionKey}: ${o.strandedCapacity} residents stranded ` +
          `(${o.cause}), ~${o.approxSpurSections ?? '?'} section-hops to connected road, ` +
          `est. cost £${o.estimatedReconnectCost ?? 'unknown'} vs relocate-all upper bound £${o.relocateAllCostUpperBound}`,
      );
    } else {
      console.log(
        `  #${o.rank} CONSOLIDATE section ${o.sectionKey}: ${o.groupCount}x ${o.fromSpec} -> ${o.toSpec}, ` +
          `net cost £${o.netCost}, capacity gain ${o.capacityGain}, buildings -${o.buildingCountReduction}`,
      );
    }
  }
  // Reconnection opportunities must sort ahead of every consolidation opportunity (Aaron's ranking ruling).
  const firstConsolidateIndex = top.findIndex((o) => o.kind === 'consolidate');
  const lastReconnectIndex = top.map((o) => o.kind).lastIndexOf('reconnect');
  if (firstConsolidateIndex !== -1 && lastReconnectIndex !== -1) {
    assert.ok(
      lastReconnectIndex < firstConsolidateIndex,
      'every reconnection opportunity must rank ahead of every consolidation opportunity',
    );
  }
});
