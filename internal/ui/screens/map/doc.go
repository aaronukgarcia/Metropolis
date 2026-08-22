// Package mapscreen is F1, the city map screen: a scrollable viewport
// plus a minimap strip, sourced entirely from an int.protocol view
// subscription against the running engine (harness.stub in Sprint 1,
// unchanged against a real engine later — AC-7).
//
// Module key: ui.screen.map (see code.json; GUID 01272dbc-234b-47a1-a645-f61f538089e9)
// Spec refs:  §13-F1 (docs/METROPOLIS-MASTER-v2.1.md line 244), UI-SPEC §2
// (visual language — heatmaps, foreground/background two-layer cells)
//
// # Directory vs. package name
//
// code.json/the master plan and this item's acceptance criteria
// (docs/planning/acceptance/ui.screen.map.md) fix this package's
// directory at internal/ui/screens/map/ — but "map" is a Go keyword, not
// a legal package-clause identifier, so the package declared in this
// directory is named mapscreen. This is a deliberate, unavoidable
// mismatch between the directory basename and the package name; every
// import site spells it as
// `mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"`
// (or lets goimports infer the same).
//
// # View subscription
//
// This screen subscribes to exactly one view: ViewSubscriptionName
// ("f1.viewport"), the "f1.viewport" v1 patch schema documented in
// internal/engine/stub/viewport.go (a full snapshot patch first, then
// sparse patches carrying only changed cells). MapScreen never imports
// internal/engine — Golden Rule #20 (Contract-First, Stub-Forever) and
// this item's AC-1: it consumes only protocol.Delta.Patch's raw JSON,
// decoded against its own package-local copy of the wire shape (patch.go).
// The internal/engine/stub package is imported by this package's tests
// only, as the sanctioned source of recorded/generated fixture JSON —
// never by non-test code.
//
// # Scope (Sprint 1 / FEAT-005 dispatch)
//
// The full ui.screen.map acceptance criteria (12 ACs) additionally
// describe an overlay cycle (ownership, land value, zoning, ... — AC-3)
// and citizen-follow (AC-6). Both require gameplay data the Folkestone-64
// fixture does not carry (it is terrain + named roads/buildings only,
// per internal/engine/stub/fixture.go's doc comment and this item's own
// "out of scope" section: "real terrain/citizens/traffic overlays
// populate once those engine modules land (Sprint 3+)"). This package
// therefore implements the walking-skeleton slice that IS representable
// against Folkestone-64 today: terrain rendering, road/building overlay
// glyphs, pan, a minimap strip, a cursor, Inspect(x, y), and the
// staleness indicator — key BINDING (o/O/f/Enter) is ui.keys' later job
// (US in the acceptance doc); this package exposes the plain Go API
// (Pan, MoveCursor, Inspect) that binding will eventually drive.
//
// # Overlay cycle (FEAT-031, AC-3/AC-4)
//
// FEAT-031 landed the overlay CYCLE MECHANISM in full: Overlay
// (overlay.go, ten constants in the exact order the BOW item's overlay
// list quotes), MapScreen.ActiveOverlay/CycleOverlay (the plain Go API
// "o"/"O" binding will eventually drive, same posture as Pan/MoveCursor
// above), and paintOverlay (overlay_data.go) — the background-only paint
// mechanism AC-4 describes, delegating per-cell to widgets.Heatmap so its
// own "never touches Rune" contract holds per cell, not just in
// aggregate. overlay_paint_internal_test.go proves that mechanism's full
// AC-4 contract (foreground invariance, cross-overlay background
// difference, per-cell differential isolation) with synthetic data.
//
// What did NOT land, and why, is exactly what a direct investigation at
// dispatch found: NONE of the ten named overlays (ownership, land value,
// zoning, utilities, traffic, pollution, decay, per-service coverage,
// parking occupancy, vitality) have a live per-cell data source reaching
// this screen today. "f1.viewport" (patch.go's wireCell: terrain,
// elevation, road, building only) is the ONLY view wired to real content
// anywhere in the codebase — confirmed against internal/protocol
// (ValidateViewName is a syntax grammar, not a registry — any view name
// validates, but internal/engine/stub/engine.go only ever scripts real
// content for "f1.viewport" itself; every other name gets an empty
// acknowledgement stub) and internal/engine/compose (compose.Wire wires
// engine.traffic and engine.services into the daily tick and the
// attract-terms pipeline respectively, but never touches
// protocol.SubscriptionServer or any Delta-publishing path at all — the
// composition root has no view-wiring code yet, for anything). Per GR#25,
// code.json's ui.screen.map module (see code.json) also registers no
// engine.traffic/engine.services/engine.parking/etc. outbound edge, so
// even a hypothetical future Delta path could not be called from this
// package without that edge landing first.
//
// overlay_data.go's overlayLiveValue therefore reports have=false for
// every overlay, and overlayBlockedReason documents each one
// individually: three (traffic, per-service coverage, parking occupancy)
// have a real, tick-wired candidate engine module blocked purely on the
// missing code.json edge + missing Delta path — tripwire_test.go makes
// the code.json half of that mechanical, mirroring
// internal/ui/screens/services/tripwire_test.go's SVC-6 pattern. The
// other seven have no engine module or code.json node at all yet — no
// stable detection point exists to tripwire (mirroring that same file's
// SVC-3 reasoning for the same package's own per-service-coverage-map
// gap), so they are documented, not fabricated.
//
// FEAT-031's BOW item also lists AC-6 (citizen follow) among the ACs it
// re-opens; this dispatch's scope was the overlay cycle only (its title
// and detailed brief) — AC-6 remains untouched by this work and is not
// claimed done here.
package mapscreen
