// converge-fixture-emit-population.mjs — FEAT-1972079936 Phase 3 (population).
//
// Headlessly drives the REAL TS reducer (src/sim/engine.ts's initialState +
// reducer, the same pure functions converge-fixture-emit.mjs drives) against
// the canonical journal internal/converge/population_domain.go ALSO consumes
// (test/converge-fixtures/converge-population-actions.json), and emits the
// population trajectory in EXACTLY internal/converge's LoadFixture JSON shape
// ({"domain":"population","samples":[{tick,values}]}). Never hand-authored.
//
// # Field mapping (Go PopulationDomain field -> TS source)
//
//   - "births" / "deaths": the SUM, over the ticks since the previous sample,
//     of the reducer's own per-tick `state.lastDemographics.births/deaths`
//     (engine.ts advance(): Math.round(popBefore * BIRTH_RATE_PER_TICK) /
//     DEATH_RATE_PER_TICK). These are the reducer's real outputs; this file
//     contains NO copy of either rate (the services-round-1 failure was
//     re-implementing the model's arithmetic in the emitter).
//   - "population": natural-change headcount = seeded headcount + running
//     sum(births - deaths). Migration is HELD AT ZERO: after every reducer
//     tick the emitter writes the natural headcount back into
//     state.population, overwriting the reducer's own move-in/move-out
//     result (state.population is set directly exactly as the services
//     emitter's "set_population" does). The reducer therefore always
//     computes births/deaths from the natural headcount, never from a
//     migration-inflated one.
//   - "seed_headcount": the total of the journal's "seed" ops.
//
// Journal ops: "seed" {count, ageYears} (TS has no age structure: only the
// count is used — the age mix is a Go-side input, recorded in the report as a
// model difference), "advance" {days} (1 TS tick == 1 Go day-tick; 30 per
// month on both sides), "sample".
//
// # Capacity
//
// The reducer only applies births/deaths while population <= residential
// capacity (engine.ts advance(); over capacity it decays instead). The
// emitter appends always-online residential towers (builtTick:null, the
// services emitter's precedent) with ample capacity so the natural-change
// branch is the one exercised; the test below asserts this.
//
// # Determinism
//
// emitFixture() is a pure function of the journal plus the reducer: no wall
// clock, no Math.random, no localStorage (BUG-602).

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer } from '../src/sim/engine.ts';
import { onlineResidentsCapacity } from '../src/sim/data.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const ACTIONS_PATH = path.join(__dirname, 'converge-fixtures', 'converge-population-actions.json');
const OUT_PATH = path.join(
  __dirname,
  '..',
  '..',
  'internal',
  'converge',
  'testdata',
  'population-webconsole-v1.json'
);
const DOMAIN = 'population';

// Housing the emitter adds so population <= capacity for the whole journal.
const HOUSING_SPEC = 'res_tower_sgp';
const HOUSING_COUNT = 2;

function loadJournal() {
  const parsed = JSON.parse(readFileSync(ACTIONS_PATH, 'utf8'));
  if (!Array.isArray(parsed.entries)) {
    throw new Error(`${ACTIONS_PATH}: malformed journal (missing "entries" array)`);
  }
  return parsed.entries;
}

/**
 * Runs the canonical journal against the real reducer and returns
 * { fixture: {domain, samples}, minCapacityHeadroom } — headroom is
 * (capacity - natural population) minimised over every tick, so the test can
 * prove the natural-change branch was the one exercised.
 */
function run(entries) {
  let state = initialState();
  let nextId = state.nextId;
  const towers = [];
  for (let i = 0; i < HOUSING_COUNT; i++) {
    towers.push({ id: nextId++, spec: HOUSING_SPEC, x: 40 + i * 10, y: 40, builtTick: null });
  }
  state = { ...state, buildings: [...state.buildings, ...towers], nextId };

  let natural = 0;
  let seeded = 0;
  let births = 0;
  let deaths = 0;
  let minHeadroom = Infinity;
  const samples = [];

  for (const entry of entries) {
    switch (entry.op) {
      case 'seed': {
        const { count } = entry.args;
        if (!Number.isInteger(count) || count < 1) {
          throw new Error(`population journal: seed needs a positive integer count, got ${count}`);
        }
        natural += count;
        seeded += count;
        state = { ...state, population: natural };
        break;
      }
      case 'advance': {
        const { days } = entry.args;
        if (!Number.isInteger(days) || days < 1) {
          throw new Error(`population journal: advance needs a positive integer days, got ${days}`);
        }
        for (let i = 0; i < days; i++) {
          minHeadroom = Math.min(minHeadroom, onlineResidentsCapacity(state) - state.population);
          state = reducer(state, { type: 'tick' });
          const d = state.lastDemographics;
          births += d.births;
          deaths += d.deaths;
          natural += d.births - d.deaths;
          // Migration held at zero: discard the reducer's move-in/out result.
          state = { ...state, population: natural };
        }
        break;
      }
      case 'sample': {
        samples.push({
          tick: entry.tick,
          values: { population: natural, births, deaths, seed_headcount: seeded },
        });
        births = 0;
        deaths = 0;
        break;
      }
      default:
        throw new Error(`converge-population-actions.json: unrecognised op ${JSON.stringify(entry.op)}`);
    }
  }
  return { fixture: { domain: DOMAIN, samples }, minHeadroom };
}

export function emitFixture() {
  return run(loadJournal()).fixture;
}

export function fixtureJSON() {
  return JSON.stringify(emitFixture(), null, 2) + '\n';
}

export function writeFixtureFile() {
  mkdirSync(path.dirname(OUT_PATH), { recursive: true });
  const json = fixtureJSON();
  writeFileSync(OUT_PATH, json, 'utf8');
  return { path: OUT_PATH, json };
}

describe('converge-fixture-emit-population: TS population trajectory emitter (FEAT-1972079936 Phase 3)', () => {
  test('emitting twice yields byte-identical JSON (determinism)', () => {
    assert.equal(fixtureJSON(), fixtureJSON());
  });

  test("the emitted fixture matches LoadFixture's shape and the journal's sample ticks", () => {
    const entries = loadJournal();
    const wantTicks = entries.filter((e) => e.op === 'sample').map((e) => e.tick);
    const fixture = emitFixture();
    assert.equal(fixture.domain, 'population');
    assert.deepEqual(
      fixture.samples.map((s) => s.tick),
      wantTicks
    );
    for (const s of fixture.samples) {
      for (const f of ['population', 'births', 'deaths', 'seed_headcount']) {
        assert.ok(Number.isInteger(s.values[f]), `${f} at tick ${s.tick} must be an integer, got ${s.values[f]}`);
      }
    }
  });

  test('built-not-wired guard: the reducer produced non-zero births AND deaths, in the natural-change branch', () => {
    const { fixture, minHeadroom } = run(loadJournal());
    const births = fixture.samples.reduce((a, s) => a + s.values.births, 0);
    const deaths = fixture.samples.reduce((a, s) => a + s.values.deaths, 0);
    assert.ok(births > 0 && deaths > 0, `births=${births} deaths=${deaths}`);
    assert.ok(minHeadroom >= 0, `population exceeded capacity (headroom ${minHeadroom}): over-capacity branch was exercised, births/deaths are not natural change`);
  });

  test('RED proof: a different seed headcount changes the emitted trajectory (emitter is wired to the journal)', () => {
    const entries = loadJournal();
    const mutated = JSON.parse(JSON.stringify(entries));
    mutated.find((e) => e.op === 'seed').args.count += 500;
    const a = run(entries).fixture.samples.at(-1).values;
    const b = run(mutated).fixture.samples.at(-1).values;
    assert.notDeepEqual(a, b);
  });

  test('the committed testdata fixture matches a fresh regeneration (no silent drift)', () => {
    // Reads the file ALREADY on disk (the one internal/converge/population_ab_test.go
    // loads) and diffs it against a fresh run; never writes. A TS change that
    // moves the trajectory (e.g. engine.ts BIRTH_RATE_PER_TICK) without a
    // regenerated fixture fails here. Regenerate deliberately with
    // "node webconsole/test/converge-fixture-emit-population.mjs --write".
    const fresh = fixtureJSON();
    let committed;
    try {
      committed = readFileSync(OUT_PATH, 'utf8');
    } catch (err) {
      throw new Error(
        `${OUT_PATH} is missing (${err.message}); regenerate with "node webconsole/test/converge-fixture-emit-population.mjs --write" and commit it`
      );
    }
    assert.equal(
      committed,
      fresh,
      `${OUT_PATH} does not match a fresh emitFixture() run; if engine.ts or the journal changed on purpose, regenerate with --write and commit the diff`
    );
    assert.equal(JSON.parse(committed).domain, 'population');
  });
});

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url) &&
  process.argv.includes('--write')
) {
  const { path: outPath } = writeFixtureFile();
  console.log(`wrote ${outPath}`);
}
