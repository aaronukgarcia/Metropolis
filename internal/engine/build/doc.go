// Package build is the zoning & construction module (engine.build,
// MOD-026): the §34 eight-way zone catalogue (Dwelling, Shop, Office,
// Entertainment, Farming, Manufacturing, Heavy Industry, Mining), the
// §7 land-ownership gate, the §13-F3 build queue (construction materials
// + labour + lead time, with §9's winter construction slowdown), and
// demolition with compensation — all behind a single BuildAPI.
//
// Module key: engine.build (see code.json; inbound GUID
// 6fbc1a41-4d37-4ed5-81dc-3ae5e0ffa0a4 "BuildAPI", outbound GUID
// 7e791c72-d564-4504-b921-33adb1d486c9)
// Spec refs:  §7 (Economy, Land & Finance — "player buys before building");
// §34 (Zoning — Land Types, the 8-way catalogue and its self-explaining
// demand bars); §13-F3 (Land & Construction — purchase, zone, build
// queue, demolition); §9 (Seasonality — construction speed, winter
// slowdown).
//
// # Contract: build orders are commands, the queue is visible (AC-1)
//
// The registered inbound contract is "build orders are commands; queue
// visible; catalogue-driven from buildings.json". Consumers reach the
// build state ONLY through *BuildAPI's exported methods — SubmitZoneCommand,
// SubmitBuildCommand, SubmitDemolishCommand (commands), Queue / Demand /
// ZoneTypes (read-only queries). The internal zone map and build queue
// are unexported types (zoneState / queue), so no consumer can write a
// cell's zone or an order's state by reaching into build internals —
// there is no exported setter on either.
//
// # Ownership gate (AC-3)
//
// engine.build enforces §7's "player buys before building" at the point
// of command acceptance, not as a documented precondition callers must
// uphold themselves: SubmitZoneCommand / SubmitBuildCommand /
// SubmitDemolishCommand each ask engine.world (via WorldAPI.TileAt)
// whether the issuing owner owns the target tile, and reject with the
// registry-sourced ErrCellNotOwned — mutating no zone/queue state — before
// touching any build state. The purchase transaction itself (money moving
// + ownership marking) is engine.finance's / engine.world's job (ASM-236);
// this module only enforces the gate.
//
// # LandPrice is consumed, never re-implemented (AC-9)
//
// engine.build does not compute §7's land-price formula. PurchasePrice and
// SubmitDemolishCommand's compensation read engine.finance's
// finance.LandPrice (its registered AC-4) — mapping a cell's terrain into
// finance's own LandCell vocabulary — and never carry a local
// base/access/amenity/scarcity implementation. The four component
// functions live in engine.finance only.
//
// # engine.season dependency (AC-6, RESOLVED)
//
// engine.build's winter construction slowdown is sourced from engine.season's
// SeasonAPI.ConstructionSpeedMultiplier via a live call at build-order
// submission — the code.json engine.build → engine.season edge landed in
// commit c36778b, so this is a normal, wired dependency, not a pending
// block (the former BUG-058/ASM-233 gap is closed). There is no
// build-local seasonal curve: moving the winter multiplier is a
// data/seasonal.json edit, never a code change in this package.
//
// # engine.services dependency (edge engine.build->engine.services, FEAT-build-services-bridge-2026-09-02)
//
// A completed building whose catalogue entry (data/buildings.json)
// declares a non-empty serviceKind registers with engine.services via
// ServicesAPI.RegisterService, wired through SetServices exactly like the
// world/season/logistics dependencies above — so the service->wellbeing->
// migration chain responds to player building instead of reading a
// capacity that never moves once the composition root has wired
// SetServices. The registration happens inside Tick's completion step
// (build.go), BEFORE world.SetStructure lands the structure: a
// registration failure (services not wired, an unregistered service kind,
// or a non-finite location/coverage-radius input) leaves the order
// incomplete and nothing lands on the map — a deliberate resolution of the
// spec's AC-2 or AC-8 ordering tension in AC-8's favour (never land a
// structure whose service failed to register), documented on the
// registration call site. A building with no declared serviceKind (or no
// BuildCommand.BuildingID at all — a plain §34 zone order) completes
// exactly as before and registers nothing (AC-7). Demolition is the
// mirror: SubmitDemolishCommand calls ServicesAPI.UnregisterService for a
// tracked structure, so a demolished service's capacity/coverage
// contribution disappears from the next CoverageSummary read.
//
// # Completed-building identity and discovery (BUG-734)
//
// A completed order's BuildOrderID IS the completed structure's
// deterministic identity: minted at SubmitBuildCommand time from the
// monotonic nextOrder counter (never wall-clock, never a map key), and that
// counter round-trips through this package's own save.Participant
// (participant.go), so a structure keeps the same ID across a save/restore
// and an id issued after a load never collides with one issued before it.
// BuildOrder.BuildingID (exported on the Queue()/CompletedBuildings()
// snapshot) names WHICH data/buildings.json catalogue entry a completed
// order built, so a consumer knows both "which structure" (ID) and "what it
// is" (BuildingID) without reaching into build internals. CompletedBuildings
// is the cursor-based discovery query — mirroring engine.deathservices'
// DeathHandoffSince idiom — a consumer (the composition root) uses to find
// newly-completed named buildings without diffing two Queue() calls itself;
// see its doc comment for the cursor contract. This is the missing half of
// FEAT-build-services-bridge-2026-09-02's own contract: that bridge already
// lets a serviceKind-declaring building register with engine.services at
// completion, but until BUG-734 nothing outside this package could discover
// a completion at all for modules (engine.deathservices' cemeteries/
// crematoria) that are NOT engine.services consumers.
//
// # Numeric safety (GR#16, FEAT-086)
//
// Every Money/int64 quantity in this package — materials quantities,
// labour, lead times, and compensation — routes through foundation/num's
// saturating arithmetic (num.SatAdd/num.SatSub/num.SafeMul) and every
// int64↔float64 conversion (the seasonal multiplier applied to lead time)
// routes through num.ClampInt64FromFloat (see numeric.go's
// effectiveLeadTime). A MaxInt64 / MinInt64 / mixed-sign input cannot wrap
// negative, produce +Inf/NaN, or invent/destroy units. Numeric inputs are
// validated at every entry point: constructor, mutator, and query.
//
// # Determinism (AC-12, AC-13)
//
// The build-queue tick is a pure function of prior tick state, the loaded
// catalogue, and command inputs: it iterates the queue in insertion (order
// ID) order — never map order — and the materials draw is engine.logistics's
// own deterministic Draw. No non-test file reads the wall clock; lead time
// and seasonal slowdown are functions of the simulation month index only.
package build
