// FEAT-2326609792 inc1 "TABLE FOUNDATION" — the population scale-ladder
// loader + log-linear interpolator, TypeScript side (mirrors
// internal/engine/traffic/scaleladder.go byte-for-byte on the interpolation
// and rounding rules; see that file's header for the full rationale).
// docs/planning/acceptance/FEAT-2326609792-inc1.md, amended AC-11
// (Bev/Aaron, 2026-09-09): this module is SCHEMA-AGNOSTIC. It does not know
// the field names inc0's data/traffic/scale_ladder.json declares. Every
// rung is flattened into an ordered list of {key, value} numeric leaves
// keyed by dotted JSON path (e.g. "tripsByMode.car"), sorted byte-wise
// ascending by key. Nested objects interpolate element-wise; non-numeric
// leaves (strings, booleans, null, mixed/non-numeric arrays) are carried
// UNCHANGED from the LOWER rung of the interpolated pair and are never
// arithmetically touched. inc2 adds a typed accessor once the field list
// exists; this surface (loadScaleLadder / ladderAt / roundCount) does not
// change between inc1 and inc2 dispatch.
//
// AC-10 (Scope & Interface): nothing in this file, or anywhere else in the
// webconsole, calls ladderAt yet -- inc2 is its first consumer. This module
// exports pure functions only; production wiring (a static `import ... with
// { type: 'json' }` of data/traffic/scale_ladder.json, per AC-4) is a thin
// separate module (scaleLadderData.ts) so this file's tests can exercise
// loadScaleLadder/ladderAt against hand-built fixtures without touching the
// sibling lane's live data file, per Aaron's inc1-builds-before-inc0-lands
// sequencing order.

/** One interpolated (or exact) numeric leaf, keyed by its dotted JSON path.
 * `fields` on a LadderPoint is always sorted byte-wise ascending by key. */
export interface LadderField {
  key: string;
  value: number;
}

/** One leaf that was NOT numeric on the source rung(s) -- carried verbatim
 * from the LOWER rung of the interpolated pair (or the exact rung, for an
 * exact-population query). Never interpolated. */
export interface NonNumericField {
  key: string;
  rawValue: unknown;
}

/** The result of interpolating (or exact-matching) the scale ladder at one
 * population. The registered TypeScript surface (mirrors Go's LadderPoint,
 * AC-11). */
export interface LadderPoint {
  population: number;
  fields: LadderField[];
  nonNumeric: NonNumericField[];
}

interface LadderRung {
  population: number;
  numeric: LadderField[];
  nonNumeric: NonNumericField[];
}

/** The loaded, validated, immutable ladder. Interpolation (ladderAt) is a
 * pure function of a ScaleLadder and a population -- no citizen state, no
 * clock, no randomness (AC-8/AC-9). */
export interface ScaleLadder {
  rungs: LadderRung[];
}

// --- Registry error codes (GR#7) ------------------------------------------
// Minted via `node tools/plan/add-error.js add MET-Vnnn --mkey ui.webconsole
// ...` (data/errors.json). Every throw below is an Error whose message
// begins with one of these codes.
export const ERR_LOAD_FAILED = 'MET-V897'; // ScaleLadderLoadFailed
export const ERR_INVALID = 'MET-V898'; // ScaleLadderInvalid
export const ERR_OUT_OF_RANGE = 'MET-V899'; // ScaleLadderPopulationOutOfRange
// BUG-829 (rework): a rung's flattened key sequence (numeric or
// non-numeric) differs from rung 0's -- rejected at load time so ladderAt's
// by-index merge can safely assume every rung shares rung 0's key set.
export const ERR_KEY_SET_MISMATCH = 'MET-V880'; // ScaleLadderKeySetMismatch

function registryError(code: string, message: string): Error {
  return new Error(`${code}: ${message}`);
}

/** Recursively flattens one decoded JSON value at dotted path `key` into
 * numeric and non-numeric leaf lists. Mirrors Go's flattenLeaf exactly:
 * objects recurse element-wise; arrays of ALL-numeric elements flatten as
 * "key.0", "key.1", ...; every other shape is a single non-numeric leaf. */
function flattenLeaf(
  key: string,
  value: unknown,
  numeric: LadderField[],
  nonNumeric: NonNumericField[],
  population: number,
): void {
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value < 0) {
      throw registryError(
        ERR_INVALID,
        `rung population ${population} has a non-finite or negative numeric leaf ${key}=${value}`,
      );
    }
    numeric.push({ key, value });
    return;
  }
  if (Array.isArray(value)) {
    const allNumeric = value.length > 0 && value.every((e) => typeof e === 'number');
    if (allNumeric) {
      value.forEach((e, i) => {
        const v = e as number;
        if (!Number.isFinite(v) || v < 0) {
          throw registryError(
            ERR_INVALID,
            `rung population ${population} has a non-finite or negative numeric leaf ${key}.${i}=${v}`,
          );
        }
        numeric.push({ key: `${key}.${i}`, value: v });
      });
      return;
    }
    nonNumeric.push({ key, rawValue: value });
    return;
  }
  if (value !== null && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    for (const k of Object.keys(obj).sort()) {
      flattenLeaf(`${key}.${k}`, obj[k], numeric, nonNumeric, population);
    }
    return;
  }
  // string, boolean, null.
  nonNumeric.push({ key, rawValue: value });
}

function flattenRung(body: Record<string, unknown>, population: number): { numeric: LadderField[]; nonNumeric: NonNumericField[] } {
  const numeric: LadderField[] = [];
  const nonNumeric: NonNumericField[] = [];
  for (const k of Object.keys(body).sort()) {
    if (k === 'population') continue;
    flattenLeaf(k, body[k], numeric, nonNumeric, population);
  }
  numeric.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  nonNumeric.sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
  return { numeric, nonNumeric };
}

/**
 * loadScaleLadder validates and flattens a parsed scale-ladder JSON document
 * (AC-1/AC-2/AC-4). It takes the ALREADY-PARSED JSON as its argument
 * (`unknown`) rather than importing the data file itself, so tests can
 * exercise every validation path against small hand-built fixtures under
 * webconsole/test/fixtures/ without depending on the sibling lane's
 * data/traffic/scale_ladder.json (which may not exist yet, or change shape,
 * while this lane works -- Aaron's explicit build-now sequencing order).
 * scaleLadderData.ts wires the real static import + this function together
 * for production use.
 *
 * AC-2/GR#15: the rung count and population list are derived from the data
 * file's OWN meta.rungPopulations declaration, never a literal.
 *
 * Throws a registry-sourced Error (message prefixed with MET-V897/898) on
 * every failure mode: non-object root, missing/malformed meta.rungPopulations,
 * fewer than 2 rungs, meta/rungs population mismatch, unsorted or duplicate
 * populations, or a non-finite/negative numeric leaf. Never a silent partial
 * load.
 */
export function loadScaleLadder(json: unknown): ScaleLadder {
  if (json === null || typeof json !== 'object') {
    throw registryError(ERR_LOAD_FAILED, 'scale ladder root is not an object');
  }
  const file = json as { meta?: { rungPopulations?: unknown }; rungs?: unknown };
  const declared = file.meta?.rungPopulations;
  if (!Array.isArray(declared) || !declared.every((p) => typeof p === 'number')) {
    throw registryError(ERR_LOAD_FAILED, 'meta.rungPopulations is missing or not a numeric array');
  }
  const rawRungs = file.rungs;
  if (!Array.isArray(rawRungs)) {
    throw registryError(ERR_LOAD_FAILED, 'rungs is missing or not an array');
  }
  if (declared.length < 2 || rawRungs.length < 2) {
    throw registryError(ERR_INVALID, `scale ladder has ${rawRungs.length} rung(s), fewer than the minimum of 2`);
  }
  if (declared.length !== rawRungs.length) {
    throw registryError(
      ERR_INVALID,
      `meta.rungPopulations declares ${declared.length} rung population(s) but rungs has ${rawRungs.length}`,
    );
  }

  const rungs: LadderRung[] = rawRungs.map((raw, i) => {
    if (raw === null || typeof raw !== 'object') {
      throw registryError(ERR_LOAD_FAILED, `rungs[${i}] is not an object`);
    }
    const body = raw as Record<string, unknown>;
    const population = body.population;
    if (typeof population !== 'number' || !Number.isFinite(population)) {
      throw registryError(ERR_LOAD_FAILED, `rungs[${i}] has no numeric "population" key`);
    }
    if (population !== declared[i]) {
      throw registryError(
        ERR_INVALID,
        `rungs[${i}].population (${population}) does not match meta.rungPopulations[${i}] (${declared[i]})`,
      );
    }
    const { numeric, nonNumeric } = flattenRung(body, population);
    return { population, numeric, nonNumeric };
  });

  for (let i = 1; i < rungs.length; i++) {
    if (rungs[i].population <= rungs[i - 1].population) {
      throw registryError(
        ERR_INVALID,
        `scale ladder rungs are not strictly ascending by population at index ${i} (${rungs[i - 1].population} >= ${rungs[i].population})`,
      );
    }
  }

  // BUG-829 (P1 PANIC, rework): ladderAt merges bracketing rungs BY INDEX,
  // which is only safe if every rung shares rung 0's exact flattened key
  // sequence (numeric and non-numeric independently). Validate that here,
  // once, at load time -- a missing/extra/renamed leaf on any later rung is
  // rejected with a registry error instead of throwing an undefined.value
  // TypeError (or silently mispairing) at query time.
  validateUniformKeySets(rungs);

  return { rungs };
}

/** keysOf returns the ordered key sequence of a flattened field list. */
function keysOf(fields: Array<{ key: string }>): string[] {
  return fields.map((f) => f.key);
}

function sameKeySequence(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}

/** validateUniformKeySets checks every rung's flattened key sequence
 * (numeric and non-numeric, independently) is identical to rung 0's --
 * mirrors Go's validateUniformKeySets exactly (BUG-829). */
function validateUniformKeySets(rungs: LadderRung[]): void {
  if (rungs.length === 0) return;
  const wantNumeric = keysOf(rungs[0].numeric);
  const wantNonNumeric = keysOf(rungs[0].nonNumeric);
  for (let i = 1; i < rungs.length; i++) {
    const gotNumeric = keysOf(rungs[i].numeric);
    if (!sameKeySequence(wantNumeric, gotNumeric)) {
      throw registryError(
        ERR_KEY_SET_MISMATCH,
        `scale ladder rung ${i} (population ${rungs[i].population}) numeric key set (${gotNumeric.length} keys) does not match rung 0's (${wantNumeric.length} keys)`,
      );
    }
    const gotNonNumeric = keysOf(rungs[i].nonNumeric);
    if (!sameKeySequence(wantNonNumeric, gotNonNumeric)) {
      throw registryError(
        ERR_KEY_SET_MISMATCH,
        `scale ladder rung ${i} (population ${rungs[i].population}) non-numeric key set (${gotNonNumeric.length} keys) does not match rung 0's (${wantNonNumeric.length} keys)`,
      );
    }
  }
}

/**
 * ladderAt interpolates the loaded ladder at `population` (AC-5/AC-11). It
 * takes ONLY the ladder and the population -- no citizen array, no world
 * state -- so it cannot scale with citizen count by construction (AC-9).
 *
 * Interpolation rule (AC-5, byte-for-byte with the Go side): for population
 * p strictly between rung i and i+1, w = (ln p - ln p_i) / (ln p_{i+1} -
 * ln p_i) using Math.log (matching Go's math.Log); each numeric field
 * f = f_i + w*(f_{i+1} - f_i). An exact rung population returns that rung's
 * values verbatim, no arithmetic. p below the first or above the last rung
 * throws ERR_OUT_OF_RANGE -- never clamped, never extrapolated (Aaron's
 * rule). Non-numeric leaves are carried from the LOWER rung of the
 * interpolated pair, unchanged.
 */
export function ladderAt(ladder: ScaleLadder, population: number): LadderPoint {
  const rungs = ladder.rungs;
  const first = rungs[0].population;
  const last = rungs[rungs.length - 1].population;
  if (population < first || population > last) {
    throw registryError(ERR_OUT_OF_RANGE, `population ${population} is outside the scale ladder range [${first}, ${last}]`);
  }

  for (const r of rungs) {
    if (r.population === population) {
      // BUG-828 (rework): copy, never alias -- the ladder is documented
      // immutable; a caller mutating the returned point must never poison
      // r.numeric/r.nonNumeric for every later call.
      return { population, fields: copyFields(r.numeric), nonNumeric: copyNonNumeric(r.nonNumeric) };
    }
  }

  let loIdx = 0;
  for (let i = 0; i < rungs.length - 1; i++) {
    if (rungs[i].population < population && population < rungs[i + 1].population) {
      loIdx = i;
      break;
    }
  }
  const rLo = rungs[loIdx];
  const rHi = rungs[loIdx + 1];

  const w = (Math.log(population) - Math.log(rLo.population)) / (Math.log(rHi.population) - Math.log(rLo.population));

  const fields: LadderField[] = rLo.numeric.map((f, i) => {
    const lo = f.value;
    const hi = rHi.numeric[i].value;
    return { key: f.key, value: lo + w * (hi - lo) };
  });

  // BUG-828 (rework): fields is already a freshly built array (the .map()
  // above), but nonNumeric is otherwise rLo's own array -- copy it too.
  return { population, fields, nonNumeric: copyNonNumeric(rLo.nonNumeric) };
}

/** copyFields returns a fresh array of fresh {key, value} objects -- used
 * on the exact-rung return path (BUG-828) so a caller mutating the result
 * can never reach the loaded ladder's own arrays/objects. */
function copyFields(src: LadderField[]): LadderField[] {
  return src.map((f) => ({ key: f.key, value: f.value }));
}

/** copyNonNumeric returns a fresh array of fresh {key, rawValue} objects,
 * deep-cloning rawValue via structuredClone so a caller mutating a nested
 * object/array inside rawValue can never reach the ladder's own data
 * (BUG-828 -- the attack's TestRoundAliasingExactRung mutates exactly this
 * shape). */
function copyNonNumeric(src: NonNumericField[]): NonNumericField[] {
  return src.map((f) => ({ key: f.key, rawValue: cloneRawValue(f.rawValue) }));
}

function cloneRawValue(v: unknown): unknown {
  if (v === null || typeof v !== 'object') return v;
  return structuredClone(v);
}

/**
 * roundCount is the ONE shared integer-rounding rule (AC-7): floor(x + 0.5)
 * for a non-negative x. Must agree byte-for-byte with Go's RoundCount
 * (internal/engine/traffic/scaleladder.go). Applied exactly once, at the
 * point of use, to any interpolated field the caller treats as an integer
 * count (people, vehicles, spaces) -- ladderAt itself never rounds.
 */
export function roundCount(x: number): number {
  return Math.floor(x + 0.5);
}
