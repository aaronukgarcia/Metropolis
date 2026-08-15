// Package dash is the dashboard-composition layer: it composes a widget
// grid of tiles (bignum, gauge, sparkline chart, table, mini-map, alert
// list, plus embedded diagram output) into named Layouts, ships the
// default layouts, provides the user layout editor with profile-JSON
// persistence, and — its load-bearing contract — enforces the
// drill-through rule (UI-SPEC §4): every number on every dashboard is
// selectable and Enter goes to its source, with no dead ends.
//
// Module key: ui.dash (see code.json)
// Spec ref:   UI-SPEC §4 (docs/METROPOLIS-MASTER-v2.1.md lines 760-765:
//
//	"Composable dashboards…", "The drill-through rule (absolute)…",
//	"Tables…", "Projections pane idiom…"); UI-SPEC §6 (view
//	subscriptions + layout/profile JSON schema).
//
// # The drill-through contract (AC-4)
//
// A Tile cannot be constructed, bound to a value, or added to a Layout
// without a valid DrillTarget. This is enforced at the type/constructor
// level, not by a separately-run validator: the Tile's drill-target
// field is unexported, the only way to build a Tile is one of the
// New<Kind>Tile constructors (each of which takes a DrillTarget as a
// required, non-optional argument and rejects a zero/invalid one), and
// Layout.AddTile re-validates defensively on every insertion. There is
// deliberately no settable-later field and no usable zero value: a
// caller that wants to attach a source identity to a tile must supply
// one up front, or the call fails with a registry-sourced error
// (MET-U602 / MET-U603).
//
// # What a DrillTarget can name (AC-14c — read before promising granularity)
//
// A DrillTarget is a (ViewName, EntityID) pair:
//
//   - ViewName is an int.protocol view name (protocol.ValidateViewName).
//     Whole-entity drill targets — UI-SPEC §4's "opens that junction",
//     "opens that school" — are already addressable today through the
//     frozen view-name grammar's entity-scoped form (e.g.
//     "junction.14.approaches", "citizen.482913.detail"); pass such a
//     name as ViewName and leave EntityID empty.
//   - EntityID (optional, empty = "whole view") names a sub-entity or
//     row within an already-open view — a ledger line, a diagram arrow.
//     It is an opaque, engine-defined identifier, forward-compatible
//     with the protocol-side entity-addressing type FEAT-042 defines
//     (int.protocol's TargetRef/EntityID); this package does not parse
//     or validate its internal structure beyond non-emptiness.
//
// A future screen's BA must therefore promise only what DrillTarget can
// carry: entity-level (whole view or one named row/entity) granularity,
// never something finer than the protocol's addressing scheme can name.
//
// # AuditDrillCoverage is the canonical completeness check (AC-5, US-5)
//
// AuditDrillCoverage walks every element of a Layout that carries a
// displayed value — every scalar tile, every row of every table tile,
// every hit-test entry of an embedded diagram tile — and returns one Gap
// per element with no resolvable DrillTarget. It is exhaustive by
// construction, not a sample: it enumerates the closed set of drillable
// element kinds, so any future screen (F2/F4/F8, FEAT-014/016/022)
// proves its own shipped layout is drill-through-complete by calling
// this one function in its own test suite, rather than writing a bespoke
// check. New work discovered here that does not call it is a drill-through
// regression waiting to happen.
//
// # Determinism (GR#21)
//
// Layout order is a slice, never a map: tile order, AuditDrillCoverage's
// walk order, and render order are all slice iteration, deterministic by
// construction. Nothing in this package reads the wall clock on the
// render or audit path.
//
// # Files
//
//   - doc.go     — this file.
//   - drill.go   — DrillTarget (the required source identity) and its
//     validation.
//   - tile.go    — TileKind, Tile, the per-kind spec types, and the
//     validating New<Kind>Tile constructors.
//   - layout.go  — Layout (ordered tile grid), the editor operations
//     (AddTile/RemoveTile/MoveTile), profile-JSON Save/Load, and the
//     shipped default layouts.
//   - audit.go   — AuditDrillCoverage and Gap.
//   - table.go   — the table tile's sort/filter/CSV-export wiring over
//     ui.widgets' table contract, preserving per-row DrillTargets.
//   - nav.go     — Navigator/Resolver seams and Dashboard.Drill (AC-6/
//     AC-9): resolve the target against live view state, then call
//     through to navigation — never reimplementing it.
//   - render.go  — Render/RenderTile (delegating each tile type to its
//     ui.widgets function) and RenderProjection (the projections-pane
//     idiom).
package dash
