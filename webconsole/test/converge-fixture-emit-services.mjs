// converge-fixture-emit-services.mjs — FEAT-2326609747 (services.convergence)
// inc1.
//
// Headlessly runs the TS webconsole sim's PURE serviceCoverageOf() read
// (src/sim/data.ts) against the canonical journal this increment's Go
// domain adapter (internal/converge/services_domain.go's ServicesDomain)
// ALSO consumes (test/converge-fixtures/converge-services-actions.json,
// Journal shape: {tick, op, args} — see that file's own schema note for why
// this mirrors internal/converge/domain.go's generic Journal rather than
// converge-finance-actions.json's bespoke actionEntry shape), and emits the
// resulting services trajectory in EXACTLY internal/converge's LoadFixture
// JSON shape (internal/converge/fixture.go's fixtureFile: {"domain": "...",
// "samples": [{"tick": N, "values": {...}}]}). Never a hand-authored
// fixture (mirrors fixture.go's SaveFixture doc comment and
// converge-fixture-emit.mjs's own discipline for finance).
//
// # Field mapping (docs/planning/acceptance/FEAT-services-convergence-inc1-2026-09-02.md
// §3's mapping table, as amended by that doc's Addendum — r1 remediation)
//
// Go's ServiceKind is COARSER than TS's serviceCoverageOf rows (fire is the
// one 1:1 candidate; education/healthcare are Go-single-kind vs TS-N-rows
// aggregates). This emitter reports exactly three compared groups, each as
// FOUR int64 fields (capacity/need rounded to the nearest integer;
// coverage scaled ×COVERAGE_SCALE and rounded — see
// internal/converge/tolerance.go's ServicesCoverage* constants):
//
//   - "fire_capacity" / "fire_need": the TS 'fire' row verbatim (1:1 with
//     Go's single "fire" ServiceKind's catalogue building — though NOT the
//     same catalogue or the same UNITS, see the Addendum: TS's fire_post
//     is "served=4000 people", Go's fire_station is "4 appliances").
//   - "education_capacity" / "education_need": the SUM of the TS 'nursery'
//     + 'primary' + 'college' rows.
//   - "healthcare_capacity" / "healthcare_need": the SUM of the TS 'gp' +
//     'hosp' rows.
//   - "{group}_coverage_x10000": the CLAMPED coverage ratio, min(1, cap/need)
//     — the SAME representation Go's engine.services coverageRatio()
//     (coverage.go:68-73) produces (clamp01), so this field is the
//     apples-to-apples comparison point against
//     internal/converge/services_domain.go's engine-read
//     CoverageForDistrict(group).CoverageRatio. Compare() checks this field
//     (it is in ServicesDomain's Contract).
//   - "{group}_ts_unclamped_coverage_x10000": the RAW, un-clamped cap/need
//     ratio (can exceed 10000) — TS-ONLY, informational (mirrors
//     converge-fixture-emit.mjs's income/expenses/net fields): Go's
//     ServicesAPI has no unclamped coverage read to compare this against,
//     and Compare() only checks fields the Go reference reports, so this
//     field is never fed through Compare — it exists purely so the
//     clamp01-vs-unclamped divergence (independent round r1's finding,
//     recorded in the acceptance doc's Addendum) is visible in the
//     committed fixture rather than silently discarded.
//
// # Placement + population (see converge-services-actions.json's own notes)
//
// "place_service" appends the building directly to state.buildings with
// builtTick:null (data.ts:461's isOnline() "always online" branch) rather
// than driving the reducer's 'place' case — this fixture tests
// serviceCoverageOf's capacity/demand arithmetic, not gameplay realism
// (affordability/construction-time/road-connectivity gates), mirroring
// converge-finance-actions.json's explicit opening-treasury seed. "goKind"/
// "goServiceID"/"goCapacity" args are TS-side no-ops (ServicesDomain reads
// them; this emitter ignores them) — the SAME action entry drives both
// sides from the same logical input, per AC-3.
//
// "set_population" assigns state.population directly, same simplification
// rationale.
//
// # Determinism
//
// emitFixture() is a pure function of the actions file's contents plus the
// TS engine's own pure serviceCoverageOf() (data.ts) — no wall clock, no
// Math.random. Two calls therefore produce byte-identical JSON, proven by
// this file's own node:test block below (node's default test glob
// discovers it automatically, same as converge-fixture-emit.mjs).

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState } from '../src/sim/engine.ts';
import { serviceCoverageOf } from '../src/sim/data.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// COVERAGE_SCALE mirrors internal/converge/tolerance.go's
// ServicesCoverageScale EXACTLY — a literal duplication (not an import),
// same TS-tooling-may-not-depend-on-the-Go-module rationale as
// converge-fixture-emit.mjs's MICROPOUNDS_PER_POUND. Any change to the Go
// constant must update this one too.
const COVERAGE_SCALE = 10000;

const ACTIONS_PATH = path.join(__dirname, 'converge-fixtures', 'converge-services-actions.json');
const OUT_PATH = path.join(
  __dirname,
  '..',
  '..',
  'internal',
  'converge',
  'testdata',
  'services-webconsole-v1.json'
);
const DOMAIN = 'services';

function loadJournal() {
  const raw = readFileSync(ACTIONS_PATH, 'utf8');
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed.entries)) {
    throw new Error(`${ACTIONS_PATH}: malformed journal (missing "entries" array)`);
  }
  return parsed.entries;
}

/** Round-half-away-from-zero to the nearest integer — matches Go's math.Round
 *  convention used by services_domain.go's snapshotServices for the same
 *  fields, so both sides quantize identically before Compare's TierExact/
 *  TierBounded checks ever see the value. */
function roundInt(v) {
  return Math.round(v);
}

function coverageOf(need, cap) {
  return need <= 0 ? 1 : cap / need;
}

/** Snapshot the three compared groups at the current state, per the mapping
 *  table (docs/planning/acceptance/FEAT-services-convergence-inc1-2026-09-02.md
 *  §3). Sums real serviceCoverageOf() rows rather than re-deriving the
 *  population multipliers by hand, so this side always tracks the TS
 *  engine's OWN formula, never a second hand-copied one (GR#3). */
function sampleAt(tick, state) {
  const rows = new Map(serviceCoverageOf(state).map((r) => [r.id, r]));
  const group = (...ids) => {
    let need = 0;
    let cap = 0;
    for (const id of ids) {
      const r = rows.get(id);
      if (!r) throw new Error(`serviceCoverageOf: missing expected row "${id}"`);
      need += r.need;
      cap += r.cap;
    }
    return { need, cap };
  };

  const fire = group('fire');
  const education = group('nursery', 'primary', 'college');
  const healthcare = group('gp', 'hosp');

  const values = {};
  for (const [name, g] of [
    ['fire', fire],
    ['education', education],
    ['healthcare', healthcare],
  ]) {
    const raw = coverageOf(g.need, g.cap);
    // r1 remediation: report BOTH the CLAMPED ratio (min(1, raw) — the same
    // representation Go's engine coverageRatio() produces, coverage.go:68-73
    // — this is the field Compare() actually checks) and the raw, unclamped
    // ratio as a TS-only informational field (never compared — see this
    // file's header doc comment).
    values[`${name}_capacity`] = roundInt(g.cap);
    values[`${name}_need`] = roundInt(g.need);
    values[`${name}_coverage_x${COVERAGE_SCALE}`] = roundInt(Math.min(1, raw) * COVERAGE_SCALE);
    values[`${name}_ts_unclamped_coverage_x${COVERAGE_SCALE}`] = roundInt(raw * COVERAGE_SCALE);
  }
  return { tick, values };
}

let nextBuildingId = 1;

/**
 * Runs the canonical journal against the TS engine's SimState (bypassing
 * the reducer for place_service/set_population, per the actions file's own
 * placementNote/populationNote) and returns the fixture object
 * ({domain, samples}) in LoadFixture's shape. Pure: takes no arguments
 * beyond the fixed journal file, touches no wall clock, no RNG.
 */
export function emitFixture() {
  const entries = loadJournal();
  let state = initialState();
  nextBuildingId = state.nextId;
  const samples = [];

  for (const entry of entries) {
    switch (entry.op) {
      case 'place_service': {
        const { tsSpec, x, y } = entry.args;
        const building = { id: nextBuildingId++, spec: tsSpec, x, y, builtTick: null };
        state = { ...state, buildings: [...state.buildings, building], nextId: nextBuildingId };
        break;
      }
      case 'set_population': {
        state = { ...state, population: entry.args.n };
        break;
      }
      case 'sample': {
        samples.push(sampleAt(entry.tick, state));
        break;
      }
      default:
        throw new Error(`converge-services-actions.json: unrecognised op ${JSON.stringify(entry.op)}`);
    }
  }

  return { domain: DOMAIN, samples };
}

/** Deterministic, indentation-fixed JSON encoding — same shape every call. */
export function fixtureJSON() {
  return JSON.stringify(emitFixture(), null, 2) + '\n';
}

/** Writes the emitted fixture to internal/converge/testdata, creating the directory if needed. */
export function writeFixtureFile() {
  mkdirSync(path.dirname(OUT_PATH), { recursive: true });
  const json = fixtureJSON();
  writeFileSync(OUT_PATH, json, 'utf8');
  return { path: OUT_PATH, json };
}

// ---------------------------------------------------------------------------
// node:test block. This file lives under webconsole/test/, which CI's bare
// `node --test` (repo-root, .github/workflows/ci.yml) discovers via node's
// default glob regardless of filename — same discipline as
// converge-fixture-emit.mjs. It must never write the committed fixture as a
// side effect of running (F3 lesson, see converge-fixture-emit.mjs's own
// long comment on this) — only "node ... --write" does that, deliberately.
// ---------------------------------------------------------------------------

const CANONICAL_SAMPLE_TICKS = [30, 60, 90];

describe('converge-fixture-emit-services: TS services-coverage trajectory emitter (FEAT-2326609747 inc1)', () => {
  test('emitting the fixture twice yields byte-identical JSON (determinism, AC-6)', () => {
    const a = fixtureJSON();
    const b = fixtureJSON();
    assert.equal(a, b, 'emitFixture() must be a pure function of the actions file + serviceCoverageOf');
  });

  test('the emitted fixture matches LoadFixture\'s shape (domain tag + non-empty samples, AC-1)', () => {
    const fixture = emitFixture();
    assert.equal(fixture.domain, 'services');
    assert.ok(
      Array.isArray(fixture.samples) && fixture.samples.length === CANONICAL_SAMPLE_TICKS.length,
      `expected ${CANONICAL_SAMPLE_TICKS.length} checkpoint samples (ticks ${CANONICAL_SAMPLE_TICKS.join('/')})`
    );
    for (const s of fixture.samples) {
      assert.ok(Number.isInteger(s.tick));
      for (const group of ['fire', 'education', 'healthcare']) {
        for (const suffix of ['_capacity', '_need', `_coverage_x${COVERAGE_SCALE}`, `_ts_unclamped_coverage_x${COVERAGE_SCALE}`]) {
          const field = `${group}${suffix}`;
          assert.ok(Number.isInteger(s.values[field]), `field ${field} at tick ${s.tick} must be an integer, got ${s.values[field]}`);
        }
      }
    }
    assert.deepEqual(
      fixture.samples.map((s) => s.tick),
      CANONICAL_SAMPLE_TICKS,
      'checkpoint ticks must be the canonical logical ticks the actions file declares'
    );
  });

  test('AC-5 sanity: fire is exact 1:1 (need equals population, capacity equals the fire_post catalogue figure) at every checkpoint', () => {
    const fixture = emitFixture();
    const journal = JSON.parse(readFileSync(ACTIONS_PATH, 'utf8')).entries;
    const populationAt = new Map(
      journal.filter((e) => e.op === 'set_population').map((e) => [e.tick, e.args.n])
    );
    for (const s of fixture.samples) {
      assert.equal(s.values.fire_need, populationAt.get(s.tick), `fire_need at tick ${s.tick} must equal the pushed population`);
      assert.equal(s.values.fire_capacity, 4000, 'fire_capacity must equal fire_post\'s catalogue served=4000 (data.ts)');
    }
  });

  test('RED proof: a mutated journal (extra fire station) changes the emitted fire coverage', () => {
    // Prove-can-fail per this increment's dispatch brief (AC-7): the
    // determinism/shape assertions above would pass even if emitFixture()
    // silently ignored the journal and returned a constant. This proves the
    // emitter is actually WIRED to the journal's content by re-running with
    // one extra fire station injected and confirming the final fire
    // coverage genuinely differs.
    const original = emitFixture();
    const originalJournal = JSON.parse(readFileSync(ACTIONS_PATH, 'utf8'));

    const mutated = JSON.parse(JSON.stringify(originalJournal));
    mutated.entries.splice(1, 0, {
      tick: 0,
      op: 'place_service',
      args: { tsSpec: 'fire_post', x: 6, y: 5, buildingID: 'fire_station', goKind: 'fire', goServiceID: 'fire-2' },
    });

    let state = initialState();
    let id = state.nextId;
    const samples = [];
    for (const entry of mutated.entries) {
      if (entry.op === 'place_service') {
        const { tsSpec, x, y } = entry.args;
        const building = { id: id++, spec: tsSpec, x, y, builtTick: null };
        state = { ...state, buildings: [...state.buildings, building], nextId: id };
      } else if (entry.op === 'set_population') {
        state = { ...state, population: entry.args.n };
      } else if (entry.op === 'sample') {
        samples.push(sampleAt(entry.tick, state));
      }
    }
    const mutatedFinalFireCap = samples[samples.length - 1].values.fire_capacity;
    const originalFinalFireCap = original.samples[original.samples.length - 1].values.fire_capacity;
    assert.notEqual(
      mutatedFinalFireCap,
      originalFinalFireCap,
      'an extra fire station must change the final fire_capacity sample — proves the emitter is not vacuously constant'
    );
  });

  test('the committed testdata fixture matches a fresh regeneration (no silent drift, AC-2)', () => {
    // Mirrors converge-fixture-emit.mjs's F3 fix EXACTLY (independent round
    // r1 lesson carried over verbatim): read the file ALREADY on disk (the
    // committed fixture Go's services_ab_test.go actually loads) and diff
    // it against a fresh emitFixture() run, without ever writing through
    // this assertion path. A genuine mismatch (data.ts changed, the
    // journal changed, or the committed file was corrupted/tampered) FAILS
    // with the full JSON diff instead of being silently repaired.
    const fresh = fixtureJSON();
    let committed;
    try {
      committed = readFileSync(OUT_PATH, 'utf8');
    } catch (err) {
      throw new Error(
        `${OUT_PATH} is missing (${err.message}) — regenerate it with ` +
          `"node webconsole/test/converge-fixture-emit-services.mjs --write" and commit the result; ` +
          `this test must never write it as a side effect of running`
      );
    }
    assert.equal(
      committed,
      fresh,
      `${OUT_PATH} does not match a fresh emitFixture() run. If webconsole/src/sim/data.ts or ` +
        `converge-services-actions.json changed on purpose, regenerate the fixture with ` +
        `"node webconsole/test/converge-fixture-emit-services.mjs --write" and commit the diff -- this check ` +
        `never rewrites the committed file itself, it only compares against it (F3, GR#15).`
    );
    const parsed = JSON.parse(committed);
    assert.equal(parsed.domain, 'services');
  });
});

// Allow `node webconsole/test/converge-fixture-emit-services.mjs --write` as
// a one-off manual regeneration script too (outside of node --test),
// matching converge-fixture-emit.mjs's --write discipline and its
// documented F3-follow-up fix (an explicit flag, not argv[1]-alone, because
// node --test's per-file process isolation makes argv[1] equal this file's
// own path on every `node --test` run too).
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url) &&
  process.argv.includes('--write')
) {
  const { path: outPath } = writeFixtureFile();
  console.log(`wrote ${outPath}`);
}
