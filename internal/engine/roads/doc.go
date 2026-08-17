// Package roads is the roads & auto-naming module (MOD-024): road-as-
// named-edge identity, the full §51 class ladder with in-place upgrade
// mechanics, simulated roadworks (phased lane closures), per-road
// maintenance state, and the deterministic seed+id auto-naming service
// other modules consume for civic buildings, infrastructure, districts and
// transit.
//
// Module key: engine.roads (see code.json)
// GUID:        0fa53b51-245f-426f-95fd-ba208a024a6c
// Spec refs:   §20 (Roads & Auto-Naming); §51 (Roads v2 — Types, Upgrades
// & the Lane Myth).
//
// # Scope boundary (architecture ruling, Bill 2026-08-09)
//
// This package owns the road-identity/geometry/class-ladder/naming half of
// the §20/§51 contract and NEVER calls engine.traffic — SUE assignment,
// junction control, mode-choice, live volume/v-c/OD-flow data, and journey-
// time estimates are engine.traffic's (MOD-023), and engine.traffic reads
// this package's identity/capacity data on its own initiative — the
// dependency is one-directional (traffic → roads), never the reverse. The
// full §20 "road inspector" and §51 "pre-approval projection" views are
// composed by a consuming layer (engine.news / a future advisor screen)
// from this package's identity half plus a separate engine.traffic call;
// this package computes and exposes the capacity delta only (see
// [RoadsAPI.PreviewCapacityDelta]).
//
// # Upgrade compatibility rule (AC-4 — provisional pending Aaron)
//
// §51 says "any road converts to any compatible type" without defining
// "compatible". The escalation (step-through-adjacent-rungs vs any-to-any
// with cost scaling) is Aaron's decision, not the junior's. The provisional
// rule implemented here — and the one [RoadsAPI.ApplyUpgradeCommand]
// documents and enforces — is ANY-TO-ANY: every distinct class is a
// compatible upgrade target, and the rung distance is priced rather than
// gated (cost = delta + rebuild disruption + rung-distance scaling, all
// data-driven from data/roads.json's "upgrade" block). This is logged as
// ASM-1451 and is deliberately trivial to flip to
// step-through-adjacent (a one-line predicate change in applyUpgrade)
// once Aaron rules. The one transition ALWAYS rejected is same-class
// (a no-op, returned idempotently) and an invalid class (rejected).
//
// # Civic-building naming eligibility (AC-10 — provisional pending Aaron)
//
// §20 says civic buildings are "named for notable deceased citizens ... or
// toponym + type" without an eligibility algorithm for "notable". The
// eligibility/ranking rule is escalated to Aaron; this package implements
// the DETERMINISTIC TOPONYM+TYPE fallback only (a Kentish toponym + a
// civic-type word, both drawn from seed+id via the counter-RNG). The
// "notable deceased citizen" half is out of scope here — the underlying
// mortality/tenure data belongs to engine.citizens, and ranking it is the
// escalated decision. Logged as ASM-1452.
//
// # Tie-break rules (AC-16)
//
// Every selection that could be read as a tie is resolved by the
// counter-RNG from foundation.det (keyed (worldSeed, id, purpose)), never
// by Go's undefined map/slice iteration order and never by an unseeded
// math/rand. There is in fact no "tie" to break: the corpus/suffix index is
// COMPUTED (stream.IntN modulo the list length), not chosen among equal
// candidates, so the same (seed, id) always yields the same index. Where
// two candidates could be equal (e.g. two place names), the deterministic
// index is the rule. Any output that enumerates the road/node set (a
// snapshot, a determinism hash, a listing) sorts by ascending ID first.
//
// # Determinism (AC-14/AC-15/AC-17)
//
// Naming, upgrade cost, maintenance decay and the current-capacity query
// are pure functions of prior state + commands + the simulation month
// index. No `for range` over a Go map feeds a result-affecting decision
// without a prior sort (AC-15); no wall clock is read anywhere in this
// package (AC-17 — maintenance decay and roadworks are functions of the
// simulation tick/month only, and the "summer" roadworks window is a
// month-index calendar predicate, not a real-world date check).
//
// # Route cache placement (AC-20)
//
// The warm-start route cache lives in engine.traffic, not here. This
// package holds no route data of any kind.
package roads
