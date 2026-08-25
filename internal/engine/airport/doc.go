// Package airport is the Heathrow-class international airport module
// (MOD-075): the §MP mega-node with its own runways, terminals and freight
// apron, feeding tourism (MOD-057), FDI (MOD-059) and freight (MOD-047), and
// blighting its surroundings through engine.mining's general blight model.
//
// Feature key: feat.airport (FEAT-096)
// Module key:  engine.airport (see code.json)
// GUID:        71f98f6a-8f9f-4b3d-bbad-f2d8b8e783b0
// Spec refs:   §MP (Heathrow-class International Airport — M11 + regional
//
//	airport achievement, 5B capex, 4 runways, ~200k pax/day, huge noise
//	contour + its own motorway/rail spurs); §44 Holiday Tourism (airport
//	tiers step-change reach: domestic → continental → global); §46 FDI
//	(aerospace campus needs airport adjacency + runway access); §33 The
//	Freight Harbour (the tonnes-conservation identity the air-cargo arm
//	joins); §32 The Blight Model (the airport is one of the seven named
//	blighting-object classes); §18 Wellbeing (noise as a mental-health
//	driver).
//
// # What this package does
//
//   - The airport is a multi-component node (§MP "4 runways", AC-1/AC-2):
//     [AirportAPI.RunwayCount] (runways), [AirportAPI.TerminalCapacity]
//     (terminal gates × pax/gate/day), and [AirportAPI.FreightApronCapacity]
//     (air-cargo t/day) are distinct components with distinct capacities.
//   - [AirportAPI.PassengerThroughput] is a documented function of those
//     components — the binding constraint of (runways × per-runway pax/day)
//     and (terminal gates × per-gate pax/day) — never a single paxPerDay
//     constant (AC-2). Adding a runway or a terminal moves throughput.
//   - [AirportAPI.AccessTier]/[AirportAPI.ReachMultiplier] expose the §44
//     access-tier step-change (domestic → continental → global) as a reach
//     multiplier that rises with the tier — the input fed to engine.tourism
//     (AC-5). See "Feed edges" below.
//   - [AirportAPI.RunwayAccess] exposes the §46 runway-access/adjacency query
//     the aerospace-campus prospect tests against (AC-6).
//   - [AirportAPI.AirCargo] hands air-cargo tonnage into engine.freight's
//     conserved-tonnes identity as one more modal arm (AC-4) — there is no
//     airport-local air-cargo tonnage ledger.
//   - The airport's noise contour is registered with engine.mining's BlightAPI
//     as a blighting object (AC-7) — this package never computes its own
//     noise/viewshed effect.
//   - [AirportAPI.Build] is permit-gated (feat.facilitypermits) and land-hungry
//     (a data-sourced footprint), and full throughput is gated on the road/rail
//     surface-access spurs (AC-8/AC-9).

// # Balance-number regime (GR#15)
//
// EVERY numeric figure this package consumes — runway counts, per-runway pax
// rates, terminal gate counts, per-gate pax rates, the reach multiplier, the
// freight-apron t/day, the blight class, the noise-contour radius, the noise
// level, the land footprint, the jobs count, the rail-spur requirement, and
// the surface-access-reduced throughput percentage — is a PLACEHOLDER in
// data/airport.json, each carrying a non-empty disclosure naming it pending
// Aaron's balance pass (AC-14/AC-15). No AC is satisfied by a junior-invented
// final figure; tests check direction/structure only (more runways ⇒ more pax;
// a tiered airport out-reaches a regional one; without surface access
// throughput degrades), never a pinned magnitude.

// # Feed edges and their direction (airport-into-consumer, ASM-667)
//
// This module is a PRODUCER of three feed edges, all airport-into-consumer:
//
//   - engine.airport → engine.tourism (MOD-057): the §44 access-tier reach
//     figure ([AirportAPI.AccessTier]/[AirportAPI.ReachMultiplier]) is the
//     tourism-draw multiplier input. The edge is queryable locally here; the
//     cross-module injection is the composition root's job once engine.tourism
//     lands.
//   - engine.airport → engine.fdi (MOD-059): the §46 runway-access/adjacency
//     query ([AirportAPI.RunwayAccess]) satisfies the aerospace-campus
//     requirement sheet.
//   - engine.airport → engine.freight (MOD-047, done): the §33 air-cargo
//     tonnage ([AirportAPI.AirCargo]) enters engine.freight's conserved-tonnes
//     identity through the [AirCargoMover] seam — air is a new modal arm
//     freight's road/rail/sea modal caps do not yet model, so the handoff is
//     consumer-driven dependency inversion, exactly as feat.containerport
//     hands sea↔rail↔road transfers through its RailIntermodal seam.

// # Blight reuse commitment (AC-7)
//
// The airport is one of §32's seven named blighting-object classes ("applies
// to mines, heavy industry, abattoir, incinerator, landfill, airport,
// motorway"). It registers as a blighting object through engine.mining's
// BlightAPI — consumed via the [BlightRegistrar] dependency-inversion seam —
// never by computing its own noise/viewshed effect. The elevation-aware
// viewshed/noise machinery is engine.mining's (MOD-046), not this package's.
//
// The seam is a single-method atomic upsert (SEC-141): the airport registers
// under one stable object key (blightObjectKey) for its whole life, and an
// upgrade re-registers that key with the new tier's class/radius in one call —
// a re-register is a replace, never a duplicate-key error. The composition root
// wiring engine.mining's BlightAPI must satisfy exactly this contract, because
// Build performs no deregister-then-register sequence and relies on the upsert
// to keep the registrar from ever holding a half-updated contour (AC-10).

// # Surface-access burden (AC-8)
//
// §MP names the airport's "own motorway/rail spurs" as a prerequisite. Full
// throughput requires those links: without them the airport runs at a
// materially reduced, data-driven percentage (surfaceAccessReducedPct in
// data/airport.json). This package only READS whether the links exist via the
// [SurfaceAccess] seam — the construction of the road/rail spurs is
// engine.roads' (MOD-024) and engine.rail's (MOD-060) own job, never
// reimplemented here.

// # Permit and land gate (AC-9)
//
// Building the airport is permit-gated through feat.facilitypermits (FEAT-053,
// the §7 permit "for ANY large facility") via the [PermitAuthority] seam, and
// requires an enormous, data-sourced land footprint (landFootprintHectares).
// Neither the three-route permit gate nor the put-back-to-nature decommission
// liability is reimplemented here — no permit-state or liability-provision
// field lives on [AirportAPI].

// # Determinism (AC-12)
//
// Throughput computation, air-cargo handoff, access-tier derivation and
// surface-access gating are pure functions of (tick, prior state, loaded data,
// commands) — no wall-clock read (the wall-clock accessor scan over this
// package's non-test files returns no matches), and every tier iteration that
// feeds a result is fixed at load time (GR#21). Re-running the same command
// sequence from identical state yields byte-identical output across worker
// counts.
//
// # Loading, data, and errors (GR#7/GR#15)
//
// [Load] reads data/airport.json through a self-contained loader (the
// engine.freight/feat.containerport pattern). Every balance figure lives in
// that data file, never as a Go literal here (GR#15). Every failure is a
// registry-sourced *errs.E (MET-G28xx, this module's claimed sub-range — see
// errors.go).
//
// # Locking and blight-registration decisions (FEAT-084 ASM folds)
//
// SEC-119 (ASM-1191): [Airport] serializes Build with a dedicated buildMu
// sync.Mutex so a.mu is never held across a seam callback; a seam that
// re-enters Build itself (rather than Tick/reads) would deadlock on buildMu and
// is out of scope. SEC-116 (ASM-1188): the BlightRegistrar seam is extended
// with DeregisterBlightingObject and de-registers the PRIOR tier's contour
// BEFORE registering the new one, keeping per-tier object keys rather than a
// stable key. ASM-1265: blightObjectKey is a hardcoded Go constant "airport" —
// an identity key, not a balance figure, so GR#15 does not require it in
// data/airport.json.
package airport
