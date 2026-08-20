// Package airunits is the rotary-wing air-units module (engine.airunits,
// MOD-074): four distinct chopper unit types — police, fire, ambulance, and
// VIP/commercial — that are EXPENSIVE, SKILLED, MAINTENANCE-HEAVY, EFFECTIVE,
// POPULAR, and TRAFFIC-IMMUNE.
//
// Module key: engine.airunits (see code.json; inbound GUID
// b9658c5f-1d3b-4830-acd3-f3c6f919e66f "AirUnitsAPI", outbound GUID
// bba25b12-a2ef-404a-96b4-de6dc739ba49). Spec refs: §26 Emergency & Care
// Dispatch Model (the air ambulance ignores roads — weather-limited, 1 unit ≈
// 10 ground units of marginal coverage in congested eras, the anti-gridlock
// asset), §10 Service & Feature Inventory (the air-ambulance pad: M8+DP, 4M,
// 1 unit, beats traffic), §28 Crime, Policing & Security (concave deterrence
// in patrol coverage — the police-chopper coverage-extension precedent), and
// §54 The Fiscal Circuit (the Public Service Pie staffing targets — the
// approval/staffing tie-in).
//
// # Four distinct roles, four distinct effects (AC-1, AC-8)
//
// The four roles are NOT one generic chopper carrying a role string: each is a
// distinct [UnitType] constant resolving to a distinct [EffectKind] and a
// distinct, data-driven effect parameter (see [RoleEffect]):
//
//   - police → coverage-radius extension (deterrence/response radius)
//   - fire → remote/blaze reach bonus (fire-spread/block-loss reduction)
//   - ambulance → hospital-landing-time reduction (simulation-minutes)
//   - VIP/commercial → commercial revenue per month (a prestige/commercial
//     asset, NOT an incident responder — ASM-586)
//
// The four types are enumerated by the package-level [UnitTypes] registry,
// never ranged over via a map in map order (GR#21).
//
// # Balance-number regime (AC-16, AC-17)
//
// Every numeric figure this package consumes — purchase cost, the four
// running-cost components (fuel, hangar, insurance, crew), pilot/crew cost,
// maintenance engineer-hours, the response-time delta, the coverage radius,
// the commercial revenue, and the approval weight — is loaded from
// data/helicopters.json and is a PLACEHOLDER pending Aaron's row-by-row
// balance pass. No magnitude is a Go literal, and tests assert DIRECTION and
// STRUCTURE (a chopper costs more than a ground unit; a flying chopper costs
// more than a grounded one; two roles differ; the four effects are
// distinguishable), never a specific figure.
//
// # Traffic-immune, weather-limited (AC-7)
//
// A chopper's travel time is computed by a distinct air path
// ([AirUnitsAPI.AirTravelTimeMinutes]) that is independent of road congestion,
// never the ground blue-light pathfinder. An adverse-weather state (wind speed
// at or above the data-loaded grounding threshold, read through the injected
// world/weather seam per ASM-589) grounds or degrades air dispatch
// ([AirUnitsAPI.WeatherGrounded]).
//
// # Pilot-supply boundary (AC-5)
//
// "No pilot, no flight" is mechanical: a chopper enters a flying/dispatchable
// state only with a trained pilot assigned, and removing the pilot grounds it.
// The pilot SUPPLY (how citizens train to be pilots, the skill-pool size) is
// MOD-073's (engine.staffing) — this package only consumes an injected pilot
// assignment through the staffing seam ([StaffingSeam]), never trains a pilot.
//
// # Maintenance-demand boundary (AC-6)
//
// Each chopper carries a significant engineer-hour burden: flying hours
// accumulate wear, and un-serviced wear transitions the chopper to
// out-of-service. The engineer-hour cost surfaces through MOD-072's
// (engine.maintenance) demand surface ([MaintenanceSeam]) — never a
// chopper-local maintenance ledger.
//
// # Registered-edge honesty (AC-2)
//
// This package imports NO sibling engine package. dispatch, finance, staffing,
// maintenance, and the world weather surface are all consumed through the
// interface seams declared in types.go ([DispatchSeam], [FinanceSeam],
// [StaffingSeam], [MaintenanceSeam], [WorldSeam]) and injected by the
// composition root — so each call already has a code.json outbound edge, and
// no raw sibling-engine package import exists here (GR#20).
//
// # Determinism & numeric safety (AC-13, AC-14, GR#16, GR#21)
//
// Nothing in this package reads the wall clock, and there is no shared or
// global RNG source. Chopper state advances only by the simulation tick
// ([AirUnitsAPI.AdvanceMonth]); every fleet iteration is in sorted UnitID
// order (never map range), so the tick is a deterministic function of
// (worldSeed, tick, prior state, commands). The only stochastic draw — the
// VIP commercial-revenue roll — uses a counter-based stream via
// det.NewStream(worldSeed, unitID, month, purpose). All money is int64
// micro-pounds ([det.Micropounds]) and all quantity arithmetic routes through
// foundation/num's saturating helpers (SatAdd/SatSub/SafeMul), never float and
// never a wrapping int.
package airunits
