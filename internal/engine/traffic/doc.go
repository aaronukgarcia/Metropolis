// Package traffic implements the transport, routing & traffic module
// (MOD-023), currently shipping the Baseline One coarse-approximation
// layer plus the Stage 1 network primitives -- NOT the full §19 model.
// This module was twice destructively rejected for fabricating claims
// about what it does (fabricated "SUE convergence" prose with zero
// assignment code; a second fabricated "SUE" implementation whose
// assignment loop never actually reads the BPR travel times it computes).
// This doc.go states exactly what ships and what is deferred, per that
// history: the doc is the deliverable as much as the code.
//
// Key: engine.traffic
// Cites: §19 Transport, Routing & Traffic, §51 Roads v2, A4 Assignment
// structure, and §II.5 Movement (docs/METROPOLIS-MASTER-v2.1.md). Full
// acceptance criteria: docs/planning/acceptance/engine.traffic.md (MOD-023).
//
// # Numeric convention (FEAT-041)
//
// FEAT-041 ruled canonical-order float64 (not fixed-point) for traffic flow
// accounting: shared accumulators are summed in a deterministic key order
// (sorted uint64 IDs), never via unordered map-range or per-shard
// partial-sum-then-merge. Every place in this package that reduces a map
// into a scalar (demandMultiplier's total demand) sorts its keys first
// (AC-18). Citizen demand counts (int64) use num.SatAdd (GR#16) so a
// pathological accumulation saturates at MaxInt64 instead of wrapping
// negative.
//
// # What SHIPS in this delivery
//
//   - Coarse demand-multiplier layer: AddDemand / AddTripDemand /
//     RegisterTrip accumulate destination-keyed demand (saturating,
//     GR#16); CommuteHours / CommuteMinutes / AccessMinutes / and
//     ActiveTravelShare read a base config value (data/traffic.json,
//     GR#15) multiplied by a coarse v/c-STYLE scalar derived from total
//     accumulated demand (demandMultiplier). This is a citywide-average
//     stand-in for a real per-trip journey time, not a per-link or
//     per-route figure.
//   - Stage 1 network primitives: AddNode/AddLink build a graph of
//     Node/Link records; AddLinkVolume deterministically loads volume
//     onto a link; LinkTravelTime queries a SINGLE link's BPR volume-delay
//     travel time (T = T0 * (1 + alpha * (V/C)^beta)), reading lane count
//     and speed limit from engine.roads (SetRoads) when wired, falling
//     back to sane defaults (1 lane, 50kph) otherwise. LinkTravelTime is
//     guarded against non-finite results (capacity<=0, negative/non-finite
//     volume, or an overflowing pow term all reject with
//     ErrNonFiniteTravelTime rather than returning +Inf/NaN with a nil
//     error -- see api.go's LinkTravelTime doc comment).
//   - LoadConfig validates every Config field (including bprAlpha/bprBeta,
//     previously unvalidated) as finite and positive (or non-negative for
//     the active-travel share), fail-closed.
//   - Day-boundary contract for AdvanceTick: see below.
//   - G4500-G4599 registry error codes (MET-G4501..MET-G4503, MET-G4599).
//
// # What does NOT ship (deferred to a later heavy-model iteration)
//
// Per Aaron's 2026-08-14 Baseline One scope ruling (BOW comment on
// MOD-023): only a coarse approximation ships now; the full §19 model is
// deferred until after the baseline loop runs. Specifically deferred, with
// their acceptance-criteria clusters from engine.traffic.md:
//
//   - SUE (stochastic user equilibrium) assignment, the OD-matrix solver
//     round-trip through int.solver, and the warm-start route cache
//     (AC-2, AC-3, AC-3b, AC-14, AC-16b, AC-22). A prior version of this
//     module (commit a4791c1) shipped a "sue.go" that computed BPR travel
//     times and then never read them -- a topology-blind uniform demand
//     split masquerading as an assignment. That file does NOT ship; it was
//     rejected in full by an independent destructive round (2026-08-19)
//     and is not part of this package. Building a real SUE assignment
//     remains future work.
//   - Junction control types, turn-movement capacities, and queue
//     spillback (AC-6, AC-7, AC-8). No junction model exists at all in
//     this package; Node is an ID-only placeholder.
//   - The 11-mode table and per-trip nested logit mode choice (AC-9,
//     AC-10). No mode table, no logit, no data/modes.json consumer exists
//     here (ASM-215's coefficient schema is still unauthored).
//   - Door-to-door commute-time write into engine.citizens' hot record
//     (AC-11's write half). CommuteHours/CommuteMinutes/AccessMinutes are
//     read-only query surfaces returning the coarse multiplier; nothing in
//     this package calls into engine.citizens to persist a value.
//   - Freight-as-PCU network sharing (AC-12) and induced demand emerging
//     from a latent-demand pool (AC-13). Neither concept is modelled.
//   - Vehicle conservation (AC-5) does not apply: with no assignment loop,
//     there is no flow to conserve or drop.
//   - The full cross-worker-count determinism suite (AC-17, AC-20)
//     targeting a parallel SUE reduction: not applicable without an
//     assignment loop. The coarse layer's own reduction (demandMultiplier)
//     IS deterministic (sorted-key summation, no map-range on the
//     result path) and is tested as such, but this is a much smaller claim
//     than AC-17/AC-20's synthetic-city-scale, multi-worker-count proof.
//
// # Registered outbound edges: live vs declared
//
// This package imports and calls engine.roads (SetRoads / RoadsAPI --
// LIVE: LinkTravelTime reads CurrentLaneCount/RoadInfo when wired) and
// engine.education / engine.leisure (LIVE: RegisterTrip / AddTripDemand
// accept those packages' TripDemand types as their sole use of the
// import). code.json also registers engine.traffic -> engine.world,
// engine.citizens, int.solver, and engine.invariant as outbound edges
// from MOD-023's full design scope; NONE of those four are imported or
// called anywhere in this package as shipped -- they are
// forward-declarations for the heavy model listed above, not live calls. A reader grepping this package for
// "world." or "citizens." or "solver." will find nothing, which is
// correct and intentional at this scope, not an oversight.
//
// # Day-boundary contract for AdvanceTick (FEAT-206)
//
// AdvanceTick wipes the demand map unconditionally: everything accumulated
// via AddDemand / AddTripDemand / RegisterTrip since the LAST AdvanceTick
// call (the "prior day"'s demand) is cleared. Demand added AFTER
// AdvanceTick returns belongs to the new day, is immediately visible to
// CommuteHours/CommuteMinutes/AccessMinutes, and survives untouched until
// the NEXT AdvanceTick call.
//
// This makes AdvanceTick's correctness entirely a matter of WHEN it is
// called: the composition root owns calling it exactly once per simulated
// day, at a single fixed phase point, before that day's demand-generating
// systems (engine.shopping, engine.dispatch, etc.) run their own tick
// logic for the day. Calling it twice in one day, not at all, or after
// demand generators have already run for the day, breaks the contract --
// this package cannot detect or prevent those misuse patterns from inside
// AdvanceTick itself, since it has no visibility into the tick scheduler.
//
// AS SHIPPED, no composition root wires TrafficAPI or calls AdvanceTick at
// all (FEAT-206, tracked separately, not part of this delivery) --
// engine.shopping and engine.dispatch hold direct references for
// AddDemand/CommuteMinutes today. Until FEAT-206 lands, demand accumulated
// through those call sites is NEVER cleared in a running composition, and
// the original unbounded-growth defect this AdvanceTick method exists to
// fix is not closed end-to-end -- only within this package's own tests,
// which call AdvanceTick directly and prove the bounded behaviour when it
// IS called each day (see api_test.go's bounded-across-many-days and
// day-boundary-ordering tests).
package traffic
