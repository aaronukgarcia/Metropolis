// Package diagrams is the three auto-laid-out block-text diagram engines
// UI-SPEC §2 names at the end of the visual-language list: chain diagrams
// (production chains as boxes and arrows, §33/§50, live t/day figures on
// the arrows), network schematics (power/water/chemical grids as node-and-
// edge schematics with load colouring, plus a tube-map transit-strip
// variant), and the text Sankey for the §54 Fiscal Circuit (money flows as
// proportional block-width bands from sources through the budget to
// sinks). These are computed layouts (layered graph drawing, small n),
// cached until topology changes.
//
// Module key: ui.diagrams (see code.json)
// Spec ref:   UI-SPEC §2 (docs/METROPOLIS-MASTER-v2.1.md line 741),
//
//	§33/§50 (chain-diagram subject matter — freight/chemical production
//	chains), §54 (Fiscal Circuit, the text-Sankey subject matter),
//	§4 (dashboards and the drill-through rule — the consumer contract
//	below), §5 (the 10 Hz performance budget the cache serves).
//
// Depends on: ui.core (the shared cell-buffer), ui.widgets (the shared
// semantic palette + border glyphs). Per Golden Rule #20, this package
// receives its topology as a caller-supplied argument and never calls back
// into the engine or protocol layer to fetch data — it is a pure render
// library other UI modules call with data they already hold.
//
// # Caller contract (AC-1 + AC-5 — read this before reusing this package)
//
// Two halves, and both are load-bearing:
//
//  1. Topology in, cell-buffer layout out (AC-1). Every Render* function
//     takes the topology as an argument and draws into a caller-supplied
//     *core.Buffer. This package makes no call of its own to fetch
//     anything: the only inbound dependency is the topology struct the
//     caller passes in. A screen that embeds a diagram supplies the
//     topology it already holds; the seam to the engine is the caller's
//     job, never this package's (GR#20).
//
//  2. Source identity round-trips (AC-5, US-4). Every rendered element
//     that carries a caller-supplied numeric or identity value — a chain
//     node or edge, a network node or edge, a Sankey band — comes back in
//     the Result's Hits as a (cell region, SourceID) pair, the SourceID
//     passed through unchanged. This package does not invent, resolve,
//     renumber, or drop a SourceID. A caller that drops the Hits on the
//     floor, or that re-wires this package's constructors without
//     threading the hit-test data through, is exactly how a number inside
//     a diagram quietly becomes a drill-through dead end for ui.dash —
//     this package deliberately makes that impossible to do by accident by
//     returning the pairing from every layout call, not stashing it behind
//     an opt-in.
//
// # Determinism (GR#21, AC-8/AC-9)
//
// Every layout is a pure function of its input topology: no goroutines, no
// channels, no wall-clock reads (searching the non-test .go files for the
// standard library's clock-read call returns nothing). Every placement or
// draw order derived from caller input is sorted first — equal-rank nodes
// tie-break by ID — so the same topology produces byte-identical cells and
// an identical hit-test mapping across repeated calls and process
// restarts. The 300 ms threshold pulse, if it ever applies to a diagram
// element, is ui.widgets' shared PulseState driven by the caller's tick,
// never sampled here.
//
// # Caching (AC-6, US-5)
//
// Engine caches rendered diagrams keyed on the topology hash plus the
// buffer width and the semantic palette (each topology type has a Hash
// method; cache.go's layoutKey folds the rest in). A 10 Hz UI tick
// re-rendering an unchanged topology at the same width and palette never
// re-runs a full layered-graph-drawing pass. The cache is safe for
// concurrent use (AC-10).
//
// # Errors (GR#7)
//
// Malformed input (an edge referencing a node ID absent from the node set)
// returns a registry-sourced error (MET-U900) naming the missing node ID,
// with no partial layout returned alongside it. An out-of-range network
// grid coordinate — a magnitude beyond maxCoord, or a node span exceeding
// the render buffer — returns MET-U901 and is rejected before any traversal
// (SEC-067). Degenerate-but-valid input (zero nodes, an isolated node, an
// unbalanced Sankey) renders a documented empty/partial state, never an
// error and never a panic.
package diagrams
