package stub

import "encoding/json"

// # The "f1.viewport" patch schema (v1)
//
// This is the de-facto v1 view schema for F1 (the map screen) — the UI
// dev builds against it concurrently with this package, so it is kept
// deliberately simple, flat, and explicitly versioned (per the AC-3/AC-6
// brief). It travels as the opaque Delta.Patch json.RawMessage
// (internal/protocol/deltas.go) — the protocol package never parses it.
//
// Wire shape (json.Marshal of ViewportPatch):
//
//	{
//	  "schemaVersion": 1,
//	  "full": true,
//	  "origin": {"x": 0, "y": 0},
//	  "extent": {"width": 64, "height": 64},
//	  "cells": [
//	    {"x": 0, "y": 0, "terrain": "shore", "elevation": 2},
//	    {"x": 5, "y": 3, "terrain": "shore", "elevation": 1, "building": "Folkestone Harbour Arm"},
//	    ...
//	  ]
//	}
//
// Field notes:
//
//   - schemaVersion: fixed at 1 for this shape. A future incompatible
//     change to the patch shape bumps this independently of
//     protocol.ProtocolVersion (deltas.go: "its schema is versioned per
//     view... not by the protocol envelope").
//   - full: true for the snapshot patch pushed as the very first Delta
//     after Subscribe is accepted (Seq 1) — Cells then covers every one
//     of the 64x64 = 4096 cells, letting a fresh subscriber render the
//     whole viewport with no prior state. false for every subsequent
//     scripted patch, where Cells lists ONLY the cells that changed —
//     the UI is expected to hold the last full state and apply patches
//     on top (T-VIEWS's "double-buffered view models" per protocol.md
//     §1).
//   - origin/extent: the viewport's requested window (SubscribePayload
//     .Params can carry "x"/"y"/"width"/"height"; the stub ignores them
//     in v1 and always serves the full 64x64 grid — origin is always
//     {0,0} and extent is always {64,64} — but the fields are wired
//     through now so a future engine narrowing the served window is not
//     a schema change).
//   - cells[].terrain/elevation: always present on every cell in a full
//     patch; omitted on a sparse patch's cells only if unchanged (v1
//     never omits them — every listed cell in this stub always carries
//     both, to keep decoding trivial; see field comments below).
//   - cells[].road/building: omitted (JSON key absent) when empty — most
//     cells have neither.
//
// This schema carries NO gameplay semantics (no traffic, no zoning) —
// Folkestone-64 is purely terrain + named roads/buildings (AC-3's
// "out of scope" note). Real view content arrives as engine.world and
// friends go real.

// Point is a flat (x, y) pair, used for ViewportPatch.Origin.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Extent is a flat (width, height) pair, used for ViewportPatch.Extent.
type Extent struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ViewportCell is one cell entry in a ViewportPatch.Cells list. See the
// package-level schema doc above for the omitempty rules.
type ViewportCell struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Terrain   string `json:"terrain,omitempty"`
	Elevation int    `json:"elevation,omitempty"`
	Road      string `json:"road,omitempty"`
	Building  string `json:"building,omitempty"`
}

// ViewportPatch is the "f1.viewport" v1 patch schema — see the doc block
// above. It is marshalled verbatim into Delta.Patch (protocol.Delta).
type ViewportPatch struct {
	SchemaVersion int            `json:"schemaVersion"`
	Full          bool           `json:"full"`
	Origin        Point          `json:"origin"`
	Extent        Extent         `json:"extent"`
	Cells         []ViewportCell `json:"cells"`
}

// viewportSchemaVersion is ViewportPatch's current schema version (see
// the package-level doc's "schemaVersion" note).
const viewportSchemaVersion = 1

// cellToViewportCell converts a fixture Cell into its wire ViewportCell
// shape.
func cellToViewportCell(c Cell) ViewportCell {
	return ViewportCell{
		X:         c.X,
		Y:         c.Y,
		Terrain:   string(c.Terrain),
		Elevation: c.Elevation,
		Road:      c.Road,
		Building:  c.Building,
	}
}

// fullViewportSnapshot builds the Full=true patch covering every cell of
// w — the first Delta pushed to a fresh "f1.viewport" subscription.
func fullViewportSnapshot(w *World) ViewportPatch {
	cells := make([]ViewportCell, 0, w.Width*w.Height)
	for y := 0; y < w.Height; y++ {
		for x := 0; x < w.Width; x++ {
			cells = append(cells, cellToViewportCell(w.Cells[y][x]))
		}
	}
	return ViewportPatch{
		SchemaVersion: viewportSchemaVersion,
		Full:          true,
		Origin:        Point{X: 0, Y: 0},
		Extent:        Extent{Width: w.Width, Height: w.Height},
		Cells:         cells,
	}
}

// scriptedViewportDeltas is the canned/recorded stream of sparse
// "f1.viewport" patches pushed after the initial full snapshot, one per
// AdvanceTicks call while the subscription is live (AC-6, AC-4). It is a
// fixed in-code slice — nothing here is computed from simulation state;
// it is handcrafted so the same sequence replays byte-identical run over
// run (AC-11).
//
// The content is illustrative, not gameplay-accurate: a small "harbour
// tide" elevation oscillation at two shoreline cells near Folkestone
// Harbour Arm. Its purpose is only to prove the sparse-patch shape (AC-6)
// and to give H-REPLAY (MOD-013, a later item) an already-shaped
// recordable/replayable stream to capture from stub runs, per this
// item's user stories.
func scriptedViewportDeltas() []ViewportPatch {
	tide := []int{2, 3, 4, 3, 2, 1}
	deltas := make([]ViewportPatch, 0, len(tide))
	for _, e := range tide {
		deltas = append(deltas, ViewportPatch{
			SchemaVersion: viewportSchemaVersion,
			Full:          false,
			Origin:        Point{X: 0, Y: 0},
			Extent:        Extent{Width: FixtureWidth, Height: FixtureHeight},
			Cells: []ViewportCell{
				{X: 5, Y: 3, Terrain: string(TerrainShore), Elevation: e, Building: "Folkestone Harbour Arm"},
				{X: 6, Y: 3, Terrain: string(TerrainShore), Elevation: e},
			},
		})
	}
	return deltas
}

// genericViewPatch is the minimal canned patch served for any Subscribe
// whose ViewName is not "f1.viewport" — v1 only ships a real fixture for
// the map viewport, but every well-formed view name must still be
// subscribable (AC-2/AC-5 apply to Subscribe generically, not just to
// f1.viewport). Its schema is intentionally not versioned/documented like
// ViewportPatch's — it is a placeholder acknowledging the subscription is
// live, not a real view.
type genericViewPatch struct {
	ViewName string `json:"viewName"`
	Note     string `json:"note"`
}

func encodePatch(v any) json.RawMessage {
	// v is always one of this package's own patch types, built here, so a
	// marshal error would be a bug in this package, not bad external
	// input — encoding/json only fails on unsupported types (channels,
	// funcs, cycles), none of which these structs contain. Falling back
	// to an empty JSON object rather than panicking keeps this a library
	// function that never panics on its own well-formed input.
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
