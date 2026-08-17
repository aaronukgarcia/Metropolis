// Package coastal is the Coastal Arrivals module (MOD-044): §30 (section 30)
// irregular migration, modelled neutrally as operations + policy, with both
// sides of the ledger honest.
//
// It owns: small-boat arrival events on shore cells (frequency scaled by era,
// world conditions, and season; never player-triggered — see below), the
// coastguard/lifeboat rescue response, the reception-and-processing capacity
// (a finite caseworker-throughput ceiling whose overflow requisitions hotels
// at real cost plus a local satisfaction friction), the months-long granted/
// not-granted status pipeline (granted → a full citizen record via
// engine.citizens with a world-profile skills distribution; not-granted → a
// managed departure at cost), three policy sliders (processing funding,
// housing approach, integration investment) each with real trade-offs and no
// right answer, and factual, non-editorialised ticker reporting through
// engine.news.
//
// # Module key and GUID
//
//	Module key: engine.coastal (see code.json)
//	GUID:        8634ea26-2d71-48c2-94a3-49c68f11a6b4
//	Spec refs:   §30 (Coastal Arrivals — irregular migration); §2.1 (the
//	             south-edge shingle/sand shoreline and sea cells); §5.2
//	             (determinism: hash(worldSeed, i, m, purpose)).
//
// # "Never player-triggered" is structural, not documented (AC-2)
//
// There is NO exported command on CoastalAPI that creates, triggers, or adds
// an arrival event. Arrivals are generated only by the scheduled
// [CoastalAPI.Advance] path, seeded deterministically by
// det.NewStream(worldSeed, i, month, purpose) — the literal §5.2 rule — so an
// arrival event cannot be produced by a player action because the exported
// surface has no entry point for one. TestNoPlayerTriggerArrival asserts the
// events appear over simulated months from Advance alone, and
// TestArrivalScheduledOnlyViaAdvance asserts the export surface carries no
// creation command.
//
// # Shore-cell geography (ASM-207)
//
// This module does NOT own shore-cell geography — engine.world does, and it is
// a hand-authored piecewise-linear coastline approximation of the real Kent
// coast, not a downloaded coastline dataset (ASM-207). Coastal consumes shore
// membership through the injected [ShoreSource] seam (code.json registers no
// engine.coastal → engine.world edge, GR#20), so an arrival event's cell is
// only as accurate as that approximation; this module neither corrects nor
// flags individual misclassifications, consistent with engine.world's own
// scope boundary.
//
// # Blocked edges (BUG-058) — not wired, not faked
//
// Three §30 mechanics are blocked pending BUG-058 findings #5/#6/#7, because
// code.json registers no engine.coastal → engine.education, engine.coastal →
// engine.households, or engine.coastal → engine.tourism/engine.build edge:
//
//   - AC-8 (ESOL/adult-ed integration speed via engine.education) — BLOCKED.
//     Integration speed is a real, queryable coefficient here
//     ([CoastalAPI.IntegrationSpeed]) but is not yet fed by an education edge.
//   - AC-9 (dispersal-vs-centres housing allocation via engine.households) —
//     BLOCKED. The housing-approach slider affects reception cost, friction,
//     and integration speed here; it does not yet reach households' stock.
//   - AC-10 (hotel requisition sourced from engine.tourism's real
//     accommodation stock) — BLOCKED. Until unblocked, AC-5's hotel cost uses
//     a documented placeholder cost-per-night figure (ASM-323), never a
//     duplicated copy of engine.tourism's data (GR#3).
//
// This package deliberately imports none of engine.education, engine.households,
// engine.tourism, or engine.build (AC-16) — the gaps are not silently routed
// around.
//
// # Determinism (AC-15)
//
// Every stochastic draw — whether an arrival occurs this month, its size, its
// cell, a case's duration and verdict, and a granted citizen's skills — uses
// det.NewStream (hash(worldSeed, i, m, purpose)), never math/rand unseeded and
// never the wall clock. Replaying the same world seed with the same Advance
// sequence produces byte-identical arrival events, cases, and pipeline
// outcomes; the non-test files import neither time nor math/rand.
package coastal
