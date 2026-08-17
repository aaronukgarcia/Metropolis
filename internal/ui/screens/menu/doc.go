// Package menu implements F10, the Menu & saves screen: a save/load
// browser, a data-driven settings panel, keymap and dashboard-layout
// profile management, and a new-game setup form. Its live "current
// session" summary is sourced from an int.protocol view subscription
// against the running engine; its save-slot list is sourced from
// int.serializer's bundle/header API on disk (never from internal/engine).
//
// Module key: ui.screen.menu (see code.json)
// Spec refs:  §13-F10 (docs/METROPOLIS-MASTER-v2.1.md line 257); UI-SPEC
//
//	§4 "Dashboards & the drill-through rule" (line 760: "F10 → layouts",
//	the user layout editor saved in the profile JSON);
//	docs/planning/acceptance/ui.screen.menu.md (this package's criteria,
//	MEN-1..MEN-7) and ui.screen.finance.md (the Shared F-Screen Contract,
//	SF-1..SF-10, inherited here — see ui.screen.menu.md's "Shared
//	contract" section).
//
// # View subscription (SF-2 field traceability)
//
// This screen subscribes to exactly one view (wire.go):
//
//	f10.session (engine.core) -- the running game's current-session
//	summary: world seed, simulation tick, game month, paused state and
//	speed. This is the one engine-derived figure the menu renders, as the
//	"current session" line of the save browser and the new-game form.
//
// Every displayed figure's exact source field (SF-2's binding
// requirement):
//
//	Current-session seed   <- f10.session.worldSeed
//	Current-session tick   <- f10.session.tick
//	Current-session month  <- f10.session.gameMonth
//	Current-session pause  <- f10.session.paused
//	Current-session speed  <- f10.session.speed
//	Save-slot name         <- bundle directory base name (on disk)
//	Save-slot timestamp    <- int.serializer Header.CreatedAtTick
//	Save-slot sim-date     <- int.serializer Header.GameMonth
//	Save-slot summary      <- derived from Header.WorldSeed/GameMonth/
//	                          CreatedAtTick/DebugTouched (see saves.go)
//
// # SF-1: protocol-only consumption, plus the serializer seam
//
// This package never imports internal/engine (GR#20 depguard-enforced,
// .golangci.yml's ui-must-not-import-engine rule): the live session
// figure arrives as protocol.Delta.Patch raw JSON, decoded against this
// package's own wire-schema copy (wire.go). Save-slot metadata arrives
// from internal/foundation/serialize (int.serializer, INT-002) — a
// foundation interface, not an engine module, so reading it directly is
// the same shape of "consume the registered interface, not the engine"
// the Shared F-Screen Contract's SF-1 requires. `go list -deps
// ./internal/ui/screens/menu/...` shows no internal/engine import.
//
// # The save-root enumeration seam (BUG-058 candidate, see Escalations)
//
// int.serializer reads a bundle's header.json but does not enumerate the
// save root — that layout knowledge (manual/autosave/milestone/.staging)
// is internal/engine/save's private contract, which this package must not
// duplicate (GR#20/weakness-pattern-#2). The enumeration is therefore an
// injected BundleLister (WithBundleLister); the production composition
// root wires it to internal/engine/save's own listing, while this
// package's default walks a save root for directories containing a
// header.json (skipping .staging). The header READ is this package's own
// call into serialize.ReadHeader/ValidateBundle/CheckFormatVersion, which
// is what MEN-1's "listing and loading go through int.serializer's
// bundle/header API" is built on. See saves.go and ASM-523 for the precise
// division of labour.
//
// # SF-3: the "stub cannot fake" differential check
//
// This screen's sim-derived data is the current-session summary (from the
// f10.session view) and the per-save-slot sim-date/summary (from each
// bundle's Header). saves_test.go's TestSaveEntries_SF3_OneSlotChanges
// drives two save roots differing in exactly one slot's CreatedAtTick and
// asserts (a) that slot's rendered row changes and (b) every other slot's
// rendered row is byte-identical; session_test.go's
// TestSession_SF3_OneFieldChanges does the same shape for the f10.session
// view (two patches differing in exactly one field).
//
// # SF-5/MEN drill-through, consumed not reimplemented
//
// DrillTargets (render.go) produces this screen's drill-through source
// identities — one canonical dash.DrillTarget (ViewName, EntityID) for the
// current-session figures and one per save slot — for a caller with
// ui.dash's (MOD-038) registration API to register. This package
// implements no navigation, dead-end detection, or graph storage itself
// (MOD-038's job). It consumes dash.DrillTarget directly (GR#3: no
// bespoke parallel type): the session figure's ViewName is ViewSession
// ("f10.session", whole view), and each save slot's ViewName is
// drillViewSaveSlot ("f1.viewport", whole view) — the registered F1
// view a loaded save lands on, never a fabricated scope (ASM-651:
// "serializer.bundle" was a dead end — int.serializer is a disk-format
// module, not a view publisher).
//
// # SF-6: alert-jump landing anchor
//
// This screen is not a documented landing target for any bottom-alert
// category (§13 names no alert text jumping to F10 in the reviewed
// sections), so no jump-anchor is exposed.
//
// # SF-7/MEN-6/MEN-7: error handling and "unavailable"
//
// A malformed f10.session patch is logged via MET-U601 (GR#7) and
// dropped; a Delta for an unbound (unknown/stale) SubscriptionID is
// logged via MET-U602 and dropped. Loading a corrupt/incompatible save
// surfaces int.serializer's own typed error verbatim (MEN-6) — never
// genericised. Settings/keymap/layout data with no source wired at boot
// renders "unavailable", not a blank pane (MEN-7), via the screen's
// have-settings/keymap/layout availability flags.
//
// # SF-8/SF-9: determinism and race safety
//
// Every render function in this package is a pure function of its
// arguments — no wall-clock call anywhere in this package's non-test
// source (determinism_test.go mechanically greps for it, mirroring
// ui.screen.demo's TestNoWallClockUsage). The save list is sorted by slot
// name before render, so identical inputs render identically (GR#21).
// Screen's every exported method locks mu, so ApplyDelta (delta-applying
// goroutine) and the render/accessor calls (render goroutine) may run
// concurrently; `go test ./internal/ui/screens/menu/... -race -count=1`
// is part of this package's verification (race_test.go).
package menu
