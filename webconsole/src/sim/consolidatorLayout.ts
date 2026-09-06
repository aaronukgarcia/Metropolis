// consolidatorLayout.ts — FEAT-2326609779 (consolidator inc3): the LAYOUT
// HIERARCHY. Aaron: "when doing the consolidation its important to lay down
// the objects in a clean hierarchy... smooth rail and smooth road motorway
// first, then dual carriageways and then A roads, then minor roads then
// place the buildings... radius bend of rail and road and junctions are
// realistic." + "free space should be turned into parks or set aside for
// future growth."
//
// SCOPE DISCIPLINE (mirrors consolidator.ts's own file header): this module
// is the PURE PLANNER + GEOMETRY layer — it computes a deterministic
// SectionLayoutPlan (which tiles each tier WOULD occupy, cost estimates, and
// the bend/junction/severance validation flags) from a snapshot of free
// tiles + already-placed-this-pass tiles. It never mutates SimState, never
// books money, never touches s.buildings. The MUTATION half — actually
// creating Building rows, charging placementCost through the 'Consolidation'
// flow line, and the funds-gated per-tier atomicity/rollback (AC-1/AC-4/
// AC-9) — lives in engine.ts's applyConsolidatorPass, mirroring exactly how
// consolidator.ts (discovery) vs engine.ts (mutation) are already split for
// inc1/inc2. Kept as its own file (not folded into consolidator.ts) because
// the acceptance doc (FEAT-2326609779 §"Files") calls for a NEW file for the
// tier planner/geometry, and because consolidator.ts is already 1,300+ lines
// and contended by other lanes.
//
// GR#21 determinism: every function here is a pure fold over its arguments.
// No Math.random/Date.now/performance.now/localStorage. Every tile iteration
// walks an explicitly sorted array or a fixed-size nested loop over a
// bounding box — never a `for (const x of someMap) { ...; break; }` early
// exit over unordered iteration.
//
// GR#15 (validators derive from data): the AC-2 bend-radius minima and the
// AC-7 free-space heuristic constants are PLACEHOLDER-tier, gathered in one
// disclosed block below (§0), never scattered literals in the logic.

// ---------------------------------------------------------------------------
// §0 PLACEHOLDER DATA — Aaron's row-by-row balance pass pending (assumption 1
// of FEAT-2326609779 §6). These are the ACs' own stated numbers, gathered
// here as named constants (never re-typed as bare literals downstream) so a
// future balance pass touches ONE place.
// ---------------------------------------------------------------------------

export type TierKind = 'rail' | 'motorway' | 'dual' | 'aroad' | 'minor';

/** Aaron's stated hierarchy order, first-placed to last-placed (AC-1). 'buildings' is not a member of this array — it is the caller's OWN successor-placement step, which already exists (inc1/inc2); this module only sequences the FIVE infrastructure tiers that must land before it. */
export const TIER_ORDER: readonly TierKind[] = ['rail', 'motorway', 'dual', 'aroad', 'minor'];

/** AC-2: interior-angle minima in degrees, PLACEHOLDER-tier (Aaron's row-by-row pass pending — see FEAT-2326609779 §6 assumption 1). A larger interior angle is a GENTLER bend; the tier's minimum is the tightest bend that tier may take. 180 = dead straight, 0 = a full reversal. */
export const TIER_MIN_INTERIOR_ANGLE_DEG: Readonly<Record<TierKind, number>> = {
  rail: 67.5,
  motorway: 90,
  dual: 112.5,
  aroad: 112.5,
  minor: 45,
};

/** AC-3 angle rule: the minimum angle between a lower tier's junction entry and the higher tier's own direction. Stated once in the acceptance doc ("≥ 90 degrees, no acute merges") — reused here rather than re-typed. */
export const MIN_JUNCTION_ENTRY_ANGLE_DEG = 90;

/**
 * Deterministic rank, lower = higher in Aaron's hierarchy (rail first). Used
 * for: (a) placement ORDER (TIER_ORDER already encodes this, this is the
 * numeric form for comparisons), (b) AC-3's hierarchy rule ("a higher tier
 * may pass through a lower tier unimpeded; a lower tier terminates at or
 * forms a junction with a higher tier"), (c) AC-6's deterministic tile-spread
 * tie-break ("prefer the higher tier").
 */
export const TIER_RANK: Readonly<Record<TierKind, number>> = {
  rail: 0,
  motorway: 1,
  dual: 2,
  aroad: 3,
  minor: 4,
};

export function isHigherTier(a: TierKind, b: TierKind): boolean {
  return TIER_RANK[a] < TIER_RANK[b];
}

/**
 * The existing road/rail catalogue spec each tier inherits its cost/tile
 * from (FEAT-2326609779 §6 assumption 3 — "the consolidator does not invent
 * new tier costs"). rd_dual/rd_aroad graduated from placeholders in
 * FEAT-1972079907 inc1 (data.ts:1868-1870); 'road' (roadTier 1) is the
 * catalogue's own base/minor tier; 'm20' is the only motorway-kind spec
 * (data.ts:1448); 'rail' is the only rail-kind spec (data.ts:1449).
 */
export const TIER_SPEC_ID: Readonly<Record<TierKind, string>> = {
  rail: 'rail',
  motorway: 'm20',
  dual: 'rd_dual',
  aroad: 'rd_aroad',
  minor: 'road',
};

/**
 * PLACEHOLDER-tier (Aaron's balance pass pending): the shortest straight run
 * of free tiles this module considers "worth" laying as a tier segment. A
 * section with fewer contiguous free tiles than this for a given tier simply
 * has no candidate for that tier this pass — not a failure, an honest "no
 * space" (AC-1's `'tier failed: no space'` reason).
 */
export const MIN_TIER_RUN_TILES = 3;

/**
 * ROUND-13 REJECT FIX (P1, "paid infrastructure with permanent upkeep laid
 * OUTSIDE THE MAP" — measured 237 of 744 tier tiles on a 160-wide dogfood
 * fixture sitting at x >= 160, up to x = 435, i.e. inside the game's real
 * MAP_W=440/MAP_H=260 grid but far outside the CITY's own built footprint):
 * every real section-grid cell is technically inside the map, so the P1's
 * actual defect is that the layout stage's whole-map month (`monthlyScopeOf`'s
 * `full` sweep) considers EVERY section in the grid, including ones
 * containing no city at all, and happily lays a rail/motorway run into
 * empty wilderness the player never built toward. The fix (engine.ts's
 * `applyConsolidatorPass`) computes the city's own occupied bounding box
 * from `cur.buildings` once per pass and drops any section whose box does
 * not intersect that bounding box expanded by this margin — infrastructure
 * may only ever extend a MARGIN's worth of tiles past the city's own
 * existing edge, never leapfrog into untouched map. PLACEHOLDER-tier
 * (Aaron's balance pass pending): two full sections' worth of reach, so a
 * section immediately adjacent to the city's edge is still eligible (the
 * common, legitimate "grow the city outward one section at a time" case)
 * without opening the door to the wilderness-scatter this round measured.
 */
export const LAYOUT_WILDERNESS_MARGIN_TILES = 64;

/**
 * AC-7 heuristic (assumption 2 — Aaron may rule a different allocation
 * strategy): a free tile within `PARK_AMENITY_RADIUS_SECTIONS` sections of a
 * school AND a residential building is a park candidate; every other free
 * tile is growth reserve. Sections, not tiles, because that is the grain
 * consolidator.ts's own adjacency facts (AC-13 of FEAT-2326609761) already
 * compute — reused via the `nearAmenity` predicate callers pass in rather
 * than re-derived here (GR#3).
 */
export const PARK_AMENITY_RADIUS_SECTIONS = 1;

/**
 * F4 FIX (independent round finding, MEDIUM): AC-7's own worked example asks
 * for a SPATIAL split within a section, not a section-wide constant — a free
 * tile within this many TILES (Chebyshev distance) of a residential
 * building becomes a park candidate; every other free tile in the same
 * section is growth reserve. PLACEHOLDER-tier (Aaron's balance pass
 * pending, same disclosure as every other constant in this block).
 */
export const PARK_TILE_PROXIMITY = 3;

/**
 * F4 FIX: parks are actually PLACED (AC-7: "parks are placed... if budget
 * allows"), capped per section-pass so a single big section cannot mint
 * hundreds of £0-placementCost park buildings in one tick — mirrors
 * CONSOLIDATOR_MAX_TRANSACTIONS_PER_PASS's role of bounding a single pass's
 * blast radius. Placeholder-tier.
 */
export const MAX_PARKS_PLACED_PER_SECTION_PASS = 12;

/**
 * R3-A FIX (round-3 finding, CRITICAL — "the bankruptcy"): rail/motorway now
 * carry real build cost AND real recurring upkeep (FEAT-2326609782), and the
 * round's own measurement showed the layout stage alone could out-pace a
 * modest city's income several times over (Parks 2,210/tick + Roads
 * 2,198/tick against income 1,442/tick) — a real, quantified path to FINAL
 * DECLINE with no player action at all. The per-tier FUNDS gate
 * (INSOLVENCY_WARNING_THRESHOLD) only ever checks the ONE-TIME build spend;
 * it has no concept of the RECURRING upkeep a newly-laid tile keeps costing
 * every tick thereafter. This is the safety floor the new upkeep-aware gate
 * (engine.ts's applyTierLayoutForSection) checks against: a tier/park batch
 * may only commit if the city's projected net income per tick — AFTER
 * subtracting every tile's `upkeepChargeableOf` this pass has already
 * committed in this section, plus the candidate's own — stays ABOVE this
 * floor. PLACEHOLDER-tier (Aaron's balance pass pending): 0 is the simplest
 * defensible value ("never let an automatic background process push the
 * city's OWN run-rate net negative") — not a buffer against pre-existing
 * negative income from other causes, which this gate deliberately does not
 * try to fix.
 */
export const LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK = 0;

/**
 * R3-A FIX refinement (found while re-verifying against the round's OWN
 * test estate): a city whose baseline net income is ALREADY at/below the
 * floor for reasons unrelated to the consolidator (a population=0 fixture
 * with no tax base is the common real case, not just a test artifact) must
 * not have EVERY tier refused forever — that is punishing a pre-existing
 * condition the layout gate was never meant to fix (see engine.ts's own
 * comment at the gate). But bypassing the gate ENTIRELY on an unhealthy
 * baseline reopened the exact bankruptcy spiral R3-A exists to close (an
 * unbounded amount of NEW upkeep every pass, forever, once baseline dips
 * below the floor even once). This is the bound: when baseline is already
 * at/below the floor, a pass may still add upkeep, but never more than
 * this much PER TICK — capping the RATE of further degradation rather
 * than refusing to degrade at all. PLACEHOLDER-tier (Aaron's balance pass
 * pending).
 */
export const LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK = 2_000;

/**
 * BUG-684 FIX / round-6 F1b (P1, "the bound rebases on its own damage"): the
 * R3-A/round-4 gate recomputed `baselineNetIncomePerTick` fresh from
 * `cur.lastFlows` on EVERY PASS, so once the layout stage's own upkeep spend
 * pushed net income down once, the NEXT pass's "baseline" was that
 * already-damaged number — the floor ratchets downward without limit across
 * hundreds of passes (measured: -494 -> -15,282/tick, a documented bound of
 * 2,000 blown by 7.4x). The fix is a CUMULATIVE BUDGET, anchored ONCE rather
 * than a floor recomputed every pass:
 *
 *   - `SimState.consolidatorLayoutBaselineNetIncome` is set the FIRST time
 *     the layout stage ever runs (or lazily on load of an old save that
 *     predates this field) from that moment's `cur.lastFlows`, and is NEVER
 *     rewritten afterward by the layout stage's own spend — it is a fixed
 *     reference point, not a rolling recomputation.
 *   - `SimState.consolidatorLayoutCumulativeUpkeepDelta` is the LIFETIME
 *     total recurring upkeep the layout stage has committed since that
 *     anchor, across every pass, not reset per pass.
 *   - The per-pass gate CONSUMES this budget (`anchor - lifetimeDelta -
 *     thisPassRunningDelta - candidateTierDelta` compared against
 *     `layoutUpkeepEffectiveFloorOf(anchor)`) instead of recomputing the
 *     baseline from the current (possibly already-degraded) `cur.lastFlows`.
 *     Because the anchor never moves, the WORST the layout stage can ever
 *     do to net income, across its entire lifetime against that anchor, is
 *     bounded by LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK — a single, one-time
 *     bound, not one that compounds pass after pass.
 *
 * Save-compat: `null`/absent baseline (every save predating this fix) means
 * "not yet anchored" — engine.ts anchors it lazily, exactly like a brand new
 * city, the first time the layout stage runs after load. A missing
 * cumulative delta reads as 0 (nothing spent yet against the new anchor).
 *
 * ROUND-8 R8-F1 CORRECTION (P2, "the documented 2,000/tick bound is false on
 * the profitable branch"): the paragraph above describes the UNPROFITABLE
 * branch (anchor <= LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK) correctly — that
 * branch's lifetime allowance really is exactly LAYOUT_UPKEEP_MAX_WORSENING_
 * PER_TICK (2,000), independent of the anchor's own value (round-8's own
 * R8-1b measures this precisely: a -500 "trough" anchor and a 0 "flat"
 * anchor both permit exactly 2,000 of lifetime degradation). The PROFITABLE
 * branch (anchor > 0) does NOT get the same 2,000 bound — its floor is the
 * flat `LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK` (0), so the lifetime allowance
 * on that branch is `anchor - 0 = anchor` itself. A city anchored at a
 * genuine 250,000/tick income spike therefore locks in a 250,000 lifetime
 * upkeep budget forever, not 2,000 — 125x the documented figure. This is a
 * DECIDED, DOCUMENTED design (not a bug being left open): a profitable
 * city's surplus is real, earned income, and letting the layout stage draw
 * down PROPORTIONALLY MORE of a healthy city's larger surplus (while a
 * struggling city's allowance stays pinned to the small, protective 2,000
 * figure) is the intended shape, not an oversight — R8-1b pins both halves
 * of this as the estate's OWN regression contract. The disclosed residual
 * risk (recorded, not eliminated): the FIRST layout pass after enabling the
 * stage anchors off whatever `lastFlows` says at that instant, so a player
 * (or a coincidental in-game event) that enables the layout stage during a
 * transient income spike locks in that spike's inflated allowance
 * PERMANENTLY, even after the spike passes and net income reverts to
 * normal — the anchor never re-checks itself downward. This is the same
 * "anchor once, never rebase" property that CLOSES the F1b ratchet
 * (rebasing on every pass is exactly the round-6 defect), so it cannot be
 * fixed by rebasing more often without reopening F1b — it is an accepted
 * trade-off of the anchor-once design, flagged here for a future balance
 * pass (e.g. an anchor computed from a SMOOTHED/multi-tick average rather
 * than a single instant, which would reduce but not eliminate the
 * gameable window) rather than fixed unilaterally in this round.
 */
/**
 * ROUND-12 LEAD RULING (Aaron via the round-11 coordinator, dated this
 * session — placeholder tier, Aaron's balance pass will retune row-by-row):
 * a FLAT lifetime allowance (LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK, 2,000) is
 * "wrong in kind, not just size" — round 11 measured that one real rail
 * placement alone consumes ~all of it, so no per-tier percentage split of a
 * FIXED, never-growing pool could ever sustain multi-pass infrastructure
 * growth as the city itself grows. `allowancePerTick` is now an explicit
 * PARAMETER (not read from the module constant internally) so the caller
 * (engine.ts's applyConsolidatorPass) can derive it per-pass from the
 * city's CURRENT scale — see LAYOUT_UPKEEP_SHARE_OF_INCOME's own doc for the
 * derivation. The default value keeps every pre-round-12 unit test (which
 * calls this with one argument to test the FORMULA in isolation) exercising
 * the exact old flat-2,000 shape unchanged; `LAYOUT_UPKEEP_MAX_WORSENING_
 * PER_TICK` itself is kept exactly as "the old constant, now used only as a
 * hard absolute-minimum floor" — engine.ts takes `Math.max` of it against
 * the new income-derived and rail-run-derived terms, so a very small/new
 * city with near-zero tax income still gets AT LEAST the old bound, never
 * less.
 *
 * ROUND-14 LEAD RULING (opus-round11-inc3 REJECT F2, P1, measured on a
 * 2,292-building dogfood city): the R8-F1 branch SHAPE above discarded
 * `allowancePerTick` ENTIRELY on the profitable branch (anchor > 0),
 * returning the flat `LAYOUT_UPKEEP_SAFETY_FLOOR_PER_TICK` (0) regardless —
 * so round 12/13's whole income-scaled-allowance mechanism was NEVER
 * REACHED on any profitable city (measured: lifetime delta plateaus at
 * 1,997 ~= the OLD flat 2,000 constant on a 1bn/200k-population dogfood
 * city, with a 2,000x CLIFF between an anchor of 0 and an anchor of +1).
 * FIXED: the branch is REMOVED — every anchor (profitable or not) now
 * consumes `allowancePerTick` via the SAME continuous formula, so the
 * effective floor is `anchor - allowancePerTick` unconditionally. This is
 * a DELIBERATE, ordered supersession of round 8's R8-F1 asymmetry (that
 * ruling's own R8-1b pin — "profitable-branch allowance == the anchor
 * itself" — is retuned in the same round to assert the NEW continuous
 * shape instead, per this ruling's explicit "no cliff" requirement).
 */
export function layoutUpkeepEffectiveFloorOf(
  anchorNetIncomePerTick: number,
  allowancePerTick: number = LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK,
): number {
  return anchorNetIncomePerTick - allowancePerTick;
}

/**
 * ROUND-12 LEAD RULING (see layoutUpkeepEffectiveFloorOf's own doc for the
 * full context): the per-pass upkeep-worsening allowance now SCALES with
 * the city, rather than being a flat constant. engine.ts computes, each
 * pass:
 *
 *   allowance = max(
 *     LAYOUT_UPKEEP_SHARE_OF_INCOME * currentMonthlyTaxIncomePerTick,
 *     oneFullRailRunUpkeep,               // real rail spec upkeep x a
 *                                          // full-run tile count — so the
 *                                          // top tier can ALWAYS place at
 *                                          // least once when capex allows,
 *                                          // never structurally starved by
 *                                          // a tiny income figure alone
 *     LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK, // the old constant, now only
 *                                            // an absolute-minimum backstop
 *   )
 *
 * `currentMonthlyTaxIncomePerTick` reads the SAME tax-inflow labels
 * (`Council Tax`/`Business Tax`/`Freight Tax`) engine.ts's own Transit
 * Subsidy cap already uses (`baseTaxIncome`, engine.ts ~line 839) — GR#3
 * SSOT, not a new income definition. This is CURRENT income, read fresh
 * every pass (unlike the anchor, which stays fixed forever per round-6
 * F1b) — deliberately, per Aaron's stated intent ("the defrag budget must
 * scale with the city"): a growing city's allowance grows with it, rather
 * than staying pinned to whatever the city looked like on the pass the
 * layout stage first ever ran. PLACEHOLDER-tier (0.10 — Aaron's balance
 * pass will retune row-by-row, same as every other constant in this file).
 */
export const LAYOUT_UPKEEP_SHARE_OF_INCOME = 0.1;

/**
 * ROUND-12/14 REJECT (opus-round12-inc3, P1-A, dated 2026-09-05 — "a bound
 * that rebases on its own damage is not a bound", the estate's OWN R6-F1b
 * pin, recurring): the round-14 fix above made `layoutUpkeepAllowanceThisPass`
 * a genuine per-pass FLOOR that scales with current income (never starved,
 * never blocked by a stale anchor) — but engine.ts then PERSISTED
 * `consolidatorLayoutCumulativeUpkeepDelta` as an OVERWRITE of that single
 * pass's own delta, not an accumulation across passes. Renaming the field
 * "cumulative"/"lifetime" in every doc comment never made it so: the round's
 * own measurement (a 1bn-treasury dogfood city) showed lifetime added upkeep
 * climbing LINEARLY without limit — 7,011 / 13,558 / 19,498 / 24,851 /
 * 29,828 / 33,792 at 5/10/15/20/25/30 passes, ~1,300/pass forever, because
 * every pass got a FULL fresh allowance with nothing ever charged against a
 * running total. A liveness fix needs BOTH bounds (project lesson,
 * 2026-09-02: a fixed-freeze regression shipped a 20x clock runaway because
 * a floor existed with no matching ceiling) — the per-pass floor above
 * prevents STARVATION; this is the missing CEILING that prevents the
 * defrag stage from being an unbounded, ever-growing drain on the city's
 * income across its entire lifetime.
 *
 * LEAD RULING (Aaron, via the round-12 coordinator): total layout-added
 * upkeep — `SimState.consolidatorLayoutCumulativeUpkeepDelta`, now made
 * GENUINELY cumulative again by engine.ts (`+=` this pass's own delta at
 * finalize, `-=` on Undo, exactly matching the doc comment that has
 * described this field as "lifetime" since round 6) — may never exceed
 * `LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME` of the city's CURRENT monthly
 * tax income (the SAME `currentTaxIncomePerTick` reading
 * `LAYOUT_UPKEEP_SHARE_OF_INCOME` already uses, GR#3 SSOT — read fresh every
 * pass, so the ceiling itself GROWS as the city's own income grows, never a
 * fixed pool). engine.ts computes the remaining headroom
 * (`ceiling - priorCumulativeDelta`, floored at 0) ONCE per pass, BEFORE the
 * per-pass allowance formula above runs, and trims
 * `layoutUpkeepAllowanceThisPass` down to that headroom via `Math.min` — a
 * pass whose raw (income/rail-run/flat-floor) allowance would breach the
 * lifetime ceiling is silently capped to whatever room is left, reporting
 * 'layout paused: lifetime upkeep ceiling' once (GR#17) rather than being
 * refused outright. This makes the defrag stage a genuinely FINITE
 * investment relative to the city's OWN size at every point in time — it
 * can never again be an unbounded drain, but it is never permanently
 * starved either, because the ceiling itself rises as the city (and its tax
 * base) grows. PLACEHOLDER-tier (0.5 — Aaron's balance pass pending, same
 * disclosure as every other constant in this file): half the city's current
 * monthly tax income is the most the layout stage's own historical spend
 * may ever cost it, in perpetuity.
 */
export const LAYOUT_LIFETIME_UPKEEP_SHARE_OF_INCOME = 0.5;

/**
 * BUG-684 FIX (F1, "76.2M of capex spent on tick 1 alone"): the per-tier
 * funds gate in engine.ts's applyTierLayoutForSection only ever checked
 * one-time build spend against the bare insolvency floor
 * (INSOLVENCY_WARNING_THRESHOLD) — nothing stopped a single pass from
 * spending the WHOLE gap between current funds and that floor in one tick,
 * which on a large fragmented city with many sections to lay out is exactly
 * what round 6 measured (a 100,000,000-pound treasury losing 76,200,000 on
 * tick 1 alone, defaulting layout ON). Two independent, additive caps close
 * this, both PLACEHOLDER-tier (Aaron's balance pass pending):
 *
 *  (a) a RESERVE MARGIN above the bare insolvency floor — see
 *      LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP / LAYOUT_CAPEX_RESERVE_FRACTION_
 *      OF_FUNDS below for the round-8 F2 TREASURY-SCALED redesign.
 *  (b) a hard per-tick CEILING on the layout stage's own one-time capex
 *      spend — see LAYOUT_CAPEX_MAX_PER_TICK / LAYOUT_CAPEX_MAX_FRACTION_
 *      PER_TICK below, likewise round-8 F2 treasury-scaled.
 */

/**
 * BUG-684 FIX (F1) months-of-upkeep TERM of the capex reserve — one of TWO
 * terms combined by `Math.max` (engine.ts's `layoutCapexReserve`), the other
 * being LAYOUT_CAPEX_RESERVE_FRACTION_OF_FUNDS below.
 *
 * ROUND-8 R8-F2 FIX (P1 REJECT — "the reserve and ceiling are flat, not
 * scale-aware"): round 7's version of this constant was 3 (months), which
 * on the estate's own fireFixture (upkeep 521/tick) sizes a reserve of only
 * 46,890 — against INSOLVENCY_WARNING_THRESHOLD (-750,000) that leaves a
 * capex FUNDS FLOOR of -703,110: NEGATIVE. A negative floor means the
 * months-of-upkeep term alone protects nothing — a solvent small city can be
 * spent straight into overdraft before this term ever binds (round 8's own
 * R8-3f/R8-3g measurement: a 30,000,000 city driven from solvent to -1.05M
 * by tick 200, layout-attributable). Upkeep on a small, low-population city
 * is a poor proxy for "how much treasury this city can safely risk" — it
 * measures the BUILDING STOCK'S running cost, not the TREASURY'S size, and
 * the round-6/7 fix never anchored the reserve to the thing it is actually
 * protecting (the city's own funds). Raised so the months-term ALONE (the
 * exact quantity round 8's R8-3f test recomputes independently, with no
 * knowledge of the fraction-of-funds term below) already clears the floor
 * at the estate's own measured 521/tick upkeep: 60 months x 30 ticks/month x
 * 521/tick = 937,800, floor = -750,000 + 937,800 = +187,800 (positive, with
 * headroom against exact-upkeep drift). PLACEHOLDER-tier (Aaron's balance
 * pass pending) — the number is disclosed as generous-by-design, not tuned
 * for realism; the FRACTION term below is what actually scales the reserve
 * with treasury size for cities where upkeep is a bigger absolute number.
 */
export const LAYOUT_CAPEX_RESERVE_MONTHS_UPKEEP = 60;

/**
 * BUG-684 FIX (F1) TREASURY-FRACTION term of the capex reserve — the round-8
 * F2 fix's actual scaling mechanism. `layoutCapexReserve` (engine.ts) is
 * `Math.max(monthsOfUpkeepTerm, thisFraction * cur.funds)`, so on a city
 * whose upkeep-based term is small relative to its OWN treasury (any city
 * past a hamlet), THIS term is what protects a floor that scales with the
 * money actually at risk — round 6/7's own missing piece: the swing between
 * layout ON and OFF was measured FLAT at 33.1M whether the treasury was
 * 100,000,000 or 1,000,000,000,000 ("scale-blind"), because neither prior
 * term (a bare constant ceiling, an upkeep-sized reserve) ever looked at
 * `cur.funds` at all. PLACEHOLDER-tier (Aaron's balance pass pending): 10%
 * reserved means the layout stage can never spend a city below 90% of its
 * OWN current treasury (before the bare floor even applies) — measured
 * sufficient, combined with LAYOUT_CAPEX_MAX_FRACTION_PER_TICK below, to
 * keep 5,000,000 / 30,000,000 / 100,000,000 / 1,000,000,000 treasuries all
 * solvent over 150-200 ticks with layout ON (round-8 R8-3's own four-row
 * A/B).
 */
export const LAYOUT_CAPEX_RESERVE_FRACTION_OF_FUNDS = 0.1;

/**
 * BUG-684 FIX (F1): hard ceiling on the layout stage's own one-time capex
 * spend (tier build cost + park placementCost, summed across every section
 * the pass commits) in a SINGLE tick/pass — the ABSOLUTE-VALUE term of TWO
 * combined by `Math.min` (engine.ts's `layoutCapexCeiling` is
 * `Math.min(this, LAYOUT_CAPEX_MAX_FRACTION_PER_TICK * cur.funds)`), a
 * backstop that never grows past this number regardless of how large the
 * treasury-fraction term below computes on an enormous city.
 *
 * ROUND-9 R9-F1 DOC CORRECTION ("the doc comment claims a run the generator
 * never emits"): this paragraph used to claim the number "stays high enough
 * that a real pass can still afford at least one MIN_TIER_RUN_TILES-length
 * run of the catalogue's priciest tier" — FALSE against the actual code:
 * `candidateTierPath` always returns the LONGEST free run in a section
 * (never a minimum-length one — see that function's own doc), so on a real,
 * mostly-open section the RAW motorway candidate is routinely 15-19 tiles
 * (22,500,000-28,500,000 at m20's 1,500,000/tile) — always over this
 * ceiling, at ANY treasury, because the ceiling's OTHER term is a
 * `Math.min`, never a `Math.max`. Round 9 measured this directly: motorway
 * placed ZERO times at both GBP 1,000,000,000 and GBP 1,000,000,000,000.
 *
 * What actually keeps a real pass solvent-but-functional today is NOT this
 * constant being "high enough" for the untrimmed candidate — it is that
 * engine.ts's applyTierLayoutForSection TRIMS an over-budget candidate to
 * the largest prefix that fits the remaining per-tick budget (never below
 * MIN_TIER_RUN_TILES, which still fails cleanly with 'tier failed: capex
 * budget' if even the minimum does not fit) rather than refusing the whole
 * tier outright. This constant's real job is narrower than the old
 * paragraph claimed: it is the hard backstop on how much the TRIMMED
 * candidate may ever cost, at any treasury — 20,000,000 is PLACEHOLDER-tier
 * (Aaron's balance pass pending), chosen so a trimmed MIN_TIER_RUN_TILES
 * run of even the priciest catalogue tier (m20, 3 tiles = 4,500,000) always
 * clears it with headroom, while still meaningfully bounding a single
 * pass's total spend regardless of treasury size (the round-6 F1 defect
 * this constant was introduced to close).
 */
export const LAYOUT_CAPEX_MAX_PER_TICK = 20_000_000;

/**
 * BUG-684 FIX (F1) TREASURY-FRACTION term of the capex ceiling — the round-8
 * F2 fix's actual scaling mechanism, paired with LAYOUT_CAPEX_MAX_PER_TICK
 * above via `Math.min`. Round 6/7's flat 20,000,000/tick ceiling was 65% of
 * an entire 30,000,000 treasury in ONE tick (round 8's own R8-F2 finding) —
 * an absolute number cannot simultaneously be "enough headroom for a real
 * pass on a small city" and "a small enough bite that a small city stays
 * safe", because those are opposite requirements once city size varies.
 * This term makes the per-tick spend a FRACTION of the CURRENT treasury
 * instead, so it shrinks automatically as the treasury shrinks (and as the
 * treasury itself shrinks pass-over-pass from any spend, further bounding
 * cumulative drawdown to a decaying geometric series, never a cliff).
 * PLACEHOLDER-tier (Aaron's balance pass pending): 2% per tick, combined
 * with the 10% reserve above, is measured sufficient to keep every treasury
 * scale in round 8's own R8-3 four-row A/B (5M/30M/100M/1bn) solvent over
 * 150-200 ticks, while still letting the ON-vs-OFF swing grow with treasury
 * size (a bigger city can safely absorb a bigger absolute spend) rather
 * than staying flat regardless of scale.
 */
export const LAYOUT_CAPEX_MAX_FRACTION_PER_TICK = 0.02;

/**
 * ROUND-11 LEAD RULING (Aaron, via the round-10 coordinator, "road lays
 * out, train layout, then the bigger consolidated buildings" — R10-F2 was
 * REJECTED as not-deferrable): round 10 measured motorway/rail placing once
 * ever and never growing while dual/aroad/minor kept accumulating tiles
 * pass after pass, at every treasury scale — the inverse of Aaron's stated
 * hierarchy. Root cause: the scarce LIFETIME upkeep allowance
 * (LAYOUT_UPKEEP_MAX_WORSENING_PER_TICK, a single shared 2,000 pool that
 * never resets) was being spent by whichever tier's candidate happened to
 * be evaluated first SECTION-by-section — since every section ran all five
 * tiers back-to-back before the NEXT section's rail even got a look, a
 * later section's cheap minor/dual tiles could exhaust the pool ahead of
 * an earlier-in-TIER_ORDER but later-in-SECTION-ORDER rail/motorway
 * candidate. The ruling: split the pool by a NAMED per-tier share so lower
 * tiers cannot consume it before the top tiers have had first claim across
 * EVERY section this pass (engine.ts's applyConsolidatorPass now walks
 * TIER_ORDER as the OUTER loop, sections as the inner loop — see that
 * file's Phase A/B/C split). A tier's unused share rolls DOWN to the next
 * tier in TIER_ORDER within the SAME pass (never back up to a tier already
 * processed) — engine.ts implements the roll-down as a running
 * `Record<TierKind, number>` reset to 0 for the just-processed tier once
 * its leftover is added to the next tier's pool.
 *
 * PLACEHOLDER-tier (Aaron's own words: "placeholders, Aaron balance") —
 * these five fractions are a first cut, not a tuned economy, and sum to
 * 1.0 by construction (rail+motorway getting the majority — 0.60 combined —
 * of whatever lifetime headroom remains, matching "smooth rail and smooth
 * road motorway first").
 */
export const TIER_UPKEEP_SHARE: Readonly<Record<TierKind, number>> = {
  rail: 0.3,
  motorway: 0.3,
  dual: 0.2,
  aroad: 0.12,
  minor: 0.08,
};

/**
 * ROUND-11 FIX (R10-F2's OTHER half, "runs do not grow across passes"):
 * without this, every pass's `candidateTierPath` call generates a totally
 * FRESH, independent run seeded off (sectionKey, tick) — even when a
 * previous pass already laid a same-tier stub in this exact section, a new
 * pass has no preference for CONTINUING that stub over starting a brand
 * new, disconnected one elsewhere in the section's free space. Measured
 * effect (round 10): motorway placed once at MIN_TIER_RUN_TILES (3 tiles)
 * and never grew across 300 ticks / ~10 passes.
 *
 * This is tried FIRST (engine.ts's buildLayoutSectionCtx), before falling
 * back to `candidateTierPath`: given the tier's own EXISTING tiles already
 * standing in this section (pre-this-pass, from `cur.buildings` — genesis
 * tiles excluded by the caller, matching every other "this pass's own
 * infra" convention in this stage), walk outward from each existing tile in
 * all four orthogonal directions through `available` (free, unclaimed)
 * tiles until hitting the section edge or a non-free tile, and return
 * whichever such walk is longest. Deterministic (GR#21): `existingTier`'s
 * keys are iterated in a fixed lexicographic sort, and the four directions
 * are tried in a fixed order every time — no seed/RNG needed since there is
 * no tie-break ambiguity a hidden ordering could expose (the longest walk
 * wins outright; a genuine tie keeps whichever was found first in the fixed
 * iteration order, which is itself deterministic).
 *
 * Returns `[]` (never below MIN_TIER_RUN_TILES) when no existing tile has a
 * long-enough free run to extend into — the caller then falls back to a
 * fresh `candidateTierPath` exactly as before this fix existed.
 */
/**
 * DEAD-END STUB FIX (FEAT-2326609779 inc4 verifier pass, 2026-09-06) —
 * `homeBox` gates the BOOTSTRAP anchor case only (engine.ts's fresh-stub
 * call at buildLayoutSectionCtx, which anchors on ANY existing network tile
 * within the wide `extBox` margin, not just this tier's own tiles). Without
 * it, a section could bootstrap off a network tile sitting deep in its
 * LAYOUT_EXTENSION_SEARCH_MARGIN_TILES margin — inside a NEIGHBOURING
 * section's box that has already converged and released — and lay a whole
 * MIN_TIER_RUN_TILES+ run entirely inside that neighbour, which never
 * revisits it: a permanent dead-end stub (measured: box 1,0's (10,6)/(10,5)
 * rail and box 63,0's (77,3) rail, both minted by a neighbouring section's
 * bootstrap anchored deep in its margin). `homeBox` is deliberately NOT
 * threaded into the TRUE-EXTENSION call (engine.ts's other call, continuing
 * a tier's OWN existing run) — that call legitimately walks OUT of its
 * anchor's box to join two already-separate networks across a section
 * boundary (BUG-754 task requirement 2 / round 13), and gating it the same
 * way regressed that behaviour when tried as a blanket fix (see this
 * function's git history / the BOW comment trail on FEAT-2326609779).
 *
 * When `homeBox` is supplied, a candidate anchor/direction walk is only
 * accepted when EITHER the anchor tile itself sits inside `homeBox` (a
 * legitimate "this section already has some of the general network, extend
 * from it") OR at least `MIN_TIER_RUN_TILES` of the walked run's tiles land
 * inside `homeBox` (the run is still substantially THIS section's own,
 * merely poking a short tail into the margin) — anything else is rejected
 * outright rather than being allowed to win `best`. The same gate is
 * re-applied to the final (possibly bend-extended) path, since the
 * right-angle/chamfer bend helpers walk `available` with no box awareness of
 * their own and could otherwise carry an accepted candidate back out of
 * `homeBox` again.
 */
/**
 * DEAD-END-SPUR FIX (FEAT-2326609779 inc4 verifier pass, 2026-09-06) —
 * "a rail or motorway run may only be committed when BOTH ends terminate at
 * a same-tier tile, a station/port tile, or the city-wide lattice line; a
 * perpendicular spur off a line that ends in open ground is never laid."
 * Passed by the caller (engine.ts) ONLY for `tier` 'rail'/'motorway' — every
 * other tier (dual/aroad/minor) keeps the pre-existing, unconstrained walk
 * (minor's cul-de-sacs are explicitly allowed). `isValidTerminus` is a
 * caller-supplied predicate (station tiles + `onCityLattice`) so this module
 * never needs to import spec/lattice data itself (avoiding a
 * consolidatorReplan.ts <-> consolidatorLayout.ts import cycle, since
 * consolidatorReplan.ts already imports FROM this file).
 */
export interface RunTerminusRule {
  readonly tier: TierKind;
  readonly isValidTerminus: (x: number, y: number) => boolean;
  /**
   * BOOTSTRAP + LINE-GROWTH EXEMPTION (FEAT-2326609779 inc4 adjudicator pass,
   * 2026-09-06 — the six-red adjudication on this item's BOW thread).
   *
   * The terminus rule as first written was a CONJUNCTION with no escape: a
   * rail/motorway run is committed only when its far end is orthogonally
   * adjacent to an already-standing same-tier tile or a station/port tile.
   * Measured on the inc3 fixtures (probe, `fireFixture` at £10m, section
   * 16,0): dual and aroad walk 31- and 34-tile runs out of the very same
   * anchor set with the very same free-tile board, while rail and motorway
   * return ZERO — because a run that walks OUT of the network into open
   * wilderness can never, by construction, have its far end touch anything.
   * With no rail and no station anywhere in the city, that makes a FIRST
   * rail/motorway run structurally impossible, and on the dogfood fixture it
   * froze rail at its pass-1 count (8 -> 8 over six passes) forever: not a
   * dead-end-stub fix, a total starvation of the tier. Aaron's inc4 ruling
   * has rail and motorway as CITY-WIDE LINES; a rule that forbids a line
   * from ever being started or ever being lengthened cannot serve it.
   *
   * Two exemptions restore line growth while keeping the defect the rule was
   * written for (a PERPENDICULAR spur hanging off a line into open ground)
   * closed:
   *
   *  (a) `bootstrap` — set by the caller when the city has NO tile of this
   *      tier ANYWHERE. The first line of a tier has nothing of its own to
   *      join by definition; `MIN_TIER_RUN_TILES`, the `homeBox` gate and
   *      the connectivity anchor still apply, so it is still a real,
   *      network-anchored line and not a scattered stub.
   *
   *  (b) COLLINEAR CONTINUATION (computed inside `extendExistingRun`, needs
   *      no caller input) — the walk's anchor tile has a same-tier tile
   *      directly BEHIND it along the walk direction, i.e. the anchor is the
   *      END of an existing line running the same way and the run makes that
   *      line LONGER. That is the growth case. A perpendicular spur off the
   *      middle (or the end) of a line fails it, because the anchor's line
   *      runs across the walk direction, not along it — which is exactly the
   *      geometry root-caused on this thread ((10,5)/(10,6) hanging south off
   *      the horizontal rail line in box 1,0).
   */
  readonly bootstrap?: boolean;
}

export function extendExistingRun(
  existingTier: ReadonlySet<string>,
  available: ReadonlySet<string>,
  box: { x0: number; y0: number; w: number; h: number },
  seed = 0,
  homeBox?: { x0: number; y0: number; w: number; h: number },
  terminusRule?: RunTerminusRule,
): TileXY[] {
  if (existingTier.size === 0) return [];
  // DEAD-END-SPUR FIX: only rail/motorway are gated (see doc comment above).
  const requireTerminus =
    terminusRule && (terminusRule.tier === 'rail' || terminusRule.tier === 'motorway') && !terminusRule.bootstrap;
  // BOOTSTRAP + LINE-GROWTH EXEMPTION (b): the anchor is the END of an
  // existing same-tier line pointing the same way as the walk, so the run
  // LENGTHENS that line rather than hanging a perpendicular spur off it. See
  // `RunTerminusRule`'s doc comment for the measurement this restores.
  const isCollinearContinuation = (anchorX: number, anchorY: number, dx: number, dy: number): boolean =>
    existingTier.has(`${anchorX - dx},${anchorY - dy}`);
  const hasValidTerminus = (path: TileXY[]): boolean => {
    if (!requireTerminus) return true;
    if (path.length === 0) return false;
    const inPath = new Set(path.map((p) => `${p.x},${p.y}`));
    const last = path[path.length - 1];
    return [
      [1, 0],
      [-1, 0],
      [0, 1],
      [0, -1],
    ].some(([dx, dy]) => {
      const nx = last.x + dx;
      const ny = last.y + dy;
      const k = `${nx},${ny}`;
      if (inPath.has(k)) return false; // the path's own previous tile, not a real terminus
      if (existingTier.has(k)) return true; // joins another already-standing same-tier tile
      return !!terminusRule && terminusRule.isValidTerminus(nx, ny); // station/port or city-wide lattice line
    });
  };
  const inHomeBox = (x: number, y: number): boolean =>
    !homeBox || (x >= homeBox.x0 && x < homeBox.x0 + homeBox.w && y >= homeBox.y0 && y < homeBox.y0 + homeBox.h);
  const homeBoxTileCount = (path: TileXY[]): number => path.reduce((n, p) => n + (inHomeBox(p.x, p.y) ? 1 : 0), 0);
  const satisfiesHomeBox = (anchorX: number, anchorY: number, path: TileXY[]): boolean =>
    !homeBox || inHomeBox(anchorX, anchorY) || homeBoxTileCount(path) >= MIN_TIER_RUN_TILES;
  const DIRS: Array<[number, number]> = [
    [1, 0],
    [-1, 0],
    [0, 1],
    [0, -1],
  ];
  // RAIL-CLUMP FIX (inc4 round-17 follow-up (b)) — A LINE IS ONE TILE WIDE.
  // Walking from EVERY existing tile of the tier in every direction, across
  // repeated passes, could pick a straight run that lands directly parallel
  // and adjacent to another already-standing same-tier line: pass N extends
  // rail down column x=10 from the tier's horizontal line at y=0, and pass
  // N+1 — now that (10,1..6) is itself part of `existingTier` — walks down
  // from the NEXT horizontal-line tile at (11,0), producing a second column
  // immediately beside the first (measured: box 1,0's rail rendered as a
  // 2-wide AREA at x=10-11, not a line — E8's own-tier component/crossing
  // counts and E2's dead-end assertion both catch this). A tile whose
  // PERPENDICULAR neighbour (relative to the walk direction) is already an
  // existing tile of this same tier is adjacent-parallel to a standing line
  // and is never added to the run — the walk simply stops one tile short,
  // exactly as it already does at the box edge or a non-free tile.
  //
  // FIX (FEAT-2326609779 inc4 verifier pass, 2026-09-06) — a CROSSING is not
  // a CLUMP. The check above alone flags any perpendicular neighbour that
  // merely belongs to `existingTier`, including a single tile that is itself
  // part of a run going the OTHER way (e.g. a short horizontal minor spur off
  // a residential building sitting one tile beside a north-south walk). That
  // is a legitimate grade crossing, not a parallel line, but the naive check
  // stopped the walk dead there anyway — measured as literal HOLES punched in
  // an otherwise-straight vertical run at every row a perpendicular stub
  // crossed it (box 1,0's minor column at x=8 broke at y=4 and y=12, the exact
  // rows the residential access spurs cross it), and the truncated stub ends
  // either side of the hole are real dead ends (inc4-E2/E8 caught this as a
  // regression from the clump fix, not fixture drift). A perpendicular
  // neighbour only counts as "parallel" when IT ALSO continues in the WALK's
  // own direction — i.e. it has a same-tier tile one step further along
  // (dx,dy) or back (-dx,-dy) from itself, proving it is part of a line
  // running alongside the walk, not a lone crossing tile running across it.
  const perpOf = (dx: number, dy: number): Array<[number, number]> => [
    [dy, dx],
    [-dy, -dx],
  ];
  const adjacentParallel = (x: number, y: number, dx: number, dy: number): boolean =>
    perpOf(dx, dy).some(([px, py]) => {
      const nx = x + px;
      const ny = y + py;
      if (!existingTier.has(`${nx},${ny}`)) return false;
      return existingTier.has(`${nx + dx},${ny + dy}`) || existingTier.has(`${nx - dx},${ny - dy}`);
    });
  let best: TileXY[] = [];
  let bestAnchor = { x: 0, y: 0 };
  let bestCollinear = false;
  for (const key of Array.from(existingTier).sort()) {
    const [xs, ys] = key.split(',');
    const ex = { x: Number(xs), y: Number(ys) };
    for (const [dx, dy] of DIRS) {
      const run: TileXY[] = [];
      let cx = ex.x + dx;
      let cy = ex.y + dy;
      while (
        cx >= box.x0 &&
        cx < box.x0 + box.w &&
        cy >= box.y0 &&
        cy < box.y0 + box.h &&
        available.has(`${cx},${cy}`) &&
        !adjacentParallel(cx, cy, dx, dy)
      ) {
        run.push({ x: cx, y: cy });
        cx += dx;
        cy += dy;
      }
      // DEAD-END STUB/SPUR FIX: a candidate that fails the homeBox gate or
      // (for rail/motorway) the terminus rule is never allowed to win `best`
      // — see this function's doc comments above.
      const collinear = isCollinearContinuation(ex.x, ex.y, dx, dy);
      if (run.length > best.length && satisfiesHomeBox(ex.x, ex.y, run) && (collinear || hasValidTerminus(run))) {
        best = run;
        bestAnchor = ex;
        bestCollinear = collinear;
      }
    }
  }
  if (best.length === 0) return [];
  // BUG-754 FIX (task requirement 2, "allow the extension to... turn once"):
  // the straight walk above is deliberately unchanged (M1's mutant pin
  // above depends on the entry guard staying exactly as written) — this is
  // an ADDITIVE attempt at one bend past the straight walk's own far end,
  // reusing the SAME turn geometry `candidateTierPath` already validates
  // elsewhere in this file (GR#3 SSOT: one bend-extension implementation,
  // not two). `box` here may be the CALLER's own section box, OR a wider
  // search box spanning into neighbouring sections (engine.ts's
  // LAYOUT_EXTENSION_SEARCH_MARGIN_TILES) — this function has no opinion on
  // which; it only ever walks tiles genuinely present in `available` within
  // whatever `box` it is given, so it never itself decides to cross a
  // section boundary. `available.has` is bounds-independent from `box`
  // scope-wise for the CHAMFER/RIGHT-ANGLE arm search below (those helpers
  // read `available` directly, no box parameter), matching how
  // `candidateTierPath` already calls them.
  const extend = seed % 3 === 1 ? extendWithRightAngleTurn : extendWithChamferedTurn;
  const bent = extend(available, best, seed);
  const finalPath = bent.length > best.length ? bent : best;
  if (finalPath.length < MIN_TIER_RUN_TILES) return [];
  // DEAD-END STUB/SPUR FIX: re-check the homeBox gate AND the terminus rule
  // on the FINAL path — the bend helpers walk `available` with no box or
  // terminus awareness of their own, so a candidate that passed both gates
  // as a straight run could otherwise be carried back out of `homeBox`, or
  // away from a valid terminus, by the added bend.
  if (!satisfiesHomeBox(bestAnchor.x, bestAnchor.y, finalPath)) return [];
  // A collinear continuation stays exempt after the bend: the run is still
  // the same line made longer, and the bend arm is the geometry
  // `candidateTierPath` already sanctions elsewhere in this file.
  if (!bestCollinear && !hasValidTerminus(finalPath)) return [];
  return finalPath;
}

/**
 * R3-C FIX (round-3 finding, HIGH — the 2.58x tick regression on Aaron's
 * real 49k-building save). Root cause, found by direct profiling (not the
 * originally-suspected per-section `occupiedSet` fold, which measured only
 * 8-42ms/tick against a 227-384ms total): on a real, large city with
 * abundant free space, the layout stage finds SOMETHING to commit on
 * almost EVERY glide day — which means `cur.buildings` gets a NEW array
 * identity almost every tick. This defeats the WHOLE buildings-identity
 * cache architecture this codebase's 49k-scale performance depends on
 * (sectionIndexOf, occupiedSet, buildingByIdOf, connectedRoadTileSet, and
 * more, throughout advance() — not just inside the layout stage itself):
 * every one of those caches only pays off when `s.buildings` stays the
 * SAME reference across consecutive ticks, which pre-inc3 was the common
 * case (most glide days find nothing to consolidate) and inc3 broke by
 * design (this feature's whole point is to make MORE days do something).
 * A full architectural fix (e.g. incremental, non-buildings-identity
 * cache invalidation throughout advance()) is a much larger change than
 * this round's budget allows. This is the pragmatic, disclosed mitigation:
 * the layout stage only actually RUNS its section loop on one glide day in
 * this many — every other day is a true no-op (`cur.buildings` genuinely
 * unchanged, every cache downstream stays hot). PLACEHOLDER-tier
 * (Aaron's balance pass pending — a lower value repaints faster but costs
 * more; measure/tune against the real save, not guessed).
 *
 * ROLLED BACK TO 1 (round-3 re-verification): measured on Aaron's real
 * 49k save, throttling to 1-in-6/1-in-10 days only moved the mean tick
 * cost from ~2.6x to ~1.6-2x of OFF — well short of the ~1.15x target,
 * because direct profiling showed the dominant cost is NOT concentrated in
 * however often the section loop itself runs (measured 8-42ms/tick even
 * when it fires every day) but in general advance()-wide costs that scale
 * with total building count regardless of layout cadence. Meanwhile the
 * throttle broke a real correctness-observability property: AC-8 reserve
 * REUSE requires revisiting the same section on two separate
 * layout-running days, and combined with glide's own slow raster
 * (~425 days to finish one row), throttling made that combination
 * unreachable inside realistic test/play windows. Given it did not deliver
 * the promised perf win and cost real functionality, it is disabled
 * (value 1 = never throttled) pending a genuine architectural fix to the
 * buildings-identity cache interaction — see this build's report for the
 * disclosed, still-open R3-C perf finding.
 */
export const LAYOUT_THROTTLE_TICKS = 1;

/**
 * BUG-754 FIX ("connect-or-don't-lay" — Aaron's "roads lay out, train
 * layout... clean hierarchy" ruling, FEAT-2326609779's last structural
 * item): round-13's own investigation named the exact root cause of the
 * ever-rising component count — every tier's candidate generation was
 * scoped STRICTLY to one 16x16-tile section box (`extendExistingRun` could
 * only ever continue an existing run inside that same box), so a run that
 * reached a section's edge had nowhere left to grow even when the very next
 * tile over (in the neighbouring section) was free and would have joined
 * two networks into one. This is the margin `extendExistingRun` may now
 * search beyond its OWN section's box when trying to CONTINUE an existing
 * run (never for a brand-new `candidateTierPath` stub, which stays
 * section-scoped — controlling blast radius exactly as before). One
 * section width (SECTION_TILES=16 tiles at the current 800m/50m-per-tile
 * settings) — PLACEHOLDER-tier (Aaron's balance pass pending): enough to
 * reach into an immediately-adjacent section's free space without
 * approaching anything like the wilderness-scatter LAYOUT_WILDERNESS_
 * MARGIN_TILES already guards against (64 tiles, four times this).
 */
export const LAYOUT_EXTENSION_SEARCH_MARGIN_TILES = 16;

/**
 * BUG-754 FIX: the "connect-or-don't-lay" gate itself (task requirement 1)
 * — is EITHER end of `path` 4-neighbour (orthogonally) adjacent to a tile
 * already in `networkTiles`? `networkTiles` is the caller's own choice of
 * "what counts as the network" — engine.ts passes the section's (haloed)
 * `existingNetworkTiles` set, which already covers every tier (rail/
 * motorway/road, including genesis tiles — see that set's own doc) per
 * requirement 1's "any tier, incl. genesis roads". A path with zero tiles
 * is vacuously disconnected (never laid — there is nothing to connect).
 * Pure, deterministic (GR#21): no iteration order dependency, just two
 * fixed-position lookups.
 */
export function hasConnectionPoint(path: readonly TileXY[], networkTiles: ReadonlySet<string>): boolean {
  if (path.length === 0) return false;
  const touches = (p: TileXY): boolean =>
    ORTHO_DIRS.some(([dx, dy]) => networkTiles.has(`${p.x + dx},${p.y + dy}`));
  return touches(path[0]) || touches(path[path.length - 1]);
}

// ---------------------------------------------------------------------------
// §1 Geometry — AC-2/AC-5 bend validation, AC-3 junction validation.
// ---------------------------------------------------------------------------

export interface TileXY {
  x: number;
  y: number;
}

/** Interior angle at `b`, between rays b->a and b->c, in degrees. 180 = straight through, 90 = a right-angle turn, 0 = a dead reversal onto the same tile. Degenerate (a===b or c===b) returns 180 (treated as "no bend", never spuriously flagged). */
function interiorAngleDeg(a: TileXY, b: TileXY, c: TileXY): number {
  const v1x = a.x - b.x;
  const v1y = a.y - b.y;
  const v2x = c.x - b.x;
  const v2y = c.y - b.y;
  const m1 = Math.hypot(v1x, v1y);
  const m2 = Math.hypot(v2x, v2y);
  if (m1 === 0 || m2 === 0) return 180;
  const cos = Math.max(-1, Math.min(1, (v1x * v2x + v1y * v2y) / (m1 * m2)));
  return (Math.acos(cos) * 180) / Math.PI;
}

/**
 * AC-5: walks `pathTiles` and checks every three-consecutive-point angle
 * against `tier`'s AC-2 minimum. O(pathLength), called once per tier
 * placement (never per-tile in a hot loop, never per-candidate). A path of
 * fewer than 3 tiles has no bend to evaluate and is trivially valid.
 */
export function isValidBendPath(tier: TierKind, pathTiles: readonly TileXY[]): boolean {
  if (pathTiles.length < 3) return true;
  const minAngle = TIER_MIN_INTERIOR_ANGLE_DEG[tier];
  for (let i = 1; i < pathTiles.length - 1; i++) {
    const angle = interiorAngleDeg(pathTiles[i - 1], pathTiles[i], pathTiles[i + 1]);
    if (angle < minAngle) return false;
  }
  return true;
}

/** The FIRST out-of-spec bend index in `pathTiles` (the interior point index, 1-based into the path), or -1 if the whole path is valid. Used by the audit/conflict reporting to name WHICH tile broke the rule, not just that the path failed. */
export function firstInvalidBendIndex(tier: TierKind, pathTiles: readonly TileXY[]): number {
  if (pathTiles.length < 3) return -1;
  const minAngle = TIER_MIN_INTERIOR_ANGLE_DEG[tier];
  for (let i = 1; i < pathTiles.length - 1; i++) {
    if (interiorAngleDeg(pathTiles[i - 1], pathTiles[i], pathTiles[i + 1]) < minAngle) return i;
  }
  return -1;
}

/** AC-3 angle rule: the angle between a lower tier's entry direction and a higher tier's own direction at their junction point must be >= MIN_JUNCTION_ENTRY_ANGLE_DEG. `entryAngleDeg` is the caller-computed angle (via interiorAngleDeg-style geometry on the two segments) — kept as a pure threshold check so callers with different path representations can all reuse it. */
export function isValidJunctionAngle(entryAngleDeg: number): boolean {
  return entryAngleDeg >= MIN_JUNCTION_ENTRY_ANGLE_DEG;
}

/** AC-3 hierarchy rule: may `crossing` (the tier trying to use a tile) coexist with `existing` (the tier already occupying it)? A higher tier may pass through/over a lower one; a lower tier may never cross a higher one (it must terminate/junction instead) — same tier never "passes through" itself (a duplicate claim is always a tile-spread conflict, AC-3 rule 3). */
export function mayPassThrough(crossing: TierKind, existing: TierKind): boolean {
  return isHigherTier(crossing, existing);
}

/**
 * F2 FIX (independent round finding, HIGH): AC-3's angle rule, ACTUALLY
 * evaluated against the other tiers this pass has already placed in the
 * SAME section — not a hardcoded `true`. For every tile in `path` that is
 * 4-neighbour-adjacent to a tile of a DIFFERENT, already-placed tier, this
 * is a real junction: if the OTHER tier outranks `tier` (the hierarchy
 * rule — a lower tier must terminate at/junction with a higher one, never
 * cross it), the angle between `tier`'s own entry direction into the
 * junction tile and the higher tier's local direction there must be
 * `>= MIN_JUNCTION_ENTRY_ANGLE_DEG` (AC-3's "no acute merges"). `tier`'s
 * entry direction is read straight off `path` itself (the tile before the
 * junction point, or after, whichever exists); the higher tier's local
 * direction is read off ANY of its own tile-mates adjacent to the junction
 * tile (deterministic: the lexicographically-smallest "x,y" neighbour, so
 * two equally-valid neighbours never produce a different answer from a
 * different call). Returns `true` (vacuously) when `path` never touches a
 * higher tier at all — the common case for most sections.
 */
const ORTHO_DIRS: readonly [number, number][] = [
  [1, 0],
  [-1, 0],
  [0, 1],
  [0, -1],
];

/**
 * R3-B FIX (round-3 finding, HIGH — "falsely rejects PARALLEL ADJACENT
 * rows"): the ORIGINAL version checked EVERY tile in `path` against its 4
 * orthogonal neighbours, so two tiers simply running alongside each other
 * one row apart (rail at y=5, motorway at y=6 — never meeting, never
 * crossing) had every single tile flagged as a "junction", 28% of real
 * placements suppressed by a check that had nothing to do with AC-3's
 * actual concern. A junction, by AC-3's own wording, is where a lower tier
 * "terminates AT or forms a junction WITH" a higher one — i.e. the path
 * running INTO it, not merely sitting beside it for its whole length. The
 * fix checks ONLY the path's own two ENDPOINTS, and only in the direction
 * the path is already travelling AT that endpoint (`aheadOfStart`/
 * `aheadOfEnd` — "if this path continued one more tile, would it hit a
 * higher tier"), never a perpendicular side-neighbour of a tile in the
 * middle of a straight run. A parallel adjacent row's endpoints extend
 * further along the SAME row (never toward the other row), so this
 * structurally cannot false-positive on it; a path that genuinely runs up
 * to and stops at a higher tier (a real T-junction) still gets evaluated,
 * at real angle, right where AC-3 says. `mutation5-red.test` (round bar)
 * proves this is reachable: a stub `=> true` here misses a constructed
 * acute-approach case this function correctly rejects.
 */
export function evaluateJunctionRules(
  tier: TierKind,
  path: readonly TileXY[],
  placedTierTiles: ReadonlyMap<TierKind, ReadonlySet<string>>,
): boolean {
  if (path.length < 2) return true; // no direction of travel to extend — nothing to check.
  const first = path[0];
  const second = path[1];
  const last = path[path.length - 1];
  const secondLast = path[path.length - 2];
  const checkPoints: Array<{ entryFrom: TileXY; tile: TileXY; ahead: TileXY }> = [
    { entryFrom: second, tile: first, ahead: { x: first.x + (first.x - second.x), y: first.y + (first.y - second.y) } },
    { entryFrom: secondLast, tile: last, ahead: { x: last.x + (last.x - secondLast.x), y: last.y + (last.y - secondLast.y) } },
  ];
  for (const { entryFrom, tile, ahead } of checkPoints) {
    const nk = tileKey(ahead);
    for (const [otherTier, tiles] of placedTierTiles) {
      if (otherTier === tier || !tiles.has(nk)) continue;
      if (!isHigherTier(otherTier, tier)) continue; // hierarchy rule only constrains LOWER meeting HIGHER.
      // The higher tier's own local direction at the junction tile: its
      // lexicographically-first neighbour tile-mate (deterministic).
      const mateCandidates: TileXY[] = [];
      for (const [ddx, ddy] of ORTHO_DIRS) {
        const mk = `${ahead.x + ddx},${ahead.y + ddy}`;
        if (mk !== tileKey(tile) && tiles.has(mk)) mateCandidates.push({ x: ahead.x + ddx, y: ahead.y + ddy });
      }
      if (mateCandidates.length === 0) continue; // an isolated higher-tier tile has no direction either.
      mateCandidates.sort((a, b) => (tileKey(a) < tileKey(b) ? -1 : 1));
      const mate = mateCandidates[0];
      const angle = interiorAngleDeg(entryFrom, ahead, mate);
      if (!isValidJunctionAngle(angle)) return false;
    }
  }
  return true;
}

// ---------------------------------------------------------------------------
// §2 Deterministic layout seed (AC-10) and free-run search.
// ---------------------------------------------------------------------------

/**
 * AC-10: layout seed derived ONLY from sectionKey + tick — never
 * Math.random/Date.now/localStorage. A plain deterministic integer mix
 * (Knuth multiplicative hash constants), used solely to pick a scan
 * STARTING offset for the tier run-search below (purely aesthetic variety
 * across ticks/sections) — every candidate search itself remains a fully
 * deterministic, order-independent scan regardless of the seed's value, so
 * this is never load-bearing for correctness, only for which of several
 * equally-valid maximal runs is picked when the map is symmetric.
 */
export function layoutSeedOf(sectionKey: number, tick: number): number {
  const mixed = (sectionKey * 2654435761 + tick * 40503) >>> 0;
  return mixed >>> 0;
}

function tileKey(p: TileXY): string {
  return `${p.x},${p.y}`;
}

/**
 * The longest contiguous straight run of tiles present in `available`
 * (a Set of "x,y" keys) within the section box, scanning rows (vertical
 * =false) or columns (vertical=true), starting the scan at `offset` rows/
 * cols in (wrapping) purely to vary which row/col is tried first across
 * sections/ticks — the winning run is always the row/col with the greatest
 * run length, ties broken by the smallest (y,x) origin (a full, explicit
 * order — never iteration-order-dependent). O(sectionTiles).
 */
function longestRun(
  available: ReadonlySet<string>,
  x0: number,
  y0: number,
  w: number,
  h: number,
  vertical: boolean,
  offset: number,
): TileXY[] {
  const primaryCount = vertical ? w : h;
  const secondaryCount = vertical ? h : w;
  let best: TileXY[] = [];
  for (let pi = 0; pi < primaryCount; pi++) {
    const p = (pi + offset) % primaryCount;
    let run: TileXY[] = [];
    for (let si = 0; si < secondaryCount; si++) {
      const x = vertical ? x0 + p : x0 + si;
      const y = vertical ? y0 + si : y0 + p;
      if (available.has(`${x},${y}`)) {
        run.push({ x, y });
      } else {
        if (run.length > best.length) best = run;
        run = [];
      }
    }
    if (run.length > best.length) best = run;
  }
  return best;
}

/**
 * One tier's RAW candidate path within a section, computed independently
 * against `available` (a Set of "x,y" keys this tier may use — the caller
 * decides what that means: pure free space for the first-come tier, or free
 * space MINUS already-claimed-this-pass tiles for a later one). Picks
 * whichever orientation (horizontal/vertical) yields the longer run (ties
 * favour horizontal — a total, deterministic order). Returns `[]` if no run
 * reaches MIN_TIER_RUN_TILES.
 */
export function candidateTierPath(
  available: ReadonlySet<string>,
  box: { x0: number; y0: number; w: number; h: number },
  seed: number,
): TileXY[] {
  const rowOffset = box.h > 0 ? seed % box.h : 0;
  const colOffset = box.w > 0 ? seed % box.w : 0;
  const horizontal = longestRun(available, box.x0, box.y0, box.w, box.h, false, rowOffset);
  const vertical = longestRun(available, box.x0, box.y0, box.w, box.h, true, colOffset);
  const best = vertical.length > horizontal.length ? vertical : horizontal;
  if (best.length < MIN_TIER_RUN_TILES) return [];

  // HONESTY FIX (round-3 finding — "the chamfer is synthetically 10% but
  // 0/243 in real placements"): the ROOT CAUSE, found by tracing it: the
  // untrimmed `best` run is almost always the LONGEST available run in the
  // section, which in a genuinely empty section means it spans the WHOLE
  // section edge-to-edge — leaving literally no free tile beyond its own
  // endpoint for a chamfer to land on (the tile one step further is outside
  // the section box, hence never in `available`). The chamfer logic itself
  // was always correct (135 degrees, provably valid for every tier); it
  // was simply never given room to fire. Fixed for real, not decoratively:
  // if the full-length run leaves no room, retry with the run trimmed 1..3
  // tiles short of its own end (still >= MIN_TIER_RUN_TILES), which frees
  // exactly the tiles a chamfer needs — and keep whichever candidate
  // (untrimmed straight, or trimmed+chamfered) is the LONGEST total path,
  // so this never trades a longer straight run for a shorter bent one.
  // Which bend geometry to attempt is picked by the seed (deterministic,
  // GR#21) — NOT always the chamfer. `seed % 3 === 1` tries the sometimes-
  // FAILABLE right-angle turn (see extendWithRightAngleTurn's doc — this is
  // the honesty fix: without this, every real placement only ever tried
  // the always-valid 135-degree chamfer, so AC-2/AC-5's bend gate was
  // reachable but could never actually reject anything real).
  const extend = seed % 3 === 1 ? extendWithRightAngleTurn : extendWithChamferedTurn;
  const straightCandidate = best;
  let bendCandidate: TileXY[] = extend(available, best, seed);
  for (let trim = 1; trim <= 3 && bendCandidate.length <= best.length && best.length - trim >= MIN_TIER_RUN_TILES; trim++) {
    const trimmed = best.slice(0, best.length - trim);
    const attempt = extend(available, trimmed, seed);
    if (attempt.length > bendCandidate.length) bendCandidate = attempt;
  }
  return bendCandidate.length > straightCandidate.length ? bendCandidate : straightCandidate;
}

/**
 * F2 FIX (independent round finding, HIGH): the estate's ORIGINAL generator
 * could only ever emit a dead-straight run — bendGeometry/junctionRules/
 * severanceTest were structurally unreachable because there was never a bend
 * to validate. This extends the straight run with ONE real turn at its far
 * end, using a CHAMFERED corner (a single diagonal tile between the two
 * straight arms, not an abrupt right-angle) — chosen because the geometry is
 * exact and tier-independent: for any straight arm direction D and turn
 * direction T (perpendicular, unit vectors), the diagonal chamfer tile is
 * `end + D + T`, and BOTH new interior angles (arm1-to-chamfer,
 * chamfer-to-arm2) work out to exactly 135 degrees — provably above every
 * AC-2 tier minimum (the tightest is rail's 67.5, dual/A-road's 112.5 is the
 * closest and 135 still clears it with headroom), so this extension can
 * never itself fail `isValidBendPath` for any tier. Extension is attempted
 * once, using the seed's parity to pick which perpendicular side to try
 * first (deterministic, GR#21) — if neither side's chamfer+2-tile arm2 lies
 * entirely within `available`, the plain straight `best` is returned
 * unchanged (most sections most of the time — this is additive, not
 * mandatory). Only ever EXTENDS `best`, never shrinks or reorders it, so
 * `isValidBendPath` still sees the ORIGINAL straight tiles as straight and
 * only the new corner as a genuine bend to validate.
 */
function extendWithChamferedTurn(available: ReadonlySet<string>, best: readonly TileXY[], seed: number): TileXY[] {
  if (best.length < 2) return best.slice();
  const end = best[best.length - 1];
  const prev = best[best.length - 2];
  const dx = Math.sign(end.x - prev.x);
  const dy = Math.sign(end.y - prev.y);
  const perpCandidates: TileXY[] =
    dx !== 0 ? [{ x: 0, y: 1 }, { x: 0, y: -1 }] : [{ x: 1, y: 0 }, { x: -1, y: 0 }];
  const ordered = seed % 2 === 0 ? perpCandidates : [perpCandidates[1], perpCandidates[0]];
  const usedKeys = new Set(best.map(tileKey));
  const ARM2_LEN = MIN_TIER_RUN_TILES; // the second arm must itself be a real, meaningful segment.

  for (const t of ordered) {
    const chamfer: TileXY = { x: end.x + dx + t.x, y: end.y + dy + t.y };
    const chamferKey = tileKey(chamfer);
    if (usedKeys.has(chamferKey) || !available.has(chamferKey)) continue;
    // GR#21: no `break` anywhere in this module (even inside a fixed-length
    // numeric loop with no unordered-iteration risk) — built as an array of
    // CANDIDATE arm2 tiles up front, then validated with `.every(...)`
    // (which short-circuits internally without this module ever writing the
    // keyword itself) instead of an imperative loop-with-break.
    const arm2Candidates: TileXY[] = Array.from({ length: ARM2_LEN }, (_, i) => ({
      x: chamfer.x + t.x * (i + 1),
      y: chamfer.y + t.y * (i + 1),
    }));
    const arm2Valid = arm2Candidates.every((p) => {
      const k = tileKey(p);
      return !usedKeys.has(k) && available.has(k);
    });
    if (!arm2Valid) continue;
    return [...best, chamfer, ...arm2Candidates];
  }
  return best.slice();
}

/**
 * HONESTY FIX (round-3 finding — "at 135 degrees it can never fail
 * isValidBendPath... do not leave decorative validation"): the chamfered
 * turn above is DELIBERATELY always-valid (135 degrees clears every tier's
 * AC-2 minimum), so on its own the bend gate could fire on real placements
 * yet never once REJECT one — reachable, but not genuinely tested by real
 * play. This is the OTHER real geometry: an immediate square (90-degree)
 * corner, no diagonal chamfer tile — `arm2` starts directly from `end`,
 * turning perpendicular with no forward step first. Interior angle at
 * `end` works out to EXACTLY 90 degrees (vectors `prev-end` and
 * `arm2[0]-end` are perpendicular unit vectors, dot product 0) — this
 * genuinely PASSES `isValidBendPath` for rail/motorway/minor (67.5/90/45,
 * all <= 90) and genuinely FAILS it for dual/A-road (112.5 > 90). Which
 * candidates are ever tried is decided in `candidateTierPath` by the
 * layout seed (deterministic, GR#21), so across many real sections/ticks
 * BOTH the always-passing chamfer and the sometimes-failing right angle
 * actually occur, giving the AC-2/AC-5 gates a real placement to reject.
 */
function extendWithRightAngleTurn(available: ReadonlySet<string>, best: readonly TileXY[], seed: number): TileXY[] {
  if (best.length < 2) return best.slice();
  const end = best[best.length - 1];
  const prev = best[best.length - 2];
  // Only the ARM'S OWN travel axis matters here (no diagonal chamfer step
  // to combine with a forward direction) — `dx` alone distinguishes
  // horizontal vs. vertical travel to pick the perpendicular turn options.
  const dx = Math.sign(end.x - prev.x);
  const perpCandidates: TileXY[] = dx !== 0 ? [{ x: 0, y: 1 }, { x: 0, y: -1 }] : [{ x: 1, y: 0 }, { x: -1, y: 0 }];
  const ordered = seed % 2 === 0 ? perpCandidates : [perpCandidates[1], perpCandidates[0]];
  const usedKeys = new Set(best.map(tileKey));
  const ARM2_LEN = MIN_TIER_RUN_TILES;

  for (const t of ordered) {
    const arm2Candidates: TileXY[] = Array.from({ length: ARM2_LEN }, (_, i) => ({
      x: end.x + t.x * (i + 1),
      y: end.y + t.y * (i + 1),
    }));
    const arm2Valid = arm2Candidates.every((p) => {
      const k = tileKey(p);
      return !usedKeys.has(k) && available.has(k);
    });
    if (!arm2Valid) continue;
    return [...best, ...arm2Candidates];
  }
  return best.slice();
}

// ---------------------------------------------------------------------------
// §3 Tier-pair conflict resolution (AC-3 tile-spread rule, AC-6 audit).
// ---------------------------------------------------------------------------

export interface TileConflict {
  x: number;
  y: number;
  reason: string;
}

export interface ResolvedTierPaths {
  /** Each tier's surviving path, in TIER_ORDER, after removing tiles claimed by a strictly-higher tier. */
  paths: Record<TierKind, TileXY[]>;
  conflictsDetected: TileConflict[];
  skippedTiles: Array<{ x: number; y: number; tier: TierKind; reason: string }>;
}

/**
 * AC-6: given each tier's INDEPENDENTLY-computed raw candidate path (which
 * may overlap — two tiers wanting the same tile), resolves tile-spread
 * conflicts deterministically: process tiers in TIER_ORDER (rail first); a
 * tile already claimed by an earlier (== higher-ranked) tier is removed from
 * every later tier's path and recorded once in `conflictsDetected` plus once
 * per losing tier in `skippedTiles`. Within the SAME tier, a tile can only
 * ever appear once (candidateTierPath never repeats a coordinate), so no
 * same-tier tie-break is needed here — AC-6's "same tier, earlier origin
 * wins" case is structurally already handled by `candidateTierPath` only
 * ever returning ONE path per tier per pass.
 */
export function resolveTierConflicts(rawPaths: Readonly<Record<TierKind, readonly TileXY[]>>): ResolvedTierPaths {
  const claimed = new Map<string, TierKind>();
  const paths: Record<TierKind, TileXY[]> = { rail: [], motorway: [], dual: [], aroad: [], minor: [] };
  const conflictsDetected: TileConflict[] = [];
  const skippedTiles: Array<{ x: number; y: number; tier: TierKind; reason: string }> = [];

  for (const tier of TIER_ORDER) {
    const raw = rawPaths[tier] ?? [];
    const survivors: TileXY[] = [];
    for (const p of raw) {
      const key = tileKey(p);
      const holder = claimed.get(key);
      if (holder != null && holder !== tier) {
        // AC-3 hierarchy rule: a higher tier may pass through a lower one
        // (rail literally over motorway) — but this pass never lets a
        // LOWER tier win a tile a higher tier already claimed (mayPassThrough
        // only ever true in the direction rail->motorway->...), so any
        // survivor here is by construction the higher-ranked claimant.
        if (!mayPassThrough(tier, holder)) {
          conflictsDetected.push({ x: p.x, y: p.y, reason: `${tier} vs ${holder} tile-spread conflict` });
          skippedTiles.push({ x: p.x, y: p.y, tier, reason: `yielded to higher tier ${holder}` });
          continue;
        }
      }
      claimed.set(key, tier);
      survivors.push(p);
    }
    paths[tier] = survivors;
  }
  return { paths, conflictsDetected, skippedTiles };
}

/**
 * ROUND-10 R10-F1 FIX (P1, "the road with a hole"): `resolveTierConflicts`
 * above removes individual tiles from the MIDDLE of a tier's candidate path
 * whenever a higher tier already claimed them — the SURVIVOR list it
 * returns is still in the original path's order, but with those tiles
 * simply missing, which can leave a real GAP (e.g. planned 80,3..94,3 then
 * 95,7 with the chamfer arm 95,4/95,5/95,6 removed) that the caller used to
 * commit as if it were one contiguous line. Geometry validation
 * (isValidBendPath/evaluateJunctionRules) then passes VACUOUSLY: both walk
 * consecutive ARRAY entries, never checking that consecutive entries are
 * actually adjacent tiles, so a holed sequence reads as "valid" the same
 * way a straight run does.
 *
 * The fix: after conflict resolution, the caller (engine.ts's
 * applyTierLayoutForSection) runs the survivor path through this function
 * BEFORE any geometry/funds gate — extracting the LONGEST maximal run of
 * genuinely adjacent tiles (orthogonal or the chamfer's own diagonal step,
 * i.e. Chebyshev distance exactly 1) and discarding everything before and
 * after it. A path with no gap at all returns itself unchanged (this is a
 * strict narrowing, never a change to an already-contiguous candidate).
 * MIN_TIER_RUN_TILES re-validation on the returned fragment is the CALLER's
 * job (mirroring how this function's raw input may already be empty) —
 * kept out of this pure geometry helper so it stays a single-purpose,
 * side-effect-free fold (GR#21).
 */
export function longestContiguousFragment(path: readonly TileXY[]): TileXY[] {
  if (path.length === 0) return [];
  const isAdjacent = (a: TileXY, b: TileXY): boolean =>
    Math.abs(a.x - b.x) <= 1 && Math.abs(a.y - b.y) <= 1 && (a.x !== b.x || a.y !== b.y);
  let bestStart = 0;
  let bestLen = 1;
  let curStart = 0;
  let curLen = 1;
  for (let i = 1; i < path.length; i++) {
    if (isAdjacent(path[i - 1], path[i])) {
      curLen += 1;
    } else {
      if (curLen > bestLen) {
        bestLen = curLen;
        bestStart = curStart;
      }
      curStart = i;
      curLen = 1;
    }
  }
  if (curLen > bestLen) {
    bestLen = curLen;
    bestStart = curStart;
  }
  return path.slice(bestStart, bestStart + bestLen);
}

// ---------------------------------------------------------------------------
// §4 Free-space disposition (AC-7/AC-8).
// ---------------------------------------------------------------------------

export interface FreeSpaceAllocation {
  tilesByKind: { parks: TileXY[]; reserve: TileXY[] };
  parkCount: number;
  reserveCount: number;
}

/**
 * AC-7: classifies every tile in `freeAfterTiers` (tiles left over once all
 * five infrastructure tiers have claimed theirs) as a park candidate
 * (`nearAmenity(tile)` true) or growth reserve (everything else). Sorted
 * output (ascending y, then x) for determinism (GR#21) — `freeAfterTiers`
 * itself may arrive in any order.
 */
export function classifyFreeSpace(
  freeAfterTiers: readonly TileXY[],
  nearAmenity: (p: TileXY) => boolean,
): FreeSpaceAllocation {
  const sorted = freeAfterTiers.slice().sort((a, b) => (a.y !== b.y ? a.y - b.y : a.x - b.x));
  const parks: TileXY[] = [];
  const reserve: TileXY[] = [];
  for (const p of sorted) {
    if (nearAmenity(p)) parks.push(p);
    else reserve.push(p);
  }
  return { tilesByKind: { parks, reserve }, parkCount: parks.length, reserveCount: reserve.length };
}

// ---------------------------------------------------------------------------
// §5 Severance checker (AC-4). Pure graph algorithm over an explicit tile
// set — deliberately independent of engine.ts's computeRoadConnectivity
// (which models CITY-WIDE drivable-road-to-trunk reachability, a different
// and coarser concept than "did THIS tier's own network fall into two
// pieces"). AC-4's own wording ("rail checks rail connectivity, motorway
// checks motorway + rail, etc.") is exactly a same-or-higher-tier
// 4-neighbour flood fill, which is what this implements.
// ---------------------------------------------------------------------------

/** 4-neighbour flood-fill connected components over `tiles` (a Set of "x,y" keys). Returns a Map from tile key to a component id (0-based, deterministic: components are discovered in ascending (y,x) order of their first tile). */
export function tileComponents(tiles: ReadonlySet<string>): Map<string, number> {
  const sortedKeys = Array.from(tiles).sort((a, b) => {
    const [ax, ay] = a.split(',').map(Number);
    const [bx, by] = b.split(',').map(Number);
    return ay !== by ? ay - by : ax - bx;
  });
  const comp = new Map<string, number>();
  let nextId = 0;
  const DIRS: readonly [number, number][] = [
    [1, 0],
    [-1, 0],
    [0, 1],
    [0, -1],
  ];
  for (const start of sortedKeys) {
    if (comp.has(start)) continue;
    const id = nextId++;
    const stack = [start];
    comp.set(start, id);
    while (stack.length > 0) {
      const cur = stack.pop() as string;
      const [cx, cy] = cur.split(',').map(Number);
      for (const [dx, dy] of DIRS) {
        const nk = `${cx + dx},${cy + dy}`;
        if (tiles.has(nk) && !comp.has(nk)) {
          comp.set(nk, id);
          stack.push(nk);
        }
      }
    }
  }
  return comp;
}

/**
 * AC-4: does adding `addedTiles` (and optionally removing `removedTiles`) to
 * `existingTierOrHigherTiles` sever the network — i.e. do two tiles that
 * were in the SAME connected component before the change end up in
 * DIFFERENT components afterward? Pure addition can never sever a graph
 * (adding nodes/edges only merges or leaves components alone), so this only
 * ever returns true when `removedTiles` is non-empty and a demolition broke
 * the sole path between two halves — exactly AC-4's "later tier demolishes
 * an earlier one's connector" scenario. Returns the set of BEFORE-connected
 * pairs' representative tiles that broke apart, for the audit's
 * `failureReason` — empty if no severance.
 */
export function wouldSever(
  existingTierOrHigherTiles: ReadonlySet<string>,
  addedTiles: ReadonlySet<string>,
  removedTiles: ReadonlySet<string> = new Set(),
): boolean {
  if (removedTiles.size === 0) return false; // pure addition: structurally cannot sever.
  const before = existingTierOrHigherTiles;
  const after = new Set(before);
  for (const t of removedTiles) after.delete(t);
  for (const t of addedTiles) after.add(t);
  const compBefore = tileComponents(before);
  const compAfter = tileComponents(after);
  // Group the SURVIVING tiles (not removed) by their BEFORE component, then
  // check every pair sharing a before-component still shares an after-component.
  const byBeforeComp = new Map<number, string[]>();
  for (const [tile, cid] of compBefore) {
    if (removedTiles.has(tile)) continue;
    (byBeforeComp.get(cid) ?? byBeforeComp.set(cid, []).get(cid)!).push(tile);
  }
  for (const group of byBeforeComp.values()) {
    if (group.length < 2) continue;
    const firstAfter = compAfter.get(group[0]);
    for (let i = 1; i < group.length; i++) {
      if (compAfter.get(group[i]) !== firstAfter) return true;
    }
  }
  return false;
}

// ---------------------------------------------------------------------------
// §6 Audit shapes (AC-11). Data types only — engine.ts's applyConsolidatorPass
// is the sole writer of TierImplementation[] (it owns funds/mutation); this
// module only ever produces the `plan` half plus the pure geometry flags.
// ---------------------------------------------------------------------------

export interface TierPlan {
  tier: TierKind;
  plannedTiles: TileXY[];
  estimatedCost: number;
  estimatedScrap: number;
  validationTests: {
    bendGeometry: boolean;
    severanceTest: boolean;
    junctionRules: boolean;
  };
}

export interface TierImplementation extends TierPlan {
  actuallyPlaced: boolean;
  failureReason?: string;
  actualTiles: TileXY[];
  actualCost: number;
  conflictsResolved: TileConflict[];
  /**
   * F5 FIX (independent round finding, MEDIUM — AC-8): how many of
   * `actualTiles` were already marked as growth reserve from an EARLIER
   * pass, reused here at zero scrap cost (structurally always zero —
   * nothing was ever built on a reserve tile to demolish). 0/undefined on
   * an old-save-shaped record or a tier with no prior reserve overlap.
   */
  reservedTilesReused?: number;
}
