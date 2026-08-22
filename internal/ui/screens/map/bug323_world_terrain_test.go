package mapscreen

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-323's consumer-side proof: this package can DRAW engine.world's
// real terrain vocabulary.
//
// Before the fix, terrainGlyph/terrainToken only knew
// internal/engine/stub's four handcrafted Folkestone-64 band names
// ("shore"/"shelf"/"motorway"/"escarpment"), which no engine module has
// ever produced. engine.world's actual Surface vocabulary
// ("grass"/"woodland"/"water"/"shingle"/"rock", internal/engine/world's
// types.go) fell through terrainGlyph's default case to blankGlyph — so
// even a correctly registered, non-empty "f1.viewport" view would have
// rendered a screen full of spaces. Same bug, different hat.
//
// Every assertion here is on the rendered BUFFER's runes, never on
// MapScreen's internal grid: a populated grid that renders as spaces is
// the exact failure this file exists to catch.

// worldSurfaceNames is engine.world's Surface vocabulary, transcribed
// independently here (this package must never import internal/engine —
// GR#20) exactly as patch.go transcribes the wire schema.
var worldSurfaceNames = []string{"grass", "woodland", "water", "shingle", "rock"}

// bug323FullPatch builds a Full "f1.viewport" patch of the given extent
// whose cell (x, y) carries terrain(x, y).
func bug323FullPatch(t *testing.T, w, h int, terrain func(x, y int) string) json.RawMessage {
	t.Helper()
	cells := make([]wireCell, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cells = append(cells, wireCell{X: x, Y: y, Terrain: terrain(x, y), Elevation: 37})
		}
	}
	raw, err := json.Marshal(wirePatch{
		SchemaVersion: wireSchemaVersion,
		Full:          true,
		Origin:        wirePoint{X: 0, Y: 0},
		Extent:        wireExtent{Width: w, Height: h},
		Cells:         cells,
	})
	if err != nil {
		t.Fatalf("marshalling test patch: %v", err)
	}
	return raw
}

// TestBUG323_EveryWorldSurface_RendersAVisibleGlyph is the direct
// regression: each of engine.world's five surface names, on its own,
// must produce a NON-BLANK rune in the rendered buffer.
func TestBUG323_EveryWorldSurface_RendersAVisibleGlyph(t *testing.T) {
	for _, surface := range worldSurfaceNames {
		t.Run(surface, func(t *testing.T) {
			m := NewMapScreen("bug323", widgets.DefaultPalette)
			m.ApplyPatch(bug323FullPatch(t, 8, 8, func(int, int) string { return surface }))

			buf := core.NewBuffer(8, 8)
			m.Render(buf, core.Rect{X: 0, Y: 0, W: 8, H: 8})

			// Row 0 is viewport (splitRect reserves only the LAST row for
			// the minimap), and (0,0) is not the cursor cell's concern —
			// the cursor changes style, never the rune.
			got := buf.Get(0, 0).Rune
			if got == blankGlyph || got == 0 {
				t.Fatalf("terrain %q rendered as a blank rune %q — ui.screen.map does not recognise engine.world's own Surface vocabulary, so a correctly-published view still draws an empty screen (BUG-323)", surface, string(got))
			}
			if got != terrainGlyph(surface) {
				t.Fatalf("terrain %q rendered %q, want %q (terrainGlyph's own answer)", surface, string(got), string(terrainGlyph(surface)))
			}
		})
	}
}

// TestBUG323_WorldSurfaces_HaveDistinctBackgroundTokens pins AC-4's
// two-layer contract for the new vocabulary: terrain drives a background
// token, and an unrecognised terrain string must still report "no token"
// rather than silently claiming token 0 (TokenMoney) for everything.
func TestBUG323_WorldSurfaces_HaveDistinctBackgroundTokens(t *testing.T) {
	for _, surface := range worldSurfaceNames {
		if _, ok := terrainToken(surface); !ok {
			t.Errorf("terrainToken(%q) reports no token — engine.world's real terrain would render with no background colour at all", surface)
		}
	}
	if tok, ok := terrainToken("not-a-real-surface"); ok {
		t.Errorf("terrainToken(%q) = (%v, true), want (_, false) — an unrecognised terrain must not silently claim a colour", "not-a-real-surface", tok)
	}
	// water and grass must not paint the same background: the whole
	// point of the token layer is that a coastline is visible as colour
	// even where the glyphs are similar.
	water, _ := terrainToken("water")
	grass, _ := terrainToken("grass")
	if water == grass {
		t.Errorf("water and grass map to the same palette token (%v) — land and sea would be indistinguishable", water)
	}
}

// TestBUG323_MixedWorldTerrain_RendersEveryCell drives the realistic
// shape — a patch whose cells carry DIFFERENT world surfaces — and
// requires the whole viewport to come back non-blank, so a single
// unrecognised name cannot hide behind its neighbours.
func TestBUG323_MixedWorldTerrain_RendersEveryCell(t *testing.T) {
	const w, h = 10, 6
	m := NewMapScreen("bug323", widgets.DefaultPalette)
	m.ApplyPatch(bug323FullPatch(t, w, h, func(x, y int) string {
		return worldSurfaceNames[(x+y)%len(worldSurfaceNames)]
	}))

	buf := core.NewBuffer(w, h)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: w, H: h})

	// Last row is the minimap strip; the rows above it are the viewport.
	for y := 0; y < h-1; y++ {
		for x := 0; x < w; x++ {
			r := buf.Get(x, y).Rune
			if r == blankGlyph || r == 0 {
				t.Fatalf("viewport cell (%d,%d) rendered blank for terrain %q", x, y, worldSurfaceNames[(x+y)%len(worldSurfaceNames)])
			}
		}
	}
}
