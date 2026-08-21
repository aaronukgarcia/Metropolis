package compose

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-323 (FEAT-208's documented §6 "f1.viewport" fast-follow, pulled
// forward to P0): the third real UI delta-publishing vertical slice, and
// the one that makes the game's DEFAULT screen show anything at all.
//
// Before this file, "f1.viewport" was the only screen subscription in
// cmd/metropolis with no registered view behind it: engine.core rejected
// the Subscribe (subscribe.go's registered-view table lookup), no Delta
// ever reached ui.screen.map, every cellData kept Known=false, and
// render.go's cellStyleAndRune painted blankGlyph across the entire
// display — a completely blank F1 at boot with a single staleness dot in
// the corner. The terrain store (engine.world) and the map renderer
// (ui.screen.map) both existed and both worked; only the view joining
// them was missing.
//
// This file mirrors services_publish.go/finance_publish.go's
// one-file-per-integration convention exactly and, per the FEAT-208
// design's §3.3, builds compose's OWN copy of the wire schema — the same
// JSON tags as ui.screen.map's patch.go wirePatch/wireCell, duplicated
// independently, NEVER importing internal/ui/screens/map (GR#20's
// engine-never-imports-ui half of the seam).

// viewportWireSchemaVersion mirrors ui.screen.map/patch.go's
// wireSchemaVersion constant VALUE (1), kept as a separate,
// independently maintained value per the same GR#20/SF-1 discipline
// servicesWireSchemaVersion and financeWireSchemaVersion follow. A
// mismatch is exactly what ui.screen.map's decodeWirePatch
// schemaVersion check exists to catch.
const viewportWireSchemaVersion = 1

// viewportPoint mirrors ui.screen.map/patch.go's wirePoint.
type viewportPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// viewportExtent mirrors ui.screen.map/patch.go's wireExtent.
type viewportExtent struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// viewportCell mirrors ui.screen.map/patch.go's wireCell field for
// field, including its omitempty rules.
type viewportCell struct {
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Terrain   string `json:"terrain,omitempty"`
	Elevation int    `json:"elevation,omitempty"`
	Road      string `json:"road,omitempty"`
	Building  string `json:"building,omitempty"`
}

// viewportWirePatch mirrors ui.screen.map/patch.go's wirePatch.
type viewportWirePatch struct {
	SchemaVersion int            `json:"schemaVersion"`
	Full          bool           `json:"full"`
	Origin        viewportPoint  `json:"origin"`
	Extent        viewportExtent `json:"extent"`
	Cells         []viewportCell `json:"cells"`
}

// buildViewportPatch reads engine.world's start tile — the one tile the
// rest of this composition's gameplay command seam already addresses
// (cellFromRef, compose.go: a protocol CellRef {x,y} is a LOCAL cell of
// the start tile, nothing else) — and returns the "f1.viewport" patch.
//
// WINDOW: exactly the start tile's own 200x200 local cell grid
// (world.TileSizeCells), origin {0,0}. That is deliberately the same
// coordinate space cellFromRef uses, so a cell the player inspects or
// builds on at map coordinate (x, y) is the SAME cell engine.world
// mutates for a Buy/Zone/Build command at CellRef{x, y}. Publishing a
// wider, multi-tile window would look more varied (the surrounding
// 30x30 expansion extent does contain sea tiles) but would silently
// desynchronise the map's coordinates from the command seam's, which is
// a worse bug than a uniform-looking tile.
//
// FULL EVERY TIME: Full is true on every publish rather than a full
// snapshot followed by sparse diffs. engine.core's pump (commands.go's
// StartSubscriptionPump) is coalescing and stateless per view — a
// ViewPatchFunc is handed no "what did this subscriber last see"
// context to diff against, and compose keeps no per-subscription
// history — so a truthful sparse patch is not expressible here without
// inventing that bookkeeping. A repeated full snapshot is always
// correct (ui.screen.map's applyFullLocked re-initialises the grid from
// it); its cost is documented on this item rather than hidden.
//
// CONCURRENCY (subscribe.go's ViewPatchFunc contract): this runs on the
// subscription-pump goroutine, concurrently with tick-phase writes to
// the same world. Safe here only because every read goes through
// world.WorldAPI.CellAt, which takes World.mu internally (worldapi.go) —
// never a plain simState or grid field read.
func (st *simState) buildViewportPatch() (json.RawMessage, error) {
	coord := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	const side = world.TileSizeCells

	// world.WorldAPI.TileCells, not a CellAt loop: see that method's own
	// doc comment for the measurement (241ms and 40,000 lock round-trips
	// per publish, on the pump goroutine that serves every other view
	// too).
	tileCells, err := st.world.TileCells(coord, st.cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{
			"module": "world", "accessor": "TileCells", "tile": coord,
		})
	}

	cells := make([]viewportCell, 0, side*side)
	for row := 0; row < side; row++ {
		for col := 0; col < side; col++ {
			c := tileCells[row*side+col]
			cells = append(cells, viewportCell{
				X: col,
				Y: row,
				// Surface.String() is engine.world's OWN terrain
				// vocabulary ("grass"/"woodland"/"water"/"shingle"/
				// "rock", types.go) — published verbatim, never
				// re-labelled into ui.screen.map's older Sprint-1
				// fixture vocabulary ("shore"/"shelf"/"motorway"/
				// "escarpment", which no engine module has ever
				// produced). The renderer was taught the real
				// vocabulary instead (render.go's terrainGlyph/
				// terrainToken), so what the player sees is what
				// engine.world actually holds.
				Terrain:   c.Surface.String(),
				Elevation: elevationToWire(c.Elevation),
				// Road is always omitted: no module composed by Wire
				// writes a per-cell road today (engine.roads exists on
				// this tree but is not in registrationOrder, and
				// world.Cell carries no road field at all), so there is
				// nothing real to publish. Fabricating one would be the
				// exact "looks fine, means nothing" failure this item
				// was raised against.
				Building: structureLabel(c.StructureRef),
			})
		}
	}

	patch := viewportWirePatch{
		SchemaVersion: viewportWireSchemaVersion,
		Full:          true,
		Origin:        viewportPoint{X: 0, Y: 0},
		Extent:        viewportExtent{Width: side, Height: side},
		Cells:         cells,
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of ints/strings cannot fail;
		// unreachable in practice — mirrored on
		// buildServicesCapacityDemandPatch's and
		// buildFinanceBalanceSheetPatch's identical "cannot fail"
		// branches. Per GR#1, degrade loudly rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "world", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// elevationToWire converts engine.world's float32 metres-AOD elevation
// into the wire schema's int metres (ui.screen.map's wireCell.Elevation
// is an int — the schema, not this function, is what forces the
// rounding). NaN/Inf (never produced by today's importer or synthesiser,
// but reachable if a future heightmap source is malformed) collapses to
// 0 rather than to int(NaN)'s implementation-defined garbage.
func elevationToWire(e float32) int {
	f := float64(e)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int(math.Round(f))
}

// structureLabel renders a cell's engine.world structure reference as
// the wire schema's building label, or "" when the cell holds none.
//
// The label is the opaque structure ID engine.world actually stores —
// not a human name — because no accessor anywhere maps a structureRef
// back to a display name today. Publishing the real ID keeps the map
// honest (a cell with a structure shows as one, with the identifier a
// developer can actually correlate) without inventing a name; a proper
// display name is a documented fast-follow for whichever module
// eventually owns the structure registry.
//
// Today this returns "" for every cell of an unowned start tile
// (world.Cell.StructureRef reads 0 whenever tile.sim is nil) and, in
// fact, for every cell of an owned one too: nothing composed by Wire
// writes structureRef yet. It is wired now so the map lights up the
// instant something does, rather than needing a second change then.
func structureLabel(ref uint32) string {
	if ref == 0 {
		return ""
	}
	return "structure " + strconv.FormatUint(uint64(ref), 10)
}

// viewportViewSubscriptionName mirrors internal/ui/screens/map's
// ViewSubscriptionName constant VALUE ("f1.viewport") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/map (GR#20's engine-never-imports-ui half of the
// seam). Kept as its own named constant for the same reason
// servicesViewSubscriptionName and financeViewSubscriptionName are.
const viewportViewSubscriptionName = "f1.viewport"
