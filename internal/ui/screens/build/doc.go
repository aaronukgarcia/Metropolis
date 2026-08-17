// Package build implements F3, the Land & Construction screen: land
// purchase, the §34 eight-way zoning selector, the §13-F3 build queue
// (materials + labour + lead time), demolition with a pre-commit cost
// confirm, and the buildings.json-driven catalogue browser with
// unlock-state badges — all sourced entirely from an int.protocol view
// subscription against harness.stub (Sprint 8) and, unchanged, a real
// engine later (SF-4).
//
// Module key: ui.screen.build (see code.json; GUID
// e987c775-5de0-4197-a00e-8fe2194841db)
// Spec refs:  §13-F3 (Land & Construction, line 250); §22 Unlock Economy
// (lines 380-386); §34 Zoning (lines 480-482);
// docs/planning/acceptance/ui.screen.build.md (this package's own
// criteria, BLD-1..BLD-8) and ui.screen.finance.md (the Shared F-Screen
// Contract, SF-1..SF-10, inherited here).
//
// # View subscription (SF-2 field traceability)
//
// This screen subscribes to exactly one view: "f3.build" (wire.go), the
// aggregate feed the engine's build/unlocks modules will push. It carries
// every figure this screen renders in one patch. Every displayed figure's
// exact source field:
//
//	Land-purchase price (BLD-1)           <- f3.build.landPrice.priceMicropounds (and .cell)
//	Zone row — economics (BLD-2)          <- f3.build.zones[].id/name/materials/labour/baseLeadTimeDays
//	Build-queue row (BLD-3)               <- f3.build.queue[].id/cell/zone/materialsBillTotal/materialsDrawn/materialsRemaining/labourRemaining/leadTimeRemaining/status
//	Catalogue row + badge (BLD-5)         <- f3.build.catalogue[].id/name/section/costRaw/capacityRaw/notes/unlockState
//	Demolition cost (BLD-4)               <- f3.build.demolition.compensationMicropounds (and .cell)
//
// # SF-1: protocol-only consumption
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule) — every field above
// arrives as protocol.Delta.Patch's raw JSON, decoded against this
// package's own wire-schema copy (wire.go), never an internal/engine
// type. `go list -deps ./internal/ui/screens/build/...` shows no
// internal/engine import (test files are exempt, and this package's tests
// build fixtures from its own wire structs, so none import it anyway).
// The view name "f3.build" is this package's own choice — the spec names
// the four sub-surfaces but not the view(s) that carry them.
//
// # Commands: the typed Buy/Zone/Build/Demolish vocabulary
//
// Unlike some earlier F-screens that rode the Debug seam, this screen uses
// the protocol's real gameplay Kinds (commands.go — internal/protocol's
// KindBuy/KindZone/KindBuild/KindDemolish and their typed payloads, which
// landed in commit 613b7d0 explicitly for this screen). The actions issue:
//
//	BuyLand(cell)        -> protocol.KindBuy     BuyPayload{Cell}
//	ZonePaint(cells, z)  -> protocol.KindZone    ZonePayload{Cell, ZoneType} — one command PER cell
//	BuildOn(cell, type)  -> protocol.KindBuild   BuildPayload{Cell, BuildingType}
//	Demolish(cell)       -> protocol.KindDemolish DemolishPayload{Cell}
//
// The engine remains the authority on accept/reject (ownership, funds,
// permit, unlock prerequisite) — those reasons arrive on the
// CommandResult the caller (transport owner) receives, never on a
// payload this screen builds. This screen's own rejections (BLD-7: a
// zone/build/demolish target the view does not carry) are loud,
// registry-sourced errors, never a silently-dropped command.
//
// # BLD-1..BLD-4: the player actions
//
// BuyLand issues the §7 purchase; the price the player sees first is
// f3.build.landPrice.priceMicropounds (LandPrice accessor). ZonePaint
// zones a run of cells with one KindZone command per cell — a test
// asserts the command count matches the cell count, so a subset can never
// be silently dropped. BuildOn enqueues a catalogue building on a cell.
// Demolish is the two-step action BLD-4 requires: DemolishCost(cell)
// returns the compensation the view reports (the "show the real cost"
// half), and Demolish(send, cell) refuses with MET-V204 if the view has
// no demolition record for that cell — so the cost-showing step cannot be
// skipped (a demolish with no view-reported cost is a refusal, not a
// silent deletion). Because DemolishPayload carries only the cell (the
// protocol computes compensation on the engine side and returns it on the
// CommandResult), "the cost shown matches the value the issued command
// carries" is satisfied as "the cost shown is for exactly the cell the
// Demolish command targets" — see ASM-1448.
//
// # BLD-5: unlock state is read, never recomputed
//
// RenderCatalogue renders each building's unlock badge straight off
// f3.build.catalogue[].unlockState (locked / unlocked / in-progress /
// unavailable). This package implements no XP/DP/milestone threshold
// logic of its own (GR#3 — that is engine.unlocks' decision, carried on
// the view); it only maps the delivered string to a badge. The badge
// vocabulary is a UI choice, not spec-fixed (ASM-258).
//
// # SF-5/BLD-6: drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces the canonical dash.DrillTarget
// (ViewName, EntityID) source identities — one per build-queue order
// (its materials/labour/lead-time figures) and one per catalogue entry
// (its cost/lead-time/unlock figures) — for a caller with ui.dash's
// (MOD-038) registration API to register. This package implements no
// navigation, dead-end detection, or graph storage (MOD-038's job).
//
// # SF-6: alert-jump landing anchor
//
// This screen is a documented landing target for §13's build-related
// alerts (e.g. a build order blocked on materials). It exposes that
// anchor as the build queue's own figure identity (a BuildOrder's ID is
// the natural anchor); no bespoke alert-priority/colour logic lives here.
//
// # SF-7/BLD-8: error handling
//
// ApplyDelta decodes via decodeWirePatch (wire.go): malformed JSON, an
// unrecognised schemaVersion, or an oversized payload is logged via
// MET-V200 (GR#7) and dropped — the screen's data keeps its last-known-
// good state, never panics, never partially applies. A Delta for an
// unbound (unknown/stale) SubscriptionID is logged via MET-V201 and
// dropped. Any sub-surface absent from the latest patch renders an
// explicit "unavailable" line (Render* functions), never blank and never
// stale (BLD-8): the previous data for that sub-surface is cleared on
// apply. A catalogue entry whose unlockState is absent/unrecognised
// renders "unavailable" per-entry, distinct from the whole-sub-surface
// "unavailable".
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for the literal call).
// Screen's every exported method locks mu, so ApplyDelta (delta-applying
// goroutine) and the accessor/Render*/command calls (render/input
// goroutine) may run concurrently; `go test ./internal/ui/screens/build/
// ... -race -count=1` is part of this package's own verification.
//
// # Scope actually built vs. out of scope
//
// This dispatch builds BLD-1..BLD-8 and SF-1..SF-10. Out of scope (per
// ui.screen.build.md): the unlock economy's own progression-tree
// mechanics (engine.unlocks); materials/labour/lead-time computation
// (engine.build — this screen renders the queue, never simulates it);
// F1's map overlays for ownership/zoning (ui.screen.map, FEAT-005 —
// F3 issues zone/build commands, F1 shows their result).
package build
