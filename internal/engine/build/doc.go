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
// # Numeric safety (GR#16, FEAT-086)
//
// Every Money/int64 quantity in this package — materials quantities,
// labour, lead times, and compensation — routes through saturating
// arithmetic (satAdd/satSub/safeMul in numeric.go) and every
// int64↔float64 conversion (the seasonal multiplier applied to lead time)
// routes through clampInt64FromFloat. A MaxInt64 / MinInt64 / mixed-sign
// input cannot wrap negative, produce +Inf/NaN, or invent/destroy units.
// Numeric inputs are validated at every entry point: constructor, mutator,
// and query.
//
// # Determinism (AC-12, AC-13)
//
// The build-queue tick is a pure function of prior tick state, the loaded
// catalogue, and command inputs: it iterates the queue in insertion (order
// ID) order — never map order — and the materials draw is engine.logistics's
// own deterministic Draw. No non-test file reads the wall clock; lead time
// and seasonal slowdown are functions of the simulation month index only.
package build
