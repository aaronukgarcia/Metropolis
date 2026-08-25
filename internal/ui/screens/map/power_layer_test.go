package mapscreen_test

import (
	"encoding/json"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// fullPatchWithPowerJSON builds a "f1.viewport" v1 full-snapshot patch
// carrying the given powerLines entries. stub.ViewportPatch has no
// PowerLines field (the stub engine never publishes one), so this marshals
// a local wire-shaped literal — the schema is the contract, and the shape
// mirrors ui.screen.map/patch.go's wirePatch exactly.
func fullPatchWithPowerJSON(t *testing.T, w *stubWorld, lines []map[string]any) json.RawMessage {
	t.Helper()
	cells := make([]map[string]any, 0, w.width*w.height)
	for y := 0; y < w.height; y++ {
		for x := 0; x < w.width; x++ {
			cells = append(cells, map[string]any{"x": x, "y": y, "terrain": "grass"})
		}
	}
	raw, err := json.Marshal(map[string]any{
		"schemaVersion": 1,
		"full":          true,
		"origin":        map[string]int{"x": 0, "y": 0},
		"extent":        map[string]int{"width": w.width, "height": w.height},
		"cells":         cells,
		"powerLines":    lines,
	})
	if err != nil {
		t.Fatalf("marshal patch: %v", err)
	}
	return raw
}

// stubWorld is this file's minimal terrain fixture: a uniform grass grid,
// so any non-grass foreground the renderer draws must come from the layer
// under test, not from terrain variety.
type stubWorld struct {
	width, height int
}

// glyphPowerPole mirrors the unexported render.go constant (this file
// lives in the external test package).
const glyphPowerPole = '|'

func TestPowerLayer_OffByDefault(t *testing.T) {
	w := stubWorld{width: 8, height: 8}
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchWithPowerJSON(t, &w, []map[string]any{
		{"id": 1, "class": "localPole", "fromX": 2, "fromY": 2, "toX": 5, "toY": 2, "capacityMW": 0.5},
	}))

	buf := core.NewBuffer(8, 8)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 8, H: 8})
	got := buf.Get(3, 2)
	if got.Rune == glyphPowerPole {
		t.Fatalf("cell (3,2) shows %q with the Power layer NOT selected — the layer is not default-OFF", got.Rune)
	}
	if got.Rune != '.' {
		t.Fatalf("cell (3,2) rune = %q, want the untouched grass glyph '.'", got.Rune)
	}
}

func TestPowerLayer_CycleReachesPower_AndPaintsClassColours(t *testing.T) {
	w := stubWorld{width: 16, height: 16}
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchWithPowerJSON(t, &w, []map[string]any{
		{"id": 1, "class": "localPole", "fromX": 2, "fromY": 2, "toX": 4, "toY": 2, "capacityMW": 0.5},
		{"id": 2, "class": "standardLattice", "fromX": 2, "fromY": 6, "toX": 4, "toY": 6, "capacityMW": 40},
		{"id": 3, "class": "superGrid", "fromX": 2, "fromY": 10, "toX": 4, "toY": 10, "capacityMW": 400},
	}))

	// Cycle forward to the eleventh entry (OverlayPower); the pinned-order
	// test guarantees exactly ten steps land on it.
	for i := 0; i < 10; i++ {
		if got := m.CycleOverlay(true); got == mapscreen.OverlayPower {
			break
		}
	}
	if got := m.ActiveOverlay(); got != mapscreen.OverlayPower {
		t.Fatalf("ActiveOverlay after cycling = %v, want OverlayPower", got)
	}

	rect := core.Rect{X: 0, Y: 0, W: 16, H: 16}
	buf := core.NewBuffer(16, 16)
	m.Render(buf, rect)

	cases := []struct {
		x    int
		y    int
		rune rune
		col  tcell.Color
	}{
		{3, 2, '|', widgets.DefaultPalette.Color(widgets.TokenPower)}, // localPole
		{3, 6, 'Y', tcell.NewHexColor(0xE67E22)},                      // standardLattice
		{3, 10, 'W', tcell.NewHexColor(0xC0392B)},                     // superGrid
	}
	for _, tc := range cases {
		got := buf.Get(tc.x, tc.y)
		wantStyle := tcell.StyleDefault.Foreground(tc.col)
		if got.Rune != tc.rune || got.Style != wantStyle {
			t.Errorf("span cell (%d,%d) = %+v, want rune %q style %+v", tc.x, tc.y, got, tc.rune, wantStyle)
		}
	}
}

func TestPowerLayer_InactiveAgainAfterCyclingOff(t *testing.T) {
	w := stubWorld{width: 8, height: 8}
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchWithPowerJSON(t, &w, []map[string]any{
		{"id": 1, "class": "superGrid", "fromX": 1, "fromY": 1, "toX": 6, "toY": 1, "capacityMW": 400},
	}))
	for i := 0; i < 11; i++ { // onto power, then one more wraps back to ownership
		m.CycleOverlay(true)
	}
	buf := core.NewBuffer(8, 8)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 8, H: 8})
	if got := buf.Get(3, 1); got.Rune != '.' {
		t.Fatalf("cell (3,1) rune = %q after cycling off power, want untouched terrain glyph", got.Rune)
	}
}

func TestPowerLine_UnknownClassSkipped(t *testing.T) {
	w := stubWorld{width: 8, height: 8}
	m := newTestScreen(t)
	m.ApplyPatch(fullPatchWithPowerJSON(t, &w, []map[string]any{
		{"id": 1, "class": "hvdcFrance", "fromX": 1, "fromY": 1, "toX": 6, "toY": 1, "capacityMW": 1000},
	}))
	for m.CycleOverlay(true) != mapscreen.OverlayPower {
	}
	buf := core.NewBuffer(8, 8)
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 8, H: 8})
	if got := buf.Get(3, 1); got.Rune != '.' {
		t.Fatalf("unknown-class span drawn as %q at (3,1), want skipped entirely", got.Rune)
	}
}
