package compose

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uimap "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
)

// BUG-323's engine-side proof set for the "f1.viewport" view
// (viewport_publish.go): it is REGISTERED, its Subscribe is ACCEPTED,
// and the delta it publishes carries real, non-empty engine.world
// terrain — the three things that were each individually true for some
// other view and collectively false for this one.
//
// The "publishes non-empty content" half is the load-bearing one. A view
// that registers and then publishes an empty payload every frame (which
// is exactly what "f4.services" does in an unmodified baseline-one run)
// renders identically to no view at all, so "the subscription succeeded"
// is explicitly NOT what any assertion here settles for.

// TestViewportViewSubscriptionName_MatchesUIScreenConstant guards the
// two independently-maintained copies of "f1.viewport" (this package's
// viewportViewSubscriptionName and ui.screen.map's own
// ViewSubscriptionName) against drifting apart in VALUE — the same
// GR#20/SF-1 discipline TestServicesViewSubscriptionName_MatchesUIScreenConstant
// applies to "f4.services". This test file is the only place in this
// package that imports internal/ui/screens/map; production code never
// does.
func TestViewportViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if viewportViewSubscriptionName != uimap.ViewSubscriptionName {
		t.Fatalf("viewportViewSubscriptionName = %q, want %q (ui.screen.map's own ViewSubscriptionName)", viewportViewSubscriptionName, uimap.ViewSubscriptionName)
	}
}

// TestViewportView_IsRegistered pins the registration itself, by name,
// in compose's fixed view-registration slice. Cheap, and it names the
// exact symptom in its failure message so the next person who deletes
// the entry learns what it costs.
func TestViewportView_IsRegistered(t *testing.T) {
	names := RegisteredViewNames()
	for _, n := range names {
		if n == viewportViewSubscriptionName {
			return
		}
	}
	t.Fatalf("%q is not in compose's registered view set %v — with it absent, engine.core rejects the map screen's Subscribe and F1 (the DEFAULT screen at boot) renders entirely blank (BUG-323)", viewportViewSubscriptionName, names)
}

// wireViewportTestEngine builds a real compose.Wire'd engine with the
// default (real) engine.world and a live subscription pump, mirroring
// wireServicesTestEngine's shape exactly.
func wireViewportTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, context.CancelFunc) {
	t.Helper()
	cid := errs.NewCorrelationID()

	e := core.NewEngine()
	if _, err := Wire(e, &Deps{CorrelationID: cid}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		cancel()
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	return e, transport, cancel
}

// TestViewportView_EndToEnd_DeltaCarriesRealWorldTerrain is the core
// engine-side proof: Subscribe("f1.viewport") against a REAL
// compose.Wire'd engine is accepted (it was unconditionally rejected
// before BUG-323), and the delivered patch covers the whole start tile
// with terrain strings and elevations that match what
// world.WorldAPI.CellAt independently reports for the same cells.
//
// The cross-check against CellAt is what makes this a CONTENT test
// rather than a shape test: the values have to be the world's, not a
// plausible-looking constant.
func TestViewportView_EndToEnd_DeltaCarriesRealWorldTerrain(t *testing.T) {
	_, transport, cancel := wireViewportTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uimap.ViewSubscriptionName)

	var patch viewportWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f1.viewport patch: %v", err)
	}
	if patch.SchemaVersion != viewportWireSchemaVersion {
		t.Fatalf("patch schemaVersion = %d, want %d (ui.screen.map's decodeWirePatch drops any other version outright)", patch.SchemaVersion, viewportWireSchemaVersion)
	}
	if !patch.Full {
		t.Fatal("patch.Full = false — the first patch a fresh subscriber receives must be a full snapshot, or ui.screen.map logs it as sparse-before-snapshot and drops it")
	}
	wantSide := world.TileSizeCells
	if patch.Extent.Width != wantSide || patch.Extent.Height != wantSide {
		t.Fatalf("patch extent = %dx%d, want %dx%d (the start tile's own local cell grid, matching compose's cellFromRef coordinate space)", patch.Extent.Width, patch.Extent.Height, wantSide, wantSide)
	}
	if len(patch.Cells) != wantSide*wantSide {
		t.Fatalf("patch carries %d cells, want %d — a registered view that publishes an empty or partial payload renders exactly like no view at all (BUG-323's own warning)", len(patch.Cells), wantSide*wantSide)
	}

	// Every cell must carry a terrain string — the field the renderer
	// turns into a glyph. One empty one is a blank cell on screen.
	for _, c := range patch.Cells {
		if c.Terrain == "" {
			t.Fatalf("cell (%d,%d) carries an empty terrain string — it would render as blankGlyph", c.X, c.Y)
		}
	}

	// Cross-check a sample against engine.world itself, through the same
	// public accessor the view uses, on an INDEPENDENTLY constructed
	// WorldAPI (the composed engine's own world is not reachable from
	// here). engine.world's terrain is deterministic and seedless — a
	// pure function of TileCoord and local position (synth_terrain.go's
	// hashCoord mixing) — so a second WorldAPI at the same start coord
	// must agree cell for cell. If it ever does not, that is a
	// determinism regression worth failing on in its own right.
	ref := world.NewWorldAPI(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY})
	coord := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	byXY := make(map[[2]int]viewportCell, len(patch.Cells))
	for _, c := range patch.Cells {
		byXY[[2]int{c.X, c.Y}] = c
	}
	samples := [][2]int{{0, 0}, {1, 0}, {0, 1}, {99, 42}, {wantSide - 1, wantSide - 1}}
	for _, s := range samples {
		got, ok := byXY[s]
		if !ok {
			t.Fatalf("patch has no cell at (%d,%d)", s[0], s[1])
		}
		want, err := ref.CellAt(coord, world.CellLocal{Col: s[0], Row: s[1]}, "bug323-test")
		if err != nil {
			t.Fatalf("reference CellAt(%d,%d): %v", s[0], s[1], err)
		}
		if got.Terrain != want.Surface.String() {
			t.Errorf("cell (%d,%d) terrain = %q, want %q (engine.world's own Surface.String())", s[0], s[1], got.Terrain, want.Surface.String())
		}
		if wantElev := elevationToWire(want.Elevation); got.Elevation != wantElev {
			t.Errorf("cell (%d,%d) elevation = %d, want %d (engine.world's own metres AOD, rounded to the wire schema's int)", s[0], s[1], got.Elevation, wantElev)
		}
	}

	// And the elevations must not all be a single value — a constant
	// would mean the view is publishing a placeholder rather than
	// reading the heightmap.
	seen := make(map[int]struct{})
	for _, c := range patch.Cells {
		seen[c.Elevation] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("every published cell reports the same elevation (%d distinct value) — the view is not reading engine.world's real heightmap", len(seen))
	}
}

// TestElevationToWire_RejectsNonFinite covers the one branch the
// end-to-end test cannot reach with today's terrain sources: a NaN/Inf
// elevation must collapse to 0 rather than to int(NaN)'s
// implementation-defined value.
func TestElevationToWire_RejectsNonFinite(t *testing.T) {
	nan := float32(math.NaN())
	if got := elevationToWire(nan); got != 0 {
		t.Errorf("elevationToWire(NaN) = %d, want 0", got)
	}
	if got := elevationToWire(float32(math.Inf(1))); got != 0 {
		t.Errorf("elevationToWire(+Inf) = %d, want 0", got)
	}
	if got := elevationToWire(float32(math.Inf(-1))); got != 0 {
		t.Errorf("elevationToWire(-Inf) = %d, want 0", got)
	}
	if got := elevationToWire(37.6); got != 38 {
		t.Errorf("elevationToWire(37.6) = %d, want 38 (round, not truncate)", got)
	}
	if got := elevationToWire(-2.4); got != -2 {
		t.Errorf("elevationToWire(-2.4) = %d, want -2", got)
	}
}

// TestStructureLabel covers the building-label branch, which today's
// composition never exercises (nothing composed by Wire writes a
// structureRef) but which is wired so the map lights up the instant
// something does.
func TestStructureLabel(t *testing.T) {
	if got := structureLabel(0); got != "" {
		t.Errorf("structureLabel(0) = %q, want \"\" (no structure means the wire field is omitted)", got)
	}
	if got := structureLabel(42); got != "structure 42" {
		t.Errorf("structureLabel(42) = %q, want %q", got, "structure 42")
	}
}

// TestViewportPatch_CoordsMatchCellFromRefExhaustively is the
// load-bearing coordinate-seam guard for this whole change, and it is
// exhaustive on purpose.
//
// The invariant: the cell published at wire coordinate (x, y) MUST be
// the very cell compose's own cellFromRef addresses for
// protocol.CellRef{X: x, Y: y}. That is what makes "the player clicks
// here" and "the engine mutates here" the same place. Transpose the
// publisher (emit X: row, Y: col) and the map still looks like a
// plausible map — it is just mirrored about the diagonal, so every
// build, zone and inspect command lands somewhere the player did not
// point at. Nothing else in the suite catches that.
//
// It is exhaustive rather than sampled because a SAMPLED version is
// provably inert here: the independent destructive round mutated the
// publisher to X: row, Y: col and the entire shipped suite — including
// the five-coordinate cross-check in
// TestViewportView_EndToEnd_DeltaCarriesRealWorldTerrain — stayed
// green. Today's start tile is uniform grass with only a handful of
// distinct rounded elevations, so a sample of five coordinates collides
// with its own transpose. Only walking all 40,000 cells separates them
// (the mutation reported 13,194 mismatches). Do not "optimise" this
// back to a sample.
func TestViewportPatch_CoordsMatchCellFromRefExhaustively(t *testing.T) {
	st := &simState{
		cid:   errs.NewCorrelationID(),
		world: world.NewWorldAPI(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}),
	}
	raw, err := st.buildViewportPatch()
	if err != nil {
		t.Fatalf("buildViewportPatch: %v", err)
	}
	var p viewportWirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshalling f1.viewport patch: %v", err)
	}
	if len(p.Cells) != world.CellsPerTile {
		t.Fatalf("patch carries %d cells, want %d", len(p.Cells), world.CellsPerTile)
	}

	byXY := make(map[[2]int]viewportCell, len(p.Cells))
	for _, c := range p.Cells {
		byXY[[2]int{c.X, c.Y}] = c
	}

	mismatch := 0
	firstX, firstY := -1, -1
	for y := 0; y < world.TileSizeCells; y++ {
		for x := 0; x < world.TileSizeCells; x++ {
			tile, local, err := st.cellFromRef(protocol.CellRef{X: x, Y: y})
			if err != nil {
				t.Fatalf("cellFromRef(%d,%d): %v", x, y, err)
			}
			want, err := st.world.CellAt(tile, local, st.cid)
			if err != nil {
				t.Fatalf("CellAt(%v,%v): %v", tile, local, err)
			}
			got, ok := byXY[[2]int{x, y}]
			if !ok {
				t.Fatalf("patch has no cell at (%d,%d)", x, y)
			}
			if got.Terrain != want.Surface.String() || got.Elevation != elevationToWire(want.Elevation) {
				mismatch++
				if firstX < 0 {
					firstX, firstY = x, y
				}
			}
		}
	}
	if mismatch != 0 {
		t.Fatalf("%d of %d published cells are NOT the cell cellFromRef addresses for CellRef{x,y} (first at (%d,%d)) — the map's coordinate space has desynchronised from the command seam's, so the player clicks one place and the engine changes another", mismatch, world.CellsPerTile, firstX, firstY)
	}
}
