// scaleLadder.test.ts — FEAT-2326609792 inc1 "TABLE FOUNDATION" TypeScript
// test suite. Consumes webconsole/test/ladder-fixtures/ladder_*.json (owned by
// this lane, NOT the sibling's data/traffic/scale_ladder.json, per Aaron's
// build-now sequencing order) for loader-error tests, and the SHARED
// data/traffic/ladder_vectors.json golden file for the cross-language
// interpolation regression (AC-6) -- the same file
// internal/engine/traffic/scaleladder_test.go consumes.
//
// Every test is written to FAIL against a plausible wrong implementation;
// the mutant is named in each test's own comment.

import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import {
  loadScaleLadder,
  ladderAt,
  roundCount,
  ERR_LOAD_FAILED,
  ERR_INVALID,
  ERR_OUT_OF_RANGE,
  ERR_KEY_SET_MISMATCH,
  type ScaleLadder,
} from '../src/sim/scaleLadder.ts';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

function readFixture(name: string): unknown {
  const raw = readFileSync(path.join(__dirname, 'ladder-fixtures', name), 'utf8');
  return JSON.parse(raw);
}

function assertThrowsCode(fn: () => unknown, code: string, label: string): void {
  try {
    fn();
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    assert.ok(msg.startsWith(code), `${label}: expected error to start with ${code}, got: ${msg}`);
    return;
  }
  assert.fail(`${label}: expected an error, got none`);
}

describe('scaleLadder AC-1 loader errors', () => {
  it('TestAC1_NullRoot: null root -> ERR_LOAD_FAILED (mutant: a loader that skips the root-type check would throw a raw TypeError instead of a registry code)', () => {
    assertThrowsCode(() => loadScaleLadder(null), ERR_LOAD_FAILED, 'null root');
  });

  it('TestAC1_MissingMeta: object with no meta.rungPopulations -> ERR_LOAD_FAILED', () => {
    assertThrowsCode(() => loadScaleLadder({ rungs: [] }), ERR_LOAD_FAILED, 'missing meta');
  });

  it('TestAC1_TooFewRungs: single-rung fixture -> ERR_INVALID (mutant: a loader accepting len<1 instead of len<2)', () => {
    assertThrowsCode(() => loadScaleLadder(readFixture('ladder_single_rung.json')), ERR_INVALID, 'too few rungs');
  });

  it('TestAC1_Unsorted: unsorted fixture -> ERR_INVALID (mutant: a loader that skips the ascending check)', () => {
    assertThrowsCode(() => loadScaleLadder(readFixture('ladder_unsorted.json')), ERR_INVALID, 'unsorted');
  });

  it('TestAC1_Duplicate: duplicate-population fixture -> ERR_INVALID (mutant: strictly-ascending check using <= instead of <)', () => {
    assertThrowsCode(() => loadScaleLadder(readFixture('ladder_duplicate.json')), ERR_INVALID, 'duplicate');
  });

  it('TestAC1_NonFiniteOrNegativeLeaf: negative leaf fixture -> ERR_INVALID (mutant: a loader that skips finite/non-negative checks on flattened leaves)', () => {
    assertThrowsCode(() => loadScaleLadder(readFixture('ladder_non_finite.json')), ERR_INVALID, 'non-finite leaf');
  });

  it('TestAC2_RungCountMismatch: meta declares 3 populations but rungs has 4 -> ERR_INVALID, never silently truncated/padded (AC-2/GR#15: derived-count enforcement)', () => {
    assertThrowsCode(() => loadScaleLadder(readFixture('ladder_rung_count_mismatch.json')), ERR_INVALID, 'rung count mismatch');
  });

  it('TestAC1_PopulationOutOfRange: querying below/above the loaded range throws ERR_OUT_OF_RANGE, never clamps (mutant: a ladderAt that clamps to the nearest rung instead of throwing)', () => {
    const ladder = loadScaleLadder(readFixture('ladder_valid.json'));
    assertThrowsCode(() => ladderAt(ladder, 1), ERR_OUT_OF_RANGE, 'below range');
    assertThrowsCode(() => ladderAt(ladder, 999999999), ERR_OUT_OF_RANGE, 'above range');
  });

  it('TestBUG834_PerIndexPopulationMismatch: meta says [100,1000,5001] but rungs[2].population is 5000 (equal length, differing VALUE) -> ERR_INVALID (mutant: a loader that disables only the per-index compare, leaving the length check, accepts this)', () => {
    assertThrowsCode(
      () => loadScaleLadder(readFixture('ladder_rung_population_value_mismatch.json')),
      ERR_INVALID,
      'per-index population value mismatch',
    );
  });
});

describe('scaleLadder BUG-831 flattened key order is sorted', () => {
  it('TestBUG831_FlattenKeyOrderSorted: ladder_key_order.json authors keys scrambled (zulu, mike, alpha; nested.zebra before nested.apple) -- the flattened output must come out byte-sorted ascending, exactly, not just "some" order (mutant: removing the .sort() calls in flattenRung/flattenLeaf)', () => {
    const ladder = loadScaleLadder(readFixture('ladder_key_order.json'));
    const point = ladderAt(ladder, 100);
    const got = point.fields.map((f) => f.key);
    const want = ['alpha', 'mike', 'nested.apple', 'nested.zebra', 'zulu'];
    assert.deepEqual(got, want);
    const sorted = [...got].sort();
    assert.deepEqual(got, sorted, 'flattened key order must already be byte-sorted');
  });
});

describe('scaleLadder BUG-829 key-set mismatch rejected at load', () => {
  const cases: Array<[string, unknown]> = [
    [
      'upper rung has FEWER numeric leaves',
      { meta: { rungPopulations: [100, 1000] }, rungs: [{ population: 100, a: 1, b: 2 }, { population: 1000, a: 2 }] },
    ],
    [
      'upper rung has MORE numeric leaves',
      { meta: { rungPopulations: [100, 1000] }, rungs: [{ population: 100, a: 1 }, { population: 1000, a: 2, zz: 9 }] },
    ],
    [
      'same count, DIFFERENT keys',
      { meta: { rungPopulations: [100, 1000] }, rungs: [{ population: 100, a: 1, b: 100 }, { population: 1000, a: 2, c: 900 }] },
    ],
    [
      'ragged numeric array',
      { meta: { rungPopulations: [100, 1000] }, rungs: [{ population: 100, a: [1, 2, 3] }, { population: 1000, a: [2] }] },
    ],
  ];
  for (const [name, body] of cases) {
    it(`TestBUG829_KeySetMismatch: ${name} -> ERR_KEY_SET_MISMATCH, rejected AT LOAD (mutant: removing validateUniformKeySets from loadScaleLadder lets this load, then ladderAt at an interpolated population either throws a raw TypeError on undefined.value or silently mispairs keys by index)`, () => {
      assertThrowsCode(() => loadScaleLadder(body), ERR_KEY_SET_MISMATCH, name);
    });
  }
});

describe('scaleLadder BUG-828 no aliasing of the loaded ladder', () => {
  it('TestBUG828_AliasingExactRung: mutating a returned LadderPoint (fields, and a nested rawValue array) must never affect a later call, on both the exact-rung and interpolated paths', () => {
    const ladder = loadScaleLadder({
      meta: { rungPopulations: [100, 1000] },
      rungs: [
        { population: 100, a: 1, tags: ['lo'] },
        { population: 1000, a: 2, tags: ['hi'] },
      ],
    });

    const p1 = ladderAt(ladder, 100);
    p1.fields[0].value = 999;
    (p1.nonNumeric[0].rawValue as string[])[0] = 'POISONED';
    const p2 = ladderAt(ladder, 100);
    assert.equal(p2.fields[0].value, 1, 'ALIASING: exact-rung fields aliases ladder internals');
    assert.deepEqual(p2.nonNumeric[0].rawValue, ['lo'], 'ALIASING: exact-rung nonNumeric aliases ladder internals');

    const p3 = ladderAt(ladder, 300);
    (p3.nonNumeric[0].rawValue as string[])[0] = 'POISONED2';
    const p4 = ladderAt(ladder, 300);
    assert.deepEqual(p4.nonNumeric[0].rawValue, ['lo'], 'ALIASING: interpolated nonNumeric aliases ladder internals');
  });
});

describe('scaleLadder AC-2 data-derived rung count', () => {
  it('TestAC2_RungCountDerivedFromFixture: rung count read from the fixture file itself, never a literal in this test', () => {
    const raw = readFixture('ladder_valid.json') as { meta: { rungPopulations: number[] } };
    const ladder = loadScaleLadder(raw);
    assert.equal(ladder.rungs.length, raw.meta.rungPopulations.length);
    for (let i = 0; i < raw.meta.rungPopulations.length; i++) {
      assert.equal(ladder.rungs[i].population, raw.meta.rungPopulations[i]);
    }
  });
});

// --- Golden vector cross-language regression (AC-5/AC-6/AC-7) --------------

interface GoldenCase {
  population: number;
  kind: string;
  expectedFields?: Record<string, number>;
  expectedNonNumeric?: Record<string, unknown>;
  expectedRoundedPeopleCount?: number;
  expectError?: boolean;
}

interface GoldenVectorFile {
  meta: { toleranceRule: string };
  ladder: unknown;
  cases: GoldenCase[];
}

function readGoldenVectors(): GoldenVectorFile {
  const raw = readFileSync(path.join(__dirname, '..', '..', 'data', 'traffic', 'ladder_vectors.json'), 'utf8');
  return JSON.parse(raw) as GoldenVectorFile;
}

describe('scaleLadder AC-5/AC-6 golden vectors (shared with Go)', () => {
  const gv = readGoldenVectors();
  let ladder: ScaleLadder;

  it('loads the shared golden ladder without error', () => {
    ladder = loadScaleLadder(gv.ladder);
    assert.ok(ladder.rungs.length >= 2);
  });

  it(`has at least 12 cases (found ${gv.cases.length})`, () => {
    assert.ok(gv.cases.length >= 12);
  });

  for (const c of gv.cases) {
    it(`population=${c.population} (${c.kind}): matches the shared golden vector (mutant this catches for p=3000: a linear-in-population interpolator instead of log-linear produces different field values, per the vector file's own meta.mutantNote)`, () => {
      if (c.expectError) {
        assert.throws(() => ladderAt(ladder, c.population));
        return;
      }
      const point = ladderAt(ladder, c.population);
      const got = new Map(point.fields.map((f) => [f.key, f.value]));
      for (const [key, want] of Object.entries(c.expectedFields ?? {})) {
        const v = got.get(key);
        assert.ok(v !== undefined, `missing field ${key}`);
        const tol = c.kind === 'exact' ? 0 : 1e-12 * Math.max(1, Math.abs(want));
        assert.ok(Math.abs((v as number) - want) <= tol, `field ${key} = ${v}, want ${want} (tol ${tol})`);
      }
      if (c.expectedRoundedPeopleCount !== undefined) {
        const rounded = roundCount(got.get('counts.peopleCount') as number);
        assert.equal(rounded, c.expectedRoundedPeopleCount);
      }
      const nn = new Map(point.nonNumeric.map((f) => [f.key, f.rawValue]));
      for (const [key, want] of Object.entries(c.expectedNonNumeric ?? {})) {
        assert.deepEqual(nn.get(key), want, `non-numeric field ${key}`);
      }
    });
  }
});

describe('scaleLadder AC-7 roundCount (shared rounding rule)', () => {
  const cases: Array<[number, number]> = [
    [0, 0],
    [0.4, 0],
    [0.5, 1],
    [0.9999, 1],
    [1865.214, 1865],
    [1865.6, 1866],
    [1865.5, 1866],
    [2.5, 3],
    // BUG-833: the half-ulp input where JS Math.round (spec: floor(x+0.5))
    // and Go math.Round (round-half-away-from-zero, computed exactly)
    // diverge: x+0.5 rounds to exactly 1.0 in float64 (ties-to-even), so
    // floor(x+0.5) = 1, while math.Round(x) = 0. Pins the AC-7 rule
    // explicitly (a Go-side "cleanup" to math.Round would silently break
    // Go/TS parity, which is exactly what AC-7 exists to prevent).
    [0.49999999999999994, 1],
  ];
  for (const [input, want] of cases) {
    it(`roundCount(${input}) === ${want} (mutant: plain Math.trunc/floor without the +0.5 offset would round 1865.6 down to 1865, not up to 1866)`, () => {
      assert.equal(roundCount(input), want);
    });
  }
});

describe('scaleLadder AC-8 determinism', () => {
  it('TestAC8_Determinism: 10 repeated calls are byte-identical via JSON.stringify (mutant: any map/object key iteration reaching an output path without a sort could diverge order across runs)', () => {
    const ladder = loadScaleLadder(readFixture('ladder_valid.json'));
    let first: string | null = null;
    for (let i = 0; i < 10; i++) {
      const point = ladderAt(ladder, 3000);
      const enc = JSON.stringify(point);
      if (first === null) {
        first = enc;
      } else {
        assert.equal(enc, first, `iteration ${i} diverged`);
      }
    }
  });

  it('TestAC8_NoTimeOrRandomInSource: grep-style guard on the production source', () => {
    const src = readFileSync(path.join(__dirname, '..', 'src', 'sim', 'scaleLadder.ts'), 'utf8');
    assert.ok(!/Date\.now|new Date\(/.test(src), 'scaleLadder.ts must not read the wall clock (GR#21)');
    assert.ok(!/Math\.random/.test(src), 'scaleLadder.ts must not use randomness (GR#21)');
  });
});

describe('scaleLadder AC-9 structural cost guarantee', () => {
  it('TestAC9_SignatureTakesNoCitizenState: ladderAt(ladder, population) -- no citizen array argument by construction (if this compiles/typechecks with only two args, the signature is right)', () => {
    const ladder = loadScaleLadder(readFixture('ladder_valid.json'));
    const point = ladderAt(ladder, 3000);
    assert.ok(point.fields.length > 0);
  });
});

describe('scaleLadder AC-10 no consumer yet', () => {
  it('TestAC10_NoConsumerInSrc: nothing under src/ except scaleLadder.ts/scaleLadderData.ts and the sanctioned traffic consumers (inc2 trafficDemand.ts, inc3 trafficAssignment.ts) calls ladderAt (mutant: a stray wiring call elsewhere)', () => {
    const srcDir = path.join(__dirname, '..', 'src', 'sim');
    const entries = readdirSync(srcDir).filter((f: string) => f.endsWith('.ts'));
    const callRe = /\bladderAt\(/;
    for (const name of entries) {
      // BUG-860 (2026-09-09): inc2 (trafficDemand.ts) is the ladder's
      // sanctioned first consumer and inc3 (trafficAssignment.ts) its second;
      // this guard is about STRAY wiring, not the increments the epic planned.
      const sanctioned = new Set(['scaleLadder.ts', 'scaleLadderData.ts', 'trafficDemand.ts', 'trafficAssignment.ts']);
      if (sanctioned.has(name)) continue;
      const content = readFileSync(path.join(srcDir, name), 'utf8');
      assert.ok(!callRe.test(content), `${name} calls ladderAt -- AC-10 forbids a consumer before inc2`);
    }
  });
});
