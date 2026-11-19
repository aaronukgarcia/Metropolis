package mapscreen_test

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/stub"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-9995385628: a player-laid road ("laid") is drawn '=', a base-map road
// label keeps '+', and a building on the cell wins over either.
func TestRender_PlayerRoadGlyph(t *testing.T) {
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchJSON(t, stub.GenerateFolkestone64()))
	m.ApplyPatch(marshalPatch(t, stub.ViewportPatch{
		SchemaVersion: 1,
		Full:          false,
		Cells: []stub.ViewportCell{
			{X: 10, Y: 10, Terrain: "grass", Road: "laid"},
			{X: 11, Y: 10, Terrain: "grass", Road: "A-123"},
			{X: 12, Y: 10, Terrain: "grass", Road: "laid", Building: "structure 7"},
		},
	}))
	buf := core.NewBuffer(20, 20)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 20, H: 20})
	for _, tc := range []struct {
		x    int
		want rune
	}{{10, '='}, {11, '+'}, {12, '#'}} {
		if got := buf.Get(tc.x, 10).Rune; got != tc.want {
			t.Errorf("cell (%d,10) drew %q, want %q", tc.x, got, tc.want)
		}
	}
}
