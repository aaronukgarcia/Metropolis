// partition-differential.mjs — FEAT-2326609764 (SPATIAL-PARTITION TICK)
// AC-18's differential harness.
//
// Exports `compareAllDerivations(s)`, which runs BOTH implementations of
// every derivation this feature re-expresses as a sector fold and compares
// them with a canonical serialisation that distinguishes -0 from 0, NaN
// from NaN (Object.is, not ===), and preserves Map/Set insertion order.
// Any non-empty result array means the two implementations disagree.
//
// INC1 SCOPE: exactly ONE derivation is compared (totalJobs — the doc's
// §10 inc1 row). The `DERIVATIONS` table below is structured so inc3 (the
// ~13-derivation family) and inc5 (the residue) add rows here without
// touching `compareAllDerivations` itself — GR#3, one comparison engine
// for the life of the feature.
//
// This file is a plain, importable module (not a *.test.mjs) — the actual
// test assertions live in test/sectorPartition.test.mjs, which imports
// `compareAllDerivations` from here and runs it over the corpus. Kept
// separate so `node --test` never tries to auto-discover this as a test
// file in its own right (it has no `test/*.test.mjs` glob match), matching
// the acceptance doc's own file path (§6 AC-18).

import { totalJobsWholeCity } from '../src/sim/data.ts';
import { totalJobsPartitioned } from '../src/sim/sectorPartition.ts';

/**
 * Canonical serialisation (AC-18): stable across key order, distinguishes
 * -0 from 0 and reports NaN literally (Object.is semantics, not ===), and
 * preserves Map/Set INSERTION order rather than re-sorting (a Map/Set
 * comparison sensitive to insertion order is deliberate — see AC-20's
 * separate, explicit order-independence test for the fold itself; this
 * serialiser's job is to catch value drift, not to paper over order bugs).
 */
export function canonicalSerialize(v) {
  if (typeof v === 'number') {
    if (Number.isNaN(v)) return 'NaN';
    if (Object.is(v, -0)) return '-0';
    return String(v);
  }
  if (typeof v === 'bigint') return `${v}n`;
  if (v === null) return 'null';
  if (v === undefined) return 'undefined';
  if (v instanceof Map) {
    const parts = [];
    for (const [k, val] of v) parts.push(`${canonicalSerialize(k)}=>${canonicalSerialize(val)}`);
    return `Map{${parts.join(',')}}`;
  }
  if (v instanceof Set) {
    const parts = [];
    for (const item of v) parts.push(canonicalSerialize(item));
    return `Set{${parts.join(',')}}`;
  }
  if (Array.isArray(v)) return `[${v.map(canonicalSerialize).join(',')}]`;
  if (typeof v === 'object') {
    const keys = Object.keys(v).sort();
    return `{${keys.map((k) => `${k}:${canonicalSerialize(v[k])}`).join(',')}}`;
  }
  return String(v);
}

/**
 * The comparison table. Each entry names a derivation and provides its two
 * independent computations. `whole` is always the pre-existing whole-city
 * path; `partitioned` is the sector-fold reimplementation.
 *
 * BUG-1019 FIX (round opus-round-feat764-inc1 REJECT, lead ruling
 * 2026-09-11): both sides call their NAMED, flag-INDEPENDENT function
 * directly (totalJobsWholeCity / totalJobsPartitioned, both exported
 * specifically so this table never has to go through totalJobs()'s own
 * flag dispatch). Before this fix `whole` called `totalJobs(s)` itself,
 * which reads PARTITIONED_DERIVATIONS internally — so the MOMENT the flag
 * flipped ON (inc6, or any local A/B/test-seam use), this row compared
 * `foldCityJobs(sectorIndexOf(s))` against itself. Proven vacuous by the
 * round: forcing the flag ON alongside a `- 1` per-building mutant in
 * buildSectorIndex left the entire 200-city corpus, every edge fixture,
 * and the 13k scale fixture GREEN — only a separate "default is OFF"
 * assertion caught it. This table's whole/partitioned pair is now
 * genuinely two independent implementations REGARDLESS of what
 * PARTITIONED_DERIVATIONS is set to anywhere in the process.
 *
 * inc1 has exactly one row. inc3 adds ~13 more here (residentsCapacity,
 * totalJobsBySector, totalChildrenCapacity, totalServedCapacity,
 * countByKindOnline, serviceCapacityAggregates, parksCapacityOf,
 * powerStats, waterCaps, wasteGeneratedOf, collectionCapacityOf,
 * processCapacitiesOf) per the acceptance doc's AC-14 list.
 */
const DERIVATIONS = [
  {
    field: 'totalJobs',
    whole: (s) => totalJobsWholeCity(s),
    partitioned: (s) => totalJobsPartitioned(s),
  },
];

/**
 * Runs every row in DERIVATIONS against `s` and reports any mismatch.
 * Returns an EMPTY array when every derivation agrees — the caller asserts
 * `compareAllDerivations(s).length === 0`.
 */
export function compareAllDerivations(s) {
  const mismatches = [];
  for (const { field, whole, partitioned } of DERIVATIONS) {
    const wholeValue = whole(s);
    const partitionedValue = partitioned(s);
    if (canonicalSerialize(wholeValue) !== canonicalSerialize(partitionedValue)) {
      mismatches.push({ field, whole: wholeValue, partitioned: partitionedValue });
    }
  }
  return mismatches;
}
