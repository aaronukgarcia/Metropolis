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

/**
 * runLint — never calls process.exit (the CLI wrapper owns exit codes).
 * Returns { totalErrors, findings, staleDefinitions, unitsChecked, filesScanned }.
 */
function runLint(opts = {}) {
  const repoDir = opts.repoDir || DEFAULT_REPO_DIR;
  const { units, registeredKeys } = loadRegistry(repoDir);

  const findings = [];        // UNITS-LINT-001
  const staleDefinitions = []; // UNITS-LINT-002
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

  // ── data/*.json ────────────────────────────────────────────────────────────
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
    totalErrors: findings.length + staleDefinitions.length,
    findings,
    staleDefinitions,
    unitsChecked: units.length,
    filesScanned,
  };
}

module.exports = { runLint, loadRegistry, VOCABULARY, parseDefinedAt };

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
  if (result.totalErrors > 0) {
    console.error(`❌ UNITS-LINT FAILED: ${result.totalErrors} finding(s).`);
    process.exit(1);
  }
  console.log('✅ UNITS-LINT PASSED: every unit in use is registered and every definition pointer resolves.');
  process.exit(0);
}
