// Package comms is the communications, internet & e-commerce module
// (MOD-048): the §35 connectivity-era ladder, its per-capability gates, the
// remote-work share ("a traffic tool disguised as telecoms"), the
// letters-vs-parcels trend, the e-commerce retail-share coefficient and its
// fulfilment-centre/last-mile-depot infrastructure requirement, the
// high-street-drain/counterplay pair, and the post-and-parcel service drawn
// from engine.services' generic pool.
//
// Module key: engine.comms (see code.json)
// GUID:        f5f764ed-ee57-41bf-96d4-ccc82dda44ac
// Spec refs:   §35 (Communications, Internet & E-commerce — the six eras of
// connectivity: telephone exchange → dial-up → broadband hub → fibre
// backbone → cellular masts (2G→5G coverage overlay) → submarine cable
// landing station; internet quality gates office tiers, data centres,
// university research rate, and remote-work share; letters decline by era
// vs parcels grow with wealth/era/e-commerce share; e-commerce share shifts
// retail demand online, requires a fulfilment centre and last-mile depots,
// and drains the high street unless counterplayed); §45 (Firms, for the
// fulfilment-centre-as-firm contract); §41 (counterplay: entertainment
// zoning, markets, pedestrianisation, café culture).
//
// # The four-gate independence contract (AC-3)
//
// The four documented capability gates — office-tier ceiling, data-centre
// eligibility, university research-rate modifier, and remote-work-share
// coefficient — are FOUR INDEPENDENT per-era values, each with its own
// era→value mapping in data/comms.json. They are deliberately NOT a single
// blended "internet quality" scalar multiplied by four fixed weights: data-
// centre eligibility is a step function that flips true at fibre and stays
// true, while the research-rate modifier and remote-work base rise
// continuously across every era, and the office-tier ceiling steps up on its
// own schedule. TestCapabilityGateIndependence asserts at least two gates
// move by DIFFERENT amounts across one era transition.
//
// # The fulfilment-centre-as-firm contract (AC-7)
//
// A fulfilment centre is NOT a comms-owned pseudo-employer with an internal
// jobs counter. [CommsAPI.RegisterFulfilmentCentre] registers it as a real
// firm through engine.firms' [firms.FirmsAPI.RegisterFirm] — thousands of
// jobs (data/comms.json's fulfilment.staff) at a real premises zone class —
// and the resulting firm is queryable through engine.firms' FirmsAPI, not
// through any comms-owned roster. Last-mile depots are registered the same
// way ([CommsAPI.RegisterLastMileDepot]).
//
// # Outstanding BUG-058 edges (do NOT assume these are wired)
//
// Two code.json edges required by §35 do not exist yet (BUG-058), and this
// package deliberately does NOT fake them:
//
//  1. engine.traffic → engine.comms: traffic has no registered inbound edge
//     to read [CommsAPI.RemoteWorkShare] back, so remote-work share is a real,
//     independently-queryable value here but is NOT yet consumed by traffic's
//     trip generation. Wiring it is engine.traffic's later obligation.
//  2. engine.comms → (the high-street vacancy/blight owner): no edge exists
//     from this package to whichever module owns high-street retail vacancy/
//     blight state, so [CommsAPI.HighStreetDrain]/[CommsAPI.NetHighStreetDrain]
//     are real, queryable coefficients but are NOT yet consumed by a blight
//     consumer. That consumer's edge is out of scope until BUG-058 lands.
//
// # Determinism (AC-12)
//
// Era-gate resolution, e-commerce share computation, and remote-work-share
// computation are pure functions of prior state + commands. This package has
// no stochastic processes, so it needs no world seed and reads no wall clock
// (the time.Time accessor scan over non-test files returns no matches —
// AC-13); repeated runs from identical starting state and command sequence
// produce byte-identical era state, gate values, and coefficients across
// worker counts. The only state that changes is mutated through the exported
// command surface, always under mu, and every map-derived result is built in
// a fixed order (GR#21).
package comms
