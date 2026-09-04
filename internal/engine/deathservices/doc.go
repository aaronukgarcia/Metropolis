// Package deathservices implements MOD-083 (mkey engine.deathservices)
// increment 1: graveyards (space+reuse), cremation, hearses, and emergency
// dispensation -- Aaron's question "when do we see crematoriums and
// hearses?" answered in engine code.
//
// # Spec refs
//
// Part IV structure catalogue (docs/METROPOLIS-MASTER-v2.1.md lines
// 1075-1079: Cemetery M2 300k/2k plots "fills permanently -- land
// pressure"; Crematorium M5+DP 3M 12/d; Memorial woodland M6+DP 1M 5k,
// deferred to inc2+); the regional-contract table's line 543
// ("Crematorium/cemetery | £/service"); §5 mortality; §26 Emergency & Care
// Dispatch; §25 Refuse & waste-health loop (the structural analog for
// collection-round logistics this module's hearse.go mirrors); §9
// Seasonality (the weather curves that drive FEAT-087's emergency signal,
// read here only through the RealisedDeath.EmergencyFlag field FEAT-087
// already stamps -- this module never recalculates weather, GR#3).
//
// # Upstream: FEAT-087 feat.deathwave
//
// This module's entire intake surface is FEAT-087's RealisedDeath handoff
// (citizens.CitizensAPI.DeathHandoff/DeathHandoffSince): {CitizenID,
// DeathMonth, EmergencyFlag} records, one per realised death, in FIFO
// release order. FEAT-087 owns mortality smoothing, weather-event
// detection, and queue ordering; this module owns body triage/disposal
// exclusively from that point forward (api.go's Intake).
//
// # Registered edges (code.json, GR#20/GR#25)
//
// engine.deathservices (seq 605) has exactly ten outbound edges:
// engine.citizens, feat.deathwave, engine.services, engine.world,
// engine.build, engine.logistics, engine.market, foundation.data,
// foundation.errors, foundation.num (the last two additions landed via
// the round-3 SSOT registrations, commits 997bba5 and 6a4e210 -- the
// engine.market edge because every exported LogisticsAPI throughput
// method takes a market.CommodityType, making the already-granted
// engine.logistics edge unconsumable without it, exactly the pair
// engine.refuse carries as precedent). Every cross-module call this
// package makes goes through one of those ten -- there is no
// hand-invented dependency (in particular, NOT engine.finance and NOT
// engine.season; costs are plain int64 micro-pounds, and the weather
// signal arrives pre-computed on RealisedDeath rather than being
// re-derived). foundation.num is registered but currently unconsumed --
// config.go's local isFinite predates the registration and can be
// swapped to num.IsFinite at leisure.
//
// # The six-term conservation identity (AC-14)
//
//	BodiesReleased == BodiesAwaitingHandling
//	                + BodiesEnRoute
//	                + BodiesBuried (lifetime-terminal, occupying-or-formerly-
//	                  occupying a plot -- see cemetery.go's plot doc for why
//	                  this is a lifetime classification, not a live
//	                  occupancy count)
//	                + BodiesCremated
//	                + BodiesHandledByDispensation
//
// Every right-hand term is independently sourced (conservation.go's
// Snapshot walks every body's CURRENT state exactly once; BodiesReleased is
// its own counter, incremented only by Intake) and the identity is
// CHECKED, never constructed by subtraction -- see conservation.go's own
// doc for why that distinction matters (AC-14's false-pass-risk note).
//
// # Terminal exclusivity (AC-15)
//
// Burial, cremation, and dispensation are the three mutually exclusive
// terminal BodyStates (BodyBuried/BodyCremated/BodyDispensed): a body
// transitions Awaiting -> (optionally EnRoute) -> exactly one terminal
// state, once, forever. Bury/Cremate/Dispense all reject a body already in
// a terminal state with ErrBodyAlreadyHandled rather than silently
// re-disposing it.
//
// # The plot-reuse contract (AC-3)
//
// A cemetery's plot capacity is fixed at registration (data-sourced, spec
// seed 2k, cemetery.go's RegisterCemetery). A consumed plot re-enters the
// allocatable pool only once month-buriedMonth >= the data-sourced
// plotReuseHorizonMonths (a disclosed placeholder, MOD-083-inc1.md
// assumption 1) -- cemetery.go's findAllocatablePlotLocked prefers a
// never-used plot first, then the OLDEST reuse-eligible occupied plot
// (deterministic tie-break on buriedMonth then bodyID, GR#21). When
// neither exists, Bury returns ErrNoPlotAvailable and changes nothing
// (AC-4's "fills permanently -- land pressure" saturation triage) -- the
// documented fallback is the caller routing the body to Cremate or
// Dispense instead.
//
// # The one-body-per-trip cap (AC-7/AC-8/AC-9)
//
// hearse.go's HearseTripCapacity is always 1 -- the structural definition
// of a hearse, as opposed to dispensation.go's multi-body vans/trucks.
// RunHearseTransport is bounded by BOTH the data-sourced monthly hearse
// budget (a coarse, month-level aggregate per AC-9 -- never per-vehicle
// routing or sub-tick scheduling, which is deferred to inc2) AND, when a
// LogisticsAPI is wired, engine.logistics' Deliverable throughput for the
// destination (AC-8 -- the same junction-saturation congestion any
// logistics round faces, expressed via the shared market.Waste commodity
// exactly as internal/engine/refuse/round.go's deliverToSite does for
// refuse collection; RESTORED in round 3 once the engine.market SSOT edge
// landed in 6a4e210, after round 2 had removed it as then-unregistered --
// see hearse.go's header for the full edge history). A congested
// logistics state measurably reduces a month's hearse throughput below
// the budget (TestHearseCongestionDelaysTrips). Bodies beyond either
// bound stay Awaiting -- the unhandled-body backlog persists across
// months rather than draining in one call, which is what keeps a death
// surge a real backlog rather than an instantly-absorbed blip.
//
// # The dispensation activation/reversion gate (AC-10/AC-12)
//
// Dispensation activates when a non-empty Intake batch carries at least
// one RealisedDeath with EmergencyFlag true (or via the explicit
// SetDispensationActive setter, for a composition-root caller that
// already has FEAT-087's live signal in hand) -- reading the SAME signal
// FEAT-087 stamps, never a local weather recalculation. Dispense's
// len(bodyIDs) > 1 while inactive is rejected outright with
// ErrMultiBodyOutsideDispensation and makes no state change, which is what
// makes reversion at event end a structural guarantee (AC-12) rather than
// a caller convention.
//
// # inc1 simplifications (documented, not silent)
//
//   - The "en route" interval is not separately ticked: RunHearseTransport
//     moves a body directly from Awaiting to Buried within one call, so
//     BodyEnRoute/BodiesEnRoute is a legitimate always-zero term at this
//     increment's depth (a future increment that models a real transit
//     delay would populate it).
//   - Cremation does not consume a hearse trip in inc1 -- it is bounded
//     solely by the crematorium's own daily throughput (AC-5), independent
//     of the hearse/logistics budget.
//   - Memorial woodland (line 1077, a third disposal option), UI
//     screens/status bubbles/backlog indicators, regional-contract capacity
//     export, and real per-vehicle hearse routing are all OUT OF SCOPE for
//     increment 1 (see docs/planning/acceptance/MOD-083-inc1.md's "Out of
//     scope" section).
//
// # Determinism (AC-18/AC-19/AC-20/GR#21)
//
// Every ordering decision (the awaiting FIFO, plot allocation tie-break,
// crematorium/hearse processing order) sorts explicitly by
// (deathMonth/buriedMonth, citizenID) rather than relying on map iteration
// order. This package never reads the wall clock -- reuse horizon, hearse/
// dispensation throughput windows, and crematorium daily counters are all
// functions of the caller-supplied simulation month/day index alone. Every mutable field is guarded by
// DeathServicesAPI.mu, and every exported method rejects a call on a
// struct-copied receiver via checkNotCopied (SEC-020 family).
package deathservices
