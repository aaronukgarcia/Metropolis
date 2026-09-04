#!/usr/bin/env node
// tools/consolidator-oversight.mjs — CONSOLIDATOR AUDIT TRAIL, oversight half
// (Aaron's ruling on FEAT-2326609761, via claude-bow.js show FEAT-2326609761's
// comments, 2026-09-04): "...and [the lead] keeps an eye on its progress to
// make sure it does not go mad."
//
// Module key: reserved for tooling (root tooling exempt per CLAUDE.md GR#2;
// no code.json GUID — mirrors tools/plan/*.js and tools/test/scoped.mjs).
//
// WHAT THIS IS: a checkable script the lead runs (today: ad hoc; "on loop
// ticks once mutation is live" per the task brief) against the
// consolidator_audit table (via the debug sink's GET /api/consolidator/audit
// route — tools/debugsink/server.js) that FLAGS mechanical pathologies in
// the consolidator's behaviour with real, configurable thresholds:
//   - runaway demolition:      a single pass demolishing more than N buildings
//   - oscillation:             the same section consolidated A->B then B->A
//                               (or vice versa) within a window of N passes
//   - spend rate:              a single pass spending more than a GBP ceiling
//   - capacity trending DOWN:  total capacity falling for N consecutive passes
//   - reserve land shrinking:  free/reserve tiles falling for N consecutive passes
//
// These all read the 'implemented' stage (what a pass ACTUALLY did) — the
// only stage that carries real spend/demolition/capacity numbers. inc1 (the
// read-only discovery/audit half, already landed) never posts 'implemented'
// events; this script is written NOW, against the CONTRACT the mutation
// lane's ConsolidationPass log is expected to emit (see
// `ImplementedAuditPayload` below), so the tripwires exist and are tested
// BEFORE mutation lands, not bolted on after the fact once "it moves".
//
// GR#16 (never trust a field is present just because a type says so): every
// analysis function below treats a missing/malformed field on an
// 'implemented' payload as "no information", not zero and not a crash — an
// entry the mutation lane emits before every field in the contract below
// exists yet must degrade gracefully, never throw and never falsely flag
// (or falsely clear) a pathology from absent data.
//
// GR#17 (silent-failure detection + a monitoring FAILURE writes a registry
// error): a genuine failure to REACH the audit trail at all (the sink is
// down, a network error, a malformed response) is itself a monitoring
// failure — "the code running is NOT the same as the DB committing" applies
// here too: this script cannot tell the lead the trail is healthy if it
// never actually read it. That failure prints the registry-sourced code
// MET-V853 (ConsolidatorOversightMonitorFailed, claimed via
// tools/plan/add-error.js, owner ui.webconsole — the webconsole error
// framework's existing V8xx block) to stderr and exits non-zero, so a
// dead/unreachable sink is loud, never a silent "0 findings" false-green.
//
// ⚠ BALANCE-NUMBER REGIME (GR#15: every threshold below is DERIVED from an
// explicit, commented, overridable constant — never a bare literal buried in
// a comparison). Every threshold is a PLACEHOLDER pending Aaron's real
// dogfood tuning once the mutation lane actually runs passes; each is
// overridable via a CONSOLIDATOR_OVERSIGHT_* env var so retuning never needs
// a code change.
//
// HONEST DOCUMENTED BOUNDARIES — TRIPWIRE EVASION GAPS (Aaron, 2026-09-04,
// deliberately NOT fixed in this landing; folded into the threshold-tuning
// item instead): every threshold-based flag below has a mechanical evasion
// shape a sustained, deliberately-tuned defect could exploit to stay
// invisible forever. Documented here rather than silently accepted:
//   - OSCILLATION EVASION: a section that flip-flops A->B then B->A at a
//     cadence of exactly (OSCILLATION_WINDOW_PASSES + 1) passes or slower —
//     e.g. every 6th pass at today's default window of 5 — never trips
//     flagOscillation; the SAME defect at a tighter cadence (<=5 passes) is
//     caught. A wider window catches slower oscillation but costs more
//     history scanned per section.
//   - ROLLING-DEMOLITION EVASION: a pass that demolishes exactly at (or one
//     under) MAX_DEMOLITIONS_PER_PASS every single pass — e.g. 49/49/49 at
//     today's default ceiling of 50 — never trips flagRunawayDemolition, no
//     matter how many consecutive passes it repeats; the flag is a PER-PASS
//     ceiling with no memory across passes, so a sustained-but-just-under
//     rate is indistinguishable from one isolated pass at the same rate.
//   - CAPACITY SAWTOOTH EVASION: a total-capacity series that rises then
//     falls every OTHER pass (e.g. 1000, 1100, 950, 1050, 900, ...) never
//     accumulates CAPACITY_DOWN_TREND_PASSES consecutive falls, no matter how
//     low the sawtooth's floor drifts over time — flagCapacityDownTrend (and
//     flagReserveLandShrinking, the same shape) only sees a genuine
//     monotonic run, never a net downward drift disguised as oscillation.
// These are NOT bugs in the flags as specified — each flag does exactly what
// its stated threshold says — they are the KNOWN LIMITS of a purely
// mechanical, per-pass/per-window threshold approach, surfaced explicitly so
// nobody mistakes "no findings" for "provably healthy". Aaron is folding
// these into the tuning item (real thresholds need real dogfood data from
// the mutation lane before they can be tightened without false-positiving on
// legitimate play) rather than having this landing guess at fixes with zero
// real pass data to tune against.

// ES modules are always strict mode; no 'use strict' directive needed.
import { pathToFileURL } from 'node:url';

// ---------------------------------------------------------------------------
// Thresholds — GR#15: derived/configurable, commented as balance-numbers.
// ---------------------------------------------------------------------------

export const THRESHOLDS = Object.freeze({
  /** PLACEHOLDER-balance: a single pass demolishing more buildings than this is "runaway" until Aaron says otherwise. */
  MAX_DEMOLITIONS_PER_PASS: numFromEnv('CONSOLIDATOR_OVERSIGHT_MAX_DEMOLITIONS', 50),
  /** PLACEHOLDER-balance: a single pass spending more than this (GBP) in one go is flagged for review. */
  MAX_SPEND_PER_PASS: numFromEnv('CONSOLIDATOR_OVERSIGHT_MAX_SPEND', 50_000_000),
  /** PLACEHOLDER-balance: how many PASSES back to look for a section that flip-flopped A->B then B->A (or B->A then A->B). */
  OSCILLATION_WINDOW_PASSES: numFromEnv('CONSOLIDATOR_OVERSIGHT_OSCILLATION_WINDOW', 5),
  /** PLACEHOLDER-balance: consecutive passes of falling total capacity before it's a trend, not noise. */
  CAPACITY_DOWN_TREND_PASSES: numFromEnv('CONSOLIDATOR_OVERSIGHT_CAPACITY_DOWN_TREND', 3),
  /** PLACEHOLDER-balance: consecutive passes of shrinking reserve/free land before it's a trend, not noise. */
  RESERVE_LAND_DOWN_TREND_PASSES: numFromEnv('CONSOLIDATOR_OVERSIGHT_RESERVE_DOWN_TREND', 3),
});

function numFromEnv(key, fallback) {
  const raw = process.env[key];
  if (raw === undefined || raw === '') return fallback;
  const n = Number(raw);
  return Number.isFinite(n) ? n : fallback; // GR#16: a malformed override never crashes the script, just falls back
}

// ---------------------------------------------------------------------------
// The 'implemented' payload contract (the client seam the mutation lane's
// ConsolidationPass log is expected to fill in — see
// webconsole/src/sim/consolidatorAudit.ts's postConsolidatorAudit, stage:
// 'implemented'). Documented here, not enforced by a schema: every reader
// below is defensive against any field being absent (GR#16), so this is a
// CONTRACT for the mutation lane to aim at, not a runtime-checked type.
//
//   {
//     tick: number,
//     twelfth: number,
//     full: boolean,
//     demolitions: number,               // buildings removed this pass
//     builds: number,                    // buildings built this pass
//     spend: number,                     // net GBP spent this pass (buildCost - scrapRecovered, summed)
//     scrapRecovered: number,
//     totalCapacityAfter: number,        // city-wide capacity total AFTER this pass (whatever units the family uses)
//     reserveLandTilesAfter: number,     // free/reserve tiles remaining AFTER this pass
//     consolidations: [                  // one entry per section this pass actually touched
//       { sectionKey: number, fromSpec: string, toSpec: string, buildingIds: number[] }
//     ],
//   }
// ---------------------------------------------------------------------------

/** GR#16-safe number read: a missing/non-numeric field reads as `null` (no information), never 0. */
function numOrNull(v) {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

/** GR#16-safe array read: a missing/malformed field reads as an empty array (no information), never a crash. */
function arrOrEmpty(v) {
  return Array.isArray(v) ? v : [];
}

/**
 * One finding the oversight script raises. `severity` is informational only
 * (this script never blocks anything — it is read-only telemetry review,
 * same spirit as the consolidator's own read-only inc1 half).
 */
function finding(kind, severity, message, detail) {
  return { kind, severity, message, detail };
}

// ---------------------------------------------------------------------------
// §1 Runaway demolition
// ---------------------------------------------------------------------------

export function flagRunawayDemolition(implementedEntries, maxPerPass = THRESHOLDS.MAX_DEMOLITIONS_PER_PASS) {
  const findings = [];
  for (const e of implementedEntries) {
    const demolitions = numOrNull(e.payload?.demolitions);
    if (demolitions === null) continue; // GR#16: no information, not a violation
    if (demolitions > maxPerPass) {
      findings.push(
        finding(
          'runaway-demolition',
          'warn',
          `Pass at tick ${e.payload?.tick ?? '?'} demolished ${demolitions} buildings (> ${maxPerPass}/pass ceiling)`,
          { id: e.id, at: e.at, tick: e.payload?.tick, demolitions, ceiling: maxPerPass }
        )
      );
    }
  }
  return findings;
}

// ---------------------------------------------------------------------------
// §2 Oscillation — the same section flip-flopping A->B then B->A
// ---------------------------------------------------------------------------

export function flagOscillation(implementedEntries, windowPasses = THRESHOLDS.OSCILLATION_WINDOW_PASSES) {
  const findings = [];
  // history: sectionKey -> array of { passIndex, fromSpec, toSpec, id, at }
  const historyBySection = new Map();

  implementedEntries.forEach((e, passIndex) => {
    const consolidations = arrOrEmpty(e.payload?.consolidations);
    for (const c of consolidations) {
      const sectionKey = numOrNull(c?.sectionKey);
      const fromSpec = typeof c?.fromSpec === 'string' ? c.fromSpec : null;
      const toSpec = typeof c?.toSpec === 'string' ? c.toSpec : null;
      if (sectionKey === null || fromSpec === null || toSpec === null) continue; // GR#16

      const history = historyBySection.get(sectionKey) ?? [];
      // Look back within the window for a REVERSED move on this exact section:
      // a prior (from=toSpec, to=fromSpec) — the section went A->B, this move
      // goes B->A, at ANY spec pair — a real flip-flop regardless of which
      // direction happened first.
      for (const prior of history) {
        if (passIndex - prior.passIndex > windowPasses) continue;
        if (prior.fromSpec === toSpec && prior.toSpec === fromSpec) {
          findings.push(
            finding(
              'oscillation',
              'error',
              `Section ${sectionKey} flip-flopped: ${prior.fromSpec}->${prior.toSpec} at pass ${prior.passIndex} then ${fromSpec}->${toSpec} at pass ${passIndex} (within ${windowPasses}-pass window)`,
              {
                sectionKey,
                firstMove: { id: prior.id, at: prior.at, fromSpec: prior.fromSpec, toSpec: prior.toSpec },
                secondMove: { id: e.id, at: e.at, fromSpec, toSpec },
              }
            )
          );
        }
      }
      history.push({ passIndex, fromSpec, toSpec, id: e.id, at: e.at });
      historyBySection.set(sectionKey, history);
    }
  });

  return findings;
}

// ---------------------------------------------------------------------------
// §3 Spend rate
// ---------------------------------------------------------------------------

export function flagSpendRate(implementedEntries, maxSpend = THRESHOLDS.MAX_SPEND_PER_PASS) {
  const findings = [];
  for (const e of implementedEntries) {
    const spend = numOrNull(e.payload?.spend);
    if (spend === null) continue;
    if (spend > maxSpend) {
      findings.push(
        finding(
          'spend-rate',
          'warn',
          `Pass at tick ${e.payload?.tick ?? '?'} spent ${formatMoney(spend)} in one pass (> ${formatMoney(maxSpend)} ceiling)`,
          { id: e.id, at: e.at, tick: e.payload?.tick, spend, ceiling: maxSpend }
        )
      );
    }
  }
  return findings;
}

function formatMoney(n) {
  return `£${Math.round(n).toLocaleString('en-GB')}`;
}

// ---------------------------------------------------------------------------
// §4 Capacity trending DOWN across passes
// ---------------------------------------------------------------------------

/**
 * Generic "N consecutive passes moving the wrong way" detector, shared by the
 * capacity-down and reserve-land-shrinking flags (same shape, different
 * field + kind). `field` reads a numeric value off each entry's payload;
 * entries with no value for `field` are SKIPPED (not treated as a break in
 * the streak and not treated as a violation) — GR#16, an instrumentation gap
 * must not itself look like either a healthy or an unhealthy trend.
 */
function flagDownTrend(implementedEntries, field, trendPasses, kind, label) {
  const findings = [];
  const withField = implementedEntries
    .map((e) => ({ e, v: numOrNull(e.payload?.[field]) }))
    .filter((x) => x.v !== null);

  let streak = 1;
  for (let i = 1; i < withField.length; i++) {
    if (withField[i].v < withField[i - 1].v) {
      streak += 1;
    } else {
      streak = 1;
    }
    if (streak >= trendPasses) {
      const startIdx = i - streak + 1;
      findings.push(
        finding(
          kind,
          'error',
          `${label} fell for ${streak} consecutive passes (tick ${withField[startIdx].e.payload?.tick ?? '?'} -> ${withField[i].e.payload?.tick ?? '?'}): ${withField[startIdx].v} -> ${withField[i].v}`,
          {
            streak,
            from: { id: withField[startIdx].e.id, at: withField[startIdx].e.at, value: withField[startIdx].v },
            to: { id: withField[i].e.id, at: withField[i].e.at, value: withField[i].v },
          }
        )
      );
      // Reset so one long slide is reported once per THRESHOLD-length window,
      // not once per tick past the threshold (would flood the report).
      streak = 1;
    }
  }
  return findings;
}

export function flagCapacityDownTrend(implementedEntries, trendPasses = THRESHOLDS.CAPACITY_DOWN_TREND_PASSES) {
  return flagDownTrend(implementedEntries, 'totalCapacityAfter', trendPasses, 'capacity-down-trend', 'Total capacity');
}

// ---------------------------------------------------------------------------
// §5 Reserve land shrinking across passes
// ---------------------------------------------------------------------------

export function flagReserveLandShrinking(implementedEntries, trendPasses = THRESHOLDS.RESERVE_LAND_DOWN_TREND_PASSES) {
  return flagDownTrend(implementedEntries, 'reserveLandTilesAfter', trendPasses, 'reserve-land-shrinking', 'Reserve land (free tiles)');
}

// ---------------------------------------------------------------------------
// §6 Aggregate runner
// ---------------------------------------------------------------------------

/**
 * Run every tripwire against the 'implemented' subset of `entries` (already
 * oldest-first, per the server's own ORDER BY at ASC — see
 * tools/debugsink/server.js's queryAuditEntries). Non-'implemented' entries
 * are ignored for the tripwires (they carry no spend/demolition/capacity
 * numbers) but are still counted in the returned summary for visibility.
 */
export function runOversight(entries, thresholds = THRESHOLDS) {
  const implementedEntries = entries.filter((e) => e.stage === 'implemented');
  const findings = [
    ...flagRunawayDemolition(implementedEntries, thresholds.MAX_DEMOLITIONS_PER_PASS),
    ...flagOscillation(implementedEntries, thresholds.OSCILLATION_WINDOW_PASSES),
    ...flagSpendRate(implementedEntries, thresholds.MAX_SPEND_PER_PASS),
    ...flagCapacityDownTrend(implementedEntries, thresholds.CAPACITY_DOWN_TREND_PASSES),
    ...flagReserveLandShrinking(implementedEntries, thresholds.RESERVE_LAND_DOWN_TREND_PASSES),
  ];
  return {
    healthy: findings.length === 0,
    totalEntries: entries.length,
    discoveredCount: entries.filter((e) => e.stage === 'discovered').length,
    plannedCount: entries.filter((e) => e.stage === 'planned').length,
    implementedCount: implementedEntries.length,
    findings,
  };
}

// ---------------------------------------------------------------------------
// §7 CLI entry point — fetches from the sink's GET /api/consolidator/audit
// and prints a human-readable report. GR#17: a failure to REACH the sink at
// all is itself a monitoring failure, reported loudly with the registry code
// (never a silent "0 findings").
// ---------------------------------------------------------------------------

const DEFAULT_SINK_URL = 'http://127.0.0.1:8642';

/**
 * Fetch every audit entry (no stage filter — the summary counts every
 * stage). Throws on any network/HTTP failure; the CLI entry point below is
 * the ONLY caller that catches this, so a library caller (a future test or
 * another tool) sees the real failure rather than a silently-empty list.
 */
export async function fetchAuditEntries(sinkUrl = DEFAULT_SINK_URL, { since } = {}) {
  const url = new URL('/api/consolidator/audit', sinkUrl);
  if (since) url.searchParams.set('since', since);
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`GET ${url} -> HTTP ${res.status}`);
  }
  const body = await res.json();
  if (!body || body.ok !== true || !Array.isArray(body.entries)) {
    throw new Error(`GET ${url} returned a malformed body`);
  }
  return body.entries;
}

function printReport(report) {
  process.stdout.write(
    `Consolidator oversight: ${report.totalEntries} audit rows (${report.discoveredCount} discovered, ${report.plannedCount} planned, ${report.implementedCount} implemented)\n`
  );
  if (report.healthy) {
    process.stdout.write('No pathologies flagged. Trail looks healthy.\n');
    return;
  }
  process.stdout.write(`${report.findings.length} finding(s):\n`);
  for (const f of report.findings) {
    process.stdout.write(`  [${f.severity.toUpperCase()}] ${f.kind}: ${f.message}\n`);
  }
}

async function main() {
  const args = process.argv.slice(2);
  const sinkUrl = argFrom(args, '--url') ?? DEFAULT_SINK_URL;
  const since = argFrom(args, '--since');
  const asJson = args.includes('--json');

  let entries;
  try {
    entries = await fetchAuditEntries(sinkUrl, { since });
  } catch (err) {
    // GR#17: a monitoring FAILURE (cannot reach/parse the audit trail) is
    // itself an error that must surface loudly, named with the registry-
    // sourced code (MET-V853, ui.webconsole owner, claimed via
    // tools/plan/add-error.js) — never a silent "nothing to report".
    const detail = err && err.message ? err.message : String(err);
    process.stderr.write(`[MET-V853] Consolidator oversight monitoring failed: ${detail}\n`);
    process.exitCode = 2;
    return;
  }

  const report = runOversight(entries);
  if (asJson) {
    process.stdout.write(JSON.stringify(report, null, 2) + '\n');
  } else {
    printReport(report);
  }
  // Non-zero exit when unhealthy so this is loop-checkable (`if ! node
  // tools/consolidator-oversight.mjs; then ...`), without needing --json.
  if (!report.healthy) process.exitCode = 1;
}

// Only run when invoked directly (`node tools/consolidator-oversight.mjs`),
// never as a side effect of importing this module from a test (BUG-543
// discipline, mirrored from tools/debugsink/server.js's require.main guard).
const isDirectRun = (() => {
  try {
    return process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href;
  } catch {
    return false;
  }
})();

function argFrom(args, name) {
  const i = args.indexOf(name);
  if (i === -1 || i + 1 >= args.length) return undefined;
  return args[i + 1];
}

if (isDirectRun) {
  main();
}
