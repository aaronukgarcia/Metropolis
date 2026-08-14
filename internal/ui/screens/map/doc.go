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
package mapscreen
