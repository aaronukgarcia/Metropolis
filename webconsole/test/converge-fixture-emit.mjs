// converge-fixture-emit.mjs — FEAT-1972079936 Phase-3 inc2.
//
// Headlessly runs the TS webconsole sim (src/sim/engine.ts's initialState +
// reducer, the SAME pure functions genesis-replay.test.mjs and fiscal.test.mjs
// already drive in node) against the canonical action list this increment's
// Go bridge (internal/converge/finance_ab_actions.go) ALSO consumes
// (test/converge-fixtures/converge-finance-actions.json), and emits the resulting
// finance trajectory in EXACTLY internal/converge's LoadFixture JSON shape
// (internal/converge/fixture.go's fixtureFile: {"domain": "...", "samples":
// [{"tick": N, "values": {...}}]}). Never a hand-authored fixture (mirrors
// fixture.go's SaveFixture doc comment: "used ... to produce a fixture from
// a live run — never hand-authored").
//
// # Field mapping (TS SimState -> the finance Contract's field names)
//
// The Go composed engine (internal/engine/compose) exposes exactly ONE
// money-stock accessor on its public Composition surface today —
// Composition.Treasury() (compose.go) — so this emitter's OWN Go-comparable
// field is just "treasury". Compare() (internal/converge/compare.go) only
// checks fields the REFERENCE (Go) trajectory reports, so this fixture is
// free to carry three more TS-only descriptive fields (income/expenses/net)
// for the AB report's readability without breaking Compare's fail-closed
// unknown-field gate.
//
//   - "treasury": state.funds (whole GBP, src/sim/fiscal.ts's STARTING_TREASURY
//     is denominated in GBP) converted to milli-pounds by multiplying by
//     finance.MicropoundsPerPound (internal/engine/finance/money.go: 1 GBP =
//     1,000 units since the BUG-452 rebase, 2026-09-01) and truncating to an
//     integer with Math.trunc — mirrors Go's own int64 Money truncation
//     convention (money.go's mulDiv doc comment: "truncated (rounded toward
//     zero)"), so both sides round the same direction.
//   - "income": the sum of computeFlows(state).inflows[].value at the sample
//     tick (GBP/tick), converted to milli-pounds the same way. TS-only —
//     Go's Composition exposes no per-tick inflow breakdown, only the
//     cumulative MoneyFlows() gross figure, so this field is descriptive-only
//     in the AB report, never fed through Compare.
//   - "expenses": the sum of computeFlows(state).outflows[].value, same
//     conversion. TS-only, same reason as "income".
//   - "net": income - expenses (post-conversion), TS-only.
//
// # Determinism
//
// emitFixture() is a pure function of the actions file's contents plus the
// TS engine's own pure initialState()/reducer() — no wall clock, no
// Math.random (the reducer's own doc discipline; this file adds none of its
// own). Two calls therefore produce byte-identical JSON, proven by this
// file's own node:test block below (which node's default test glob
// discovers automatically — see the guard comment above that block for why
// this file is safe to both `import` as a module AND run directly).

import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import { initialState, reducer, computeFlows } from '../src/sim/engine.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// MICROPOUNDS_PER_POUND mirrors internal/engine/finance/money.go's
// MicropoundsPerPound EXACTLY (1 GBP = 1,000 units since BUG-452,
// 2026-09-01) — a literal copy, not an import, because this file is TS-side
// tooling and may not depend on the Go module (mirrors the doc.go layering
// note: the TS side is a fixture, never a live process the Go module reaches
// into). Any future re-scale of finance.MicropoundsPerPound must update this
// constant too, or the units-lint class of bug (BUG-355) recurs here.
const MICROPOUNDS_PER_POUND = 1000;

// NOTE (bridge-collision fix, this increment): NOT 'fixtures' -- that
// directory name is already OWNED by serve-bundle.test.mjs, whose
// cleanupFixtures()/setupFixtures() pair does an unconditional
// rmSync(resolve(__dirname, 'fixtures'), {recursive:true, force:true})
// as its OWN test scratch-space lifecycle. node --test's default glob
// runs every test/*.mjs file in the same process tree with no ordering
// guarantee, so a shared 'fixtures' directory meant this file's fixture
// got silently deleted out from under it depending on run order --
// discovered live during this increment's gate-running (the file
// vanished between two test runs with no code change). 'converge-fixtures'
// is a dedicated namespace this increment owns exclusively.
const ACTIONS_PATH = path.join(__dirname, 'converge-fixtures', 'converge-finance-actions.json');
const OUT_PATH = path.join(
  __dirname,
  '..',
  '..',
  'internal',
  'converge',
  'testdata',
  'finance-webconsole-v1.json'
);
const DOMAIN = 'finance';

/** Convert a GBP figure to Go's milli-pound integer scale, truncating toward zero. */
function toMilliPounds(gbp) {
  return Math.trunc(gbp * MICROPOUNDS_PER_POUND);
}

function loadActions() {
  const raw = readFileSync(ACTIONS_PATH, 'utf8');
  const parsed = JSON.parse(raw);
  if (!Array.isArray(parsed.actions)) {
    throw new Error(`${ACTIONS_PATH}: malformed action list (missing "actions" array)`);
  }
  return parsed.actions;
}

function sumFlow(items) {
  let total = 0;
  for (const item of items) total += item.value;
  return total;
}

/** Snapshot the finance fields at the current state, per the mapping table above. */
function sampleAt(tick, state) {
  const { inflows, outflows } = computeFlows(state);
  const income = sumFlow(inflows);
  const expenses = sumFlow(outflows);
  return {
    tick,
    values: {
      treasury: toMilliPounds(state.funds),
      income: toMilliPounds(income),
      expenses: toMilliPounds(expenses),
      net: toMilliPounds(income) - toMilliPounds(expenses),
    },
  };
}

/**
 * Runs the canonical action list against the TS engine and returns the
 * fixture object ({domain, samples}) in LoadFixture's shape. Pure: takes no
 * arguments beyond the fixed actions file, touches no wall clock, no RNG.
 */
export function emitFixture() {
  const actions = loadActions();
  let state = initialState();
  let logicalTick = 0;
  const samples = [];

  for (const action of actions) {
    switch (action.op) {
      case 'advance': {
        const n = action.n;
        if (!Number.isInteger(n) || n <= 0) {
          throw new Error(`converge-finance-actions.json: "advance" op needs a positive integer "n", got ${n}`);
        }
        for (let i = 0; i < n; i++) {
          state = reducer(state, { type: 'tick' });
        }
        logicalTick += n;
        if (action.sampleAfterTick !== undefined && action.sampleAfterTick !== logicalTick) {
          throw new Error(
            `converge-finance-actions.json: declared sampleAfterTick=${action.sampleAfterTick} does not match the cumulative logical tick ${logicalTick} — the action list's own checkpoint bookkeeping is inconsistent`
          );
        }
        samples.push(sampleAt(logicalTick, state));
        break;
      }
      case 'zone':
      case 'place_utility_ts_only': {
        const { x, y } = action.cell;
        state = reducer(state, { type: 'place', spec: action.tsSpec, x, y });
        break;
      }
      default:
        throw new Error(`converge-finance-actions.json: unrecognised op ${JSON.stringify(action.op)}`);
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
// node:test block. This file lives under webconsole/test/, which node's
// default `node --test` discovery glob (**/test/**/*.{js,mjs,cjs}) picks up
// REGARDLESS of filename — so this file is a live CI test file, not just an
// importable module, and must behave correctly when node --test runs it with
// no arguments. The block below proves determinism (deliverable #1's
// explicit requirement) and, per the F3 fix below, COMPARES a fresh
// regeneration against the already-committed testdata/finance-webconsole-v1.json
// consumed by internal/converge/finance_ab_test.go — it deliberately does
// NOT write that file as a side effect of running (a prior version did,
// which meant `node --test` would silently repair a stale or tampered
// fixture instead of failing on it). Regenerating the committed fixture on
// purpose is a manual, visible step: `node webconsole/test/converge-fixture-emit.mjs --write`
// (the entry point at the bottom of this file) — its result must be reviewed
// and committed like any other change, never auto-applied by the test run.
// ---------------------------------------------------------------------------

// Canonical checkpoint ticks — the single source both the shape assertion
// and the exact-ticks assertion below read from (GR#15: never re-type the
// same expected value twice independently).
const CANONICAL_SAMPLE_TICKS = [30, 60, 90];

describe('converge-fixture-emit: TS finance trajectory emitter (FEAT-1972079936 inc2)', () => {
  test('emitting the fixture twice yields byte-identical JSON (determinism)', () => {
    const a = fixtureJSON();
    const b = fixtureJSON();
    assert.equal(a, b, 'emitFixture() must be a pure function of the actions file + engine.ts');
  });

  test('the emitted fixture matches LoadFixture\'s shape (domain tag + non-empty samples)', () => {
    const fixture = emitFixture();
    assert.equal(fixture.domain, 'finance');
    assert.ok(
      Array.isArray(fixture.samples) && fixture.samples.length === CANONICAL_SAMPLE_TICKS.length,
      `expected ${CANONICAL_SAMPLE_TICKS.length} checkpoint samples (ticks ${CANONICAL_SAMPLE_TICKS.join('/')})`
    );
    for (const s of fixture.samples) {
      assert.ok(Number.isInteger(s.tick));
      assert.ok(Number.isInteger(s.values.treasury));
    }
    assert.deepEqual(
      fixture.samples.map((s) => s.tick),
      CANONICAL_SAMPLE_TICKS,
      'checkpoint ticks must be the canonical logical ticks the actions file declares'
    );
  });

  test('RED proof: a mutated action list (double the residential placements) changes the emitted treasury trajectory', () => {
    // Prove-can-fail per this increment's dispatch brief: the determinism/shape
    // assertions above would pass even if emitFixture() silently ignored the
    // actions file and returned a constant. This test proves the emitter is
    // actually WIRED to the action list's content — a real functional check,
    // not a vacuous one — by re-running with one extra spend action injected
    // and confirming the tick-30 treasury sample genuinely differs.
    const original = emitFixture();
    const originalActions = JSON.parse(readFileSync(ACTIONS_PATH, 'utf8'));

    const mutatedActions = JSON.parse(JSON.stringify(originalActions));
    mutatedActions.actions.splice(1, 0, {
      op: 'zone',
      cell: { x: 50, y: 50 },
      zoneType: 'dwelling',
      tsSpec: 'res_hut',
    });

    let state = initialState();
    for (const action of mutatedActions.actions) {
      if (action.op === 'advance') {
        for (let i = 0; i < action.n; i++) state = reducer(state, { type: 'tick' });
      } else {
        state = reducer(state, { type: 'place', spec: action.tsSpec, x: action.cell.x, y: action.cell.y });
      }
    }
    const mutatedTreasury = toMilliPounds(state.funds);
    const originalFinalTreasury = original.samples[original.samples.length - 1].values.treasury;
    assert.notEqual(
      mutatedTreasury,
      originalFinalTreasury,
      'an extra GBP220,000 placement must change the final treasury sample — proves the emitter is not vacuously constant'
    );
  });

  test('the committed testdata fixture matches a fresh regeneration (no silent drift)', () => {
    // F3 FIX (independent round r1, REJECT-class): this test used to call
    // writeFixtureFile() and then read back the SAME file it had just
    // written -- a self-check of the form x==x, not a comparison against
    // anything independent. The r1 attacker corrupted a value in the
    // COMMITTED fixture on disk, ran `node --test`, and this block silently
    // overwrote the corruption back to a "correct" value and reported
    // green, with zero test failures -- exactly the silent-drift class GR#15
    // exists to prevent (a stale/tampered fixture must fail loudly, never
    // be quietly repaired). The fix: read the file ALREADY on disk (the
    // committed fixture Go's finance_ab_test.go actually loads) and diff it
    // against a fresh emitFixture() run, without ever writing through this
    // assertion path. A genuine mismatch (engine.ts changed, the action
    // list changed, or the committed file was corrupted/tampered) now FAILS
    // with the full JSON diff instead of being silently repaired.
    //
    // Regenerating the committed fixture on purpose (engine.ts or the
    // action list legitimately changed) is still a normal, visible step:
    // run `node webconsole/test/converge-fixture-emit.mjs --write` directly (the
    // manual-regeneration entry point at the bottom of this file, which
    // still calls writeFixtureFile()) and commit the resulting diff.
    const fresh = fixtureJSON();
    let committed;
    try {
      committed = readFileSync(OUT_PATH, 'utf8');
    } catch (err) {
      throw new Error(
        `${OUT_PATH} is missing (${err.message}) — regenerate it with ` +
          `"node webconsole/test/converge-fixture-emit.mjs --write" and commit the result; ` +
          `this test must never write it as a side effect of running`
      );
    }
    assert.equal(
      committed,
      fresh,
      `${OUT_PATH} does not match a fresh emitFixture() run. If webconsole/src/sim/engine.ts or ` +
        `converge-finance-actions.json changed on purpose, regenerate the fixture with ` +
        `"node webconsole/test/converge-fixture-emit.mjs --write" and commit the diff -- this check ` +
        `never rewrites the committed file itself, it only compares against it (F3, GR#15).`
    );
    const parsed = JSON.parse(committed);
    assert.equal(parsed.domain, 'finance');
  });
});

// Allow `node webconsole/test/converge-fixture-emit.mjs --write` as a one-off
// manual regeneration script too (outside of node --test), matching the
// emitFixture/writeFixtureFile exports' own "never hand-authored" discipline.
//
// F3 FOLLOW-UP FIX (2026-09-01, found while proving the F3 red/green proof):
// this used to trigger off `process.argv[1] === this file` ALONE, with no
// explicit flag. Node's test runner defaults to per-file PROCESS isolation
// (node --test spawns each discovered test file as its OWN child process,
// invoked as `node <that file>`), which means process.argv[1] equals THIS
// file's path every time `node --test` runs it too -- not just a genuine
// manual invocation. That silently re-triggered writeFixtureFile() before
// the node:test block below even ran, re-overwriting a deliberately
// corrupted committed fixture back to a fresh one and making the F3 fix
// above pass vacuously under `node --test` specifically (proven live: a
// corrupted testdata/finance-webconsole-v1.json came back CORRECT after a
// full `node --test` run, with zero test failures reported). Requiring an
// explicit `--write` flag removes the ambiguity: `node --test` never passes
// this flag, so only a genuine, deliberate manual regeneration run writes
// the file now.
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url) &&
  process.argv.includes('--write')
) {
  const { path: outPath } = writeFixtureFile();
  console.log(`wrote ${outPath}`);
}
