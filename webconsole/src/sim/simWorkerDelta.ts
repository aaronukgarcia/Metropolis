// FEAT-2326609777 (2026-09-06) — delta-sync for the Web Worker tick offload.
//
// MEASURED ROOT CAUSE (capture-13 dogfood city: 38,251 buildings / 9.48M
// citizens, E:/gotmp/dbg13.json): a full SimState clone (Landing 2's
// postMessage protocol — see simWorkerProtocol.ts's SCOPE NOTE) costs ~40ms
// per direction in a same-process structuredClone() benchmark, and per-field
// JSON-byte measurement shows `buildings` alone is 92.1% of the payload
// (3,509,593 of 3,811,285 bytes); `roadConnectivity` (recomputed from
// scratch every single tick by advance() regardless of whether any building
// actually changed — see engine.ts's `s = { ...s, roadConnectivity:
// computeRoadConnectivity(s) }`) is the next-largest single field at 4.2%
// (159,384 bytes). Together these two fields are >96% of the wire payload.
// Real browser Worker postMessage overhead measured far higher than the
// Node benchmark (814-2761ms per round trip per the BOW item, vs ~80ms/
// round-trip structuredClone() in this repo's Node harness) — the ABSOLUTE
// numbers differ by environment, but the field-size breakdown (which is
// what this module optimises) does not: `buildings` dominates by
// construction (SimState.buildings scales with city size; every other
// field is either a fixed-size scalar or a small bounded ring/history).
//
// THE FIX: rather than the full ~3.5MB buildings array crossing the thread
// boundary on every tick, this module diffs/patches it. The reducer's own
// established idiom (engine.ts: `s.buildings.map((b) => shouldChange(b) ?
// {...b, ...} : b)`) preserves REFERENCE IDENTITY for every unchanged
// building — confirmed at evaluateRoadMonitors/evaluateBuildingMonitors and
// every other buildings-array producer in engine.ts — so a same-length,
// no-removal diff is a single O(n) reference-equality scan (cheap: no deep
// compare, no serialisation) that, on the overwhelming majority of ticks
// (nothing auto-scaled, nothing was consolidator-built/scrapped this tick),
// finds ZERO changed buildings and produces a near-empty delta.
//
// PROTOCOL SHAPE: store.tsx (main) and simWorker.ts (worker) each keep a
// cache of "the last full SimState the OTHER side is known to hold" —
// main's `workerKnownStateRef`, the worker's own `cachedState` module
// variable (see simWorker.ts). Every tick request/reply is the DIFFERENCE
// between that cache and the sender's actual current/just-computed state;
// the receiver reconstructs the full state by applying the diff to ITS OWN
// matching cache (which is, by the protocol invariant below, guaranteed to
// be byte-identical to what the diff was computed against).
//
// INVARIANT this relies on (see store.tsx's issueTickRequest/worker.onmessage
// and simWorkerOffloadController.ts's `workerBusy`): AT MOST ONE tick
// request is ever outstanding at a time — beginTickRequest refuses to issue
// a second one while workerBusy is true, and workerBusy is cleared only
// once the worker's reply for the first has actually been observed. So the
// worker only ever processes requests strictly serially, and main only
// ever computes a NEW diff after the previous round trip has fully
// resolved (applied OR discarded) — there is never a "diff computed against
// a cache that has since moved" race.
//
// BACKWARD COMPATIBILITY (see store.tsx's `deltaCapable` handling): a
// legacy/mocked worker that only ever replies with a bare `tickResult` (no
// `deltaCapable` flag) is NEVER switched into delta mode — every existing
// FakeWorker-based test in test/simworker-offload.test.mjs,
// test/feat-2326609771-webworker-default-on.test.tsx, and
// test/attack-feat-webworker-defaulton-round.test.tsx keeps working
// unmodified, since none of them set that flag. Only the REAL simWorker.ts
// (which always sets it) unlocks the optimised path.
import type { Building, SimState } from './types.ts';

/** The change in `SimState.buildings` between a base state and a next
 *  state. See applyBuildingsDelta for the exact reconstruction contract. */
export interface BuildingsDelta {
  /** Full Building objects that are new (not present in the base array) or
   *  whose reference differs from the base array's entry with the same id
   *  (i.e. genuinely mutated this tick — capacityTier/heightStoreys/etc). */
  changed: Building[];
  /** ids present in the base array but absent from the next array
   *  (demolished/scrapped this tick). */
  removedIds: number[];
  /** False (the fast, common path) whenever the next array is the SAME
   *  length as the base array with NO removals — in that case every
   *  surviving building occupies the SAME INDEX in both arrays (guaranteed
   *  by the reducer's `.map()`-over-base idiom), so `changed` alone is
   *  sufficient to reconstruct the array via a single positional merge.
   *  True whenever anything was added or removed this tick (or, belt and
   *  braces, if id order itself somehow changed) — in that case `order`
   *  carries the full id sequence so reconstruction never guesses at
   *  ordering. */
  orderChanged: boolean;
  /** Present only when `orderChanged` is true: every id in `next`, in
   *  order. */
  order?: number[];
}

/** Pure diff: `base` and `next` are both real `Building[]` snapshots (the
 *  state immediately before and immediately after one tick, or the two
 *  states either side of a resync). Reference-equality only — O(n), no
 *  deep comparison, matching the reducer's own preserve-unchanged-refs
 *  contract (see this module's header). */
export function diffBuildings(base: readonly Building[], next: readonly Building[]): BuildingsDelta {
  const baseMap = new Map<number, Building>();
  for (const b of base) baseMap.set(b.id, b);

  const nextIds = new Set<number>();
  const changed: Building[] = [];
  for (const b of next) {
    nextIds.add(b.id);
    if (baseMap.get(b.id) !== b) changed.push(b);
  }

  const removedIds: number[] = [];
  for (const b of base) {
    if (!nextIds.has(b.id)) removedIds.push(b.id);
  }

  let orderChanged = removedIds.length > 0 || next.length !== base.length;
  if (!orderChanged) {
    for (let i = 0; i < base.length; i++) {
      if (base[i].id !== next[i].id) {
        orderChanged = true;
        break;
      }
    }
  }

  const delta: BuildingsDelta = { changed, removedIds, orderChanged };
  if (orderChanged) delta.order = next.map((b) => b.id);
  return delta;
}

/** Pure reconstruction: the exact inverse of diffBuildings — for any
 *  (base, next) pair, `applyBuildingsDelta(base, diffBuildings(base, next))`
 *  produces an array deep-equal to `next` (test/feat-2326609777-*.test.mjs
 *  round-trips this directly against real capture-13-derived data). */
export function applyBuildingsDelta(base: readonly Building[], delta: BuildingsDelta): Building[] {
  const changedMap = new Map<number, Building>();
  for (const b of delta.changed) changedMap.set(b.id, b);

  if (!delta.orderChanged) {
    // Fast path: same length, no removals — every base index still holds
    // the same id, so a straight positional map is exact and needs no id
    // lookup on the base side at all.
    return base.map((b) => changedMap.get(b.id) ?? b);
  }

  const baseMap = new Map<number, Building>();
  for (const b of base) baseMap.set(b.id, b);
  const order = delta.order ?? [];
  return order.map((id) => {
    const c = changedMap.get(id);
    if (c) return c;
    const orig = baseMap.get(id);
    // Defensive (GR#1): an id present in `order` (i.e. present in `next`
    // when this delta was built) that is neither `changed` nor found in
    // `base` would mean the delta was applied against the WRONG base —
    // a protocol invariant violation, not an expected runtime state. Never
    // silently fabricate a building; surface the corruption immediately so
    // it is caught at the worker.onerror/main-thread-fallback layer that
    // already exists for exactly this class of worker malfunction, rather
    // than shipping a corrupted city silently (GR#21).
    if (!orig) throw new Error(`simWorkerDelta: building id ${id} missing from both delta.changed and base — delta applied against the wrong base state`);
    return orig;
  });
}

/** Value-equality (not reference-equality) for `roadConnectivity` — it is
 *  UNCONDITIONALLY recomputed fresh every tick by advance() regardless of
 *  whether any building changed (engine.ts: `computeRoadConnectivity(s)`),
 *  so it never survives by reference even on a "nothing changed" tick. This
 *  is the second-largest field in the wire payload (4.2% of the capture-13
 *  measurement) — skipping it whenever its VALUE hasn't actually changed
 *  (the overwhelming majority of ticks, since the road/building layout
 *  that determines it usually hasn't moved) is why this module diffs it
 *  explicitly rather than always shipping it wholesale. */
export function roadConnectivityEqual(
  a: SimState['roadConnectivity'],
  b: SimState['roadConnectivity']
): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  if (a.connectedRoadTiles.length !== b.connectedRoadTiles.length) return false;
  for (let i = 0; i < a.connectedRoadTiles.length; i++) {
    if (a.connectedRoadTiles[i] !== b.connectedRoadTiles[i]) return false;
  }
  return true;
}

/**
 * FEAT-2326609777 round follow-up (opus-round-feat777, 2026-09-06):
 * `applyStateDelta` throws THIS when `delta.baseTick` doesn't match the
 * `base` state it is actually being applied to. Defence-in-depth against
 * the round's ATTACK FEAT-2326609777 finding "no integrity check on the
 * delta basis" — a delta applied against a same-shape-but-wrong-VALUE base
 * (the exact shape a lost/skipped reply, or any future regression that lets
 * the two sides' caches drift, would leave behind) previously corrupted
 * SILENTLY: no throw, no signal, the receiver just kept whatever stale
 * field values the wrong base happened to carry, forever. `baseTick` is the
 * cheapest available integrity stamp (already present on every SimState,
 * zero extra bytes worth caring about) — not a cryptographic guarantee (two
 * genuinely different states could in principle share a tick number after a
 * reset/hydrate), which is exactly why RESYNC_EVERY_TICKS below exists as a
 * second, independent bound on top of this check.
 */
export class DeltaBasisMismatchError extends Error {
  readonly expectedBaseTick: number;
  readonly actualBaseTick: number;
  constructor(expectedBaseTick: number, actualBaseTick: number) {
    super(
      `simWorkerDelta: delta basis mismatch — the delta was computed against tick ${expectedBaseTick}, but the state being patched is at tick ${actualBaseTick}`
    );
    this.name = 'DeltaBasisMismatchError';
    this.expectedBaseTick = expectedBaseTick;
    this.actualBaseTick = actualBaseTick;
  }
}

/**
 * FEAT-2326609777 round follow-up — the periodic-full-resync bound. Even
 * with the `baseTick` integrity check above, a full re-sync is forced every
 * this-many delta-mode requests regardless of whether anything has ever
 * looked wrong, so that ANY future regression which silently desyncs the
 * two sides' caches WITHOUT tripping the baseTick check (e.g. two distinct
 * states that happen to share a tick number, or a corruption class this
 * round did not anticipate) is bounded to at most this many ticks of drift,
 * never forever. 64 is a placeholder (per the round's own framing) — cheap
 * relative to the savings (one full clone every 64 ticks is still a >98%
 * reduction versus every tick) and short enough that a real desync would
 * show up in a single dogfood session rather than accumulating silently
 * for the life of it. See store.tsx's issueTickRequest for where this is
 * consumed.
 */
export const RESYNC_EVERY_TICKS = 64;

/** The change in a whole SimState between a base and a next snapshot. */
export interface SimStateDelta {
  /** The tick of the `base` state this delta was diffed FROM — the
   *  integrity stamp `applyStateDelta` checks against the state it is
   *  asked to patch (see DeltaBasisMismatchError's header). */
  baseTick: number;
  /** Every SimState field EXCEPT `buildings` and `roadConnectivity`, sent
   *  in full every time. Deliberately NOT diffed further: the capture-13
   *  measurement shows these fields combined are already only ~3.4% of the
   *  total payload (history/ledger/pipeTier/consolidatorLog/scalars), so
   *  diffing them too would add real complexity for a saving well under
   *  this feature's own <5%-of-full-clone bar — see FEAT-2326609777's BOW
   *  comment for the "cheapest SUFFICIENT fix" framing. */
  rest: Omit<SimState, 'buildings' | 'roadConnectivity'>;
  buildings: BuildingsDelta;
  /** Present only when the VALUE differs from the base (roadConnectivityEqual
   *  false) — omitted (undefined) means "unchanged, reuse the base's copy". */
  roadConnectivity?: SimState['roadConnectivity'];
}

/** Pure diff of two full SimStates. */
export function diffSimState(base: SimState, next: SimState): SimStateDelta {
  const { buildings: nextBuildings, roadConnectivity: nextRoadConnectivity, ...rest } = next;
  const buildings = diffBuildings(base.buildings, nextBuildings);
  const roadConnectivity = roadConnectivityEqual(base.roadConnectivity, nextRoadConnectivity)
    ? undefined
    : nextRoadConnectivity;
  return { baseTick: base.tick, rest: rest as Omit<SimState, 'buildings' | 'roadConnectivity'>, buildings, roadConnectivity };
}

/** Pure reconstruction: the exact inverse of diffSimState —
 *  `applyStateDelta(base, diffSimState(base, next))` produces a SimState
 *  deep-equal to `next`. This is the ONLY function either side of the
 *  thread boundary uses to turn a received delta back into a full SimState
 *  — no other reconstruction path exists (GR#21: one code path, shared by
 *  both store.tsx and simWorker.ts).
 *
 *  Throws DeltaBasisMismatchError (never silently corrupts — see its own
 *  header) if `base.tick` does not match the tick this delta was actually
 *  diffed against. Callers (store.tsx's worker.onmessage, simWorker.ts's
 *  onmessage) must catch this, record the registry-sourced MET-V891 error
 *  (GR#1/GR#7), discard whatever this round trip would have produced, and
 *  force a full resync on the NEXT request/reply — see each call site's own
 *  comment for the exact recovery sequence. */
export function applyStateDelta(base: SimState, delta: SimStateDelta): SimState {
  if (base.tick !== delta.baseTick) {
    throw new DeltaBasisMismatchError(delta.baseTick, base.tick);
  }
  const buildings = applyBuildingsDelta(base.buildings, delta.buildings);
  const roadConnectivity = delta.roadConnectivity ?? base.roadConnectivity;
  return { ...base, ...delta.rest, buildings, roadConnectivity } as SimState;
}
