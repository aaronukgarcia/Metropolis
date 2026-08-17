// Package trade implements F5, the Trade & Logistics screen: import
// contracts, the junction queue live view (the signature truck-glyph
// image), warehouse stock/buffer policy per commodity, the port panel
// (when unlocked), the balance-of-trade extension, and the pipeline-vs-
// truck safety trade view. All of it is sourced entirely from an
// int.protocol view subscription against harness.stub (Sprint 8) and,
// unchanged, a real engine later (SF-4).
//
// Module key: ui.screen.trade (see code.json)
// Spec refs:  §13-F5 (docs/METROPOLIS-MASTER-v2.1.md line 252); §33 The
//
//	Freight Harbour — Tonnes & Chains (line 464); §50 Oils, Rubber,
//	Plastics & the Chemical Network (line 658); UI-SPEC §2's
//	"queues rendered literally" idiom (the cargo-coded truck glyphs);
//	docs/planning/acceptance/ui.screen.trade.md (this package's own
//	criteria, TRD-1..TRD-8) and ui.screen.finance.md (the Shared F-Screen
//	Contract, SF-1..SF-10, inherited here).
//
// # View subscription (SF-2 field traceability)
//
// This screen subscribes to exactly one view: "f5.trade" (wire.go), the
// aggregate feed the engine's freight/logistics/unlocks modules will push.
// It carries every figure this screen renders in one patch. Every
// displayed figure's exact source field:
//
//	Import contract row (TRD-1)                <- f5.trade.contracts[].id/commodity/termMonths/monthsRemaining/pricePerUnitMicropounds/cancellationPenaltyMicropounds/status
//	Junction queue lane (TRD-2)                <- f5.trade.junctions[].approaches[].truckCount/waitSeconds/cargo
//	Warehouse stock/buffer row (TRD-3)         <- f5.trade.warehouse[].commodity/stockTonnes/capacityTonnes/bufferTonnesPerDay/flowTonnesPerDay
//	Port panel figures (TRD-4)                 <- f5.trade.port.unlocked/berths/craneRateTonnesPerHour/operatingHoursPerDay/customsThroughputTonnesPerDay/smugglingRisk
//	Balance-of-trade flows (TRD-5)             <- f5.trade.balance.imports/exports.byCommodity[].tonnesPerDay/valuePerDayMicropounds and byArtery[] (same fields)
//	Pipeline-vs-truck corridor (TRD-6)         <- f5.trade.safety.corridors[].pipelineCapacityTonnesPerDay/truckMovementsPerDay/leakRisk
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. `go list -deps ./internal/ui/screens/trade/...` shows no
// internal/engine import (test files are exempt per the depguard config,
// and this package's tests build fixtures from its own wire structs, so
// none import it anyway). The view name "f5.trade" is this package's own
// choice — the spec names the six sub-surfaces but not the view(s) that
// carry them (ASM-1192).
//
// # TRD-1: import contracts + create/cancel commands
//
// RenderContracts draws the list; CreateContract/CancelContract
// (commands.go) issue the player actions. Because int.protocol's sealed v1
// command vocabulary has no CreateContract/CancelContract/SetBuffer Kind
// and this screen may not edit internal/protocol, the actions are carried
// as protocol.DebugPayload commands with fixed Op strings
// ("trade.create-contract", "trade.cancel-contract", "trade.set-buffer")
// — the ui.screen.menu ASM-524 precedent, logged here as ASM-1193 and
// flagged to Bill (these are in-world gameplay actions riding the Debug
// seam that commands.go's extension-rule note reserves for F12).
//
// # TRD-7: cancellation penalty is surfaced, never silent
//
// CancellationPenalty(contractID) returns the penalty the view reports for
// a contract (0 while inside the penalty-free window). The caller shows it
// in a confirmation step BEFORE calling CancelContract, which carries the
// penalty in the command's Args — so a cancellation past the penalty-free
// window is never a silent rejection and never a silently-applied charge.
// A cancel/create/set-buffer against an entity absent from the view is
// refused loudly with a registry error (MET-V103/MET-V104), never silently
// dropped.
//
// # TRD-4: port unlock state is reflected, not implemented
//
// RenderPort reads port.unlocked straight off the view (engine.unlocks-
// sourced data) and renders "not yet unlocked" when false, "unavailable"
// when the port sub-surface is absent — this package implements no
// tier-gating logic of its own.
//
// # TRD-6: pipeline-vs-truck view — BLOCKED on a registry edge (BUG-058)
//
// The safety sub-surface (f5.trade.safety) is defined in the wire schema
// for forward compatibility, and RenderSafety renders the honest
// "unavailable" state when it is absent (SF-7/TRD-8). The substantive
// comparison (chemical/fuel pipeline capacity vs truck movements) cannot
// be built against a named, SF-2-traceable field until engine.chemicals/
// engine.fuel is a registered code.json outbound edge for ui.screen.trade
// (or its absence is confirmed deliberate) — flagged for Bill, not built
// against a phantom field.
//
// # SF-3: the "stub cannot fake" differential check
//
// sf3_test.go drives two wire patches differing in exactly one figure
// (e.g. one contract's price) and asserts (a) that figure's rendered row
// differs and (b) an untouched figure's rendered output is byte-identical
// between the two runs — this package's own instance of the shared SF-3
// shape, so a screen hardcoding a value or wiring the wrong field fails it.
//
// # SF-5: drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces the drill-through source identities —
// one canonical dash.DrillTarget (ViewName, EntityID) per contract,
// junction, warehouse commodity, the port, each balance flow, and each
// safety corridor — for a caller with ui.dash's (MOD-038) registration API
// to register. This package implements no navigation, dead-end detection,
// or graph storage itself (MOD-038's job). It consumes dash.DrillTarget
// directly (GR#3), with ViewName ViewSubscriptionName and EntityID the
// sub-entity path within that view.
//
// # SF-6: alert-jump landing anchor
//
// This screen is a documented landing target for §13's "Junction at 94%"
// alert category — the junction queue view. It exposes that anchor as the
// junction queue's own figure identity (a JunctionQueue's JunctionID is
// the natural anchor); no bespoke alert-priority/colour logic lives here.
//
// # SF-7/TRD-8: error handling
//
// ApplyDelta decodes via decodeWirePatch (wire.go): malformed JSON, an
// unrecognised schemaVersion, or an oversized payload is logged via
// MET-V100 (GR#7) and dropped — the screen's data keeps its last-known-
// good state, never panics, never partially applies. A Delta for an
// unbound (unknown/stale) SubscriptionID is logged via MET-V101 and
// dropped. Any sub-surface absent from the latest patch renders an explicit
// "unavailable" line (Render* functions), never blank and never stale
// (TRD-8): the previous data for that sub-surface is cleared on apply.
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call).
// Screen's every exported method locks mu, so ApplyDelta (delta-applying
// goroutine) and the accessor/Render*/command calls (render/input
// goroutine) may run concurrently; `go test ./internal/ui/screens/trade/
// ... -race -count=1` is part of this package's own verification.
//
// # Scope actually built vs. out of scope
//
// This dispatch builds TRD-1..TRD-5, TRD-7, TRD-8, and SF-1..SF-10.
// Out of scope (per ui.screen.trade.md): junction slot allocation and
// traffic-equilibrium computation (engine.traffic/engine.roads); the
// diagram engines' layout algorithm (MOD-037); port ecosystem construction
// as buildable objects (ui.screen.build). TRD-6's substantive comparison
// is BLOCKED on the BUG-058 registry edge (see above). The command kinds
// (create/cancel/set-buffer) ride the Debug seam pending real protocol
// Kinds (ASM-1193).
package trade
