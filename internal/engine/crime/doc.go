// Package crime is the crime, policing, gangs, security-services and
// justice-chain module (engine.crime, MOD-042): §28 "Crime, Policing &
// Security" plus the Security & Justice additions supplement (Constabulary
// HQ, Security Service liaison, customs house, youth centre, probation
// office, reception & processing centre, ESOL).
//
// Module key: engine.crime (see code.json; inbound GUID
// ec4eb047-5101-496f-b9c4-760e21846954 "CrimeAPI", outbound GUID
// 24cdc0fa-0cc2-45c2-9c45-4cd3778ab136). Spec ref: §28 (per-district,
// per-type monthly generation from isolable drivers; the concave-deterrence
// + detective-clearance + prevention triad; the gang lifecycle; the
// command ladder; the MI5-analogue threat dial; the justice chain).
//
// # Generation, decomposed and drill-through (AC-1/AC-2/AC-3)
//
// Nine crime types (petty theft, burglary, vehicle crime, criminal damage,
// violent crime, drugs supply, organised crime, fraud/cyber, smuggling) are
// tracked as nine DISTINCT per-district sub-figures, never one blended
// index split for display. Each type responds only to the §28 drivers it is
// tied to (see the [typeDrivers] mapping): smuggling scales with port/harbour
// throughput against customs funding and nothing else; fraud/cyber scales
// with era/wealth; and only burglary/violent respond to the inter-district
// inequality driver. The inequality driver is a genuine neighbour
// comparison — the gap between this district's own deprivation and its
// adjacent districts' wealth — never a synonym for this district's own
// poverty. Deterrence (concave in patrol coverage), clearance (detective
// persistence suppression) and prevention (generation reduction) are three
// separately-drillable contributions ([CrimeAPI.Deterrence],
// [CrimeAPI.Clearance], [CrimeAPI.Prevention]).
//
// # Gang lifecycle (AC-6/AC-7/AC-8/AC-9)
//
// A gang is a stateful entity with a real negative direction. FORMATION:
// a named, tracked gang forms only after high youth unemployment + blight +
// low clearance hold SIMULTANEOUSLY for more than the data-loaded
// consecutive-month run (24 in crime.json) — not "ever peaked within the
// trailing window". A district that relaxes one condition in the final
// month does NOT form a gang. A formed gang claims a queryable territory,
// raises every local crime type (not only organised crime), levies a
// queryable business tax that raises closures, and recruits from the
// matching demographic, reducing the eligible pool the generation drivers
// read from. REMOVAL: gang strength trends toward the removed state ONLY
// when clearance pressure, prison absorption, regeneration investment and
// youth provision are ALL concurrently above their thresholds — clearance
// pressure alone, at any magnitude, does not remove a gang. DECAPITATION
// ASYMMETRY: a decapitation issued WITHOUT concurrent regeneration
// investment deletes the entity but leaves the generative conditions
// standing, so a fresh gang (a new id) re-forms within the respawn window;
// the identical decapitation WITH regeneration investment breaks the
// formation run and does not respawn.
//
// # Justice chain conservation (AC-12/AC-13)
//
// The justice chain is a pipeline of identifiable people (Option B): every
// offender is a stable uint64 id that flows arrest → charge → trial →
// sentence, and a conservation identity holds at every stage, per month,
// per district (and therefore in city aggregate):
//
//	OffendersArrested      == OffendersCharged + OffendersReleasedNoCharge
//	OffendersCharged       == OffendersConvicted + OffendersAcquitted + OffendersAwaitingTrial
//	OffendersConvicted     == OffendersSentencedToPrison + OffendersSentencedNonCustodial
//
// OffendersAwaitingTrial is the charged-this-month overflow into the
// courthouse's backlog stock (carried to next month), read from the
// courthouse's own throughput overflow — never a remainder computed to make
// the identity balance. Each term is the count of that stage's own log.
// OffendersSentencedToPrison is cross-checked against engine.prison's
// INDEPENDENTLY-tracked intake ledger through the [PrisonIntake] seam
// ([CrimeAPI.VerifyPrisonIntake]) — not trusted as this module's own say-so.
// The courthouse backlog, once it exceeds the data-loaded threshold, is
// released on bail/lapsed charge as a distinct OffendersReleasedOnBacklog
// outcome, which measurably softens the low-clearance driver the gang
// formation condition reads (AC-13).
//
// # Registered-edge honesty (US-6)
//
// This package consumes NO unregistered module call. The four registered
// outbound edges (engine.citizens, engine.services, engine.wellbeing,
// engine.spiral) are the intended ultimate sources of the per-district
// driver values, but their district-level driver surfaces are not all live
// on a registered call today (engine.wellbeing has not landed), so those
// values arrive as pushed input ([DistrictInput]) from the composition
// root. Two named §28 effects are explicitly NOT reachable and are left as
// locally-queryable values pending edges: the conservation registration
// with engine.invariant (no edge — its inbound is populated by
// engine.citizens/engine.finance/engine.traffic only), and the insurance
// costs on firms / gang business-tax effect feeding engine.market /
// engine.firms (no edge — the levy stays a locally-queryable value). There
// is no engine.crime→engine.invariant or engine.crime→engine.market /
// engine.firms call in this package.
//
// # Determinism & numeric safety (AC-16/AC-17, GR#16/GR#21)
//
// Nothing in this package reads the wall clock, and there is no shared or
// global RNG source: every stochastic draw (charge/trial/sentence coins,
// gang names/territory, threat-event rolls) uses the counter-based
// hash(worldSeed, id, month, purposeTag) stream. Every numeric input is
// validated at every entry point, and every float64↔int64 conversion routes
// through a clamping choke point.
package crime
